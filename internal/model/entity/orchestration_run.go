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
// SQL migration：AutoMigrate 每次启动都会对账，发现库里唯一而实体上没标 unique，就按自己算的
// 名字 uni_orchestration_runs_trace_id 去删，删不掉直接 panic。见 migrations/README.md。
//
// 其余约束同样全部由 tag 声明：六个 CHECK、UNIQUE (trigger_message_id, attempt_no)、
// 两条外键（conversation 与触发消息均级联删除），以及「按会话查最近编排」的复合索引
// idx_orchestration_runs_recent——它的第二列是 created_at，所以下方遮蔽了
// BaseModel.CreatedAt 来挂索引（遮蔽安全，见 KnowledgeDocument.UpdatedAt 的注释）。
type OrchestrationRun struct {
	BaseModel

	ConversationID      uint64          `gorm:"column:conversation_id;not null;index:idx_orchestration_runs_recent" json:"conversation_id"`
	TriggerMessageID    uint64          `gorm:"column:trigger_message_id;not null;uniqueIndex:orchestration_runs_trigger_attempt_key" json:"trigger_message_id"`
	AttemptNo           int16           `gorm:"column:attempt_no;not null;default:1;uniqueIndex:orchestration_runs_trigger_attempt_key;check:orchestration_runs_attempt_no_check,attempt_no >= 1" json:"attempt_no"`
	TraceID             string          `gorm:"column:trace_id;type:varchar(32);not null;unique;check:orchestration_runs_trace_id_format_check,trace_id ~ '^[0-9a-f]{32}$'" json:"trace_id"`
	Status              string          `gorm:"column:status;type:varchar(32);not null;check:orchestration_runs_status_check,status IN ('queued', 'running', 'waiting_user', 'completed', 'failed', 'cancelled')" json:"status"`
	MaxTurns            int16           `gorm:"column:max_turns;not null;check:orchestration_runs_max_turns_check,max_turns BETWEEN 1 AND 50" json:"max_turns"`
	StopReason          *string         `gorm:"column:stop_reason;type:varchar(32);check:orchestration_runs_stop_reason_check,stop_reason IS NULL OR stop_reason IN ('completed', 'waiting_user', 'max_turns', 'error', 'cancelled')" json:"stop_reason"`
	OrchestratorVersion string          `gorm:"column:orchestrator_version;type:varchar(40);not null" json:"orchestrator_version"`
	ConfigSnapshot      json.RawMessage `gorm:"column:config_snapshot;type:jsonb;not null;default:'{}'" json:"config_snapshot"`
	StartedAt           *time.Time      `gorm:"column:started_at;check:orchestration_runs_time_range_check,finished_at IS NULL OR started_at IS NULL OR finished_at >= started_at" json:"started_at"`
	FinishedAt          *time.Time      `gorm:"column:finished_at" json:"finished_at"`
	ErrorMessage        *string         `gorm:"column:error_message;type:text" json:"error_message"`

	// CreatedAt 遮蔽 BaseModel 的同名字段，只为给 idx_orchestration_runs_recent 挂第二列。
	CreatedAt time.Time `gorm:"column:created_at;not null;autoCreateTime;index:idx_orchestration_runs_recent,sort:DESC" json:"created_at"`

	// Conversation / TriggerMessage 仅供 AutoMigrate 建外键（均 ON DELETE CASCADE）。
	// 业务代码禁止给它们赋值或 Preload。
	Conversation   *ClassroomConversation `gorm:"foreignKey:ConversationID;constraint:orchestration_runs_conversation_id_fkey,OnDelete:CASCADE" json:"-"`
	TriggerMessage *ConversationMessage   `gorm:"foreignKey:TriggerMessageID;constraint:orchestration_runs_trigger_message_id_fkey,OnDelete:CASCADE" json:"-"`
}

func (OrchestrationRun) TableName() string { return "orchestration_runs" }
