package response

import "time"

// KnowledgeUploadRecord 是上传记录的对外结构 —— 一次文件投递的流水，不是一份知识。
//
// 与 KnowledgeDocument 的关键差别有两处：
//
//   - **包含已经收录成功的那些**。记录是"投递"这个动作的历史，一条 ready 记录
//     说明那次上传顺利入库了；文档被删掉之后记录仍留着，此时 DocumentID 为空，
//     界面读作"已收录后删除"。文档列表只会列还在的文档，两者口径不同。
//   - **带投递侧的信息**：原始文件名与文件字节数。这两样在文档上没有归宿
//     （原名只挤在 source_uri 里、字节数干脆没有），失败原因也只有记录里那一句是
//     直接可读的 —— 文档的 metadata 是处理产物，结构随实现变化。
type KnowledgeUploadRecord struct {
	ID         uint64  `json:"id"`          // 记录主键
	DocumentID *uint64 `json:"document_id"` // 关联文档 ID；为空表示那次上传的文档已经被删了

	// Title 展示用的标题：优先取关联文档的标题，文档已删除时回落到原始文件名。
	// 回落规则在服务层，不写进 SQL —— 它属于展示语义。
	Title string `json:"title"`

	OriginalName string `json:"original_name"` // 用户看到的原始文件名
	SizeBytes    int64  `json:"size_bytes"`    // 文件字节数；0 表示调用方没提供

	Status string `json:"status"`          // pending、processing、ready 或 failed
	Error  string `json:"error,omitempty"` // 失败原因；成功或未结束时为空

	CreatedAt time.Time `json:"created_at"` // 投递时间，抽屉按它倒序
	UpdatedAt time.Time `json:"updated_at"` // 最近一次状态变更时间
}
