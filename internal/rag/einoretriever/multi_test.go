package einoretriever

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"narra/internal/rag"
)

// fakeChatModel 是 BaseChatModel 的离线替身：返回固定内容，记录被调用次数。
type fakeChatModel struct {
	content string
	err     error
	calls   int
}

var _ model.BaseChatModel = (*fakeChatModel)(nil)

func (f *fakeChatModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return &schema.Message{Role: schema.Assistant, Content: f.content}, nil
}

func (f *fakeChatModel) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("测试替身不支持流式")
}

// sortedTexts 返回排序后的检索文本，便于与期望集合比较（多查询是并发跑的，
// 完成顺序不固定）。
func sortedTexts(searcher *fakeSearcher) []string {
	texts := searcher.recordedTexts()
	slices.Sort(texts)
	return texts
}

// TestMultiQueryFusesVariantsByRRF 校验多查询的核心行为：原查询 + 变体各自检索，
// 命中的切片按 RRF 融合、同一片段的两次命中得分相加、成分合并成 hybrid。
func TestMultiQueryFusesVariantsByRRF(t *testing.T) {
	searcher := &fakeSearcher{results: map[string]rag.RetrieveResult{
		"原查询": {
			Model: "bge-m3",
			Terms: []string{"原"},
			Hits:  []rag.Hit{mockHit(11, rag.MethodVector), mockHit(12, rag.MethodVector)},
		},
		"变体一": {
			Model: "bge-m3",
			Terms: []string{"变"},
			Hits:  []rag.Hit{mockHit(12, rag.MethodLexical), mockHit(13, rag.MethodLexical)},
		},
	}}
	facade, err := NewMultiQuery(searcher)
	if err != nil {
		t.Fatalf("构造多查询门面失败: %v", err)
	}

	result, err := facade.Retrieve(context.Background(), rag.RetrieveInput{
		Text:     "原查询",
		TopK:     3,
		Variants: []string{"变体一"},
	})
	if err != nil {
		t.Fatalf("多查询检索失败: %v", err)
	}

	// 12 被两路都命中（1/62 + 1/61），应当排第一；11 与 13 各只被一路命中，
	// 按名次 11（第 1 名，1/61）在前、13（第 2 名，1/62）在后。
	wantOrder := []uint64{12, 11, 13}
	got := make([]uint64, 0, len(result.Hits))
	for _, hit := range result.Hits {
		got = append(got, hit.ChunkID)
	}
	if !slices.Equal(got, wantOrder) {
		t.Fatalf("融合顺序不对: %v（期望 %v）", got, wantOrder)
	}

	first := result.Hits[0]
	if first.Method != rag.MethodHybrid {
		t.Fatalf("被向量与词法两路命中过应当合并成 hybrid: %+v", first)
	}
	if first.Similarity == nil {
		t.Fatalf("hybrid 应当带上向量路的相似度: %+v", first)
	}
	if result.Hits[2].Method != rag.MethodLexical || result.Hits[2].Similarity != nil {
		t.Fatalf("纯词法命中不该有相似度: %+v", result.Hits[2])
	}

	// model / terms 取原查询那一路，不因变体而混淆。
	if result.Model != "bge-m3" || !slices.Equal(result.Terms, []string{"原"}) {
		t.Fatalf("model / terms 应当来自原查询: model=%q terms=%v", result.Model, result.Terms)
	}
}

// TestMultiQuerySingleQueryPassesThrough 校验没有变体时直通底层检索：
// 不构造任何多查询编排，输入原样传下去。
func TestMultiQuerySingleQueryPassesThrough(t *testing.T) {
	searcher := &fakeSearcher{results: map[string]rag.RetrieveResult{
		"q": {Hits: []rag.Hit{mockHit(11, rag.MethodVector)}},
	}}
	facade, err := NewMultiQuery(searcher)
	if err != nil {
		t.Fatalf("构造多查询门面失败: %v", err)
	}

	result, err := facade.Retrieve(context.Background(), rag.RetrieveInput{Text: "q", TopK: 5})
	if err != nil {
		t.Fatalf("检索失败: %v", err)
	}
	if len(result.Hits) != 1 {
		t.Fatalf("结果不对: %+v", result)
	}
	if len(searcher.inputs) != 1 || searcher.inputs[0].Text != "q" || searcher.inputs[0].TopK != 5 {
		t.Fatalf("单查询应当原样直通: %+v", searcher.inputs)
	}
}

// TestMultiQueryNormalizesVariants 校验变体清洗：空串丢掉、去重（大小写不敏感）、
// 与主查询相同的丢掉、超出 3 条的截掉。
func TestMultiQueryNormalizesVariants(t *testing.T) {
	searcher := &fakeSearcher{results: map[string]rag.RetrieveResult{}}
	facade, err := NewMultiQuery(searcher)
	if err != nil {
		t.Fatalf("构造多查询门面失败: %v", err)
	}

	_, err = facade.Retrieve(context.Background(), rag.RetrieveInput{
		Text:     "q",
		TopK:     5,
		Variants: []string{"", "  ", "v1", "v1", "Q", "v2", "v3", "v4"},
	})
	if err != nil {
		t.Fatalf("检索失败: %v", err)
	}

	texts := searcher.recordedTexts()
	slices.Sort(texts)
	want := []string{"q", "v1", "v2", "v3"}
	if !slices.Equal(texts, want) {
		t.Fatalf("变体清洗不对: %v（期望 %v）", texts, want)
	}
}

// TestMultiQueryFallsBackToSingleQueryOnError 校验多查询失败退回单查询：
// 变体是增强，任一变体把整次多查询拖下水时，原查询仍要能出结果。
func TestMultiQueryFallsBackToSingleQueryOnError(t *testing.T) {
	searcher := &fakeSearcher{
		results: map[string]rag.RetrieveResult{
			"q": {Model: "bge-m3", Hits: []rag.Hit{mockHit(11, rag.MethodVector)}},
		},
		errs: map[string]error{"坏变体": errors.New("向量服务挂了")},
	}
	facade, err := NewMultiQuery(searcher)
	if err != nil {
		t.Fatalf("构造多查询门面失败: %v", err)
	}

	result, err := facade.Retrieve(context.Background(), rag.RetrieveInput{
		Text:     "q",
		TopK:     5,
		Variants: []string{"坏变体"},
	})
	if err != nil {
		t.Fatalf("多查询失败应当退回单查询而不是报错: %v", err)
	}
	if len(result.Hits) != 1 || result.Hits[0].ChunkID != 11 {
		t.Fatalf("退回单查询后结果不对: %+v", result)
	}

	// "q" 被检索两次：多查询里一次、退回后一次。
	count := 0
	for _, text := range searcher.recordedTexts() {
		if text == "q" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("应当退回并重查原查询一次，实际 %d 次", count)
	}
}

// TestMultiQueryRewritesWithInjectedModel 校验 ctx 注入改写模型后，调用方只给一条
// 检索词也会被扩写；模型输出的编号、项目符号会被清掉，重复与原词会被丢掉。
func TestMultiQueryRewritesWithInjectedModel(t *testing.T) {
	searcher := &fakeSearcher{results: map[string]rag.RetrieveResult{
		"令牌桶算法": {Hits: []rag.Hit{mockHit(11, rag.MethodVector)}},
	}}
	chatModel := &fakeChatModel{content: "1. 限流算法 令牌桶\n2、令牌桶原理\n- 令牌桶 实现\n令牌桶算法"}
	facade, err := NewMultiQuery(searcher)
	if err != nil {
		t.Fatalf("构造多查询门面失败: %v", err)
	}

	result, err := facade.Retrieve(WithRewriteModel(context.Background(), chatModel),
		rag.RetrieveInput{Text: "令牌桶算法", TopK: 5})
	if err != nil {
		t.Fatalf("检索失败: %v", err)
	}
	if len(result.Hits) != 1 {
		t.Fatalf("结果不对: %+v", result)
	}
	if chatModel.calls != 1 {
		t.Fatalf("改写模型应当被调用一次，实际 %d 次", chatModel.calls)
	}

	want := []string{"令牌桶 实现", "令牌桶原理", "令牌桶算法", "限流算法 令牌桶"}
	slices.Sort(want)
	if got := sortedTexts(searcher); !slices.Equal(got, want) {
		t.Fatalf("改写没有生效或清洗不对: %v（期望 %v）", got, want)
	}
}

// TestMultiQueryPrefersCallerVariantsOverRewrite 校验调用方给了变体就不再调模型：
// 写查询的那个 Agent 手里有更多上下文，它的说法比二次改写更准，也省一次调用。
func TestMultiQueryPrefersCallerVariantsOverRewrite(t *testing.T) {
	searcher := &fakeSearcher{results: map[string]rag.RetrieveResult{}}
	chatModel := &fakeChatModel{content: "不应被使用"}
	facade, err := NewMultiQuery(searcher)
	if err != nil {
		t.Fatalf("构造多查询门面失败: %v", err)
	}

	ctx := WithRewriteModel(context.Background(), chatModel)
	if _, err := facade.Retrieve(ctx, rag.RetrieveInput{
		Text:     "q",
		TopK:     5,
		Variants: []string{"v1"},
	}); err != nil {
		t.Fatalf("检索失败: %v", err)
	}
	if chatModel.calls != 0 {
		t.Fatalf("有调用方变体时不该再调改写模型，实际调了 %d 次", chatModel.calls)
	}

	want := []string{"q", "v1"}
	slices.Sort(want)
	if got := sortedTexts(searcher); !slices.Equal(got, want) {
		t.Fatalf("检索词不对: %v（期望 %v）", got, want)
	}
}

// TestMultiQueryFallsBackWhenRewriteFails 校验改写模型失败只降级、不报错：
// 按原检索词照常检索。
func TestMultiQueryFallsBackWhenRewriteFails(t *testing.T) {
	searcher := &fakeSearcher{results: map[string]rag.RetrieveResult{
		"q": {Hits: []rag.Hit{mockHit(11, rag.MethodVector)}},
	}}
	chatModel := &fakeChatModel{err: errors.New("模型超时")}
	facade, err := NewMultiQuery(searcher)
	if err != nil {
		t.Fatalf("构造多查询门面失败: %v", err)
	}

	result, err := facade.Retrieve(WithRewriteModel(context.Background(), chatModel),
		rag.RetrieveInput{Text: "q", TopK: 5})
	if err != nil {
		t.Fatalf("改写失败应当降级而不是报错: %v", err)
	}
	if len(result.Hits) != 1 || result.Hits[0].ChunkID != 11 {
		t.Fatalf("降级后结果不对: %+v", result)
	}
	if got := searcher.recordedTexts(); len(got) != 1 || got[0] != "q" {
		t.Fatalf("改写失败后应当只按原检索词查一次: %v", got)
	}
}
