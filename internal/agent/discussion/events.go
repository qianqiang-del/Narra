package discussion

// 事件载荷：写进 conversation_events.payload 的 JSON 结构，也是 SSE 流对前端的契约。
//
// 前端有一份对应的 TS 类型（frontend/src/api/conversation.ts），改字段要两边一起改。
// 事件一旦写进库就按当时的样子重放，老事件不会因为代码更新而变形 ——
// 所以只加字段、不改已有字段的含义；确实要改就换事件类型名。
//
// 写入是**尽力而为**的：失败只记日志，不把整场讨论带崩（见 Orchestrator.emit）。

// RunStartedPayload 是 run.started 的载荷：这一趟讨论开始了。
type RunStartedPayload struct {
	TriggerMessageID uint64               `json:"trigger_message_id"` // 哪条用户消息触发的
	MaxTurns         int16                `json:"max_turns"`          // 这一趟最多几轮
	Participants     []ParticipantPayload `json:"participants"`       // 圆桌上有谁
}

// ParticipantPayload 是圆桌成员的展示快照。
//
// 抄的是发言当时的值而不是只存角色 ID：角色以后改了名字，历史事件里显示的
// 仍是当时那份（与消息的 sender_snapshot 同一个理由）。
type ParticipantPayload struct {
	AgentID uint64 `json:"agent_id"` // classroom_agents.id
	Name    string `json:"name"`
	Role    string `json:"role"`
}

// DirectorDecisionPayload 是 director.decision 的载荷：下一个该谁、为什么。
type DirectorDecisionPayload struct {
	TurnNo    int16  `json:"turn_no"`
	AgentID   uint64 `json:"agent_id"`
	AgentName string `json:"agent_name"`
	Reason    string `json:"reason"` // 给用户看的一句说明，来自选人策略
}

// AgentStartedPayload 是 agent.started 的载荷：某个回合开始。
type AgentStartedPayload struct {
	TurnID    uint64 `json:"turn_id"`
	TurnNo    int16  `json:"turn_no"`
	AgentID   uint64 `json:"agent_id"`
	AgentName string `json:"agent_name"`
}

// MessageDeltaPayload 是 message.delta 的载荷：正文增量。
//
// 当前模型层没有流式输出，所以一条消息只发一批（整段正文）；
// 接入流式之后会变成多批，前端按"往当前消息后面追加"处理即可，契约不变。
type MessageDeltaPayload struct {
	TurnID    uint64 `json:"turn_id"`
	MessageID uint64 `json:"message_id"`
	Delta     string `json:"delta"`
}

// MessageCompletedPayload 是 message.completed 的载荷：一条消息完成。
//
// 带完整正文：前端就算错过了 delta（断线、丢帧），也能拿这一份对齐。
type MessageCompletedPayload struct {
	TurnID     uint64 `json:"turn_id"`
	MessageID  uint64 `json:"message_id"`
	Content    string `json:"content"`
	TokenCount int32  `json:"token_count"`
}

// AgentCompletedPayload 是 agent.completed 的载荷：某个回合结束。
type AgentCompletedPayload struct {
	TurnID     uint64 `json:"turn_id"`
	TurnNo     int16  `json:"turn_no"`
	MessageID  uint64 `json:"message_id"`
	NextAction string `json:"next_action"` // 模型给出的下一步动作（已归一）
}

// RunWaitingUserPayload 是 run.waiting_user 的载荷：整趟讨论挂起等用户。
type RunWaitingUserPayload struct {
	Reason string `json:"reason"` // 停止原因，取值见 entity.RunStop*
}

// RunCompletedPayload 是 run.completed 的载荷：整趟讨论正常收尾。
type RunCompletedPayload struct {
	StopReason string `json:"stop_reason"` // completed / max_turns
	Turns      int    `json:"turns"`       // 实际说了几轮
}

// RunFailedPayload 是 run.failed 的载荷：整趟讨论失败。
type RunFailedPayload struct {
	Error string `json:"error"`
}

// participantPayloads 把圆桌成员翻成事件里的展示快照。
func participantPayloads(participants []Participant) []ParticipantPayload {
	out := make([]ParticipantPayload, len(participants))
	for index, participant := range participants {
		out[index] = ParticipantPayload{
			AgentID: participant.ClassroomAgentID,
			Name:    participant.Name,
			Role:    participant.Role,
		}
	}
	return out
}

// decisionReason 取选人策略给的理由；策略没给时用一句通用说明兜底，
// 保证 director.decision 事件里永远有一句能显示给用户的话。
func decisionReason(decision Decision) string {
	if decision.Reason != "" {
		return decision.Reason
	}
	return "按当前讨论状态选出的下一位发言人"
}
