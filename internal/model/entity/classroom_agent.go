package entity

// ClassroomAgent 课堂与角色的关联，对应表 classroom_agents。
type ClassroomAgent struct {
	BaseModel

	ClassroomID uint64 `gorm:"column:classroom_id;not null;uniqueIndex:classroom_agents_classroom_id_agent_id_key;comment:所属课程 ID，指向 classrooms.id；课程删除时级联删除" json:"classroom_id"`                                      // 所属课程；级联删除
	AgentID     uint64 `gorm:"column:agent_id;not null;uniqueIndex:classroom_agents_classroom_id_agent_id_key;index:idx_classroom_agents_agent_id;comment:使用的角色 ID，指向 preset_agents.id；仍被课程引用的角色不允许删除" json:"agent_id"` // 指向 preset_agents.id

	// Classroom / Agent 仅供 AutoMigrate 建外键（分别 ON DELETE CASCADE / RESTRICT）。
	// 业务代码禁止给它们赋值或 Preload：赋了非空值再保存本行，GORM 会连带 upsert 目标表的行。
	Classroom *Classroom   `gorm:"foreignKey:ClassroomID;constraint:classroom_agents_classroom_id_fkey,OnDelete:CASCADE" json:"-"`
	Agent     *PresetAgent `gorm:"foreignKey:AgentID;constraint:classroom_agents_agent_id_fkey,OnDelete:RESTRICT" json:"-"`

	VoiceID string `gorm:"column:voice_id;type:varchar(120);not null;comment:本课程为这个角色选定的音色 ID；它决定已合成音频里是谁在说话，生成后改不回来，所以每堂课各存一份" json:"voice_id"` // 本课程为这个角色选定的音色；来源见上面的说明，写入前须用 service.IsValidVoiceID 校验
}

// TableName 返回表名。
func (ClassroomAgent) TableName() string { return "classroom_agents" }
