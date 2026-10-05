package service

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"narra/internal/agent/discussion"
	responsedto "narra/internal/model/dto/response"
	"narra/internal/model/entity"
	"narra/internal/repository"
	apperrors "narra/pkg/errors"
)

type traceService struct {
	runs  repository.RunRepository
	turns repository.TurnRepository
	spans repository.AgentTraceSpanRepository
}

var _ TraceService = (*traceService)(nil)

func NewTraceService(runs repository.RunRepository, turns repository.TurnRepository, spans repository.AgentTraceSpanRepository) TraceService {
	return &traceService{runs: runs, turns: turns, spans: spans}
}

func (s *traceService) ListRuns(ctx context.Context, conversationID uint64, limit int) ([]responsedto.DiscussionRun, error) {
	runs, err := s.runs.ListByConversation(ctx, conversationID, normalizeTraceLimit(limit))
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "查询讨论运行失败", err)
	}
	out := make([]responsedto.DiscussionRun, 0, len(runs))
	for _, run := range runs {
		item, err := s.summary(ctx, run)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *traceService) GetTrace(ctx context.Context, conversationID, runID uint64) (*responsedto.DiscussionTrace, error) {
	run, err := s.runs.FindByID(ctx, runID)
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && run.ConversationID != conversationID) {
		return nil, apperrors.New(apperrors.CodeNotFound, "讨论运行不存在")
	}
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "查询讨论运行失败", err)
	}
	spans, err := s.spans.ListByRun(ctx, runID)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "查询讨论链路失败", err)
	}
	summary, err := s.summary(ctx, *run)
	if err != nil {
		return nil, err
	}
	out := &responsedto.DiscussionTrace{Run: summary, Spans: make([]responsedto.TraceSpan, 0, len(spans))}
	for _, span := range spans {
		out.Spans = append(out.Spans, safeSpan(span))
	}
	return out, nil
}

func (s *traceService) summary(ctx context.Context, run entity.OrchestrationRun) (responsedto.DiscussionRun, error) {
	turns, err := s.turns.ListByRun(ctx, run.ID)
	if err != nil {
		return responsedto.DiscussionRun{}, apperrors.NewWithErr(apperrors.CodeInternalError, "查询讨论回合失败", err)
	}
	var input, output int64
	for _, turn := range turns {
		input += int64(turn.InputTokens)
		output += int64(turn.OutputTokens)
	}
	spans, spanErr := s.spans.ListByRun(ctx, run.ID)
	if spanErr != nil {
		return responsedto.DiscussionRun{}, apperrors.NewWithErr(apperrors.CodeInternalError, "查询讨论链路失败", spanErr)
	}
	item := responsedto.DiscussionRun{ID: run.ID, TraceID: run.TraceID, Status: run.Status, StopReason: run.StopReason, StartedAt: run.StartedAt, FinishedAt: run.FinishedAt, TurnCount: len(turns), InputTokens: input, OutputTokens: output, TotalTokens: input + output, TokenSource: tokenSource(input, output), ErrorMessage: safeText(deref(run.ErrorMessage)), CreatedAt: run.CreatedAt}
	item.TokenSource = spanTokenSource(spans, item.TokenSource)
	var snapshot struct {
		ModelID string                   `json:"model_id"`
		Pricing *discussion.ModelPricing `json:"pricing"`
	}
	if len(run.ConfigSnapshot) > 0 && json.Unmarshal(run.ConfigSnapshot, &snapshot) == nil {
		item.ModelID = snapshot.ModelID
		if snapshot.Pricing != nil {
			item.Currency = snapshot.Pricing.Currency
			if item.Currency == "" {
				item.Currency = "USD"
			}
			confirmed := snapshot.Pricing.Source == "user"
			tokenPrice := snapshot.Pricing.PricingMode == "" || snapshot.Pricing.PricingMode == "token_price"
			if tokenPrice && confirmed && snapshot.Pricing.InputPerMillion != nil && snapshot.Pricing.OutputPerMillion != nil &&
				validPrice(*snapshot.Pricing.InputPerMillion) && validPrice(*snapshot.Pricing.OutputPerMillion) {
				cost := float64(input)/1_000_000*(*snapshot.Pricing.InputPerMillion) + float64(output)/1_000_000*(*snapshot.Pricing.OutputPerMillion)
				item.EstimatedCost = &cost
			}
		}
	}
	if run.StartedAt != nil && run.FinishedAt != nil {
		duration := run.FinishedAt.Sub(*run.StartedAt).Milliseconds()
		item.DurationMS = &duration
	}
	return item, nil
}

func safeSpan(span entity.AgentTraceSpan) responsedto.TraceSpan {
	item := responsedto.TraceSpan{ID: span.ID, SpanID: span.SpanID, ParentSpanID: span.ParentSpanID, Kind: span.Kind, Name: span.Name, Status: span.Status, StartedAt: span.StartedAt, EndedAt: span.EndedAt, TurnID: span.TurnID, InputSummary: safeText(deref(span.InputSummary)), OutputSummary: safeText(deref(span.OutputSummary)), ErrorMessage: safeText(deref(span.ErrorMessage))}
	if span.EndedAt != nil {
		duration := span.EndedAt.Sub(span.StartedAt).Milliseconds()
		item.DurationMS = &duration
	}
	var attrs struct {
		InputTokens  int64  `json:"input_tokens"`
		OutputTokens int64  `json:"output_tokens"`
		Attempt      int    `json:"attempt"`
		TurnNo       int    `json:"turn_no"`
		Turns        int    `json:"turns"`
		Agent        string `json:"agent"`
		NextAction   string `json:"next_action"`
		StopReason   string `json:"stop_reason"`
	}
	if json.Unmarshal(span.Attributes, &attrs) == nil {
		item.InputTokens, item.OutputTokens, item.Attempt = attrs.InputTokens, attrs.OutputTokens, attrs.Attempt
		item.TurnNo, item.Turns, item.AgentName, item.NextAction, item.StopReason = attrs.TurnNo, attrs.Turns, attrs.Agent, attrs.NextAction, attrs.StopReason
		if item.Kind == entity.TraceSpanKindOrchestration && item.InputSummary == "" {
			item.InputSummary = safeText(strings.TrimSpace("运行总回合：" + strconv.Itoa(attrs.Turns) + "；停止原因：" + attrs.StopReason))
		}
		if item.Kind == entity.TraceSpanKindAgent && item.InputSummary == "" {
			item.InputSummary = safeText(strings.TrimSpace("角色：" + attrs.Agent + "；第 " + strconv.Itoa(attrs.TurnNo) + " 轮；下一步：" + attrs.NextAction))
		}
	}
	return item
}

func normalizeTraceLimit(limit int) int {
	if limit <= 0 {
		return 20
	}
	if limit > 100 {
		return 100
	}
	return limit
}
func tokenSource(input, output int64) string {
	if input == 0 && output == 0 {
		return "none"
	}
	return "reported"
}

func spanTokenSource(spans []entity.AgentTraceSpan, fallback string) string {
	hasActual, hasEstimated := false, false
	for _, span := range spans {
		var attrs struct {
			TokenSource string `json:"token_source"`
		}
		if json.Unmarshal(span.Attributes, &attrs) != nil {
			continue
		}
		switch attrs.TokenSource {
		case "actual":
			hasActual = true
		case "estimated":
			hasEstimated = true
		}
	}
	switch {
	case hasActual && hasEstimated:
		return "mixed"
	case hasActual:
		return "actual"
	case hasEstimated:
		return "estimated"
	default:
		return fallback
	}
}
func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
func safeText(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "\r", " "), "\n", " "))
	lower := strings.ToLower(value)
	if strings.Contains(lower, "proxyconnect") || strings.Contains(lower, "dial tcp") || strings.Contains(lower, "stacktrace") {
		return "模型服务连接失败"
	}
	if len(value) > 600 {
		value = value[:600] + "…"
	}
	return value
}
