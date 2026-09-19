package response

import "time"

// KnowledgeDocument 是知识库文档的对外结构。
//
// 刻意不含正文：一篇文档的正文可能有几十万字，列表和详情接口带上它，
// 响应会大到没有必要。这里给出字符数，足够前端显示"这篇文档有多大"；
// 真正要看正文的是预览接口，它用下面的 KnowledgeDocumentPreview 单独带上 Content。
//
// 同样不含 metadata：它是处理过程的产物（解析器身份、耗时、失败原因），
// 结构会随实现变化。其中真正要给用户看的只有两样，所以各提成一列 ——
// 失败原因归 Error，解析器身份归 Parser。
type KnowledgeDocument struct {
	ID         uint64    `json:"id"`                   // 文档主键
	Title      string    `json:"title"`                // 展示标题
	SourceType string    `json:"source_type"`          // 来源类型：manual、import 或 api
	SourceURI  string    `json:"source_uri,omitempty"` // 来源标识，如上传时的原始文件名
	Enabled    bool      `json:"enabled"`              // 是否参与知识检索
	Status     string    `json:"status"`               // pending、processing、ready 或 failed
	Parser     string    `json:"parser,omitempty"`     // 实际使用的解析器
	Error      string    `json:"error,omitempty"`      // 处理失败的原因；成功时为空
	Chunks     int       `json:"chunks"`               // 切片数量
	Characters int       `json:"characters"`           // 正文的字符数
	CreatedAt  time.Time `json:"created_at"`           // 创建时间
	UpdatedAt  time.Time `json:"updated_at"`           // 最近一次状态或内容变更时间
}

// KnowledgeDocumentPreview 在文档概要之上多带一份解析后的正文，
// 只由预览接口返回（GET /knowledge/documents/:id/preview）——
// 这是全项目唯一会输出正文的地方。
// 文档还没处理完（pending / processing）或已失败时，Content 是空串，
// 前端据此显示"暂无解析正文"。
type KnowledgeDocumentPreview struct {
	KnowledgeDocument
	Content string `json:"content"` // 解析后的 Markdown 正文
}
