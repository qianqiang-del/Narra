package rag

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"narra/internal/model/entity"
	"narra/internal/ownership"
	"narra/pkg/documentparser"
	"narra/pkg/embedding"
	"narra/pkg/logger"
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

// ImagePublisher 把解析产出的图片发布到持久存储，返回"临时路径 → 对外 URL"的映射。
//
// 解析脚本导出的图片放在临时目录里（见 documentparser.Result.WorkDir），收录链路
// 必须在清理它之前完成发布与回填（见 Ingester.backfillImages）。生产装配注入本地
// 存储（documentimage.Store）或对象存储（documentimage.OSSStore），两种实现同形。
type ImagePublisher interface {
	// Publish 发布一篇文档的图片。任何一张失败都返回错误，调用方据此整篇失败，
	// 而不是把本地路径留在正文里变成死链。
	Publish(documentID uint64, paths []string) (map[string]string, error)
}

// FileStore 是上传原件的对象存储面。
//
// 接入后原件以对象存储为唯一存储：SubmitFile 先把本地暂存文件传上去、把 key 写进
// metadata（upload_key），解析成功即删本地；解析失败也不再把原件归档到磁盘，重试时
// 由 Worker 按 key 重新下载（见 worker.go）。nil 表示未接入，原件只留本地（旧行为）。
type FileStore interface {
	// PutFile 把本地文件上传为 key；同 key 覆盖，重试幂等。
	PutFile(key, filePath string) error
	// GetToFile 把 key 下载到本地路径，供解析阶段使用。
	GetToFile(key, filePath string) error
	// Delete 删除一个 key；key 不存在不报错。
	Delete(key string) error
}

// ModelRegistry 是收录与检索两条链路对向量模型登记的最小依赖面。
type ModelRegistry interface {
	// GetDefault 取当前默认模型。库里没有默认模型时返回 gorm.ErrRecordNotFound。
	GetDefault(ctx context.Context) (*entity.EmbeddingModel, error)

	// EnsureDefault 把一份模型登记为唯一默认模型；同名且维度一致时复用已有行。
	// 同名但维度不同、且该模型下已有向量时，实现方会拒绝并返回维度冲突错误。
	EnsureDefault(ctx context.Context, model entity.EmbeddingModel) (*entity.EmbeddingModel, error)

	// CountVectorsByModel 统计每个模型名下的向量数，给"向量召回静默为零"的体检用
	// （见 vector_census.go）。收录链路不用它，但两链路共用同一个接口，不另拆一个。
	CountVectorsByModel(ctx context.Context) ([]entity.ModelVectorCount, error)

	// DeleteUnusedModels 删除名下已无向量的非默认模型行，返回被删掉的名字。
	// 收录成功后做一次收尾：重新向量化把旧模型的最后一批向量替换掉后，旧行就是空壳。
	DeleteUnusedModels(ctx context.Context) ([]string, error)
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
	// Purpose 是入库用途标记。空 = 普通知识库文档；material = 课程材料（临时，
	// 未关联课堂时带 expires_at，到期由后台清理）。取值见 entity.KnowledgeDocumentKindXxx。
	Purpose string
}

// TextInput 是直接收录一段正文所需的输入。
type TextInput struct {
	Title      string // 调用方指定的标题；为空时依次回落到正文一级标题、首行
	Content    string // 待收录的正文：可能是 Markdown，也可能是代码文本，由切分入口判定类型（见 classify.go）
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

// defaultMaterialTTL 是课程材料未关联课堂时的默认保留时长，与 config 的默认值一致。
const defaultMaterialTTL = 7 * 24 * time.Hour

// maxTitleRunes 标题长度上限，与 knowledge_documents.title 的 varchar(300) 对齐。
const maxTitleRunes = 300

// TxRunner 把若干仓储写入包进一个数据库事务，由 repository.TransactionManager 实现，
// 在 internal/app 注入（与课堂生成链路的 Tx 同一装法）。
//
// 收录链路需要它，是因为一次文件提交要跨三张表写：建文档行、写 upload_path 元数据、
// 建上传记录。任何一步失败都必须整体回滚 —— 否则会留下一条没有路径的 pending 孤儿行，
// worker 捡起来只能落成 failed，而磁盘上还残留一份没人认领的原件。
type TxRunner interface {
	// Run 开启事务并回调 fn；fn 返回错误则整体回滚。fn 拿到的 ctx 携带事务句柄，
	// 参与调用的仓储方法会自动落在同一个事务上（见 repository.conn）。
	Run(ctx context.Context, fn func(ctx context.Context) error) error
}

// VisionProvider 提供当前启用的视觉模型（VLM）配置：设置页维护，启用即生效。
//
// 收录链路在每次解析前现取一次 —— 与 embedding / rerank 的常驻运行时不同，
// 视觉配置只在这一个点用到，没有需要热推送的理由（实现方是 vlm 设置服务）。
// 返回 ok = false 表示"未启用视觉理解"，不是错误。
type VisionProvider interface {
	CurrentVision(ctx context.Context, ownerID ...uint64) (documentparser.VLMConfig, bool, error)
}

// IngestOptions 是收录器的运行约束，由配置与装配点注入。
type IngestOptions struct {
	// Tx 跨表写入的事务管理器，SubmitFile 与 Retry 依赖它。必须非 nil：
	// 缺了它宁可当场报错，也不能静默退化成没有事务的三条独立写入。
	Tx TxRunner

	// QueueCapacity 是 pending + processing 文档数的硬上限；<=0 表示不限制。
	//
	// 检查在事务内、与建行一起完成，并由 pg_advisory_xact_lock 串行化 ——
	// 多实例部署时同一个上限不会被各自的计数绕过。
	QueueCapacity int

	// EmbeddingConcurrency 是全局同时进行向量化的文档数；<=0 按 1 处理。
	//
	// 它独立于 Worker 的解析并发：解析吃 CPU 与 OCR，向量化吃上游额度与内存，
	// 两者分开限流，避免两个解析任务把 embedding 批次放大成并发请求。
	EmbeddingConcurrency int

	// Images 是解析图片的发布器；nil 表示未接入。只有不产出图片的链路
	// （IngestText、纯文本解析）可以不依赖它 —— 解析出图片却没接发布器时收录会
	// 明确失败，而不是等临时目录一删、正文里的引用全变成死链（见 backfillImages）。
	Images ImagePublisher

	// Files 是上传原件的对象存储；nil 表示原件只留本地（本地部署模式）。
	// 接入后 SubmitFile 会把原件上传并记 upload_key，解析失败不再归档本地文件，
	// 重试按 key 重新下载（见 worker.go 的 prepareParseInput）。
	Files FileStore

	// Vision 是视觉模型配置的提供者；nil 表示未接入（解析只走 OCR）。
	Vision VisionProvider

	// MaterialTTL 是课程材料（Purpose = material）未关联课堂时的保留时长。
	// <= 0 按默认 7 天处理；只在上传建行时用来算 expires_at。
	MaterialTTL time.Duration
}

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
type Ingester struct {
	store     DocumentStore
	records   UploadRecordStore
	models    ModelRegistry
	embedding *embedding.Manager
	parser    documentparser.Parser
	images    ImagePublisher
	// files 是原件的对象存储；nil 表示本地模式（原件留在 upload 目录）。
	files FileStore
	// vision 是当前启用的视觉模型配置来源；nil 表示未接入（解析只走 OCR）。
	vision VisionProvider

	// tx 与 queueCapacity 由 SubmitFile / Retry 使用：每次提交在事务里先校验队列
	// 还有没有空位，再落三张表的行。
	tx            TxRunner
	queueCapacity int
	// materialTTL 是课程材料未关联课堂时的保留时长；上传建行时算 expires_at 用。
	materialTTL time.Duration

	// embeddingSem 是全局向量化名额（见 IngestOptions.EmbeddingConcurrency）。
	// nil 表示不限（只在不走 Ingester 的同步测试路径里可能出现）。
	embeddingSem chan struct{}

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

	// CountActive 统计 pending + processing 的文档数，供队列容量检查。
	CountActive(context.Context) (int64, error)

	// AcquireIngestQueueLock 在**当前事务**里取得队列容量检查的排他锁
	// （pg_advisory_xact_lock）。必须在事务内调用：锁随事务提交/回滚自动释放，
	// 在事务外调用等于没锁。多实例部署时它让"计数 + 建行"串行，队列容量因此
	// 是硬上限，而不是各实例各算一次的软上限。
	AcquireIngestQueueLock(context.Context) error

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

	// RequeueForReembed 把一行 ready 文档改回 pending，准备用当前默认模型重新向量化；
	// 只改状态与阶段，不碰 metadata 与上传记录。返回 false 表示它已经不是 ready
	// （并发的另一次请求抢先了）。与 Requeue 一样是"条件更新里决胜负"，不需要额外加锁。
	RequeueForReembed(context.Context, uint64, string) (bool, error)

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
// parser 可以是 nil —— 表示文档解析能力没启用，此时只有直读格式（md / txt 与源码、
// 数据文件）能收录，其它格式会收到一句明确的错误（见 documentparser.ParserFor）。
// 这里不做 fail-fast，是因为"解析器没装好"不该拦住纯文本导入和整个服务的启动。
//
// records 是上传记录的写入口，只在文件收录（SubmitFile）里用到。
// options 里的 Tx 必须非 nil（见 IngestOptions）。
func NewIngester(
	store DocumentStore,
	records UploadRecordStore,
	models ModelRegistry,
	embeddingManager *embedding.Manager,
	parser documentparser.Parser,
	options IngestOptions,
) *Ingester {
	limit := options.EmbeddingConcurrency
	if limit < 1 {
		limit = 1
	}
	materialTTL := options.MaterialTTL
	if materialTTL <= 0 {
		materialTTL = defaultMaterialTTL
	}
	return &Ingester{
		store:         store,
		records:       records,
		models:        models,
		embedding:     embeddingManager,
		parser:        parser,
		images:        options.Images,
		files:         options.Files,
		vision:        options.Vision,
		tx:            options.Tx,
		queueCapacity: options.QueueCapacity,
		materialTTL:   materialTTL,
		embeddingSem:  make(chan struct{}, limit),
		newEmbedder:   newModelEmbedderFactory(embeddingManager),
	}
}

// runInTx 在事务里执行跨表写入。
//
// 没注入事务管理器时直接报错而不是退化成"三条独立写入"：后者在失败时会留下没有
// upload_path 的 pending 孤儿行，这种数据只能人工清，比当场喊出来危险得多。
func (i *Ingester) runInTx(ctx context.Context, fn func(context.Context) error) error {
	if i.tx == nil {
		return fmt.Errorf("知识库收录事务不可用：未注入事务管理器")
	}
	return i.tx.Run(ctx, fn)
}

// reserveQueueSlot 在事务内为一次提交或重试检查队列空位。
//
// 顺序固定在事务里：先取得 pg_advisory_xact_lock，再计数。分两处写就给了多实例
// "两边都看到还剩一个位"的窗口，队列容量也就从硬上限退化成各自计数的软上限。
// 计数包含 pending 与 processing 两种行：前者还没被 worker 接手，后者正在跑，
// 都占着磁盘上的暂存文件与后台的处理位。
func (i *Ingester) reserveQueueSlot(ctx context.Context, store FileTaskStore) error {
	if i.queueCapacity <= 0 {
		return nil
	}
	if err := store.AcquireIngestQueueLock(ctx); err != nil {
		return fmt.Errorf("检查收录队列失败: %w", err)
	}
	active, err := store.CountActive(ctx)
	if err != nil {
		return fmt.Errorf("检查收录队列失败: %w", err)
	}
	if active >= int64(i.queueCapacity) {
		return ErrIngestQueueFull
	}
	return nil
}

// acquireEmbeddingSlot 取得一个全局向量化名额，返回释放函数。
//
// 等待期间任务心跳仍在跑（本函数在 processOne 的 ctx 下调用），所以排队不会被
// 周期回收误判成僵尸。名额只约束异步文件收录的 embed 阶段：同步的正文收录
// （IngestText）与检索都不共用它 —— 否则用户在编辑器里保存一段正文会被批量导入堵住。
func (i *Ingester) acquireEmbeddingSlot(ctx context.Context) (func(), error) {
	if i.embeddingSem == nil {
		return func() {}, nil
	}
	select {
	case i.embeddingSem <- struct{}{}:
		return func() { <-i.embeddingSem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// SubmitFile 创建待处理文档并持久化任务路径，实际处理由 Worker 完成。
//
// 返回时文档是 pending，正文与切片都还是空的，调用方拿 id 去轮询即可。
// metadata 里给 worker 留三个键：upload_path 是磁盘上的暂存文件、upload_key 是
// 对象存储里的原件（接入时才有，见 IngestOptions.Files）、explicit_title 记录调用方
// 有没有指定过标题 —— 后者决定 worker 要不要用正文的一级标题替换文件名的暂时代替标题。
//
// 三件事在**一个事务**里完成：校验队列空位、建文档行、写 metadata 与上传记录。
// 接入对象存储时上传在事务之前完成，建行失败会补偿删除对象（没有行认领的孤儿）。
// 批量上传时调用方逐文件调用它，每个文件独立成败：某个文件撞上队列满或写库失败只
// 回滚它自己，此前已提交的文件不受影响。
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
	metadata := map[string]any{
		"upload_path":    path,
		"explicit_title": strings.TrimSpace(input.Title) != "",
	}

	// 对象存储模式：先把原件传上去，再把 key 写进 metadata。
	//
	// 顺序是先传文件、后开事务建行：key 由时间戳+随机后缀生成，不依赖文档 ID。
	// 上传失败直接拒绝这一份 —— 不建行、不占队列位；建行失败（队列满、写库失败）
	// 则由下面的补偿删除把对象清掉。两步之间文档尚未存在，worker 看不到任何中间态。
	uploadKey := ""
	if i.files != nil {
		uploadKey = newUploadObjectKey(path)
		if err := i.files.PutFile(uploadKey, path); err != nil {
			return IngestResult{}, fmt.Errorf("上传原件到对象存储失败: %w", err)
		}
		metadata["upload_key"] = uploadKey
	}
	payload, _ := json.Marshal(metadata)

	var document *entity.KnowledgeDocument
	err = i.runInTx(ctx, func(ctx context.Context) error {
		if err := i.reserveQueueSlot(ctx, store); err != nil {
			return err
		}

		created, err := i.createDocument(ctx, title, sourceType, sourceURI, input.Purpose)
		if err != nil {
			return err
		}
		if err := store.SetMetadata(ctx, created.ID, payload); err != nil {
			return fmt.Errorf("记录上传暂存路径失败: %w", err)
		}

		// 投递历史与文档行同事务：建失败就让整个提交回滚。
		//
		// 早期版本刻意忽略这里的失败（"记录只是历史，缺一条不影响收录"），批量上线后
		// 改成硬失败：一次批量里几十条文件各自提交，若允许半截成功，会出现"文档在转圈、
		// 抽屉里没有这条记录"的条目 —— 用户在界面上既看不到进度也没有重试入口。
		record := &entity.KnowledgeUploadRecord{
			OwnerID:      created.OwnerID,
			DocumentID:   &created.ID,
			OriginalName: truncateTitle(sourceURI),
			SizeBytes:    input.SizeBytes,
			Status:       entity.KnowledgeUploadRecordStatusPending,
			// 类型随文档快照：文档被删后记录仍能区分知识库投递与课程材料投递。
			Kind: created.Kind,
		}
		if err := i.records.CreateUploadRecord(ctx, record); err != nil {
			return fmt.Errorf("创建上传记录失败: %w", err)
		}

		document = created
		return nil
	})
	if err != nil {
		// 建行失败时清掉刚上传的对象：没有文档行认领它，留着就是一份永远没人回收的孤儿。
		// 删除失败只留告警 —— 补偿动作失败不能盖住原始的提交错误。
		if uploadKey != "" {
			if deleteErr := i.files.Delete(uploadKey); deleteErr != nil {
				logger.Warn("清理未入队原件对象失败，对象可能残留",
					zap.String("key", uploadKey), zap.Error(deleteErr))
			}
		}
		return IngestResult{}, err
	}
	return IngestResult{Document: document}, nil
}

// FetchOriginal 把一份原件从对象存储下载到本地路径，供 Worker 在解析阶段使用。
//
// 只在 metadata 里确实有 upload_key 时才会被调用；未接入对象存储时返回错误，
// 由调用方转成明确的收录失败。
func (i *Ingester) FetchOriginal(key, destPath string) error {
	if i.files == nil {
		return fmt.Errorf("原件的对象存储未接入")
	}
	return i.files.GetToFile(key, destPath)
}

// uploadObjectKeyPrefix 是上传原件在对象存储里的 key 空间。
const uploadObjectKeyPrefix = "knowledge/uploads"

// newUploadObjectKey 生成原件 key：knowledge/uploads/<日期>/<纳秒时间戳>-<随机后缀><文件后缀>。
//
// 日期目录让运维能按天对账与清理；时间戳+随机后缀保证批量上传不重名。
// 后缀来自服务端落盘时的命名（upload.<ext>，见 controller.stageUpload），
// 解析器按它选实现，所以必须保留；取值仍按不可信输入清洗（见 safeObjectExtension）。
func newUploadObjectKey(path string) string {
	var random [4]byte
	_, _ = rand.Read(random[:])
	now := time.Now()
	return fmt.Sprintf("%s/%s/%d-%s%s",
		uploadObjectKeyPrefix,
		now.Format("20060102"),
		now.UnixNano(),
		hex.EncodeToString(random[:]),
		safeObjectExtension(path))
}

// safeObjectExtension 提取文件后缀，只保留小写字母数字组成的短后缀；异常取值返回空串。
func safeObjectExtension(path string) string {
	extension := strings.ToLower(filepath.Ext(strings.TrimSpace(path)))
	if len(extension) < 2 || len(extension) > 10 {
		return ""
	}
	for _, char := range extension[1:] {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') {
			return ""
		}
	}
	return extension
}

// Retry 把一条收录失败的文档重新入队，由 Worker 再跑一遍。
//
// stage 是调用方（服务层）按现实材料算好的恢复点，见 ResolveRecoveryStage：
// 有切片重向量化、没切片有正文重分块、都没了才重新解析原文件。
// 它只改状态与阶段、不碰文件：本地模式下原件在失败时被 worker 归档到 failed/<文档ID>/，
// metadata 里的 upload_path 指着那里；对象存储模式下原件始终在 OSS，重试时由 worker
// 按 upload_key 重新下载（见 worker.prepareParseInput）。下一轮轮询都会照常捡起这一行。
//
// 返回 false 表示这一行不满足重试条件 —— 不存在，或状态已经不是 failed
// （另一个请求抢先重试了，或它已经被删掉）。这不是错误，调用方按"不需要重试"处理。
//
// 它是**原地重试**：复用同一行文档与同一条上传记录，不新建任何东西。
// 一份文件一份资产，重试只是让它再跑一次，投递历史里不该凭空多出一条。
//
// 重试要占一个队列位：目标文档此刻是 failed（不计入活跃数），改回 pending 后会占住
// 一个处理位，所以容量检查必须在事务内与 Requeue 一起做，见 reserveQueueSlot。
func (i *Ingester) Retry(ctx context.Context, id uint64, stage string) (bool, error) {
	store, ok := i.store.(FileTaskStore)
	if !ok {
		return false, fmt.Errorf("知识库存储不支持异步文件任务")
	}

	requeued := false
	err := i.runInTx(ctx, func(ctx context.Context) error {
		if err := i.reserveQueueSlot(ctx, store); err != nil {
			return err
		}
		var err error
		requeued, err = store.Requeue(ctx, id, stage)
		return err
	})
	return requeued, err
}

// Reembed 把一条已经 ready 的文档重新排队，用**当前默认模型**重算向量。
//
// 它服务"换过默认模型、但存量向量还挂在旧模型名下"的场景：后台按现实材料算恢复点，
// 切片还在就直接从 embed 阶段重跑 —— 不重新解析、不重新切分，保存新向量时旧的
// 一并被替换（见 SaveEmbeddingsAndMarkReady）。默认模型没变时调用方不该调它，
// 否则只会白烧一次上游额度。
//
// 与 Retry 共用同一套机制，只有起点状态不同：同样在事务里占一个队列名额，
// 同样靠条件更新决定并发胜负（两个请求同时点，只有一个的 requeued 为 true）。
// 返回 false 表示这一行不满足条件 —— 它已经不是 ready，安静跳过即可。
//
// 队列满时原样返回 ErrIngestQueueFull，批量入队的调用方据此停下（见 service.Reembed）。
func (i *Ingester) Reembed(ctx context.Context, id uint64, stage string) (bool, error) {
	store, ok := i.store.(FileTaskStore)
	if !ok {
		return false, fmt.Errorf("知识库存储不支持异步文件任务")
	}

	requeued := false
	err := i.runInTx(ctx, func(ctx context.Context) error {
		if err := i.reserveQueueSlot(ctx, store); err != nil {
			return err
		}
		var err error
		requeued, err = store.RequeueForReembed(ctx, id, stage)
		return err
	})
	return requeued, err
}

// backfillImages 发布解析产出的图片，并把 Markdown 里的本地路径替换成对外 URL。
//
// 顺序不能颠倒：发布与回填必须都完成，调用方的 defer 才能清理临时目录（见
// Result.Cleanup）。没有图片时原样返回；有图片但没接发布器、或发布失败时返回错误 ——
// 静默保留本地路径的话，临时目录一删，正文里全是死链，而用户和检索都看不出异常。
func (i *Ingester) backfillImages(documentID uint64, markdown string, paths []string) (string, error) {
	if len(paths) == 0 {
		return markdown, nil
	}
	if i.images == nil {
		return "", fmt.Errorf("解析产出 %d 张图片，但图片存储未接入，无法发布", len(paths))
	}

	urls, err := i.images.Publish(documentID, paths)
	if err != nil {
		return "", fmt.Errorf("发布解析图片失败: %w", err)
	}

	// 按路径长度从长到短替换：若一张图的路径恰好是另一张的前缀（如 .../a.png 与
	// .../a.png.bak），先替换短的会把长的切坏，之后长路径就再也匹配不上了。
	ordered := make([]string, 0, len(urls))
	for local := range urls {
		ordered = append(ordered, local)
	}
	sort.Slice(ordered, func(a, b int) bool { return len(ordered[a]) > len(ordered[b]) })
	for _, local := range ordered {
		markdown = strings.ReplaceAll(markdown, local, urls[local])
	}
	return markdown, nil
}

// processExistingFile 处理一条已经建好行的文件收录任务，由 Worker 调用。
//
// stage 是 Worker 按现实材料算好的恢复点（ResolveRecoveryStage），不是行上的 ingest_stage：
// parse 读原文件解析，chunk 从已落库的正文切分，embed 从已落库的切片生成向量。
// 每步成功都把中间结果与阶段一起落库，所以进程崩溃、向量服务抖动都不会让昂贵的解析
// 白跑一遍 —— 这正是分阶段收录的意义。
//
// 标题回落的判据来自任务元数据：调用方当初没指定标题时，worker 会传空标题进来，
// 这里才走到"用正文一级标题替换"。attempt 是这次处理的租约编号，所有阶段写入
// 都要求与它相符（见 stagedUpdate）。
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
		request := documentparser.Request{Path: input.Path}
		// 视觉配置每次解析现取：设置页启停/改模型后，下一份文档立即生效。
		// 取配置失败降级为纯 OCR（告警），不让整个收录失败 —— 视觉描述是增量，
		// 本地 OCR 的正文仍然完整可用。
		if i.vision != nil {
			vision, ok, err := i.vision.CurrentVision(ctx, document.OwnerID)
			if err != nil {
				logger.Warn("读取视觉模型配置失败，本次解析只走 OCR",
					zap.Uint64("document_id", document.ID), zap.Error(err))
			} else if ok {
				request.VLM = &vision
			}
		}
		started := time.Now().UTC()
		result, err := parser.Parse(ctx, request)
		if err != nil {
			return i.failIngest(ctx, document, attempt, "parse", err)
		}
		defer func() { _ = result.Cleanup() }()

		// 缺页的文档不能标 ready：正文不完整时宁可整篇失败，让用户重试。
		if err := ocrCoverageError(result); err != nil {
			return i.failIngest(ctx, document, attempt, "parse", err)
		}
		// 图片发布、URL 回填必须在 SaveParsedContent 之前：chunk / embed 阶段是
		// 从库里读正文恢复的，这一步没做的话，崩溃恢复出来的切片里全是失效的本地路径。
		markdown, err = i.backfillImages(document.ID, result.Markdown, result.PicturePaths)
		if err != nil {
			return i.failIngest(ctx, document, attempt, "parse", err)
		}
		if strings.TrimSpace(input.Title) == "" {
			title = preferHeadingTitle(title, markdown)
		}

		parseMetadata := map[string]any{
			"parser":   parserName(result),
			"parse_ms": time.Since(started).Milliseconds(),
		}
		// 视觉理解的现场（启用了哪个模型 / 是否发生降级 / 调用了几次）由脚本写进
		// metadata，原样带进文档行，排障时不必翻 worker 日志。
		for _, key := range []string{"vision", "vlm_model", "vlm_calls", "vision_fallback"} {
			if value, ok := result.Metadata[key]; ok {
				parseMetadata[key] = value
			}
		}
		metadata := marshalMetadata(parseMetadata)
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
		chunks, err := chunkDocument(ctx, markdown, DocumentHint{Path: input.Path, SourceURI: input.SourceURI})
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

		// 向量化名额：全局同时只允许配置数量的文档在跑，等待期间心跳照常。
		// 名额在 embedInBatches 之前取得、整个文档的向量化结束后释放（defer），
		// 目的就是不让两个解析任务的批次请求互相叠加。
		release, err := i.acquireEmbeddingSlot(ctx)
		if err != nil {
			return i.failIngest(ctx, document, attempt, "embed", err)
		}
		defer release()

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
		// 向量换完了，被替换光的旧模型行就此收尾（best-effort，不参与成败）。
		i.cleanupUnusedModels(ctx)

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

	document, err := i.createDocument(ctx, title, sourceType, strings.TrimSpace(input.SourceURI), "")
	if err != nil {
		return IngestResult{}, err
	}
	return i.ingestMarkdown(ctx, document, title, content, DocumentHint{SourceURI: strings.TrimSpace(input.SourceURI)}, map[string]any{"parser": "direct"})
}

// ingestMarkdown 是同步链路（正文收录）的公共后半段：切分 → 向量化 → 一次事务落三张表。
//
// hint 是切分入口做文档类型判定的旁证（来源路径/文件名），可为空。
//
// 它不参与分阶段恢复：正文收录没有原文件可重试，也没有任务队列，
// 要"要么全成、要么全不成"，所以走 ReplaceChunks 的单事务。
// 异步文件链路由 processExistingFile 按 ingest_stage 分阶段推进。
func (i *Ingester) ingestMarkdown(
	ctx context.Context,
	document *entity.KnowledgeDocument,
	title string,
	markdown string,
	hint DocumentHint,
	metadata map[string]any,
) (IngestResult, error) {
	if strings.TrimSpace(markdown) == "" {
		return i.failIngest(ctx, document, 0, "parse", fmt.Errorf("%w: 没有可入库的正文", ErrEmptyContent))
	}
	if err := i.store.MarkProcessing(ctx, document.ID); err != nil {
		return i.failIngest(ctx, document, 0, "status", fmt.Errorf("更新文档状态失败: %w", err))
	}

	chunkStarted := time.Now().UTC()
	chunks, err := chunkDocument(ctx, markdown, hint)
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
	// 重新收录也会把旧模型的向量整批换掉，同样顺手收尾（best-effort，不参与成败）。
	i.cleanupUnusedModels(ctx)

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
		Dimensions: int32(cfg.Dimensions),
	})
	if err != nil {
		return nil, fmt.Errorf("向量模型登记与当前配置不一致，重新对齐失败: %w", err)
	}
	return aligned, nil
}

// cleanupUnusedModels 清掉名下已无向量的非默认模型行（best-effort）。
//
// 重新向量化把旧模型的最后一批向量替换掉之后，那个模型行就只剩空壳；在每次
// 向量写入成功之后顺手收尾，旧行不会在库里长期留着。它刻意不参与收录成败：
// 失败只记一条 Warn —— 为一条清理把一篇已经成功的文档判成失败，代价完全不成比例，
// 而且下一次成功的收录还会再清一次。
func (i *Ingester) cleanupUnusedModels(ctx context.Context) {
	deleted, err := i.models.DeleteUnusedModels(ctx)
	if err != nil {
		// 关停时 ctx 已取消，失败是预期内的，不必喧哗。
		if ctx.Err() == nil {
			logger.Warn("清理无向量的旧模型失败（不影响收录结果）", zap.Error(err))
		}
		return
	}
	for _, name := range deleted {
		logger.Info("已清理名下无向量的旧模型", zap.String("model", name))
	}
}

// createDocument 建文档行，此时正文还是空的，状态是 pending。
//
// purpose = material 时按课程材料入库：kind 标 material 并带上 expires_at（未关联
// 课堂时到期由 retention 清理）；普通知识库文档 kind = knowledge、expires_at 为空。
func (i *Ingester) createDocument(ctx context.Context, title, sourceType, sourceURI, purpose string) (*entity.KnowledgeDocument, error) {
	document := &entity.KnowledgeDocument{
		OwnerID:    ownership.FromContext(ctx),
		Title:      truncateTitle(title),
		SourceType: sourceType,
		Enabled:    true,
		Status:     entity.KnowledgeDocumentStatusPending,
		Kind:       entity.KnowledgeDocumentKindKnowledge,
		// metadata 是 NOT NULL 的 jsonb，必须写 '{}' 而不是留空：
		// 留空在 GORM 里会变成 NULL，被列约束直接拒掉。
		Metadata: json.RawMessage(`{}`),
	}
	if purpose == entity.KnowledgeDocumentKindMaterial {
		document.Kind = entity.KnowledgeDocumentKindMaterial
		expiresAt := time.Now().UTC().Add(i.materialTTL)
		document.ExpiresAt = &expiresAt
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

// chunkDocument 把正文切成切片，并做两道校验：切不出任何切片、超过单篇上限。
// 同步链路与异步文件链路共用它，保证两条路对"什么算合法切片"的判断一致。
//
// 它是切分链路的唯一入口：先由 splitDocument 判定内容类型（文档/普通文本/代码），
// 再交给对应的切分器。判定放在这里而不是解析前，是因为重试与崩溃恢复只会从库里
// 拿到 content —— 判定必须能离线重放（见 classify.go）。
func chunkDocument(ctx context.Context, content string, hint DocumentHint) ([]Chunk, error) {
	chunks, err := splitDocument(ctx, content, hint, ChunkOptions{})
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
		if chunk.SectionPath != "" {
			sectionPath := chunk.SectionPath
			stored[index].SectionPath = &sectionPath
		}
		stored[index].ContentType = optionalString(chunk.ContentType)
		stored[index].Language = optionalString(chunk.Language)
		stored[index].Symbol = optionalString(chunk.Symbol)
		stored[index].SymbolType = optionalString(chunk.SymbolType)
	}
	return stored
}

// optionalString 把非空字符串折成可空列的值；空串写 NULL 而不是空串，
// 让"有没有这项信息"在库里是一个明确的事实。
func optionalString(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	copied := value
	return &copied
}

// stringValue 是可空列的回读：nil 一律折成空串，调用方不必到处判空。
func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// chunksFromEntities 把已落库的切片折回切分产物的形状，供 embed 阶段向量化使用。
// 恢复路径不重新切分，切片内容与顺序都以上次落库的为准。
func chunksFromEntities(stored []entity.KnowledgeChunk) []Chunk {
	chunks := make([]Chunk, len(stored))
	for index, row := range stored {
		chunks[index] = Chunk{
			Index:       int(row.ChunkIndex),
			Heading:     stringValue(row.Heading),
			SectionPath: stringValue(row.SectionPath),
			Content:     row.Content,
			ContentType: stringValue(row.ContentType),
			Language:    stringValue(row.Language),
			Symbol:      stringValue(row.Symbol),
			SymbolType:  stringValue(row.SymbolType),
		}
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

// preferHeadingTitle 在调用方没指定标题时，用正文里的标题代替文件名：
// front matter 的 title 优先（它是作者显式写下的文档名），其次正文里的首个一级标题。
// 两者都取不到才退回 fallback（文件名 / 首行）。
func preferHeadingTitle(fallback, markdown string) string {
	if title := FrontMatterTitle(markdown); title != "" {
		return truncateTitle(title)
	}
	heading := firstHeading(markdown)
	if heading == "" {
		return truncateTitle(fallback)
	}
	return truncateTitle(heading)
}

// firstHeading 取正文的第一个一级标题。front matter 整块跳过（那是元数据不是正文）；
// 开头孤立的一条 --- 也先删掉（它会把整篇吞成纯文本块，见 sections.go 的说明）；
// 只看第一行有效内容：一级标题本来就该出现在文档开头，往下找只会把正文里引用到的
// 别处标题当成这篇文档的标题。
func firstHeading(markdown string) string {
	markdown = dropUnclosedFrontMatterDelimiter(markdown)
	lines := strings.Split(markdown, "\n")
	for _, line := range lines[frontMatterRange(lines):] {
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
