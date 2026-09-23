package discussion

import (
	"context"
	"errors"
	"strings"
	"testing"

	"narra/internal/model/entity"
)

// 第 3 步（让讨论会看情况）的测试。
//
// 与第 2 步的测试分文件，是为了让这一步新加的能力有独立用例集：
// 老的那 6 个继续守着"轮流策略 + 基本链路能不能跑通"，
// 这里守三件新事 —— 按动作选人、能停下等用户、出错自己重试。

// ScriptedReply 是剧本里的一条：说这句话、给出下一步动作，或者干脆"这次调用失败"。
type ScriptedReply struct {
	Content    string
	NextAction string
	Err        error
}

// ScriptedModel 按事先排好的剧本逐条回话。
//
// 它放在测试文件里而不是 model.go：这是**测试替身**，没有理由跟着二进制一起发出去。
// 相比之下 FakeModel 留在 model.go，是因为本地联调（不接真模型跑通链路）也需要它。
//
// 为什么需要"按剧本"而不是"永远回同一句"：第 3 步要验证的是"编排有没有按模型给的
// 下一步动作去走"，这就必须能精确构造出"第几轮说了什么、给了什么动作"。
type ScriptedModel struct {
	Replies []ScriptedReply
	calls   int
}

// Generate 返回剧本里的下一条。
func (m *ScriptedModel) Generate(ctx context.Context, request GenerationRequest) (GenerationResponse, error) {
	if err := ctx.Err(); err != nil {
		return GenerationResponse{}, err
	}

	index := m.calls
	m.calls++

	if index >= len(m.Replies) {
		// 剧本用完了还在被调用，说明停止判断有问题（本该更早就停）。
		// 这里不 panic —— panic 会把真正的线索埋进堆栈里；改成回一条"宣布结束"，
		// 让讨论自然收尾，用例随后会因为"回合数不符合预期"而失败，指向就清楚了。
		return GenerationResponse{
			Content:    "（剧本已用完）",
			NextAction: entity.AgentTurnActionEnd,
		}, nil
	}

	reply := m.Replies[index]
	if reply.Err != nil {
		return GenerationResponse{}, reply.Err
	}
	return GenerationResponse{
		Content:      reply.Content,
		InputTokens:  estimateTokens(topicAndHistory(request)),
		OutputTokens: estimateTokens(reply.Content),
		NextAction:   reply.NextAction,
	}, nil
}

// newOrchestratorWith 用指定的模型和选人策略装配编排器。
//
// 与 fixture.newOrchestrator 的区别是多传一个 Director：
// 第 3 步的重点就是换选人策略，用例必须能自己指定它。
func (f *fixture) newOrchestratorWith(t *testing.T, model Model, director Director) *Orchestrator {
	t.Helper()

	orchestrator, err := New(Deps{
		Tx:            f.tx,
		Conversations: f.conversations,
		Messages:      f.messages,
		Runs:          f.runs,
		Turns:         f.turns,
		Events:        f.events,
		Compactions:   f.compactions,
		Model:         model,
		// 摘要器沿用假模型，理由同 orchestrator_test.go 的 newOrchestrator：
		// 这些用例不触发压缩，只是装配校验要求非空。
		Summarizer: FakeModel{},
		Director:   director,
	})
	if err != nil {
		t.Fatalf("装配编排器失败: %v", err)
	}
	return orchestrator
}

// TestTurnTakingStopsWhenAskingUser 是最重要的一条：
// 角色说"该问用户了"，整趟活要停下并标成"等用户"，而不是继续硬聊。
func TestTurnTakingStopsWhenAskingUser(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	model := &ScriptedModel{Replies: []ScriptedReply{
		{Content: "我先讲讲瑞利散射。不过要接着往下讲，得先知道你们学过光的波动没有。",
			NextAction: entity.AgentTurnActionAskUser},
	}}
	orchestrator := f.newOrchestratorWith(t, model, TurnTakingDirector{})

	result, err := orchestrator.Run(ctx, Request{
		ConversationID:   f.conversation.ID,
		TriggerMessageID: f.trigger.ID,
		Participants:     f.participants[:3],
		MaxTurns:         6,
	})
	if err != nil {
		t.Fatalf("跑讨论失败: %v", err)
	}

	if result.Status != entity.RunStatusWaitingUser {
		t.Errorf("返回值状态 = %s，期望 %s（停下来等用户，不是完成）", result.Status, entity.RunStatusWaitingUser)
	}
	if result.StopReason != entity.RunStopWaiting {
		t.Errorf("返回值停止原因 = %s，期望 %s", result.StopReason, entity.RunStopWaiting)
	}
	if len(result.Turns) != 1 {
		t.Errorf("回合数 = %d，期望 1（第一轮就提出要问用户）", len(result.Turns))
	}

	run, err := f.runs.FindByID(ctx, result.RunID)
	if err != nil {
		t.Fatalf("回查运行失败: %v", err)
	}
	if run.Status != entity.RunStatusWaitingUser {
		t.Errorf("库里运行状态 = %s，期望 %s", run.Status, entity.RunStatusWaitingUser)
	}
	if run.StopReason == nil || *run.StopReason != entity.RunStopWaiting {
		t.Errorf("库里 stop_reason = %v，期望 %s", run.StopReason, entity.RunStopWaiting)
	}
	if run.FinishedAt == nil {
		t.Errorf("等用户的运行也要有结束时间，否则会一直显示成'进行中'")
	}

	// 已经说出来的那句话必须照常落库 —— 用户回来时要看到"系统问了什么"。
	messages, err := f.messages.ListByConversation(ctx, f.conversation.ID, 0, 100)
	if err != nil {
		t.Fatalf("回查消息失败: %v", err)
	}
	if len(messages) != 2 {
		t.Errorf("消息数 = %d，期望 2（1 条用户 + 1 条 Agent）", len(messages))
	}
}

// TestTurnTakingStopsWhenEnding 验证角色宣布"结束"时正常收尾。
func TestTurnTakingStopsWhenEnding(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	model := &ScriptedModel{Replies: []ScriptedReply{
		{Content: "这个问题到这里就清楚了，不用再讨论。", NextAction: entity.AgentTurnActionEnd},
	}}
	orchestrator := f.newOrchestratorWith(t, model, TurnTakingDirector{})

	result, err := orchestrator.Run(ctx, Request{
		ConversationID:   f.conversation.ID,
		TriggerMessageID: f.trigger.ID,
		Participants:     f.participants[:3],
		MaxTurns:         6,
	})
	if err != nil {
		t.Fatalf("跑讨论失败: %v", err)
	}

	if result.Status != entity.RunStatusCompleted {
		t.Errorf("状态 = %s，期望 %s", result.Status, entity.RunStatusCompleted)
	}
	if result.StopReason != entity.RunStopCompleted {
		t.Errorf("停止原因 = %s，期望 %s（是'说完了'而不是'被上限截断'）",
			result.StopReason, entity.RunStopCompleted)
	}
	if len(result.Turns) != 1 {
		t.Errorf("回合数 = %d，期望 1", len(result.Turns))
	}

	run, err := f.runs.FindByID(ctx, result.RunID)
	if err != nil {
		t.Fatalf("回查运行失败: %v", err)
	}
	if run.StopReason == nil || *run.StopReason != entity.RunStopCompleted {
		t.Errorf("库里 stop_reason = %v，期望 %s", run.StopReason, entity.RunStopCompleted)
	}
}

// TestTurnTakingContinueKeepsSameSpeaker 验证"继续"这个动作真的能让同一个人再说一轮。
//
// 这是最能证明"选人真的按动作走"的一条：轮流策略永远不可能让同一个人连说两次。
func TestTurnTakingContinueKeepsSameSpeaker(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	roundtable := f.participants[:3]
	model := &ScriptedModel{Replies: []ScriptedReply{
		{Content: "我先把结论说了。", NextAction: entity.AgentTurnActionContinue},
		{Content: "补充一句，边界情况是这样。", NextAction: entity.AgentTurnActionSwitchAgent},
		{Content: "那我想问一下……", NextAction: entity.AgentTurnActionEnd},
	}}
	orchestrator := f.newOrchestratorWith(t, model, TurnTakingDirector{})

	result, err := orchestrator.Run(ctx, Request{
		ConversationID:   f.conversation.ID,
		TriggerMessageID: f.trigger.ID,
		Participants:     roundtable,
		MaxTurns:         6,
	})
	if err != nil {
		t.Fatalf("跑讨论失败: %v", err)
	}
	if len(result.Turns) != 3 {
		t.Fatalf("回合数 = %d，期望 3", len(result.Turns))
	}

	if result.Turns[0].AgentName != roundtable[0].Name || result.Turns[1].AgentName != roundtable[0].Name {
		t.Errorf("前两轮 = %s、%s，期望都是 %s（continue 应该是同一个人接着说）",
			result.Turns[0].AgentName, result.Turns[1].AgentName, roundtable[0].Name)
	}
	if result.Turns[2].AgentName != roundtable[1].Name {
		t.Errorf("第三轮 = %s，期望 %s（switch_agent 之后应该换人）",
			result.Turns[2].AgentName, roundtable[1].Name)
	}
}

// TestTurnTakingFallsBackOnUnknownAction 验证认不出的动作不会把讨论搞崩。
//
// 真实模型不一定乖乖按格式回话，所以这条路一定会被走到：
// 兜底必须"安全地继续"（换人），而不是卡死或让同一人霸着话筒。
func TestTurnTakingFallsBackOnUnknownAction(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	roundtable := f.participants[:2]
	model := &ScriptedModel{Replies: []ScriptedReply{
		{Content: "我说一句。", NextAction: "模型瞎给的动作"},
		{Content: "我也说一句。", NextAction: ""},
	}}
	orchestrator := f.newOrchestratorWith(t, model, TurnTakingDirector{})

	result, err := orchestrator.Run(ctx, Request{
		ConversationID:   f.conversation.ID,
		TriggerMessageID: f.trigger.ID,
		Participants:     roundtable,
		MaxTurns:         6,
	})
	if err != nil {
		t.Fatalf("认不出的动作不该让讨论失败: %v", err)
	}
	if len(result.Turns) != 2 {
		t.Fatalf("回合数 = %d，期望 2", len(result.Turns))
	}
	if result.Turns[0].AgentName != roundtable[0].Name || result.Turns[1].AgentName != roundtable[1].Name {
		t.Errorf("前两轮 = %s、%s，期望按默认轮换是 %s、%s",
			result.Turns[0].AgentName, result.Turns[1].AgentName, roundtable[0].Name, roundtable[1].Name)
	}
}

// TestTurnTakingRecordsNextAction 验证"下一步动作"终于写进了那个一直空着的字段。
func TestTurnTakingRecordsNextAction(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	model := &ScriptedModel{Replies: []ScriptedReply{
		{Content: "第一句。", NextAction: entity.AgentTurnActionContinue},
		{Content: "第二句。", NextAction: entity.AgentTurnActionSwitchAgent},
		{Content: "第三句。", NextAction: entity.AgentTurnActionEnd},
	}}
	orchestrator := f.newOrchestratorWith(t, model, TurnTakingDirector{})

	result, err := orchestrator.Run(ctx, Request{
		ConversationID:   f.conversation.ID,
		TriggerMessageID: f.trigger.ID,
		Participants:     f.participants[:3],
		MaxTurns:         6,
	})
	if err != nil {
		t.Fatalf("跑讨论失败: %v", err)
	}

	turns, err := f.turns.ListByRun(ctx, result.RunID)
	if err != nil {
		t.Fatalf("回查回合失败: %v", err)
	}
	want := []string{
		entity.AgentTurnActionContinue,
		entity.AgentTurnActionSwitchAgent,
		entity.AgentTurnActionEnd,
	}
	if len(turns) != len(want) {
		t.Fatalf("回合数 = %d，期望 %d", len(turns), len(want))
	}
	for i, turn := range turns {
		if turn.NextAction == nil {
			t.Errorf("第 %d 个回合的 next_action 是空的（这个字段不该再空着）", i+1)
			continue
		}
		if *turn.NextAction != want[i] {
			t.Errorf("第 %d 个回合 next_action = %s，期望 %s", i+1, *turn.NextAction, want[i])
		}
	}
}

// TestTurnTakingRetriesModelOnce 验证模型偶发失败会自己重试，而不是整趟完蛋。
func TestTurnTakingRetriesModelOnce(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	model := &ScriptedModel{Replies: []ScriptedReply{
		{Err: errors.New("模拟第一次调用超时")},
		{Content: "重试之后答上来了。", NextAction: entity.AgentTurnActionEnd},
	}}
	orchestrator := f.newOrchestratorWith(t, model, TurnTakingDirector{})

	result, err := orchestrator.Run(ctx, Request{
		ConversationID:   f.conversation.ID,
		TriggerMessageID: f.trigger.ID,
		Participants:     f.participants[:2],
		MaxTurns:         4,
	})
	if err != nil {
		t.Fatalf("第一次失败、第二次成功时整趟活应当成功，实际报错: %v", err)
	}
	if result.Status != entity.RunStatusCompleted {
		t.Errorf("状态 = %s，期望 %s", result.Status, entity.RunStatusCompleted)
	}

	run, err := f.runs.FindByID(ctx, result.RunID)
	if err != nil {
		t.Fatalf("回查运行失败: %v", err)
	}
	if run.Status != entity.RunStatusCompleted {
		t.Errorf("库里运行状态 = %s，期望 %s", run.Status, entity.RunStatusCompleted)
	}

	// 重试的是"这一次模型调用"，不是"这一轮"，所以消息只会有一条。
	messages, err := f.messages.ListByConversation(ctx, f.conversation.ID, 0, 100)
	if err != nil {
		t.Fatalf("回查消息失败: %v", err)
	}
	if len(messages) != 2 {
		t.Errorf("消息数 = %d，期望 2（1 条用户 + 1 条 Agent，重试不该写重）", len(messages))
	}
}

// TestTurnTakingGivesUpAfterRetry 验证重试也没救时，整趟活如实标成失败。
func TestTurnTakingGivesUpAfterRetry(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	model := &ScriptedModel{Replies: []ScriptedReply{
		{Err: errors.New("第一次就挂了")},
		{Err: errors.New("重试还是挂")},
	}}
	orchestrator := f.newOrchestratorWith(t, model, TurnTakingDirector{})

	result, err := orchestrator.Run(ctx, Request{
		ConversationID:   f.conversation.ID,
		TriggerMessageID: f.trigger.ID,
		Participants:     f.participants[:2],
		MaxTurns:         4,
	})
	if err == nil {
		t.Fatalf("重试后仍失败时应当返回错误")
	}
	// 失败时 Run 仍会返回带 RunID 的结果（见 Run 的契约说明）。这里显式断言一句：
	// 既守住这个契约，也说明下面用 result.RunID 不是"err 之后碰了零值"。
	if result.RunID == 0 {
		t.Fatal("失败时也应返回 RunID，否则排查时找不到是哪一趟活失败了")
	}

	run, loadErr := f.runs.FindByID(ctx, result.RunID)
	if loadErr != nil {
		t.Fatalf("回查运行失败: %v", loadErr)
	}
	if run.Status != entity.RunStatusFailed {
		t.Errorf("库里运行状态 = %s，期望 %s", run.Status, entity.RunStatusFailed)
	}
	if run.StopReason == nil || *run.StopReason != entity.RunStopError {
		t.Errorf("库里 stop_reason = %v，期望 %s", run.StopReason, entity.RunStopError)
	}
	if run.ErrorMessage == nil || *run.ErrorMessage == "" {
		t.Errorf("库里没有记下失败原因")
	}
	// 错误信息里应能看出是重试后仍然失败，否则线上分不清是首调还是重试挂了。
	if run.ErrorMessage != nil && !strings.Contains(*run.ErrorMessage, "重试") {
		t.Errorf("失败信息里看不出'重试过'：%q", *run.ErrorMessage)
	}
}
