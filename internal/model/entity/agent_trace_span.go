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
//
// 约束全部由字段上的 tag 声明，AutoMigrate 建：四个 CHECK、UNIQUE (trace_id, span_id)、
// 两条外键（run 级联删除，turn 置 NULL）、三个索引。ExpiresAt 的默认值
// （写入后 30 天过期）同样由 default tag 声明，注意事项见 ConversationEvent.ExpiresAt。
type AgentTraceSpan struct {
	BaseModel

	TraceID       string          `gorm:"column:trace_id;type:varchar(32);not null;uniqueIndex:agent_trace_spans_trace_span_key;index:idx_agent_trace_spans_trace_started,priority:1;check:agent_trace_spans_id_format_check,trace_id ~ '^[0-9a-f]{32}$' AND span_id ~ '^[0-9a-f]{16}$' AND (parent_span_id IS NULL OR parent_span_id ~ '^[0-9a-f]{16}$')" json:"trace_id"`
	SpanID        string          `gorm:"column:span_id;type:varchar(16);not null;uniqueIndex:agent_trace_spans_trace_span_key" json:"span_id"`
	ParentSpanID  *string         `gorm:"column:parent_span_id;type:varchar(16)" json:"parent_span_id"`
	RunID         uint64          `gorm:"column:run_id;not null;index:idx_agent_trace_spans_run_started,priority:1" json:"run_id"`
	TurnID        *uint64         `gorm:"column:turn_id" json:"turn_id"`
	Kind          string          `gorm:"column:kind;type:varchar(32);not null;check:agent_trace_spans_kind_check,kind IN ('orchestration', 'director', 'agent', 'model', 'tool', 'memory', 'sse')" json:"kind"`
	Name          string          `gorm:"column:name;type:varchar(120);not null" json:"name"`
	Status        string          `gorm:"column:status;type:varchar(16);not null;check:agent_trace_spans_status_check,status IN ('running', 'ok', 'error', 'cancelled')" json:"status"`
	StartedAt     time.Time       `gorm:"column:started_at;not null;index:idx_agent_trace_spans_run_started,priority:2;index:idx_agent_trace_spans_trace_started,priority:2;check:agent_trace_spans_time_range_check,ended_at IS NULL OR ended_at >= started_at" json:"started_at"`
	EndedAt       *time.Time      `gorm:"column:ended_at" json:"ended_at"`
	Attributes    json.RawMessage `gorm:"column:attributes;type:jsonb;not null;default:'{}'" json:"attributes"`
	InputSummary  *string         `gorm:"column:input_summary;type:text" json:"input_summary"`
	OutputSummary *string         `gorm:"column:output_summary;type:text" json:"output_summary"`
	ErrorMessage  *string         `gorm:"column:error_message;type:text" json:"error_message"`
	ExpiresAt     time.Time       `gorm:"column:expires_at;not null;default:(CURRENT_TIMESTAMP + INTERVAL '30 days');index:idx_agent_trace_spans_expires_at" json:"expires_at"`

	// Run / Turn 仅供 AutoMigrate 建外键（CASCADE / SET NULL）。
	// 业务代码禁止给它们赋值或 Preload。
	Run  *OrchestrationRun `gorm:"foreignKey:RunID;constraint:agent_trace_spans_run_id_fkey,OnDelete:CASCADE" json:"-"`
	Turn *AgentTurn        `gorm:"foreignKey:TurnID;constraint:agent_trace_spans_turn_id_fkey,OnDelete:SET NULL" json:"-"`
}

func (AgentTraceSpan) TableName() string { return "agent_trace_spans" }
