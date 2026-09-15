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
type AgentTurn struct {
	BaseModel

	RunID            uint64          `gorm:"column:run_id;not null" json:"run_id"`
	TurnNo           int16           `gorm:"column:turn_no;not null" json:"turn_no"`
	ClassroomAgentID *uint64         `gorm:"column:classroom_agent_id" json:"classroom_agent_id"`
	AgentSnapshot    json.RawMessage `gorm:"column:agent_snapshot;type:jsonb;not null;default:'{}'" json:"agent_snapshot"`
	OutputMessageID  *uint64         `gorm:"column:output_message_id" json:"output_message_id"`
	Status           string          `gorm:"column:status;type:varchar(32);not null" json:"status"`
	SelectionReason  *string         `gorm:"column:selection_reason;type:text" json:"selection_reason"`
	NextAction       *string         `gorm:"column:next_action;type:varchar(32)" json:"next_action"`
	Model            *string         `gorm:"column:model;type:varchar(120)" json:"model"`
	InputTokens      int32           `gorm:"column:input_tokens;not null;default:0" json:"input_tokens"`
	OutputTokens     int32           `gorm:"column:output_tokens;not null;default:0" json:"output_tokens"`
	StartedAt        *time.Time      `gorm:"column:started_at" json:"started_at"`
	FinishedAt       *time.Time      `gorm:"column:finished_at" json:"finished_at"`
	ErrorMessage     *string         `gorm:"column:error_message;type:text" json:"error_message"`
}

func (AgentTurn) TableName() string { return "agent_turns" }
