// Package discussion 实现课堂里的多 Agent 讨论编排。
//
// 一次讨论的完整链路：
//
//	用户发一句话 → 开一趟编排运行（orchestration_runs）
//	  → 逐个角色发言，每次发言是一轮"回合"（agent_turns）
//	  → 每个回合产出一条对用户可见的消息（conversation_messages）
//	  → 满足停止条件后收尾，记下为什么停
//
// 本包的边界：
//   - **不认识大模型**：通过 Model 接口注入。测试和本地联调用 FakeModel，不花一分钱。
//   - **不认识课堂角色从哪来**：参与者列表由调用方传进来，本包不查 classroom_agents 表
//     （那是别的模块的表，将来由上层组装好再传下来）。
//   - **不管 SSE**：事件表的写入属于另一条链路，等与成员 B 对齐契约后再接。
//
// 本包只做一件事：决定谁在什么时候说话，并把过程如实记录到库里。
package discussion

import (
	"encoding/json"
)

// 一次运行的编排器版本号，写进 orchestration_runs.orchestrator_version。
// 将来编排逻辑换代时改这里，历史运行记录仍能看出当时跑的是哪一版。
const orchestratorVersion = "discussion-v1"

const (
	// defaultMaxTurns 是调用方没指定时的默认上限。
	defaultMaxTurns = 6

	// minMaxTurns / maxMaxTurns 与数据库 CHECK (max_turns BETWEEN 1 AND 50) 对齐。
	// 在 Go 侧先拦一道：否则超出范围要到 INSERT 才报错，而那时事务已经开了、
	// 报出来的也只是一句约束冲突，看不出是参数问题。
	minMaxTurns = 1
	maxMaxTurns = 50
)

// Participant 是圆桌上的一个课堂角色。
//
// 只带"这次讨论需要知道的"信息：是谁（ID）、叫什么（显示名）、什么身份、人设是什么。
// 角色的其他字段（头像、音色等）本包用不到，不往这里塞。
type Participant struct {
	ClassroomAgentID uint64 // classroom_agents.id，写进回合与消息的归属字段
	Name             string // 显示名，写入 sender_snapshot / agent_snapshot
	Role             string // teacher / student 等
	Persona          string // 人设提示词
}

// Request 是一次编排的输入。
type Request struct {
	ConversationID   uint64        // 讨论发生在哪个对话里
	TriggerMessageID uint64        // 哪条用户消息触发了这次讨论
	Participants     []Participant // 圆桌上有谁，顺序即默认发言顺序
	MaxTurns         int           // 最多几轮，0 表示用默认值
}

// Result 是一次编排的结果，供调用方展示与日志使用。
type Result struct {
	RunID      uint64        // 本次运行的 ID
	TraceID    string        // 追踪号，排查问题时用它串起整条链路
	Status     string        // 运行终态：completed / failed
	StopReason string        // 为什么停：completed / max_turns / error
	Turns      []TurnOutcome // 每个回合的结果，按发言顺序
}

// TurnOutcome 是一个回合的结果。
type TurnOutcome struct {
	TurnID       uint64
	TurnNo       int16
	AgentName    string
	MessageID    uint64 // 这个回合产出的可见消息
	Content      string
	OutputTokens int32
	NextAction   string // 这一轮给出的下一步动作，供调用方和日志使用
}

// snapshot 组装写进快照列的 JSON。
//
// 快照的意义是"发言当时角色长什么样"：角色以后改了名字，历史消息显示的仍是当时那份。
// 所以这里抄的是此刻的值，而不是存一个指向角色的 ID —— ID 会跟着角色一起变。
// 第一版只抄名字和身份；等前端要显示头像时再加字段（加字段不影响已存的历史）。
func (p Participant) snapshot() json.RawMessage {
	encoded, err := json.Marshal(map[string]string{"name": p.Name, "role": p.Role})
	if err != nil {
		// 对 string 只有无效 UTF-8 才会失败。退回空对象比写进一段坏 JSON 好：
		// 列上本来就有 default '{}'，空对象是合法值。
		return json.RawMessage("{}")
	}
	return encoded
}
