package rag

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"narra/internal/model/entity"
	"narra/pkg/config"
	"narra/pkg/embedding"
)

// 与 ingest_test.go 同一个立场：不连数据库、不调向量服务。检索链路真正需要真环境的
// 只有两条 SQL（那是仓储层 knowledge_search_repository_test.go 的事），
// 词项拆分、RRF 融合、降级规则全是纯逻辑，用替身就能测干净。

// fakeChunkSearcher 是 ChunkSearcher 的内存替身。
type fakeChunkSearcher struct {
	vectorRows  []entity.KnowledgeChunkView
	vectorErr   error
	lexicalRows []entity.KnowledgeChunkView
	lexicalErr  error

	// 下面两列记录最近一次收到的参数，供断言两路的召回口径。
	vectorQuery  entity.KnowledgeVectorQuery
	lexicalQuery entity.KnowledgeLexicalQuery
}

var _ ChunkSearcher = (*fakeChunkSearcher)(nil)

func (s *fakeChunkSearcher) SearchVector(ctx context.Context, query entity.KnowledgeVectorQuery) ([]entity.KnowledgeChunkView, error) {
	s.vectorQuery = query
	return s.vectorRows, s.vectorErr
}

func (s *fakeChunkSearcher) SearchLexical(ctx context.Context, query entity.KnowledgeLexicalQuery) ([]entity.KnowledgeChunkView, error) {
	s.lexicalQuery = query
	return s.lexicalRows, s.lexicalErr
}

// chunkView 造一行召回结果，得分由调用方给（向量路是相似度，词法路是加权命中分）。
func chunkView(chunkID uint64, score float64) entity.KnowledgeChunkView {
	heading := "章节"
	source := fmt.Sprintf("来源-%d.md", chunkID)
	return entity.KnowledgeChunkView{
		ChunkID:       chunkID,
		DocumentID:    chunkID / 10,
		ChunkIndex:    int32(chunkID % 10),
		Heading:       &heading,
		Content:       fmt.Sprintf("切片正文 %d", chunkID),
		DocumentTitle: fmt.Sprintf("文档 %d", chunkID/10),
		SourceType:    entity.KnowledgeDocumentSourceImport,
		SourceURI:     &source,
		RawScore:      score,
	}
}

// newRetrieverWith 是测试用的完整构造：向量化换成桩（走真实的 newEmbedder 会去连
// example.test），其余照 NewRetriever 装配。
func newRetrieverWith(
	search ChunkSearcher,
	models ModelRegistry,
	embedder Embedder,
	cfg config.EmbeddingConfig,
) *Retriever {
	retriever := NewRetriever(search, models, embedding.NewManager(cfg))
	retriever.newEmbedder = func(model *entity.EmbeddingModel) (Embedder, error) {
		if embedder == nil {
			return nil, fmt.Errorf("用例没有提供向量桩")
		}
		return embedder, nil
	}
	return retriever
}

// newTestRetriever 是正常路径的检索器：向量服务已启用，库里有一个默认模型。
func newTestRetriever(search *fakeChunkSearcher, embedder Embedder) *Retriever {
	return newRetrieverWith(search, newFakeModels(), embedder, testEmbeddingConfig())
}

// lexicalTexts 取一次词法计划里的词项文本（切分细节由 internal/rag/tokenize 的用例钉住，
// 这里只验"接进检索链路之后"的口径）。
func lexicalTexts(query string) []string {
	return lexicalTermTexts(buildLexicalPlan(query).Terms)
}

// termTexts 把带权词项拼成逗号串，给断言用。
func termTexts(terms []entity.KnowledgeLexicalTerm) string {
	parts := make([]string, 0, len(terms))
	for _, term := range terms {
		parts = append(parts, term.Text)
	}
	return strings.Join(parts, ",")
}

// TestLexicalTermsSplitsRuns 校验英文标识与编号被整段保留、下划线被当作分隔符。
func TestLexicalTermsSplitsRuns(t *testing.T) {
	terms := lexicalTexts("P99 800ms knowledge_embeddings 索引")

	want := []string{"P99", "800ms", "knowledge", "embeddings", "索引"}
	if strings.Join(terms, ",") != strings.Join(want, ",") {
		t.Fatalf("词项拆得不对: %v（期望 %v）", terms, want)
	}
}

// TestLexicalTermsSegmentsChineseWithDictionary 校验中文走词典分词：
// "向量检索"切成两个词，而不是改造前 向量/量检/检索 三个二元组；
// 词典不认识的连续汉字才退回二元组（OOV 兜底，见 tokenize 包）。
func TestLexicalTermsSegmentsChineseWithDictionary(t *testing.T) {
	if terms := lexicalTexts("向量检索"); strings.Join(terms, ",") != "向量,检索" {
		t.Fatalf("四字词应切成两个词典词: %v", terms)
	}

	if terms := lexicalTexts("索引"); strings.Join(terms, ",") != "索引" {
		t.Fatalf("二字词应整段保留: %v", terms)
	}

	if terms := lexicalTexts("a 的。"); len(terms) != 0 {
		t.Fatalf("单字词项全是噪声，应当丢干净: %v", terms)
	}
}

// TestLexicalTermsDeduplicatesIgnoringCase 校验去重按大小写不敏感进行 ——
// 同一个词项重复出现只值一次权重，留两份只会白白占掉名额。
func TestLexicalTermsDeduplicatesIgnoringCase(t *testing.T) {
	terms := lexicalTexts("Go go GOLANG")
	if strings.Join(terms, ",") != "Go,GOLANG" {
		t.Fatalf("去重不对: %v", terms)
	}
}

// TestLexicalTermsPrefersExactTermsWhenTruncated 校验名额不够时先保精确词：
// 编号与专名挑得动结果，词典词与二元组之间却彼此可以替代。
func TestLexicalTermsPrefersExactTermsWhenTruncated(t *testing.T) {
	terms := lexicalTexts("W38 P99 hnsw 向量检索调研的排序质量到底怎么保证")

	if len(terms) != maxLexicalTerms {
		t.Fatalf("词项应截断到 %d 个，实际 %d 个: %v", maxLexicalTerms, len(terms), terms)
	}
	for _, want := range []string{"W38", "P99", "hnsw"} {
		if !containsTerm(terms, want) {
			t.Fatalf("精确词 %q 应当优先保住，实际是 %v", want, terms)
		}
	}
}

// TestBuildLexicalPlanProducesPhrases 校验短语随词项一起进计划：
// 整段"向量检索"在词典分词之外额外留一份，作为"原样出现"的打分信号。
func TestBuildLexicalPlanProducesPhrases(t *testing.T) {
	plan := buildLexicalPlan("向量检索")

	if got := termTexts(plan.Terms); got != "向量,检索" {
		t.Fatalf("词项不对: %v", got)
	}
	if len(plan.Phrases) != 1 || plan.Phrases[0].Text != "向量检索" {
		t.Fatalf("短语不对: %+v", plan.Phrases)
	}
	if plan.Phrases[0].Weight <= plan.Terms[0].Weight {
		t.Fatalf("短语权重应当高于词项权重: %+v", plan)
	}
}

// TestFuseByRRFMergesBothRoutes 校验同一个切片被两路召回时合并成一条、得分相加、
// 记为 hybrid —— "两边都点头"正是混合排序最该排到前面的命中。
func TestFuseByRRFMergesBothRoutes(t *testing.T) {
	shared := chunkView(11, 0.90)
	vectorOnly := chunkView(21, 0.85)

	hits := fuseByRRF(rrfConstant, []entity.KnowledgeChunkView{shared, vectorOnly}, []entity.KnowledgeChunkView{shared})

	if len(hits) != 2 {
		t.Fatalf("同一个切片应当合并成一条，实际 %d 条", len(hits))
	}
	first := hits[0]
	if first.ChunkID != shared.ChunkID || first.Method != MethodHybrid {
		t.Fatalf("两路都召回的那条应当排第一且是 hybrid: %+v", first)
	}
	if first.Similarity == nil || *first.Similarity != shared.RawScore {
		t.Fatalf("相似度应当来自向量路: %+v", first)
	}

	// 融合分 = 1/(60+1) + 1/(60+1)（两路都是第 1 名），只被一路召回的那条只有 1/62。
	if want := 2 * reciprocalRank(rrfConstant, 0); first.Score != want {
		t.Fatalf("融合分不对: %v，期望 %v", first.Score, want)
	}
	if hits[1].Method != MethodVector {
		t.Fatalf("只被向量路召回的应当记为 vector: %+v", hits[1])
	}
}

// TestFuseByRRFKeepsLexicalHitWithoutSimilarity 校验纯词法命中没有相似度（nil 而不是 0）：
// 0 是一个真实存在的相似度，用它当"没有"就把两种情况混成了一种。
func TestFuseByRRFKeepsLexicalHitWithoutSimilarity(t *testing.T) {
	hits := fuseByRRF(rrfConstant, nil, []entity.KnowledgeChunkView{chunkView(11, 3)})

	if len(hits) != 1 || hits[0].Method != MethodLexical {
		t.Fatalf("纯词法命中不对: %+v", hits)
	}
	if hits[0].Similarity != nil {
		t.Fatalf("纯词法命中不该有相似度: %+v", hits[0])
	}
}

// TestFuseByRRFIsDeterministicOnTies 校验融合分打平时顺序稳定：
// 融合分的粒度本来就粗（只有几种名次组合），打平是常态，而融合草稿存在 map 里、
// 遍历顺序每次都不同 —— 没有确定的次序规则，同一句检索词两次搜出来都不一样。
func TestFuseByRRFIsDeterministicOnTies(t *testing.T) {
	// 向量路第 i 名与词法路第 i 名的融合分相同（都是 1/(k+i)），正好凑出三对打平。
	vector := []entity.KnowledgeChunkView{chunkView(11, 0.9), chunkView(21, 0.8), chunkView(31, 0.7)}
	lexical := []entity.KnowledgeChunkView{chunkView(12, 3), chunkView(22, 2), chunkView(32, 1)}

	var want []uint64
	var got []uint64
	for run := 0; run < 20; run++ {
		hits := fuseByRRF(rrfConstant, vector, lexical)
		got = make([]uint64, len(hits))
		for index, hit := range hits {
			got[index] = hit.ChunkID
		}
		if run == 0 {
			want = got
			continue
		}
		if !slices.Equal(got, want) {
			t.Fatalf("打平时顺序应当稳定: %v vs %v", got, want)
		}
	}

	// 次序规则是"先按文档、再按切片"：同一对打平的命中来自同一篇文档时，
	// 按 chunk_index 还原原文顺序，引用读起来才连贯。
	if want := []uint64{11, 12, 21, 22, 31, 32}; !slices.Equal(got, want) {
		t.Fatalf("打平时应当按文档与切片顺序排: %v", got)
	}
}

// TestRetrieveFusesBothRoutes 走一遍正常路径：两路各给一条候选，
// 断言结果按融合分排好、条数不超过 topK、两路的召回口径符合约定。
func TestRetrieveFusesBothRoutes(t *testing.T) {
	search := &fakeChunkSearcher{
		vectorRows:  []entity.KnowledgeChunkView{chunkView(11, 0.9), chunkView(21, 0.8)},
		lexicalRows: []entity.KnowledgeChunkView{chunkView(11, 3), chunkView(31, 1)},
	}
	retriever := newTestRetriever(search, &stubEmbedder{dimension: testVectorDims})

	result, err := retriever.Retrieve(context.Background(), RetrieveInput{Text: "向量检索", TopK: 3})
	if err != nil {
		t.Fatalf("检索失败: %v", err)
	}

	if result.Model != testModelName {
		t.Fatalf("应当记下向量路用的模型名，实际是 %q", result.Model)
	}
	if len(result.Hits) != 3 {
		t.Fatalf("应当返回 3 条命中，实际 %d 条", len(result.Hits))
	}
	if result.Hits[0].ChunkID != 11 || result.Hits[0].Method != MethodHybrid {
		t.Fatalf("两路都召回的应当排第一: %+v", result.Hits[0])
	}

	// 向量路按 topK × 过采样倍数取候选；词法路收到的词项就是拆出来的那几个。
	if want := recallLimit(3); search.vectorQuery.Limit != want || search.lexicalQuery.Limit != want {
		t.Fatalf("两路的候选上限应当一致且为过采样值: %d / %d（期望 %d）",
			search.vectorQuery.Limit, search.lexicalQuery.Limit, want)
	}
	if got := termTexts(search.lexicalQuery.Terms); got != "向量,检索" {
		t.Fatalf("词法路收到的词项不对: %v", got)
	}
	if len(search.lexicalQuery.Phrases) != 1 || search.lexicalQuery.Phrases[0].Text != "向量检索" {
		t.Fatalf("词法路收到的短语不对: %+v", search.lexicalQuery.Phrases)
	}
}

// TestRetrieveClampsTopK 校验条数被钳到上限，同时过采样也跟着按上限走。
func TestRetrieveClampsTopK(t *testing.T) {
	search := &fakeChunkSearcher{}
	for index := 1; index <= maxTopK+10; index++ {
		search.vectorRows = append(search.vectorRows, chunkView(uint64(index), float64(index)))
	}
	retriever := newTestRetriever(search, &stubEmbedder{dimension: testVectorDims})

	result, err := retriever.Retrieve(context.Background(), RetrieveInput{Text: "向量检索", TopK: 1000})
	if err != nil {
		t.Fatalf("检索失败: %v", err)
	}
	if len(result.Hits) != maxTopK {
		t.Fatalf("命中应当钳到 %d 条，实际 %d 条", maxTopK, len(result.Hits))
	}
	if search.vectorQuery.Limit != maxTopK*recallOverfetch {
		t.Fatalf("过采样上限不对: %d", search.vectorQuery.Limit)
	}
}

// TestRetrieveEmbedsQueryWithDefaultModel 校验查询串用**库里的默认模型**去向量化，
// 并按模型维度比对 —— 用配置里的模型会让查询向量落到另一个语义空间，
// 相似度照样算得出来、不报任何错，只是排序全是噪声。
func TestRetrieveEmbedsQueryWithDefaultModel(t *testing.T) {
	search := &fakeChunkSearcher{}
	var askedModel *entity.EmbeddingModel
	retriever := newRetrieverWith(search, newFakeModels(), &stubEmbedder{dimension: testVectorDims}, testEmbeddingConfig())
	retriever.newEmbedder = func(model *entity.EmbeddingModel) (Embedder, error) {
		askedModel = model
		return &stubEmbedder{dimension: int(model.Dimensions)}, nil
	}

	if _, err := retriever.Retrieve(context.Background(), RetrieveInput{Text: "向量检索"}); err != nil {
		t.Fatalf("检索失败: %v", err)
	}

	if askedModel == nil || askedModel.ID != testModelID {
		t.Fatalf("向量化应当用默认模型，实际是 %+v", askedModel)
	}
	if search.vectorQuery.ModelID != testModelID || search.vectorQuery.Dimensions != testVectorDims {
		t.Fatalf("向量召回的模型口径不对: %+v", search.vectorQuery)
	}
	// stubEmbedder 把首维写成文本长度（4 个汉字），其余补 0，正好当一个可核对的指纹。
	if search.vectorQuery.Vector != "[4,0,0,0]" {
		t.Fatalf("查询向量不对: %q", search.vectorQuery.Vector)
	}
}

// TestRetrievePassesFilterToBothRoutes 校验过滤条件原样进入两条召回路 ——
// 只加在一路时，融合的两份名单搜的范围不一致，"两路都召回"就不再是同一个前提，
// 结果会以很难解释的方式偏移，所以这个功能最容易犯的错要拿用例钉住。
func TestRetrievePassesFilterToBothRoutes(t *testing.T) {
	search := &fakeChunkSearcher{}
	retriever := newTestRetriever(search, &stubEmbedder{dimension: testVectorDims})

	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	filter := entity.KnowledgeChunkFilter{
		SourceTypes: []string{entity.KnowledgeDocumentSourceImport},
		DocumentIDs: []uint64{11, 22},
		CreatedFrom: &from,
	}
	if _, err := retriever.Retrieve(context.Background(), RetrieveInput{Text: "向量检索", Filter: filter}); err != nil {
		t.Fatalf("检索失败: %v", err)
	}

	if !reflect.DeepEqual(search.vectorQuery.Filter, filter) {
		t.Fatalf("向量路没有收到过滤条件: %+v", search.vectorQuery.Filter)
	}
	if !reflect.DeepEqual(search.lexicalQuery.Filter, filter) {
		t.Fatalf("词法路没有收到过滤条件: %+v", search.lexicalQuery.Filter)
	}
}

// TestRetrieveCleansQueryBeforeRecall 校验清洗层真的接进了两路：
// 向量路用剥壳后的文本向量化，词法路用剥壳后的词项召回，响应里的 terms 回显的也是它。
func TestRetrieveCleansQueryBeforeRecall(t *testing.T) {
	search := &fakeChunkSearcher{}
	embedder := &stubEmbedder{dimension: testVectorDims}
	retriever := newTestRetriever(search, embedder)

	result, err := retriever.Retrieve(context.Background(),
		RetrieveInput{Text: "帮我找下之前上传的那份讲义里讲的令牌桶算法"})
	if err != nil {
		t.Fatalf("检索失败: %v", err)
	}

	if len(embedder.batches) != 1 || len(embedder.batches[0]) != 1 || embedder.batches[0][0] != "讲义 令牌桶算法" {
		t.Fatalf("向量路应当拿到剥壳后的文本，实际批次: %v", embedder.batches)
	}
	const wantTerms = "讲义,令牌,算法"
	if got := termTexts(search.lexicalQuery.Terms); got != wantTerms {
		t.Fatalf("词法路应当拿到剥壳后的词项，实际: %v", got)
	}
	if got := strings.Join(result.Terms, ","); got != wantTerms {
		t.Fatalf("响应里的 terms 应当回显清洗后的词项，实际: %v", result.Terms)
	}
}

// TestRetrieveDegradesToLexicalWhenEmbeddingFails 校验降级规则的前半句：
// 一路挂掉不影响另一路，结果照常返回，来源写在 Method 里。
func TestRetrieveDegradesToLexicalWhenEmbeddingFails(t *testing.T) {
	search := &fakeChunkSearcher{lexicalRows: []entity.KnowledgeChunkView{chunkView(11, 2)}}
	retriever := newTestRetriever(search, &stubEmbedder{err: fmt.Errorf("上游 502")})

	result, err := retriever.Retrieve(context.Background(), RetrieveInput{Text: "向量检索", TopK: 5})
	if err != nil {
		t.Fatalf("向量路挂掉不该让整次检索失败: %v", err)
	}
	if len(result.Hits) != 1 || result.Hits[0].Method != MethodLexical {
		t.Fatalf("应当只剩词法路的命中: %+v", result.Hits)
	}
	if result.Model != testModelName {
		t.Fatalf("向量路没跑起来也该记下它本来要用的模型，便于排障: %q", result.Model)
	}
}

// TestRetrieveDegradesToVectorWhenLexicalFails 校验降级规则的另一半方向也对称成立。
func TestRetrieveDegradesToVectorWhenLexicalFails(t *testing.T) {
	search := &fakeChunkSearcher{
		vectorRows: []entity.KnowledgeChunkView{chunkView(11, 0.9)},
		lexicalErr: fmt.Errorf("数据库查不动"),
	}
	retriever := newTestRetriever(search, &stubEmbedder{dimension: testVectorDims})

	result, err := retriever.Retrieve(context.Background(), RetrieveInput{Text: "向量检索", TopK: 5})
	if err != nil {
		t.Fatalf("词法路挂掉不该让整次检索失败: %v", err)
	}
	if len(result.Hits) != 1 || result.Hits[0].Method != MethodVector {
		t.Fatalf("应当只剩向量路的命中: %+v", result.Hits)
	}
}

// TestRetrieveReportsFailureWhenBothRoutesFail 校验降级规则的后半句：
// 两条路都没给出结果时，失败原因必须冒泡 —— 空结果加一行日志比报错难查得多。
func TestRetrieveReportsFailureWhenBothRoutesFail(t *testing.T) {
	search := &fakeChunkSearcher{lexicalErr: fmt.Errorf("数据库查不动")}
	retriever := newTestRetriever(search, &stubEmbedder{err: fmt.Errorf("上游 502")})

	_, err := retriever.Retrieve(context.Background(), RetrieveInput{Text: "向量检索"})
	if err == nil {
		t.Fatal("两路都失败时必须报错")
	}
	if !strings.Contains(err.Error(), "上游 502") || !strings.Contains(err.Error(), "数据库查不动") {
		t.Fatalf("两条路的失败原因都该在错误里: %v", err)
	}
}

// TestRetrieveChecksVectorCensusOnce 校验"静默零召回"体检的接线与去重：
// 向量召回零命中时体检一次；结论记账后，后续空结果不再重复跑统计查询。
func TestRetrieveChecksVectorCensusOnce(t *testing.T) {
	search := &fakeChunkSearcher{}
	models := newFakeModels()
	models.census = []entity.ModelVectorCount{
		{ModelID: testModelID, Name: testModelName, Vectors: 0},
		{ModelID: 6, Name: "旧模型", Vectors: 148},
	}
	retriever := newRetrieverWith(search, models, &stubEmbedder{dimension: testVectorDims}, testEmbeddingConfig())

	for run := 0; run < 3; run++ {
		if _, err := retriever.Retrieve(context.Background(), RetrieveInput{Text: "向量检索"}); err != nil {
			t.Fatalf("检索失败: %v", err)
		}
	}
	if models.censusCalls != 1 {
		t.Fatalf("体检应当只做一次（没有向量的状态不会自己恢复），实际 %d 次", models.censusCalls)
	}
}

// TestRetrieveReturnsEmptyOnNoMatch 校验"查到了但一条都没命中"不算失败 ——
// 那就是一次正常的空结果。与上一个用例合起来才是完整的降级规则。
func TestRetrieveReturnsEmptyOnNoMatch(t *testing.T) {
	retriever := newTestRetriever(&fakeChunkSearcher{}, &stubEmbedder{dimension: testVectorDims})

	result, err := retriever.Retrieve(context.Background(), RetrieveInput{Text: "向量检索"})
	if err != nil {
		t.Fatalf("空结果不该报错: %v", err)
	}
	if len(result.Hits) != 0 {
		t.Fatalf("应当没有命中: %+v", result.Hits)
	}
}

// TestRetrieveUsesLexicalWithoutModel 校验没有登记模型时词法路照常可用 ——
// 没有模型只是没有向量路，不代表知识库不能按词查。
func TestRetrieveUsesLexicalWithoutModel(t *testing.T) {
	search := &fakeChunkSearcher{lexicalRows: []entity.KnowledgeChunkView{chunkView(11, 2)}}
	models := &fakeModelRegistry{findErr: gorm.ErrRecordNotFound}
	retriever := newRetrieverWith(search, models, &stubEmbedder{dimension: testVectorDims}, testEmbeddingConfig())

	result, err := retriever.Retrieve(context.Background(), RetrieveInput{Text: "向量检索", TopK: 5})
	if err != nil {
		t.Fatalf("没有模型时应当退化成纯词法检索: %v", err)
	}
	if len(result.Hits) != 1 || result.Hits[0].Method != MethodLexical {
		t.Fatalf("应当只剩词法路的命中: %+v", result.Hits)
	}
	if result.Model != "" {
		t.Fatalf("没有模型时不该编一个模型名出来: %q", result.Model)
	}
}

// TestRetrieveRejectsEmptyQuery 校验空检索词被拦下来，不发起任何召回。
func TestRetrieveRejectsEmptyQuery(t *testing.T) {
	search := &fakeChunkSearcher{}
	retriever := newTestRetriever(search, &stubEmbedder{dimension: testVectorDims})

	if _, err := retriever.Retrieve(context.Background(), RetrieveInput{Text: "   "}); !errors.Is(err, ErrEmptyQuery) {
		t.Fatalf("空检索词应当返回 ErrEmptyQuery，实际是 %v", err)
	}
	if search.vectorQuery.Limit != 0 || len(search.lexicalQuery.Terms) != 0 {
		t.Fatal("空检索词不该发起召回")
	}
}

// containsTerm 判断词项表里有没有某个词（大小写不敏感）。
func containsTerm(terms []string, want string) bool {
	for _, term := range terms {
		if strings.EqualFold(term, want) {
			return true
		}
	}
	return false
}
