package request

// KnowledgeRetrieve 是一次知识库检索的请求。
//
// 它是 MCP 契约 rag_retrieve 的 HTTP 形态（见 docs/modules/agent-mcp-tools.md）：
// { query, top_k } → { results: [{content, source, score}] }。
type KnowledgeRetrieve struct {
	// Query 检索词：一句自然语言、关键词，或两者混着写。
	Query string `json:"query" binding:"required"`
	// TopK 返回条数。0 或越界时由服务层钳位（默认 5 条，上限 50 条），
	// 调用方不必知道这两个口径。
	TopK int `json:"top_k"`
}
