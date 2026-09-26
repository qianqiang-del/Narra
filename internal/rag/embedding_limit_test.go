package rag

import (
	"context"
	"sync"
	"testing"
	"time"

	einoembedding "github.com/cloudwego/eino/components/embedding"

	"narra/internal/model/entity"
)

// blockingEmbedder 是一个会停在闸门前的向量桩：每次进入 EmbedStrings 都发信号并记录
// 并发峰值。用它验证"向量化并发是独立的一路限流"——不是靠 worker 的解析并发顺带约束的。
type blockingEmbedder struct {
	entered chan struct{}
	release chan struct{}

	mu        sync.Mutex
	current   int
	peak      int
	entrances int
}

var _ Embedder = (*blockingEmbedder)(nil)

func newBlockingEmbedder() *blockingEmbedder {
	return &blockingEmbedder{
		entered: make(chan struct{}, 2),
		release: make(chan struct{}),
	}
}

func (e *blockingEmbedder) EmbedStrings(ctx context.Context, inputs []string, _ ...einoembedding.Option) ([][]float64, error) {
	e.mu.Lock()
	e.current++
	e.entrances++
	if e.current > e.peak {
		e.peak = e.current
	}
	e.mu.Unlock()

	e.entered <- struct{}{}
	select {
	case <-e.release:
	case <-ctx.Done():
		e.mu.Lock()
		e.current--
		e.mu.Unlock()
		return nil, ctx.Err()
	}

	e.mu.Lock()
	e.current--
	e.mu.Unlock()

	vectors := make([][]float64, len(inputs))
	for index := range vectors {
		vectors[index] = make([]float64, testVectorDims)
	}
	return vectors, nil
}

func (e *blockingEmbedder) snapshot() (entrances, peak int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.entrances, e.peak
}

// TestEmbeddingLimitSerializesDocuments 向量化名额为 1 时，两份文档的 embed 阶段
// 必须一前一后：第二个任务要等第一个放掉名额才能进入上游。
//
// 这正是"两路限流"里除解析并发之外的那一路：解析并发决定同时有几个 OCR/解析在跑，
// 向量化名额决定同时有几个文档在打 embedding 服务；两者不能互相代偿。
func TestEmbeddingLimitSerializesDocuments(t *testing.T) {
	store := &fakeDocumentStore{chunks: []entity.KnowledgeChunk{
		{BaseModel: entity.BaseModel{ID: 1}, ChunkIndex: 0, Content: "第一段"},
		{BaseModel: entity.BaseModel{ID: 2}, ChunkIndex: 1, Content: "第二段"},
	}}
	embedder := newBlockingEmbedder()
	ingester := newIngesterWithOptions(store, newFakeModels(), nil, testEmbeddingConfig(),
		IngestOptions{Tx: testTx{}, EmbeddingConcurrency: 1})
	ingester.newEmbedder = func(*entity.EmbeddingModel) (Embedder, error) { return embedder, nil }

	stage := entity.KnowledgeDocumentStageEmbed
	document := &entity.KnowledgeDocument{
		BaseModel:   entity.BaseModel{ID: testDocumentID},
		Title:       "并发向量化",
		Status:      entity.KnowledgeDocumentStatusProcessing,
		Content:     "已落库的正文",
		IngestStage: &stage,
	}

	done := make(chan error, 2)
	go func() {
		_, err := ingester.processExistingFile(context.Background(), document, FileInput{}, 1, stage)
		done <- err
	}()

	select {
	case <-embedder.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("第一个文档没有进入向量化")
	}

	go func() {
		_, err := ingester.processExistingFile(context.Background(), document, FileInput{}, 2, stage)
		done <- err
	}()

	// 给第二个任务足够时间走到名额检查：它必须停在信号量前，而不是与第一个并发打上游。
	time.Sleep(50 * time.Millisecond)
	if entrances, _ := embedder.snapshot(); entrances != 1 {
		t.Fatalf("第一个文档持有名额时不该有第二次向量化调用，实际进入 %d 次", entrances)
	}

	close(embedder.release)
	for index := 0; index < 2; index++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("第 %d 个文档的向量化应当成功: %v", index+1, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("向量化任务没有收尾（信号量可能没有释放）")
		}
	}

	if _, peak := embedder.snapshot(); peak != 1 {
		t.Errorf("向量化并发峰值 = %d，期望 1（名额为 1 时必须串行）", peak)
	}
}
