package entity

import "encoding/json"

const (
	KnowledgeDocumentSourceManual = "manual"
	KnowledgeDocumentSourceImport = "import"
	KnowledgeDocumentSourceAPI    = "api"
)

// 文档的处理状态。上传后由后台异步推进（解析一份 PDF 可能几分钟），
// 前端靠这个字段轮询进度。
//
// 取值必须与 migrations/0004_knowledge_document_status.sql 里的
// knowledge_documents_status_check 保持一致，改一处要改两处（见 migrations/README.md）。
const (
	KnowledgeDocumentStatusPending    = "pending"    // 已入库，排队等待解析
	KnowledgeDocumentStatusProcessing = "processing" // 解析、切分或向量化进行中
	KnowledgeDocumentStatusReady      = "ready"      // 处理完成，切片可参与检索
	KnowledgeDocumentStatusFailed     = "failed"     // 处理失败，原因记在 metadata
)

// KnowledgeDocument 知识原文实体对应知识原文表，是全局知识库中的一篇完整文章。
// 它不归属于课程。正文保存未经切分的原文，是编辑、审计和重新生成切片的唯一来源；
// 停用后文章及其切片不会参与知识检索。更新原文后应替换其全部切片，关联向量会级联删除。
type KnowledgeDocument struct {
	BaseModel

	Title           string          `gorm:"column:title;type:varchar(300);not null" json:"title"`                  // 文章展示标题
	Content         string          `gorm:"column:content;type:text;not null" json:"content"`                      // 未切分的完整原文，是重建切片的唯一来源
	SourceType      string          `gorm:"column:source_type;type:varchar(32);not null" json:"source_type"`       // 文章来源类型：manual、import 或 api
	SourceURI       *string         `gorm:"column:source_uri;type:text" json:"source_uri"`                         // 外部来源地址、文件位置或接口标识；手工文章可为空
	ContentChecksum *string         `gorm:"column:content_checksum;type:char(64)" json:"content_checksum"`         // 原文内容的 SHA-256 摘要，用于判断是否需要重新切片
	Enabled         bool            `gorm:"column:enabled;not null" json:"enabled"`                                // 是否允许该文章的切片参与 RAG 检索
	Status          string          `gorm:"column:status;type:varchar(32);not null;default:pending" json:"status"` // 处理状态：pending、processing、ready 或 failed
	Metadata        json.RawMessage `gorm:"column:metadata;type:jsonb;not null" json:"metadata"`                   // 扩展信息：分类、标签、作者，以及解析失败原因等处理产物
}

func (KnowledgeDocument) TableName() string { return "knowledge_documents" }
