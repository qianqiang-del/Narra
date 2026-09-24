package discussion

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"narra/internal/model/entity"
	"narra/internal/repository"
)

// 第 6 步（C 的部分：在编排里发事件）的测试。
//
// 这一步要守三件事：
//
//	1. **发了什么、按什么顺序** —— 前端靠这个顺序画"谁正在说话"；
//	2. **载荷字段名与前端契约一致** —— 改一个名字前端就解析不出来；
//	3. **事件与它描述的记录同生共死** —— 消息没落库，就不该有"消息已完成"的事件。
//
// 载荷一律解到 map 而不是 events.go 里的结构体：解到自己的结构体会让"字段名写错"
// 两边一起错、用例反而看不出来；解到 map 才能验证真正发出去的键名。

// readEvents 按序号读回这个会话的全部事件。
func (f *fixture) readEvents(t *testing.T, ctx context.Context) []entity.ConversationEvent {
	t.Helper()

	events, err := f.events.ListAfter(ctx, f.conversation.ID, 0, 200)
	if err != nil {
		t.Fatalf("回查事件失败: %v", err)
	}
	return events
}

// eventTypes 取出"类型序列"，用来断言顺序。
func eventTypes(events []entity.ConversationEvent) []string {
	types := make([]string, 0, len(events))
	for _, event := range events {
		types = append(types, event.EventType)
	}
	return types
}

// onlyOf 挑出某一类事件。
func onlyOf(events []entity.ConversationEvent, eventType string) []entity.ConversationEvent {
	matched := make([]entity.ConversationEvent, 0, 1)
	for _, event := range events {
		if event.EventType == eventType {
			matched = append(matched, event)
		}
	}
	return matched
}

// payloadOf 解出一条事件的载荷。
func payloadOf(t *testing.T, event entity.ConversationEvent) map[string]any {
	t.Helper()

	var payload map[string]any
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("%s 的载荷不是合法 JSON: %v", event.EventType, err)
	}
	return payload
}

// assertPayloadKeys 断言载荷里**恰好**是这些字段：少一个前端取不到，多一个说明发错了。
func assertPayloadKeys(t *testing.T, eventType string, payload map[string]any, want ...string) {
	t.Helper()

	for _, key := range want {
		if _, ok := payload[key]; !ok {
			t.Errorf("%s 的载荷缺少字段 %q（前端按这个名字取）", eventType, key)
		}
	}
	if len(payload) != len(want) {
		t.Errorf("%s 的载荷有 %d 个字段，契约里是 %d 个；实际字段：%v",
			eventType, len(payload), len(want), sortedKeys(payload))
	}
}

// sortedKeys 把载荷的字段名排好序，报错时好读。
func sortedKeys(payload map[string]any) []string {
	keys := make([]string, 0, len(payload))
	for key := range payload {
		keys = append(keys, key)
	}
	for i := range keys {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}

// TestEventsFullRunEmitsExpectedSequence 验证一趟正常讨论发出来的事件：种类、条数、顺序、序号。
func TestEventsFullRunEmitsExpectedSequence(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	const onStage = 3
	orchestrator := f.newOrchestrator(t, FakeModel{})

	result, err := orchestrator.Run(ctx, Request{
		ConversationID:   f.conversation.ID,
		TriggerMessageID: f.trigger.ID,
		Participants:     f.participants[:onStage],
		MaxTurns:         6,
	})
	if err != nil {
		t.Fatalf("跑讨论失败: %v", err)
	}
	if len(result.Turns) != onStage {
		t.Fatalf("回合数 = %d，期望 %d", len(result.Turns), onStage)
	}

	events := f.readEvents(t, ctx)

	// ---- 顺序：开始 → （选人、开口、正文、说完、收尾）× N → 结束 ----
	want := []string{entity.ConversationEventRunStarted}
	for turn := 0; turn < onStage; turn++ {
		want = append(want,
			entity.ConversationEventDirectorDecision,
			entity.ConversationEventAgentStarted,
			entity.ConversationEventMessageDelta,
			entity.ConversationEventMessageCompleted,
			entity.ConversationEventAgentCompleted,
		)
	}
	want = append(want, entity.ConversationEventRunCompleted)

	got := eventTypes(events)
	if len(got) != len(want) {
		t.Fatalf("事件条数 = %d，期望 %d\n实际顺序：%v", len(got), len(want), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("第 %d 条事件是 %s，期望 %s\n实际顺序：%v", index+1, got[index], want[index], got)
		}
	}

	// ---- 序号：从 1 开始、连续、单调 ----
	for index, event := range events {
		if event.SequenceNo != int64(index+1) {
			t.Errorf("第 %d 条事件的序号 = %d，期望 %d", index+1, event.SequenceNo, index+1)
		}
	}

	// ---- 归属：会话必须是本场，运行必须是这一趟 ----
	for _, event := range events {
		if event.ConversationID != f.conversation.ID {
			t.Errorf("%s 挂在了别的会话上：%d", event.EventType, event.ConversationID)
		}
		if event.RunID == nil || *event.RunID != result.RunID {
			t.Errorf("%s 没有挂在本次运行 %d 上（实际 %v）", event.EventType, result.RunID, event.RunID)
		}
	}
	// 回合级的事件必须带上回合；整趟级的不该乱带。
	for _, event := range onlyOf(events, entity.ConversationEventAgentStarted) {
		if event.TurnID == nil {
			t.Errorf("agent.started 没带 turn_id，前端不知道是哪个回合开始了")
		}
	}
	if event := onlyOf(events, entity.ConversationEventRunStarted)[0]; event.TurnID != nil {
		t.Errorf("run.started 不该带 turn_id（那时还没有回合）")
	}
}

// TestEventsPayloadsMatchFrontendContract 逐个事件核对载荷字段与内容。
//
// 字段名以 frontend/src/api/conversation.ts 为准 —— 那是前后端的契约，
// 事件一旦写进库就按当时的样子重放，字段名改不得。
func TestEventsPayloadsMatchFrontendContract(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	const onStage = 2
	roundtable := f.participants[:onStage]
	orchestrator := f.newOrchestrator(t, FakeModel{})

	result, err := orchestrator.Run(ctx, Request{
		ConversationID:   f.conversation.ID,
		TriggerMessageID: f.trigger.ID,
		Participants:     roundtable,
		MaxTurns:         4,
	})
	if err != nil {
		t.Fatalf("跑讨论失败: %v", err)
	}
	events := f.readEvents(t, ctx)

	// ---- run.started：主题来源、上限、圆桌名单 ----
	started := payloadOf(t, onlyOf(events, entity.ConversationEventRunStarted)[0])
	assertPayloadKeys(t, entity.ConversationEventRunStarted, started,
		"trigger_message_id", "max_turns", "participants")
	if started["trigger_message_id"] != float64(f.trigger.ID) {
		t.Errorf("run.started 的 trigger_message_id = %v，期望 %d", started["trigger_message_id"], f.trigger.ID)
	}
	if started["max_turns"] != float64(4) {
		t.Errorf("run.started 的 max_turns = %v，期望 4", started["max_turns"])
	}
	participants, ok := started["participants"].([]any)
	if !ok || len(participants) != onStage {
		t.Fatalf("run.started 的 participants = %v，期望 %d 项", started["participants"], onStage)
	}
	first, ok := participants[0].(map[string]any)
	if !ok {
		t.Fatalf("run.started 的 participants[0] 不是对象：%v", participants[0])
	}
	assertPayloadKeys(t, "run.started.participants[0]", first, "agent_id", "name", "role")
	if first["agent_id"] != float64(roundtable[0].ClassroomAgentID) {
		t.Errorf("participants[0].agent_id = %v，期望 %d（应当是 classroom_agents.id，不是名次）",
			first["agent_id"], roundtable[0].ClassroomAgentID)
	}
	if first["name"] != roundtable[0].Name {
		t.Errorf("participants[0].name = %v，期望 %s", first["name"], roundtable[0].Name)
	}

	// ---- 每个回合：选人 → 开口 → 正文 → 说完 → 收尾 ----
	decisions := onlyOf(events, entity.ConversationEventDirectorDecision)
	starteds := onlyOf(events, entity.ConversationEventAgentStarted)
	deltas := onlyOf(events, entity.ConversationEventMessageDelta)
	completed := onlyOf(events, entity.ConversationEventMessageCompleted)
	agentCompleted := onlyOf(events, entity.ConversationEventAgentCompleted)
	for _, group := range [][]entity.ConversationEvent{decisions, starteds, deltas, completed, agentCompleted} {
		if len(group) != onStage {
			t.Fatalf("每类回合级事件都该有 %d 条，实际各有 %d/%d/%d/%d/%d 条",
				onStage, len(decisions), len(starteds), len(deltas), len(completed), len(agentCompleted))
		}
	}

	// 库里那一轮真正的发言，用来和事件里的正文比对。
	messages, err := f.messages.ListByConversation(ctx, f.conversation.ID, 0, 100)
	if err != nil {
		t.Fatalf("回查消息失败: %v", err)
	}
	agentMessages := messages[1:] // 第一条是用户消息

	for index := 0; index < onStage; index++ {
		speaker := roundtable[index]
		message := agentMessages[index]

		decision := payloadOf(t, decisions[index])
		assertPayloadKeys(t, entity.ConversationEventDirectorDecision, decision,
			"turn_no", "agent_id", "agent_name", "reason")
		if decision["turn_no"] != float64(index+1) {
			t.Errorf("第 %d 条 director.decision 的 turn_no = %v，期望 %d", index+1, decision["turn_no"], index+1)
		}
		if decision["agent_id"] != float64(speaker.ClassroomAgentID) {
			t.Errorf("第 %d 条 director.decision 的 agent_id = %v，期望 %d",
				index+1, decision["agent_id"], speaker.ClassroomAgentID)
		}
		if decision["agent_name"] != speaker.Name {
			t.Errorf("第 %d 条 director.decision 的 agent_name = %v，期望 %s",
				index+1, decision["agent_name"], speaker.Name)
		}
		if reason, _ := decision["reason"].(string); reason == "" {
			t.Errorf("第 %d 条 director.decision 没给理由 —— 前端要显示\"为什么轮到他\"", index+1)
		}

		agentStarted := payloadOf(t, starteds[index])
		assertPayloadKeys(t, entity.ConversationEventAgentStarted, agentStarted,
			"turn_id", "turn_no", "agent_id", "agent_name")
		if agentStarted["turn_no"] != float64(index+1) {
			t.Errorf("第 %d 条 agent.started 的 turn_no = %v，期望 %d", index+1, agentStarted["turn_no"], index+1)
		}
		if agentStarted["agent_name"] != speaker.Name {
			t.Errorf("第 %d 条 agent.started 的 agent_name = %v，期望 %s", index+1, agentStarted["agent_name"], speaker.Name)
		}

		// delta 与 completed 描述同一条消息，正文必须都对得上库里那条。
		delta := payloadOf(t, deltas[index])
		assertPayloadKeys(t, entity.ConversationEventMessageDelta, delta, "turn_id", "message_id", "delta")
		if delta["message_id"] != float64(message.ID) {
			t.Errorf("第 %d 条 message.delta 的 message_id = %v，期望 %d", index+1, delta["message_id"], message.ID)
		}
		if delta["delta"] != message.Content {
			t.Errorf("第 %d 条 message.delta 的正文与库里那条消息不一致", index+1)
		}

		messageCompleted := payloadOf(t, completed[index])
		assertPayloadKeys(t, entity.ConversationEventMessageCompleted, messageCompleted,
			"turn_id", "message_id", "content", "token_count")
		if messageCompleted["message_id"] != float64(message.ID) {
			t.Errorf("第 %d 条 message.completed 的 message_id = %v，期望 %d",
				index+1, messageCompleted["message_id"], message.ID)
		}
		if messageCompleted["content"] != message.Content {
			t.Errorf("第 %d 条 message.completed 的 content 与库里那条消息不一致", index+1)
		}
		if messageCompleted["token_count"] != float64(message.TokenCount) {
			t.Errorf("第 %d 条 message.completed 的 token_count = %v，期望 %d",
				index+1, messageCompleted["token_count"], message.TokenCount)
		}

		agentDone := payloadOf(t, agentCompleted[index])
		assertPayloadKeys(t, entity.ConversationEventAgentCompleted, agentDone,
			"turn_id", "turn_no", "message_id", "next_action")
		// 假模型不给下一步动作 → 编排按"换人"兜底（见 normalizeNextAction）。
		if agentDone["next_action"] != entity.AgentTurnActionSwitchAgent {
			t.Errorf("第 %d 条 agent.completed 的 next_action = %v，期望 %s",
				index+1, agentDone["next_action"], entity.AgentTurnActionSwitchAgent)
		}
	}

	// ---- run.completed：为什么停、一共几轮 ----
	completedRun := payloadOf(t, onlyOf(events, entity.ConversationEventRunCompleted)[0])
	assertPayloadKeys(t, entity.ConversationEventRunCompleted, completedRun, "stop_reason", "turns")
	if completedRun["stop_reason"] != entity.RunStopCompleted {
		t.Errorf("run.completed 的 stop_reason = %v，期望 %s", completedRun["stop_reason"], entity.RunStopCompleted)
	}
	if completedRun["turns"] != float64(len(result.Turns)) {
		t.Errorf("run.completed 的 turns = %v，期望 %d", completedRun["turns"], len(result.Turns))
	}
}

// TestEventsStopWaitingEmitsWaitingUser 验证"停下等用户"发的是挂起事件，不是结束事件。
func TestEventsStopWaitingEmitsWaitingUser(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	// 第一轮就说"该问用户了" → 整趟活挂起。
	model := &ScriptedModel{Replies: []ScriptedReply{
		{Content: "这个问题我需要先确认一下背景。", NextAction: entity.AgentTurnActionAskUser},
	}}
	orchestrator := f.newOrchestratorWith(t, model, TurnTakingDirector{})

	result, err := orchestrator.Run(ctx, Request{
		ConversationID:   f.conversation.ID,
		TriggerMessageID: f.trigger.ID,
		Participants:     f.participants[:2],
		MaxTurns:         4,
	})
	if err != nil {
		t.Fatalf("跑讨论失败: %v", err)
	}
	if result.Status != entity.RunStatusWaitingUser {
		t.Fatalf("运行状态 = %s，期望 %s", result.Status, entity.RunStatusWaitingUser)
	}

	events := f.readEvents(t, ctx)
	last := events[len(events)-1]
	if last.EventType != entity.ConversationEventRunWaitingUser {
		t.Fatalf("最后一条事件是 %s，期望 %s（挂起不能被写成结束）", last.EventType, entity.ConversationEventRunWaitingUser)
	}
	if waiting := onlyOf(events, entity.ConversationEventRunCompleted); len(waiting) != 0 {
		t.Errorf("挂起时不该发 run.completed，实际发了 %d 条", len(waiting))
	}

	payload := payloadOf(t, last)
	assertPayloadKeys(t, entity.ConversationEventRunWaitingUser, payload, "reason")
	if payload["reason"] != entity.RunStopWaiting {
		t.Errorf("run.waiting_user 的 reason = %v，期望 %s", payload["reason"], entity.RunStopWaiting)
	}
}

// TestEventsFailureEmitsRunFailed 验证失败收尾也留一条事件，且带上原因。
func TestEventsFailureEmitsRunFailed(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	orchestrator := f.newOrchestrator(t, failingModel{})

	result, err := orchestrator.Run(ctx, Request{
		ConversationID:   f.conversation.ID,
		TriggerMessageID: f.trigger.ID,
		Participants:     f.participants[:2],
		MaxTurns:         4,
	})
	if err == nil {
		t.Fatalf("模型失败时应当返回错误")
	}
	if result.RunID == 0 {
		t.Fatal("失败时也应返回 RunID")
	}

	events := f.readEvents(t, ctx)
	last := events[len(events)-1]
	if last.EventType != entity.ConversationEventRunFailed {
		t.Fatalf("最后一条事件是 %s，期望 %s", last.EventType, entity.ConversationEventRunFailed)
	}
	if completed := onlyOf(events, entity.ConversationEventMessageCompleted); len(completed) != 0 {
		t.Errorf("一轮都没说成，不该有 %d 条 message.completed", len(completed))
	}

	payload := payloadOf(t, last)
	assertPayloadKeys(t, entity.ConversationEventRunFailed, payload, "error")
	if reason, _ := payload["error"].(string); reason == "" {
		t.Errorf("run.failed 没带失败原因 —— 前端和运维都靠它")
	}
}

// TestEventsInTransactionRolledBackWithTurn 验证"回合没建成，它的开始事件也不该留下"。
//
// 用一个不存在的角色 ID：agent_turns.classroom_agent_id 有外键，写回合那笔事务必然失败。
// 这正是要验的场景 —— 事件与它所描述的记录必须同生共死。
func TestEventsInTransactionRolledBackWithTurn(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	ghost := Participant{
		ClassroomAgentID: 999999999,
		Name:             "幽灵角色",
		Role:             "测试",
		Persona:          "不存在于 classroom_agents 的角色",
	}
	orchestrator := f.newOrchestrator(t, FakeModel{})

	if _, err := orchestrator.Run(ctx, Request{
		ConversationID:   f.conversation.ID,
		TriggerMessageID: f.trigger.ID,
		Participants:     []Participant{ghost},
		MaxTurns:         1,
	}); err == nil {
		t.Fatalf("用不存在的角色应当失败")
	}

	events := f.readEvents(t, ctx)
	if started := onlyOf(events, entity.ConversationEventAgentStarted); len(started) != 0 {
		t.Errorf("回合没落库，却留下了 %d 条 agent.started（同事务没回滚）", len(started))
	}
	if deltas := onlyOf(events, entity.ConversationEventMessageDelta); len(deltas) != 0 {
		t.Errorf("消息没落库，却留下了 %d 条 message.delta", len(deltas))
	}
	// 事务外的那两条该留下：它们各自一笔事务，已经提交。
	if started := onlyOf(events, entity.ConversationEventRunStarted); len(started) != 1 {
		t.Errorf("run.started = %d 条，期望 1（它是独立一笔事务，不受回合失败影响）", len(started))
	}
	if failed := onlyOf(events, entity.ConversationEventRunFailed); len(failed) != 1 {
		t.Errorf("run.failed = %d 条，期望 1", len(failed))
	}
}

// flakyEventRepo 是"按事件类型定点报错"的事件仓储替身。
//
// 必须只让指定类型失败、其余照常落库：否则"事务外失败不影响讨论"和"事务内失败回滚"
// 这两条相反的规则会互相掩盖 —— 一个全都失败的替身证明不了其中任何一条。
type flakyEventRepo struct {
	inner  repository.ConversationEventRepository
	failOn map[string]bool
}

func (r flakyEventRepo) AppendNext(ctx context.Context, event *entity.ConversationEvent) error {
	if r.failOn[event.EventType] {
		return fmt.Errorf("模拟事件写入失败：%s", event.EventType)
	}
	return r.inner.AppendNext(ctx, event)
}

func (r flakyEventRepo) ListAfter(ctx context.Context, conversationID uint64, after int64, limit int) ([]entity.ConversationEvent, error) {
	return r.inner.ListAfter(ctx, conversationID, after, limit)
}

func (r flakyEventRepo) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	return r.inner.DeleteExpired(ctx, before)
}

// TestEventsStandaloneFailureDoesNotBreakRun 验证"过程记录"写不进去不该拖垮讨论。
func TestEventsStandaloneFailureDoesNotBreakRun(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	orchestrator := f.newOrchestrator(t, FakeModel{})
	// 只让首尾两条（事务外发的）失败。
	orchestrator.deps.Events = flakyEventRepo{
		inner: f.events,
		failOn: map[string]bool{
			entity.ConversationEventRunStarted:   true,
			entity.ConversationEventRunCompleted: true,
		},
	}

	result, err := orchestrator.Run(ctx, Request{
		ConversationID:   f.conversation.ID,
		TriggerMessageID: f.trigger.ID,
		Participants:     f.participants[:2],
		MaxTurns:         4,
	})
	if err != nil {
		t.Fatalf("首尾事件写失败不该让讨论失败，实际出错: %v", err)
	}
	if result.Status != entity.RunStatusCompleted {
		t.Errorf("运行状态 = %s，期望 %s", result.Status, entity.RunStatusCompleted)
	}

	events := f.readEvents(t, ctx)
	if started := onlyOf(events, entity.ConversationEventRunStarted); len(started) != 0 {
		t.Errorf("run.started 本该写失败，却留下了 %d 条", len(started))
	}
	if completed := onlyOf(events, entity.ConversationEventMessageCompleted); len(completed) != 2 {
		t.Errorf("message.completed = %d 条，期望 2（事务内的不受影响）", len(completed))
	}
}

// TestEventsInTransactionFailureFailsTheRun 验证反过来的一面：
// 事务内的事件写失败 → 那笔事务整体回滚（消息也不落库），讨论算失败。
func TestEventsInTransactionFailureFailsTheRun(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	orchestrator := f.newOrchestrator(t, FakeModel{})
	// 只让"回合开始"这条（事务内发的）失败。
	orchestrator.deps.Events = flakyEventRepo{
		inner:  f.events,
		failOn: map[string]bool{entity.ConversationEventAgentStarted: true},
	}

	if _, err := orchestrator.Run(ctx, Request{
		ConversationID:   f.conversation.ID,
		TriggerMessageID: f.trigger.ID,
		Participants:     f.participants[:2],
		MaxTurns:         4,
	}); err == nil {
		t.Fatalf("事务内的事件写失败应当让讨论失败")
	}

	// 消息也不该落库 —— 这才是"同生共死"的意思。
	messages, err := f.messages.ListByConversation(ctx, f.conversation.ID, 0, 100)
	if err != nil {
		t.Fatalf("回查消息失败: %v", err)
	}
	if len(messages) != 1 {
		t.Errorf("消息数 = %d，期望 1（只有用户那条；事件写失败时那笔事务该整体回滚）", len(messages))
	}

	events := f.readEvents(t, ctx)
	if deltas := onlyOf(events, entity.ConversationEventMessageDelta); len(deltas) != 0 {
		t.Errorf("消息没落库，却留下了 %d 条 message.delta", len(deltas))
	}
}
