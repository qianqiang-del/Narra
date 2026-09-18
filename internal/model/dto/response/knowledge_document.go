package response

import "time"

// KnowledgeDocument 是知识库文档的对外结构。
//
// 刻意不含正文：一篇文档的正文可能有几十万字，列表和详情接口带上它，
// 响应会大到没有必要；正文只在重建切片时才需要读，而那属于检索链路的内部行为。
// 这里而是给出字符数，足够前端显示"这篇文档有多大"。
//
// 同样不含 metadata：它是处理过程的产物（解析器身份、耗时、失败原因），
// 结构会随实现变化。唯一真正需要给用户看的是失败原因，所以单独提成 Error 字段。
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

type KnowledgeDocumentPreview struct {
	KnowledgeDocument
	Content string `json:"content"`
}
