package entity

import (
	"encoding/json"
	"time"
)

// 文档的来源类型，取值必须与 source_type 字段上那条 CHECK 约束保持一致。
const (
	KnowledgeDocumentSourceManual = "manual" // 用户在编辑器里直接录入的正文
	KnowledgeDocumentSourceImport = "import" // 由文件导入（上传的原件）
	KnowledgeDocumentSourceAPI    = "api"    // 外部系统通过接口同步
)

// 文档的处理状态。上传后由后台异步推进（解析一份 PDF 可能几分钟），
// 前端靠这个字段轮询进度。
//
// 取值必须与 Status 字段上 check tag 里的 knowledge_documents_status_check
// 保持一致，改一处要改两处（见 migrations/README.md）。
const (
	KnowledgeDocumentStatusPending    = "pending"    // 已入库，排队等待解析
	KnowledgeDocumentStatusProcessing = "processing" // 解析、切分或向量化进行中
	KnowledgeDocumentStatusReady      = "ready"      // 处理完成，切片可参与检索
	KnowledgeDocumentStatusFailed     = "failed"     // 处理失败，原因记在 metadata
)

// KnowledgeDocument 知识原文实体对应知识原文表，是全局知识库中的一篇完整文章。
// 它不归属于课程。正文保存未经切分的原文，是编辑、审计和重新生成切片的唯一来源；
// 停用后文章及其切片不会参与知识检索。更新原文后应替换其全部切片，关联向量会级联删除。
//
// 后台取待处理任务的队列索引 knowledge_documents_status_created_at_idx：只覆盖队列里的行，
// 处理完就退出索引，索引一直很小。它的 WHERE 谓词含逗号，写法见 CreatedAt 字段上的注释。
type KnowledgeDocument struct {
	BaseModel

	Title           string          `gorm:"column:title;type:varchar(300);not null" json:"title"`                                                                                                                // 文章展示标题
	Content         string          `gorm:"column:content;type:text;not null;check:knowledge_documents_ready_has_content_check,status <> 'ready' OR length(content) > 0" json:"content"`                         // 未切分的完整原文，是重建切片的唯一来源；CHECK 跨 status 与 content 两列，挂在本字段（每字段限一条 check tag）
	SourceType      string          `gorm:"column:source_type;type:varchar(32);not null;check:knowledge_documents_source_type_check,source_type IN ('manual', 'import', 'api')" json:"source_type"`              // 文章来源类型：manual、import 或 api
	SourceURI       *string         `gorm:"column:source_uri;type:text" json:"source_uri"`                                                                                                                       // 外部来源地址、文件位置或接口标识；手工文章可为空
	ContentChecksum *string         `gorm:"column:content_checksum;type:char(64)" json:"content_checksum"`                                                                                                       // 原文内容的 SHA-256 摘要，用于判断是否需要重新切片
	Enabled         bool            `gorm:"column:enabled;not null" json:"enabled"`                                                                                                                              // 是否允许该文章的切片参与 RAG 检索
	Status          string          `gorm:"column:status;type:varchar(32);not null;default:pending;check:knowledge_documents_status_check,status IN ('pending', 'processing', 'ready', 'failed')" json:"status"` // 处理状态：pending、processing、ready 或 failed
	Metadata        json.RawMessage `gorm:"column:metadata;type:jsonb;not null;index:knowledge_documents_metadata_idx,type:gin" json:"metadata"`                                                                 // 扩展信息：分类、标签、作者，以及解析失败原因等处理产物

	// CreatedAt / UpdatedAt 遮蔽 BaseModel 的同名字段，只为给它们挂索引。
	// 遮蔽在 GORM schema 里是安全的：直接声明的字段 BindNames 更短，会覆盖嵌入字段
	// （schema.go 的 FieldsByDBName 去重逻辑），autoCreateTime / autoUpdateTime 行为不变。
	//
	// CreatedAt 上挂着后台取待处理任务的队列索引。注意 where 里的逗号写成了 `\,`：
	// index tag 的选项按逗号切分（ParseTagSetting），逗号必须转义；而 `\,` 在 Go 源码里
	// 又必须写成 `\\,` —— reflect.StructTag 用 strconv.Unquote 解码，`\,` 不是合法的 Go
	// 转义序列，会让**整条 tag 被静默丢弃**（不是报错，是字段上什么 tag 都没有）。
	// 两层的次序是：源码 `\\,` → Unquote 得 `\,` → GORM 还原成 `,`。
	// 对照：check tag 的表达式逗号**不用**转义，ParseCheckConstraints 会把逗号拼回去。
	//
	// 别把它改成 status IN ('pending', 'processing') 手写形式——那会被 Unquote 丢弃，
	// 索引静默消失。改动后记得核对 pg_indexes 里这条索引还在。
	CreatedAt time.Time `gorm:"column:created_at;not null;autoCreateTime;index:knowledge_documents_status_created_at_idx,where:status IN ('pending'\\,'processing')" json:"created_at"`

	// UpdatedAt 挂「启用中文档按更新时间倒序」的部分索引；谓词不含逗号，无需转义。
	UpdatedAt time.Time `gorm:"column:updated_at;not null;autoUpdateTime;index:knowledge_documents_enabled_updated_at_idx,where:enabled,sort:DESC" json:"updated_at"`
}

func (KnowledgeDocument) TableName() string { return "knowledge_documents" }
