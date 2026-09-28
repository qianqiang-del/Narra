package response

// KnowledgeIngestItem 是批量上传里单个文件的处理结果。
//
// 它是**逐项结果**而不是整批一口价：某个文件格式不支持、超过单文件上限或队列已满时，
// 只有它自己被拒绝，同批已经入队的文件不受影响。DocumentID 只在入队成功时非空；
// 被拒时 Error 给出原因，用户据此决定改文件还是稍后重试。
type KnowledgeIngestItem struct {
	OriginalName string  `json:"original_name"`   // 用户看到的原始文件名；不做落盘路径
	DocumentID   *uint64 `json:"document_id"`     // 入队成功后的文档 ID；被拒时为 null
	Status       string  `json:"status"`          // pending（已入队）或 rejected（未入队）
	Error        string  `json:"error,omitempty"` // 未入队的原因；成功时为空
}

// KnowledgeIngestItemStatusPending / Rejected 是 KnowledgeIngestItem.Status 的合法取值。
const (
	KnowledgeIngestItemStatusPending  = "pending"
	KnowledgeIngestItemStatusRejected = "rejected"
)

// KnowledgeIngestBatch 是批量上传的响应体。
//
// Accepted / Rejected 是给界面直接用的汇总，免得前端为了显示"3 份已入队、7 份被拒"
// 再遍历一遍数组 —— 两者必须与 Items 自洽，由构造它的那一处保证。
type KnowledgeIngestBatch struct {
	Items    []KnowledgeIngestItem `json:"items"`    // 与请求里的文件顺序一一对应
	Accepted int                   `json:"accepted"` // 已入队文件数
	Rejected int                   `json:"rejected"` // 被拒文件数
}

// KnowledgeUploadLimits 是批量上传的限制值，由状态接口下发给前端做预检。
//
// 服务端始终是唯一裁判：这里给出一份不是为了让前端代替它判断，而是为了让"选完文件
// 立刻看到超限"不必等一次注定失败的请求。两种拒绝的差别只在时机，口径必须同源。
type KnowledgeUploadLimits struct {
	MaxFiles      int   `json:"max_files"`       // 单次最多几个文件
	MaxFileBytes  int64 `json:"max_file_bytes"`  // 单个文件字节数上限
	MaxBatchBytes int64 `json:"max_batch_bytes"` // 单次请求全部文件的总字节数上限
}
