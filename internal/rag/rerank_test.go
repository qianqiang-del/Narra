package rag

import (
	"context"
	"errors"
	"testing"
)

// fakeSearcher 是内层替身：记录收到的输入，返回预设结果。
type fakeSearcher struct {
	calls  int
	got    RetrieveInput
	result RetrieveResult
	err    error
}

func (f *fakeSearcher) Retrieve(_ context.Context, input RetrieveInput) (RetrieveResult, error) {
	f.calls++
	f.got = input
	return f.result, f.err
}

// fakeReranker 是精排替身：记录参数，返回预设分数。
type fakeReranker struct {
	calls    int
	gotQuery string
	gotDocs  []string
	scores   []float64
	err      error
}

func (f *fakeReranker) Rerank(_ context.Context, query string, documents []string) ([]float64, error) {
	f.calls++
	f.gotQuery = query
	f.gotDocs = documents
	return f.scores, f.err
}

// rerankTestHits 造三条融合后的命中（顺序就是内层给的精排前顺序）。
func rerankTestHits() []Hit {
	return []Hit{
		{ChunkID: 1, DocumentID: 11, Content: "A", DocumentTitle: "文档一", Method: MethodVector, Similarity: floatPtr(0.91), Score: 0.032},
		{ChunkID: 2, DocumentID: 12, Content: "B", DocumentTitle: "文档二", Method: MethodHybrid, Similarity: floatPtr(0.88), Score: 0.031},
		{ChunkID: 3, DocumentID: 13, Content: "C", DocumentTitle: "文档三", Method: MethodLexical, Score: 0.016},
	}
}

func floatPtr(value float64) *float64 { return &value }

// providerOf 把固定替身包成"永远生效"的来源，模拟精排开着的情形。
func providerOf(reranker Reranker) RerankerProvider {
	return func() Reranker { return reranker }
}

// 精排按分重排、分数替换成精排分、召回元数据原样保留、最终截到 top_k；
// 内层收到的 TopK 是候选深度（30），不是调用方要的 2。
func TestRerankedReordersByScoreAndTruncates(t *testing.T) {
	inner := &fakeSearcher{result: RetrieveResult{
		Model: "bge-m3", Terms: []string{"检索", "优化"}, Hits: rerankTestHits(),
	}}
	reranker := &fakeReranker{scores: []float64{0.10, 0.90, 0.50}}
	decorated, err := NewReranked(inner, providerOf(reranker))
	if err != nil {
		t.Fatalf("构造装饰器失败: %v", err)
	}

	result, err := decorated.Retrieve(context.Background(), RetrieveInput{Text: "检索优化", TopK: 2})
	if err != nil {
		t.Fatalf("检索失败: %v", err)
	}

	if inner.got.TopK != rerankCandidateDepth {
		t.Errorf("内层 TopK = %d，期望候选深度 %d", inner.got.TopK, rerankCandidateDepth)
	}
	if reranker.gotQuery != "检索优化" {
		t.Errorf("精排检索词 = %q，期望原检索词", reranker.gotQuery)
	}
	if len(reranker.gotDocs) != 3 || reranker.gotDocs[0] != "A" || reranker.gotDocs[2] != "C" {
		t.Errorf("精排候选 = %v，期望按内层顺序的 A/B/C", reranker.gotDocs)
	}

	if len(result.Hits) != 2 {
		t.Fatalf("返回条数 = %d，期望 2", len(result.Hits))
	}
	if result.Hits[0].ChunkID != 2 || result.Hits[0].Score != 0.90 {
		t.Errorf("第 1 条 = id %d score %v，期望 id 2 / 0.90", result.Hits[0].ChunkID, result.Hits[0].Score)
	}
	if result.Hits[1].ChunkID != 3 || result.Hits[1].Score != 0.50 {
		t.Errorf("第 2 条 = id %d score %v，期望 id 3 / 0.50", result.Hits[1].ChunkID, result.Hits[1].Score)
	}
	if result.Hits[0].Method != MethodHybrid || result.Hits[0].Similarity == nil || *result.Hits[0].Similarity != 0.88 {
		t.Errorf("召回元数据应原样保留: method=%q similarity=%v", result.Hits[0].Method, result.Hits[0].Similarity)
	}
	if result.Model != "bge-m3" || len(result.Terms) != 2 {
		t.Errorf("排障字段应原样保留: model=%q terms=%v", result.Model, result.Terms)
	}
}

// 调用方要的比候选深度多时（top_k=50），内层至少要取到 50；没给 top_k 时仍按深度 30。
func TestRerankedDepthFollowsLargerTopK(t *testing.T) {
	inner := &fakeSearcher{result: RetrieveResult{Hits: rerankTestHits()}}
	reranker := &fakeReranker{scores: []float64{0.5, 0.5, 0.5}}
	decorated, err := NewReranked(inner, providerOf(reranker))
	if err != nil {
		t.Fatalf("构造装饰器失败: %v", err)
	}

	if _, err := decorated.Retrieve(context.Background(), RetrieveInput{Text: "q", TopK: 50}); err != nil {
		t.Fatalf("检索失败: %v", err)
	}
	if inner.got.TopK != 50 {
		t.Errorf("内层 TopK = %d，期望 50（比候选深度大时以调用方为准）", inner.got.TopK)
	}

	if _, err := decorated.Retrieve(context.Background(), RetrieveInput{Text: "q"}); err != nil {
		t.Fatalf("检索失败: %v", err)
	}
	if inner.got.TopK != rerankCandidateDepth {
		t.Errorf("内层 TopK = %d，期望候选深度 %d", inner.got.TopK, rerankCandidateDepth)
	}
}

// 0/1 条候选不调用精排：没有可排的东西，不值得一次网络往返。
func TestRerankedSkipsSmallLists(t *testing.T) {
	inner := &fakeSearcher{result: RetrieveResult{Hits: []Hit{{ChunkID: 1, Content: "A", Score: 0.03}}}}
	reranker := &fakeReranker{scores: []float64{0.9}}
	decorated, err := NewReranked(inner, providerOf(reranker))
	if err != nil {
		t.Fatalf("构造装饰器失败: %v", err)
	}

	result, err := decorated.Retrieve(context.Background(), RetrieveInput{Text: "q", TopK: 5})
	if err != nil {
		t.Fatalf("检索失败: %v", err)
	}
	if reranker.calls != 0 {
		t.Errorf("单条候选不应调用精排，实际 %d 次", reranker.calls)
	}
	if len(result.Hits) != 1 || result.Hits[0].Score != 0.03 {
		t.Errorf("结果应原样返回: %+v", result.Hits)
	}
}

// 精排报错：不把错误往上抛，保持融合顺序并照常截断，分数仍是 RRF 分。
func TestRerankedFallsBackOnError(t *testing.T) {
	inner := &fakeSearcher{result: RetrieveResult{Hits: rerankTestHits()}}
	reranker := &fakeReranker{err: errors.New("connection refused")}
	decorated, err := NewReranked(inner, providerOf(reranker))
	if err != nil {
		t.Fatalf("构造装饰器失败: %v", err)
	}

	result, err := decorated.Retrieve(context.Background(), RetrieveInput{Text: "q", TopK: 2})
	if err != nil {
		t.Fatalf("精排失败不应让检索失败: %v", err)
	}
	if len(result.Hits) != 2 {
		t.Fatalf("返回条数 = %d，期望截到 2", len(result.Hits))
	}
	if result.Hits[0].ChunkID != 1 || result.Hits[0].Score != 0.032 {
		t.Errorf("应保持融合顺序与融合分: %+v", result.Hits[0])
	}
}

// 精排返回的分数与候选条数对不上：同样降级，绝不猜对应关系。
func TestRerankedFallsBackOnScoreMismatch(t *testing.T) {
	inner := &fakeSearcher{result: RetrieveResult{Hits: rerankTestHits()}}
	reranker := &fakeReranker{scores: []float64{0.9, 0.1}}
	decorated, err := NewReranked(inner, providerOf(reranker))
	if err != nil {
		t.Fatalf("构造装饰器失败: %v", err)
	}

	result, err := decorated.Retrieve(context.Background(), RetrieveInput{Text: "q", TopK: 3})
	if err != nil {
		t.Fatalf("检索失败: %v", err)
	}
	if result.Hits[0].ChunkID != 1 || result.Hits[0].Score != 0.032 {
		t.Errorf("条数不符应保持融合顺序: %+v", result.Hits[0])
	}
}

// 内层失败是检索真的坏了，原样上抛，且不调用精排。
func TestRerankedPropagatesInnerError(t *testing.T) {
	sentinel := errors.New("数据库不可用")
	inner := &fakeSearcher{err: sentinel}
	reranker := &fakeReranker{scores: []float64{0.9}}
	decorated, err := NewReranked(inner, providerOf(reranker))
	if err != nil {
		t.Fatalf("构造装饰器失败: %v", err)
	}

	_, err = decorated.Retrieve(context.Background(), RetrieveInput{Text: "q"})
	if !errors.Is(err, sentinel) {
		t.Errorf("应上抛内层错误，实际 %v", err)
	}
	if reranker.calls != 0 {
		t.Errorf("内层失败时不应调用精排，实际 %d 次", reranker.calls)
	}
}

// 分数打平时保持内层（融合）顺序：同一查询两次结果必须一致。
func TestRerankedStableOnTies(t *testing.T) {
	inner := &fakeSearcher{result: RetrieveResult{Hits: rerankTestHits()}}
	reranker := &fakeReranker{scores: []float64{0.5, 0.5, 0.5}}
	decorated, err := NewReranked(inner, providerOf(reranker))
	if err != nil {
		t.Fatalf("构造装饰器失败: %v", err)
	}

	result, err := decorated.Retrieve(context.Background(), RetrieveInput{Text: "q", TopK: 3})
	if err != nil {
		t.Fatalf("检索失败: %v", err)
	}
	for index, want := range []uint64{1, 2, 3} {
		if result.Hits[index].ChunkID != want {
			t.Errorf("打平时顺序应保持：位置 %d = %d，期望 %d", index, result.Hits[index].ChunkID, want)
		}
	}
}

// 精排关闭（来源返回 nil）：完全原样透传——内层收到调用方原始 top_k，不加深候选、
// 不调精排、分数保持融合分。保证"没配精排"与"没包装饰器"行为一致。
func TestRerankedPassesThroughWhenDisabled(t *testing.T) {
	inner := &fakeSearcher{result: RetrieveResult{Hits: rerankTestHits()}}
	reranker := &fakeReranker{}
	decorated, err := NewReranked(inner, func() Reranker { return nil })
	if err != nil {
		t.Fatalf("构造装饰器失败: %v", err)
	}

	result, err := decorated.Retrieve(context.Background(), RetrieveInput{Text: "q", TopK: 2})
	if err != nil {
		t.Fatalf("检索失败: %v", err)
	}
	if inner.got.TopK != 2 {
		t.Errorf("透传时内层 TopK = %d，期望调用方原始值 2", inner.got.TopK)
	}
	if reranker.calls != 0 {
		t.Errorf("关闭时不应调用精排，实际 %d 次", reranker.calls)
	}
	// 透传不截断、不改分：内层返回什么就是什么（真实内层自己会按 TopK 截）。
	if len(result.Hits) != 3 || result.Hits[0].ChunkID != 1 || result.Hits[2].ChunkID != 3 {
		t.Errorf("结果应按内层原样返回: %+v", result.Hits)
	}
	if result.Hits[0].Score != 0.032 || result.Hits[1].Score != 0.031 {
		t.Errorf("透传不应改分: %+v", result.Hits)
	}
}

// 依赖为空时构造失败：装配错误要在启动时暴露，而不是等第一次检索。
func TestNewRerankedValidatesDependencies(t *testing.T) {
	if _, err := NewReranked(nil, providerOf(&fakeReranker{})); err == nil {
		t.Error("内层为空应当报错")
	}
	if _, err := NewReranked(&fakeSearcher{}, nil); err == nil {
		t.Error("精排来源为空应当报错")
	}
}
