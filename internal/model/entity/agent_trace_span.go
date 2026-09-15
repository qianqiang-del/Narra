package entity

import (
	"encoding/json"
	"time"
)

const (
	TraceSpanKindOrchestration = "orchestration"
	TraceSpanKindDirector      = "director"
	TraceSpanKindAgent         = "agent"
	TraceSpanKindModel         = "model"
	TraceSpanKindTool          = "tool"
	TraceSpanKindMemory        = "memory"
	TraceSpanKindSSE           = "sse"

	TraceSpanStatusRunning   = "running"
	TraceSpanStatusOK        = "ok"
	TraceSpanStatusError     = "error"
	TraceSpanStatusCancelled = "cancelled"
)

// AgentTraceSpan 保存本地可观测链路中的一个步骤。
type AgentTraceSpan struct {
	BaseModel

	TraceID       string          `gorm:"column:trace_id;type:varchar(32);not null" json:"trace_id"`
	SpanID        string          `gorm:"column:span_id;type:varchar(16);not null" json:"span_id"`
	ParentSpanID  *string         `gorm:"column:parent_span_id;type:varchar(16)" json:"parent_span_id"`
	RunID         uint64          `gorm:"column:run_id;not null" json:"run_id"`
	TurnID        *uint64         `gorm:"column:turn_id" json:"turn_id"`
	Kind          string          `gorm:"column:kind;type:varchar(32);not null" json:"kind"`
	Name          string          `gorm:"column:name;type:varchar(120);not null" json:"name"`
	Status        string          `gorm:"column:status;type:varchar(16);not null" json:"status"`
	StartedAt     time.Time       `gorm:"column:started_at;not null" json:"started_at"`
	EndedAt       *time.Time      `gorm:"column:ended_at" json:"ended_at"`
	Attributes    json.RawMessage `gorm:"column:attributes;type:jsonb;not null;default:'{}'" json:"attributes"`
	InputSummary  *string         `gorm:"column:input_summary;type:text" json:"input_summary"`
	OutputSummary *string         `gorm:"column:output_summary;type:text" json:"output_summary"`
	ErrorMessage  *string         `gorm:"column:error_message;type:text" json:"error_message"`
	ExpiresAt     time.Time       `gorm:"column:expires_at;not null" json:"expires_at"`
}

func (AgentTraceSpan) TableName() string { return "agent_trace_spans" }
