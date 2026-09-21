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

	ConversationID   uint64          `gorm:"column:conversation_id;not null;uniqueIndex:conversation_messages_conversation_sequence_key;comment:所属会话 ID，指向 classroom_conversations.id；会话删除时级联删除" json:"conversation_id"`
	SequenceNo       int64           `gorm:"column:sequence_no;not null;uniqueIndex:conversation_messages_conversation_sequence_key;check:conversation_messages_sequence_no_check,sequence_no >= 1;comment:会话内的消息序号，从 1 开始递增；与 conversation_id 组成唯一约束" json:"sequence_no"`
	SenderType       string          `gorm:"column:sender_type;type:varchar(16);not null;check:conversation_messages_sender_type_check,sender_type IN ('user', 'agent', 'system');comment:发言方类型，取值 user（用户）/ agent（课堂角色）/ system（系统）" json:"sender_type"`
	ClassroomAgentID *uint64         `gorm:"column:classroom_agent_id;comment:发言角色在本课程的实例，指向 classroom_agents.id；用户与系统消息为空，角色被移出课程后置空" json:"classroom_agent_id"`
	SenderSnapshot   json.RawMessage `gorm:"column:sender_snapshot;type:jsonb;not null;default:'{}';comment:发言时的发言者快照 JSON（含当时的名字）；角色后来改了名，历史消息里显示的仍是当时那个称呼" json:"sender_snapshot"`
	Content          string          `gorm:"column:content;type:text;not null;comment:消息正文" json:"content"`
	Status           string          `gorm:"column:status;type:varchar(32);not null;check:conversation_messages_status_check,status IN ('streaming', 'completed', 'failed', 'cancelled');comment:消息状态，取值 streaming（正在流式接收）/ completed（完成）/ failed（失败）/ cancelled（用户取消）" json:"status"`
	ReplyToMessageID *uint64         `gorm:"column:reply_to_message_id;comment:被回复的消息 ID，自引用；不是回复时为空" json:"reply_to_message_id"`
	TokenCount       int32           `gorm:"column:token_count;not null;default:0;check:conversation_messages_token_count_check,token_count >= 0;comment:这条消息消耗的 token 数；0 表示没有统计" json:"token_count"`
	Metadata         json.RawMessage `gorm:"column:metadata;type:jsonb;not null;default:'{}';comment:消息扩展信息 JSON" json:"metadata"`

	// 以下关联仅供 AutoMigrate 建外键（CASCADE / SET NULL / 自引用 SET NULL）。
	// 业务代码禁止给它们赋值或 Preload。
	Conversation   *ClassroomConversation `gorm:"foreignKey:ConversationID;constraint:conversation_messages_conversation_id_fkey,OnDelete:CASCADE" json:"-"`
	ClassroomAgent *ClassroomAgent        `gorm:"foreignKey:ClassroomAgentID;constraint:conversation_messages_classroom_agent_id_fkey,OnDelete:SET NULL" json:"-"`
	ReplyToMessage *ConversationMessage   `gorm:"foreignKey:ReplyToMessageID;constraint:conversation_messages_reply_to_message_id_fkey,OnDelete:SET NULL" json:"-"`
}

func (ConversationMessage) TableName() string { return "conversation_messages" }
