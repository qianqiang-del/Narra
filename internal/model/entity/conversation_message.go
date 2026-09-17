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
//
// 约束全部由字段上的 tag 声明，AutoMigrate 建：四个 CHECK、
// UNIQUE (conversation_id, sequence_no)、三条外键（conversation 级联删除，
// 发言角色与被回复消息置 NULL；回复关系是自引用）。
type ConversationMessage struct {
	BaseModel

	ConversationID   uint64          `gorm:"column:conversation_id;not null;uniqueIndex:conversation_messages_conversation_sequence_key" json:"conversation_id"`
	SequenceNo       int64           `gorm:"column:sequence_no;not null;uniqueIndex:conversation_messages_conversation_sequence_key;check:conversation_messages_sequence_no_check,sequence_no >= 1" json:"sequence_no"`
	SenderType       string          `gorm:"column:sender_type;type:varchar(16);not null;check:conversation_messages_sender_type_check,sender_type IN ('user', 'agent', 'system')" json:"sender_type"`
	ClassroomAgentID *uint64         `gorm:"column:classroom_agent_id" json:"classroom_agent_id"`
	SenderSnapshot   json.RawMessage `gorm:"column:sender_snapshot;type:jsonb;not null;default:'{}'" json:"sender_snapshot"`
	Content          string          `gorm:"column:content;type:text;not null" json:"content"`
	Status           string          `gorm:"column:status;type:varchar(32);not null;check:conversation_messages_status_check,status IN ('streaming', 'completed', 'failed', 'cancelled')" json:"status"`
	ReplyToMessageID *uint64         `gorm:"column:reply_to_message_id" json:"reply_to_message_id"`
	TokenCount       int32           `gorm:"column:token_count;not null;default:0;check:conversation_messages_token_count_check,token_count >= 0" json:"token_count"`
	Metadata         json.RawMessage `gorm:"column:metadata;type:jsonb;not null;default:'{}'" json:"metadata"`

	// 以下关联仅供 AutoMigrate 建外键（CASCADE / SET NULL / 自引用 SET NULL）。
	// 业务代码禁止给它们赋值或 Preload。
	Conversation   *ClassroomConversation `gorm:"foreignKey:ConversationID;constraint:conversation_messages_conversation_id_fkey,OnDelete:CASCADE" json:"-"`
	ClassroomAgent *ClassroomAgent        `gorm:"foreignKey:ClassroomAgentID;constraint:conversation_messages_classroom_agent_id_fkey,OnDelete:SET NULL" json:"-"`
	ReplyToMessage *ConversationMessage   `gorm:"foreignKey:ReplyToMessageID;constraint:conversation_messages_reply_to_message_id_fkey,OnDelete:SET NULL" json:"-"`
}

func (ConversationMessage) TableName() string { return "conversation_messages" }
