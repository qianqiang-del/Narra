package discussion

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	"narra/internal/model/entity"
)

// 讨论过程中的事件（第 6 步）。
//
// 这里定义两件事：**发出去的每条通知长什么样**，以及**怎么把它写进库**。
// 本文件不碰推送 —— 事件先落库，SSE 那一层再从库里按序号读出去发给前端。
//
// 载荷的字段名**必须**与前端 frontend/src/api/conversation.ts 里的同名接口逐字一致：
// 改一个字段前端就解析不出来；而且事件一旦写进库，就按当时的样子重放，老事件不跟着变形。
//
// 事件类型一律用 entity 里的常量，不要在代码里手打字符串 —— 打错了不会编译报错，
// 只会让前端收到一个它不认识的名字。

// participantBrief 是圆桌成员在事件里的展示快照（发言当时的样子）。
type participantBrief struct {
	AgentID uint64 `json:"agent_id"`
	Name    string `json:"name"`
	Role    string `json:"role"`
}

// runStartedPayload：这一趟讨论开始了。
type runStartedPayload struct {
	TriggerMessageID uint64             `json:"trigger_message_id"`
	MaxTurns         int16              `json:"max_turns"`
	Participants     []participantBrief `json:"participants"`
}

// directorDecisionPayload：下一个该谁、为什么。
type directorDecisionPayload struct {
	TurnNo    int16  `json:"turn_no"`
	AgentID   uint64 `json:"agent_id"`
	AgentName string `json:"agent_name"`
	Reason    string `json:"reason"`
}

// agentStartedPayload：某个回合开始。
type agentStartedPayload struct {
	TurnID    uint64 `json:"turn_id"`
	TurnNo    int16  `json:"turn_no"`
	AgentID   uint64 `json:"agent_id"`
	AgentName string `json:"agent_name"`
}

// messageDeltaPayload：正文增量。
//
// 当前模型不是流式的（一次返回整段），所以一条消息就是一批 delta；
// 等第 8 步接上流式模型，同一个字段会分多次发，**契约不变**。
type messageDeltaPayload struct {
	TurnID    uint64 `json:"turn_id"`
	MessageID uint64 `json:"message_id"`
	Delta     string `json:"delta"`
}

// messageCompletedPayload：一条消息完成，带完整正文供对齐。
type messageCompletedPayload struct {
	TurnID     uint64 `json:"turn_id"`
	MessageID  uint64 `json:"message_id"`
	Content    string `json:"content"`
	TokenCount int32  `json:"token_count"`
}

// agentCompletedPayload：某个回合结束。
type agentCompletedPayload struct {
	TurnID     uint64 `json:"turn_id"`
	TurnNo     int16  `json:"turn_no"`
	MessageID  uint64 `json:"message_id"`
	NextAction string `json:"next_action"`
}

// runWaitingUserPayload：整趟讨论挂起等用户。
type runWaitingUserPayload struct {
	Reason string `json:"reason"`
}

// runCompletedPayload：整趟讨论正常收尾。
type runCompletedPayload struct {
	StopReason string `json:"stop_reason"`
	Turns      int    `json:"turns"`
}

// runFailedPayload：整趟讨论失败。
type runFailedPayload struct {
	Error string `json:"error"`
}

// participantBriefs 把圆桌上的角色转成事件载荷里的形状。
//
// AgentID 取的是 classroom_agents.id（不是圆桌上的名次）—— 前端拿它去对上头像和人设。
func participantBriefs(participants []Participant) []participantBrief {
	briefs := make([]participantBrief, 0, len(participants))
	for _, participant := range participants {
		briefs = append(briefs, participantBrief{
			AgentID: participant.ClassroomAgentID,
			Name:    participant.Name,
			Role:    participant.Role,
		})
	}
	return briefs
}

// appendEvent 在**调用方的事务内**追加一条事件。
//
// 必须放在已经开着的事务里调用：事件仓储要靠对话行锁给事件发序号，
// 脱离事务调用的话那把锁随语句结束就释放了，序号不再安全。
//
// 写失败要把错误抛出去、让那笔事务跟着回滚 —— 事件与它描述的记录必须同生共死，
// 否则前端会先收到"消息已完成"、却查不到那条消息。
func (o *Orchestrator) appendEvent(
	ctx context.Context,
	conversationID uint64,
	runID *uint64,
	turnID *uint64,
	eventType string,
	payload any,
) error {
	event, err := newEvent(conversationID, runID, turnID, eventType, payload)
	if err != nil {
		return err
	}
	return o.deps.Events.AppendNext(ctx, event)
}

// emitEvent 自己起一笔事务发一条事件；**失败只记日志，不返回错误**。
//
// 用于"过程记录"性质的事件（讨论开始、整趟终态）：它们是给前端画进度用的，
// 写不进去最多是这一次的流少一段，不该让已经跑完（或已经失败）的讨论再变个结果。
// 与记忆提炼同一个口径：收尾类动作失败就吞掉，只留日志。
func (o *Orchestrator) emitEvent(
	ctx context.Context,
	conversationID uint64,
	runID *uint64,
	turnID *uint64,
	eventType string,
	payload any,
) {
	if err := o.deps.Tx.Run(ctx, func(ctx context.Context) error {
		return o.appendEvent(ctx, conversationID, runID, turnID, eventType, payload)
	}); err != nil {
		o.deps.Logger.Error("写事件失败（不影响讨论结果）",
			zap.String("event_type", eventType),
			zap.Error(err),
		)
	}
}

// newEvent 组装一条待写入的事件。
//
// 只填"属于谁"和"发生了什么"，另外三列一律留空、交给数据库和仓储：
// sequence_no 由仓储在事务内发号，created_at 有默认值，expires_at 由数据库算成 7 天后。
// 尤其 expires_at **不能**在这里填零值 —— 那会被当成"很久以前"，事件一出生就算过期，
// 清理任务立刻把它删掉，而正在连着的前端看不出任何异常。
func newEvent(
	conversationID uint64,
	runID *uint64,
	turnID *uint64,
	eventType string,
	payload any,
) (*entity.ConversationEvent, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化 %s 的载荷失败: %w", eventType, err)
	}
	return &entity.ConversationEvent{
		ConversationID: conversationID,
		RunID:          runID,
		TurnID:         turnID,
		EventType:      eventType,
		Payload:        encoded,
	}, nil
}
