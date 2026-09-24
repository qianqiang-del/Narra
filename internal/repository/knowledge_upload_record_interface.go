package repository

import (
	"context"

	"narra/internal/model/entity"
)

// KnowledgeUploadRecordRepository 负责上传记录表的持久化。
//
// 与 KnowledgeDocumentRepository 分成两个仓储，是因为两张表的生命周期与消费方都不同：
// 文档是资产（正文、切片、向量的父表），记录是投递流水（只有历史意义）。
//
// 状态同步刻意不在这里开口子：记录的 ready / failed 必须和文档的状态变更在同一个事务里
// 落地，否则会出现"文档已经 ready、记录还停在处理中"这种自相矛盾的两行 ——
// 所以那两处由 KnowledgeDocumentRepository 顺带完成（同步链路是 ReplaceChunks /
// MarkFailed，异步文件链路是 SaveEmbeddingsAndMarkReady / MarkFailed），
// 本接口只留"提交时建一条 pending 记录"和查询侧。
type KnowledgeUploadRecordRepository interface {
	// CreateUploadRecord 插入一条上传记录，落库后回填 record.ID。
	// 提交一次文件收录时调用，状态是 pending。
	//
	// 名字里带 UploadRecord 不是啰嗦：调用点同时会用到 KnowledgeDocumentRepository
	// 的 Create，两者签名不同却同名 —— Go 没有重载，一个类型就不可能同时满足
	// 两个接口，注入方只能拆成两个参数。换个名字才能让同一个仓储对象兼任两职。
	CreateUploadRecord(ctx context.Context, record *entity.KnowledgeUploadRecord) error

	// List 按创建时间倒序分页返回上传记录，并带上关联文档的标题。
	//
	// 标题是 LEFT JOIN knowledge_documents 取出来的（见 entity.KnowledgeUploadRecordView）：
	// 文档已被删除时为空串，由调用方回落到 OriginalName。
	// 一次 JOIN 而不是逐条回查，是因为抽屉与主页一样是分页列表，N+1 会很显眼。
	List(ctx context.Context, offset, limit int) ([]entity.KnowledgeUploadRecordView, int64, error)

	// GetByID 按主键取一条记录。查不到返回 gorm.ErrRecordNotFound。
	GetByID(ctx context.Context, id uint64) (*entity.KnowledgeUploadRecord, error)

	// DeleteRecord 删掉一条上传记录，**若它关联的文档尚未收录成功，连同文档一起删**。
	//
	// 连带删除不是可选项：未就绪的文档行（pending / processing）正是上传闸门
	// （CountActive）的输入，留着它会让"一次只收一份"永久返回 409，而记录删掉之后
	// 用户在界面上再也没有任何入口能清掉它。
	//
	// 已 ready 的文档绝不触碰 —— 那种情况下只是这条投递历史消失了，
	// 记录表里从此不再有它，而知识本身（正文、切片、向量）原样保留。
	// 切片与向量不用显式删，外键 ON DELETE CASCADE 会带走。
	DeleteRecord(ctx context.Context, id uint64) error
}
