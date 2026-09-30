package response

// KnowledgeEmbeddingStatus 是"存量向量是否跟上了当前默认模型"的体检结果。
//
// 给知识库页的提示与进度显示用，两个数字分工不同：
//   - StaleDocuments > 0：有 ready 文档的向量还挂在旧模型（或干脆没有向量）名下 ——
//     检索只查默认模型名下的向量，它们现在向量路召回不到，只能靠词法命中。
//     提示条据此出现，一键重新向量化的挑选也以它为准。
//   - PendingDocuments：同样缺新模型向量、但已经回到队列正在补算的文档数。
//     入队后文档立刻不再是 ready（StaleDocuments 归零），进度只能看这个数字，
//     它降到 0 才代表这一轮重新向量化真正收敛。
//
// 没有默认模型时 Model 为空、两个计数都为 0：那是"还没配置向量服务"，不是"需要重算"，
// 提示条据此不出现（引导配置是设置页的事，不该混进这里的口径）。
type KnowledgeEmbeddingStatus struct {
	Model            string `json:"model,omitempty"`   // 当前默认模型名；未配置时为空
	Dimensions       int    `json:"dimensions"`        // 当前默认模型的输出维度；未配置时为 0
	StaleDocuments   int    `json:"stale_documents"`   // ready 里仍缺该模型向量的文档数（待修复）
	PendingDocuments int    `json:"pending_documents"` // pending / processing 里仍缺该模型向量的文档数（正在补）
}

// KnowledgeReembedResult 是一次"重新向量化"的受理结果。
//
// 它是批处理：逐个文档入队，中途队列满就停止，所以总数与两个计数不一定对得上 ——
// Total = Queued + Skipped + 未入队的剩余（QueueFull 为真时才有剩余）。
// 剩余部分调用方稍后再点一次即可，不必在这里做重试循环。
type KnowledgeReembedResult struct {
	Total     int  `json:"total"`      // 检测到的缺当前模型向量的文档数
	Queued    int  `json:"queued"`     // 本次已重新排队的文档数
	Skipped   int  `json:"skipped"`    // 状态已变、安静跳过（并发下被别的请求抢先）
	QueueFull bool `json:"queue_full"` // 是否因收录队列满而提前停止
}
