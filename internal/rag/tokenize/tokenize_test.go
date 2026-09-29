package tokenize

import (
	"strings"
	"sync"
	"testing"
)

// 词典加载是秒级动作，整个测试包共用一份分词器；需要自定义词的用例再单独构造。
var (
	sharedOnce      sync.Once
	sharedTokenizer *Tokenizer
	sharedErr       error
)

func shared(t *testing.T) *Tokenizer {
	t.Helper()
	sharedOnce.Do(func() { sharedTokenizer, sharedErr = New(Options{}) })
	if sharedErr != nil {
		t.Fatalf("分词器初始化失败: %v", sharedErr)
	}
	return sharedTokenizer
}

// texts 把词项拼成逗号串，方便与期望值对比。
func texts(terms []Term) string {
	parts := make([]string, 0, len(terms))
	for _, term := range terms {
		parts = append(parts, term.Text)
	}
	return strings.Join(parts, ",")
}

// weights 取词项权重序列。
func weights(terms []Term) []float64 {
	out := make([]float64, 0, len(terms))
	for _, term := range terms {
		out = append(out, term.Weight)
	}
	return out
}

func equalWeights(got []float64, want ...float64) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

// TestAnalyseKeepsExactTerms 校验英文标识、编号与数字整段保留，
// 并按下划线切成两段 —— 精确词是词法路最该保住的形态。
func TestAnalyseKeepsExactTerms(t *testing.T) {
	result := shared(t).Analyse("P99 800ms knowledge_embeddings 索引")

	if got := texts(result.Terms); got != "P99,800ms,knowledge,embeddings,索引" {
		t.Fatalf("词项拆得不对: %v", result.Terms)
	}
	if !equalWeights(weights(result.Terms), WeightExact, WeightExact, WeightExact, WeightExact, WeightDict) {
		t.Fatalf("精确词与词典词的权重不对: %v", weights(result.Terms))
	}
	if len(result.Phrases) != 0 {
		t.Fatalf("这些片段都与词项重复，不该有短语: %v", result.Phrases)
	}
}

// TestAnalyseSegmentsChineseWithDictionary 校验中文走词典分词：
// "向量检索" 切成两个词，而不是改造前的 向量/量检/检索 三个二元组；
// 整段同时留作短语，把"原样出现"这个更强的信号补回来。
func TestAnalyseSegmentsChineseWithDictionary(t *testing.T) {
	result := shared(t).Analyse("向量检索")

	if got := texts(result.Terms); got != "向量,检索" {
		t.Fatalf("词典分词结果不对: %v", result.Terms)
	}
	if !equalWeights(weights(result.Terms), WeightDict, WeightDict) {
		t.Fatalf("词典词权重不对: %v", weights(result.Terms))
	}
	if got := texts(result.Phrases); got != "向量检索" {
		t.Fatalf("整段应当留作短语: %v", result.Phrases)
	}
	if !equalWeights(weights(result.Phrases), WeightPhrase) {
		t.Fatalf("短语权重不对: %v", weights(result.Phrases))
	}

	// "桶"是单字，按"单字丢弃"的规则不进词项；短语仍然完整。
	if got := texts(shared(t).Analyse("令牌桶算法").Terms); got != "令牌,算法" {
		t.Fatalf("含单字词的切分不对: %v", got)
	}
}

// TestAnalyseFallsBackToBigramsOutsideDictionary 校验词典不认识的连续汉字
// 退成相邻二元组：OOV 不丢召回，代价是可能切出噪声对（权重给得最低）。
func TestAnalyseFallsBackToBigramsOutsideDictionary(t *testing.T) {
	result := shared(t).Analyse("龘靐齉爩")

	if got := texts(result.Terms); got != "龘靐,靐齉,齉爩" {
		t.Fatalf("OOV 应当拆成相邻二元组: %v", result.Terms)
	}
	if !equalWeights(weights(result.Terms), WeightBigram, WeightBigram, WeightBigram) {
		t.Fatalf("二元组权重不对: %v", weights(result.Terms))
	}
	if got := texts(result.Phrases); got != "龘靐齉爩" {
		t.Fatalf("OOV 整段也应留作短语: %v", result.Phrases)
	}

	// 单个汉字命中半张表，丢干净。
	if result := shared(t).Analyse("核"); len(result.Terms) != 0 || len(result.Phrases) != 0 {
		t.Fatalf("单字应当丢干净: %+v", result)
	}
}

// TestAnalyseDeduplicatesIgnoringCase 校验去重按大小写不敏感进行 ——
// 同一个词重复出现只值一次分，留两份只会白占名额。
func TestAnalyseDeduplicatesIgnoringCase(t *testing.T) {
	result := shared(t).Analyse("Go go GOLANG")
	if got := texts(result.Terms); got != "Go,GOLANG" {
		t.Fatalf("去重不对: %v", result.Terms)
	}
}

// TestAnalysePrefersExactTermsWhenTruncated 校验名额不够时先保精确词，
// 且输出顺序确定（精确词在词典词与二元组之前）。
func TestAnalysePrefersExactTermsWhenTruncated(t *testing.T) {
	result := shared(t).Analyse("W38 P99 hnsw 向量检索调研的排序质量到底怎么保证")

	if len(result.Terms) != DefaultMaxTerms {
		t.Fatalf("词项应截断到 %d 个，实际 %d 个: %v", DefaultMaxTerms, len(result.Terms), result.Terms)
	}
	if got := texts(result.Terms[:3]); got != "W38,P99,hnsw" {
		t.Fatalf("精确词应当排在最前: %v", result.Terms)
	}
}

// TestAnalyseKeepsMixedScriptPhrase 校验中英混排的整段保留为短语 ——
// 它跨过了 splitRuns 的中英边界，是短语机制最典型的受益者。
func TestAnalyseKeepsMixedScriptPhrase(t *testing.T) {
	result := shared(t).Analyse("pgvector索引")

	if got := texts(result.Terms); got != "pgvector,索引" {
		t.Fatalf("中英混排的词项不对: %v", result.Terms)
	}
	if got := texts(result.Phrases); got != "pgvector索引" {
		t.Fatalf("中英混排整段应当留作短语: %v", result.Phrases)
	}
}

// TestAnalyseSkipsUselessPhrases 校验两类短语不产出：
// 与词项相同的（加分等于算两遍）、以及长到几乎不可能原样命中的整句。
func TestAnalyseSkipsUselessPhrases(t *testing.T) {
	if result := shared(t).Analyse("索引"); len(result.Phrases) != 0 {
		t.Fatalf("与词项相同的短语不该重复产出: %v", result.Phrases)
	}
	if result := shared(t).Analyse("向量检索调研的排序质量到底怎么保证"); len(result.Phrases) != 0 {
		t.Fatalf("超长整句不该产出短语: %v", result.Phrases)
	}
}

// TestAnalyseIsDeterministic 校验同一输入多次切分结果完全一致 ——
// 输出会进 SQL 与响应，可复算才可调参。
func TestAnalyseIsDeterministic(t *testing.T) {
	var want string
	for run := 0; run < 10; run++ {
		result := shared(t).Analyse("W38 向量检索 pgvector索引")
		got := texts(result.Terms) + "|" + texts(result.Phrases)
		if run == 0 {
			want = got
			continue
		}
		if got != want {
			t.Fatalf("切分应当可复算: %q vs %q", got, want)
		}
	}
}

// TestAnalyseWithUserWords 校验自定义词典：加词后整体切出，不再被自带词典切碎；
// 短语与词项重合时不再重复产出。
func TestAnalyseWithUserWords(t *testing.T) {
	tokenizer, err := New(Options{UserWords: []string{"向量检索"}})
	if err != nil {
		t.Fatalf("带自定义词的分词器初始化失败: %v", err)
	}

	result := tokenizer.Analyse("向量检索怎么做")
	if got := texts(result.Terms); got != "向量检索,怎么" {
		t.Fatalf("自定义词应当整体切出: %v", result.Terms)
	}
	if got := texts(result.Phrases); got != "向量检索怎么做" {
		t.Fatalf("整段短语不对: %v", result.Phrases)
	}
}

// TestRuleBasedKeepsLegacyShape 校验降级切分与改造前的老规则一致 ——
// 词典加载失败时，词法路照常可用，只是切得糙一点。
func TestRuleBasedKeepsLegacyShape(t *testing.T) {
	result := RuleBased("向量检索", 0, 0)
	if got := texts(result.Terms); got != "向量,量检,检索" {
		t.Fatalf("降级切分应当保持二元组形态: %v", result.Terms)
	}
	if !equalWeights(weights(result.Terms), WeightBigram, WeightBigram, WeightBigram) {
		t.Fatalf("降级切分的权重不对: %v", weights(result.Terms))
	}
	if got := texts(result.Phrases); got != "向量检索" {
		t.Fatalf("降级切分也要产出短语: %v", result.Phrases)
	}
	// 降级路径里只有"正好两字"的整段按词典词加权，更长的段全是二元组。
	if !equalWeights(weights(RuleBased("索引", 0, 0).Terms), WeightDict) {
		t.Fatalf("二字词应当按词典词加权: %v", weights(RuleBased("索引", 0, 0).Terms))
	}

	if got := texts(RuleBased("P99 800ms 索引", 0, 0).Terms); got != "P99,800ms,索引" {
		t.Fatalf("降级切分的精确词不对: %v", got)
	}
	if result := RuleBased("核", 0, 0); len(result.Terms) != 0 || len(result.Phrases) != 0 {
		t.Fatalf("降级切分应丢弃单字: %+v", result)
	}
}
