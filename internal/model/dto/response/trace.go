package response

import "time"

// DiscussionRun 是用户可见的单次讨论运行摘要。
type DiscussionRun struct {
	ID            uint64     `json:"id"`
	TraceID       string     `json:"trace_id"`
	Status        string     `json:"status"`
	StopReason    *string    `json:"stop_reason,omitempty"`
	ModelID       string     `json:"model_id,omitempty"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
	DurationMS    *int64     `json:"duration_ms,omitempty"`
	TurnCount     int        `json:"turn_count"`
	InputTokens   int64      `json:"input_tokens"`
	OutputTokens  int64      `json:"output_tokens"`
	TotalTokens   int64      `json:"total_tokens"`
	TokenSource   string     `json:"token_source"`
	EstimatedCost *float64   `json:"estimated_cost,omitempty"`
	Currency      string     `json:"currency,omitempty"`
	ErrorMessage  string     `json:"error_message,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// DiscussionTrace 是一条运行的脱敏链路详情。
type DiscussionTrace struct {
	Run   DiscussionRun `json:"run"`
	Spans []TraceSpan   `json:"spans"`
}

type TraceSpan struct {
	ID            uint64     `json:"id"`
	SpanID        string     `json:"span_id"`
	ParentSpanID  *string    `json:"parent_span_id,omitempty"`
	Kind          string     `json:"kind"`
	Name          string     `json:"name"`
	Status        string     `json:"status"`
	StartedAt     time.Time  `json:"started_at"`
	EndedAt       *time.Time `json:"ended_at,omitempty"`
	DurationMS    *int64     `json:"duration_ms,omitempty"`
	TurnID        *uint64    `json:"turn_id,omitempty"`
	InputTokens   int64      `json:"input_tokens,omitempty"`
	OutputTokens  int64      `json:"output_tokens,omitempty"`
	Attempt       int        `json:"attempt,omitempty"`
	TurnNo        int        `json:"turn_no,omitempty"`
	Turns         int        `json:"turns,omitempty"`
	AgentName     string     `json:"agent_name,omitempty"`
	NextAction    string     `json:"next_action,omitempty"`
	StopReason    string     `json:"stop_reason,omitempty"`
	InputSummary  string     `json:"input_summary,omitempty"`
	OutputSummary string     `json:"output_summary,omitempty"`
	ErrorMessage  string     `json:"error_message,omitempty"`
}
