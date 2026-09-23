package discussion

import (
	"context"
	"encoding/json"
	"testing"

	"narra/internal/model/entity"
)

// 事件流的源头测试：一场讨论跑完，conversation_events 里是不是按
// 开始 → 选人 → 开讲 → 正文 → 收尾 → 结束 的顺序落了一整套。
//
// 与编排的其余用例同一条约定：真库、默认跳过（NARRA_INTEGRATION_TEST=1 打开）。
// SSE 端点只负责把这里写下的东西推出去，所以"事件写得对不对"必须在这一层验。

func TestOrchestratorWritesEventStream(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	const onStage = 2
	roundtable := f.participants[:onStage]
	orchestrator := f.newOrchestrator(t, FakeModel{})

	if _, err := orchestrator.Run(ctx, Request{
		ConversationID:   f.conversation.ID,
		TriggerMessageID: f.trigger.ID,
		Participants:     roundtable,
		MaxTurns:         6,
	}); err != nil {
		t.Fatalf("跑讨论失败: %v", err)
	}

	events, err := f.events.ListAfter(ctx, f.conversation.ID, 0, 100)
	if err != nil {
		t.Fatalf("读事件失败: %v", err)
	}

	want := []string{
		entity.ConversationEventRunStarted,
		entity.ConversationEventDirectorDecision,
		entity.ConversationEventAgentStarted,
		entity.ConversationEventMessageDelta,
		entity.ConversationEventMessageCompleted,
		entity.ConversationEventAgentCompleted,
		entity.ConversationEventDirectorDecision,
		entity.ConversationEventAgentStarted,
		entity.ConversationEventMessageDelta,
		entity.ConversationEventMessageCompleted,
		entity.ConversationEventAgentCompleted,
		entity.ConversationEventRunCompleted,
	}
	if len(events) != len(want) {
		t.Fatalf("事件数 = %d，期望 %d：%v", len(events), len(want), eventTypes(events))
	}

	turnLevel := map[string]bool{
		entity.ConversationEventAgentStarted:     true,
		entity.ConversationEventMessageDelta:     true,
		entity.ConversationEventMessageCompleted: true,
		entity.ConversationEventAgentCompleted:   true,
	}
	for index, event := range events {
		if event.SequenceNo != int64(index+1) {
			t.Errorf("第 %d 条事件序号 = %d，期望 %d（对话内从 1 连续递增）", index, event.SequenceNo, index+1)
		}
		if event.EventType != want[index] {
			t.Errorf("第 %d 条事件 = %s，期望 %s", index, event.EventType, want[index])
		}
		if event.RunID == nil {
			t.Errorf("第 %d 条事件没挂运行 ID", index)
		}
		// 回合级事件必须挂上回合，运行级事件不该挂：前端靠它把事件归到某一轮。
		if turnLevel[event.EventType] != (event.TurnID != nil) {
			t.Errorf("第 %d 条事件（%s）的 turn_id = %v，与事件级别不符",
				index, event.EventType, event.TurnID)
		}
	}

	// run.started 要带上圆桌成员快照，前端才能先画出"有谁"。
	var started RunStartedPayload
	if err := json.Unmarshal(events[0].Payload, &started); err != nil {
		t.Fatalf("run.started 载荷不是合法 JSON: %v", err)
	}
	if started.TriggerMessageID != f.trigger.ID || started.MaxTurns != 6 {
		t.Errorf("run.started = %+v，触发消息/回合上限与请求不符", started)
	}
	if len(started.Participants) != onStage {
		t.Fatalf("run.started 带上了 %d 位成员，期望 %d", len(started.Participants), onStage)
	}
	for index, member := range started.Participants {
		if member.AgentID != roundtable[index].ClassroomAgentID || member.Name != roundtable[index].Name {
			t.Errorf("第 %d 位成员快照 = %+v，期望 %s", index, member, roundtable[index].Name)
		}
	}

	// 正文增量与完成帧必须是同一段话：前端会先按 delta 渲染，再拿 completed 对齐。
	var delta MessageDeltaPayload
	var completed MessageCompletedPayload
	if err := json.Unmarshal(events[3].Payload, &delta); err != nil {
		t.Fatalf("message.delta 载荷不是合法 JSON: %v", err)
	}
	if err := json.Unmarshal(events[4].Payload, &completed); err != nil {
		t.Fatalf("message.completed 载荷不是合法 JSON: %v", err)
	}
	if delta.Delta == "" || delta.Delta != completed.Content {
		t.Errorf("delta = %q，completed.content = %q，两者应一致且非空", delta.Delta, completed.Content)
	}
	if delta.MessageID != completed.MessageID || delta.TurnID != completed.TurnID {
		t.Errorf("delta 与 completed 指向的消息/回合不一致：%d/%d vs %d/%d",
			delta.MessageID, delta.TurnID, completed.MessageID, completed.TurnID)
	}

	var finished RunCompletedPayload
	if err := json.Unmarshal(events[len(events)-1].Payload, &finished); err != nil {
		t.Fatalf("run.completed 载荷不是合法 JSON: %v", err)
	}
	if finished.StopReason != entity.RunStopCompleted || finished.Turns != onStage {
		t.Errorf("run.completed = %+v，期望 stop_reason=completed、turns=%d", finished, onStage)
	}
}

// 失败路径也要在流上留下交代：没有 run.failed，前端会一直转圈等结果。
func TestOrchestratorWritesRunFailedEvent(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	orchestrator := f.newOrchestrator(t, failingModel{})

	if _, err := orchestrator.Run(ctx, Request{
		ConversationID:   f.conversation.ID,
		TriggerMessageID: f.trigger.ID,
		Participants:     f.participants[:1],
		MaxTurns:         6,
	}); err == nil {
		t.Fatal("模型必然失败，讨论不该返回 nil 错误")
	}

	events, err := f.events.ListAfter(ctx, f.conversation.ID, 0, 100)
	if err != nil {
		t.Fatalf("读事件失败: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("事件数 = %d，期望 4（开始、选人、开讲、失败）：%v", len(events), eventTypes(events))
	}
	last := events[len(events)-1]
	if last.EventType != entity.ConversationEventRunFailed {
		t.Fatalf("最后一条事件 = %s，期望 %s", last.EventType, entity.ConversationEventRunFailed)
	}
	var failed RunFailedPayload
	if err := json.Unmarshal(last.Payload, &failed); err != nil {
		t.Fatalf("run.failed 载荷不是合法 JSON: %v", err)
	}
	if failed.Error == "" {
		t.Error("run.failed 没带失败原因")
	}
}

// eventTypes 把事件类型摘出来，失败信息里好读。
func eventTypes(events []entity.ConversationEvent) []string {
	out := make([]string, len(events))
	for index, event := range events {
		out[index] = event.EventType
	}
	return out
}
