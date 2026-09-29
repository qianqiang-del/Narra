package request

import "time"

// KnowledgeRetrieve 是一次知识库检索的请求。
//
// 它是 MCP 契约 rag_retrieve 的 HTTP 形态（见 docs/modules/agent-mcp-tools.md）：
// { query, queries?, top_k, filters? } → { results: [{content, source, score}] }。
// 契约里的 classroom_id 没有实现（知识库不关联课程），filters 只在 HTTP 面提供 ——
// MCP 工具暂不暴露过滤参数，理由同 classroom_id：模型手里没有文档的来源分类，
// 给它一个没有语义的参数只会诱导它乱填。
type KnowledgeRetrieve struct {
	// Query 检索词：一句自然语言、关键词，或两者混着写。
	Query string `json:"query" binding:"required"`
	// Queries 是同一问题的其他说法（可选，最多 3 条）。
	//
	// 给了就用它们；没给时，如果本次运行注入了改写模型（课堂 Agent 会把自己的模型
	// 注入检索链路），服务端用模型自动扩写，失败或超时退回原查询。原 query 永远参与
	// 检索，多条结果经 RRF 融合后统一排序；空串、重复、与原 query 相同的条目会被忽略，
	// 超出 3 条的部分截掉。
	Queries []string `json:"queries"`
	// TopK 返回条数。0 或越界时由服务层钳位（默认 5 条，上限 50 条），
	// 调用方不必知道这两个口径。
	TopK int `json:"top_k"`
	// Filters 是文档侧的过滤条件，可选：给了就只在符合条件的文档里召回，
	// 缺省或全部为空等价于现在的不过滤行为。条件之间是 AND，列表内部是 OR。
	Filters *KnowledgeRetrieveFilters `json:"filters"`
}

// KnowledgeRetrieveFilters 是检索请求里的过滤条件。
//
// 字段全部可空，零值表示"这一项不参与过滤"；非法值由服务层拦下并翻成 400
// （见 service 的 ErrInvalidFilter），不会带着半截条件发起召回。
type KnowledgeRetrieveFilters struct {
	// SourceTypes 只看这些来源类型的文档，可选值 manual（手工录入）/ import（文件导入）。
	// 空 = 不限。
	SourceTypes []string `json:"source_types"`

	// DocumentIDs 只看这些文档下的切片。空 = 不限；上限见 service 的
	// maxFilterDocumentIDs —— 过滤条件该收敛范围，不是第二个批量导出口。
	DocumentIDs []uint64 `json:"document_ids"`

	// CreatedFrom / CreatedTo 是文档创建时间（上传时刻）的范围，RFC3339，
	// 前者含、后者不含。单边缺省表示只限一边；两端顺序颠倒由服务层拒绝。
	CreatedFrom *time.Time `json:"created_from"`
	CreatedTo   *time.Time `json:"created_to"`
}
