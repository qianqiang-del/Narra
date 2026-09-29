package response

import (
	"encoding/json"
	"time"
)

// Conversation 是课堂对话的列表与创建响应。
type Conversation struct {
	ID            uint64     `json:"id"`
	ClassroomID   uint64     `json:"classroom_id"`
	Title         string     `json:"title"`
	Type          string     `json:"type"`
	Status        string     `json:"status"`
	LastMessageAt *time.Time `json:"last_message_at"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// ConversationMessage 是对话历史中一条用户可见的消息。
//
// SenderSnapshot 保留发言当时的角色称呼，前端不可用当前角色池覆盖它。
type ConversationMessage struct {
	ID               uint64          `json:"id"`
	ConversationID   uint64          `json:"conversation_id"`
	SequenceNo       int64           `json:"sequence_no"`
	SenderType       string          `json:"sender_type"`
	ClassroomAgentID *uint64         `json:"classroom_agent_id"`
	SenderSnapshot   json.RawMessage `json:"sender_snapshot"`
	Content          string          `json:"content"`
	Status           string          `json:"status"`
	ReplyToMessageID *uint64         `json:"reply_to_message_id"`
	TokenCount       int32           `json:"token_count"`
	Metadata         json.RawMessage `json:"metadata"`
	CreatedAt        time.Time       `json:"created_at"`
}
