package rag

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	einoembedding "github.com/cloudwego/eino/components/embedding"

	"narra/internal/model/entity"
	"narra/pkg/embedding"
)

// flakyEmbedder 前 failTimes 次调用返回指定的错误，之后正常返回向量。
//
// 用它验证重试：真实向量服务不可用时没法复现"第一次 429、第二次成功"这种序列，
// 而重试策略恰恰只在这种序列上才有意义。
type flakyEmbedder struct {
	calls     int
	failTimes int
	err       error
	dimension int
}

func (f *flakyEmbedder) EmbedStrings(ctx context.Context, inputs []string, _ ...einoembedding.Option) ([][]float64, error) {
	f.calls++
	if f.calls <= f.failTimes {
		return nil, f.err
	}

	vectors := make([][]float64, len(inputs))
	for index := range inputs {
		vectors[index] = make([]float64, f.dimension)
		vectors[index][0] = float64(index)
	}
	return vectors, nil
}

// setEmbedRetryDelay 把退避间隔压到测试能接受的范围，用例结束恢复。
// 不压的话，一个重试用例要白白跑好几秒。
func setEmbedRetryDelay(t *testing.T, delay time.Duration) {
	t.Helper()

	original := embedRetryBaseDelay
	embedRetryBaseDelay = delay
	t.Cleanup(func() { embedRetryBaseDelay = original })
}

func testChunks(count int) []Chunk {
	chunks := make([]Chunk, count)
	for index := range chunks {
		chunks[index] = Chunk{Index: index, Content: fmt.Sprintf("第 %d 片正文", index)}
	}
	return chunks
}

// 429 是典型的"再试一次就好"：第一次限流、第二次成功，整篇不该因此失败。
func TestEmbedInBatchesRetriesTransientFailure(t *testing.T) {
	setEmbedRetryDelay(t, time.Millisecond)

	embedder := &flakyEmbedder{
		failTimes: 1,
		err:       &embedding.HTTPError{StatusCode: 429, Body: "rate limited"},
		dimension: testVectorDims,
	}

	vectors, err := embedInBatches(context.Background(), embedder, testChunks(3))
	if err != nil {
		t.Fatalf("限流后应当重试成功，实际: %v", err)
	}
	if embedder.calls != 2 {
		t.Fatalf("应当调用 2 次（1 次失败 + 1 次重试），实际 %d 次", embedder.calls)
	}
	if len(vectors) != 3 {
		t.Fatalf("向量数 = %d，期望 3", len(vectors))
	}
	// 重试成功后，向量仍要按批次内的顺序归位。
	if vectors[2][0] != 2 {
		t.Errorf("第 3 条的向量对不上: %v", vectors[2])
	}
}

// 400 这类参数错误重试多少次都一样，不该浪费退避时间。
func TestEmbedInBatchesDoesNotRetryPermanentFailure(t *testing.T) {
	setEmbedRetryDelay(t, time.Millisecond)

	embedder := &flakyEmbedder{
		failTimes: 99,
		err:       &embedding.HTTPError{StatusCode: 400, Body: "bad request"},
		dimension: testVectorDims,
	}

	_, err := embedInBatches(context.Background(), embedder, testChunks(3))
	if err == nil {
		t.Fatal("参数错误必须失败")
	}
	if embedder.calls != 1 {
		t.Errorf("参数错误不该重试，实际调用 %d 次", embedder.calls)
	}
	var httpErr *embedding.HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != 400 {
		t.Errorf("错误里应当保留原始状态码，实际: %v", err)
	}
}

// 上游持续故障时只试固定次数就放弃：不能把一次收录拖成几分钟。
func TestEmbedInBatchesGivesUpAfterAttempts(t *testing.T) {
	setEmbedRetryDelay(t, time.Millisecond)

	embedder := &flakyEmbedder{
		failTimes: 99,
		err:       &embedding.HTTPError{StatusCode: 503, Body: "unavailable"},
		dimension: testVectorDims,
	}

	_, err := embedInBatches(context.Background(), embedder, testChunks(3))
	if err == nil {
		t.Fatal("持续 503 必须失败")
	}
	if embedder.calls != embedBatchAttempts {
		t.Errorf("应当试满 %d 次后放弃，实际 %d 次", embedBatchAttempts, embedder.calls)
	}
	var httpErr *embedding.HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != 503 {
		t.Errorf("放弃时应当带上最后一次的原始错误，实际: %v", err)
	}
}

// 关停时连退避等待都要能被打断：否则关服会被一次重试拖住。
func TestEmbedInBatchesStopsRetryingWhenCanceled(t *testing.T) {
	setEmbedRetryDelay(t, time.Hour) // 正常退避足够长，只有取消才能立刻结束

	embedder := &flakyEmbedder{
		failTimes: 99,
		err:       &embedding.HTTPError{StatusCode: 429, Body: "rate limited"},
		dimension: testVectorDims,
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := embedInBatches(ctx, embedder, testChunks(3))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消时应当返回 context.Canceled，实际: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("取消应当立刻打断重试等待，实际等了 %v", elapsed)
	}
	if embedder.calls != 1 {
		t.Errorf("取消后不该再发起请求，实际调用 %d 次", embedder.calls)
	}
}

// 上游返回的向量条数不对是契约被破坏，重试没有意义。
func TestEmbedInBatchesDoesNotRetryCountMismatch(t *testing.T) {
	setEmbedRetryDelay(t, time.Millisecond)

	embedder := &stubEmbedder{dimension: testVectorDims, shortBy: 1}
	_, err := embedInBatches(context.Background(), embedder, testChunks(3))
	if err == nil {
		t.Fatal("条数不符必须失败")
	}
	if !errors.Is(err, ErrEmbeddingMismatch) {
		t.Errorf("应当能被 errors.Is 判成 ErrEmbeddingMismatch，实际: %v", err)
	}
	if len(embedder.batches) != 1 {
		t.Errorf("条数不符不该重试，实际调用 %d 次", len(embedder.batches))
	}
}

// TestModelEmbedderConfigPrefersModelRow 校验"模型身份以模型行为准、连接信息以全局配置为准"。
//
// 这条判断是收录与检索共用的命门：模型行与配置一旦漂移而这里没对齐，查询向量会落到
// 另一个语义空间 —— 相似度照样算得出来、不报任何错，只是排序全是噪声。
func TestModelEmbedderConfigPrefersModelRow(t *testing.T) {
	global := testEmbeddingConfig()
	global.BaseURL = "https://global.example.test/v1"
	manager := embedding.NewManager(global)

	baseURL := "https://model-gateway.example.test/v1"
	cfg := modelEmbedderConfig(manager, &entity.EmbeddingModel{
		Name:       "bge-m3",
		Dimensions: 1024,
		BaseURL:    &baseURL,
	})

	if cfg.Model != "bge-m3" || cfg.Dimensions != 1024 {
		t.Fatalf("模型身份应当以模型行为准: %+v", cfg)
	}
	if cfg.BaseURL != baseURL {
		t.Fatalf("模型行带地址时应当以它为准: %q", cfg.BaseURL)
	}
	if cfg.APIKey != global.APIKey || cfg.Timeout != global.Timeout || !cfg.Enabled {
		t.Fatalf("密钥与超时应当来自全局配置: %+v", cfg)
	}
}

// TestModelEmbedderConfigFallsBackToGlobal 校验模型行没有地址时沿用全局地址 ——
// 空白是"没写"，不能当成"明确配成了空地址"，否则会去连一个不存在的相对地址。
func TestModelEmbedderConfigFallsBackToGlobal(t *testing.T) {
	global := testEmbeddingConfig()
	global.BaseURL = "https://global.example.test/v1"
	manager := embedding.NewManager(global)

	blank := "   "
	cases := map[string]*entity.EmbeddingModel{
		"模型行没有地址":  {Name: "bge-m3", Dimensions: 1024},
		"模型行地址是空白": {Name: "bge-m3", Dimensions: 1024, BaseURL: &blank},
		"没有模型行":    nil,
	}
	for name, model := range cases {
		if cfg := modelEmbedderConfig(manager, model); cfg.BaseURL != global.BaseURL {
			t.Fatalf("%s：应当沿用全局地址，实际 %q", name, cfg.BaseURL)
		}
	}
}

// TestNewModelEmbedderFactoryBuildsEinoEmbedder 校验生产工厂真的造得出 Eino 适配器，
// 且配置不合法时**在构造期**就报错 —— 收录与检索各自还有一道 Enabled 检查，
// 这里是最后一道：真等到调用才发现，前面已经白解析了一份文档。
func TestNewModelEmbedderFactoryBuildsEinoEmbedder(t *testing.T) {
	factory := newModelEmbedderFactory(embedding.NewManager(testEmbeddingConfig()))
	embedder, err := factory(&entity.EmbeddingModel{Name: testModelName, Dimensions: testVectorDims})
	if err != nil {
		t.Fatalf("合法配置应当能建出向量化能力: %v", err)
	}
	if _, ok := embedder.(*embedding.EinoEmbedder); !ok {
		t.Fatalf("生产工厂应当返回 Eino 适配器，实际 %T", embedder)
	}

	disabled := testEmbeddingConfig()
	disabled.Enabled = false
	model := &entity.EmbeddingModel{Name: testModelName, Dimensions: testVectorDims}
	if _, err := newModelEmbedderFactory(embedding.NewManager(disabled))(model); err == nil {
		t.Fatal("向量服务未启用时应当在构造期报错")
	}
}
