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
// 写入口刻意开得很窄，只有 Create / MarkProcessing / MarkFailed / ReplaceChunks 四个，
// 而且状态推进是分开的方法而不是一个通用的 Update。原因是这张表的状态机是有方向的：
// pending → processing → ready / failed。开放一个接受任意字段的 Update，
// 就等于把"状态可以随便改"的口子留在仓储层，而状态机和它带来的约束
// （ready 必须有正文、slice 必须成组写入）是数据库层面的约定，不该由调用方自觉遵守。
//
// 三张表之间是 ON DELETE CASCADE（切片随原文、向量随切片），删除只用删最外层一行。
type KnowledgeDocumentRepository interface {
	// Create 插入一篇文档，通常是 pending 状态、正文待填。
	// 落库后 document.ID 会被回填，后续的切片与向量都要挂在它下面。
	Create(ctx context.Context, document *entity.KnowledgeDocument) error

	// GetByID 按主键取文档。查不到返回 gorm.ErrRecordNotFound。
	GetByID(ctx context.Context, id uint64) (*entity.KnowledgeDocument, error)

	// List 按创建时间倒序分页返回文档，同时给出总数。
	List(ctx context.Context, offset, limit int) ([]entity.KnowledgeDocument, int64, error)

	// CountChunksByDocument 统计每篇文档的切片数，只返回入参里出现过的 ID。
	// 列表页要靠它显示"这篇文档被切成了多少片"，而逐篇去 count 会变成 N+1 次查询。
	CountChunksByDocument(ctx context.Context, documentIDs []uint64) (map[uint64]int64, error)

	// MarkProcessing 把文档推进到 processing，表示后台正在解析或向量化。
	MarkProcessing(ctx context.Context, id uint64) error

	// MarkFailed 把文档推进到 failed，并把失败现场写进 metadata。
	// metadata 必须是一份完整的 JSON 对象（可以带阶段、原因、时间），
	// 仓储不负责和旧值合并 —— 合并规则属于业务语义。
	MarkFailed(ctx context.Context, id uint64, metadata json.RawMessage) error

	// ReplaceChunks 用一个事务完成"换掉这篇文档的全部切片与向量，并把文档标记为可检索"。
	//
	// 它必须是原子的，这是 entity.KnowledgeDocument 那句"更新原文后应替换其全部切片"
	// 的实现方式：只要中间任何一步失败，旧的切片和旧的正文就都还在，
	// 不会出现"正文换了、切片还是旧的"这种检索结果与原文对不上的状态。
	ReplaceChunks(ctx context.Context, id uint64, input entity.ChunkReplacement) error
	SetMetadata(ctx context.Context, id uint64, metadata json.RawMessage) error
	ListPending(ctx context.Context, limit int) ([]entity.KnowledgeDocument, error)
	Claim(ctx context.Context, id uint64) (bool, error)
	ResetStale(ctx context.Context, olderThan time.Time) error
	Delete(ctx context.Context, id uint64) error
}
