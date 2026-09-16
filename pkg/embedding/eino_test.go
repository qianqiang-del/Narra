package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"narra/pkg/config"

	"github.com/cloudwego/eino/components/embedding"
)

const testDimensions = 3

// newTestClient 起一个假的 OpenAI 兼容 /embeddings 服务，返回指向它的 Client。
// 路径写死在 handler 里断言：Client 拼接端点的方式变了这里要能察觉。
func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Errorf("请求路径 = %q, 期望 %q", r.URL.Path, "/embeddings")
			w.WriteHeader(http.StatusNotFound)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(config.EmbeddingConfig{
		Enabled:    true,
		APIKey:     "test-key",
		BaseURL:    server.URL,
		Model:      "text-embedding-3-small",
		Timeout:    5 * time.Second,
		Dimensions: testDimensions,
	})
	if err != nil {
		t.Fatalf("创建测试用 Client 失败: %v", err)
	}
	return client
}

type embeddingsPayload struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

type vectorPayload struct {
	Embedding []float32 `json:"embedding"`
	Index     int       `json:"index"`
}

type embeddingsBody struct {
	Data []vectorPayload `json:"data"`
}

// writeVectors 按入参顺序返回一批向量，索引与位置一致。
func writeVectors(t *testing.T, w http.ResponseWriter, vectors [][]float32) {
	t.Helper()

	body := embeddingsBody{Data: make([]vectorPayload, 0, len(vectors))}
	for index, vector := range vectors {
		body.Data = append(body.Data, vectorPayload{Embedding: vector, Index: index})
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Errorf("写入测试响应失败: %v", err)
	}
}

// newTestEmbedder 组装被测对象。
func newTestEmbedder(t *testing.T, handler http.HandlerFunc) (*EinoEmbedder, *Client) {
	t.Helper()

	client := newTestClient(t, handler)
	embedder, err := NewEinoEmbedder(client)
	if err != nil {
		t.Fatalf("创建 Eino 适配器失败: %v", err)
	}
	return embedder, client
}

func TestNewEinoEmbedderRejectsNilClient(t *testing.T) {
	if _, err := NewEinoEmbedder(nil); err == nil {
		t.Fatal("nil Client 应当被拒绝")
	}
}

// 值与维度原样透传，且返回类型是接口要求的 float64。
func TestEinoEmbedderReturnsFloat64Vectors(t *testing.T) {
	want := [][]float32{{0.5, -1.25, 2}, {0, 0.25, -4}}
	embedder, _ := newTestEmbedder(t, func(w http.ResponseWriter, r *http.Request) {
		writeVectors(t, w, want)
	})

	got, err := embedder.EmbedStrings(context.Background(), []string{"第一段", "第二段"})
	if err != nil {
		t.Fatalf("EmbedStrings 失败: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("向量条数 = %d, 期望 %d", len(got), len(want))
	}
	for index := range want {
		if len(got[index]) != len(want[index]) {
			t.Fatalf("第 %d 条维度 = %d, 期望 %d", index, len(got[index]), len(want[index]))
		}
		for position := range want[index] {
			if want := float64(want[index][position]); got[index][position] != want {
				t.Errorf("got[%d][%d] = %v, 期望 %v", index, position, got[index][position], want)
			}
		}
	}
}

// 上游乱序返回时仍按 index 对齐——这是换成 eino-ext 官方实现会丢掉的能力。
func TestEinoEmbedderKeepsVectorsAlignedByIndex(t *testing.T) {
	embedder, _ := newTestEmbedder(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// 故意倒序返回：index 是唯一可信的顺序依据，位置不是。
		if err := json.NewEncoder(w).Encode(embeddingsBody{Data: []vectorPayload{
			{Embedding: []float32{1, 1, 1}, Index: 1},
			{Embedding: []float32{2, 2, 2}, Index: 0},
		}}); err != nil {
			t.Errorf("写入测试响应失败: %v", err)
		}
	})

	got, err := embedder.EmbedStrings(context.Background(), []string{"零", "一"})
	if err != nil {
		t.Fatalf("EmbedStrings 失败: %v", err)
	}
	if got[0][0] != 2 {
		t.Errorf("第 0 条向量首元素 = %v, 期望 2（应取自 index=0 的那条）", got[0][0])
	}
	if got[1][0] != 1 {
		t.Errorf("第 1 条向量首元素 = %v, 期望 1（应取自 index=1 的那条）", got[1][0])
	}
}

// option 里的模型名要生效，但不能改到共享的 Client 上。
func TestEinoEmbedderHonorsModelOptionWithoutMutatingClient(t *testing.T) {
	var payload embeddingsPayload
	embedder, client := newTestEmbedder(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("解析请求体失败: %v", err)
		}
		writeVectors(t, w, [][]float32{{0, 0, 0}})
	})

	if _, err := embedder.EmbedStrings(
		context.Background(),
		[]string{"文本"},
		embedding.WithModel("text-embedding-3-large"),
	); err != nil {
		t.Fatalf("EmbedStrings 失败: %v", err)
	}
	if payload.Model != "text-embedding-3-large" {
		t.Errorf("请求里的 model = %q, 期望 %q", payload.Model, "text-embedding-3-large")
	}
	if client.model != "text-embedding-3-small" {
		t.Errorf("Client 的 model 变成了 %q，适配器不应改动共享的 Client", client.model)
	}
}

// 不带 option 时仍用 Client 自带的模型名。
func TestEinoEmbedderFallsBackToClientModel(t *testing.T) {
	var payload embeddingsPayload
	embedder, _ := newTestEmbedder(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("解析请求体失败: %v", err)
		}
		writeVectors(t, w, [][]float32{{0, 0, 0}})
	})

	if _, err := embedder.EmbedStrings(context.Background(), []string{"文本"}); err != nil {
		t.Fatalf("EmbedStrings 失败: %v", err)
	}
	if payload.Model != "text-embedding-3-small" {
		t.Errorf("请求里的 model = %q, 期望 %q", payload.Model, "text-embedding-3-small")
	}
}

// 上游少返向量必须报错，否则向量会与文本静默错位。
func TestEinoEmbedderRejectsMissingVectors(t *testing.T) {
	embedder, _ := newTestEmbedder(t, func(w http.ResponseWriter, r *http.Request) {
		writeVectors(t, w, [][]float32{{0, 0, 0}})
	})

	if _, err := embedder.EmbedStrings(context.Background(), []string{"甲", "乙"}); err == nil {
		t.Fatal("上游少返向量时未报错，向量会与文本错位")
	}
}

// 维度与配置不符必须报错。
func TestEinoEmbedderRejectsDimensionMismatch(t *testing.T) {
	embedder, _ := newTestEmbedder(t, func(w http.ResponseWriter, r *http.Request) {
		writeVectors(t, w, [][]float32{{0, 0}})
	})

	if _, err := embedder.EmbedStrings(context.Background(), []string{"文本"}); err == nil {
		t.Fatal("维度与配置不符时未报错")
	}
}

// 空批次直接失败，不发请求。
func TestEinoEmbedderRejectsEmptyBatch(t *testing.T) {
	embedder, _ := newTestEmbedder(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("空批次不应发起请求")
		writeVectors(t, w, nil)
	})

	if _, err := embedder.EmbedStrings(context.Background(), nil); err == nil {
		t.Fatal("空批次应当报错")
	}
}

// 上游报错要原样透传，不能被适配层吞掉。
func TestEinoEmbedderPropagatesUpstreamError(t *testing.T) {
	embedder, _ := newTestEmbedder(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		if _, err := w.Write([]byte(`{"error":{"message":"bad key"}}`)); err != nil {
			t.Errorf("写入测试响应失败: %v", err)
		}
	})

	if _, err := embedder.EmbedStrings(context.Background(), []string{"文本"}); err == nil {
		t.Fatal("上游 401 时未报错")
	}
}
