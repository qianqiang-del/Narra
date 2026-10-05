package discussion

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"narra/pkg/llm"
)

func TestDiscussionWhiteboardToolRoundTrip(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprint(streaming), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Messages []llm.Message     `json:"messages"`
					Tools    []json.RawMessage `json:"tools"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				calls++
				if len(request.Tools) == 0 {
					t.Error("whiteboard tool not bound")
				}
				message := map[string]any{"role": "assistant"}
				finish := "stop"
				if calls == 1 {
					message["tool_calls"] = []map[string]any{{"index": 0, "id": "board-1", "type": "function", "function": map[string]string{"name": "show_classroom_whiteboard", "arguments": `{"title":"Agent loop","kind":"steps","content":"1. Observe\n2. Act"}`}}}
					finish = "tool_calls"
				} else {
					found := false
					for _, m := range request.Messages {
						if m.Role == "tool" && m.ToolCallID == "board-1" {
							found = true
						}
					}
					if !found {
						t.Error("tool result missing in followup")
					}
					if streaming {
						message["content"] = "<content>See the board.</content><next_action>end</next_action><next_speaker></next_speaker>"
					} else {
						message["content"] = `{"content":"See the board.","next_action":"end"}`
					}
				}
				if streaming {
					w.Header().Set("Content-Type", "text/event-stream")
					raw, _ := json.Marshal(map[string]any{"choices": []map[string]any{{"delta": message, "finish_reason": finish}}})
					fmt.Fprintf(w, "data: %s\n\n", raw)
					// Providers may send usage AFTER finish_reason.
					fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5,\"total_tokens\":15}}\n\ndata: [DONE]\n\n")
				} else {
					json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": message, "finish_reason": finish}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}})
				}
			}))
			defer server.Close()
			client, _ := llm.NewClient(llm.Config{BaseURL: server.URL, Model: "test"})
			adapter, _ := NewOpenAIModels(client)
			adapter.EnableTeachingTools(nil)
			var board *WhiteboardArtifact
			var input, output int32
			if streaming {
				stream, err := adapter.GenerateStream(context.Background(), GenerationRequest{Topic: "Explain Agent loop"})
				if err != nil {
					t.Fatal(err)
				}
				var text strings.Builder
				for chunk := range stream {
					if chunk.Err != nil {
						t.Fatal(chunk.Err)
					}
					text.WriteString(chunk.Delta)
					if chunk.Done {
						board = chunk.Whiteboard
						input = chunk.InputTokens
						output = chunk.OutputTokens
						if len(chunk.ToolCalls) != 1 {
							t.Fatal("tool trace missing")
						}
					}
				}
				if text.String() != "See the board." {
					t.Fatal(text.String())
				}
			} else {
				response, err := adapter.Generate(context.Background(), GenerationRequest{Topic: "Explain Agent loop"})
				if err != nil {
					t.Fatal(err)
				}
				board = response.Whiteboard
				input = response.InputTokens
				output = response.OutputTokens
				if len(response.ToolCalls) != 1 {
					t.Fatal("tool trace missing")
				}
			}
			if board == nil || board.Title != "Agent loop" {
				t.Fatalf("missing board: %+v", board)
			}
			if input != 20 || output != 10 {
				t.Fatalf("usage must include both calls: %d/%d", input, output)
			}
			if calls != 2 {
				t.Fatal(calls)
			}
		})
	}
}

func TestWhiteboardValidation(t *testing.T) {
	for _, raw := range []string{`{"title":"","kind":"steps","content":"x"}`, `{"title":"x","kind":"reasoning","content":"private"}`, `{"title":"x","kind":"steps","content":"x","reasoning":"secret"}`} {
		tool := newWhiteboardTool(&generationToolsState{})
		if _, err := tool.InvokableRun(context.Background(), raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
