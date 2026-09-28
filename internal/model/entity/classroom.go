package entity

import "encoding/json"

// 课程模式 classrooms.mode（§4.2）。
const (
	ClassroomModeVocational  = "vocational"  // 职业技能
	ClassroomModeInteractive = "interactive" // 互动课堂
)

// 课程状态 classrooms.status（§5.1）。
//
// 只有四个值。曾经的 draft、outlining 已删除：课程建出来就开始生成，没有「等待用户确认大纲」
// 这一步；大纲和场景内容的生成在前端都是「正在生成」，用户看不出区别，后端自己知道走到哪一步
// 就够了，不需要往库里写。
const (
	ClassroomStatusGenerating = "generating" // 正在生成（大纲与场景内容算在一起），首页尚未就绪，进不去
	ClassroomStatusPlayable   = "playable"   // 首页已就绪，可以进入课堂；后续场景仍在生成，或流程已中断不再生成（看 GenerationError）
	ClassroomStatusReady      = "ready"      // 所有场景及其讲解均已生成，没有任何一页失败
	ClassroomStatusFailed     = "failed"     // 生成中断，且首页尚未就绪，课程不可进入
)

// Classroom 课程主记录，对应表 classrooms（设计文档 §4.2）。

type Classroom struct {
	BaseModel

	FolderID *uint64 `gorm:"column:folder_id;index:idx_classrooms_folder_id;comment:所属文件夹 ID，指向 folders.id；可为空表示未归档，文件夹被删除时置空" json:"folder_id"` // 所属文件夹，可空；删除文件夹时置 NULL

	// Folder 仅供 AutoMigrate 建立外键 classrooms_folder_id_fkey（ON DELETE SET NULL）。
	// 业务代码禁止给它赋值或 Preload：赋了非空值再保存本课程，GORM 会连带 upsert folders 行。
	Folder *Folder `gorm:"foreignKey:FolderID;constraint:classrooms_folder_id_fkey,OnDelete:SET NULL" json:"-"`

	Title       string `gorm:"column:title;type:varchar(200);not null;comment:课程名称" json:"title"`                                                                                                                                                                           // 课程名称
	Requirement string `gorm:"column:requirement;type:text;not null;comment:用户提交的原始生成需求，重新生成时以它为准" json:"requirement"`                                                                                                                                                      // 用户原始生成需求
	Mode        string `gorm:"column:mode;type:varchar(32);not null;check:classrooms_mode_check,mode IN ('vocational', 'interactive');comment:课程模式，取值 vocational（职业技能）/ interactive（互动课堂）" json:"mode"`                                                                     // vocational | interactive
	Status      string `gorm:"column:status;type:varchar(32);not null;check:classrooms_status_check,status IN ('generating', 'playable', 'ready', 'failed');comment:课程状态，取值 generating（生成中，首页未就绪进不去）/ playable（首页已就绪可进入）/ ready（全部场景完整生成）/ failed（中断且首页未就绪）" json:"status"` // 课程当前状态，见 §5.1
	// GenerationError 记录生成中断的原因，为空表示没有中断。
	// 与 status 配合读：playable 且它为空表示大纲已完成、场景仍在后台生成；非空表示流程已中断。
	GenerationError *string `gorm:"column:generation_error;type:text;comment:生成中断的原因；为空表示未中断。status=playable 且此列为空表示大纲已完成、场景仍在生成" json:"generation_error"`

	Plan json.RawMessage `gorm:"column:plan;type:jsonb;not null;default:'{}';comment:本轮生成的课堂计划快照（JSON）；同时作为前端大纲与段二执行的唯一事实源" json:"plan"`
	// PlanVersion 计划的版本号，首轮为 1，重新规划时递增。
	// 用它判断已落库的场景结果是否属于当前计划，而不是解析 JSON 再比。
	PlanVersion int32 `gorm:"column:plan_version;type:int;not null;default:1;comment:计划版本号；首轮为 1，重新规划时递增，用于判断已落库的场景结果是否属于当前计划" json:"plan_version"`

	// GenerationRunID 是本轮生成运行的标识：课程受理时生成，同一门课的所有重试沿用同一个。
	// 任务重投时靠它区分「同一趟运行的再次尝试」与「新一轮生成」，链路追踪也以它为一条 trace。
	GenerationRunID string `gorm:"column:generation_run_id;type:varchar(64);not null;default:'';comment:本轮生成运行的唯一标识；受理时生成，任务重试沿用同一个，用于幂等判重与链路追踪归属" json:"generation_run_id"`

	// GenerationConfig 本次生成的模型、搜索、解析器等快照。
	GenerationConfig json.RawMessage `gorm:"column:generation_config;type:jsonb;not null;default:'{}';comment:本次生成用的模型、搜索、解析器等配置快照（JSON）" json:"generation_config"`

	// AgentConfig 角色选择模式、自动生成策略与 TTS 配置。
	// 只保存本次采用的选择方式和生成策略；角色明细统一存 classroom_agents，不在此重复。
	AgentConfig json.RawMessage `gorm:"column:agent_config;type:jsonb;not null;default:'{}';comment:本次的角色选择模式、自动生成策略与 TTS 配置（JSON）；角色明细在 classroom_agents，不在此重复" json:"agent_config"`
}

// TableName 返回表名。
func (Classroom) TableName() string { return "classrooms" }
