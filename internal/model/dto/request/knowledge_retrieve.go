package request

// KnowledgeRetrieve 是一次知识库检索的请求。
//
// 它是 MCP 契约 rag_retrieve 的 HTTP 形态（见 docs/modules/agent-mcp-tools.md）：
// { query, queries?, top_k } → { results: [{content, source, score}] }。
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
}
