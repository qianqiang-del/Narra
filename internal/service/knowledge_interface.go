package service

import (
	"context"

	requestdto "narra/internal/model/dto/request"
	responsedto "narra/internal/model/dto/response"
)

// KnowledgeService 是知识库的 HTTP 面：收录入口与文档查询。
//
// 实现分两处，与 MCP 同一种切法（internal/mcp 管运行时、mcp_server_service 管配置）：
//   - 收录链路的编排在 internal/rag：解析 → 切分 → 向量化 → 一个事务写三张表；
//   - 本层只做 DTO ↔ entity 的映射和文档分页查询。
//
// 文件收录是**异步**的：SubmitFile 建好 pending 行就返回，解析与向量化由 rag.Worker
// 在后台推进，调用方靠 Get 轮询状态。当初异步化要解决的三个问题都有了对策：
// 任务队列 = knowledge_documents 里的 pending 行；进度查询 = Get；
// 重启时未完成的任务 = Worker 启动时 ResetStale 把超时的 processing 打回 pending。
// 正文收录（IngestText）仍同步：没有解析这一步，切分与向量化是秒级的。
type KnowledgeService interface {
	// IngestFile 从磁盘读一份文件并收录。解析器按文件后缀选择：
	// md / txt 直接读，其余格式交给文档解析器（需要 document_parser 已启用）。
	//
	// 任何一步失败都会把文档置为 failed 并把原因写进 metadata，同时把错误返回给调用方。
	IngestFile(ctx context.Context, input requestdto.KnowledgeIngestFile) (responsedto.KnowledgeDocument, error)
	SubmitFile(ctx context.Context, input requestdto.KnowledgeIngestFile) (responsedto.KnowledgeDocument, error)

	// IngestText 直接把一段正文收录为 Markdown，跳过解析。
	IngestText(ctx context.Context, input requestdto.KnowledgeIngestText) (responsedto.KnowledgeDocument, error)

	// List 分页返回文档列表，同时给出总数。page 从 1 开始。
	List(ctx context.Context, page, size int) ([]responsedto.KnowledgeDocument, int64, error)

	// Get 返回单篇文档。文档不存在时返回带明确说明的错误。
	Get(ctx context.Context, id uint64) (responsedto.KnowledgeDocument, error)
	Preview(ctx context.Context, id uint64) (responsedto.KnowledgeDocumentPreview, error)
	Delete(ctx context.Context, id uint64) error
}
