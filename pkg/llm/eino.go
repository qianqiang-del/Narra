// Eino 适配层：把 Client 暴露成 Eino 的 model.ToolCallingChatModel。
// 与 pkg/embedding/eino.go、internal/mcp/adapter.go 同一套路。
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// EinoChatModel 把 Client 适配成 Eino 的聊天模型。
type EinoChatModel struct {
	client *Client
	tools  []*schema.ToolInfo
}

var _ model.ToolCallingChatModel = (*EinoChatModel)(nil)

// NewEinoChatModel 用已配置好的 Client 创建适配器。
func NewEinoChatModel(client *Client) (*EinoChatModel, error) {
	if client == nil {
		return nil, fmt.Errorf("Eino 大模型适配器需要非空的 Client")
	}
	return &EinoChatModel{client: client}, nil
}

// WithTools 返回带工具的新实例。不能原地改接收者——react.Agent 会对同一个实例反复调用。
func (m *EinoChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return &EinoChatModel{client: m.client, tools: append([]*schema.ToolInfo(nil), tools...)}, nil
}

// Generate 实现 model.BaseChatModel。
func (m *EinoChatModel) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	options := model.GetCommonOptions(nil, opts...)

	// 工具优先取调用点传的，其次是用 WithTools 绑定的。
	tools := m.tools
	if len(options.Tools) > 0 {
		tools = options.Tools
	}
	toolDefinitions, err := toToolDefinitions(tools)
	if err != nil {
		return nil, err
	}
	messages, err := toMessages(in)
	if err != nil {
		return nil, err
	}

	request := ChatRequest{
		Messages:    messages,
		Tools:       toolDefinitions,
		Temperature: options.Temperature,
		MaxTokens:   options.MaxTokens,
	}
	if options.ToolChoice != nil {
		request.ToolChoice = toOpenAIToolChoice(*options.ToolChoice)
	}

	completion, err := m.client.Chat(ctx, request)
	if err != nil {
		return nil, err
	}
	return fromCompletion(completion), nil
}

// Stream 用非流式结果包成单 chunk 的流。逐 token 推送不在本模块。
func (m *EinoChatModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	message, err := m.Generate(ctx, in, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{message}), nil
}

// GetType 返回组件展示名。
func (m *EinoChatModel) GetType() string { return "NarraOpenAI" }

func toMessages(in []*schema.Message) ([]Message, error) {
	out := make([]Message, 0, len(in))
	for index, item := range in {
		if item == nil {
			return nil, fmt.Errorf("第 %d 条消息为 nil", index)
		}
		message := Message{
			Role:       string(item.Role),
			Content:    item.Content,
			ToolCallID: item.ToolCallID,
			Name:       item.Name,
		}
		for _, call := range item.ToolCalls {
			message.ToolCalls = append(message.ToolCalls, ToolCall{
				ID:   call.ID,
				Type: defaultString(call.Type, "function"),
				Function: FunctionCall{
					Name:      call.Function.Name,
					Arguments: call.Function.Arguments,
				},
			})
		}
		out = append(out, message)
	}
	return out, nil
}

func fromCompletion(completion *Completion) *schema.Message {
	message := &schema.Message{
		Role:             schema.Assistant,
		Content:          completion.Content,
		ReasoningContent: completion.ReasoningContent,
	}
	for _, call := range completion.ToolCalls {
		message.ToolCalls = append(message.ToolCalls, schema.ToolCall{
			ID:   call.ID,
			Type: defaultString(call.Type, "function"),
			Function: schema.FunctionCall{
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			},
		})
	}
	if completion.FinishReason != "" || completion.Usage != nil {
		meta := &schema.ResponseMeta{FinishReason: completion.FinishReason}
		if completion.Usage != nil {
			meta.Usage = &schema.TokenUsage{
				PromptTokens:     completion.Usage.PromptTokens,
				CompletionTokens: completion.Usage.CompletionTokens,
				TotalTokens:      completion.Usage.TotalTokens,
			}
		}
		message.ResponseMeta = meta
	}
	return message
}

// toToolDefinitions 把 Eino 的工具声明转成 OpenAI 的 tools 数组。
func toToolDefinitions(tools []*schema.ToolInfo) ([]ToolDefinition, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	definitions := make([]ToolDefinition, 0, len(tools))
	for _, item := range tools {
		if item == nil || strings.TrimSpace(item.Name) == "" {
			return nil, fmt.Errorf("工具缺少名字")
		}
		definition := ToolDefinition{
			Type:     "function",
			Function: FunctionDefinition{Name: item.Name, Description: item.Desc},
		}
		if item.ParamsOneOf != nil {
			jsonSchema, err := item.ParamsOneOf.ToJSONSchema()
			if err != nil {
				return nil, fmt.Errorf("解析工具 %q 的参数 schema 失败: %w", item.Name, err)
			}
			raw, err := json.Marshal(jsonSchema)
			if err != nil {
				return nil, fmt.Errorf("编码工具 %q 的参数 schema 失败: %w", item.Name, err)
			}
			definition.Function.Parameters = raw
		}
		definitions = append(definitions, definition)
	}
	return definitions, nil
}

func toOpenAIToolChoice(choice schema.ToolChoice) string {
	switch choice {
	case schema.ToolChoiceForbidden:
		return "none"
	case schema.ToolChoiceForced:
		return "required"
	case schema.ToolChoiceAllowed:
		return "auto"
	default:
		return ""
	}
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
