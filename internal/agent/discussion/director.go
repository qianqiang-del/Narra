package discussion

import (
	"narra/internal/model/entity"
)

// Director 决定下一个该谁发言，以及这场讨论什么时候可以结束。
//
// 它被单独抽出来，是因为"选人"是整个讨论里最需要反复调整的一块：
// 从"每人轮流说一次"，到"按上一轮给出的动作走"，后面还要接"按问题内容挑最合适的角色"。
// 抽成接口之后，换策略只动这个文件，编排主循环一行都不用改。
type Director interface {
	// Decide 看当前局面，给出下一步：谁说话，或者就此停下。
	Decide(state DiscussionState) Decision
}

// DiscussionState 是决定"下一步怎么走"时能看到的全部信息。
//
// 只带决策需要的东西，不带模型、不带数据库句柄：选人策略应当是接近纯函数的判断，
// 这样才能单独测，也才能在后面接上下文预算、共享记忆时不被牵连。
type DiscussionState struct {
	Participants []Participant
	Spoken       []int  // 与 Participants 一一对应：各人已经发过几次言
	LastSpeaker  int    // 上一轮发言人的下标；-1 表示还没人说过
	LastAction   string // 上一轮给出的下一步动作；空串表示还没人说过、或模型没给
	TurnNo       int16  // 即将进行的是第几轮（从 1 开始）
}

// Decision 是"下一步怎么走"的结论。
type Decision struct {
	SpeakerIndex int    // 下一位发言人的下标；Stop 为 true 时无意义
	Stop         bool   // 是否就此停下
	StopReason   string // 停止原因，取值见 entity.RunStop*

	// Reason 是给用户看的"为什么这么走"，写进 director.decision 事件。
	// 选人策略自己填；留空时编排层用一句通用说明兜底。它只影响展示，不影响判断。
	Reason string
}

// RoundRobinDirector 让每个角色轮流发言，全员都说过话就结束。
//
// 这是最简策略，第 2 步的链路就是用它验证的；留着它的意义有两个：
// 作为"完全不看下一步动作"时的对照实现，以及给不需要复杂选人的场景直接用。
type RoundRobinDirector struct{}

// Decide 只看"谁还没说过话"来轮换，不理会上一轮给出的动作。
func (RoundRobinDirector) Decide(state DiscussionState) Decision {
	for index := range state.Participants {
		if index < len(state.Spoken) && state.Spoken[index] == 0 {
			return Decision{SpeakerIndex: index, Reason: "按顺序轮换到下一位"}
		}
	}
	return Decision{Stop: true, StopReason: entity.RunStopCompleted, Reason: "全员都已发过言"}
}

// TurnTakingDirector 按"上一轮给出的下一步动作"决定下一个谁说话。
//
// 规则一共四条，覆盖讨论里全部该有的走向：
//
//	ask_user   → 停下等用户（整趟活挂起，不是结束）
//	end        → 讨论自然结束
//	continue   → 上一个发言人接着补充
//	其余（含认不出、空值）→ 挑下一个还没说过话的人；全都说过就结束
//
// 最后一条同时承担"默认轮换"和"兜底"两件事。兜底选"换人"而不是"继续"，是因为
// 真模型未必按格式回话：一个莫名其妙的动作如果被当成"继续"，同一个人就会一直霸着话筒，
// 直到把回合数耗光 —— 那是最糟的失败方式。换成下一个人说话则总能往前推进。
type TurnTakingDirector struct{}

// Decide 按上述四条规则给出下一步。
func (TurnTakingDirector) Decide(state DiscussionState) Decision {
	switch state.LastAction {
	case entity.AgentTurnActionAskUser:
		return Decision{Stop: true, StopReason: entity.RunStopWaiting, Reason: "上一位要求先听用户的回答"}
	case entity.AgentTurnActionEnd:
		return Decision{Stop: true, StopReason: entity.RunStopCompleted, Reason: "上一位宣布讨论结束"}
	case entity.AgentTurnActionContinue:
		// "继续"是对同一个人的指令，所以只在确实有人说过话时才算数；
		// 第一轮就收到 continue 时退回默认轮换，否则会挑出一个不存在的发言人。
		if state.LastSpeaker >= 0 && state.LastSpeaker < len(state.Participants) {
			return Decision{SpeakerIndex: state.LastSpeaker, Reason: "上一位要求继续补充"}
		}
	}
	return nextUnspoken(state)
}

// nextUnspoken 挑第一个还没发过言的人；全都说过就宣布讨论结束。
func nextUnspoken(state DiscussionState) Decision {
	for index := range state.Participants {
		if index < len(state.Spoken) && state.Spoken[index] == 0 {
			return Decision{SpeakerIndex: index, Reason: "换下一位还没发过言的成员"}
		}
	}
	return Decision{Stop: true, StopReason: entity.RunStopCompleted, Reason: "全员都已发过言"}
}

// normalizeNextAction 把模型给出的动作收敛成"库里认识的值"。
//
// 库上有一条 CHECK（agent_turns_next_action_check），只允许这四个值或 NULL。
// 真模型不一定会乖乖按格式回话，所以**不能把它的原话直接写库**：那样会撞约束，
// 而这类"格式问题导致的写入失败"最难查 —— 现象是讨论中途崩掉，根因却是模型多打了一个字。
//
// 收敛规则与上面的判定规则**共用同一套默认**（认不出就当 switch_agent），
// 于是"库里记的动作"和"实际怎么走"永远是同一件事，不会出现记着 A、实际走 B 的分歧。
func normalizeNextAction(raw string) string {
	switch raw {
	case entity.AgentTurnActionContinue,
		entity.AgentTurnActionSwitchAgent,
		entity.AgentTurnActionAskUser,
		entity.AgentTurnActionEnd:
		return raw
	default:
		return entity.AgentTurnActionSwitchAgent
	}
}
