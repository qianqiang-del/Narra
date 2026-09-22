package embedding

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"narra/pkg/config"
)

// HTTPError 必须保留状态码：收录链路靠它判断"值不值得重试"。
// 如果这里退回成一句格式化字符串，429 和 400 在上层就分不出来了。
func TestEmbedReturnsTypedHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer server.Close()

	client, err := NewClient(config.EmbeddingConfig{
		Enabled:    true,
		BaseURL:    server.URL,
		Model:      "test-model",
		Dimensions: 4,
		Timeout:    5 * time.Second,
	})
	if err != nil {
		t.Fatalf("构造客户端失败: %v", err)
	}

	_, err = client.Embed(context.Background(), []string{"一段正文"})

	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("应当返回 *HTTPError，实际 %T: %v", err, err)
	}
	if httpErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("状态码 = %d，期望 429", httpErr.StatusCode)
	}
	if !httpErr.Retryable() {
		t.Error("429 应当被判为可重试")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("错误文案应当带上状态码，实际: %v", err)
	}
}

// 分类规则：429 与 5xx 可重试，其余 4xx 不可重试。
func TestHTTPErrorRetryableClassification(t *testing.T) {
	cases := map[int]bool{
		400: false,
		401: false,
		403: false,
		404: false,
		413: false,
		422: false,
		429: true,
		500: true,
		502: true,
		503: true,
		504: true,
	}
	for status, want := range cases {
		if got := (&HTTPError{StatusCode: status}).Retryable(); got != want {
			t.Errorf("状态码 %d 的 Retryable() = %v，期望 %v", status, got, want)
		}
	}
}
