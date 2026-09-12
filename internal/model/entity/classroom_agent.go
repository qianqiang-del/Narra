package entity

// 角色来源 classroom_agents.source（§4.3）。
const (
	ClassroomAgentSourcePreset = "preset" // 用户手动选择代码中已有的角色
	ClassroomAgentSourceAuto   = "auto"   // 系统按生成策略从已有预设角色中自动选择
)

// 角色类型 classroom_agents.role_type（§4.3）。
const (
	ClassroomAgentRoleTypeTeacher   = "teacher"
	ClassroomAgentRoleTypeAssistant = "assistant"
	ClassroomAgentRoleTypeStudent   = "student"
)

// ClassroomAgent 课程角色快照，对应表 classroom_agents（设计文档 §4.3）。
//
// 本表保存一节课程最终实际使用的角色，不是全局角色库：角色的默认定义、系统提示词模板
// 和可用音色目录仍由代码/配置维护，生成时将最终使用的角色信息复制到这里，
// 保证后续回放和重试不受默认配置变化影响。
//
// source 为 auto 时表示系统从已有预设角色中自动选择，不代表大模型临时创建新角色。
// 无论哪种来源，写入后的 name、role、role_type、system_prompt、voice_id、avatar、color
// 都是本课程的冻结快照；修改角色应创建新的生成版本，不覆盖历史记录。
//
// 约束（§4.3）：UNIQUE (classroom_id, sort_order)、UNIQUE (classroom_id, avatar)。
// classroom_id 同时参与这两个复合唯一约束，而 GORM 的 tag 在同一字段上无法声明两个复合索引
// （ParseTagSetting 对重复 key 是覆盖而非追加），因此这两条约束写在 SQL migration 中。
type ClassroomAgent struct {
	BaseModel

	ClassroomID uint64 `gorm:"column:classroom_id;not null" json:"classroom_id"` // 所属课程；级联删除

	Source   string `gorm:"column:source;type:varchar(32);not null" json:"source"`       // preset | auto
	AgentKey string `gorm:"column:agent_key;type:varchar(80);not null" json:"agent_key"` // 角色定义 ID，如 teacher、curious
	Name     string `gorm:"column:name;type:varchar(120);not null" json:"name"`          // 角色展示名称
	Role     string `gorm:"column:role;type:varchar(120);not null" json:"role"`          // 角色职责/定位，如「主讲」「提问」
	RoleType string `gorm:"column:role_type;type:varchar(32);not null" json:"role_type"` // teacher | assistant | student

	SystemPrompt string `gorm:"column:system_prompt;type:text;not null" json:"system_prompt"` // 本课程实际使用的完整系统提示词快照
	VoiceID      string `gorm:"column:voice_id;type:varchar(120);not null" json:"voice_id"`   // 本课程实际使用的音色 ID
	Avatar       string `gorm:"column:avatar;type:varchar(255);not null" json:"avatar"`       // 六个默认头像之一的资源路径或 key
	Color        string `gorm:"column:color;type:varchar(16);not null" json:"color"`          // 界面主题色快照，如 #52c41a

	SortOrder int32 `gorm:"column:sort_order;not null" json:"sort_order"` // 课堂中的角色顺序，>= 0
}

// TableName 返回表名。
func (ClassroomAgent) TableName() string { return "classroom_agents" }
