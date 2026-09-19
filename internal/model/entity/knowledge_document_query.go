package entity

// KnowledgeDocumentQuery 是文档列表的查询条件。
//
// 它放在 entity 而不是仓储层，理由和 ChunkReplacement 一样：同时被 repository
// （执行查询）与 service / rag（给出条件）引用，而 entity 是它们共同依赖的叶子包。
// 留在仓储层，上层就得反过来 import 仓储，分层会反转。
//
// 条件全部可空：Statuses 为空表示不限状态，Keyword 为空表示不限关键字。
// Offset / Limit 由调用方归一化，仓储不校验也不设默认值 —— 分页口径
// （页从 1 起、每页上限 100）归服务层，仓储只管把这个窗口落到 SQL 上。
type KnowledgeDocumentQuery struct {
	// Statuses 只看这些状态的文档；空切片表示不限。
	// 取值见 KnowledgeDocumentStatusXxx，合法性的校验在服务层。
	Statuses []string

	// Keyword 在标题与来源标识上做不区分大小写的模糊匹配；空串表示不限。
	Keyword string

	// Offset / Limit 分页窗口。Limit 为 0 时 GORM 会当成"不限制"，
	// 所以调用方必须给出大于 0 的值。
	Offset int
	Limit  int
}
