package discussion

import (
	"context"
	"errors"
	"testing"
	"time"

	"narra/internal/model/entity"
)

type failingSpanRepository struct{}

func (failingSpanRepository) Create(context.Context, *entity.AgentTraceSpan) error {
	return errors.New("追踪表不可写")
}

func (failingSpanRepository) ListByRun(context.Context, uint64) ([]entity.AgentTraceSpan, error) {
	return nil, errors.New("追踪表不可读")
}

func (failingSpanRepository) DeleteExpired(context.Context, time.Time) (int64, error) {
	return 0, errors.New("追踪表不可删")
}

func TestDiscussionTraceSpansFormRunTurnModelTree(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()

	model := &ScriptedModel{Replies: []ScriptedReply{
		{Content: "第一位回答。", NextAction: entity.AgentTurnActionSwitchAgent},
		{Content: "第二位回答。", NextAction: entity.AgentTurnActionEnd},
	}}
	result, err := f.newOrchestratorWith(t, model, TurnTakingDirector{}).Run(context.Background(), Request{
		ConversationID: f.conversation.ID, TriggerMessageID: f.trigger.ID,
		Participants: f.participants[:2], MaxTurns: 4,
	})
	if err != nil {
		t.Fatalf("运行讨论失败: %v", err)
	}

	spans, err := f.spans.ListByRun(context.Background(), result.RunID)
	if err != nil {
		t.Fatalf("读取追踪 span 失败: %v", err)
	}
	if len(spans) != 5 {
		t.Fatalf("span 数 = %d，期望 5（1 根 + 2 回合 + 2 模型）", len(spans))
	}

	var root entity.AgentTraceSpan
	agents := make(map[uint64]entity.AgentTraceSpan)
	models := make([]entity.AgentTraceSpan, 0, 2)
	for _, span := range spans {
		if span.EndedAt == nil || span.EndedAt.Before(span.StartedAt) {
			t.Errorf("span %s 的结束时间不合法: %+v", span.SpanID, span)
		}
		switch span.Kind {
		case entity.TraceSpanKindOrchestration:
			root = span
		case entity.TraceSpanKindAgent:
			if span.TurnID != nil {
				agents[*span.TurnID] = span
			}
		case entity.TraceSpanKindModel:
			models = append(models, span)
		}
	}
	if root.SpanID == "" || root.ParentSpanID != nil || root.Status != entity.TraceSpanStatusOK {
		t.Errorf("根 span 不正确: %+v", root)
	}
	if len(agents) != 2 || len(models) != 2 {
		t.Fatalf("agent/model span 数不正确：%d/%d", len(agents), len(models))
	}
	for _, modelSpan := range models {
		if modelSpan.TurnID == nil || modelSpan.ParentSpanID == nil {
			t.Errorf("模型 span 缺父级或回合: %+v", modelSpan)
			continue
		}
		agent := agents[*modelSpan.TurnID]
		if agent.ParentSpanID == nil || *agent.ParentSpanID != root.SpanID || *modelSpan.ParentSpanID != agent.SpanID {
			t.Errorf("父子关系错误：root=%s agent=%+v model=%+v", root.SpanID, agent, modelSpan)
		}
	}
}

func TestDiscussionTraceSpansRecordModelRetry(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()

	model := &ScriptedModel{Replies: []ScriptedReply{
		{Err: context.DeadlineExceeded},
		{Content: "重试成功。", NextAction: entity.AgentTurnActionEnd},
	}}
	result, err := f.newOrchestratorWith(t, model, TurnTakingDirector{}).Run(context.Background(), Request{
		ConversationID: f.conversation.ID, TriggerMessageID: f.trigger.ID,
		Participants: f.participants[:1], MaxTurns: 2,
	})
	if err != nil {
		t.Fatalf("重试后应成功，实际: %v", err)
	}

	spans, err := f.spans.ListByRun(context.Background(), result.RunID)
	if err != nil {
		t.Fatalf("读取追踪 span 失败: %v", err)
	}
	var modelSpans []entity.AgentTraceSpan
	for _, span := range spans {
		if span.Kind == entity.TraceSpanKindModel {
			modelSpans = append(modelSpans, span)
		}
	}
	if len(modelSpans) != 2 || modelSpans[0].Status != entity.TraceSpanStatusError || modelSpans[1].Status != entity.TraceSpanStatusOK {
		t.Fatalf("模型重试 span = %+v，期望 error 后接 ok", modelSpans)
	}
}

func TestDiscussionContinuesWhenTraceSpanWriteFails(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()

	orchestrator := f.newOrchestratorWith(t, &ScriptedModel{Replies: []ScriptedReply{
		{Content: "讨论仍应完成。", NextAction: entity.AgentTurnActionEnd},
	}}, TurnTakingDirector{})
	orchestrator.deps.Spans = failingSpanRepository{}

	if _, err := orchestrator.Run(context.Background(), Request{
		ConversationID: f.conversation.ID, TriggerMessageID: f.trigger.ID,
		Participants: f.participants[:1], MaxTurns: 2,
	}); err != nil {
		t.Fatalf("追踪写入失败不应中断讨论，实际: %v", err)
	}
}
