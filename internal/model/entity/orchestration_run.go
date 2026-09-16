package entity

import (
	"encoding/json"
	"time"
)

const (
	RunStatusQueued      = "queued"
	RunStatusRunning     = "running"
	RunStatusWaitingUser = "waiting_user"
	RunStatusCompleted   = "completed"
	RunStatusFailed      = "failed"
	RunStatusCancelled   = "cancelled"

	RunStopCompleted = "completed"
	RunStopWaiting   = "waiting_user"
	RunStopMaxTurns  = "max_turns"
	RunStopError     = "error"
	RunStopCancelled = "cancelled"
)

// OrchestrationRun 是一条用户消息触发的一次完整多 Agent 编排。
//
// UNIQUE (trace_id) 由 TraceID 上的 unique tag 声明，AutoMigrate 负责建。单列唯一约束不能写进
// 0003 的 SQL：AutoMigrate 每次启动都会对账，发现库里唯一而实体上没标 unique，就按自己算的
// 名字 uni_orchestration_runs_trace_id 去删，删不掉直接 panic。见 migrations/README.md。
type OrchestrationRun struct {
	BaseModel

	ConversationID      uint64          `gorm:"column:conversation_id;not null" json:"conversation_id"`
	TriggerMessageID    uint64          `gorm:"column:trigger_message_id;not null" json:"trigger_message_id"`
	AttemptNo           int16           `gorm:"column:attempt_no;not null;default:1" json:"attempt_no"`
	TraceID             string          `gorm:"column:trace_id;type:varchar(32);not null;unique" json:"trace_id"`
	Status              string          `gorm:"column:status;type:varchar(32);not null" json:"status"`
	MaxTurns            int16           `gorm:"column:max_turns;not null" json:"max_turns"`
	StopReason          *string         `gorm:"column:stop_reason;type:varchar(32)" json:"stop_reason"`
	OrchestratorVersion string          `gorm:"column:orchestrator_version;type:varchar(40);not null" json:"orchestrator_version"`
	ConfigSnapshot      json.RawMessage `gorm:"column:config_snapshot;type:jsonb;not null;default:'{}'" json:"config_snapshot"`
	StartedAt           *time.Time      `gorm:"column:started_at" json:"started_at"`
	FinishedAt          *time.Time      `gorm:"column:finished_at" json:"finished_at"`
	ErrorMessage        *string         `gorm:"column:error_message;type:text" json:"error_message"`
}

func (OrchestrationRun) TableName() string { return "orchestration_runs" }
