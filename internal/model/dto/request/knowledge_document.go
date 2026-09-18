package request

// KnowledgeIngestFile 是从磁盘文件收录一篇知识文档的请求。
//
// 它走异步链路：接口收到之后只建一条 pending 行就返回，
// 解析与向量化由 rag.Worker 在后台推进。
type KnowledgeIngestFile struct {
	// Path 待解析文件的本地路径。调用方必须先把它落到磁盘（上传接口就是先存盘再提交）。
	//
	// 文件的生命周期不归调用方管：提交成功之后，暂存目录由 worker 在处理完（无论成败）
	// 时删掉；提交阶段就失败的话（建行失败、存储不支持异步任务），由上传接口自己兜底清理。
	Path string `json:"path" binding:"required"`
	// Title 文档标题；留空时先取 SourceURI 当临时标题，若正文开头有一级标题，
	// worker 会改用那个标题（文件名常带日期和版本号，可读性差得多）。
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
// 没有文件格式需要转换。也因为不需要解析，这条链路是**同步**的 ——
// 请求返回时文档已经是 ready 或 failed，不存在 pending 中间态。
type KnowledgeIngestText struct {
	Title      string `json:"title"`                      // 文档标题；留空时取正文首个一级标题，再回落到首行
	Content    string `json:"content" binding:"required"` // 待收录的正文，本身已经是 Markdown
	SourceType string `json:"source_type"`                // 来源类型；留空按 manual 处理
	SourceURI  string `json:"source_uri"`                 // 来源标识，可为空
}
