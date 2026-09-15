package entity

import (
	"encoding/json"
	"time"
)

const (
	ConversationEventRunStarted       = "run.started"
	ConversationEventDirectorDecision = "director.decision"
	ConversationEventAgentStarted     = "agent.started"
	ConversationEventMessageDelta     = "message.delta"
	ConversationEventMessageCompleted = "message.completed"
	ConversationEventAgentCompleted   = "agent.completed"
	ConversationEventRunWaitingUser   = "run.waiting_user"
	ConversationEventRunCompleted     = "run.completed"
	ConversationEventRunFailed        = "run.failed"
)

// ConversationEvent 是供 SSE 推送和断线续传使用的追加式事件。
type ConversationEvent struct {
	ID             uint64          `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	ConversationID uint64          `gorm:"column:conversation_id;not null" json:"conversation_id"`
	RunID          *uint64         `gorm:"column:run_id" json:"run_id"`
	TurnID         *uint64         `gorm:"column:turn_id" json:"turn_id"`
	SequenceNo     int64           `gorm:"column:sequence_no;not null" json:"sequence_no"`
	EventType      string          `gorm:"column:event_type;type:varchar(64);not null" json:"event_type"`
	Payload        json.RawMessage `gorm:"column:payload;type:jsonb;not null;default:'{}'" json:"payload"`
	CreatedAt      time.Time       `gorm:"column:created_at;not null;autoCreateTime" json:"created_at"`
	ExpiresAt      time.Time       `gorm:"column:expires_at;not null" json:"expires_at"`
}

func (ConversationEvent) TableName() string { return "conversation_events" }
