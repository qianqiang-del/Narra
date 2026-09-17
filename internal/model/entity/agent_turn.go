package entity

import (
	"encoding/json"
	"time"
)

const (
	AgentTurnStatusScheduled = "scheduled"
	AgentTurnStatusRunning   = "running"
	AgentTurnStatusCompleted = "completed"
	AgentTurnStatusFailed    = "failed"
	AgentTurnStatusCancelled = "cancelled"

	AgentTurnActionContinue    = "continue"
	AgentTurnActionSwitchAgent = "switch_agent"
	AgentTurnActionAskUser     = "ask_user"
	AgentTurnActionEnd         = "end"
)

// AgentTurn 是 Director 选择一个课堂角色后产生的一次 Agent 执行回合。
//
// UNIQUE (output_message_id) 由 OutputMessageID 上的 unique tag 声明；理由同
// OrchestrationRun.TraceID——单列唯一约束归 AutoMigrate，不能写进 SQL。
// 其余约束同样全部由 tag 声明：四个 CHECK、UNIQUE (run_id, turn_no)、
// 三条外键（run 级联删除，课堂角色与产出消息置 NULL）。
type AgentTurn struct {
	BaseModel

	RunID            uint64          `gorm:"column:run_id;not null;uniqueIndex:agent_turns_run_turn_key" json:"run_id"`
	TurnNo           int16           `gorm:"column:turn_no;not null;uniqueIndex:agent_turns_run_turn_key;check:agent_turns_counts_check,turn_no >= 1 AND input_tokens >= 0 AND output_tokens >= 0" json:"turn_no"`
	ClassroomAgentID *uint64         `gorm:"column:classroom_agent_id" json:"classroom_agent_id"`
	AgentSnapshot    json.RawMessage `gorm:"column:agent_snapshot;type:jsonb;not null;default:'{}'" json:"agent_snapshot"`
	OutputMessageID  *uint64         `gorm:"column:output_message_id;unique" json:"output_message_id"`
	Status           string          `gorm:"column:status;type:varchar(32);not null;check:agent_turns_status_check,status IN ('scheduled', 'running', 'completed', 'failed', 'cancelled')" json:"status"`
	SelectionReason  *string         `gorm:"column:selection_reason;type:text" json:"selection_reason"`
	NextAction       *string         `gorm:"column:next_action;type:varchar(32);check:agent_turns_next_action_check,next_action IS NULL OR next_action IN ('continue', 'switch_agent', 'ask_user', 'end')" json:"next_action"`
	Model            *string         `gorm:"column:model;type:varchar(120)" json:"model"`
	InputTokens      int32           `gorm:"column:input_tokens;not null;default:0" json:"input_tokens"`
	OutputTokens     int32           `gorm:"column:output_tokens;not null;default:0" json:"output_tokens"`
	StartedAt        *time.Time      `gorm:"column:started_at;check:agent_turns_time_range_check,finished_at IS NULL OR started_at IS NULL OR finished_at >= started_at" json:"started_at"`
	FinishedAt       *time.Time      `gorm:"column:finished_at" json:"finished_at"`
	ErrorMessage     *string         `gorm:"column:error_message;type:text" json:"error_message"`

	// 以下关联仅供 AutoMigrate 建外键（CASCADE / SET NULL / SET NULL）。
	// 业务代码禁止给它们赋值或 Preload。
	Run            *OrchestrationRun    `gorm:"foreignKey:RunID;constraint:agent_turns_run_id_fkey,OnDelete:CASCADE" json:"-"`
	ClassroomAgent *ClassroomAgent      `gorm:"foreignKey:ClassroomAgentID;constraint:agent_turns_classroom_agent_id_fkey,OnDelete:SET NULL" json:"-"`
	OutputMessage  *ConversationMessage `gorm:"foreignKey:OutputMessageID;constraint:agent_turns_output_message_id_fkey,OnDelete:SET NULL" json:"-"`
}

func (AgentTurn) TableName() string { return "agent_turns" }
