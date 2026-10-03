package discussion

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	"narra/internal/model/entity"
)

func newSpanID() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("生成 span ID 失败: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func (o *Orchestrator) traceID(log *zap.Logger) string {
	id, err := newSpanID()
	if err != nil {
		log.Warn("生成追踪 span ID 失败，本次跳过该 span", zap.Error(err))
	}
	return id
}

func (o *Orchestrator) recordSpan(log *zap.Logger, span *entity.AgentTraceSpan) {
	if span.SpanID == "" {
		return
	}
	if err := o.deps.Spans.Create(context.Background(), span); err != nil {
		log.Warn("写入追踪 span 失败", zap.String("kind", span.Kind), zap.Error(err))
	}
}

func traceAttributes(values map[string]any) json.RawMessage {
	raw, err := json.Marshal(values)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

func (o *Orchestrator) recordRootSpan(log *zap.Logger, run *entity.OrchestrationRun, spanID string, startedAt time.Time, status, stopReason string, turns int, cause error) {
	endedAt := time.Now().UTC()
	span := &entity.AgentTraceSpan{
		TraceID: run.TraceID, SpanID: spanID, RunID: run.ID,
		Kind: entity.TraceSpanKindOrchestration, Name: "discussion.run", Status: status,
		StartedAt: startedAt, EndedAt: &endedAt,
		Attributes: traceAttributes(map[string]any{"turns": turns, "stop_reason": stopReason}),
	}
	if cause != nil {
		message := truncate(cause.Error(), errorMessageLimit)
		span.ErrorMessage = &message
	}
	o.recordSpan(log, span)
}

func (o *Orchestrator) recordAgentSpan(log *zap.Logger, run *entity.OrchestrationRun, turn *entity.AgentTurn, spanID, rootSpanID string, startedAt time.Time, participant Participant, status, nextAction string, cause error) {
	endedAt := time.Now().UTC()
	span := &entity.AgentTraceSpan{
		TraceID: run.TraceID, SpanID: spanID, RunID: run.ID, TurnID: &turn.ID,
		Kind: entity.TraceSpanKindAgent, Name: "discussion.turn", Status: status,
		StartedAt: startedAt, EndedAt: &endedAt,
		Attributes: traceAttributes(map[string]any{"turn_no": turn.TurnNo, "agent": participant.Name, "next_action": nextAction}),
	}
	if rootSpanID != "" {
		span.ParentSpanID = &rootSpanID
	}
	if cause != nil {
		message := truncate(cause.Error(), errorMessageLimit)
		span.ErrorMessage = &message
	}
	o.recordSpan(log, span)
}

func (o *Orchestrator) recordModelSpan(log *zap.Logger, run *entity.OrchestrationRun, turn *entity.AgentTurn, spanID, agentSpanID string, startedAt time.Time, request GenerationRequest, attempt int, response GenerationResponse, cause error) {
	endedAt := time.Now().UTC()
	status := entity.TraceSpanStatusOK
	if cause != nil {
		status = entity.TraceSpanStatusError
	}
	span := &entity.AgentTraceSpan{
		TraceID: run.TraceID, SpanID: spanID, RunID: run.ID, TurnID: &turn.ID,
		Kind: entity.TraceSpanKindModel, Name: "chat.completion", Status: status,
		StartedAt: startedAt, EndedAt: &endedAt,
		Attributes: traceAttributes(map[string]any{"attempt": attempt, "input_tokens": response.InputTokens, "output_tokens": response.OutputTokens, "token_source": response.TokenSource}),
	}
	if agentSpanID != "" {
		span.ParentSpanID = &agentSpanID
	}
	input := truncate(fmt.Sprintf("第 %d 轮：%s", request.TurnNo, request.Participant.Name), errorMessageLimit)
	span.InputSummary = &input
	if cause != nil {
		message := truncate(cause.Error(), errorMessageLimit)
		span.ErrorMessage = &message
	} else {
		output := truncate(response.Content, errorMessageLimit)
		span.OutputSummary = &output
	}
	o.recordSpan(log, span)
}
