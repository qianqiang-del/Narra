package repository

import (
	"context"
	"encoding/json"
	"time"

	"narra/internal/model/entity"
)

// KnowledgeDocumentRepository 负责知识库三张表的持久化：
// knowledge_documents（原文）、knowledge_chunks（切片）、knowledge_embeddings（向量）。
//
// 写入口刻意开得很窄，而且状态推进是分开的方法而不是一个通用的 Update。
// 原因是这张表的状态机是有方向的：pending → processing → ready / failed，
// 开放一个接受任意字段的 Update，就等于把"状态可以随便改"的口子留在仓储层，
// 而状态机和它带来的约束（ready 必须有正文、切片必须成组写入）是数据库层面的约定，
// 不该由调用方自觉遵守。所以方法一律按角色命名，调用方也只能按角色改。
//
// 按消费方分三组：
//   - 收录链路（rag.DocumentStore）：Create / GetByID / MarkProcessing / MarkFailed / ReplaceChunks
//   - 后台任务队列（rag.FileTaskStore）：SetMetadata / SetUploadPath / ListPending / Claim / Touch / ResetStale / Requeue / MarkFailed
//   - 查询与删除（service）：List / GetByID / CountChunksByDocument / Delete
//
// MarkFailed 被前两组共用，所以它在两处都出现。
//
// 三张表之间是 ON DELETE CASCADE（切片随原文、向量随切片），删除只用删最外层一行。
type KnowledgeDocumentRepository interface {
	// Create 插入一篇文档，通常是 pending 状态、正文待填。
	// 落库后 document.ID 会被回填，后续的切片与向量都要挂在它下面。
	Create(ctx context.Context, document *entity.KnowledgeDocument) error

	// GetByID 按主键取文档。查不到返回 gorm.ErrRecordNotFound。
	GetByID(ctx context.Context, id uint64) (*entity.KnowledgeDocument, error)

	// List 按创建时间倒序分页返回满足条件的文档，同时给出总数。
	// 条件为空时等价于"全部文档"，见 entity.KnowledgeDocumentQuery。
	List(ctx context.Context, query entity.KnowledgeDocumentQuery) ([]entity.KnowledgeDocument, int64, error)

	// CountActive 统计还在收录中的文档数（pending + processing）。
	//
	// 上传入口用它做"一次只收一份"的并发约束：大于 0 就说明后台还在忙。
	// failed 与 ready 都不算 —— 一份失败的上传不该把知识库永久锁住。
	CountActive(ctx context.Context) (int64, error)

	// CountChunksByDocument 统计每篇文档的切片数，只返回入参里出现过的 ID。
	// 列表页要靠它显示"这篇文档被切成了多少片"，而逐篇去 count 会变成 N+1 次查询。
	CountChunksByDocument(ctx context.Context, documentIDs []uint64) (map[uint64]int64, error)

	// MarkProcessing 把文档推进到 processing，表示后台正在解析或向量化。
	MarkProcessing(ctx context.Context, id uint64) error

	// MarkFailed 把文档推进到 failed，把失败现场**合并**进 metadata，
	// 并把原因同步到这次上传的记录上（两处在同一个事务里，见实现）。
	//
	// 只处理仍在 processing 的文档：行已被删除、或已被推进到别的状态时，
	// 整体回滚并返回错误，不写任何失败现场。调用方据此判断"这次失败没能落库"。
	//
	// metadata 是一份 JSON 对象（阶段、原因、时间），合并时只覆盖同名的键：
	// upload_path 与 explicit_title 这些收录链路的输入会原样留着 ——
	// 失败原件的归档、删除时的清理、以及重试都要靠它们。见实现的 mergeMetadata。
	// reason 是给用户看的那一句中文（界面直接显示它），为空时记录的失败原因清空。
	MarkFailed(ctx context.Context, id uint64, metadata json.RawMessage, reason string) error

	// ReplaceChunks 用一个事务完成"换掉这篇文档的全部切片与向量，并把文档标记为可检索"。
	//
	// 它必须是原子的，这是 entity.KnowledgeDocument 那句"更新原文后应替换其全部切片"
	// 的实现方式：只要中间任何一步失败，旧的切片和旧的正文就都还在，
	// 不会出现"正文换了、切片还是旧的"这种检索结果与原文对不上的状态。
	//
	// 同样只处理仍在 processing 的文档：文档在处理期间被删除时，
	// 最后的 UPDATE 影响 0 行，整个事务回滚（切片也不会写进去）并返回错误。
	ReplaceChunks(ctx context.Context, id uint64, input entity.ChunkReplacement) error

	// SetMetadata 整份覆盖文档的 metadata。上传链路用它记下暂存文件路径与标题回落标记；
	// 覆盖的规则由调用方决定（这里只负责写）。
	SetMetadata(ctx context.Context, id uint64, metadata json.RawMessage) error

	// SetUploadPath 只替换 metadata 里的 upload_path，其余键原样保留。
	// 失败原件归档到 failed/<文档ID>/ 之后用它把指针挪过去 —— 这时 metadata 里
	// 已经有 MarkFailed 写下的失败现场，整份覆盖会把失败原因抹掉。
	SetUploadPath(ctx context.Context, id uint64, path string) error

	// ListPending 按创建时间取最多 limit 条 pending 文档，供后台任务队列取任务。
	// 它只是查询，不代表这些任务已经被抢到 —— 并发执行者之间靠 Claim 决出胜负。
	ListPending(ctx context.Context, limit int) ([]entity.KnowledgeDocument, error)

	// Claim 用一条带 status = 'pending' 条件的 UPDATE 把文档抢成 processing。
	// 返回 false 表示这条已经被别的执行者抢走了，调用方应当跳过。
	// 条件写在 UPDATE 的 WHERE 里而不是"先查再改"，是为了让并发下的取舍由数据库一次性决定。
	Claim(ctx context.Context, id uint64) (bool, error)

	// ResetStale 把 updated_at 早于 olderThan 且仍在 processing 的文档打回 pending。
	// 用于回收僵尸任务：进程在处理中退出后，那些行没有任何人会再碰。
	// Worker 在启动时与轮询循环里周期调用它（阈值与心跳间隔配套，见实现的 Touch）。
	ResetStale(ctx context.Context, olderThan time.Time) error

	// Touch 只把 processing 文档的 updated_at 推到当前时刻，作为任务心跳。
	// 它不参与状态机：行不是 processing（被删、已 ready、已被回收）时影响 0 行，不报错。
	Touch(ctx context.Context, id uint64) error

	// Requeue 把一行 failed 文档改回 pending 重新排队，并清掉上一次的失败现场，
	// 同一个事务里把上传记录也置回 pending（见实现）。这是"原地重试"的写入口。
	//
	// 返回 false 表示这一行不满足条件（不存在，或状态已经不是 failed），
	// 调用方据此报"不需要重试" —— 不把它当成错误，是因为并发点两次重试时
	// 后到的那次本来就该安静地输掉。
	Requeue(ctx context.Context, id uint64) (bool, error)

	// Delete 删除一篇文档。切片与向量不在这里删 —— 外键 ON DELETE CASCADE 会把它们带走。
	// 硬删除，不走软删除：UNIQUE (document_id, chunk_index) 要求同序号的上一条先消失。
	Delete(ctx context.Context, id uint64) error
}
