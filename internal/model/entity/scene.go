package entity

import "encoding/json"

// 场景类型 scenes.type（§4.4）。
// 只是场景的分类标签，不参与渲染——前端按 content.blocks 里每个 block 的 type 渲染；
// 它用于大纲排课（interactive 模式下排入更多交互演示页）、侧栏分类上色和识别课程完成页。
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
// 约束（§4.4）：UNIQUE (classroom_id, sort_order)，以及 Type、ContentStatus、
// NarrationStatus 三个 CHECK，均写在 SQL migration 中。
type Scene struct {
	BaseModel

	ClassroomID uint64 `gorm:"column:classroom_id;not null" json:"classroom_id"`     // 所属课程；级联删除
	SortOrder   int32  `gorm:"column:sort_order;not null" json:"sort_order"`         // 场景顺序，从 0 开始
	Type        string `gorm:"column:type;type:varchar(32);not null" json:"type"`    // 场景分类标签，不参与渲染；slide | quiz | interactive | pbl | complete
	Title       string `gorm:"column:title;type:varchar(200);not null" json:"title"` // 场景标题；由大模型产出，写入前须按字符截断到 200 以内

	ContentStatus   string `gorm:"column:content_status;type:varchar(32);not null" json:"content_status"`     // 页面 JSON 生成状态，见 §5.2
	NarrationStatus string `gorm:"column:narration_status;type:varchar(32);not null" json:"narration_status"` // 老师讲解段落生成状态，见 §5.2

	// Content 场景内容，无论场景 Type 是什么都统一为 {"blocks":[...]}，例如：
	//   {"blocks":[{"key":"intro-variable","type":"paragraph","content":"..."}]}
	// 前端按每个 block 的 type 渲染，与场景的 Type 无关。block 的 type 取值是一份
	// 前后端契约（heading、paragraph、list-item、callout、code、quiz、browser、columns），
	// 每种 type 的 JSON schema 由后端领域层校验；同一场景内 block 的 key 不得重复，
	// 且必须能被 scene_segments.content_key 找到；大模型只生成结构化 JSON，不生成 HTML。
	//
	// block 是最小的可高亮单位：content_key 只指向具体 block，不支持 list.2 这类子路径，
	// 所以列表的每个要点、分栏里的每个条目都各占一个 block。
	Content json.RawMessage `gorm:"column:content;type:jsonb;not null;default:'{}'" json:"content"`

	// ErrorMessage 本场景生成失败的错误摘要。一页失败不会中断流程——跳过它、继续生成后面的
	// 场景，所以这一页为什么没出得来只有这里记。与 classrooms.generation_error 分工不同：
	// 那边记的是「整条流程为什么停了」，这一列记的是「这一页为什么没生成出来」。
	// 只写能给人看的摘要，禁止写入原始 API 响应、堆栈和密钥，落库前按字符截断（建议 500）。
	// 类型用 text 而非 varchar：varchar 超长是报错，会把整个生成事务回滚掉。
	ErrorMessage *string `gorm:"column:error_message;type:text" json:"error_message"`
}

// TableName 返回表名。
func (Scene) TableName() string { return "scenes" }
