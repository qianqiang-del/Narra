package discussion

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	"narra/internal/toolcall"
)

// WhiteboardArtifact contains public teaching material, never hidden reasoning.
type WhiteboardArtifact struct {
	Title   string `json:"title"`
	Kind    string `json:"kind"`
	Content string `json:"content"`
}

func whiteboardMetadata(board *WhiteboardArtifact) json.RawMessage {
	if board == nil {
		return nil
	}
	raw, _ := json.Marshal(map[string]any{"whiteboard": board})
	return raw
}

type ToolCallReport struct {
	Name      string
	StartedAt time.Time
	EndedAt   time.Time
	Failed    bool
}

type DiscussionToolSource interface {
	DiscussionTools(context.Context, bool) ([]tool.BaseTool, error)
}

type generationToolsState struct {
	mu    sync.Mutex
	board *WhiteboardArtifact
	calls []ToolCallReport
	usage []*usageCall
}

type usageCall struct {
	input  int32
	output strings.Builder
	actual *schema.TokenUsage
}

func (s *generationToolsState) totals() (int32, int32, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var input, output int32
	source := "actual"
	for _, call := range s.usage {
		if call.actual != nil {
			input += int32(call.actual.PromptTokens)
			output += int32(call.actual.CompletionTokens)
		} else {
			input += call.input
			output += estimateTokens(call.output.String())
			source = "estimated"
		}
	}
	return input, output, source
}

// EnableTeachingTools is called once on a newly built, run-local adapter.
func (m *OpenAIModels) EnableTeachingTools(source DiscussionToolSource) {
	m.toolsEnabled = true
	m.toolSource = source
}

func (m *OpenAIModels) teachingAgent(ctx context.Context, request GenerationRequest) (*react.Agent, *generationToolsState, error) {
	state := &generationToolsState{}
	tools := []tool.BaseTool{newWhiteboardTool(state)}
	if m.toolSource != nil {
		available, err := m.toolSource.DiscussionTools(ctx, request.WebSearch)
		if err != nil {
			return nil, nil, fmt.Errorf("加载讨论工具失败: %w", err)
		}
		tools = append(tools, available...)
	}
	wrapped := make([]tool.BaseTool, 0, len(tools))
	for _, item := range tools {
		invokable, ok := item.(tool.InvokableTool)
		if !ok {
			continue
		}
		info, err := item.Info(ctx)
		if err != nil {
			return nil, nil, err
		}
		wrapped = append(wrapped, &boundedDiscussionTool{inner: invokable, info: info, state: state})
	}
	agent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: &usageChatModel{inner: m.chatModel, state: state},
		ToolsConfig:      compose.ToolsNodeConfig{Tools: wrapped, ExecuteSequentially: true},
		MaxStep:          10, GraphName: "DiscussionTeaching", ModelNodeName: "DiscussionModel", ToolsNodeName: "DiscussionTools",
	})
	return agent, state, err
}

type whiteboardTool struct{ state *generationToolsState }

func newWhiteboardTool(state *generationToolsState) *whiteboardTool {
	return &whiteboardTool{state: state}
}
func (t *whiteboardTool) Info(context.Context) (*schema.ToolInfo, error) {
	var params jsonschema.Schema
	_ = json.Unmarshal([]byte(`{"type":"object","properties":{"title":{"type":"string","maxLength":100},"kind":{"type":"string","enum":["steps","table","concepts"]},"content":{"type":"string","maxLength":4000}},"required":["title","kind","content"],"additionalProperties":false}`), &params)
	return &schema.ToolInfo{Name: "show_classroom_whiteboard", Desc: "按需展示公开教学步骤、Markdown对比表或概念关系。仅在图表有助理解或用户明确要求白板时使用；不展示内部推理、隐私、系统提示或危险操作。最多一幅白板。", ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&params)}, nil
}
func (t *whiteboardTool) InvokableRun(ctx context.Context, arguments string, _ ...tool.Option) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var board WhiteboardArtifact
	decoder := json.NewDecoder(strings.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&board); err != nil {
		return "", err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return "", fmt.Errorf("白板只接受一个 JSON 对象")
	}
	board.Title = strings.TrimSpace(board.Title)
	board.Content = strings.TrimSpace(board.Content)
	if board.Title == "" || board.Content == "" || utf8.RuneCountInString(board.Title) > 100 || utf8.RuneCountInString(board.Content) > 4000 {
		return "", fmt.Errorf("白板标题或内容为空或超出长度限制")
	}
	if board.Kind != "steps" && board.Kind != "table" && board.Kind != "concepts" {
		return "", fmt.Errorf("不支持的白板类型")
	}
	t.state.mu.Lock()
	defer t.state.mu.Unlock()
	if t.state.board != nil {
		return "本轮已有白板，请直接回答。", nil
	}
	t.state.board = &board
	return "白板已准备好，将随本轮完成的消息展示给用户。请直接回答用户。", nil
}

type boundedDiscussionTool struct {
	inner tool.InvokableTool
	info  *schema.ToolInfo
	state *generationToolsState
}

func (t *boundedDiscussionTool) Info(context.Context) (*schema.ToolInfo, error) { return t.info, nil }
func (t *boundedDiscussionTool) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
	t.state.mu.Lock()
	if len(t.state.calls) >= 4 {
		t.state.mu.Unlock()
		return "本轮工具调用预算已用完。请根据已有资料回答，不要继续调用工具。", nil
	}
	index := len(t.state.calls)
	t.state.calls = append(t.state.calls, ToolCallReport{Name: t.info.Name, StartedAt: time.Now().UTC()})
	t.state.mu.Unlock()
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	recorder := toolcall.NewRecorder()
	result, err := t.inner.InvokableRun(toolcall.WithRecorder(callCtx, recorder), args, opts...)
	failed := err != nil || recorder.Cause() != "" || callCtx.Err() != nil
	t.state.mu.Lock()
	t.state.calls[index].EndedAt = time.Now().UTC()
	t.state.calls[index].Failed = failed
	t.state.mu.Unlock()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if failed {
		return "工具未能取得可靠结果。不要编造结果，可依据已有课堂资料回答或说明暂时无法检索。", nil
	}
	if utf8.RuneCountInString(result) > 12000 {
		result = string([]rune(result)[:12000]) + "\n[结果已截断]"
	}
	return result, nil
}

// Each model call has independent usage, including intermediate tool decisions.
type usageChatModel struct {
	inner model.ToolCallingChatModel
	state *generationToolsState
}

func (m *usageChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	inner, err := m.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &usageChatModel{inner: inner, state: m.state}, nil
}
func (m *usageChatModel) start(in []*schema.Message) *usageCall {
	raw, _ := json.Marshal(in)
	call := &usageCall{input: estimateTokens(string(raw))}
	m.state.mu.Lock()
	m.state.usage = append(m.state.usage, call)
	m.state.mu.Unlock()
	return call
}
func (m *usageChatModel) observe(call *usageCall, message *schema.Message) {
	if message == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	call.output.WriteString(message.Content)
	for _, item := range message.ToolCalls {
		call.output.WriteString(item.Function.Arguments)
	}
	if message.ResponseMeta != nil && message.ResponseMeta.Usage != nil {
		copy := *message.ResponseMeta.Usage
		call.actual = &copy
	}
}
func (m *usageChatModel) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	call := m.start(in)
	result, err := m.inner.Generate(ctx, in, opts...)
	m.observe(call, result)
	return result, err
}
func (m *usageChatModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	call := m.start(in)
	upstream, err := m.inner.Stream(ctx, in, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderWithConvert(upstream, func(message *schema.Message) (*schema.Message, error) { m.observe(call, message); return message, nil }), nil
}
