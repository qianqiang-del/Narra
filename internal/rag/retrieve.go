package rag

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"narra/internal/model/entity"
	"narra/pkg/embedding"
	"narra/pkg/logger"
)

// retrieve.go 检索门面：一次查询 → 两路召回 → RRF 融合 → top_k。
//
// 与 ingest.go 那半个门面是同一套装法：IO 从窄接口进来（ChunkSearcher / ModelRegistry /
// Embedder），实现在 internal/app 注入，于是这条链路的测试可以在完全离线的条件下跑完。
//
// 为什么要两路：向量召回管"意思对得上"（"怎么让检索更快"能召回讲索引的那篇），
// 但它对精确词不敏感 —— "W38"、"P99 800ms"、"knowledge_embeddings" 这类查询，
// 向量空间里挤在一起的往往是毫不相干的段落，而子串匹配一拿一个准。反过来，词法路
// 对自然语言整句无能为力（中文连分词器都没有）。两路各补对方的短板，融合交给 RRF。

// ChunkSearcher 是检索链路对持久化的最小依赖面。
//
// 与 repository.KnowledgeSearchRepository 的两个方法一一对应，单独立一个接口的意义
// 在于测试：没有它，测一次融合排序就得先摆一套 PostgreSQL + pgvector。
type ChunkSearcher interface {
	// SearchVector 在同一模型下按余弦相似度召回候选，返回按相似度降序。
	SearchVector(ctx context.Context, query entity.KnowledgeVectorQuery) ([]entity.KnowledgeChunkView, error)

	// SearchLexical 取命中任意词项的候选，返回按命中词项数降序。terms 为空时返回空。
	SearchLexical(ctx context.Context, terms []string, limit int) ([]entity.KnowledgeChunkView, error)
}

// 命中来源。对外是三个稳定字符串，界面与 MCP 适配层都按它判断这条结果是怎么来的。
const (
	MethodVector  = "vector"  // 只被向量路召回：意思相近
	MethodLexical = "lexical" // 只被词法路召回：含查询里的精确词
	MethodHybrid  = "hybrid"  // 两路都召回了它：既相近又含词，通常是最佳命中
)

// 检索的条数口径。
const (
	// defaultTopK 调用方没给 top_k 时的默认返回条数。
	// 5 是照着"喂给一段提示词的引用数"定的：引用太多会挤掉正文的预算，
	// 而且第 6 名之后的相关性通常已经明显下滑。
	defaultTopK = 5

	// maxTopK 单次检索的返回上限。检索是同步接口，一次要 50 条以上时多半是拿它当
	// 爬虫用，那条路应该走列表接口，而不是把整库从检索口倒出去。
	maxTopK = 50

	// recallOverfetch 是每条召回路的过采样倍数：两路各取 topK × 4 的候选再融合。
	// 过采样是融合的前提 —— 各取 top_k 条的话，RRF 只能在一个长度 top_k 的列表里做并集，
	// 第一路排第 1 的那条永远压过第二路排第 1 的，融合名存实亡。
	recallOverfetch = 4

	// minRecallLimit 过采样后的最小候选数。topK 很小时 4 倍仍然太少（topK = 1 只取 4 条），
	// 给融合留一点余量。
	minRecallLimit = 10

	// maxLexicalTerms 词法路最多用几个词项。
	//
	// 这是给 SQL 兜底的：词法匹配是 OR 拼起来的顺序扫描，词项越多每次检索扫得越久。
	// 8 个已经覆盖"几个专有名词 + 一小截中文"的常见形态，再往上是长句，
	// 那部分本来就该由向量路负责。
	maxLexicalTerms = 8
)

// rrfConstant 是倒数排名融合的常数 k，得分 = Σ 1 / (k + rank)。
//
// 取 60 是为了拉开头部、抹平尾部：第 1 名与第 2 名的差距足够决定排序，而 10 名开外
// 几乎没有差别 —— 混合排序里靠后的名次本来就不可靠（每路都过采样了 4 倍）。
const rrfConstant = 60.0

// RetrieveInput 是一次检索的输入。
//
// 用结构体而不是 (text string, topK int)：与 FileInput / TextInput 同一个路数，
// 将来加过滤条件（来源类型、只在某几篇文档里找）时不用改签名。
type RetrieveInput struct {
	Text string // 检索词：一句自然语言、关键词，或两者混着写
	TopK int    // 返回条数；0 或越界时按 defaultTopK / maxTopK 钳位
}

// Hit 是一次检索命中的一条切片。
type Hit struct {
	ChunkID       uint64 // 切片 ID
	DocumentID    uint64 // 所属文档 ID
	ChunkIndex    int32  // 切片在原文中的顺序号，按它可以还原上下文顺序
	Heading       string // 所在章节标题；无章节归属时为空串
	Content       string // 切片正文
	DocumentTitle string // 所属文档标题
	SourceType    string // 所属文档的来源类型
	SourceURI     string // 所属文档的来源标识；手工录入时为空串

	// Score 是 RRF 融合分，只用于**同一次检索内部**排序。
	// 它是名次的函数（1/61 + 1/64 这种量级），既不是相似度也没有绝对含义，
	// 跨次比较它没有意义 —— 要判断相关性看 Similarity 与 Method。
	Score float64

	// Similarity 是余弦相似度（-1 ~ 1，越大越像），只有向量路召回过它才有值。
	// 纯词法命中的切片没有相似度可言，此时是 nil 而不是 0 —— 0 是一个真实存在的
	// 相似度（正交），拿它当"没有"会把两种情况混成一种。
	Similarity *float64

	// Method 是命中来源：MethodVector / MethodLexical / MethodHybrid。
	Method string
}

// RetrieveResult 是一次检索的结果。
type RetrieveResult struct {
	// Model 是本次向量召回所用的模型名；向量路没跑起来时为空串。
	// 带上它是为了让"检索结果不对劲"时能一眼看出是不是换了模型导致的：
	// 向量只在同一模型下可比，换了默认模型却没有重建切片时，向量路会一条都召回不到。
	Model string

	// Terms 是词法路实际使用的词项（已经过分词规则与截断），给调参与排障看。
	Terms []string

	// Hits 是按 Score 降序的命中，长度不超过 topK。
	Hits []Hit
}

// queryEmbedderFactory 按"库里向量所属的那个模型"现建一个 Embedder。
//
// 与 embedderFactory 的差别只在模型从哪来，但这一条是检索的命门：查询串必须用
// knowledge_embeddings.model_id 指的那个模型去向量化。用全局配置里的模型，
// 一旦配置与模型行漂移（改了设置却没有重建切片），查询向量就落到了另一个语义空间 ——
// 相似度照样算得出来、不报任何错，只是排序全是噪声。这类静默劣化最难查，
// 所以模型由入参显式指定，而不是从配置里读。
type queryEmbedderFactory func(model *entity.EmbeddingModel) (Embedder, error)

// Retriever 是检索链路的门面。
type Retriever struct {
	search      ChunkSearcher
	models      ModelRegistry
	embedding   *embedding.Manager
	newEmbedder queryEmbedderFactory
}

// NewRetriever 创建检索器。
//
// embeddingManager 只用来取"当前生效的连接配置"（地址、密钥、超时），向量化用哪个模型
// 以库里的默认模型为准（见 queryEmbedderFactory）。
func NewRetriever(
	search ChunkSearcher,
	models ModelRegistry,
	embeddingManager *embedding.Manager,
) *Retriever {
	retriever := &Retriever{
		search:    search,
		models:    models,
		embedding: embeddingManager,
	}
	retriever.newEmbedder = func(model *entity.EmbeddingModel) (Embedder, error) {
		cfg := embeddingManager.Config()
		cfg.Model = model.Name
		cfg.Dimensions = int(model.Dimensions)
		// 模型行上有自己的 base_url 时以它为准：同一个密钥配到不同网关的场景下，
		// 全局配置里的地址可能根本托管不了这个模型。
		if model.BaseURL != nil && strings.TrimSpace(*model.BaseURL) != "" {
			cfg.BaseURL = *model.BaseURL
		}
		client, err := embedding.NewClient(cfg)
		if err != nil {
			return nil, fmt.Errorf("向量服务配置不可用: %w", err)
		}
		return client, nil
	}
	return retriever
}

// Retrieve 两路召回并融合出 topK 条命中。
//
// 降级规则一句话说清：**一路挂掉不影响另一路召回，但两条路都没给出结果时，失败原因
// 必须冒泡**。空结果加一行日志比报错难查得多 —— 用户看到的只是"没搜到"，
// 真实原因却可能是向量服务没配好、也可能是数据库查不动。单路成功时结果照常返回，
// 来源写在 Hit.Method 里，失败留一条告警日志，不静默。
//
// 反过来，"查到了但一条都没命中"不算失败：那就是一次正常的空结果，直接返回空列表。
func (r *Retriever) Retrieve(ctx context.Context, input RetrieveInput) (RetrieveResult, error) {
	query := strings.TrimSpace(input.Text)
	if query == "" {
		return RetrieveResult{}, fmt.Errorf("%w: 检索词不能为空", ErrEmptyQuery)
	}
	topK := clampTopK(input.TopK)
	limit := recallLimit(topK)

	result := RetrieveResult{Terms: lexicalTerms(query)}
	var failures []error

	// 向量路的准入条件是模型行：没有它就不知道该用哪个模型向量化查询串。
	// 取不到模型按"这一路不可用"记账，词法路照常跑。
	var vectorHits []entity.KnowledgeChunkView
	model, err := r.models.GetDefault(ctx)
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		err = fmt.Errorf("%w：请先在设置页保存一次向量服务配置", ErrNoEmbeddingModel)
		failures = append(failures, err)
		logger.Warn("向量召回不可用，本次检索只走词法路", zap.Error(err))
	case err != nil:
		err = fmt.Errorf("查询默认向量模型失败: %w", err)
		failures = append(failures, err)
		logger.Warn("向量召回不可用，本次检索只走词法路", zap.Error(err))
	default:
		result.Model = model.Name
		vectorHits, err = r.vectorRecall(ctx, model, query, limit)
		if err != nil {
			failures = append(failures, err)
			logger.Warn("向量召回不可用，本次检索只走词法路", zap.Error(err))
		}
	}

	lexicalHits, err := r.search.SearchLexical(ctx, result.Terms, limit)
	if err != nil {
		failures = append(failures, fmt.Errorf("词法召回失败: %w", err))
		logger.Warn("词法召回不可用，本次检索只走向量路", zap.Error(err))
		lexicalHits = nil
	}

	hits := fuseByRRF(rrfConstant, vectorHits, lexicalHits)
	if len(hits) > topK {
		hits = hits[:topK]
	}
	result.Hits = hits

	if len(hits) == 0 && len(failures) > 0 {
		return RetrieveResult{}, fmt.Errorf("检索失败: %w", errors.Join(failures...))
	}
	return result, nil
}

// vectorRecall 向量路：向量化查询串，再按余弦相似度召回候选。
func (r *Retriever) vectorRecall(
	ctx context.Context,
	model *entity.EmbeddingModel,
	query string,
	limit int,
) ([]entity.KnowledgeChunkView, error) {
	if cfg := r.embedding.Config(); !cfg.Enabled {
		return nil, fmt.Errorf("%w：请先在设置页配置向量服务", ErrEmbeddingDisabled)
	}
	embedder, err := r.newEmbedder(model)
	if err != nil {
		return nil, err
	}

	// 查询串只有一条，但仍然走批量入口：重试与条数校验对单条查询同样重要 ——
	// 429 / 网络抖动重试三次，返回条数不符直接判失败，这两件事在检索里不能少。
	batch, err := embedBatchWithRetry(ctx, embedder, []string{query})
	if err != nil {
		return nil, fmt.Errorf("检索词向量化失败: %w", err)
	}
	vector := batch[0]

	// 维度必须与模型登记值一致：库里的向量是按它 CAST 再比的，查询向量差一维，
	// 数据库报出来的是一句看不出根因的类型错误。这里的判断把话说在前面。
	if len(vector) != int(model.Dimensions) {
		return nil, fmt.Errorf("%w: 检索词向量 %d 维，模型 %s 登记为 %d 维",
			ErrEmbeddingMismatch, len(vector), model.Name, model.Dimensions)
	}
	literal, err := vectorLiteral(vector)
	if err != nil {
		return nil, fmt.Errorf("检索词向量无效: %w", err)
	}

	hits, err := r.search.SearchVector(ctx, entity.KnowledgeVectorQuery{
		ModelID:    model.ID,
		Dimensions: int(model.Dimensions),
		Vector:     literal,
		Limit:      limit,
	})
	if err != nil {
		return nil, err
	}
	return hits, nil
}

// clampTopK 把调用方要的条数钳到 [1, maxTopK]；没给（≤0）时用默认值。
func clampTopK(topK int) int {
	if topK <= 0 {
		return defaultTopK
	}
	if topK > maxTopK {
		return maxTopK
	}
	return topK
}

// recallLimit 是每条召回路的候选上限（过采样，见 recallOverfetch）。
func recallLimit(topK int) int {
	limit := topK * recallOverfetch
	if limit < minRecallLimit {
		return minRecallLimit
	}
	return limit
}

// lexicalRun 是检索词里一段连续的词字符，以及它是不是 CJK 段。
type lexicalRun struct {
	text string
	cjk  bool
}

// lexicalTerms 把一次检索词拆成词法匹配用的词项。
//
// 词法路是给"精确词"准备的：专有名词、编号、英文标识（"W38"、"P99"、"pgvector"）
// 这类查询向量路经常排不准，而子串匹配一拿一个准。反过来它对自然语言整句无能为力，
// 那部分交给向量路 —— 两条路各管一半，这正是要做混合检索的原因。
//
// 拆分规则（没有中文分词器可用，所以规则必须简单、可解释、可复算）：
//
//  1. 按"词字符"切段：字母、数字与 CJK 算词内字符，空白与中英文标点都是分隔。
//     于是 "knowledge_embeddings" 拆成 knowledge 与 embeddings —— 分开匹配反而更准，
//     整串匹配会被中间的下划线卡死。中英混排（"pgvector索引"）也在这里被切成两段。
//  2. 非 CJK 段（英文、数字、编号）整段作一个词项，长度不足 2 个字符的丢掉
//     （"a"、"3"命中半张表，全是噪声）。
//  3. CJK 段按长度：正好 2 字整段作词项（"索引"本身就是词）；**3 字及以上拆成相邻
//     二元组**（"向量检索" → 向量、量检、检索）。整句短语几乎不可能在库里逐字出现，
//     拿它当词项等于这一路直接归零；二元组是中文没有分词器时的近似，代价是会有
//     "量检"这种不成词的噪声 —— 打分只数命中词项个数，噪声词项命中谁都只得 1 分，
//     排不上去。单字丢掉（"的"、"了"）。
//  4. 词项去重（大小写不敏感）后按"精确词优先"取前 maxLexicalTerms 个：
//     专有名词与编号挑得动结果，长句的二元组之间却彼此可以替代，挤名额时先让前者。
//     顺序上先非 CJK 后 CJK，各自保持出现顺序，保证同一句检索词拆出来的结果可复算。
func lexicalTerms(query string) []string {
	var exact, approximate []string
	seen := make(map[string]struct{})
	appendTerm := func(bucket *[]string, term string) {
		key := strings.ToLower(term)
		if _, duplicated := seen[key]; duplicated {
			return
		}
		seen[key] = struct{}{}
		*bucket = append(*bucket, term)
	}

	for _, run := range splitLexicalRuns(query) {
		runes := []rune(run.text)
		if !run.cjk {
			if len(runes) >= 2 {
				appendTerm(&exact, run.text)
			}
			continue
		}
		switch {
		case len(runes) < 2:
			// 单字噪声，丢。
		case len(runes) == 2:
			appendTerm(&approximate, run.text)
		default:
			for index := 0; index+1 < len(runes); index++ {
				appendTerm(&approximate, string(runes[index:index+2]))
			}
		}
	}

	terms := make([]string, 0, maxLexicalTerms)
	for _, bucket := range [][]string{exact, approximate} {
		for _, term := range bucket {
			if len(terms) >= maxLexicalTerms {
				return terms
			}
			terms = append(terms, term)
		}
	}
	return terms
}

// splitLexicalRuns 按词字符把检索词切成若干段，段内字符同为 CJK 或同为非 CJK。
func splitLexicalRuns(query string) []lexicalRun {
	var runs []lexicalRun
	var current []rune
	currentCJK := false

	flush := func() {
		if len(current) > 0 {
			runs = append(runs, lexicalRun{text: string(current), cjk: currentCJK})
			current = current[:0]
		}
	}

	for _, character := range query {
		if !isWordRune(character) {
			flush()
			continue
		}
		cjk := isCJKRune(character)
		if len(current) > 0 && cjk != currentCJK {
			flush()
		}
		currentCJK = cjk
		current = append(current, character)
	}
	flush()
	return runs
}

// isWordRune 判断一个字符能不能待在词项中间。
//
// 用"字母或数字"而不是"不是标点"：后者会把 ①、★、→ 这类符号也收进词项，
// 它们既匹配不到东西，又会把词项的字面量撑长。
func isWordRune(character rune) bool {
	return unicode.IsLetter(character) || unicode.IsDigit(character)
}

// isCJKRune 判断一个字符是不是中日韩文字。
//
// 日文假名与韩文一并算进来：它们与中文一样没有空格分词，二元组的近似同样适用。
// 标点（，。！？）虽然在这些区段里，但已经被 isWordRune 挡在外面了。
func isCJKRune(character rune) bool {
	return unicode.Is(unicode.Han, character) ||
		unicode.Is(unicode.Hiragana, character) ||
		unicode.Is(unicode.Katakana, character) ||
		unicode.Is(unicode.Hangul, character)
}

// fuseByRRF 把两条召回路的结果按倒数排名融合（Reciprocal Rank Fusion）成一个排序。
//
// 为什么不用加权求和：两条路的"得分"根本不在一个量纲上 —— 向量路是余弦相似度
// （-1 ~ 1，通常挤在 0.6 ~ 0.95），词法路是命中的词项数（1 ~ 8）。任何权重都是拍脑袋，
// 换一批数据就要重调，而且谁也解释不清那个权重为什么对。RRF 只看名次：
// rank 从 1 数起，得分 = Σ 1/(k + rank)，于是"在自己那一路排第几"被统一成同一个刻度。
//
// 同一个切片被两路都召回时**合并成一条**，两路的贡献都算上（得分相加）并记为 hybrid：
// 它既语义相近、又含有查询里的词，通常就是最该排第一的那条 —— 混合检索的价值有一半
// 体现在这种"两边都点头"的命中上。
//
// 入参 vector / lexical 必须已经按各自的得分降序排好（仓储返回的就是这个顺序）。
func fuseByRRF(k float64, vector, lexical []entity.KnowledgeChunkView) []Hit {
	drafts := make(map[uint64]*Hit, len(vector)+len(lexical))

	for index, row := range vector {
		hit := draftFor(drafts, row)
		hit.Score += reciprocalRank(k, index)
		similarity := row.RawScore
		hit.Similarity = &similarity
		hit.Method = MethodVector
	}

	for index, row := range lexical {
		hit := draftFor(drafts, row)
		hit.Score += reciprocalRank(k, index)
		if hit.Method == MethodVector {
			hit.Method = MethodHybrid
		} else {
			hit.Method = MethodLexical
		}
	}

	hits := make([]Hit, 0, len(drafts))
	for _, hit := range drafts {
		hits = append(hits, *hit)
	}
	// 融合分相同时按文档与切片顺序排，保证同一句检索词两次搜出来的结果完全一致 ——
	// 混合排序的分数粒度本来就粗（只有几种名次组合），打平是常态。
	slices.SortFunc(hits, func(left, right Hit) int {
		if difference := cmp.Compare(right.Score, left.Score); difference != 0 {
			return difference
		}
		if difference := cmp.Compare(left.DocumentID, right.DocumentID); difference != 0 {
			return difference
		}
		return cmp.Compare(left.ChunkIndex, right.ChunkIndex)
	})
	return hits
}

// reciprocalRank 是 RRF 里"名次 → 得分"的那一步。
// index 从 0 数起，对外的名次从 1 数起，所以这里要 +1。
func reciprocalRank(k float64, index int) float64 {
	return 1 / (k + float64(index+1))
}

// draftFor 取（没有就建）一个切片的融合草稿。
// 两路都召回同一个切片时共用一个草稿，两路的得分相加 —— 这正是 RRF 要的"两边都说好，
// 就更好"；拆成两条各自排序的话，同一页内容会占掉两个名额。
func draftFor(drafts map[uint64]*Hit, row entity.KnowledgeChunkView) *Hit {
	if hit, ok := drafts[row.ChunkID]; ok {
		return hit
	}
	hit := &Hit{
		ChunkID:       row.ChunkID,
		DocumentID:    row.DocumentID,
		ChunkIndex:    row.ChunkIndex,
		Content:       row.Content,
		DocumentTitle: row.DocumentTitle,
		SourceType:    row.SourceType,
	}
	if row.Heading != nil {
		hit.Heading = *row.Heading
	}
	if row.SourceURI != nil {
		hit.SourceURI = *row.SourceURI
	}
	drafts[row.ChunkID] = hit
	return hit
}
