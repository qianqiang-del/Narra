package vlm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 探测必须真的发一张图片：模型不支持视觉、图片格式被拒这类配置错误，
// 只测纯文本是发现不了的。
func TestProbeSendsImageAndReturnsReply(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" {
			t.Errorf("请求 %s %s，期望 POST /chat/completions", r.Method, r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")

		var payload struct {
			Model    string `json:"model"`
			Messages []struct {
				Content []struct {
					Type     string `json:"type"`
					Text     string `json:"text"`
					ImageURL struct {
						URL string `json:"url"`
					} `json:"image_url"`
				} `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("解析请求体失败: %v", err)
		}
		if payload.Model != "Qwen/Qwen3.5-35B-A3B" {
			t.Errorf("model = %q", payload.Model)
		}
		if len(payload.Messages) != 1 || len(payload.Messages[0].Content) != 2 {
			t.Fatalf("消息结构不对: %+v", payload.Messages)
		}
		image := payload.Messages[0].Content[1].ImageURL.URL
		if !strings.HasPrefix(image, "data:image/png;base64,") {
			t.Errorf("图片应为 data:image/png;base64，实际 %q", image[:min(40, len(image))])
		}
		// 探测图必须能过视觉模型的最小边长检查：Qwen3 VL 系要求长宽都大于 28
		//（硅基流动实测返回 400 code 20015），1x1 的图会让"测试连接"永远失败。
		decodedImage, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(image, "data:image/png;base64,"))
		if err != nil {
			t.Fatalf("解码探测图片失败: %v", err)
		}
		probeImage, err := png.Decode(bytes.NewReader(decodedImage))
		if err != nil {
			t.Fatalf("解析探测图片失败: %v", err)
		}
		if bounds := probeImage.Bounds(); bounds.Dx() <= 28 || bounds.Dy() <= 28 {
			t.Errorf("探测图 %dx%d 会被视觉模型拒绝（要求长宽大于 28）", bounds.Dx(), bounds.Dy())
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"OK"}}]}`))
	}))
	defer server.Close()

	client, err := NewClient(Config{
		BaseURL: server.URL + "/", APIKey: "sk-test",
		Model: "Qwen/Qwen3.5-35B-A3B", Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}
	result, err := client.Probe(context.Background())
	if err != nil {
		t.Fatalf("探测失败: %v", err)
	}
	if result.Reply != "OK" {
		t.Errorf("回复 = %q，期望 OK", result.Reply)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("Authorization = %q", gotAuth)
	}
}

// 非 2xx 要保留状态码与响应体真因（额度、鉴权、模型名错误都在里面）。
func TestProbeHTTPErrorKeepsBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"invalid api key"}`))
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL, Model: "m"})
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}
	_, err = client.Probe(context.Background())
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("期望 *HTTPError，实际 %v", err)
	}
	if httpErr.StatusCode != http.StatusUnauthorized || !strings.Contains(httpErr.Body, "invalid api key") {
		t.Errorf("错误信息不完整: %+v", httpErr)
	}
}

// 空回复按失败处理：端点通了但模型没在应答，同样是坏配置。
func TestProbeRejectsEmptyContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":""}}]}`))
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL, Model: "m"})
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}
	if _, err := client.Probe(context.Background()); err == nil {
		t.Fatal("空回复应当报错")
	}
}

func TestNewClientValidates(t *testing.T) {
	if _, err := NewClient(Config{BaseURL: "", Model: "m"}); err == nil {
		t.Error("空地址应当报错")
	}
	if _, err := NewClient(Config{BaseURL: "https://a.com/v1", Model: " "}); err == nil {
		t.Error("空模型应当报错")
	}
}
