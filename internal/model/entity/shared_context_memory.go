package entity

import "time"

const (
	MemoryScopeConversation = "conversation"
	MemoryScopeClassroom    = "classroom"

	MemoryTypeFact          = "fact"
	MemoryTypeDecision      = "decision"
	MemoryTypeLearningState = "learning_state"
	MemoryTypePreference    = "preference"
	MemoryTypeOpenQuestion  = "open_question"

	MemoryStatusActive     = "active"
	MemoryStatusSuperseded = "superseded"
	MemoryStatusRetracted  = "retracted"
)

// SharedContextMemory 是同一课堂内所有 Agent 可读取的结构化记忆。
//
// 约束全部由字段上的 tag 声明，AutoMigrate 建：五个 CHECK、五条外键
// （课堂与会话级联删除，来源消息/回合/取代者置 NULL，取代者是自引用）。
// 两个复合索引的列序与字段声明顺序不一致，所以用显式 priority 固定：
// idx_shared_memories_classroom (classroom_id, scope, status, importance DESC)、
// idx_shared_memories_conversation (conversation_id, status, importance DESC)。
type SharedContextMemory struct {
	BaseModel

	ClassroomID     uint64     `gorm:"column:classroom_id;not null;index:idx_shared_memories_classroom,priority:1" json:"classroom_id"`
	ConversationID  *uint64    `gorm:"column:conversation_id;index:idx_shared_memories_conversation,priority:1" json:"conversation_id"`
	Scope           string     `gorm:"column:scope;type:varchar(16);not null;index:idx_shared_memories_classroom,priority:2;check:shared_context_memories_scope_check,(scope = 'classroom' AND conversation_id IS NULL) OR (scope = 'conversation' AND conversation_id IS NOT NULL)" json:"scope"`
	MemoryType      string     `gorm:"column:memory_type;type:varchar(32);not null;check:shared_context_memories_type_check,memory_type IN ('fact', 'decision', 'learning_state', 'preference', 'open_question')" json:"memory_type"`
	Content         string     `gorm:"column:content;type:text;not null" json:"content"`
	Importance      int16      `gorm:"column:importance;not null;default:3;index:idx_shared_memories_classroom,priority:4,sort:DESC;index:idx_shared_memories_conversation,priority:3,sort:DESC;check:shared_context_memories_importance_check,importance BETWEEN 1 AND 5" json:"importance"`
	SourceMessageID *uint64    `gorm:"column:source_message_id" json:"source_message_id"`
	SourceTurnID    *uint64    `gorm:"column:source_turn_id" json:"source_turn_id"`
	Status          string     `gorm:"column:status;type:varchar(16);not null;default:active;index:idx_shared_memories_classroom,priority:3;index:idx_shared_memories_conversation,priority:2;check:shared_context_memories_status_check,status IN ('active', 'superseded', 'retracted')" json:"status"`
	SupersededByID  *uint64    `gorm:"column:superseded_by_id;check:shared_context_memories_not_self_superseded_check,superseded_by_id IS NULL OR superseded_by_id <> id" json:"superseded_by_id"`
	ExpiresAt       *time.Time `gorm:"column:expires_at" json:"expires_at"`
	LastUsedAt      *time.Time `gorm:"column:last_used_at" json:"last_used_at"`

	// 以下关联仅供 AutoMigrate 建外键（CASCADE / CASCADE / SET NULL / SET NULL / 自引用 SET NULL）。
	// 业务代码禁止给它们赋值或 Preload。
	Classroom     *Classroom             `gorm:"foreignKey:ClassroomID;constraint:shared_context_memories_classroom_id_fkey,OnDelete:CASCADE" json:"-"`
	Conversation  *ClassroomConversation `gorm:"foreignKey:ConversationID;constraint:shared_context_memories_conversation_id_fkey,OnDelete:CASCADE" json:"-"`
	SourceMessage *ConversationMessage   `gorm:"foreignKey:SourceMessageID;constraint:shared_context_memories_source_message_id_fkey,OnDelete:SET NULL" json:"-"`
	SourceTurn    *AgentTurn             `gorm:"foreignKey:SourceTurnID;constraint:shared_context_memories_source_turn_id_fkey,OnDelete:SET NULL" json:"-"`
	SupersededBy  *SharedContextMemory   `gorm:"foreignKey:SupersededByID;constraint:shared_context_memories_superseded_by_id_fkey,OnDelete:SET NULL" json:"-"`
}

func (SharedContextMemory) TableName() string { return "shared_context_memories" }
