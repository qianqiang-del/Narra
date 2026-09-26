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
//   - 后台任务队列（rag.FileTaskStore）：SetMetadata / SetUploadPath / FailedLeaseOwned /
//     ListPending / ClaimAndReturnAttempt / Touch / ResetStale / Requeue / MarkFailed /
//     SaveParsedContent / ReplaceStagedChunks / ListChunksByDocument / SaveEmbeddingsAndMarkReady /
//     CountActive / AcquireIngestQueueLock
//   - 查询与删除（service）：List / GetByID / CountChunksByDocument / Delete
//
// MarkFailed 被前两组共用，所以它在两处都出现。分阶段写入的四个方法只服务异步文件链路
// （Worker 按 ingest_stage 恢复）；同步链路（IngestText / IngestFile）仍走 ReplaceChunks，
// 用一次事务把正文、切片、向量与 ready 一起写完。
//
// 三张表之间是 ON DELETE CASCADE（切片随原文、向量随切片），删除只用删最外层一行。
type KnowledgeDocumentRepository interface {
	// Create 插入一篇文档，通常是 pending 状态、正文待填。
	// 落库后 document.ID 会被回填，后续的切片与向量都要挂在它下面。
	Create(ctx context.Context, document *entity.KnowledgeDocument) error

	// GetByID 按主键取文档。查不到返回 gorm.ErrRecordNotFound。
	GetByID(ctx context.Context, id uint64) (*entity.KnowledgeDocument, error)

	// SetEnabled 单独切换一篇文档的检索开关，返回是否命中一行（false = 文档不存在）。
	//
	// 只改 enabled 一列：收录状态、切片与向量都不动 —— 停用只是让召回 SQL 过滤掉它，
	// 改回 true 立即恢复。updated_at 由 GORM 的 autoUpdateTime 跟着刷新。
	SetEnabled(ctx context.Context, id uint64, enabled bool) (applied bool, err error)

	// List 按创建时间倒序分页返回满足条件的文档，同时给出总数。
	// 条件为空时等价于"全部文档"，见 entity.KnowledgeDocumentQuery。
	List(ctx context.Context, query entity.KnowledgeDocumentQuery) ([]entity.KnowledgeDocument, int64, error)

	// CountActive 统计还在收录中的文档数（pending + processing）。
	//
	// 它是队列容量检查的一半：收录链路的提交与重试在事务里先取
	// AcquireIngestQueueLock 再调它，达到 knowledge_ingest.queue_capacity 时拒绝新任务。
	// failed 与 ready 都不算 —— 一份失败的上传不该把知识库永久锁住。
	CountActive(ctx context.Context) (int64, error)

	// AcquireIngestQueueLock 在**当前事务**里取得收录队列容量检查的排他锁
	// （pg_advisory_xact_lock）。必须在事务内调用：锁随事务提交/回滚自动释放。
	// 它让"计数 + 建行"在多实例部署下串行，队列容量因此是硬上限。
	AcquireIngestQueueLock(ctx context.Context) error

	// CountChunksByDocument 统计每篇文档的切片数，只返回入参里出现过的 ID。
	// 列表页要靠它显示"这篇文档被切成了多少片"，而逐篇去 count 会变成 N+1 次查询。
	CountChunksByDocument(ctx context.Context, documentIDs []uint64) (map[uint64]int64, error)

	// MarkProcessing 把文档推进到 processing，表示后台正在解析或向量化。
	MarkProcessing(ctx context.Context, id uint64) error

	// MarkFailed 把文档推进到 failed，把失败现场**合并**进 metadata，
	// 并把原因同步到这次上传的记录上（两处在同一个事务里，见实现）。
	//
	// attempt 是这次处理的租约编号（同步链路传 0，它没有认领这一步）。只有仍在 processing
	// 且编号相符的文档才会被写入：行已被删除、已被回收并重新认领时，影响 0 行并返回
	// applied = false —— 调用方据此判断"这次失败没能落库"，绝不能移动磁盘上的原件
	// （旧执行者把新一轮正在用的文件挪走，会把新任务一起打断）。
	// 数据库层面的错误以 err 返回，此时 applied 也是 false。
	//
	// metadata 是一份 JSON 对象（阶段、原因、时间），合并时只覆盖同名的键：
	// upload_path 与 explicit_title 这些收录链路的输入会原样留着 ——
	// 失败原件的归档、删除时的清理、以及重试都要靠它们。见实现的 mergeMetadata。
	// reason 是给用户看的那一句中文（界面直接显示它），为空时记录的失败原因清空。
	MarkFailed(ctx context.Context, id uint64, attempt int32, metadata json.RawMessage, reason string) (applied bool, err error)

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

	// SetUploadPath 只替换 metadata 里的 upload_path，其余键原样保留。归档时 metadata 里
	// 已经有 MarkFailed 写下的失败现场，整份覆盖会把失败原因抹掉。
	//
	// 只处理仍由这次失败持有的行（id、status = failed、ingest_attempt 三者相符）：
	// 返回 applied = false 表示用户已经重试或任务已被重新认领，指针不能改 ——
	// 调用方据此把已挪走的文件挪回原位（见 rag.Worker.archiveStagedFile）。
	SetUploadPath(ctx context.Context, id uint64, attempt int32, path string) (applied bool, err error)

	// FailedLeaseOwned 判断这一行是否仍由这次失败持有（status = failed 且租约编号相符）。
	// 归档原件之前用它做最后一道核对：不匹配就说明已经有人重试或接手，
	// 旧 Worker 必须停止，不能再碰磁盘上的文件。
	FailedLeaseOwned(ctx context.Context, id uint64, attempt int32) (bool, error)

	// ListPending 按创建时间取最多 limit 条 pending 文档，供后台任务队列取任务。
	// 它只是查询，不代表这些任务已经被抢到 —— 并发执行者之间靠 Claim 决出胜负。
	ListPending(ctx context.Context, limit int) ([]entity.KnowledgeDocument, error)

	// ClaimAndReturnAttempt 用一条带 status = 'pending' 条件的 UPDATE 把文档抢成
	// processing，并把 ingest_attempt 原子递增，返回递增后的编号（本次处理的租约编号）。
	// claimed 为 false 表示这条已经被别的执行者抢走了，调用方应当跳过。
	// 条件写在 UPDATE 的 WHERE 里而不是"先查再改"，是为了让并发下的取舍由数据库一次性决定。
	//
	// 递增必须和抢占在同一条语句里完成：编号就是"谁在跑"的凭据，分两步写会留下
	// 两个执行者拿到同一个编号的窗口，租约也就形同虚设。
	ClaimAndReturnAttempt(ctx context.Context, id uint64) (attempt int32, claimed bool, err error)

	// ResetStale 把 updated_at 早于 olderThan 且仍在 processing 的文档打回 pending。
	// 用于回收僵尸任务：进程在处理中退出后，那些行没有任何人会再碰。
	// Worker 在启动时与轮询循环里周期调用它（阈值与心跳间隔配套，见实现的 Touch）。
	// 它不改 ingest_attempt —— 行被打回 pending 之后，旧租约的写入已经过不了
	// status 条件；重新认领时编号还会再递增一次。
	ResetStale(ctx context.Context, olderThan time.Time) error

	// Touch 只把 processing 文档的 updated_at 推到当前时刻，作为任务心跳。
	// 它不参与状态机：行不是 processing、或租约编号已经对不上时影响 0 行，不报错。
	//
	// 编号是必须的：旧租约的僵尸心跳若还能推时间，一个已经死掉的新任务会被
	// 一直"续命"，周期回收永远等不到它。
	Touch(ctx context.Context, id uint64, attempt int32) error

	// Requeue 把一行 failed 文档改回 pending 重新排队，并清掉上一次的失败现场，
	// 同一个事务里把上传记录也置回 pending（见实现）。这是"原地重试"的写入口。
	//
	// stage 是调用方按现实材料算出的恢复点（见 rag.ResolveRecoveryStage）：重试不该死守
	// 原来的阶段 —— 切片没了就退回正文，正文没了就退回原文件，都没有才会在计算时被拒。
	//
	// 返回 false 表示这一行不满足条件（不存在，或状态已经不是 failed），
	// 调用方据此报"不需要重试" —— 不把它当成错误，是因为并发点两次重试时
	// 后到的那次本来就该安静地输掉。
	Requeue(ctx context.Context, id uint64, stage string) (bool, error)

	// SaveParsedContent 保存解析产物，并把 ingest_stage 推进到 chunk ——
	// 正文与阶段必须在同一个事务里改：只写正文不推阶段会让恢复重新解析（浪费但安全），
	// 只推阶段不写正文会让恢复读到空正文（数据丢失，不可接受）。
	//
	// metadata 只做顶层合并，upload_path 与 explicit_title 必须原样保留到成功清理原文件为止 ——
	// 进程崩溃后靠它找回原件。只处理仍在 processing 且租约编号相符的文档。
	SaveParsedContent(ctx context.Context, id uint64, attempt int32, input entity.ParsedContent) error

	// ReplaceStagedChunks 换掉这篇文档的全部切片，并把 ingest_stage 推进到 embed。
	// 旧切片先删（硬删除，外键级联带走旧向量），新切片在同一事务里写入。
	// 只有正文已经落库（stage = chunk）的文档才会走到这里，所以不写 content。
	// 同样只处理仍在 processing 且租约编号相符的文档。
	ReplaceStagedChunks(ctx context.Context, id uint64, attempt int32, chunks []entity.KnowledgeChunk, metadata json.RawMessage) error

	// ListChunksByDocument 按 chunk_index 升序取回一篇文档的全部切片，供 embed 阶段
	// 从库里恢复输入（不再读原文件）。返回顺序就是向量与切片的对应顺序。
	ListChunksByDocument(ctx context.Context, id uint64) ([]entity.KnowledgeChunk, error)

	// SaveEmbeddingsAndMarkReady 写入向量并把文档置为 ready、ingest_stage 置 NULL。
	// 它必须是一个事务：只有全部向量写成功，文档才能 ready —— 否则会出现一篇
	// "可检索但缺向量"的文档，界面上看不出任何异常，检索却永远漏掉它。
	//
	// embeddings 的 ChunkID 必须属于这篇文档，数量也必须与当前切片数一致，
	// 否则整个事务回滚。重复执行为幂等：先清掉这些切片的旧向量再写。
	// 同一个事务里把上传记录也置为 ready（手动录入没有记录，匹配 0 行无害）。
	SaveEmbeddingsAndMarkReady(ctx context.Context, id uint64, attempt int32, embeddings []entity.KnowledgeEmbedding, metadata json.RawMessage) error

	// Delete 删除一篇文档。切片与向量不在这里删 —— 外键 ON DELETE CASCADE 会把它们带走。
	// 硬删除，不走软删除：UNIQUE (document_id, chunk_index) 要求同序号的上一条先消失。
	Delete(ctx context.Context, id uint64) error
}
