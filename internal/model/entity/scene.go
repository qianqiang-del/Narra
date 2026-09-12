package entity

import "encoding/json"

// 场景类型 scenes.type（§4.4）。
const (
	SceneTypeSlide       = "slide"
	SceneTypeQuiz        = "quiz"
	SceneTypeInteractive = "interactive"
	SceneTypePBL         = "pbl"
	SceneTypeComplete    = "complete" // 课程完成页
)

// 场景内容状态 scenes.content_status（§5.2）。
const (
	SceneContentStatusPending    = "pending"    // 已有大纲，等待生成页面 JSON
	SceneContentStatusGenerating = "generating" // 正在生成页面 JSON
	SceneContentStatusReady      = "ready"      // 页面 JSON 已生成，可供前端渲染
	SceneContentStatusFailed     = "failed"     // 生成失败，可重试
)

// 场景讲解状态 scenes.narration_status（§5.2）。
const (
	SceneNarrationStatusPending    = "pending"    // 页面 JSON 已生成，等待生成讲解段落
	SceneNarrationStatusGenerating = "generating" // 正在生成老师讲解段落
	SceneNarrationStatusReady      = "ready"      // 所有讲解段落均已生成
	SceneNarrationStatusFailed     = "failed"     // 讲解生成失败，可重试
)

// Scene 课程场景/课件页，对应表 scenes（设计文档 §4.4）。
//
// 页面内容与老师讲解拆成两个独立状态，用于准确表达「页面已经生成，但讲解还在生成」
// 的中间状态；两者都为 ready 时场景才可完整播放（§5.2）。
//
// 约束（§4.4）：UNIQUE (classroom_id, sort_order)，写在 SQL migration 中。
type Scene struct {
	BaseModel

	ClassroomID     uint64  `gorm:"column:classroom_id;not null" json:"classroom_id"`     // 所属课程；级联删除
	GenerationJobID *uint64 `gorm:"column:generation_job_id" json:"generation_job_id"`    // 生成该场景的课程任务，可空
	SortOrder       int32   `gorm:"column:sort_order;not null" json:"sort_order"`         // 场景顺序，从 0 开始
	Type            string  `gorm:"column:type;type:varchar(32);not null" json:"type"`    // slide | quiz | interactive | pbl | complete
	Title           string  `gorm:"column:title;type:varchar(200);not null" json:"title"` // 场景标题

	ContentStatus   string `gorm:"column:content_status;type:varchar(32);not null" json:"content_status"`     // 页面 JSON 生成状态，见 §5.2
	NarrationStatus string `gorm:"column:narration_status;type:varchar(32);not null" json:"narration_status"` // 老师讲解段落生成状态，见 §5.2

	// Content 按场景类型保存的内容，统一包含可渲染的 blocks 数组，例如：
	//   {"blocks":[{"key":"intro-variable","type":"paragraph","content":"..."}]}
	// 同一场景内 block 的 key 不得重复；大模型只生成结构化 JSON，不直接生成 HTML；
	// 不同 type 的 JSON schema 由后端领域层校验。
	Content json.RawMessage `gorm:"column:content;type:jsonb;not null;default:'{}'" json:"content"`

	ContentVersion int16   `gorm:"column:content_version;not null;default:1" json:"content_version"` // 场景 JSON schema 版本，用于未来结构迁移
	ErrorMessage   *string `gorm:"column:error_message;type:text" json:"error_message"`              // 本场景生成失败的错误摘要
}

// TableName 返回表名。
func (Scene) TableName() string { return "scenes" }
