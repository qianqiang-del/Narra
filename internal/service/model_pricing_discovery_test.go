package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	internalmcp "narra/internal/mcp"
	"narra/internal/model/entity"
	"narra/internal/repository"
)

type pricingProviderRepo struct {
	repository.LLMProviderRepository
	provider *entity.LLMProvider
}

func (r pricingProviderRepo) FindByID(context.Context, uint64) (*entity.LLMProvider, error) {
	return r.provider, nil
}

type pricingSearchTools struct {
	descriptors []internalmcp.ToolDescriptor
	calls       int
}

func (t *pricingSearchTools) ListTools() []internalmcp.ToolDescriptor { return t.descriptors }

func (t *pricingSearchTools) CallTool(_ context.Context, name string, args json.RawMessage) (*sdk.CallToolResult, error) {
	t.calls++
	if name != "mcp_search_web_search" || !strings.Contains(string(args), "deepseek-flash") {
		return nil, nil
	}
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{
		Text: "https://example.com/pricing deepseek-flash input $0.4 / 1M tokens, output $1.6 / 1M tokens",
	}}}, nil
}

func TestSuggestPricingUsesWebSearchAndReturnsSourcedPrice(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var request struct {
			Model string            `json:"model"`
			Tools []json.RawMessage `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("请求格式错误: %v", err)
		}
		if request.Model != "deepseek-flash" {
			t.Errorf("使用的模型 = %q", request.Model)
		}
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			if len(request.Tools) != 1 {
				t.Errorf("首次调用应只挂一个搜索工具，实际 %d 个", len(request.Tools))
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"mcp_search_web_search","arguments":"{\"query\":\"deepseek-flash pricing per million tokens\"}"}}]}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"model_id\":\"deepseek-flash\",\"input_per_million\":0.4,\"output_per_million\":1.6,\"currency\":\"USD\",\"source_url\":\"https://example.com/pricing\"}"}}]}`))
	}))
	defer server.Close()

	tools := &pricingSearchTools{descriptors: []internalmcp.ToolDescriptor{{
		ID: "mcp_search_web_search", ServerID: "search", RemoteName: "web_search",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`),
	}}}
	provider := &entity.LLMProvider{BaseURL: server.URL, Models: json.RawMessage(`["deepseek-flash"]`), TimeoutSeconds: 5}
	svc := NewLLMProviderService(pricingProviderRepo{provider: provider}, nil, tools)
	result, err := svc.SuggestPricing(context.Background(), 1, "deepseek-flash")
	if err != nil {
		t.Fatalf("查价失败: %v", err)
	}
	if !result.Found || result.Pricing == nil || result.Pricing.InputPerMillion == nil || result.Pricing.OutputPerMillion == nil {
		t.Fatalf("没有返回可核对的价格: %+v", result)
	}
	if *result.Pricing.InputPerMillion != 0.4 || *result.Pricing.OutputPerMillion != 1.6 || result.Pricing.SourceURL != "https://example.com/pricing" {
		t.Fatalf("价格或来源错误: %+v", result.Pricing)
	}
	if result.Pricing.Source != "search" || result.Pricing.CheckedAt == nil || tools.calls != 1 || calls != 2 {
		t.Fatalf("没有完整执行搜索和提取: result=%+v toolCalls=%d modelCalls=%d", result, tools.calls, calls)
	}
}

func TestSuggestPricingRequiresConfiguredWebSearch(t *testing.T) {
	tools := &pricingSearchTools{}
	svc := NewLLMProviderService(pricingProviderRepo{}, nil, tools)
	_, err := svc.SuggestPricing(context.Background(), 1, "deepseek-flash")
	if err == nil || !strings.Contains(err.Error(), "联网搜索") {
		t.Fatalf("未配置搜索工具时应明确报错，实际 %v", err)
	}
}

func TestPriceEvidenceRejectsUnsupportedClaims(t *testing.T) {
	searchText := "deepseek-flash https://example.com/pricing input 0.4 output 1.6 USD per 1M"
	cases := []struct {
		name  string
		reply string
	}{
		{"wrong model", `{"model_id":"another-model","input_per_million":0.4,"output_per_million":1.6,"currency":"USD","source_url":"https://example.com/pricing"}`},
		{"invented source", `{"model_id":"deepseek-flash","input_per_million":0.4,"output_per_million":1.6,"currency":"USD","source_url":"https://unknown.example/pricing"}`},
		{"missing output price", `{"model_id":"deepseek-flash","input_per_million":0.4,"currency":"USD","source_url":"https://example.com/pricing"}`},
		{"invented prices", `{"model_id":"deepseek-flash","input_per_million":999,"output_per_million":888,"currency":"USD","source_url":"https://example.com/pricing"}`},
		{"negative price", `{"model_id":"deepseek-flash","input_per_million":-1,"output_per_million":1.6,"currency":"USD","source_url":"https://example.com/pricing"}`},
		{"non-json reply", "the price might be 0.4 USD"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parsePriceEvidence(tc.reply, searchText, "deepseek-flash"); got != nil {
				t.Fatalf("应拒绝没有证据支持的价格: %+v", got)
			}
		})
	}
	validReply := `{"model_id":"deepseek-flash","input_per_million":0.4,"output_per_million":1.6,"currency":"USD","source_url":"https://example.com/pricing"}`
	if got := parsePriceEvidence(validReply, "https://example.com/pricing input 0.4 output 1.6 USD", "deepseek-flash"); got != nil {
		t.Fatalf("搜索结果未提及指定模型时不能填价: %+v", got)
	}
}
