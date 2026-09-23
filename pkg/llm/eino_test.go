package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

// Stream 要把上游的块逐个转成 Eino 消息，结束时以 io.EOF 收尾。
func TestEinoStreamForwardsChunks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"你"}}]}`+"\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"好"}}]}`+"\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer server.Close()

	model := newTestEinoModel(t, server.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	reader, err := model.Stream(ctx, []*schema.Message{{Role: schema.User, Content: "hi"}})
	if err != nil {
		t.Fatalf("发起流式请求失败: %v", err)
	}
	defer reader.Close()

	var content strings.Builder
	for {
		chunk, err := reader.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("收流失败: %v", err)
		}
		content.WriteString(chunk.Content)
	}
	if content.String() != "你好" {
		t.Errorf("拼出的正文 = %q，期望 你好", content.String())
	}
}

// 下游提前不读了，上游请求必须被一起取消 —— 否则模型还在那边白生成、连接也不放。
func TestEinoStreamCancelsUpstreamWhenReaderClosed(t *testing.T) {
	canceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				close(canceled)
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

	model := newTestEinoModel(t, server.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	reader, err := model.Stream(ctx, []*schema.Message{{Role: schema.User, Content: "hi"}})
	if err != nil {
		t.Fatalf("发起流式请求失败: %v", err)
	}

	// 先读一块，确认流真的在动，然后提前收手。
	if _, err := reader.Recv(); err != nil {
		t.Fatalf("收第一块失败: %v", err)
	}
	reader.Close()

	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("下游关闭后，上游请求没有被取消")
	}
}

// newTestEinoModel 建一个指向测试上游的适配器。
func newTestEinoModel(t *testing.T, baseURL string) *EinoChatModel {
	t.Helper()

	client, err := NewClient(Config{BaseURL: baseURL, Model: "test-model"})
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}
	model, err := NewEinoChatModel(client)
	if err != nil {
		t.Fatalf("创建适配器失败: %v", err)
	}
	return model
}
