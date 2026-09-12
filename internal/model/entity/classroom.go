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
	ClassroomStatusGenerating = "generating" // 正在逐场景生成内容
	ClassroomStatusReady      = "ready"      // 所有场景及其讲解均已生成，课程可播放
	ClassroomStatusFailed     = "failed"     // 最近生成流程失败
)

// Classroom 课程主记录，对应表 classrooms（设计文档 §4.2）。
//
// 课程是核心聚合根：场景、课程角色、讲解段落、生成任务和工作台会话均从属于课程，
// 删除课程时全部级联删除（§6）。
type Classroom struct {
	BaseModel

	FolderID *uint64 `gorm:"column:folder_id" json:"folder_id"` // 所属文件夹，可空；删除文件夹时置 NULL

	Title       string `gorm:"column:title;type:varchar(200);not null" json:"title"`     // 课程名称
	Requirement string `gorm:"column:requirement;type:text;not null" json:"requirement"` // 用户原始生成需求
	Mode        string `gorm:"column:mode;type:varchar(32);not null" json:"mode"`        // vocational | interactive
	Status      string `gorm:"column:status;type:varchar(32);not null" json:"status"`    // 课程当前状态，见 §5.1

	// GenerationConfig 本次生成的模型、搜索、解析器等快照。
	GenerationConfig json.RawMessage `gorm:"column:generation_config;type:jsonb;not null;default:'{}'" json:"generation_config"`

	// AgentConfig 角色选择模式、自动生成策略与 TTS 配置。
	// 只保存本次采用的选择方式和生成策略；角色明细统一存 classroom_agents，不在此重复。
	AgentConfig json.RawMessage `gorm:"column:agent_config;type:jsonb;not null;default:'{}'" json:"agent_config"`

	// LatestGenerationJobID 最近一次生成任务，避免读取列表时聚合查询。
	// 该列的外键指向 generation_jobs(id)，因建表顺序靠后，由迁移脚本延迟添加（§8 第一批第 7 步）。
	LatestGenerationJobID *uint64 `gorm:"column:latest_generation_job_id" json:"latest_generation_job_id"`
}

// TableName 返回表名。
func (Classroom) TableName() string { return "classrooms" }
