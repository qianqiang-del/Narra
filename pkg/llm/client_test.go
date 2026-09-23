package llm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 逐行解析是流式链路里最容易在边界上翻车的一段：上游的流里混着空行、注释心跳、
// [DONE]、只有 usage 的尾块，工具调用的参数还是分片到达的。这里把每种边界钉死。
func TestParseStreamLine(t *testing.T) {
	cases := []struct {
		name   string
		line   string
		wantOK bool
		check  func(t *testing.T, chunk StreamChunk)
	}{
		{
			name:   "普通正文",
			line:   `data: {"choices":[{"delta":{"content":"你好"}}]}`,
			wantOK: true,
			check: func(t *testing.T, chunk StreamChunk) {
				if chunk.Content != "你好" {
					t.Errorf("Content = %q，期望 你好", chunk.Content)
				}
			},
		},
		{
			name:   "推理内容",
			line:   `data: {"choices":[{"delta":{"reasoning_content":"想一想"}}]}`,
			wantOK: true,
			check: func(t *testing.T, chunk StreamChunk) {
				if chunk.ReasoningContent != "想一想" {
					t.Errorf("ReasoningContent = %q，期望 想一想", chunk.ReasoningContent)
				}
			},
		},
		{name: "结束标记", line: `data: [DONE]`, wantOK: false},
		{name: "注释心跳", line: `: ping`, wantOK: false},
		{name: "空行", line: ``, wantOK: false},
		{name: "data 后为空", line: `data:`, wantOK: false},
		{name: "非 data 行", line: `event: message`, wantOK: false},
		{name: "坏 JSON", line: `data: {oops`, wantOK: false},
		{
			name:   "只有 usage 的尾块",
			line:   `data: {"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`,
			wantOK: true,
			check: func(t *testing.T, chunk StreamChunk) {
				if chunk.Usage == nil || chunk.Usage.TotalTokens != 3 {
					t.Errorf("Usage = %+v，期望 total_tokens = 3", chunk.Usage)
				}
			},
		},
		{
			name:   "带 finish_reason",
			line:   `data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			wantOK: true,
			check: func(t *testing.T, chunk StreamChunk) {
				if chunk.FinishReason != "stop" {
					t.Errorf("FinishReason = %q，期望 stop", chunk.FinishReason)
				}
			},
		},
		{
			name:   "工具调用分片",
			line:   `data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_1","type":"function","function":{"name":"web_search","arguments":"{\"q\":"}}]}}]}`,
			wantOK: true,
			check: func(t *testing.T, chunk StreamChunk) {
				if len(chunk.ToolCalls) != 1 {
					t.Fatalf("ToolCalls = %d 条，期望 1", len(chunk.ToolCalls))
				}
				call := chunk.ToolCalls[0]
				if call.Index != 1 || call.ID != "call_1" || call.Name != "web_search" || call.Arguments != `{"q":` {
					t.Errorf("ToolCalls[0] = %+v，字段没有原样带出", call)
				}
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			chunk, ok := parseStreamLine(testCase.line)
			if ok != testCase.wantOK {
				t.Fatalf("parseStreamLine(%q) ok = %v，期望 %v", testCase.line, ok, testCase.wantOK)
			}
			if ok && testCase.check != nil {
				testCase.check(t, chunk)
			}
		})
	}
}

// 整链路：请求带 stream=true，逐块解析上游，空行与心跳跳过，工具调用分片原样带出。
func TestChatStreamParsesUpstream(t *testing.T) {
	streamBody := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"你"}}]}`,
		``,
		`: ping`,
		`data: {"choices":[{"delta":{"content":"好"}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"web_search","arguments":"{\"q\":"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"x\"}"}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: {"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`,
		`data: [DONE]`,
	}, "\n") + "\n"

	requestBody := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("请求路径 = %q，期望 /chat/completions", r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		requestBody <- string(raw)

		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, streamBody)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL, Model: "test-model", APIKey: "sk-test"})
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	chunks, err := client.ChatStream(ctx, ChatRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("发起流式请求失败: %v", err)
	}

	var collected []StreamChunk
	for chunk := range chunks {
		if chunk.Err != nil {
			t.Fatalf("流里出现错误块: %v", chunk.Err)
		}
		collected = append(collected, chunk)
	}

	// 请求体必须带 stream=true，否则上游会按非流式回一坨 JSON。
	if body := <-requestBody; !strings.Contains(body, `"stream":true`) {
		t.Errorf("请求体里没有 stream=true: %s", body)
	}

	if len(collected) != 6 {
		t.Fatalf("收到 %d 块，期望 6（正文×2、工具分片×2、结束、用量）：%+v", len(collected), collected)
	}
	if collected[0].Content+collected[1].Content != "你好" {
		t.Errorf("正文 = %q + %q，期望 你 + 好", collected[0].Content, collected[1].Content)
	}

	first := collected[2].ToolCalls
	if len(first) != 1 || first[0].Index != 0 || first[0].ID != "call_1" ||
		first[0].Name != "web_search" || first[0].Arguments != `{"q":` {
		t.Errorf("第一段工具调用 = %+v", first)
	}
	second := collected[3].ToolCalls
	if len(second) != 1 || second[0].Index != 0 || second[0].Arguments != `"x"}` {
		t.Errorf("第二段工具调用 = %+v", second)
	}
	if collected[4].FinishReason != "stop" {
		t.Errorf("finish_reason = %q，期望 stop", collected[4].FinishReason)
	}
	if collected[5].Usage == nil || collected[5].Usage.TotalTokens != 3 {
		t.Errorf("用量块 = %+v，期望 total_tokens = 3", collected[5].Usage)
	}
}

// 上游拒绝（限流、密钥错）要在开流之前直接报错，不能给一个空 channel 让调用方干等。
func TestChatStreamReportsUpstreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"rate limited"}}`)
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL, Model: "test-model"})
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	chunks, err := client.ChatStream(ctx, ChatRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatal("上游返回 429 时应当直接报错")
	}
	if chunks != nil {
		t.Error("报错时不该返回 channel")
	}
	if !strings.Contains(err.Error(), "429") || !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("错误信息 = %q，期望带上状态码与上游原因", err.Error())
	}
}

// 取消之后 channel 要能关闭：调用方收手不该留下一个永远堵着的协程。
func TestChatStreamStopsOnContextCancel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"x"}}]}`+"\n\n")
				if flusher != nil {
					flusher.Flush()
				}
			}
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL, Model: "test-model"})
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	chunks, err := client.ChatStream(ctx, ChatRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("发起流式请求失败: %v", err)
	}

	select {
	case <-chunks:
	case <-time.After(3 * time.Second):
		t.Fatal("3 秒内没读到第一块")
	}

	cancel()

	deadline := time.After(3 * time.Second)
	for {
		select {
		case _, ok := <-chunks:
			if !ok {
				return // channel 已关闭，符合预期
			}
		case <-deadline:
			t.Fatal("取消之后 channel 没有关闭")
		}
	}
}
