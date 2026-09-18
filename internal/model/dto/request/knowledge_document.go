package request

// KnowledgeIngestFile 是从磁盘文件收录一篇知识文档的请求。
type KnowledgeIngestFile struct {
	// Path 待解析文件的本地路径。调用方负责把上传的文件落到磁盘并在此之后清理。
	Path string `json:"path" binding:"required"`
	// Title 文档标题；留空时取 SourceURI，若正文有首个一级标题则改用那个标题。
	Title string `json:"title"`
	// SourceType 来源类型，取值见 entity.KnowledgeDocumentSourceXxx；留空按 import 处理。
	SourceType string `json:"source_type"`
	// SourceURI 来源标识。上传场景里放用户看到的原始文件名，
	// 而不是服务器上的临时路径 —— 后者对用户没有意义，还会泄露目录结构。
	SourceURI string `json:"source_uri"`
}

// KnowledgeIngestText 是把一段正文直接收录为知识文档的请求。
//
// 它跳过解析这一步：调用方给的已经是正文（编辑器保存、外部系统同步的内容），
// 没有文件格式需要转换。source_type 通常落成 manual。
type KnowledgeIngestText struct {
	Title      string `json:"title"`
	Content    string `json:"content" binding:"required"`
	SourceType string `json:"source_type"`
	SourceURI  string `json:"source_uri"`
}
