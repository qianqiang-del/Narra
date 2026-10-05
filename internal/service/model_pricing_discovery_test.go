package service

import (
	"context"
	"encoding/json"
	"fmt"
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
	expectedID  string
	calls       int
}

func (t *pricingSearchTools) ListTools() []internalmcp.ToolDescriptor { return t.descriptors }

func (t *pricingSearchTools) CallTool(_ context.Context, name string, args json.RawMessage) (*sdk.CallToolResult, error) {
	t.calls++
	if name != t.expectedID || !strings.Contains(string(args), "deepseek-flash") {
		return nil, nil
	}
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{
		Text: "https://example.com/pricing deepseek-flash input $0.4 / 1M tokens, output $1.6 / 1M tokens",
	}}}, nil
}

func TestSuggestPricingUsesSearchToolAndReturnsSourcedPrice(t *testing.T) {
	for _, tc := range []struct {
		name     string
		toolName string
	}{
		{name: "generic", toolName: "web_search"},
		{name: "tavily", toolName: "tavily_search"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			toolID := "mcp_search_" + tc.toolName
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
					if len(request.Tools) != 1 || !strings.Contains(string(request.Tools[0]), toolID) {
						t.Errorf("首次调用应只挂 %s，实际 %s", toolID, request.Tools)
					}
					_, _ = w.Write([]byte(fmt.Sprintf(`{"choices":[{"message":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":%q,"arguments":"{\"query\":\"deepseek-flash pricing per million tokens\"}"}}]}}]}`, toolID)))
					return
				}
				_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"model_id\":\"deepseek-flash\",\"input_per_million\":0.4,\"output_per_million\":1.6,\"currency\":\"USD\",\"source_url\":\"https://example.com/pricing\"}"}}]}`))
			}))
			defer server.Close()

			tools := &pricingSearchTools{expectedID: toolID, descriptors: []internalmcp.ToolDescriptor{{
				ID: toolID, ServerID: "search", RemoteName: tc.toolName,
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
		})
	}
}

func TestFindPricingSearchTool(t *testing.T) {
	tools := []internalmcp.ToolDescriptor{
		{ID: "mcp_search_tavily_research", RemoteName: "tavily_research", Description: "Can research and search the web"},
		{ID: "mcp_search_tavily_extract", RemoteName: "tavily_extract"},
		{ID: "mcp_search_tavily_search", RemoteName: "tavily_search"},
	}
	if selected := findPricingSearchTool(tools); selected == nil || selected.ID != "mcp_search_tavily_search" {
		t.Fatalf("应选择 Tavily 搜索工具，实际 %+v", selected)
	}
	tools = append(tools, internalmcp.ToolDescriptor{ID: "mcp_other_web_search", RemoteName: "web_search"})
	if selected := findPricingSearchTool(tools); selected == nil || selected.ID != "mcp_other_web_search" {
		t.Fatalf("应优先保留原有 web_search 工具，实际 %+v", selected)
	}
	if selected := findPricingSearchTool(tools[1:2]); selected != nil {
		t.Fatalf("提取工具不应冒充网页搜索: %+v", selected)
	}
	lookup := []internalmcp.ToolDescriptor{{ID: "mcp_other_lookup", RemoteName: "lookup", Description: "Search the web for current information"}}
	if selected := findPricingSearchTool(lookup); selected == nil || selected.ID != "mcp_other_lookup" {
		t.Fatalf("应识别描述明确的网页搜索工具，实际 %+v", selected)
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
	multiTierReply := `{"model_id":"deepseek-flash","input_per_million":0.15,"output_per_million":0.6,"currency":"USD","source_url":"https://api-docs.deepseek.com/quick_start/pricing"}`
	multiTierSearch := "deepseek-flash PRICING 1M INPUT TOKENS CACHE HIT $0.003 CACHE MISS $0.15 1M OUTPUT TOKENS OFF-PEAK $0.6 PEAK $1.2 https://api-docs.deepseek.com/quick_start/pricing"
	if got := parsePriceEvidence(multiTierReply, multiTierSearch, "deepseek-flash"); got == nil {
		t.Fatal("多档官方价格应接受缓存未命中输入价和非高峰输出价")
	}
}

func TestParsePriceEvidenceSupportsCandidatesWithoutChoosingMultiplier(t *testing.T) {
	searchText := `gpt-6-astra https://nowcoding.ai/api/pricing group Codex Mix model_ratio 5 completion_ratio 5 group_ratio 0.25`
	reply := `{"model_id":"gpt-6-astra","candidates":[{"pricing_mode":"multiplier","group":"Codex Mix","model_ratio":5,"completion_ratio":5,"group_ratio":0.25,"billing_note":"渠道倍率，缺少基础币价，无法直接换算实际费用","source_url":"https://nowcoding.ai/api/pricing"}]}`
	candidates := parsePriceCandidates(reply, searchText, "gpt-6-astra")
	if len(candidates) != 1 {
		t.Fatalf("倍率价格应作为候选返回，实际 %+v", candidates)
	}
	if candidates[0].PricingMode != "multiplier" || candidates[0].Group != "Codex Mix" || candidates[0].InputPerMillion != nil {
		t.Fatalf("倍率候选不应伪装成 token 单价: %+v", candidates[0])
	}
}

func TestParsePriceEvidenceReturnsMultipleUnitCandidates(t *testing.T) {
	searchText := `deepseek-flash https://api.deepseek.com/quick_start/pricing cache miss 0.15 cache hit 0.003 off-peak 0.6 peak 1.2`
	reply := `{"model_id":"deepseek-flash","candidates":[{"pricing_mode":"token_price","input_per_million":0.15,"output_per_million":0.6,"currency":"USD","billing_note":"缓存未命中输入、非高峰输出","source_url":"https://api.deepseek.com/quick_start/pricing"},{"pricing_mode":"token_price","input_per_million":0.003,"output_per_million":1.2,"currency":"USD","billing_note":"缓存命中输入、高峰输出","source_url":"https://api.deepseek.com/quick_start/pricing"}]}`
	candidates := parsePriceCandidates(reply, searchText, "deepseek-flash")
	if len(candidates) != 2 {
		t.Fatalf("多档价格应完整返回，实际 %+v", candidates)
	}
	if candidates[0].BillingNote == "" || candidates[1].InputPerMillion == nil {
		t.Fatalf("候选说明或价格缺失: %+v", candidates)
	}
}
