package rag

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

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

func (f *flakyEmbedder) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	f.calls++
	if f.calls <= f.failTimes {
		return nil, f.err
	}

	vectors := make([][]float32, len(inputs))
	for index := range inputs {
		vectors[index] = make([]float32, f.dimension)
		vectors[index][0] = float32(index)
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
