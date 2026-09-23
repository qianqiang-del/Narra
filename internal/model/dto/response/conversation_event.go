package response

import (
	"encoding/json"
	"time"
)

// ConversationEvent 是 SSE 事件流上的一条事件，也是 data 行的形状。
//
// 它刻意做成"自描述"的：sequence_no 同时是 SSE 的 id（断线续传的起点），
// event_type 与 SSE 的 event 行重复一份 —— 只读 data 的客户端（日志、回放工具）
// 不必再解析 SSE 帧就能知道这条是什么。
//
// payload 由生产者（编排 / 工作台）定义，SSE 层不认识它的内容、原样透传；
// 事件类型见 entity 的 ConversationEvent* 常量。
type ConversationEvent struct {
	SequenceNo int64           `json:"sequence_no"`       // 对话内序号，从 1 开始且单调递增（允许空洞）
	EventType  string          `json:"event_type"`        // 事件类型，如 run.started、message.delta
	RunID      *uint64         `json:"run_id,omitempty"`  // 所属编排运行；事件与运行无关时为空
	TurnID     *uint64         `json:"turn_id,omitempty"` // 所属 Agent 回合；事件与回合无关时为空
	Payload    json.RawMessage `json:"payload"`           // 生产者自定义的事件载荷
	CreatedAt  time.Time       `json:"created_at"`        // 事件产生时间
}
