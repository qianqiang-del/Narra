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
//
// 约束全部由字段上的 tag 声明，AutoMigrate 建：sequence_no 的 CHECK、
// UNIQUE (conversation_id, sequence_no)、三条外键（会话级联删除，run 与 turn 置 NULL）。
// ExpiresAt 的默认值（写入后 7 天过期）也由 default tag 声明—— PostgreSQL 会把
// 表达式规范化存储，AutoMigrate 的默认值比较可能因此每次启动都重发一次
// ALTER COLUMN SET DEFAULT，幂等无害，只是日志多一行。
type ConversationEvent struct {
	ID             uint64          `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	ConversationID uint64          `gorm:"column:conversation_id;not null;uniqueIndex:conversation_events_conversation_sequence_key" json:"conversation_id"`
	RunID          *uint64         `gorm:"column:run_id" json:"run_id"`
	TurnID         *uint64         `gorm:"column:turn_id" json:"turn_id"`
	SequenceNo     int64           `gorm:"column:sequence_no;not null;uniqueIndex:conversation_events_conversation_sequence_key;check:conversation_events_sequence_no_check,sequence_no >= 1" json:"sequence_no"`
	EventType      string          `gorm:"column:event_type;type:varchar(64);not null" json:"event_type"`
	Payload        json.RawMessage `gorm:"column:payload;type:jsonb;not null;default:'{}'" json:"payload"`
	CreatedAt      time.Time       `gorm:"column:created_at;not null;autoCreateTime" json:"created_at"`
	ExpiresAt      time.Time       `gorm:"column:expires_at;not null;default:(CURRENT_TIMESTAMP + INTERVAL '7 days');index:idx_conversation_events_expires_at" json:"expires_at"`

	// 以下关联仅供 AutoMigrate 建外键（CASCADE / SET NULL / SET NULL）。
	// 业务代码禁止给它们赋值或 Preload。
	Conversation *ClassroomConversation `gorm:"foreignKey:ConversationID;constraint:conversation_events_conversation_id_fkey,OnDelete:CASCADE" json:"-"`
	Run          *OrchestrationRun      `gorm:"foreignKey:RunID;constraint:conversation_events_run_id_fkey,OnDelete:SET NULL" json:"-"`
	Turn         *AgentTurn             `gorm:"foreignKey:TurnID;constraint:conversation_events_turn_id_fkey,OnDelete:SET NULL" json:"-"`
}

func (ConversationEvent) TableName() string { return "conversation_events" }
