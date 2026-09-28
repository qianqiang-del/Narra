package classroom

import (
	"context"

	"github.com/cloudwego/eino/compose"

	"narra/internal/model/entity"
)

// 单页 Graph 的节点名。
const (
	pageNodePlan      = "page_plan"
	pageNodeResearch  = "research"
	pageNodeContent   = "content_expert"
	pageNodeNarration = "narration_expert"
	pageNodeReview    = "review"
	pageNodeRevision  = "revision_route"
)

// pageRouteDone 是修订路由的收束出口。
const pageRouteDone = compose.END

// maxPageGraphSteps 是单页 Graph 的执行步数上限，容得下两轮修订往返。
const maxPageGraphSteps = 24

// pageRunState 是单页 Graph 在节点之间传递的运行状态。
//
// 一页一个实例，页与页之间不共享，所以不需要加锁。
type pageRunState struct {
	Scene   entity.Scene
	Page    PlanPage
	Context PageContext
	Budget  *pageBudget

	Plan      *PageExecutionPlan
	Evidence  *EvidenceBundle
	Blocks    []contentBlock
	Narration []narrationSegment
	Review    *ReviewResult

	// HTML 是交互页的完整文档，其余场景类型为空串。
	HTML string

	// ReviewNote 记审核本身没做成的原因，与审核判不通过是两回事。
	ReviewNote string
	Rounds     int
	Route      string

	// ResearchSteps 是上次调研用的工具步骤指纹；修订回到调研时指纹没变就沿用已有证据。
	ResearchSteps string

	// ResearchNote 记这一页为什么没拿到外部资料；与审核结论分开，互不覆盖。
	ResearchNote string

	// Revision 是本轮审核意见，交给被点到的专家；Feedback 是校验失败的回灌，两者分开。
	Revision          string
	ContentFeedback   string
	NarrationFeedback string
}

// pageNodes 是单页 Graph 的六个节点，抽成接口便于用假节点验证图本身。
type pageNodes interface {
	plan(ctx context.Context, state *pageRunState) error
	research(ctx context.Context, state *pageRunState) error
	content(ctx context.Context, state *pageRunState) error
	narration(ctx context.Context, state *pageRunState) error
	review(ctx context.Context, state *pageRunState) error
	route(ctx context.Context, state *pageRunState) error
}

// buildPageGraph 建单页 Graph：规划 → 按需调研 → 内容 → 讲稿 → 审核 → 修订路由。
//
// 校验与重试收在内容、讲稿两个节点内部，所以图上的环只有一种含义——修订。
func buildPageGraph(ctx context.Context, nodes pageNodes) (compose.Runnable[*pageRunState, *pageRunState], error) {
	graph := compose.NewGraph[*pageRunState, *pageRunState]()
	entries := []struct {
		key string
		run func(context.Context, *pageRunState) error
	}{
		{pageNodePlan, nodes.plan},
		{pageNodeResearch, nodes.research},
		{pageNodeContent, nodes.content},
		{pageNodeNarration, nodes.narration},
		{pageNodeReview, nodes.review},
		{pageNodeRevision, nodes.route},
	}
	for _, entry := range entries {
		run := entry.run
		if err := graph.AddLambdaNode(entry.key, compose.InvokableLambda(func(ctx context.Context, state *pageRunState) (*pageRunState, error) {
			if err := run(ctx, state); err != nil {
				return nil, err
			}
			return state, nil
		})); err != nil {
			return nil, err
		}
	}

	if err := graph.AddEdge(compose.START, pageNodePlan); err != nil {
		return nil, err
	}
	if err := graph.AddBranch(pageNodePlan, compose.NewGraphBranch(planBranch, map[string]bool{
		pageNodeResearch: true,
		pageNodeContent:  true,
	})); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(pageNodeResearch, pageNodeContent); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(pageNodeContent, pageNodeNarration); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(pageNodeNarration, pageNodeReview); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(pageNodeReview, pageNodeRevision); err != nil {
		return nil, err
	}
	if err := graph.AddBranch(pageNodeRevision, compose.NewGraphBranch(routeBranch, map[string]bool{
		pageNodePlan:      true,
		pageNodeResearch:  true,
		pageNodeContent:   true,
		pageNodeNarration: true,
		pageRouteDone:     true,
	})); err != nil {
		return nil, err
	}
	return graph.Compile(ctx, compose.WithMaxRunSteps(maxPageGraphSteps))
}

// planBranch 决定规划之后要不要先去调研。
func planBranch(_ context.Context, state *pageRunState) (string, error) {
	if state.Plan != nil && state.Plan.RequiresTools {
		return pageNodeResearch, nil
	}
	return pageNodeContent, nil
}

// routeBranch 按修订路由节点算出的出口决定下一步。
func routeBranch(_ context.Context, state *pageRunState) (string, error) {
	if state.Route == "" {
		return pageRouteDone, nil
	}
	return state.Route, nil
}
