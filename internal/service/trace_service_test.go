package service

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"narra/internal/model/entity"
	"narra/internal/repository"
)

type pricingTraceTurns struct {
	repository.TurnRepository
}

func (pricingTraceTurns) ListByRun(context.Context, uint64) ([]entity.AgentTurn, error) {
	return []entity.AgentTurn{{InputTokens: 1000, OutputTokens: 2000}}, nil
}

type pricingTraceSpans struct {
	repository.AgentTraceSpanRepository
}

func (pricingTraceSpans) ListByRun(context.Context, uint64) ([]entity.AgentTraceSpan, error) {
	return nil, nil
}

func TestTraceCostRequiresConfirmedTokenPrice(t *testing.T) {
	for _, tc := range []struct {
		name     string
		pricing  string
		wantCost bool
	}{
		{"manual legacy", `{"source":"user","input_per_million":2,"output_per_million":8,"currency":"CNY"}`, true},
		{"adopted candidate", `{"source":"user","pricing_mode":"token_price","confirmed_at":"2026-10-03T00:00:00Z","input_per_million":2,"output_per_million":8,"currency":"CNY"}`, true},
		{"search result", `{"source":"search","input_per_million":2,"output_per_million":8,"currency":"CNY"}`, false},
		{"search with timestamp", `{"source":"search","confirmed_at":"2026-10-03T00:00:00Z","input_per_million":2,"output_per_million":8,"currency":"CNY"}`, false},
		{"multiplier", `{"source":"user","pricing_mode":"multiplier","input_per_million":2,"output_per_million":8,"currency":"CNY"}`, false},
		{"unknown mode", `{"source":"user","pricing_mode":"unknown","input_per_million":2,"output_per_million":8,"currency":"CNY"}`, false},
		{"incomplete", `{"source":"user","input_per_million":2,"currency":"CNY"}`, false},
		{"negative", `{"source":"user","input_per_million":-2,"output_per_million":8,"currency":"CNY"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &traceService{turns: pricingTraceTurns{}, spans: pricingTraceSpans{}}
			run := entity.OrchestrationRun{ConfigSnapshot: json.RawMessage(`{"model_id":"test-model","pricing":` + tc.pricing + `}`)}
			got, err := svc.summary(context.Background(), run)
			if err != nil {
				t.Fatal(err)
			}
			if got.InputTokens != 1000 || got.OutputTokens != 2000 || got.TotalTokens != 3000 {
				t.Fatalf("token totals changed: %+v", got)
			}
			if !tc.wantCost {
				if got.EstimatedCost != nil {
					t.Fatalf("unconfirmed or invalid pricing produced cost: %v", *got.EstimatedCost)
				}
				return
			}
			if got.EstimatedCost == nil || math.Abs(*got.EstimatedCost-0.018) > 1e-9 || got.Currency != "CNY" {
				t.Fatalf("expected 0.018 CNY: %+v", got)
			}
		})
	}
}
