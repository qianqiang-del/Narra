package rerank

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestClient 构造一个指向替身服务的客户端。
func newTestClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	client, err := NewClient(Config{BaseURL: baseURL, Model: "test-reranker", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("构造客户端失败: %v", err)
	}
	return client
}

// 归位必须按 index，不能按返回顺序：上游把结果按分数重排后，返回顺序与输入顺序不同，
// 按顺序取分会让每条候选拿到别人的分——比不精排更糟。
func TestRerankAlignsScoresByIndex(t *testing.T) {
	var gotRequest rerankRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Errorf("解析请求失败: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[
			{"index":2,"relevance_score":0.31},
			{"index":0,"relevance_score":0.92},
			{"index":1,"relevance_score":0.55}
		]}`))
	}))
	defer server.Close()

	scores, err := newTestClient(t, server.URL).Rerank(context.Background(), "检索怎么优化", []string{"A", "B", "C"})
	if err != nil {
		t.Fatalf("Rerank 失败: %v", err)
	}
	want := []float64{0.92, 0.55, 0.31}
	for index, score := range want {
		if scores[index] != score {
			t.Errorf("第 %d 条分数 = %v，期望 %v", index, scores[index], score)
		}
	}
	if gotRequest.Model != "test-reranker" || gotRequest.Query != "检索怎么优化" {
		t.Errorf("请求形状不对: %+v", gotRequest)
	}
	if gotRequest.TopN != 3 || len(gotRequest.Documents) != 3 {
		t.Errorf("top_n 应为候选条数 3，实际 top_n=%d documents=%d", gotRequest.TopN, len(gotRequest.Documents))
	}
}

// 结果条数不符、索引重复或越界都必须报错：静默错位会把不相关的切片排到前面。
func TestRerankRejectsMalformedResults(t *testing.T) {
	cases := map[string]string{
		"条数不符":       `{"results":[{"index":0,"relevance_score":0.5}]}`,
		"索引越界":       `{"results":[{"index":0,"relevance_score":0.5},{"index":5,"relevance_score":0.4}]}`,
		"索引重复":       `{"results":[{"index":0,"relevance_score":0.5},{"index":0,"relevance_score":0.4}]}`,
		"results 为空": `{"results":[]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()

			_, err := newTestClient(t, server.URL).Rerank(context.Background(), "查询", []string{"A", "B"})
			if err == nil {
				t.Fatal("不合法的响应应当报错")
			}
		})
	}
}

// HTTPError 必须保留状态码：降级策略与将来的重试都靠它判断。
func TestRerankReturnsTypedHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"message":"rate limited"}`))
	}))
	defer server.Close()

	_, err := newTestClient(t, server.URL).Rerank(context.Background(), "查询", []string{"A"})

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
	if !strings.Contains(err.Error(), "429") || !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("错误文案应当带上状态码与上游原因，实际: %v", err)
	}
}

// 分类规则：429 与 5xx 可重试，其余 4xx 不可重试。
func TestHTTPErrorRetryableClassification(t *testing.T) {
	cases := map[int]bool{
		400: false,
		401: false,
		403: false,
		404: false,
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

// 空检索词、空候选、空文档一律在本地拦下：上游收到的坏请求会变成一条没有信息的 4xx。
func TestRerankRejectsEmptyInput(t *testing.T) {
	client, err := NewClient(Config{BaseURL: "https://example.com/v1", Model: "m"})
	if err != nil {
		t.Fatalf("构造客户端失败: %v", err)
	}
	cases := []struct {
		name      string
		query     string
		documents []string
	}{
		{name: "空检索词", query: "   ", documents: []string{"A"}},
		{name: "空候选", query: "查询", documents: nil},
		{name: "空白文档", query: "查询", documents: []string{"A", " "}},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if _, err := client.Rerank(context.Background(), item.query, item.documents); err == nil {
				t.Fatal("空输入应当报错")
			}
		})
	}
}

// 地址与模型必填；地址末尾的斜杠要被归一，避免拼出 //rerank。
func TestNewClientValidatesConfig(t *testing.T) {
	if _, err := NewClient(Config{Model: "m"}); err == nil {
		t.Error("空地址应当报错")
	}
	if _, err := NewClient(Config{BaseURL: "https://example.com/v1"}); err == nil {
		t.Error("空模型应当报错")
	}
	client, err := NewClient(Config{BaseURL: "https://example.com/v1/", Model: " m ", Timeout: 0})
	if err != nil {
		t.Fatalf("合法配置不应报错: %v", err)
	}
	if client.rerankURL != "https://example.com/v1/rerank" {
		t.Errorf("rerankURL = %q，期望去掉末尾斜杠并拼上 /rerank", client.rerankURL)
	}
	if client.model != "m" {
		t.Errorf("model = %q，期望去掉首尾空白", client.model)
	}
}

// 配了 Key 就必须带 Authorization；自建服务没 Key 时不能画蛇添足。
func TestRerankSendsAuthHeader(t *testing.T) {
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"results":[{"index":0,"relevance_score":0.5}]}`))
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL, APIKey: "sk-test", Model: "m"})
	if err != nil {
		t.Fatalf("构造客户端失败: %v", err)
	}
	if _, err := client.Rerank(context.Background(), "查询", []string{"A"}); err != nil {
		t.Fatalf("Rerank 失败: %v", err)
	}
	if authorization != "Bearer sk-test" {
		t.Errorf("Authorization = %q，期望 Bearer sk-test", authorization)
	}

	anonymous := newTestClient(t, server.URL)
	if _, err := anonymous.Rerank(context.Background(), "查询", []string{"A"}); err != nil {
		t.Fatalf("无 Key 调用失败: %v", err)
	}
	if authorization != "" {
		t.Errorf("未配置 Key 时不应带 Authorization，实际 %q", authorization)
	}
}

// 探测固定发两条文档（相关 + 无关），返回值与位置对应。
func TestProbeReturnsBothScores(t *testing.T) {
	var gotRequest rerankRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Errorf("解析请求失败: %v", err)
		}
		_, _ = w.Write([]byte(`{"results":[
			{"index":0,"relevance_score":0.97},
			{"index":1,"relevance_score":0.02}
		]}`))
	}))
	defer server.Close()

	result, err := newTestClient(t, server.URL).Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe 失败: %v", err)
	}
	if result.Relevant != 0.97 || result.Irrelevant != 0.02 {
		t.Errorf("探测结果 = %+v，期望 0.97 / 0.02", result)
	}
	if len(gotRequest.Documents) != 2 {
		t.Errorf("探测应发两条文档，实际 %d", len(gotRequest.Documents))
	}
}
