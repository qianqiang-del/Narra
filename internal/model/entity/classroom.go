package entity

import "encoding/json"

// 课程模式 classrooms.mode（§4.2）。
const (
	ClassroomModeVocational  = "vocational"  // 职业技能
	ClassroomModeInteractive = "interactive" // 互动课堂
)

// 课程状态 classrooms.status（§5.1）。
const (
	ClassroomStatusDraft      = "draft"      // 已创建，尚未开始生成
	ClassroomStatusOutlining  = "outlining"  // 正在生成课程大纲
	ClassroomStatusGenerating = "generating" // 正在生成场景内容，首页尚未就绪
	ClassroomStatusPlayable   = "playable"   // 首页已就绪，可以进入课堂；后续场景仍在生成
	ClassroomStatusReady      = "ready"      // 所有场景及其讲解均已生成
	ClassroomStatusFailed     = "failed"     // 生成流程中断，课程不可进入
)

// Classroom 课程主记录，对应表 classrooms（设计文档 §4.2）。
//
// 课程是核心聚合根：场景、课程角色、讲解段落和工作台会话均从属于课程，
// 删除课程时全部级联删除（§6）。
//
// 生成状态直接记在本表上：V1 一次生成只跑一趟，不并发、不重试、不取消、不支持断点续传，
// 所以没有独立的生成任务表——「这门课生成到哪了」就是 Status，「为什么停了」就是
// GenerationError。将来若开放「对同一门课重新生成」，才会需要单独记录每一趟任务。
//
// 约束（§4.2）：CHECK (mode IN (...))、CHECK (status IN (...))，与 §5.1 一一对应；
// 增减状态值时必须同时改两处。写在 SQL migration 中。
type Classroom struct {
	BaseModel

	FolderID *uint64 `gorm:"column:folder_id" json:"folder_id"` // 所属文件夹，可空；删除文件夹时置 NULL

	Title       string `gorm:"column:title;type:varchar(200);not null" json:"title"`     // 课程名称
	Requirement string `gorm:"column:requirement;type:text;not null" json:"requirement"` // 用户原始生成需求
	Mode        string `gorm:"column:mode;type:varchar(32);not null" json:"mode"`        // vocational | interactive
	Status      string `gorm:"column:status;type:varchar(32);not null" json:"status"`    // 课程当前状态，见 §5.1

	// GenerationError 整条生成流程中断的原因，给用户看的一句话；只有 Status 为 failed 时才有值。
	// 与 scenes.error_message 分工不同：那边记的是「这一页为什么没生成出来」，记的时候整条流程
	// 还在往下跑；这里记的是「整个流程为什么停了」。大纲阶段挂掉时一条场景都还没建，scenes 表
	// 里空无一物，这一列是唯一的失败记录。
	// 同样只写能给人看的摘要，禁止写入原始 API 响应、堆栈和密钥，落库前按字符截断（建议 500）。
	// 用 text 而非 varchar：varchar 超长是报错，会把整个生成事务回滚掉。
	GenerationError *string `gorm:"column:generation_error;type:text" json:"generation_error"`

	// GenerationConfig 本次生成的模型、搜索、解析器等快照。
	GenerationConfig json.RawMessage `gorm:"column:generation_config;type:jsonb;not null;default:'{}'" json:"generation_config"`

	// AgentConfig 角色选择模式、自动生成策略与 TTS 配置。
	// 只保存本次采用的选择方式和生成策略；角色明细统一存 classroom_agents，不在此重复。
	AgentConfig json.RawMessage `gorm:"column:agent_config;type:jsonb;not null;default:'{}'" json:"agent_config"`
}

// TableName 返回表名。
func (Classroom) TableName() string { return "classrooms" }
