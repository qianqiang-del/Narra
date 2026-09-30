package rag

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"go.uber.org/zap"

	"narra/pkg/logger"
)

// rerank.go 精排装饰器：两层 RRF 融合之后、截断到 top_k 之前，把候选交给重排模型重新打分。
//
// 为什么要装饰在**最外层**（包住多查询门面，而不是单查询检索器内）：
//   - 精排只调用一次。放在每个变体内部的话，三个变体就是三次精排调用，外层 RRF 还会把
//     精排分再碾成名次，钱花了、信号也丢了；
//   - 候选池由"融合后的完整名单"构成，而不是某个变体的前几条；
//   - 对外行为不变：内层怎么召回、怎么降级都照旧，这一层只影响顺序与分数。
//
// 候选深度（rerankCandidateDepth）与返回条数（top_k）解耦：内层按深度取（并经它自己的
// 4 倍过采样），精排看得比最终返回的多，才有"把埋没在中间的条目捞上来"的空间。

// rerankCandidateDepth 是交给精排的候选条数，写死为常量而不是配置项。
//
// 它是检索策略参数，不是服务连接参数：让用户在设置页填"精排看 30 条还是 50 条"，
// 十个人有九个不知道怎么填。评测阶段扫 20/30/50 时改这一行重编译，选出最优值后定稿。
// 上限天然受 maxTopK（50）约束：调用方要的条数比它多时，内层至少取到调用方要的条数。
const rerankCandidateDepth = 30

// Searcher 是「可被精排包装的检索能力」的最小面。
//
// *rag.Retriever 与外层的多查询门面（internal/rag/einoretriever.MultiQuery）都满足它。
// 定义在 rag 包而不是反过来依赖 einoretriever，是为了让装饰器待在纯检索域里。
type Searcher interface {
	Retrieve(ctx context.Context, input RetrieveInput) (RetrieveResult, error)
}

// Reranker 是精排能力的最小依赖面；*pkg/rerank.Client 满足它。
type Reranker interface {
	// Rerank 返回与 documents 等长、同序的相关度分（分越大越相关）。
	Rerank(ctx context.Context, query string, documents []string) ([]float64, error)
}

// RerankerProvider 返回当前生效的精排能力；返回 nil 表示精排关闭（按原样检索）。
//
// 做成函数而不是接口：精排配置可以在运行时切换（设置页保存/启停即时生效），装饰器
// 每次检索都重新取一次；*pkg/rerank.Manager 的 Current() 返回具体类型 *rerank.Client，
// 由装配处用一个闭包适配（接口方法的返回类型必须精确匹配，闭包最省事）。
type RerankerProvider func() Reranker

// Reranked 是精排装饰器：实现与内层相同的 Retrieve 签名，可直接替换给服务层使用。
type Reranked struct {
	inner    Searcher
	provider RerankerProvider
}

// NewReranked 构造精排装饰器。
func NewReranked(inner Searcher, provider RerankerProvider) (*Reranked, error) {
	if inner == nil {
		return nil, fmt.Errorf("精排装饰器需要非空的检索能力")
	}
	if provider == nil {
		return nil, fmt.Errorf("精排装饰器需要非空的重排能力来源")
	}
	return &Reranked{inner: inner, provider: provider}, nil
}

// Retrieve 先让内层多取一些候选，再精排、截断到调用方要的条数。
//
// 降级规则与两路召回一致：**精排失败不影响检索本身**。超时、报错、返回条数不符
// 都只留一条告警日志并按融合顺序返回 —— 用户可能感觉不到这次没有精排，但绝不能
// 因为精排挂了就搜不出东西。内层失败则原样上抛（那是检索真的坏了）。
func (r *Reranked) Retrieve(ctx context.Context, input RetrieveInput) (RetrieveResult, error) {
	// 精排关闭：原样透传（连候选深度都不改）。这样"没配精排"与"没包装饰器"
	// 在行为上完全一致，不会因为多包了一层就悄悄加深召回、多查库。
	reranker := r.provider()
	if reranker == nil {
		return r.inner.Retrieve(ctx, input)
	}

	topK := clampTopK(input.TopK)

	// 内层按候选深度取；调用方要得更多时至少要给它那么多（上限是 maxTopK，
	// clampTopK 已保证 topK ≤ maxTopK，而深度常量 30 在范围内）。
	depth := rerankCandidateDepth
	if topK > depth {
		depth = topK
	}
	innerInput := input
	innerInput.TopK = depth

	result, err := r.inner.Retrieve(ctx, innerInput)
	if err != nil {
		return RetrieveResult{}, err
	}
	if len(result.Hits) <= 1 {
		// 0 条没什么可排；1 条排了也还是它——跳过这次网络调用。
		return truncateHits(result, topK), nil
	}

	documents := make([]string, len(result.Hits))
	for index, hit := range result.Hits {
		documents[index] = hit.Content
	}

	started := time.Now()
	scores, err := reranker.Rerank(ctx, input.Text, documents)
	elapsed := time.Since(started)
	if err != nil {
		logger.Warn("精排失败，保留融合顺序",
			zap.String("query", input.Text),
			zap.Int("candidates", len(result.Hits)),
			zap.Duration("elapsed", elapsed),
			zap.Error(err),
		)
		return truncateHits(result, topK), nil
	}
	if len(scores) != len(result.Hits) {
		// 客户端已校验条数，这里再兜一道：装饰器不信任任何"分数与候选对不上"的输入。
		logger.Warn("精排返回的分数与候选条数不符，保留融合顺序",
			zap.Int("candidates", len(result.Hits)),
			zap.Int("scores", len(scores)),
		)
		return truncateHits(result, topK), nil
	}

	// 稳定排序：分数打平时保留内层（RRF）的顺序，保证同一查询两次结果一致。
	ranked := make([]scoredHit, len(result.Hits))
	for index, hit := range result.Hits {
		ranked[index] = scoredHit{hit: hit, score: scores[index]}
	}
	slices.SortStableFunc(ranked, func(left, right scoredHit) int {
		return cmp.Compare(right.score, left.score)
	})
	for index := range ranked {
		// Score 语义升级为"本次检索最终排序分"：精排生效时就是精排分。
		// Similarity / Method 等召回元数据原样保留，不伪造。
		ranked[index].hit.Score = ranked[index].score
		result.Hits[index] = ranked[index].hit
	}

	logger.Debug("精排完成",
		zap.String("query", input.Text),
		zap.Int("candidates", len(result.Hits)),
		zap.Duration("elapsed", elapsed),
	)
	return truncateHits(result, topK), nil
}

// scoredHit 是重排过程中的一条候选：命中 + 它这次拿到的精排分。
type scoredHit struct {
	hit   Hit
	score float64
}

// truncateHits 把命中截到 topK 条；不改变其它字段。
func truncateHits(result RetrieveResult, topK int) RetrieveResult {
	if len(result.Hits) > topK {
		result.Hits = result.Hits[:topK]
	}
	return result
}
