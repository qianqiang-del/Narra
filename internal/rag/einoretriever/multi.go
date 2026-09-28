package einoretriever

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/flow/retriever/multiquery"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"

	"narra/internal/rag"
	"narra/pkg/logger"
)

// 多查询的条数口径。
const (
	// maxVariants 是变体上限（不含原查询）。3 条已覆盖"换几个说法"的常见形态；
	// 再多收益递减 —— 每个变体都要多跑一遍完整的两路召回与一次查询向量化。
	maxVariants = 3

	// rrfK 是融合常数，与 rag 的 rrfConstant 同值：得分 = Σ 1/(k + rank)。
	// 只看名次，跨层（切片融合 / 变体融合）用同一把尺子，排序才有一致的含义。
	rrfK = 60.0

	// rewriteTimeout 是自动改写（模型调用）的上限。改写是检索的辅助步骤：
	// 超时就放弃变体、按原查询检索，不能让调用方（尤其同步接口）一直等。
	rewriteTimeout = 10 * time.Second
)

// rewritePrompt 是自动改写的提示词。写死"只输出说法本身"是为了让解析尽量简单；
// 保留编号与英文标识是硬要求 —— 它们恰恰是词法路最依赖的精确词。
const rewritePrompt = `把下面的检索词改写成 %d 个不同的说法，用于从知识库中召回文档。

要求：
- 每条说法占一行，只输出说法本身：不要编号、不要解释、不要加引号；
- 覆盖不同的措辞与同义词，不要重复原检索词；
- 原检索词里的专有名词、编号与英文标识（如 W38、P99、pgvector）必须原样保留。

检索词：%s`

// rewriteModelKey 是 ctx 里"可用于查询改写的模型"的键。
type rewriteModelKey struct{}

// WithRewriteModel 把本次运行可用的改写模型放进 ctx，返回新的 ctx。
//
// 由接入了模型的运行方注入：课堂生成在跑 Agent 之前把本次课堂的 chatModel 放进来
// （见 internal/agent/classroom），多查询门面在调用方没有提供变体时用它自动扩写。
// 没有注入时"多查询"只认调用方给的变体 —— HTTP 入口没有模型，走的正是这条路。
func WithRewriteModel(ctx context.Context, chatModel model.BaseChatModel) context.Context {
	if chatModel == nil {
		return ctx
	}
	return context.WithValue(ctx, rewriteModelKey{}, chatModel)
}

// rewriteModelFrom 取出 ctx 里的改写模型；没有时返回 nil。
func rewriteModelFrom(ctx context.Context) model.BaseChatModel {
	chatModel, _ := ctx.Value(rewriteModelKey{}).(model.BaseChatModel)
	return chatModel
}

// MultiQuery 是多查询门面：实现与 rag.Retriever 相同的方法签名，
// 把"原查询 + 若干变体"各自检索，融合后返回一份结果。
//
// 变体有两个来源，按优先级：
//  1. 调用方给的 Variants（HTTP body / MCP 工具的 queries 参数）—— 写查询的那个
//     Agent 手里有课程需求与上一轮结果，它给的说法最准；
//  2. ctx 里注入的改写模型（见 WithRewriteModel）—— Agent 只给一条时自动扩写，
//     失败或超时就退回原查询。
//
// 两者都没有时就是单查询，直通内层，不付任何编排开销。编排复用 Eino 的 multiquery
// 流程（并发检索 + 融合钩子），默认的按文档去重换成 RRF。检索核心 internal/rag
// 仍然不依赖大模型：改写只发生在门面这一层，模型由运行方在调用前注入。
type MultiQuery struct {
	inner Searcher
}

// NewMultiQuery 创建多查询门面。
func NewMultiQuery(inner Searcher) (*MultiQuery, error) {
	if inner == nil {
		return nil, fmt.Errorf("多查询门面需要非空的检索能力")
	}
	return &MultiQuery{inner: inner}, nil
}

// Retrieve 实现与 *rag.Retriever 相同的方法签名，可直接替换给服务层使用。
func (m *MultiQuery) Retrieve(ctx context.Context, input rag.RetrieveInput) (rag.RetrieveResult, error) {
	variants := normalizeVariants(input.Text, input.Variants)
	if len(variants) == 0 {
		variants = rewriteVariants(ctx, rewriteModelFrom(ctx), input.Text)
	}
	if len(variants) == 0 {
		return m.inner.Retrieve(ctx, input)
	}

	topK := rag.ClampTopK(input.TopK)
	queries := append([]string{input.Text}, variants...)

	// 适配器与 multiquery 按调用现建：变体与 top_k 都是这次请求的参数，而 multiquery
	// 的改写钩子只能拿到 query 字符串（拿不到变体列表）。构建本身只是小对象的组装，
	// 相比后面的向量化与两路 SQL 可以忽略。
	adapter, err := New(m.inner, topK)
	if err != nil {
		return rag.RetrieveResult{}, err
	}
	multi, err := multiquery.NewRetriever(ctx, &multiquery.Config{
		OrigRetriever: adapter,
		MaxQueriesNum: len(queries),
		RewriteHandler: func(context.Context, string) ([]string, error) {
			// 改写器直接返回算好的列表：原查询排第一（它信息量最大，也是 model /
			// terms 等排障口径的来源），变体跟在后面。
			return queries, nil
		},
		FusionFunc: fuseByRRF,
	})
	if err != nil {
		return rag.RetrieveResult{}, fmt.Errorf("构造多查询检索失败: %w", err)
	}

	docs, err := multi.Retrieve(ctx, input.Text)
	if err != nil {
		// multiquery 的约定是任一路失败整次失败。退回单查询：原查询通常已经能召回
		// 大部分内容，后台留一条告警说明这次变体没用上。
		logger.Warn("多查询检索失败，退回单查询",
			zap.String("query", input.Text),
			zap.Int("variants", len(variants)),
			zap.Error(err),
		)
		return m.inner.Retrieve(ctx, rag.RetrieveInput{Text: input.Text, TopK: input.TopK})
	}
	return fusedToResult(docs, topK), nil
}

// normalizeVariants 清洗调用方给的同义检索词：去空白、丢空串、去重（大小写不敏感）、
// 丢掉与主查询相同的，最后截到 maxVariants 条。
func normalizeVariants(query string, variants []string) []string {
	seen := map[string]struct{}{strings.ToLower(strings.TrimSpace(query)): {}}
	out := make([]string, 0, maxVariants)
	for _, variant := range variants {
		variant = strings.TrimSpace(variant)
		if variant == "" {
			continue
		}
		key := strings.ToLower(variant)
		if _, duplicated := seen[key]; duplicated {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, variant)
		if len(out) >= maxVariants {
			break
		}
	}
	return out
}

// rewriteVariants 用注入的模型把检索词扩写成若干同义说法。
//
// 任何一步失败（超时、上游报错、输出解析不出东西）都只留一条告警并返回空列表，
// 由调用方按原查询检索 —— 改写是增强，不该让整次检索失败。
func rewriteVariants(ctx context.Context, chatModel model.BaseChatModel, query string) []string {
	if chatModel == nil {
		return nil
	}
	promptCtx, cancel := context.WithTimeout(ctx, rewriteTimeout)
	defer cancel()

	message, err := chatModel.Generate(promptCtx, []*schema.Message{
		schema.UserMessage(fmt.Sprintf(rewritePrompt, maxVariants, query)),
	})
	if err != nil {
		logger.Warn("查询改写失败，按原检索词检索",
			zap.String("query", query),
			zap.Error(err),
		)
		return nil
	}

	variants := parseRewriteVariants(message.Content, query)
	if len(variants) == 0 {
		logger.Warn("查询改写没有产出有效变体，按原检索词检索", zap.String("query", query))
		return nil
	}
	logger.Debug("查询改写完成",
		zap.String("query", query),
		zap.Strings("variants", variants),
	)
	return variants
}

// parseRewriteVariants 解析模型输出的逐行变体：去编号与引号、丢空行、去重
// （含与原检索词相同的），截到 maxVariants 条。
func parseRewriteVariants(content string, query string) []string {
	seen := map[string]struct{}{strings.ToLower(strings.TrimSpace(query)): {}}
	variants := make([]string, 0, maxVariants)
	for _, line := range strings.Split(content, "\n") {
		variant := cleanRewriteLine(line)
		if variant == "" {
			continue
		}
		key := strings.ToLower(variant)
		if _, duplicated := seen[key]; duplicated {
			continue
		}
		seen[key] = struct{}{}
		variants = append(variants, variant)
		if len(variants) >= maxVariants {
			break
		}
	}
	return variants
}

// cleanRewriteLine 去掉模型常见的排版噪声：行首编号（1. / 2、/ 3)）、项目符号
// 与首尾引号。模型不总听话，宁可在这里宽容一点，也不要因为一行写错整批丢掉。
func cleanRewriteLine(line string) string {
	line = strings.TrimSpace(line)
	line = strings.TrimSpace(strings.TrimLeft(line, "-—–•*·"))

	runes := []rune(line)
	digits := 0
	for digits < len(runes) && digits < 3 && unicode.IsDigit(runes[digits]) {
		digits++
	}
	if digits > 0 && digits < len(runes) {
		switch runes[digits] {
		case '.', '、', ')', '）', ':', '：':
			line = strings.TrimSpace(string(runes[digits+1:]))
		}
	}
	return strings.TrimSpace(strings.Trim(line, `"'“”‘’`))
}

// fuseByRRF 把各变体的命中列表按倒数排名融合（Reciprocal Rank Fusion）。
//
// 每个列表内部已经按自己的相关度排好（rag 的两路融合结果），融合只看"这条在自己的
// 列表里排第几"：Σ 1/(k + rank)。同一切片被多个变体命中时得分相加 —— 多个说法都
// 指向它，通常就是最该排第一的那条。
//
// 合并后的元数据：
//   - method：任一变体给过向量成分就保留向量成分，任一变体给过词法成分就保留词法
//     成分，两者都有记 hybrid；similarity 取各次的最大值；
//   - model / terms：统一取**原查询**那一路的值（列表第一个），排障口径与单查询一致。
func fuseByRRF(_ context.Context, lists [][]*schema.Document) ([]*schema.Document, error) {
	type draft struct {
		doc        *schema.Document
		score      float64
		vector     bool
		lexical    bool
		similarity float64
		hasSim     bool
	}
	drafts := make(map[string]*draft, len(lists)*4)
	for _, list := range lists {
		for rank, doc := range list {
			item, ok := drafts[doc.ID]
			if !ok {
				// 复制一份再改：融合会写分数与合并后的元数据，不能改动调用方
				// 拿到的 Document（它们可能同时被回调或上游持有）。
				cloned := *doc
				cloned.MetaData = maps.Clone(doc.MetaData)
				item = &draft{doc: &cloned}
				drafts[doc.ID] = item
			}
			item.score += 1 / (rrfK + float64(rank+1))
			switch metaString(doc, metaMethod) {
			case rag.MethodVector:
				item.vector = true
			case rag.MethodLexical:
				item.lexical = true
			case rag.MethodHybrid:
				item.vector, item.lexical = true, true
			}
			if value, ok := doc.MetaData[metaSimilarity].(float64); ok && (!item.hasSim || value > item.similarity) {
				item.similarity, item.hasSim = value, true
			}
		}
	}

	fused := make([]*schema.Document, 0, len(drafts))
	for _, item := range drafts {
		switch {
		case item.vector && item.lexical:
			item.doc.MetaData[metaMethod] = rag.MethodHybrid
		case item.vector:
			item.doc.MetaData[metaMethod] = rag.MethodVector
		case item.lexical:
			item.doc.MetaData[metaMethod] = rag.MethodLexical
		}
		if item.hasSim {
			item.doc.MetaData[metaSimilarity] = item.similarity
		}
		fused = append(fused, item.doc.WithScore(item.score))
	}

	// 融合分打平时按文档与切片顺序排，保证同样的输入两次检索结果一致
	// （融合的分数粒度粗，打平是常态，与 rag 的排序规则一致）。
	slices.SortFunc(fused, func(left, right *schema.Document) int {
		if difference := cmp.Compare(right.Score(), left.Score()); difference != 0 {
			return difference
		}
		if difference := cmp.Compare(metaUint64(left, metaDocumentID), metaUint64(right, metaDocumentID)); difference != 0 {
			return difference
		}
		return cmp.Compare(metaInt(left, metaChunkIndex), metaInt(right, metaChunkIndex))
	})

	// model / terms 统一取原查询那一路：排障时"用了哪个模型、拆出哪些词项"
	// 只有一个来源，不会被变体的值覆盖成随机的那个。
	if len(lists) > 0 && len(lists[0]) > 0 {
		origin := lists[0][0].MetaData
		for _, doc := range fused {
			if value, ok := origin[metaModel]; ok {
				doc.MetaData[metaModel] = value
			}
			if value, ok := origin[metaTerms]; ok {
				doc.MetaData[metaTerms] = value
			}
		}
	}
	return fused, nil
}

// fusedToResult 把融合结果翻回项目侧的检索结果：截到有效 top_k，
// model / terms 从元数据取（原查询那一路），命中逐条还原。
func fusedToResult(docs []*schema.Document, topK int) rag.RetrieveResult {
	if len(docs) > topK {
		docs = docs[:topK]
	}
	result := rag.RetrieveResult{Hits: make([]rag.Hit, 0, len(docs))}
	for _, doc := range docs {
		result.Hits = append(result.Hits, documentToHit(doc))
	}
	if len(docs) > 0 {
		result.Model = metaString(docs[0], metaModel)
		if terms, ok := docs[0].MetaData[metaTerms].([]string); ok {
			result.Terms = terms
		}
	}
	return result
}
