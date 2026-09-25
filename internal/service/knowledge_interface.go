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
	// IngestFile 从磁盘读一份文件并**同步**收录完（解析 → 切分 → 向量化 → 入库）。
	// 解析器按文件后缀选择：md / txt 直接读，其余格式交给文档解析器（需要 document_parser 已启用）。
	//
	// 任何一步失败都会把文档置为 failed 并把原因写进 metadata，同时把错误返回给调用方。
	// HTTP 面没有路由指向它（上传走的是 SubmitFile），保留它是给需要"传完就等结果"的
	// 进程内调用与测试用。
	IngestFile(ctx context.Context, input requestdto.KnowledgeIngestFile) (responsedto.KnowledgeDocument, error)

	// SubmitFile 提交一份文件给后台收录，建好 pending 行就返回。
	//
	// 返回的文档 status 是 pending，chunks 是 0 —— 解析与向量化由 rag.Worker 推进。
	// 调用方拿返回的 id 用 Get 轮询进度。
	SubmitFile(ctx context.Context, input requestdto.KnowledgeIngestFile) (responsedto.KnowledgeDocument, error)

	// Retry 把一条收录失败的文档重新排队，让它再跑一遍（**原地重试**）。
	//
	// 复用同一行文档与同一条上传记录，起点按现实材料计算：有切片直接重新向量化，
	// 切片没了但有正文就重新分块，正文也没了才重新解析服务器上的原件
	// （data/uploads/failed/<文档ID>/）。只要三者还剩一样，就不需要用户重新上传。
	// 返回的文档 status 是 pending，调用方接着用 Get 轮询进度。
	//
	// 三种情形返回可判定的错误，接口层据此翻成 409 而不是 400：
	// 状态不是 failed（ErrRetryNotFailed）、原件与中间结果全都不在（ErrRecoveryInputMissing）、
	// 或此刻还有其他任务在跑（ErrIngestBusy）。
	Retry(ctx context.Context, id uint64) (responsedto.KnowledgeDocument, error)

	// IngestText 直接把一段正文收录为 Markdown，跳过解析。这条链路仍是同步的。
	IngestText(ctx context.Context, input requestdto.KnowledgeIngestText) (responsedto.KnowledgeDocument, error)

	// List 分页返回满足条件的文档，同时给出总数。
	//
	// 条件由 ParseDocumentListQuery 从查询参数解析而来（page / size 已钳位、
	// status 已校验并拆成列表）。直接传零值等价于"第一页、默认页长、不限条件"。
	List(ctx context.Context, query requestdto.KnowledgeListQuery) ([]responsedto.KnowledgeDocument, int64, error)

	// ListUploadRecords 分页返回上传记录 —— 文件投递的历史流水，含已经收录成功的那些。
	//
	// 它与 List 是两份不同的东西：List 列的是资产（能参与检索的文档），这里列的是动作
	// （谁在什么时候投了什么文件、成没成）。两者靠 document_id 弱关联，删一个不影响另一个。
	ListUploadRecords(ctx context.Context, page, size int) ([]responsedto.KnowledgeUploadRecord, int64, error)

	// DeleteUploadRecord 删除一条记录；关联文档尚未收录成功时把它一起删掉。
	// 记录不存在时返回带明确说明的错误。
	DeleteUploadRecord(ctx context.Context, id uint64) error

	// Get 返回单篇文档。文档不存在时返回带明确说明的错误。
	Get(ctx context.Context, id uint64) (responsedto.KnowledgeDocument, error)

	// SetEnabled 切换一篇文档是否参与检索，返回更新后的文档。
	//
	// 停用不删任何东西：切片与向量原样保留，只是两条召回 SQL 过滤掉它
	// （见 knowledge_search_repository），改回 true 立即恢复。文档不存在时返回
	// 带明确说明的错误；它对文档状态没有要求（字段与收录状态正交）。
	SetEnabled(ctx context.Context, id uint64, enabled bool) (responsedto.KnowledgeDocument, error)

	// Preview 返回单篇文档的解析正文。正文只有这个接口会出网。
	Preview(ctx context.Context, id uint64) (responsedto.KnowledgeDocumentPreview, error)

	// Delete 删除一篇文档，连同它的切片与向量（外键级联），并清理上传暂存目录。
	// 文档不存在时返回带明确说明的错误。
	Delete(ctx context.Context, id uint64) error

	// Retrieve 检索知识库，返回最相关的切片。
	//
	// 两路召回（余弦相似度 + 词项命中）经 RRF 融合后取前 top_k 条，编排在 rag.Retriever。
	// 这里只做 DTO ↔ rag 的映射与检索词校验 —— 它是 MCP 契约 rag_retrieve 的进程内入口
	// （见 docs/modules/agent-mcp-tools.md）。
	//
	// 检索词为空时返回可判定的 ErrEmptyQuery，接口层据此翻成 400。
	// 另有一条降级约定在 rag.Retriever 里：一路召回挂掉不影响另一路，
	// 但两路都没结果时失败原因会上抛，接口层按 500 处理。
	Retrieve(ctx context.Context, input requestdto.KnowledgeRetrieve) (responsedto.KnowledgeRetrieveResult, error)
}
