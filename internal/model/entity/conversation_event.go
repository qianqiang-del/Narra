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
	ID             uint64          `gorm:"column:id;primaryKey;autoIncrement;comment:事件主键" json:"id"`
	ConversationID uint64          `gorm:"column:conversation_id;not null;uniqueIndex:conversation_events_conversation_sequence_key;comment:所属会话 ID，指向 classroom_conversations.id；会话删除时级联删除" json:"conversation_id"`
	RunID          *uint64         `gorm:"column:run_id;comment:产生该事件的编排运行 ID，指向 orchestration_runs.id；运行被删除后置空" json:"run_id"`
	TurnID         *uint64         `gorm:"column:turn_id;comment:产生该事件的 Agent 回合 ID，指向 agent_turns.id；回合被删除后置空" json:"turn_id"`
	SequenceNo     int64           `gorm:"column:sequence_no;not null;uniqueIndex:conversation_events_conversation_sequence_key;check:conversation_events_sequence_no_check,sequence_no >= 1;comment:会话内的事件序号，从 1 开始；与 conversation_id 组成唯一约束，断线续传按它接上断点" json:"sequence_no"`
	EventType      string          `gorm:"column:event_type;type:varchar(64);not null;comment:事件类型，如 run.started、director.decision、message.delta、run.completed" json:"event_type"`
	Payload        json.RawMessage `gorm:"column:payload;type:jsonb;not null;default:'{}';comment:事件负载 JSON，内容随 event_type 而定" json:"payload"`
	CreatedAt      time.Time       `gorm:"column:created_at;not null;autoCreateTime;comment:事件产生时间，timestamptz 按 UTC 存" json:"created_at"`
	ExpiresAt      time.Time       `gorm:"column:expires_at;not null;default:(CURRENT_TIMESTAMP + INTERVAL '7 days');index:idx_conversation_events_expires_at;comment:过期时间，默认写入后 7 天；到期由清理任务删除，断线续传只能回溯这么久" json:"expires_at"`

	// 以下关联仅供 AutoMigrate 建外键（CASCADE / SET NULL / SET NULL）。
	// 业务代码禁止给它们赋值或 Preload。
	Conversation *ClassroomConversation `gorm:"foreignKey:ConversationID;constraint:conversation_events_conversation_id_fkey,OnDelete:CASCADE" json:"-"`
	Run          *OrchestrationRun      `gorm:"foreignKey:RunID;constraint:conversation_events_run_id_fkey,OnDelete:SET NULL" json:"-"`
	Turn         *AgentTurn             `gorm:"foreignKey:TurnID;constraint:conversation_events_turn_id_fkey,OnDelete:SET NULL" json:"-"`
}

func (ConversationEvent) TableName() string { return "conversation_events" }
