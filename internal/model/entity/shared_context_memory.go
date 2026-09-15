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
type SharedContextMemory struct {
	BaseModel

	ClassroomID     uint64     `gorm:"column:classroom_id;not null" json:"classroom_id"`
	ConversationID  *uint64    `gorm:"column:conversation_id" json:"conversation_id"`
	Scope           string     `gorm:"column:scope;type:varchar(16);not null" json:"scope"`
	MemoryType      string     `gorm:"column:memory_type;type:varchar(32);not null" json:"memory_type"`
	Content         string     `gorm:"column:content;type:text;not null" json:"content"`
	Importance      int16      `gorm:"column:importance;not null;default:3" json:"importance"`
	SourceMessageID *uint64    `gorm:"column:source_message_id" json:"source_message_id"`
	SourceTurnID    *uint64    `gorm:"column:source_turn_id" json:"source_turn_id"`
	Status          string     `gorm:"column:status;type:varchar(16);not null;default:active" json:"status"`
	SupersededByID  *uint64    `gorm:"column:superseded_by_id" json:"superseded_by_id"`
	ExpiresAt       *time.Time `gorm:"column:expires_at" json:"expires_at"`
	LastUsedAt      *time.Time `gorm:"column:last_used_at" json:"last_used_at"`
}

func (SharedContextMemory) TableName() string { return "shared_context_memories" }
