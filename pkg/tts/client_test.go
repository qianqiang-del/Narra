package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"narra/pkg/config"
)

// 假的 WAV 载荷。内容无所谓，测的是它有没有原样从下载那一步传出来。
var testAudio = []byte("RIFF\x00\x00\x00\x00WAVEfmt fake-audio-bytes")

func TestSynthesizePostsAndDownloadsAudio(t *testing.T) {
	client := newTestClient(t, func(audioURL string) http.HandlerFunc {
		return func(response http.ResponseWriter, request *http.Request) {
			if request.Method != http.MethodPost {
				t.Errorf("method = %q, want POST", request.Method)
			}
			if request.URL.Path != qwenSynthesisPath {
				t.Errorf("path = %q, want %q", request.URL.Path, qwenSynthesisPath)
			}
			if got := request.Header.Get("Authorization"); got != "Bearer tts-secret" {
				t.Errorf("Authorization = %q, want the bearer key", got)
			}

			var body synthesisRequest
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if body.Model != "qwen3-tts-flash" {
				t.Errorf("model = %q, want qwen3-tts-flash", body.Model)
			}
			if body.Input.Text != "同学们好" || body.Input.Voice != "Cherry" || body.Input.LanguageType != "Chinese" {
				t.Errorf("unexpected input: %#v", body.Input)
			}

			writeSynthesisResponse(response, audioURL)
		}
	})

	audio, err := client.Synthesize(context.Background(), "同学们好", "Cherry")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if !bytes.Equal(audio, testAudio) {
		t.Errorf("audio = %q, want the downloaded payload", audio)
	}
}

func TestSynthesizeRejectsUnsupportedVoice(t *testing.T) {
	client := newTestClient(t, func(string) http.HandlerFunc {
		return func(response http.ResponseWriter, _ *http.Request) {
			// 音色不被支持时百炼返回 200 但 audio.url 为空
			_ = json.NewEncoder(response).Encode(synthesisResponse{})
		}
	})

	_, err := client.Synthesize(context.Background(), "同学们好", "Stella")
	if err == nil || !strings.Contains(err.Error(), "Stella") || !strings.Contains(err.Error(), "qwen3-tts-flash") {
		t.Fatalf("error = %v, want a message naming both the voice and the model", err)
	}
}

func TestSynthesizeReportsServiceErrorInsideSuccessfulResponse(t *testing.T) {
	client := newTestClient(t, func(string) http.HandlerFunc {
		return func(response http.ResponseWriter, _ *http.Request) {
			// 百炼的错误也走 HTTP 200，所以不能只看状态码
			_ = json.NewEncoder(response).Encode(synthesisResponse{
				Code:      "InvalidParameter",
				Message:   "voice is not supported",
				RequestID: "req-1",
			})
		}
	})

	_, err := client.Synthesize(context.Background(), "同学们好", "Cherry")
	if err == nil || !strings.Contains(err.Error(), "InvalidParameter") ||
		!strings.Contains(err.Error(), "voice is not supported") {
		t.Fatalf("error = %v, want the service code and message", err)
	}
}

func TestSynthesizeReportsHTTPErrorBody(t *testing.T) {
	client := newTestClient(t, func(string) http.HandlerFunc {
		return func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(http.StatusUnauthorized)
			_, _ = response.Write([]byte(`{"code":"InvalidApiKey"}`))
		}
	})

	_, err := client.Synthesize(context.Background(), "同学们好", "Cherry")
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "InvalidApiKey") {
		t.Fatalf("error = %v, want the status and the body", err)
	}
}

func TestSynthesizeFailsWhenAudioDownloadFails(t *testing.T) {
	client := newTestClientWithAudio(t,
		func(response http.ResponseWriter, _ *http.Request) {
			http.Error(response, "gone", http.StatusNotFound)
		},
		func(audioURL string) http.HandlerFunc {
			return func(response http.ResponseWriter, _ *http.Request) {
				writeSynthesisResponse(response, audioURL)
			}
		})

	_, err := client.Synthesize(context.Background(), "同学们好", "Cherry")
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("error = %v, want the download status", err)
	}
}

func TestSynthesizeRejectsEmptyAudioBody(t *testing.T) {
	client := newTestClientWithAudio(t,
		func(http.ResponseWriter, *http.Request) {},
		func(audioURL string) http.HandlerFunc {
			return func(response http.ResponseWriter, _ *http.Request) {
				writeSynthesisResponse(response, audioURL)
			}
		})

	_, err := client.Synthesize(context.Background(), "同学们好", "Cherry")
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("error = %v, want an empty-body error", err)
	}
}

func TestSynthesizeRejectsOversizedTextBeforeRequest(t *testing.T) {
	client := unreachableClient(t)

	_, err := client.Synthesize(context.Background(), strings.Repeat("字", maxTextLength+1), "Cherry")
	if err == nil || !strings.Contains(err.Error(), "601") {
		t.Fatalf("error = %v, want a length error naming the count", err)
	}
}

func TestSynthesizeCountsCharactersNotBytes(t *testing.T) {
	reached := false
	client := newTestClient(t, func(string) http.HandlerFunc {
		return func(response http.ResponseWriter, _ *http.Request) {
			reached = true
			_ = json.NewEncoder(response).Encode(synthesisResponse{})
		}
	})

	// 600 个汉字是 1800 字节。按字节算的话这里会被误判成超长。
	_, err := client.Synthesize(context.Background(), strings.Repeat("字", maxTextLength), "Cherry")
	if !reached {
		t.Fatal("600 characters was rejected; the limit is counting bytes instead of characters")
	}
	if err == nil || strings.Contains(err.Error(), "character limit") {
		t.Fatalf("error = %v, want it to get past the length check", err)
	}
}

func TestSynthesizeRejectsEmptyInputBeforeRequest(t *testing.T) {
	client := unreachableClient(t)

	if _, err := client.Synthesize(context.Background(), "   ", "Cherry"); err == nil {
		t.Error("blank text was accepted")
	}
	if _, err := client.Synthesize(context.Background(), "同学们好", "  "); err == nil {
		t.Error("blank voice was accepted")
	}
}

func TestNewClientRejectsInvalidConfiguration(t *testing.T) {
	valid := config.TTSConfig{
		Enabled:  true,
		Provider: config.TTSProviderQwen,
		APIKey:   "tts-secret",
		BaseURL:  "https://dashscope.aliyuncs.com/api/v1",
		Model:    "qwen3-tts-flash",
		Timeout:  5 * time.Second,
	}

	cases := map[string]func(cfg *config.TTSConfig){
		"disabled":         func(cfg *config.TTSConfig) { cfg.Enabled = false },
		"missing api key":  func(cfg *config.TTSConfig) { cfg.APIKey = "" },
		"unknown provider": func(cfg *config.TTSConfig) { cfg.Provider = "openai" },
		"missing model":    func(cfg *config.TTSConfig) { cfg.Model = "" },
		"zero timeout":     func(cfg *config.TTSConfig) { cfg.Timeout = 0 },
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := valid
			mutate(&cfg)

			if _, err := NewClient(cfg); err == nil {
				t.Fatal("NewClient() accepted an invalid configuration")
			}
		})
	}
}

func writeSynthesisResponse(response http.ResponseWriter, audioURL string) {
	_ = json.NewEncoder(response).Encode(map[string]any{
		"output": map[string]any{
			"audio": map[string]any{"url": audioURL, "expires_at": 1766113409},
		},
	})
}

// newTestClient 造一个"音频地址永远好使"的客户端。
func newTestClient(t *testing.T, synthesize func(audioURL string) http.HandlerFunc) *Client {
	t.Helper()

	return newTestClientWithAudio(t, func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write(testAudio)
	}, synthesize)
}

// newTestClientWithAudio 起一对 stub：一个扮百炼，一个扮它返回的那个音频地址。分成两个
// 是为了让"下载"那一步也被真实覆盖——这正是 Synthesize 返回字节而不是 URL 换来的可测性。
func newTestClientWithAudio(t *testing.T, audioHandler http.HandlerFunc, synthesize func(audioURL string) http.HandlerFunc) *Client {
	t.Helper()

	audio := httptest.NewServer(audioHandler)
	t.Cleanup(audio.Close)

	synthesis := httptest.NewServer(synthesize(audio.URL))
	t.Cleanup(synthesis.Close)

	client, err := NewClient(config.TTSConfig{
		Enabled:  true,
		Provider: config.TTSProviderQwen,
		APIKey:   "tts-secret",
		BaseURL:  synthesis.URL,
		Model:    "qwen3-tts-flash",
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	return client
}

// unreachableClient 造一个发了请求就让测试失败的客户端，用来断言参数校验发生在发请求之前。
func unreachableClient(t *testing.T) *Client {
	t.Helper()

	return newTestClient(t, func(string) http.HandlerFunc {
		return func(http.ResponseWriter, *http.Request) {
			t.Error("client sent a request despite invalid input")
		}
	})
}
