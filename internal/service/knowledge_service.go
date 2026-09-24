package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"gorm.io/gorm"

	requestdto "narra/internal/model/dto/request"
	responsedto "narra/internal/model/dto/response"
	"narra/internal/model/entity"
	"narra/internal/rag"
)

const (
	// defaultPageSize / maxPageSize 列表分页的默认值与上限。
	defaultPageSize = 20
	maxPageSize     = 100
)

// documentQuerier 是本服务对文档存储的最小依赖面。
//
// 比 repository.KnowledgeDocumentRepository 窄：这份服务负责列表、详情、预览与删除，
// 状态推进和切片替换是收录链路（rag.DocumentStore）的事，这里看不见它们。
//
// ⚠️ 删除是其中一处例外：Delete 还没有写进这个接口，实现里靠类型断言去取（见 Delete 的注释）。
// 将来补进来，缺方法就能在编译期暴露，而不是等用户点了删除才报"存储不支持删除"。
type documentQuerier interface {
	// List 按创建时间倒序分页返回满足条件的文档，同时给出总数。
	// 条件为空时等价于"全部文档"，见 entity.KnowledgeDocumentQuery。
	List(ctx context.Context, query entity.KnowledgeDocumentQuery) ([]entity.KnowledgeDocument, int64, error)

	// CountActive 统计还在收录中的文档数（pending + processing），
	// 供上传入口判断后台忙不忙。仓储实现里这个查询走 status 的部分索引。
	CountActive(ctx context.Context) (int64, error)

	// GetByID 按主键取文档。查不到返回 gorm.ErrRecordNotFound。
	GetByID(ctx context.Context, id uint64) (*entity.KnowledgeDocument, error)

	// CountChunksByDocument 统计每篇文档的切片数，只返回入参里出现过的 ID。
	CountChunksByDocument(ctx context.Context, documentIDs []uint64) (map[uint64]int64, error)
}

// uploadRecordStore 是本服务对上传记录存储的最小依赖面。
//
// 比 repository.KnowledgeUploadRecordRepository 窄一个方法：Create 是收录链路的
// 入口（rag.UploadRecordStore），服务层只负责列表与删除，看不见它。
type uploadRecordStore interface {
	// List 按创建时间倒序分页返回上传记录，并带上关联文档的标题。
	List(ctx context.Context, offset, limit int) ([]entity.KnowledgeUploadRecordView, int64, error)

	// GetByID 按主键取一条记录。查不到返回 gorm.ErrRecordNotFound。
	GetByID(ctx context.Context, id uint64) (*entity.KnowledgeUploadRecord, error)

	// DeleteRecord 删掉一条记录；关联文档尚未收录成功时连同它一起删。
	DeleteRecord(ctx context.Context, id uint64) error
}

// ingester 是本服务对收录能力的最小依赖面。
//
// 与 mcp_server_service 里的 mcpRuntime 同一路数：服务层负责 HTTP 面（DTO 进、DTO 出），
// 收录链路本身归 internal/rag。定义接口而不直接依赖 *rag.Ingester，
// 是为了让服务层的测试不必真的走一遍切分与向量化。
type ingester interface {
	// IngestFile 同步收录一份文件：解析 → 切分 → 向量化 → 入库，返回时已是终态。
	IngestFile(ctx context.Context, input rag.FileInput) (rag.IngestResult, error)

	// IngestText 同步收录一段正文（没有解析这一步）。
	IngestText(ctx context.Context, input rag.TextInput) (rag.IngestResult, error)
}

// asyncIngester 是收录能力里"只建任务、不干活"的那一半，由 *rag.Ingester 提供。
//
// 单拆一个接口是异步化改造的遗留：它后加，并进 ingester 会要求所有已有的测试替身
// 再补一个方法，所以先用类型断言取（见 SubmitFile 与 documentQuerier 的说明）。
// 代价是这层保障从编译期退到运行期 —— 缺了方法要等用户上传时才报错。
type asyncIngester interface {
	// SubmitFile 建一条 pending 文档并记下待处理的文件路径，真正的处理由 rag.Worker 接手。
	SubmitFile(context.Context, rag.FileInput) (rag.IngestResult, error)
}

// fileRetrier 是收录能力里"把失败的任务重新入队"的那一半，由 *rag.Ingester 提供。
//
// 与 asyncIngester 同一个路数（类型断言而非并进 ingester）：它后加，并进去会要求
// 所有已有的测试替身再补一个方法。代价同样是这层保障从编译期退到运行期。
type fileRetrier interface {
	// Retry 把一行 failed 文档改回 pending 重新排队；stage 是按现实材料算出的恢复点。
	// 返回 false 表示它不满足重试条件。
	Retry(ctx context.Context, id uint64, stage string) (bool, error)
}

// retriever 是本服务对检索能力的最小依赖面。
//
// 与 ingester 同一个路数：服务层负责 HTTP 面（DTO 进、DTO 出），检索链路本身归
// internal/rag。它由 *rag.Retriever 提供，但**不是** ingester 的一部分 ——
// 收录与检索是两个对象、两种负载（一个吃上游额度，一个吃数据库），合在一起
// 会让"上传很慢"与"检索很慢"看起来像同一件事。
//
// 可以是 nil：检索能力没接上（或测试里不关心它）时服务照常提供收录与列表，
// 只有 Retrieve 会返回一句明确的"知识检索不可用"。与 parser 可以为 nil 同一个理由：
// 不因为一块能力缺位就拦住整个服务。
type retriever interface {
	// Retrieve 两路召回并融合出 top_k 条命中，见 rag.Retriever。
	Retrieve(ctx context.Context, input rag.RetrieveInput) (rag.RetrieveResult, error)
}

// ErrIngestBusy 表示知识库正在收录另一份文件，此刻不接受新的上传。
//
// 它是**可判定的**：接口层据此把"忙"翻译成 409，而不是和参数错误一起塞进 400 ——
// 两者的处置方式不同（等一会儿重试 vs. 改参数再试），给用户的话也不该一样。
var ErrIngestBusy = errors.New("已有文件正在收录，请等它处理完再上传")

// ErrRetryNotFailed 表示这份文档不是失败状态，不需要（也不能）重试。
//
// 与 ErrIngestBusy 同属"现在不行"这一类的可判定错误，接口层一并翻成 409。
var ErrRetryNotFailed = errors.New("这份文档不是失败状态，不需要重试")

// ErrRecoveryInputMissing 表示恢复所需的材料全都不在了：原件、正文、切片一个都没有。
//
// 它和上面两个的区别在处置方式：用户能做的是**重新上传**这份文件，
// 而不是等一会儿再点一次。所以文案里要把这句话说出来。
//
// 直接复用 rag 侧的哨兵：恢复点由 rag.ResolveRecoveryStage 计算，两边认的必须是同一个值。
var ErrRecoveryInputMissing = rag.ErrRecoveryInputMissing

// ErrEmptyQuery 表示检索词是空的。
//
// 在服务层拦一道而不是只靠 rag：接口层要按"参数错了"翻成 400，而它不该为了认一个
// 哨兵值去 import internal/rag（那是运行时模块，接口层只认服务层的错误口径）。
var ErrEmptyQuery = errors.New("检索词不能为空")

// SubmitFile 把一份文件交给后台收录，建好 pending 行就返回。
//
// 返回的文档是 pending、chunks 为 0：解析与向量化由 rag.Worker 接着做，
// 调用方拿 id 轮询 Get 看进度。
//
// **一次只收一份**，两道闸门各挡一半：进程内的锁挡住"两个请求同时到达"（否则两边
// 都会看到队列是空的，各建一条 pending 行）；库里的活跃行数挡住"已经在跑的任务"——
// 锁只在提交期间持有，之后的请求只能靠文档状态判断忙不忙。正文收录（IngestText）
// 不走这道闸门：它没有解析与排队，是秒级的同步链路。
func (s *knowledgeService) SubmitFile(ctx context.Context, input requestdto.KnowledgeIngestFile) (responsedto.KnowledgeDocument, error) {
	async, ok := s.ingester.(asyncIngester)
	if !ok {
		return responsedto.KnowledgeDocument{}, fmt.Errorf("知识库异步收录不可用")
	}

	if !s.ingestMu.TryLock() {
		return responsedto.KnowledgeDocument{}, ErrIngestBusy
	}
	defer s.ingestMu.Unlock()

	active, err := s.documents.CountActive(ctx)
	if err != nil {
		return responsedto.KnowledgeDocument{}, fmt.Errorf("检查收录队列失败: %w", err)
	}
	if active > 0 {
		return responsedto.KnowledgeDocument{}, ErrIngestBusy
	}

	result, err := async.SubmitFile(ctx, rag.FileInput{
		Path:       input.Path,
		Title:      input.Title,
		SourceType: input.SourceType,
		SourceURI:  input.SourceURI,
		SizeBytes:  input.SizeBytes,
	})
	return toDocumentResponse(result.Document, result.Chunks), err
}

// Retry 把一条收录失败的文档重新排队，让它再跑一遍。
//
// **原地重试**：复用同一行文档与同一条上传记录，不新建任何东西。起点不是失败时停在的
// 阶段，而是按现实材料算出来的恢复点（见 resolveRecoveryStage）：有切片就直接重新
// 向量化，切片没了但有正文就重新分块，正文也没了才重新解析原文件 —— 所以
// "向量服务故障、原件已被清掉"的文档仍然可以重试，不必重新上传。
//
// 三道检查按"最可能是哪个原因"排序，每一道都给出可判定的错误：
//   - 文档不存在 → 照常报"文档不存在"；
//   - 状态不是 failed → ErrRetryNotFailed（正在跑的不需要重试，ready 的更不需要）；
//   - 原件、正文、切片三者全都不在 → ErrRecoveryInputMissing（只能重新上传）。
//
// 恢复点那一道放在进闸门之前：它最多是一次磁盘探测与两次查询，不该和正在跑的收录抢那把锁。
//
// 闸门与 SubmitFile 是同一套，而且是必要的 —— 重试同样会占住后台的收录位。
// 两次检查之间那一行可能被别人重试或删掉，所以 Requeue 返回 false 时
// 仍然按"不需要重试"处理，而不是当成内部错误。
func (s *knowledgeService) Retry(ctx context.Context, id uint64) (responsedto.KnowledgeDocument, error) {
	document, err := s.documents.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return responsedto.KnowledgeDocument{}, fmt.Errorf("知识文档 %d 不存在", id)
		}
		return responsedto.KnowledgeDocument{}, fmt.Errorf("查询知识文档失败: %w", err)
	}
	if document.Status != entity.KnowledgeDocumentStatusFailed {
		return responsedto.KnowledgeDocument{}, ErrRetryNotFailed
	}
	stage, err := s.resolveRecoveryStage(ctx, document)
	if err != nil {
		return responsedto.KnowledgeDocument{}, err
	}

	if !s.ingestMu.TryLock() {
		return responsedto.KnowledgeDocument{}, ErrIngestBusy
	}
	defer s.ingestMu.Unlock()

	active, err := s.documents.CountActive(ctx)
	if err != nil {
		return responsedto.KnowledgeDocument{}, fmt.Errorf("检查收录队列失败: %w", err)
	}
	if active > 0 {
		return responsedto.KnowledgeDocument{}, ErrIngestBusy
	}

	retrier, ok := s.ingester.(fileRetrier)
	if !ok {
		return responsedto.KnowledgeDocument{}, fmt.Errorf("知识库异步收录不可用")
	}
	requeued, err := retrier.Retry(ctx, id, stage)
	if err != nil {
		return responsedto.KnowledgeDocument{}, fmt.Errorf("重新排队失败: %w", err)
	}
	if !requeued {
		return responsedto.KnowledgeDocument{}, ErrRetryNotFailed
	}

	// 回读一次再返回：上面那份文档还是 failed，被改成 pending 的是库里的行。
	// 走 Get 而不是自己拼响应，顺带把切片数也按同一个口径算出来。
	return s.Get(ctx, id)
}

// resolveRecoveryStage 按现实材料算重试从哪一步开始，规则见 rag.ResolveRecoveryStage。
//
// 材料三样：原始文件（还在服务器上）、正文（documents.content）、切片（已落库的数量）。
// 优先复用最靠后的产物 —— 切片还在就直接重新向量化，连切分都省了；正文还在就重新分块；
// 都没了才重新解析原文件。只有三者都不在才返回 ErrRecoveryInputMissing。
//
// 这里算出的阶段会写回行上（Requeue），Worker 开始处理前还会用同一个函数再算一次：
// 两次之间材料若又少了，Worker 会自己再退一步，而不是走进死胡同。
func (s *knowledgeService) resolveRecoveryStage(ctx context.Context, document *entity.KnowledgeDocument) (string, error) {
	counts, err := s.documents.CountChunksByDocument(ctx, []uint64{document.ID})
	if err != nil {
		return "", fmt.Errorf("统计已落库的切片失败: %w", err)
	}

	hasOriginal := false
	if path := metadataUploadPath(document.Metadata); path != "" && s.isUploadPath(path) {
		if _, err := os.Stat(path); err == nil {
			hasOriginal = true
		}
	}

	return rag.ResolveRecoveryStage(rag.RecoveryMaterial{
		HasOriginal: hasOriginal,
		HasContent:  strings.TrimSpace(document.Content) != "",
		HasChunks:   counts[document.ID] > 0,
	})
}

// knowledgeService 是 KnowledgeService 的实现。
//
// 它只做三件事：把 HTTP DTO 翻成收录链路的输入、把 entity 翻成 HTTP DTO、
// 分页查询文档。收录本身的编排（状态机、切分、向量化、一个事务落三张表）全在 internal/rag ——
// 这里是"面"，那里是"里"。
type knowledgeService struct {
	documents documentQuerier
	records   uploadRecordStore
	ingester  ingester
	retriever retriever
	uploadDir string

	// ingestMu 让"查活跃任务 + 建 pending 行"在单个进程内是原子的。
	// 它只在提交期间持有，不覆盖真正的收录（那跑在 rag.Worker 里、可能几分钟）——
	// 任务是否还在跑靠库里的活跃行数判断，见 SubmitFile。
	ingestMu sync.Mutex
}

var _ KnowledgeService = (*knowledgeService)(nil)

// NewKnowledgeService 创建知识库服务。
//
// retriever 可以传 nil（见 retriever 的说明）。uploadDir 是可选参数（早期调用点只传
// 两个依赖）：它必须与 controller、worker 用同一个值 —— 删除文档时靠它判断一条记录的
// upload_path 是否可信。不传（空串）时 isUploadPath 恒为 false，清理动作整体跳过，
// 删除功能不受影响。
func NewKnowledgeService(documents documentQuerier, records uploadRecordStore, ingester ingester, retriever retriever, uploadDirs ...string) KnowledgeService {
	uploadDir := ""
	if len(uploadDirs) > 0 {
		uploadDir = uploadDirs[0]
	}
	return &knowledgeService{
		documents: documents,
		records:   records,
		ingester:  ingester,
		retriever: retriever,
		uploadDir: uploadDir,
	}
}

// IngestFile 收录一份文件，并把结果翻成对外的文档结构。
//
// 失败时返回的响应体里也带着那份文档：它已经写进库了（status = failed，
// 失败阶段与原因在 metadata）。接口层现在只用错误信息，但这个值必须能拿到 ——
// 收录异步化之后，它就是查询一次上传进度的入口。
func (s *knowledgeService) IngestFile(
	ctx context.Context,
	input requestdto.KnowledgeIngestFile,
) (responsedto.KnowledgeDocument, error) {
	result, err := s.ingester.IngestFile(ctx, rag.FileInput{
		Path:       input.Path,
		Title:      input.Title,
		SourceType: input.SourceType,
		SourceURI:  input.SourceURI,
	})
	return toDocumentResponse(result.Document, result.Chunks), err
}

// IngestText 直接收录一段正文，跳过解析这一步。
func (s *knowledgeService) IngestText(
	ctx context.Context,
	input requestdto.KnowledgeIngestText,
) (responsedto.KnowledgeDocument, error) {
	result, err := s.ingester.IngestText(ctx, rag.TextInput{
		Title:      input.Title,
		Content:    input.Content,
		SourceType: input.SourceType,
		SourceURI:  input.SourceURI,
	})
	return toDocumentResponse(result.Document, result.Chunks), err
}

// NormalizePage 归一化分页参数：page 从 1 起，size 落在 [1, MaxPageSize]。
//
// 导出它是为了让接口层用同一套口径组装响应体。接口层如果按用户传来的原值回填
// "page / size"，用户请求 size=500 时会收到一个"每页 500 条"、实际只有 100 条的响应，
// 前端据此算出来的页码全是错的。
func NormalizePage(page, size int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = defaultPageSize
	}
	if size > maxPageSize {
		size = maxPageSize
	}
	return page, size
}

// ParseDocumentListQuery 把原始查询参数解析成列表条件。
//
// 归一化与校验都放服务层、接口层只负责把 query string 递进来，是为了让
// "页从 1 起、每页最多 100 条"和"status 只认那四个值"这两条口径只有一处实现 ——
// 与 NormalizePage 同一个理由。
//
// status 不合法时返回错误，而不是当作"不限"：静默忽略在界面上和"确实没有数据"
// 长得一模一样，排查时会白绕一圈。
func ParseDocumentListQuery(page, size int, status, keyword string) (requestdto.KnowledgeListQuery, error) {
	page, size = NormalizePage(page, size)
	statuses, err := parseDocumentStatuses(status)
	if err != nil {
		return requestdto.KnowledgeListQuery{}, err
	}
	return requestdto.KnowledgeListQuery{
		Page:     page,
		Size:     size,
		Statuses: statuses,
		Keyword:  strings.TrimSpace(keyword),
	}, nil
}

// parseDocumentStatuses 解析 status 参数，支持逗号分隔的多个状态。
//
// 允许给多个，是为了让"上传记录"（pending / processing / failed）一次问出来。
// 那是 documents 接口上的一个临时用法：批 ② 会把上传记录独立成自己的接口，
// 之后这里只剩主列表的单个 ready。
func parseDocumentStatuses(raw string) ([]string, error) {
	statuses := make([]string, 0, 2)
	for _, field := range strings.Split(raw, ",") {
		value := strings.TrimSpace(field)
		if value == "" {
			continue
		}
		if !isDocumentStatus(value) {
			return nil, fmt.Errorf("状态 %q 无效，可选值：%s、%s、%s、%s",
				value,
				entity.KnowledgeDocumentStatusPending,
				entity.KnowledgeDocumentStatusProcessing,
				entity.KnowledgeDocumentStatusReady,
				entity.KnowledgeDocumentStatusFailed)
		}
		if !slices.Contains(statuses, value) {
			statuses = append(statuses, value)
		}
	}
	return statuses, nil
}

// isDocumentStatus 判断一个取值是不是 knowledge_documents.status 的合法状态。
// 取值与实体 Status 字段上那条 CHECK 约束同源，改一处要改两处。
func isDocumentStatus(value string) bool {
	switch value {
	case entity.KnowledgeDocumentStatusPending,
		entity.KnowledgeDocumentStatusProcessing,
		entity.KnowledgeDocumentStatusReady,
		entity.KnowledgeDocumentStatusFailed:
		return true
	}
	return false
}

// List 分页返回满足条件的文档，并补齐每篇的切片数。
func (s *knowledgeService) List(ctx context.Context, query requestdto.KnowledgeListQuery) ([]responsedto.KnowledgeDocument, int64, error) {
	// page / size 再钳一次：List 是接口上的公开方法，调用方不一定走过 ParseDocumentListQuery。
	page, size := NormalizePage(query.Page, query.Size)

	documents, total, err := s.documents.List(ctx, entity.KnowledgeDocumentQuery{
		Statuses: query.Statuses,
		Keyword:  strings.TrimSpace(query.Keyword),
		Offset:   (page - 1) * size,
		Limit:    size,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("查询知识文档失败: %w", err)
	}

	ids := make([]uint64, len(documents))
	for index, document := range documents {
		ids[index] = document.ID
	}
	counts, err := s.documents.CountChunksByDocument(ctx, ids)
	if err != nil {
		return nil, 0, fmt.Errorf("统计切片数量失败: %w", err)
	}

	out := make([]responsedto.KnowledgeDocument, len(documents))
	for index, document := range documents {
		current := document
		out[index] = toDocumentResponse(&current, int(counts[current.ID]))
	}
	return out, total, nil
}

// Get 返回单篇文档。
func (s *knowledgeService) Get(ctx context.Context, id uint64) (responsedto.KnowledgeDocument, error) {
	document, err := s.documents.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return responsedto.KnowledgeDocument{}, fmt.Errorf("知识文档 %d 不存在", id)
		}
		return responsedto.KnowledgeDocument{}, fmt.Errorf("查询知识文档失败: %w", err)
	}

	counts, err := s.documents.CountChunksByDocument(ctx, []uint64{id})
	if err != nil {
		return responsedto.KnowledgeDocument{}, fmt.Errorf("统计切片数量失败: %w", err)
	}
	return toDocumentResponse(document, int(counts[id])), nil
}

// Preview 返回单篇文档的解析正文，供前端打开预览。
//
// 与 Get 的唯一差别是响应里带上 Content：正文可能有几十万字，只有这个接口需要它，
// 列表与详情刻意不带。
//
// Content 的可用性跟着收录阶段走：分阶段收录下，解析成功（stage 推进到 chunk）
// 正文就已经落库，所以一篇卡在 chunk / embed 阶段的 failed 文档也能预览到正文 ——
// 那是"解析产物是可信中间结果"的直接体现。只有还没解析的文档（pending / processing +
// parse 阶段，或解析本身失败）Content 才是空串。
func (s *knowledgeService) Preview(ctx context.Context, id uint64) (responsedto.KnowledgeDocumentPreview, error) {
	document, err := s.documents.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return responsedto.KnowledgeDocumentPreview{}, fmt.Errorf("知识文档 %d 不存在", id)
		}
		return responsedto.KnowledgeDocumentPreview{}, fmt.Errorf("查询知识文档失败: %w", err)
	}
	counts, err := s.documents.CountChunksByDocument(ctx, []uint64{id})
	if err != nil {
		return responsedto.KnowledgeDocumentPreview{}, fmt.Errorf("统计切片数量失败: %w", err)
	}
	return responsedto.KnowledgeDocumentPreview{KnowledgeDocument: toDocumentResponse(document, int(counts[id])), Content: document.Content}, nil
}

// Delete 删除一篇文档，连同它的切片与向量，并清理上传时的暂存目录。
//
// 先取一次文档再删，因为清理暂存目录要用 metadata 里的上传路径，顺序反过来就没得取了。
// 删除本身只有一条 DELETE：切片与向量由外键 ON DELETE CASCADE 带走，不用逐个删。
//
// 暂存目录要额外判断"是否落在自己管的上传目录内"：upload_path 来自 metadata，
// 没有任何东西约束它的取值，构造一条记录就能指向任意路径 ——
// 少了这道判断，等于把"删除任意目录"的能力交给了能写这张表的人。
//
// ⚠️ 已知短板：存储的 Delete 目前靠类型断言取（见 documentQuerier 的注释），
// 且目录清理失败会被静默忽略（内容已经进库，残留只是占磁盘，但排查时看不到痕迹）。
func (s *knowledgeService) Delete(ctx context.Context, id uint64) error {
	document, err := s.documents.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("知识文档 %d 不存在", id)
		}
		return fmt.Errorf("查询知识文档失败: %w", err)
	}
	deleter, ok := s.documents.(interface {
		Delete(context.Context, uint64) error
	})
	if !ok {
		return fmt.Errorf("知识库存储不支持删除")
	}
	if err := deleter.Delete(ctx, id); err != nil {
		return fmt.Errorf("删除知识文档失败: %w", err)
	}
	if path := metadataUploadPath(document.Metadata); path != "" && s.removableStagingDir(path) {
		_ = os.RemoveAll(filepath.Dir(path))
	}
	return nil
}

// Retrieve 检索知识库，返回最相关的切片。
//
// 这是 MCP 契约 rag_retrieve 的进程内入口（见 docs/modules/agent-mcp-tools.md）：
// { query, top_k } → { results: [{content, source, score}] }。本层多给几列
// （标题、章节、命中方式、相似度）方便界面显示与调参，MCP 适配层按需取用即可。
//
// 检索词在这里校验而不是全丢给 rag：接口层要按"参数错了"翻 400，而它只认服务层的
// 错误口径（见 ErrEmptyQuery）。rag 那边同样有一道，供不经服务层的调用方（MCP）兜底。
func (s *knowledgeService) Retrieve(
	ctx context.Context,
	input requestdto.KnowledgeRetrieve,
) (responsedto.KnowledgeRetrieveResult, error) {
	if s.retriever == nil {
		return responsedto.KnowledgeRetrieveResult{}, fmt.Errorf("知识检索不可用")
	}
	if strings.TrimSpace(input.Query) == "" {
		return responsedto.KnowledgeRetrieveResult{}, ErrEmptyQuery
	}

	result, err := s.retriever.Retrieve(ctx, rag.RetrieveInput{Text: input.Query, TopK: input.TopK})
	if err != nil {
		return responsedto.KnowledgeRetrieveResult{}, err
	}

	hits := make([]responsedto.KnowledgeHit, 0, len(result.Hits))
	for _, hit := range result.Hits {
		hits = append(hits, toHitResponse(hit))
	}
	return responsedto.KnowledgeRetrieveResult{
		Model:   result.Model,
		Terms:   result.Terms,
		Results: hits,
	}, nil
}

// ListUploadRecords 分页返回上传记录 —— 文件投递的历史流水，含已经收录成功的那些。
//
// 它与 List 是两份不同的东西：List 列的是**资产**（能参与检索的文档），
// 这里列的是**动作**（谁在什么时候投了什么文件、成没成）。同一次上传在两边各出现一次，
// 但它们会分开消失 —— 删掉一条记录不该动那份知识，删掉一份知识也不会让这次投递
// 从历史里消失（那行会变成"已收录后删除"）。
func (s *knowledgeService) ListUploadRecords(ctx context.Context, page, size int) ([]responsedto.KnowledgeUploadRecord, int64, error) {
	// 与 List 同一个理由再钳一次：它是接口上的公开方法，调用方不一定走过归一化。
	page, size = NormalizePage(page, size)

	views, total, err := s.records.List(ctx, (page-1)*size, size)
	if err != nil {
		return nil, 0, fmt.Errorf("查询上传记录失败: %w", err)
	}

	out := make([]responsedto.KnowledgeUploadRecord, len(views))
	for index, view := range views {
		out[index] = toUploadRecordResponse(view)
	}
	return out, total, nil
}

// DeleteUploadRecord 删除一条上传记录；关联文档尚未收录成功时把它一起删掉。
//
// 两条顺序上的讲究：
//
//  1. 先把文档的暂存路径读出来再删。删除会连带删掉文档行，之后 metadata 就取不到了，
//     而暂存文件留在磁盘上没有人会再清理它。
//  2. 文档可能已经不存在（记录还在、成果被删了）。那不是错误，是"已收录后删除"
//     这条历史正在被清理，ErrRecordNotFound 直接跳过即可。
//
// 连带删除只发生在文档未收录成功时（pending / processing / failed），判定在仓储里：
// 未就绪的行正是上传闸门 CountActive 的输入，留着它会让"一次只收一份"永久返回 409，
// 而记录删掉之后用户在界面上再也没有入口能清掉它。已 ready 的文档绝不触碰。
func (s *knowledgeService) DeleteUploadRecord(ctx context.Context, id uint64) error {
	record, err := s.records.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("上传记录 %d 不存在", id)
		}
		return fmt.Errorf("查询上传记录失败: %w", err)
	}

	stagedPath := ""
	if record.DocumentID != nil {
		if document, err := s.documents.GetByID(ctx, *record.DocumentID); err == nil {
			stagedPath = metadataUploadPath(document.Metadata)
		}
	}

	if err := s.records.DeleteRecord(ctx, id); err != nil {
		return fmt.Errorf("删除上传记录失败: %w", err)
	}

	if stagedPath != "" && s.removableStagingDir(stagedPath) {
		_ = os.RemoveAll(filepath.Dir(stagedPath))
	}
	return nil
}

// toUploadRecordResponse 把记录（含联表带来的文档标题）翻成对外结构。
//
// 标题回落放在这里而不是写成 SQL 的 COALESCE：COALESCE 能少一次判断，
// 但"文档没了就看原始文件名"是展示语义，埋进 SQL 之后只有读仓储的人才知道它存在。
func toUploadRecordResponse(view entity.KnowledgeUploadRecordView) responsedto.KnowledgeUploadRecord {
	title := strings.TrimSpace(view.DocumentTitle)
	if title == "" {
		title = view.OriginalName
	}

	out := responsedto.KnowledgeUploadRecord{
		ID:           view.ID,
		DocumentID:   view.DocumentID,
		Title:        title,
		OriginalName: view.OriginalName,
		SizeBytes:    view.SizeBytes,
		Status:       view.Status,
		CreatedAt:    view.CreatedAt,
		UpdatedAt:    view.UpdatedAt,
	}
	if view.ErrorMessage != nil {
		out.Error = *view.ErrorMessage
	}
	if view.DocumentIngestStage != nil {
		out.Stage = *view.DocumentIngestStage
	}
	if view.DocumentFailedStage != nil {
		out.FailedStage = *view.DocumentFailedStage
	}
	return out
}

// removableStagingDir 判断一条 upload_path 是否指向我们管理的暂存文件 ——
// 也就是"它的父目录可以安全删掉"。
//
// 与 isUploadPath 的区别在严格程度。那个只要求路径落在上传根目录内（用于读，
// 例如重试前确认原件还在）；而清理动作删的是 filepath.Dir(path)，
// 所以这里必须要求形态精确：文件恰好比根目录深两层
// （<root>/pending/<一层>/<文件> 或 <root>/failed/<一层>/<文件>）。
// 放宽的后果很具体：如果 metadata 里是 <root>/upload.md，Dir 就是 root 本身，
// RemoveAll 会把整棵上传目录清空 —— 包括其他正在处理的原件。
func (s *knowledgeService) removableStagingDir(path string) bool {
	if strings.TrimSpace(s.uploadDir) == "" {
		// 没配上上传目录（早期调用点）时一律不清理，避免按 cwd 误判。
		return false
	}
	root, err := filepath.Abs(s.uploadDir)
	if err != nil {
		return false
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return len(strings.Split(rel, string(os.PathSeparator))) == 3
}

// metadataUploadPath 从 metadata 里取上传时的暂存文件路径，读不到返回空串。
func metadataUploadPath(raw json.RawMessage) string {
	var metadata map[string]any
	if json.Unmarshal(raw, &metadata) != nil {
		return ""
	}
	path, _ := metadata["upload_path"].(string)
	return strings.TrimSpace(path)
}

// isUploadPath 判断路径是否落在本服务配置的上传目录之内。
//
// 与 worker 的 isUnderRoot 是同一段容器判定：先取绝对路径再比相对路径，
// 免得被 ../ 绕过、也免得把同前缀的兄弟目录算进来。两处没有合并，是因为
// 分别属于服务层与运行时模块、各自持有一份配置；但两边必须用同一个 uploadDir，
// 否则"上传时记录在 A 目录、删除时用 B 目录判断"，清理会静默失效。
func (s *knowledgeService) isUploadPath(path string) bool {
	if strings.TrimSpace(s.uploadDir) == "" {
		return false
	}
	root, err := filepath.Abs(s.uploadDir)
	if err != nil {
		return false
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// toDocumentResponse 把实体翻成对外结构。
//
// 正文只用来算字符数，不进响应体：一篇文档的正文可能有几十万字，列表和详情带上它
// 会让响应大到没有必要。唯一需要正文的是 Preview，由它自己填 Content。
//
// document 为 nil 时返回零值：收录链路在"连文档行都没建起来"的情况下会返回空结果
// （例如路径为空、来源类型非法），那种时候响应体没有内容可填，但调用方拿到的
// 错误信息是完整的。
// toHitResponse 把一条命中翻成对外结构。
//
// source 取来源标识（原始文件名等），没有来源标识的手工录入文档回落到标题 ——
// MCP 契约里这一列回答的是"这条内容从哪来"，空着就等于没回答。
func toHitResponse(hit rag.Hit) responsedto.KnowledgeHit {
	source := hit.SourceURI
	if source == "" {
		source = hit.DocumentTitle
	}
	return responsedto.KnowledgeHit{
		ChunkID:    hit.ChunkID,
		DocumentID: hit.DocumentID,
		Title:      hit.DocumentTitle,
		ChunkIndex: hit.ChunkIndex,
		Heading:    hit.Heading,
		Content:    hit.Content,
		Source:     source,
		Score:      hit.Score,
		Similarity: hit.Similarity,
		Method:     hit.Method,
	}
}

func toDocumentResponse(document *entity.KnowledgeDocument, chunks int) responsedto.KnowledgeDocument {
	if document == nil {
		return responsedto.KnowledgeDocument{}
	}

	out := responsedto.KnowledgeDocument{
		ID:          document.ID,
		Title:       document.Title,
		SourceType:  document.SourceType,
		Enabled:     document.Enabled,
		Status:      document.Status,
		Stage:       stageName(document.IngestStage),
		FailedStage: failureStage(document.Metadata),
		Parser:      parserName(document.Metadata),
		Error:       failureReason(document.Metadata),
		Chunks:      chunks,
		Characters:  len([]rune(document.Content)),
		CreatedAt:   document.CreatedAt,
		UpdatedAt:   document.UpdatedAt,
	}
	if document.SourceURI != nil {
		out.SourceURI = *document.SourceURI
	}
	return out
}

// stageName 取文档的收录阶段。ready 文档没有下一步（列是 NULL），返回空串；
// 响应里该字段 omitempty，前端会整列隐藏。
func stageName(stage *string) string {
	if stage == nil {
		return ""
	}
	return *stage
}

// parserName 从 metadata 里取本次解析用的解析器身份，没有记录时返回空串
// （响应体里该字段 omitempty，前端会整列隐藏）。
func parserName(metadata json.RawMessage) string {
	if len(metadata) == 0 {
		return ""
	}
	var payload struct {
		Parser string `json:"parser"`
	}
	if err := json.Unmarshal(metadata, &payload); err != nil {
		return ""
	}
	return payload.Parser
}

// failureStage 从 metadata 里取出失败卡在哪一步（细粒度：select_parser / parse /
// chunk / model / vector / store / worker / status）。
//
// 它与粗粒度的 ingest_stage 分工不同：阶段回答"失败后从哪一步恢复"，这里回答
// "具体是哪一环失败" —— 两者都带出去，前端才不会把"向量已算好、写库失败"
// 误说成"向量化失败"。
func failureStage(metadata json.RawMessage) string {
	if len(metadata) == 0 {
		return ""
	}
	var payload struct {
		Stage string `json:"stage"`
	}
	if err := json.Unmarshal(metadata, &payload); err != nil {
		return ""
	}
	return payload.Stage
}

// failureReason 从 metadata 里取出失败原因。
//
// 只认 error 这一个键：metadata 的完整结构会随实现变化，前端不该依赖它，
// 它需要的只是一句能显示给用户的话 —— 而 error 里放的就是这样一句话（纯中文）。
// 完整诊断在 error_detail 键里，那是给排障看的，两者别对调。
func failureReason(metadata json.RawMessage) string {
	if len(metadata) == 0 {
		return ""
	}
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(metadata, &payload); err != nil {
		return ""
	}
	return payload.Error
}
