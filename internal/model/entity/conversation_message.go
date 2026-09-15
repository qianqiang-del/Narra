package entity

import "encoding/json"

const (
	MessageSenderUser   = "user"
	MessageSenderAgent  = "agent"
	MessageSenderSystem = "system"

	MessageStatusStreaming = "streaming"
	MessageStatusCompleted = "completed"
	MessageStatusFailed    = "failed"
	MessageStatusCancelled = "cancelled"
)

// ConversationMessage 是课堂对话中对用户可见的完整消息。
type ConversationMessage struct {
	BaseModel

	ConversationID   uint64          `gorm:"column:conversation_id;not null" json:"conversation_id"`
	SequenceNo       int64           `gorm:"column:sequence_no;not null" json:"sequence_no"`
	SenderType       string          `gorm:"column:sender_type;type:varchar(16);not null" json:"sender_type"`
	ClassroomAgentID *uint64         `gorm:"column:classroom_agent_id" json:"classroom_agent_id"`
	SenderSnapshot   json.RawMessage `gorm:"column:sender_snapshot;type:jsonb;not null;default:'{}'" json:"sender_snapshot"`
	Content          string          `gorm:"column:content;type:text;not null" json:"content"`
	Status           string          `gorm:"column:status;type:varchar(32);not null" json:"status"`
	ReplyToMessageID *uint64         `gorm:"column:reply_to_message_id" json:"reply_to_message_id"`
	TokenCount       int32           `gorm:"column:token_count;not null;default:0" json:"token_count"`
	Metadata         json.RawMessage `gorm:"column:metadata;type:jsonb;not null;default:'{}'" json:"metadata"`
}

func (ConversationMessage) TableName() string { return "conversation_messages" }
