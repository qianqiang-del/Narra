package response

// KnowledgeHit 是一次检索命中的一条切片。
//
// content / source / score 三个字段与 MCP 契约 rag_retrieve 的 results 元素一一对应，
// 多出来的字段（标题、章节、命中方式、相似度）是给界面显示与调参用的，
// MCP 适配层按需取用即可，不必对齐。
type KnowledgeHit struct {
	ChunkID    uint64   `json:"chunk_id"`             // 切片 ID
	DocumentID uint64   `json:"document_id"`          // 所属文档 ID
	Title      string   `json:"title"`                // 所属文档标题
	ChunkIndex int32    `json:"chunk_index"`          // 切片在原文中的顺序号，按它还原上下文顺序
	Heading    string   `json:"heading,omitempty"`    // 所在章节标题；无章节归属时不出现
	Content    string   `json:"content"`              // 切片正文
	Source     string   `json:"source"`               // 来源标识（原始文件名等）；手工录入的文档回落到标题
	Score      float64  `json:"score"`                // 融合排序分，只用于本次检索内部比较
	Similarity *float64 `json:"similarity,omitempty"` // 余弦相似度；纯词法命中的切片没有这个值
	Method     string   `json:"method"`               // 命中来源：vector / lexical / hybrid
}

// KnowledgeRetrieveResult 是一次检索的响应。
//
// 顶层的 model 与 terms 是调参与排障用的：结果不对劲时，先看是不是换了向量模型
// （换了模型而没重建切片，向量路会一条都召回不到），再看词法路到底拆出了哪几个词项。
type KnowledgeRetrieveResult struct {
	Model   string         `json:"model,omitempty"` // 向量召回所用的模型名；向量路没跑起来时不出现
	Terms   []string       `json:"terms,omitempty"` // 词法路实际使用的词项
	Results []KnowledgeHit `json:"results"`         // 按融合分降序的命中
}
