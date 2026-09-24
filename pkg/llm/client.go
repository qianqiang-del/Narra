// Package llm 提供对 OpenAI 兼容对话补全服务的访问。
//
// Client 是配置快照：base URL / key / model 在 NewClient 时固化，需在使用点现建现用。
// 与 Eino 的对接在 eino.go。
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Config 是一次对话所需的连接信息。APIKey 传明文，解密由调用方负责。
type Config struct {
	BaseURL string
	APIKey  string
	Model   string
	Timeout time.Duration
}

// Client 是已配置 OpenAI 兼容对话服务的 HTTP 客户端。
//
// httpClient 不设整条请求的死线——流式长回答会被它掐断；非流式的时限由 Chat 用 ctx 施加。
type Client struct {
	apiKey     string
	model      string
	chatURL    string
	timeout    time.Duration
	httpClient *http.Client
}

// NewClient 根据配置创建客户端。BaseURL 与 Model 必填，Timeout 缺省 60 秒。
func NewClient(cfg Config) (*Client, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("大模型服务地址不能为空")
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		return nil, fmt.Errorf("大模型 model 不能为空")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &Client{
		apiKey:     cfg.APIKey,
		model:      model,
		chatURL:    baseURL + "/chat/completions",
		timeout:    timeout,
		httpClient: &http.Client{},
	}, nil
}

// Message 是发往 /chat/completions 的一条消息。
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolCall 是模型返回的一次工具调用。
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall 的 Arguments 是 JSON 字符串，不是对象。
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolDefinition 是发给模型的工具声明。
type ToolDefinition struct {
	Type     string             `json:"type"`
	Function FunctionDefinition `json:"function"`
}

// FunctionDefinition 描述一个函数工具。
type FunctionDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// ChatRequest 是一次对话补全请求。
//
// Temperature / MaxTokens 为 nil 时不下发：推理模型对这两个字段会返回 400。
type ChatRequest struct {
	Messages    []Message
	Tools       []ToolDefinition
	ToolChoice  string
	Temperature *float32
	MaxTokens   *int
}

// Completion 是一次对话补全的结果。
type Completion struct {
	Content          string
	ReasoningContent string
	ToolCalls        []ToolCall
	FinishReason     string
	Usage            *Usage
}

// Usage 是 token 用量，部分上游不返回。
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type chatWireRequest struct {
	Model       string           `json:"model"`
	Messages    []Message        `json:"messages"`
	Stream      bool             `json:"stream"`
	Tools       []ToolDefinition `json:"tools,omitempty"`
	ToolChoice  string           `json:"tool_choice,omitempty"`
	Temperature *float32         `json:"temperature,omitempty"`
	MaxTokens   *int             `json:"max_tokens,omitempty"`
}

type chatWireResponse struct {
	Choices []struct {
		Message struct {
			Role             string     `json:"role"`
			Content          string     `json:"content"`
			ReasoningContent string     `json:"reasoning_content"`
			ToolCalls        []ToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *Usage `json:"usage"`
}

// chatWireStreamResponse 是流式响应的一个 data 块，增量在 delta 里。
type chatWireStreamResponse struct {
	Choices []struct {
		Delta struct {
			Content          string             `json:"content"`
			ReasoningContent string             `json:"reasoning_content"`
			ToolCalls        []chatWireToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *Usage `json:"usage"`
}

// chatWireToolCall 是流式返回的一段工具调用：arguments 分片到达，靠 index 拼。
type chatWireToolCall struct {
	Index    *int   `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// Chat 打一次对话补全并返回首个 choice。
//
// 只校验 choices 非空，不要求 content 非空：纯工具调用的一轮 content 就是空的。
func (c *Client) Chat(ctx context.Context, req ChatRequest) (*Completion, error) {
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("对话消息不能为空")
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	payload, err := json.Marshal(chatWireRequest{
		Model:       c.model,
		Messages:    req.Messages,
		Stream:      false,
		Tools:       req.Tools,
		ToolChoice:  req.ToolChoice,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	})
	if err != nil {
		return nil, fmt.Errorf("编码对话请求失败: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.chatURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("创建对话请求失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	response, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("连接失败或请求超时: %w", err)
	}
	defer response.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("读取服务响应失败")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("服务返回 HTTP %d: %s", response.StatusCode, safeUpstreamMessage(raw))
	}

	var result chatWireResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("服务响应不是合法 JSON，请确认 Base URL 指向 OpenAI 兼容接口")
	}
	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("服务响应没有 choices 字段，请确认 Base URL 指向 OpenAI 兼容接口")
	}

	choice := result.Choices[0]
	return &Completion{
		Content:          choice.Message.Content,
		ReasoningContent: choice.Message.ReasoningContent,
		ToolCalls:        choice.Message.ToolCalls,
		FinishReason:     choice.FinishReason,
		Usage:            result.Usage,
	}, nil
}

// StreamChunk 是流式补全的一块增量。Err 非空表示这一趟到此结束，其余字段无意义。
type StreamChunk struct {
	Content          string
	ReasoningContent string
	ToolCalls        []ToolCallDelta
	FinishReason     string
	Usage            *Usage
	Err              error
}

// ToolCallDelta 是一段工具调用增量：Arguments 可能只到一半，按 Index 累积。
type ToolCallDelta struct {
	Index     int
	ID        string
	Type      string
	Name      string
	Arguments string
}

// ChatStream 打一次流式对话补全，逐块返回增量。
//
// 请求体带 stream，逐行读上游的 data 块，读到 [DONE] 结束；channel 由本方法关闭。
// 上游断开或读失败时送出一个 Err 非空的块再关闭，所以调用方必须检查 Err。
// 整趟的时限由调用方的 ctx 决定——长回答不该被一个固定死线掐断。
func (c *Client) ChatStream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("对话消息不能为空")
	}

	payload, err := json.Marshal(chatWireRequest{
		Model:       c.model,
		Messages:    req.Messages,
		Stream:      true,
		Tools:       req.Tools,
		ToolChoice:  req.ToolChoice,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	})
	if err != nil {
		return nil, fmt.Errorf("编码对话请求失败: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.chatURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("创建对话请求失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	response, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("连接失败或请求超时: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		defer response.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		return nil, fmt.Errorf("服务返回 HTTP %d: %s", response.StatusCode, safeUpstreamMessage(raw))
	}

	chunks := make(chan StreamChunk, 16)
	go func() {
		defer close(chunks)
		defer response.Body.Close()

		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for scanner.Scan() {
			chunk, ok := parseStreamLine(scanner.Text())
			if !ok || chunk.isEmpty() {
				continue
			}
			select {
			case chunks <- chunk:
			case <-ctx.Done():
				return
			}
			if chunk.Err != nil {
				return
			}
		}
		if err := scanner.Err(); err != nil && ctx.Err() == nil {
			select {
			case chunks <- StreamChunk{Err: fmt.Errorf("读取流式响应失败: %w", err)}:
			case <-ctx.Done():
			}
		}
	}()
	return chunks, nil
}

// isEmpty 表示这块不含任何内容，只有心跳之类的空壳。
func (c StreamChunk) isEmpty() bool {
	return c.Err == nil && c.Content == "" && c.ReasoningContent == "" &&
		len(c.ToolCalls) == 0 && c.FinishReason == "" && c.Usage == nil
}

// parseStreamLine 解析一行 SSE：注释、非 data 行、[DONE] 与坏块都返回 false。
func parseStreamLine(line string) (StreamChunk, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "data:") {
		return StreamChunk{}, false
	}
	data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if data == "" || data == "[DONE]" {
		return StreamChunk{}, false
	}

	var piece chatWireStreamResponse
	if err := json.Unmarshal([]byte(data), &piece); err != nil {
		return StreamChunk{}, false
	}
	// 带 include_usage 的上游会在末尾单发一块只有 usage 的，没有 choices。
	if len(piece.Choices) == 0 {
		return StreamChunk{Usage: piece.Usage}, true
	}

	choice := piece.Choices[0]
	chunk := StreamChunk{
		Content:          choice.Delta.Content,
		ReasoningContent: choice.Delta.ReasoningContent,
		FinishReason:     choice.FinishReason,
		Usage:            piece.Usage,
	}
	for _, call := range choice.Delta.ToolCalls {
		index := 0
		if call.Index != nil {
			index = *call.Index
		}
		chunk.ToolCalls = append(chunk.ToolCalls, ToolCallDelta{
			Index:     index,
			ID:        call.ID,
			Type:      call.Type,
			Name:      call.Function.Name,
			Arguments: call.Function.Arguments,
		})
	}
	return chunk, true
}

// Ping 打一次最小对话补全，验证地址、密钥、模型三者可用。
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.Chat(ctx, ChatRequest{
		Messages: []Message{{Role: "user", Content: "Reply with OK only."}},
	})
	return err
}

// safeUpstreamMessage 只取上游错误体里的 message，不回显整个响应。
func safeUpstreamMessage(raw []byte) string {
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &body) == nil && body.Error.Message != "" {
		return truncate(body.Error.Message, 300)
	}
	return "请检查服务地址、API Key 和模型 ID"
}

func truncate(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}
