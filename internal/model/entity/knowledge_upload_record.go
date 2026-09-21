package entity

import "time"

// 上传记录的状态。取值与 knowledge_documents.status 完全同源 —— 一次上传的生命周期
// 就是它那条文档的生命周期，用别名而不是另立一组字符串，让"两边必须一致"由编译器兜住。
//
// 取值必须与 Status 字段上 check tag 里的 knowledge_upload_records_status_check 保持一致，
// 改一处要改两处（与 KnowledgeDocument 同一条约定）。
const (
	KnowledgeUploadRecordStatusPending    = KnowledgeDocumentStatusPending    // 已登记，排队等 worker 取走
	KnowledgeUploadRecordStatusProcessing = KnowledgeDocumentStatusProcessing // 解析 / 切分 / 向量化进行中
	KnowledgeUploadRecordStatusReady      = KnowledgeDocumentStatusReady      // 收录完成
	KnowledgeUploadRecordStatusFailed     = KnowledgeDocumentStatusFailed     // 收录失败，原因记在 error_message
)

// KnowledgeUploadRecord 上传记录实体对应上传记录表，是"一次文件上传"这件事本身。
//
// 它与 KnowledgeDocument 是两张表，因为承载的是两种不同的东西：文档是可检索的资产
// （正文、切片、向量都挂在它下面，删了就是知识的损失），记录是投递历史（只是一行流水）。
// 拆开之后"删掉一条历史"与"删掉一份知识"才是两件事 —— 原型里两个方向的删除互不影响，
// 靠的就是这个拆分；把它们挤在 knowledge_documents 一行里时，删记录必然等于删文档。
//
// 记录要能独立表达状态，是因为关联是弱关联：document_id 可以指向空的。
// 上传时它指向那条文档；文档被删除后外键把它置空（ON DELETE SET NULL），而记录留着 ——
// 此时 status 仍是 ready，界面上读作"已收录后删除"。光看 document_id 分不出
// "收录成功后被删"和"当时就失败了"，所以状态必须落在记录自己身上。
//
// 它同时是投递侧信息的归宿：原始文件名、文件字节数、失败原因。这三样在只有文档表的
// 时候无处可放 —— 原名挤在 documents.source_uri、字节数干脆没有、失败原因埋在
// documents.metadata 的 JSON 里（那是处理产物，结构随实现变化，不该被界面依赖）。
type KnowledgeUploadRecord struct {
	BaseModel

	// 一次上传只写一条记录，但**刻意不做成唯一约束**。
	//
	// 重试（批 ③）走的是原地重跑：同一篇文档改回 pending 再跑一遍，既不新建文档、
	// 也不新建记录（见 repository.Requeue）。"一篇文档至多一条记录"因此由实现保证，
	// 不靠约束兜底 —— 多一条唯一索引换不来现在需要的东西，却会把将来"重试另建一条
	// 记录"这种形态挡死。普通索引足够 —— 状态同步与列表联表都按这一列走。
	DocumentID *uint64 `gorm:"column:document_id;index:knowledge_upload_records_document_id_idx;comment:关联文档 ID，指向 knowledge_documents.id；文档被删除后置空（ON DELETE SET NULL），表示这次投递的成果已不在" json:"document_id"` // 关联文档 ID；文档被删除后为空，表示这次上传的成果已经不在了

	OriginalName string `gorm:"column:original_name;type:varchar(300);not null;comment:上传时的原始文件名，与 documents.title 同为 varchar(300)，超长会被截断" json:"original_name"` // 用户看到的原始文件名，与 documents.title 同为 varchar(300)；截断见 rag.truncateTitle

	// 文件字节数。取值来自 HTTP 层的 header.Size，是**接收时**的大小，不做二次统计。
	// 上传记录里最有用的一列：用户看到"这份 834 KB 的 pptx 失败了"，就能对上自己传的是哪个文件。
	SizeBytes int64 `gorm:"column:size_bytes;not null;default:0;comment:接收到的文件字节数，取自上传请求头；0 表示回填的历史数据没有这个值" json:"size_bytes"`

	Status string `gorm:"column:status;type:varchar(32);not null;default:pending;check:knowledge_upload_records_status_check,status IN ('pending', 'processing', 'ready', 'failed');comment:投递状态，取值 pending（排队）/ processing（处理中）/ ready（收录成功）/ failed（失败），与关联文档的状态同源" json:"status"` // 收录状态：pending、processing、ready 或 failed

	// 失败原因，给界面直接显示的一句话 —— 纯中文，不带错误码与 stderr 原文
	// （"文档解析失败：解析环境缺少 Python 模块 scipy"）。
	// 与 documents.metadata 里那份的关系：metadata 的 error 键是同一句话，
	// error_detail 才是完整的诊断（错误码 + stderr），两者分工见 rag.Ingester.failIngest。
	// 同步写入的时机见 repository.MarkFailed。
	ErrorMessage *string `gorm:"column:error_message;type:text;comment:失败原因的一句话，供界面直接显示；收录成功时为空" json:"error_message"`

	// Document 仅供 AutoMigrate 建外键 knowledge_upload_records_document_id_fkey（ON DELETE SET NULL）。
	// 业务代码禁止给它赋值或 Preload。
	//
	// 用 SET NULL 而不是 CASCADE：文档消失时这条投递历史要留着（界面靠它显示
	// "已收录后删除"），删掉记录等于把用户的投递记录抹了。
	Document *KnowledgeDocument `gorm:"foreignKey:DocumentID;constraint:knowledge_upload_records_document_id_fkey,OnDelete:SET NULL" json:"-"`

	// CreatedAt 遮蔽 BaseModel 的同名字段，只为挂"抽屉按时间倒序"的索引。
	// 遮蔽在 GORM schema 里是安全的，理由见 KnowledgeDocument.CreatedAt 的注释。
	CreatedAt time.Time `gorm:"column:created_at;not null;autoCreateTime;index:knowledge_upload_records_created_at_idx,sort:DESC;comment:投递时间，timestamptz 按 UTC 存；上传记录抽屉按这一列倒序" json:"created_at"`
}

func (KnowledgeUploadRecord) TableName() string { return "knowledge_upload_records" }

// KnowledgeUploadRecordView 是上传记录与它关联文档标题的组合，只用于列表查询。
//
// 标题不进记录表：两处都存必然漂移（用户改了文档标题，记录里还是旧的）。
// 它是 LEFT JOIN knowledge_documents 的产物，不落库、不建表 —— 放在 entity 的理由
// 与 ChunkReplacement、KnowledgeDocumentQuery 相同：service 与 repository 共同依赖。
//
// 文档已被删除时 DocumentTitle 是空串，由调用方回落到 OriginalName。
type KnowledgeUploadRecordView struct {
	KnowledgeUploadRecord

	DocumentTitle string `gorm:"column:document_title"`
}
