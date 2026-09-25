package rag

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"narra/internal/model/entity"
	"narra/pkg/documentparser"
	"narra/pkg/embedding"
	"narra/pkg/logger"
	"narra/pkg/utils"
)

// DocumentStore 是收录链路对持久化的最小依赖面。
//
// 刻意比 repository.KnowledgeDocumentRepository 窄：收录真正用到的只有这五个方法，
// 而列表分页、切片计数、删除是查询侧的事，收录不该看得见它们 ——
// 接口窄了，测试替身也就小，写替身的时候不会被迫去实现一堆用不上的方法。
// 仓储包实现它，注入在 internal/app 完成。
type DocumentStore interface {
	// Create 插入一篇文档，落库后回填 document.ID，后续切片与向量都挂在它下面。
	Create(ctx context.Context, document *entity.KnowledgeDocument) error

	// GetByID 按主键取文档。收录成功后要用它回读一次，拿到事务里更新过的 updated_at。
	GetByID(ctx context.Context, id uint64) (*entity.KnowledgeDocument, error)

	// MarkProcessing 把文档推进到 processing。
	MarkProcessing(ctx context.Context, id uint64) error

	// MarkFailed 把文档推进到 failed，把失败现场写进 metadata，并把 reason 同步到
	// 这次上传的记录上（实现在同一个事务里改两行）。
	//
	// attempt 是这次处理的租约编号（同步链路传 0，它没有认领这一步）；返回的 applied
	// 表示失败现场是否真的落库 —— 异步链路的调用方据此决定能不能移动磁盘上的原件。
	MarkFailed(ctx context.Context, id uint64, attempt int32, metadata json.RawMessage, reason string) (applied bool, err error)

	// ReplaceChunks 用一个事务换掉这篇文档的全部切片与向量，把它标记为可检索，
	// 并把这次上传的记录置为 ready。
	ReplaceChunks(ctx context.Context, id uint64, input entity.ChunkReplacement) error
}

// UploadRecordStore 是文件收录对上传记录的最小依赖面。
//
// 只有建记录这一个方法，因为记录之后的状态变更不从这里走：置 ready 与置 failed
// 必须和文档的状态变更在同一个事务里完成，那两处由仓储在 ReplaceChunks / MarkFailed
// 内部顺带处理（见 DocumentStore 的说明）。正文收录（IngestText）没有上传这回事，
// 用不到这个接口。
type UploadRecordStore interface {
	// CreateUploadRecord 插入一条上传记录，落库后回填 record.ID。
	CreateUploadRecord(ctx context.Context, record *entity.KnowledgeUploadRecord) error
}

// ModelRegistry 是收录链路对向量模型登记的最小依赖面。
type ModelRegistry interface {
	// GetDefault 取当前默认模型。库里没有默认模型时返回 gorm.ErrRecordNotFound。
	GetDefault(ctx context.Context) (*entity.EmbeddingModel, error)

	// EnsureDefault 把一份模型登记为唯一默认模型；同名且维度一致时复用已有行。
	// 同名但维度不同、且该模型下已有向量时，实现方会拒绝并返回维度冲突错误。
	EnsureDefault(ctx context.Context, model entity.EmbeddingModel) (*entity.EmbeddingModel, error)
}

// FileInput 是从磁盘收录一份文件所需的输入。
//
// 用本模块自己的类型而不是 HTTP 请求 DTO：文件收录跑在后台 worker 里
// （见 worker.go），调用方是 worker 而不是 HTTP 处理器，不该被迫构造一个
// HTTP 请求结构。HTTP DTO 到这里的那层映射归服务层。
type FileInput struct {
	Path       string // 磁盘上的文件路径，解析器按它的后缀选实现
	Title      string // 调用方指定的标题；为空时依次回落到正文一级标题、文件名
	SourceType string // manual / import；为空时按 import 处理
	SourceURI  string // 用户看到的来源标识；为空时取 Path 的文件名部分
	SizeBytes  int64  // 原始文件的字节数，只写进上传记录供界面显示；0 表示调用方没提供
}

// TextInput 是直接收录一段正文所需的输入。
type TextInput struct {
	Title      string // 调用方指定的标题；为空时依次回落到正文一级标题、首行
	Content    string // 待收录的正文，已经是 Markdown，不需要解析
	SourceType string // manual / import；为空时按 manual 处理
	SourceURI  string
}

// IngestResult 是一次收录的结果。
//
// Document 在失败时也是非 nil 的：失败的那份文档已经写进库里（status = failed，
// 失败阶段与原因在 metadata），调用方需要它的 ID 才能说清是哪一次上传出了问题。
// Chunks 是实际写入的切片数，失败时为 0 —— 提交异步任务（SubmitFile）时也是 0，
// 因为那一刻只有一条 pending 行，切片要等 worker 处理完才有。
type IngestResult struct {
	Document *entity.KnowledgeDocument
	Chunks   int
}

// ingestMaxChunks 单篇文档的切片上限。
//
// 这道闸门是给"传错文件"准备的：把一个几百兆的日志当成知识文档传上来，
// 会变成上千次向量化调用。宁可在这里明确失败，也不要把上游额度烧完。
//
// 异步化之后它已经不受 HTTP 写超时的约束（解析与向量化跑在 worker 的 ctx 里），
// 但额度这条约束并没有消失：600 片相当于 38 个批次。要放宽应做成配置项，
// 而不是直接删掉上限。
const ingestMaxChunks = 600

// maxTitleRunes 标题长度上限，与 knowledge_documents.title 的 varchar(300) 对齐。
const maxTitleRunes = 300

// Ingester 是收录链路的门面：把一份原文变成库里可检索的切片与向量。
//
// 它做四件事：解析（可选）→ 切分 → 向量化 → 落三张表。
// 每一步失败都会把文档置为 failed，并把细粒度阶段写进 metadata（stage / error /
// error_detail），所以链路是自证的：库里任何一行的状态都能说明它走到了哪一步、
// 为什么停在那里。
//
// 文件收录是**异步**的：SubmitFile 只建 pending 行、把文件路径写进 metadata，
// 真正的处理由 Worker 在后台调 processExistingFile 完成（见 worker.go），
// 状态按 pending → processing → ready / failed 推进。异步链路是**分阶段**的：
// 解析、切分、向量化各自落库，任务行的 ingest_stage 记着失败后从哪一步恢复
// （见 processExistingFile）。正文收录（IngestText）仍同步：没有解析这一步，
// 切分与向量化是秒级的，走 ReplaceChunks 的单事务。
//
// IngestFile 是文件收录的同步版本。HTTP 面已经不再走它（controller 调的是 SubmitFile），
// 服务层接口上虽然还留着这个方法，但没有路由指向它，目前只剩测试在用。
// 它与 processExistingFile 在持久化上已经是两条路：前者一次成型（没有队列与恢复），
// 后者按阶段落库；解析那一段逻辑相同，将来可以再抽一层。
type Ingester struct {
	store     DocumentStore
	records   UploadRecordStore
	models    ModelRegistry
	embedding *embedding.Manager
	parser    documentparser.Parser

	// newEmbedder 是这个包唯一的注入点，默认按模型行 + 当前生效配置现建 Eino 适配器
	// （见 embedderFactory）。测试把它换成返回桩的工厂，整条链路就能完全离线跑完。
	newEmbedder embedderFactory
}

// FileTaskStore 是异步文件收录对任务队列的最小依赖面。
//
// 队列就是 knowledge_documents 表本身，没有独立的任务表。它比 DocumentStore 多出的
// 方法全部围绕"发任务、抢任务、收任务、按阶段恢复"：提交时写元数据，取任务时查 pending，
// 抢任务时做条件更新并领取租约编号，失败、归档与回收各一个，另有四个分阶段写入
// （解析产物 / 切片 / 向量 / 恢复用的切片读取）。之所以单独一个接口而不是并进
// DocumentStore，是因为同步的 IngestText 用不到其中任何一个。
//
// 消费方有两处：Worker（抢任务、处理、收尾）与 Ingester（SubmitFile 发任务、
// Retry 重新入队）。
type FileTaskStore interface {
	// SetMetadata 覆盖文档的 metadata，用于记下上传的暂存路径与标题回落标记。
	SetMetadata(context.Context, uint64, json.RawMessage) error

	// SetUploadPath 只替换 metadata 里的 upload_path，其余键（失败现场）原样保留。
	// 失败原件归档到 failed/<文档ID>/ 之后用它把指针挪过去。
	//
	// 只处理仍由这次失败持有的行（status = failed 且租约编号相符）；返回 false 表示
	// 用户已经重试或任务已被重新认领，指针没有改 —— 调用方要把已挪走的文件挪回原位。
	SetUploadPath(context.Context, uint64, int32, string) (bool, error)

	// FailedLeaseOwned 判断这一行是否仍由这次失败持有。归档原件之前核对：
	// 不匹配就说明已经有人重试或接手，旧 Worker 必须停止，不能再碰磁盘上的文件。
	FailedLeaseOwned(context.Context, uint64, int32) (bool, error)

	// MarkFailed 把文档推进到 failed 并合并写入失败现场，同时把 reason 同步到上传记录。
	// attempt 是租约编号；返回的 applied 为 false 表示这次失败没有落库（行已删、
	// 已被回收并重新认领、或数据库出错），调用方不得归档原件。
	// reason 的口径见 DocumentStore.MarkFailed。
	MarkFailed(context.Context, uint64, int32, json.RawMessage, string) (bool, error)

	// ListPending 按创建时间取最多 limit 条 pending 文档。
	ListPending(context.Context, int) ([]entity.KnowledgeDocument, error)

	// ClaimAndReturnAttempt 把一条 pending 文档抢成 processing，并原子递增、返回
	// 本次处理的租约编号（ingest_attempt）。claimed 为 false 表示这条已经被别的执行者抢走了。
	ClaimAndReturnAttempt(context.Context, uint64) (attempt int32, claimed bool, err error)

	// Touch 推进 processing 文档的 updated_at，作为"任务还活着"的心跳。
	// 周期 ResetStale 靠它把"真僵尸"与"跑得慢的正常任务"区分开，见 Worker.startHeartbeat。
	// 带租约编号：旧租约的僵尸心跳不该给已经重新认领的任务续命。
	Touch(context.Context, uint64, int32) error

	// ResetStale 把 updated_at 早于 olderThan 的 processing 打回 pending，
	// 用来回收上一个进程遗留的僵尸任务。
	ResetStale(context.Context, time.Time) error

	// Requeue 把一行 failed 文档改回 pending 重新排队，并把阶段改写成按现实材料算出的
	// 恢复点（stage 由调用方算好传入，见 ResolveRecoveryStage）；返回 false 表示它已经不是
	// 失败状态。重试因此不会死守原来的阶段：切片没了就退回正文，正文没了就退回原文件。
	Requeue(context.Context, uint64, string) (bool, error)

	// CountChunksByDocument 统计文档已落库的切片数，供恢复点计算判断"切片还在不在"。
	// 与查询侧共用同一个方法（服务层列表要批量统计，传多个 ID 一次查完）；
	// 只数数、不取内容：embed 阶段真正要读切片时用 ListChunksByDocument。
	CountChunksByDocument(context.Context, []uint64) (map[uint64]int64, error)

	// SaveParsedContent 保存解析产物，并把 ingest_stage 推进到 chunk。
	// 只处理仍在 processing 且租约编号相符的文档。
	SaveParsedContent(context.Context, uint64, int32, entity.ParsedContent) error

	// ReplaceStagedChunks 换掉这篇文档的全部切片，并把 ingest_stage 推进到 embed。
	ReplaceStagedChunks(context.Context, uint64, int32, []entity.KnowledgeChunk, json.RawMessage) error

	// ListChunksByDocument 按 chunk_index 升序取回全部切片，供 embed 阶段从库里恢复输入。
	ListChunksByDocument(context.Context, uint64) ([]entity.KnowledgeChunk, error)

	// SaveEmbeddingsAndMarkReady 在一个事务里写入向量并把文档置为 ready、阶段置 NULL。
	SaveEmbeddingsAndMarkReady(context.Context, uint64, int32, []entity.KnowledgeEmbedding, json.RawMessage) error
}

// NewIngester 创建收录器。
//
// parser 可以是 nil —— 表示文档解析能力没启用，此时只有 md / txt 能收录，
// 其它格式会收到一句明确的错误（见 documentparser.ParserFor）。这里不做 fail-fast，
// 是因为"解析器没装好"不该拦住纯文本导入和整个服务的启动。
//
// records 是上传记录的写入口，只在文件收录（SubmitFile）里用到。
func NewIngester(
	store DocumentStore,
	records UploadRecordStore,
	models ModelRegistry,
	embeddingManager *embedding.Manager,
	parser documentparser.Parser,
) *Ingester {
	ingester := &Ingester{
		store:     store,
		records:   records,
		models:    models,
		embedding: embeddingManager,
		parser:    parser,
	}
	ingester.newEmbedder = newModelEmbedderFactory(embeddingManager)
	return ingester
}

// SubmitFile 创建待处理文档并持久化任务路径，实际处理由 Worker 完成。
//
// 返回时文档是 pending，正文与切片都还是空的，调用方拿 id 去轮询即可。
// metadata 里给 worker 留两个键：upload_path 是磁盘上的暂存文件，
// explicit_title 记录调用方有没有指定过标题 —— 后者决定 worker 要不要
// 用正文的一级标题替换掉这个暂时代替标题的文件名。
func (i *Ingester) SubmitFile(ctx context.Context, input FileInput) (IngestResult, error) {
	path := strings.TrimSpace(input.Path)
	if path == "" {
		return IngestResult{}, fmt.Errorf("待收录的文件路径不能为空")
	}
	store, ok := i.store.(FileTaskStore)
	if !ok {
		return IngestResult{}, fmt.Errorf("知识库存储不支持异步文件任务")
	}
	sourceType, err := normalizeSourceType(input.SourceType, entity.KnowledgeDocumentSourceImport)
	if err != nil {
		return IngestResult{}, err
	}
	sourceURI := strings.TrimSpace(input.SourceURI)
	if sourceURI == "" {
		sourceURI = filepath.Base(path)
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = sourceURI
	}
	document, err := i.createDocument(ctx, title, sourceType, sourceURI)
	if err != nil {
		return IngestResult{}, err
	}
	metadata := map[string]any{
		"upload_path":    path,
		"explicit_title": strings.TrimSpace(input.Title) != "",
	}
	payload, _ := json.Marshal(metadata)
	if err := store.SetMetadata(ctx, document.ID, payload); err != nil {
		return IngestResult{}, err
	}

	// 建这条投递的历史记录。它的状态往后由 Worker 链路的 MarkFailed /
	// SaveEmbeddingsAndMarkReady 顺带推进（同步链路是 MarkFailed / ReplaceChunks），
	// 这里只负责在起点写一条 pending。
	//
	// 建失败**不阻断这次收录**：记录只是历史，缺一条不影响文档本身能不能入库。
	// 反过来若在这里返回错误，用户会看到"上传失败"，而文档行其实已经建好、
	// worker 也照样会把它收录成功 —— 一个"报错但其实成功了"的假象更难解释。
	record := &entity.KnowledgeUploadRecord{
		DocumentID:   &document.ID,
		OriginalName: truncateTitle(sourceURI),
		SizeBytes:    input.SizeBytes,
		Status:       entity.KnowledgeUploadRecordStatusPending,
	}
	if err := i.records.CreateUploadRecord(ctx, record); err != nil {
		logger.Error("创建上传记录失败，这份文件将没有投递历史",
			zap.Uint64("document_id", document.ID),
			zap.String("original_name", sourceURI),
			zap.Error(err),
		)
	}
	return IngestResult{Document: document}, nil
}

// Retry 把一条收录失败的文档重新入队，由 Worker 再跑一遍。
//
// stage 是调用方（服务层）按现实材料算好的恢复点，见 ResolveRecoveryStage：
// 有切片重向量化、没切片有正文重分块、都没了才重新解析原文件。
// 它只改状态与阶段、不碰文件：原件在失败时就被 worker 归档到了 failed/<文档ID>/，
// metadata 里的 upload_path 指着那里（见 worker.archiveStagedFile），
// 下一轮轮询自然会照常把这一行捡起来。
//
// 返回 false 表示这一行不满足重试条件 —— 不存在，或状态已经不是 failed
// （另一个请求抢先重试了，或它已经被删掉）。这不是错误，调用方按"不需要重试"处理。
//
// 它是**原地重试**：复用同一行文档与同一条上传记录，不新建任何东西。
// 一份文件一份资产，重试只是让它再跑一次，投递历史里不该凭空多出一条。
func (i *Ingester) Retry(ctx context.Context, id uint64, stage string) (bool, error) {
	store, ok := i.store.(FileTaskStore)
	if !ok {
		return false, fmt.Errorf("知识库存储不支持异步文件任务")
	}
	return store.Requeue(ctx, id, stage)
}

// IngestFile 读一份文件并收录。
//
// 顺序是"先建文档行、再解析"：文档行是整条链路的主线，状态机挂在它上面。
// 反过来先解析再建行的话，解析失败就没有任何记录可查 —— 用户只看到一句报错，
// 不知道失败的是哪次上传。
func (i *Ingester) IngestFile(ctx context.Context, input FileInput) (IngestResult, error) {
	path := strings.TrimSpace(input.Path)
	if path == "" {
		return IngestResult{}, fmt.Errorf("待收录的文件路径不能为空")
	}

	sourceType, err := normalizeSourceType(input.SourceType, entity.KnowledgeDocumentSourceImport)
	if err != nil {
		return IngestResult{}, err
	}

	// SourceURI 存用户看到的原始文件名，不存服务器上的临时路径：
	// 后者对用户没有意义，还会把部署目录结构带进数据库。
	sourceURI := strings.TrimSpace(input.SourceURI)
	if sourceURI == "" {
		sourceURI = filepath.Base(path)
	}

	explicitTitle := strings.TrimSpace(input.Title) != ""
	title := strings.TrimSpace(input.Title)
	if title == "" {
		// 回落到 SourceURI，而不是回落到入参里的路径：对 HTTP 上传来说，那个路径是
		// 服务端自己起的临时文件名（controller 会规范成 upload.md），写进库里
		// 用户根本认不出是自己传的哪一份；SourceURI 才是他看到的那个名字。
		title = sourceURI
	}

	document, err := i.createDocument(ctx, title, sourceType, sourceURI)
	if err != nil {
		return IngestResult{}, err
	}

	// 同步链路没有 Worker 认领这一步，状态要自己在这里推进：失败现场只认
	// processing 的行，停在 pending 会让解析失败的原因根本写不进去。
	if err := i.store.MarkProcessing(ctx, document.ID); err != nil {
		return IngestResult{Document: document}, fmt.Errorf("更新文档状态失败: %w", err)
	}

	parser, err := documentparser.ParserFor(path, i.parser)
	if err != nil {
		return i.failIngest(ctx, document, 0, "select_parser", err)
	}

	started := time.Now().UTC()
	result, err := parser.Parse(ctx, documentparser.Request{Path: path})
	if err != nil {
		return i.failIngest(ctx, document, 0, "parse", err)
	}
	// 解析产物目录（导出的图片）归调用方清理。
	// 已知短板：图片外链尚未接入对象存储，所以 Markdown 里指向本地图片的路径
	// 在这一步之后会失效。等对象存储落地，改成"先上传图片、回填 URL、再删目录"。
	defer func() { _ = result.Cleanup() }()

	// 有页 OCR 失败时正文不完整，宁可整篇失败也不静默入库（Cleanup 已经挂上，临时目录照删）。
	if err := ocrCoverageError(result); err != nil {
		return i.failIngest(ctx, document, 0, "parse", err)
	}

	// 调用方没指定标题时，用正文的首个一级标题代替文件名：
	// 文件名常带版本号和日期（"架构说明_2026-09-17_v3.md"），
	// 而一级标题是作者给这篇文档起的正式名字，在列表页里可读得多。
	if !explicitTitle {
		title = preferHeadingTitle(title, result.Markdown)
	}

	metadata := map[string]any{
		"parser":   parserName(result),
		"parse_ms": time.Since(started).Milliseconds(),
	}
	return i.ingestMarkdown(ctx, document, title, result.Markdown, metadata)
}

// processExistingFile 处理一条已经建好行的文件收录任务，由 Worker 调用。
//
// stage 是 Worker 按现实材料算好的恢复点（ResolveRecoveryStage），不是行上的 ingest_stage：
// parse 读原文件解析，chunk 从已落库的正文切分，embed 从已落库的切片生成向量。
// 每步成功都把中间结果与阶段一起落库，所以进程崩溃、向量服务抖动都不会让昂贵的解析
// 白跑一遍 —— 这正是分阶段收录的意义。
//
// 与 IngestFile 的差别有三处：文档行是现成的（不再新建，状态也已经由 worker 抢任务时
// 置为 processing）；标题回落的判据来自任务元数据 —— 调用方当初没指定标题时，
// worker 会传空标题进来，这里才走到"用正文一级标题替换"；以及 attempt 这个租约编号。
//
// 失败时和别处一样把文档置为 failed（停在失败的那一步），而不是让它停在 processing：
// 停在 processing 的行此后没有任何执行者会再碰它，只能等下一次进程启动时
// 被 ResetStale 打回 pending 再跑一遍 —— 等于同一份坏文件被反复解析。
func (i *Ingester) processExistingFile(
	ctx context.Context,
	document *entity.KnowledgeDocument,
	input FileInput,
	attempt int32,
	stage string,
) (IngestResult, error) {
	store, ok := i.store.(FileTaskStore)
	if !ok {
		return i.failIngest(ctx, document, attempt, "store", fmt.Errorf("知识库存储不支持异步文件任务"))
	}

	switch stage {
	case entity.KnowledgeDocumentStageParse, entity.KnowledgeDocumentStageChunk, entity.KnowledgeDocumentStageEmbed:
	default:
		// 未知取值不能静默按 parse 处理：那会把一份本可以恢复的文档重头解析一遍，
		// 而真正的问题（比如将来加了新阶段、旧版本进程还在跑）被掩盖掉。
		return i.failIngest(ctx, document, attempt, "worker",
			fmt.Errorf("收录阶段 %q 无法识别，拒绝继续处理", stage))
	}

	// 两个变量在 parse 之后被解析结果顶替，在 chunk / embed 阶段则直接来自文档行 ——
	// 中间结果落库的收益就体现在这里。
	markdown := document.Content
	title := document.Title

	if stage == entity.KnowledgeDocumentStageParse {
		parser, err := documentparser.ParserFor(input.Path, i.parser)
		if err != nil {
			return i.failIngest(ctx, document, attempt, "select_parser", err)
		}
		started := time.Now().UTC()
		result, err := parser.Parse(ctx, documentparser.Request{Path: input.Path})
		if err != nil {
			return i.failIngest(ctx, document, attempt, "parse", err)
		}
		defer func() { _ = result.Cleanup() }()

		// 与 IngestFile 同一条守卫：缺页的文档不能标 ready。
		if err := ocrCoverageError(result); err != nil {
			return i.failIngest(ctx, document, attempt, "parse", err)
		}
		markdown = result.Markdown
		if strings.TrimSpace(input.Title) == "" {
			title = preferHeadingTitle(title, markdown)
		}

		metadata := marshalMetadata(map[string]any{
			"parser":   parserName(result),
			"parse_ms": time.Since(started).Milliseconds(),
		})
		if err := store.SaveParsedContent(ctx, document.ID, attempt, entity.ParsedContent{
			Title:    truncateTitle(title),
			Content:  markdown,
			Checksum: checksum(markdown),
			Metadata: metadata,
		}); err != nil {
			return i.failIngest(ctx, document, attempt, "store", fmt.Errorf("保存解析正文失败: %w", err))
		}
		stage = entity.KnowledgeDocumentStageChunk
	}

	if stage == entity.KnowledgeDocumentStageChunk {
		if strings.TrimSpace(markdown) == "" {
			return i.failIngest(ctx, document, attempt, "chunk",
				fmt.Errorf("%w：这篇文档没有已落库的正文，无法从切分阶段恢复；请重新上传", ErrEmptyContent))
		}

		chunkStarted := time.Now().UTC()
		chunks, err := chunkMarkdown(ctx, markdown)
		if err != nil {
			return i.failIngest(ctx, document, attempt, "chunk", err)
		}
		metadata := marshalMetadata(map[string]any{
			"chunks":   len(chunks),
			"chunk_ms": time.Since(chunkStarted).Milliseconds(),
		})
		if err := store.ReplaceStagedChunks(ctx, document.ID, attempt, buildStoredChunks(document.ID, chunks), metadata); err != nil {
			return i.failIngest(ctx, document, attempt, "store", fmt.Errorf("保存切片失败: %w", err))
		}
		stage = entity.KnowledgeDocumentStageEmbed
	}

	if stage == entity.KnowledgeDocumentStageEmbed {
		stored, err := store.ListChunksByDocument(ctx, document.ID)
		if err != nil {
			return i.failIngest(ctx, document, attempt, "store", fmt.Errorf("读取已落库的切片失败: %w", err))
		}
		if len(stored) == 0 {
			return i.failIngest(ctx, document, attempt, "embed",
				fmt.Errorf("这篇文档没有已落库的切片，无法从向量阶段恢复；请重新上传"))
		}

		model, err := i.resolveModel(ctx)
		if err != nil {
			return i.failIngest(ctx, document, attempt, "model", err)
		}
		embedder, err := i.newEmbedder(model)
		if err != nil {
			return i.failIngest(ctx, document, attempt, "model", err)
		}

		embedStarted := time.Now().UTC()
		vectors, err := embedInBatches(ctx, embedder, chunksFromEntities(stored))
		if err != nil {
			return i.failIngest(ctx, document, attempt, "embed", err)
		}
		embeddings, err := buildStoredEmbeddings(stored, vectors, model)
		if err != nil {
			return i.failIngest(ctx, document, attempt, "vector", err)
		}

		metadata := marshalMetadata(map[string]any{
			"model":    model.Name,
			"model_id": model.ID,
			"embed_ms": time.Since(embedStarted).Milliseconds(),
		})
		if err := store.SaveEmbeddingsAndMarkReady(ctx, document.ID, attempt, embeddings, metadata); err != nil {
			return i.failIngest(ctx, document, attempt, "store", fmt.Errorf("保存向量失败: %w", err))
		}

		// 回读一次再返回：内存里这份是 worker 取任务时读到的，中间的阶段推进与正文替换
		// 都没有回写到它身上。
		if refreshed, err := i.store.GetByID(ctx, document.ID); err == nil {
			return IngestResult{Document: refreshed, Chunks: len(stored)}, nil
		}
		document.Status = entity.KnowledgeDocumentStatusReady
		document.IngestStage = nil
		return IngestResult{Document: document, Chunks: len(stored)}, nil
	}

	// 到不了这里：三个阶段各自都会在成功或失败时返回。
	return IngestResult{Document: document}, nil
}

// IngestText 直接把正文收录为 Markdown。正文已经是目标格式，没有解析这一步。
func (i *Ingester) IngestText(ctx context.Context, input TextInput) (IngestResult, error) {
	content := strings.TrimSpace(input.Content)
	if content == "" {
		return IngestResult{}, fmt.Errorf("%w: 正文不能为空", ErrEmptyContent)
	}

	sourceType, err := normalizeSourceType(input.SourceType, entity.KnowledgeDocumentSourceManual)
	if err != nil {
		return IngestResult{}, err
	}

	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = preferHeadingTitle(firstLineTitle(content), content)
	}

	document, err := i.createDocument(ctx, title, sourceType, strings.TrimSpace(input.SourceURI))
	if err != nil {
		return IngestResult{}, err
	}
	return i.ingestMarkdown(ctx, document, title, content, map[string]any{"parser": "direct"})
}

// ingestMarkdown 是同步链路的公共后半段：切分 → 向量化 → 一次事务落三张表。
// 文件收录（IngestFile）与正文收录（IngestText）在这里合流，之后的处理完全一样。
//
// 它不参与分阶段恢复：正文收录没有原文件可重试，同步文件收录也没有任务队列；
// 两者都要"要么全成、要么全不成"，所以走 ReplaceChunks 的单事务。
// 异步文件链路由 processExistingFile 按 ingest_stage 分阶段推进。
func (i *Ingester) ingestMarkdown(
	ctx context.Context,
	document *entity.KnowledgeDocument,
	title string,
	markdown string,
	metadata map[string]any,
) (IngestResult, error) {
	if strings.TrimSpace(markdown) == "" {
		return i.failIngest(ctx, document, 0, "parse", fmt.Errorf("%w: 没有可入库的正文", ErrEmptyContent))
	}
	if err := i.store.MarkProcessing(ctx, document.ID); err != nil {
		return i.failIngest(ctx, document, 0, "status", fmt.Errorf("更新文档状态失败: %w", err))
	}

	chunkStarted := time.Now().UTC()
	chunks, err := chunkMarkdown(ctx, markdown)
	if err != nil {
		return i.failIngest(ctx, document, 0, "chunk", err)
	}
	metadata["chunks"] = len(chunks)
	metadata["chunk_ms"] = time.Since(chunkStarted).Milliseconds()

	// 模型必须在向量化之前定下来：向量的 model_id 指向它，维度校验也以它为准。
	model, err := i.resolveModel(ctx)
	if err != nil {
		return i.failIngest(ctx, document, 0, "model", err)
	}
	metadata["model"] = model.Name
	metadata["model_id"] = model.ID

	embedder, err := i.newEmbedder(model)
	if err != nil {
		return i.failIngest(ctx, document, 0, "model", err)
	}

	embedStarted := time.Now().UTC()
	vectors, err := embedInBatches(ctx, embedder, chunks)
	if err != nil {
		return i.failIngest(ctx, document, 0, "embed", err)
	}
	metadata["embed_ms"] = time.Since(embedStarted).Milliseconds()

	replacement, err := buildReplacement(document.ID, title, markdown, metadata, chunks, vectors, model)
	if err != nil {
		return i.failIngest(ctx, document, 0, "vector", err)
	}

	if err := i.store.ReplaceChunks(ctx, document.ID, replacement); err != nil {
		return i.failIngest(ctx, document, 0, "store", err)
	}

	// 回读一次再返回：内存里这份是创建文档时读到的，中间的状态推进和正文替换
	// 都没有回写到它身上。直接拿它出响应会给出一个"刚创建就再没更新过"的
	// updated_at，与实际入库时间对不上，而前端很可能拿这个字段做排序。
	if refreshed, err := i.store.GetByID(ctx, document.ID); err == nil {
		return IngestResult{Document: refreshed, Chunks: len(chunks)}, nil
	}

	// 回读失败不该让已经成功的收录变成失败，退回内存里那份并补上已知变化。
	document.Title = title
	document.Content = markdown
	document.Status = entity.KnowledgeDocumentStatusReady
	return IngestResult{Document: document, Chunks: len(chunks)}, nil
}

// resolveModel 确定新向量该挂在哪个模型下。
func (i *Ingester) resolveModel(ctx context.Context) (*entity.EmbeddingModel, error) {
	cfg := i.embedding.Config()
	if !cfg.Enabled {
		return nil, fmt.Errorf("%w：请先在设置页配置向量服务", ErrEmbeddingDisabled)
	}

	model, err := i.models.GetDefault(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w：请先在设置页保存一次向量服务配置", ErrNoEmbeddingModel)
		}
		return nil, fmt.Errorf("查询默认向量模型失败: %w", err)
	}
	if model.Name == cfg.Model && int(model.Dimensions) == cfg.Dimensions {
		return model, nil
	}

	// 模型行与当前生效配置对不上。这时继续写会很隐蔽：向量挂在旧模型名下，
	// 而检索按配置里的模型去查，一条也取不到。
	// 以配置为准重新登记一次，让维度冲突在这里变成明确错误，而不是变成一份查不到的向量。
	aligned, err := i.models.EnsureDefault(ctx, entity.EmbeddingModel{
		Name:       cfg.Model,
		Provider:   entity.EmbeddingProviderOpenAICompatible,
		BaseURL:    utils.OptionalString(cfg.BaseURL),
		Dimensions: int32(cfg.Dimensions),
	})
	if err != nil {
		return nil, fmt.Errorf("向量模型登记与当前配置不一致，重新对齐失败: %w", err)
	}
	return aligned, nil
}

// createDocument 建文档行，此时正文还是空的，状态是 pending。
func (i *Ingester) createDocument(ctx context.Context, title, sourceType, sourceURI string) (*entity.KnowledgeDocument, error) {
	document := &entity.KnowledgeDocument{
		Title:      truncateTitle(title),
		SourceType: sourceType,
		Enabled:    true,
		Status:     entity.KnowledgeDocumentStatusPending,
		// metadata 是 NOT NULL 的 jsonb，必须写 '{}' 而不是留空：
		// 留空在 GORM 里会变成 NULL，被列约束直接拒掉。
		Metadata: json.RawMessage(`{}`),
	}
	if sourceURI != "" {
		document.SourceURI = &sourceURI
	}

	if err := i.store.Create(ctx, document); err != nil {
		return nil, fmt.Errorf("创建知识文档失败: %w", err)
	}
	return document, nil
}

// failIngest 记录失败现场，然后把原始错误交回调用方，同时带上那份已经置为 failed 的文档。
//
// 状态必须落 failed：文档行是解析之前就建好的，失败时如果不管它，
// 库里会永远留着一篇 status = pending 的文档，看起来像"还在处理"，
// 实际上那次上传早就结束了。
//
// 失败原因分两个键写，因为它们的读者不是同一个人：
//   - error：给界面看的一句话，纯中文（见 userFacingReason）
//   - error_detail：给排障看的完整诊断，含错误码与 stderr 原文
//
// 挤在一个键里只能二选一：给用户看就会被机器串污染，给排障看用户又读不懂。
// 同步给上传记录的 error_message 是前者 —— 抽屉里显示的就是它。
func (i *Ingester) failIngest(
	ctx context.Context,
	document *entity.KnowledgeDocument,
	attempt int32,
	stage string,
	cause error,
) (IngestResult, error) {
	// 取消不是失败，也不该留下 failed。两层原因：
	//  1) ctx 已取消时 MarkFailed 多半也写不进去（事务的 BeginTx 直接返回 ctx.Err），
	//     文档会停在 processing —— 记一条"已失败"只会误导排障；
	//  2) 即使写得进去，把一次关服记成文档失败也是错的，用户会以为文件有问题。
	// 直接返回，让 worker 的周期 ResetStale 重新入队。
	if errors.Is(cause, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		logger.Info("收录被取消，不写终态，等待周期回收重新入队",
			zap.Uint64("document_id", document.ID),
			zap.String("stage", stage),
		)
		return IngestResult{Document: document}, cause
	}

	reason := userFacingReason(cause)
	fields := map[string]any{
		"stage":        stage,
		"error":        reason,
		"error_detail": cause.Error(),
		"failed_at":    time.Now().UTC().Format(time.RFC3339),
	}
	if code := parserErrorCode(cause); code != "" {
		fields["error_code"] = code
	}

	payload, err := json.Marshal(fields)
	if err != nil {
		payload = json.RawMessage(`{"error":"记录失败原因时出错"}`)
	}

	// 写失败现场用独立预算的 ctx：真正的失败可能正好撞上关服（ctx 被取消），
	// 那时用任务 ctx 会把这次失败现场一起丢掉。5 秒足够一次 UPDATE。
	writeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	applied, markErr := i.store.MarkFailed(writeCtx, document.ID, attempt, payload, reason)
	if markErr != nil {
		// 这一步失败不影响给用户的答复，但会让文档停在中间状态，必须留下日志。
		logger.Error("知识文档标记失败状态时出错，文档可能停在中间状态",
			zap.Uint64("document_id", document.ID),
			zap.Int32("ingest_attempt", attempt),
			zap.String("stage", stage),
			zap.Error(markErr),
		)
	} else if !applied {
		// 行已被删除、已被回收并重新认领：这次失败不属于当前那一轮。
		// Worker 据此不归档原件（见 failureRecorded）。
		logger.Warn("失败现场未写入：文档已不在处理中或租约已失效",
			zap.Uint64("document_id", document.ID),
			zap.Int32("ingest_attempt", attempt),
			zap.String("stage", stage),
		)
	}

	failed := *document
	failed.Status = entity.KnowledgeDocumentStatusFailed
	failed.Metadata = payload
	return IngestResult{Document: &failed}, &ingestFailure{cause: cause, applied: applied}
}

// ingestFailure 把"失败现场有没有落库"随错误一起带给 Worker。
//
// 失败原因本身仍然原样可读（Error 与 Unwrap 都转发给 cause），所以日志与
// errors.Is/As 的用法不变；多出来的 applied 只服务一个判断：能不能移动磁盘上的原件。
type ingestFailure struct {
	cause   error
	applied bool
}

func (f *ingestFailure) Error() string { return f.cause.Error() }

func (f *ingestFailure) Unwrap() error { return f.cause }

// failureRecorded 判断一次失败是否已经写进库里。
//
// 只有写进去了，Worker 才能归档原件。旧租约的迟到失败（行已被重新认领）、
// 已删除的文档、以及写库本身失败这三种情况都不能移动文件：
// 前两种会让新一轮任务失去输入，第三种会把文件挪到一个没人认领的地方。
func failureRecorded(err error) bool {
	var failure *ingestFailure
	return errors.As(err, &failure) && failure.applied
}

// userFacingReason 把一次失败折成一句给用户看的中文。
//
// 不能直接用 cause.Error()：那是日志格式的完整诊断，错误码挂在前面、stderr 拖在后面，
// 落到界面上就是 "PARSER_FAILED: 文档解析失败 (stderr: parser failed: No module named
// 'scipy')" 这样一段中英混杂的机器串 —— 用户读不懂错误码，也判断不出该做什么。
// 解析器的错误自带一句中文（见 documentparser.Error.UserMessage），取它即可。
//
// 别的错误一律原样用：向量化、切分、落库这几条路本来就是 Go 侧直接写的中文
// （"第 1~16 个切片向量化失败: 上游返回 429"），没有需要剥掉的包装。
func userFacingReason(cause error) string {
	var parseErr *documentparser.Error
	if errors.As(cause, &parseErr) {
		if message := parseErr.UserMessage(); message != "" {
			return message
		}
	}
	return cause.Error()
}

// parserErrorCode 取稳定错误码，供排障按码检索。
//
// 只有解析器的错误有码，别处返回空串 —— 空串不进 metadata，免得那个键时有时无。
func parserErrorCode(cause error) string {
	var parseErr *documentparser.Error
	if errors.As(cause, &parseErr) {
		return parseErr.Code
	}
	return ""
}

// buildReplacement 组装入库所需的三份数据。
func buildReplacement(
	documentID uint64,
	title string,
	markdown string,
	metadata map[string]any,
	chunks []Chunk,
	vectors [][]float64,
	model *entity.EmbeddingModel,
) (entity.ChunkReplacement, error) {
	if len(chunks) != len(vectors) {
		return entity.ChunkReplacement{}, fmt.Errorf("切片与向量数量不一致: %d / %d", len(chunks), len(vectors))
	}

	rawMetadata, err := json.Marshal(metadata)
	if err != nil {
		rawMetadata = json.RawMessage(`{}`)
	}

	replacement := entity.ChunkReplacement{
		Title:      truncateTitle(title),
		Content:    markdown,
		Checksum:   checksum(markdown),
		Metadata:   rawMetadata,
		Chunks:     buildStoredChunks(documentID, chunks),
		Embeddings: make([]entity.KnowledgeEmbedding, len(chunks)),
	}

	// 同一批向量的生成时间取同一个时刻：它们本来就是同一次向量化调用的产物，
	// 逐条取 time.Now() 只会让这一列出现毫无意义的毫秒差。
	generatedAt := time.Now().UTC()

	for index := range chunks {
		literal, err := vectorLiteral(vectors[index])
		if err != nil {
			return entity.ChunkReplacement{}, fmt.Errorf("第 %d 个切片的向量无效: %w", index+1, err)
		}

		replacement.Embeddings[index] = entity.KnowledgeEmbedding{
			ModelID: model.ID,
			// 维度用向量的真实长度，而不是模型登记的维度：模型行的维度可能是刚被改过的，
			// 两者一旦不一致，check 约束 vector_dims(embedding) = dimensions 会当场拒绝，
			// 报出来的是一个看不出根因的约束错误。
			Dimensions:  int32(len(vectors[index])),
			Embedding:   literal,
			GeneratedAt: generatedAt,
		}
	}
	return replacement, nil
}

// marshalMetadata 把处理产物折成 JSON。这里的值都是字符串与整数，编码失败
// 只可能是内存问题，给一份空对象让流程继续 —— 与 buildReplacement 同一种兜底。
func marshalMetadata(metadata map[string]any) json.RawMessage {
	payload, err := json.Marshal(metadata)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return payload
}

// chunkMarkdown 把正文切成切片，并做两道校验：切不出任何切片、超过单篇上限。
// 同步链路与异步文件链路共用它，保证两条路对"什么算合法切片"的判断一致。
func chunkMarkdown(ctx context.Context, markdown string) ([]Chunk, error) {
	chunks, err := splitMarkdown(ctx, markdown, ChunkOptions{})
	if err != nil {
		return nil, err
	}
	if len(chunks) == 0 {
		return nil, fmt.Errorf("%w: 切分没有产出任何切片，正文可能只有空白字符", ErrEmptyContent)
	}
	if len(chunks) > ingestMaxChunks {
		return nil, fmt.Errorf(
			"%w：文档切出 %d 个切片，超过单篇上限 %d；请拆成多篇后再导入",
			ErrTooManyChunks, len(chunks), ingestMaxChunks)
	}
	return chunks, nil
}

// buildStoredChunks 把切分产物折成待落库的切片行，供同步链路的 ReplaceChunks
// 与异步链路的 ReplaceStagedChunks 共用。
func buildStoredChunks(documentID uint64, chunks []Chunk) []entity.KnowledgeChunk {
	stored := make([]entity.KnowledgeChunk, len(chunks))
	for index, chunk := range chunks {
		stored[index] = entity.KnowledgeChunk{
			DocumentID: documentID,
			ChunkIndex: int32(chunk.Index),
			Content:    chunk.Content,
			// character_count 的语义是字符数而不是字节数：PostgreSQL 的 char_length
			// 按字符算，而 check 约束要求它大于 0。这里与数据库口径保持一致。
			CharacterCount: int32(len([]rune(chunk.Content))),
		}
		if chunk.Heading != "" {
			heading := chunk.Heading
			stored[index].Heading = &heading
		}
	}
	return stored
}

// chunksFromEntities 把已落库的切片折回切分产物的形状，供 embed 阶段向量化使用。
// 恢复路径不重新切分，切片内容与顺序都以上次落库的为准。
func chunksFromEntities(stored []entity.KnowledgeChunk) []Chunk {
	chunks := make([]Chunk, len(stored))
	for index, row := range stored {
		heading := ""
		if row.Heading != nil {
			heading = *row.Heading
		}
		chunks[index] = Chunk{Index: int(row.ChunkIndex), Heading: heading, Content: row.Content}
	}
	return chunks
}

// buildStoredEmbeddings 把向量折成待落库的向量行，ChunkID 与 vectors 一一对应。
// 顺序由 ListChunksByDocument 保证（按 chunk_index 升序）。
func buildStoredEmbeddings(stored []entity.KnowledgeChunk, vectors [][]float64, model *entity.EmbeddingModel) ([]entity.KnowledgeEmbedding, error) {
	if len(stored) != len(vectors) {
		return nil, fmt.Errorf("切片与向量数量不一致: %d / %d", len(stored), len(vectors))
	}

	// 同一批向量的生成时间取同一个时刻，理由同 buildReplacement。
	generatedAt := time.Now().UTC()
	embeddings := make([]entity.KnowledgeEmbedding, len(vectors))
	for index, vector := range vectors {
		literal, err := vectorLiteral(vector)
		if err != nil {
			return nil, fmt.Errorf("第 %d 个切片的向量无效: %w", index+1, err)
		}
		embeddings[index] = entity.KnowledgeEmbedding{
			ChunkID: stored[index].ID,
			ModelID: model.ID,
			// 维度取真实长度，理由同 buildReplacement。
			Dimensions:  int32(len(vector)),
			Embedding:   literal,
			GeneratedAt: generatedAt,
		}
	}
	return embeddings, nil
}

// normalizeSourceType 校验来源类型。
//
// 必须在这里挡一次：数据库上 source_type 的 CHECK 只接受 manual / import 两个值，
// 直接透传会让用户收到一条 "violates check constraint" 的原始报错。
func normalizeSourceType(value, fallback string) (string, error) {
	sourceType := strings.TrimSpace(value)
	if sourceType == "" {
		return fallback, nil
	}
	switch sourceType {
	case entity.KnowledgeDocumentSourceManual, entity.KnowledgeDocumentSourceImport:
		return sourceType, nil
	default:
		return "", fmt.Errorf("来源类型 %q 无效，只能是 manual 或 import", sourceType)
	}
}

// checksum 计算正文的 SHA-256。列类型是 char(64)，所以输出必须是 64 位十六进制。
func checksum(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// truncateTitle 按字符截断标题，与 varchar(300) 的语义一致。
// 文件名动辄上百个字符，不截断会让创建文档这一步直接撞列长度。
func truncateTitle(title string) string {
	title = strings.TrimSpace(title)
	runes := []rune(title)
	if len(runes) <= maxTitleRunes {
		return title
	}
	return string(runes[:maxTitleRunes])
}

// preferHeadingTitle 在调用方没指定标题时，用正文里的首个一级标题代替文件名。
func preferHeadingTitle(fallback, markdown string) string {
	heading := firstHeading(markdown)
	if heading == "" {
		return truncateTitle(fallback)
	}
	return truncateTitle(heading)
}

// firstHeading 取正文的第一个一级标题。
//
// 只看第一行：一级标题本来就该出现在文档开头，往下找只会把正文里引用到的
// 别处标题当成这篇文档的标题。
func firstHeading(markdown string) string {
	for _, line := range strings.Split(markdown, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, "# ") {
			return ""
		}
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
	}
	return ""
}

// firstLineTitle 取正文的第一行做标题，用于既没有标题也没有一级标题的正文。
func firstLineTitle(content string) string {
	for _, line := range strings.Split(content, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return truncateTitle(trimmed)
		}
	}
	return "未命名文档"
}

// parserName 取本次解析用的解析器身份，取不到时给一个明确的占位值 ——
// metadata 里出现空字符串，排查时反而要多想一步"是没记录还是真没有"。
func parserName(result *documentparser.Result) string {
	if name := result.ParserName(); name != "" {
		return name
	}
	return "unknown"
}

// maxPageNumbersInReason 报错里最多列几个失败页码，其余折叠成"共 N 页失败"。
const maxPageNumbersInReason = 10

// ocrCoverageError 把"有页 OCR 失败"变成一次明确的解析失败。
//
// 脚本对单页失败不中断整篇，只把 failed 标在 pages 上（见 documentparser.Page）——
// 那是对的，但后果是结果可能缺页。这里必须拦一道：宁可整篇 failed 让用户重试，
// 也不能把缺页的文档静默标成 ready —— 后者检索不到那些页的内容，而界面上看不出任何异常。
// 失败时原件已归档，重试路径完整（API OCR 的网络抖动重试一次往往就好了）。
func ocrCoverageError(result *documentparser.Result) error {
	failed := result.OCRFailedPages()
	if len(failed) == 0 {
		return nil
	}
	return &documentparser.Error{
		Code: documentparser.CodeOCRFailedPages,
		Message: fmt.Sprintf("OCR 未能识别部分页面（%s），正文不完整；请重试，若持续失败请检查扫描质量或 OCR 配置",
			formatPageNumbers(failed)),
	}
}

// documentExists 判断一条文档是否还在，供 worker 决定失败原件要不要归档。
//
// 查不动（DB 抖动）时按"还在"处理：宁可留下一个以后能清理的归档，
// 也不能因为一次查询失败就把用户的原件删掉 —— 那会让重试永久失去输入。
func (i *Ingester) documentExists(id uint64) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := i.store.GetByID(ctx, id)
	if err == nil {
		return true
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false
	}
	logger.Warn("确认文档是否仍存在时出错，按仍存在处理",
		zap.Uint64("document_id", id), zap.Error(err))
	return true
}

// formatPageNumbers 把页码列成"第 3、7、12 页"；超过上限时折叠，避免长文档的报错刷屏。
func formatPageNumbers(pages []int) string {
	limit := len(pages)
	suffix := ""
	if limit > maxPageNumbersInReason {
		limit = maxPageNumbersInReason
		suffix = fmt.Sprintf(" 等，共 %d 页失败", len(pages))
	}
	parts := make([]string, limit)
	for index := 0; index < limit; index++ {
		parts[index] = strconv.Itoa(pages[index])
	}
	return "第 " + strings.Join(parts, "、") + " 页" + suffix
}
