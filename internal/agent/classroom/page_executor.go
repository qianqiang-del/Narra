package classroom

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"narra/internal/model/entity"
	"narra/internal/repository"
	"narra/pkg/logger"
	"narra/pkg/utils"
)

// pageLeaseTTL 是页面租约的有效期。实测一次页面生成最长约三分钟，这里留足余量：
// 租约过期只是最后一道兜底，正常情况下靠心跳续命，崩溃恢复则靠同 run 直接接管。
const pageLeaseTTL = 10 * time.Minute

// pageLeaseHeartbeat 是续租间隔，比 TTL 短得多，漏掉一次心跳也还来得及。
const pageLeaseHeartbeat = 2 * time.Minute

// leaseWriteTimeout 是租约写库（抢、续、放）的超时，这些写不该拖住页面本身。
const leaseWriteTimeout = 5 * time.Second

// errLeaseLost 表示这一页的租约已经被别的执行者接管。
//
// 它不是失败：这一页现在归别人做，本次执行者唯一要做的是立刻停手——
// 继续跑只是白烧模型调用，结果也落不进库（每次写库都会撞上 fencing）。
var errLeaseLost = errors.New("页面租约已被接管")

// newLeaseOwner 造本次执行的租约标识：运行标识 + 随机尾巴。
//
// 随机尾巴是关键。同一个 run 被重复投递时（进程崩溃后重投、asynq 重试、启动时补投），
// 两边拿到的 run_id 一模一样，只用 run_id 当 owner 的话，老执行者的写库仍能通过租约校验，
// fencing 就形同虚设。owner 必须逐次执行都不同。
func newLeaseOwner(runID string) (string, error) {
	suffix, err := utils.RandomHex(8)
	if err != nil {
		return "", fmt.Errorf("生成页面租约标识失败: %w", err)
	}
	if runID == "" {
		return suffix, nil
	}
	if len(runID) > 32 {
		runID = runID[:32]
	}
	return runID + "-" + suffix, nil
}

// pageExecutor 执行一页：抢租约 → 规划 → 按需调研 → 内容 → 讲稿 → 审核 → 落库 → 合成语音。
//
// 单页 Graph 每页现建：图在构建期带可变状态，多页共用要靠框架的内部保证，
// 而重建的代价相对一次模型调用可以忽略。
type pageExecutor struct {
	deps      Deps
	classroom *entity.Classroom
	teacher   entity.PresetAgent
	voice     string
	rt        *runtime
	context   ClassroomContext
	outline   []OutlineEntry
	ttsPool   ttsLimiter
	// runID 是本轮生成的运行标识，owner 是本执行者在这一轮里的身份。
	// 两者都随执行者走，逐页共用：租约是按页（行）算的，同一次执行手里的页互不干扰。
	runID string
	owner string
}

// run 执行一页：先抢这一页的租约，抢到才算这一页归本执行者做。
//
// 抢不到不是失败——这一页正被别人生成，或者已经被人做完，直接跳过，
// 让调度方照常推进后面的页。
func (e *pageExecutor) run(ctx context.Context, task pageTask) error {
	acquired, err := e.deps.Scenes.AcquireLease(ctx, task.Scene.ID, e.owner, e.runID, pageLeaseTTL)
	if err != nil {
		return err
	}
	if !acquired {
		logger.Info("这一页的租约在别处，跳过",
			zap.Uint64("classroom_id", e.classroom.ID),
			zap.Int32("sort_order", task.Scene.SortOrder),
			zap.String("title", task.Scene.Title),
		)
		return nil
	}
	defer e.releaseLease(task.Scene.ID)
	stopHeartbeat := e.startLeaseHeartbeat(ctx, task.Scene.ID)
	defer stopHeartbeat()

	err = e.execute(ctx, task)
	if err == nil {
		return nil
	}
	if errors.Is(err, errLeaseLost) {
		// 页面已被接管，这一页的成败现在由接管者写，本执行者退出时不算失败。
		logger.Info("页面租约已被接管，本执行者退出这一页",
			zap.Uint64("classroom_id", e.classroom.ID),
			zap.Int32("sort_order", task.Scene.SortOrder),
		)
		return nil
	}
	e.failScene(ctx, task.Scene.ID, err)
	return err
}

// execute 把这一页做完：能接着合成就直接补语音，否则在图上走一遍，最后落库。
func (e *pageExecutor) execute(ctx context.Context, task pageTask) error {
	started := time.Now()
	if err := e.deps.Scenes.UpdateStatus(ctx, task.Scene.ID, e.owner, entity.SceneStatusGenerating, nil); err != nil {
		return err
	}

	state := &pageRunState{
		Scene:   task.Scene,
		Page:    task.Page,
		Context: e.pageContext(task),
		Budget:  &pageBudget{},
	}
	checkpointRestored := restorePageCheckpoint(state, task.Scene.GenerationCheckpoint)
	resumed, err := e.resumeSynthesis(ctx, task.Scene)
	if err == nil && !resumed {
		err = e.runPageGraph(ctx, state)
	}

	logger.Info("课堂页面生成结束",
		zap.Uint64("classroom_id", e.classroom.ID),
		zap.Int32("sort_order", task.Scene.SortOrder),
		zap.String("title", task.Scene.Title),
		zap.Duration("elapsed", time.Since(started)),
		zap.Int("revisions", state.Rounds),
		zap.Int("model_calls", state.Budget.used),
		zap.Bool("resumed", resumed),
		zap.Bool("checkpoint_restored", checkpointRestored),
		zap.Bool("ok", err == nil),
	)
	if err != nil {
		return err
	}
	if err := e.deps.Scenes.CompleteGeneration(ctx, task.Scene.ID, e.owner); err != nil {
		if errors.Is(err, repository.ErrLeaseLost) {
			return errLeaseLost
		}
		return err
	}
	return nil
}

// runPageGraph 建图、跑图、落库：这一页的全部模型工作都在这里。
func (e *pageExecutor) runPageGraph(ctx context.Context, state *pageRunState) error {
	if state.ResumeNode == pageRouteDone {
		return e.persistResult(ctx, state)
	}
	graph, err := buildPageGraph(ctx, e)
	if err != nil {
		return err
	}
	result, err := graph.Invoke(ctx, state)
	if err != nil {
		if restoreRevisionFallback(state, "修订执行失败，已恢复修订前版本："+truncateRunes(err.Error(), 200)) {
			return e.persistResult(ctx, state)
		}
		return err
	}
	return e.persistResult(ctx, result)
}

// resumeSynthesis 走断点续传的近路：内容与讲稿都已落库、只差语音的页面，
// 直接从讲解段落接着合成，不把图从头再跑一遍——重跑要白花一遍模型调用，产出还是同一份内容。
//
// 返回 true 表示这一页已由这条近路处理完（成不成看 err），调用方不必再跑图。
// 触发条件只有一种：scenes.phase 停在 synthesizing。那个标记是在内容与讲稿整批落库成功
// 之后才写的，所以它是「内容已在库里、可以接着合成」的可靠凭据，不需要再解析页面 JSON 判断。
func (e *pageExecutor) resumeSynthesis(ctx context.Context, scene entity.Scene) (bool, error) {
	if e.deps.TTS == nil || scene.Phase != entity.ScenePhaseSynthesis {
		return false, nil
	}
	segments, err := e.deps.Segments.ListByScene(ctx, scene.ID)
	if err != nil {
		// 读不到段落就别去跑图：库有问题时跑图只会白烧三分钟模型调用，最后照样落不了库。
		return true, err
	}
	if len(segments) == 0 {
		// 进度标记说内容已落库，库里却没有段落；标记不可信，老实把图重跑一遍。
		return false, nil
	}
	logger.Info("这一页的内容与讲稿已在库里，接着补语音",
		zap.Uint64("classroom_id", e.classroom.ID),
		zap.Int32("sort_order", scene.SortOrder),
		zap.Int("segments", len(segments)),
	)
	return true, synthesizeSegments(ctx, e.deps, e.classroom.ID, scene.ID, e.owner, e.voice, segments, e.ttsPool)
}

// failScene 把这一页置 failed 并写下原因。
//
// 写库脱离任务 ctx：任务超时或取消也要留下失败记录，否则这一页会带着 generating 状态留在库里。
// 租约已经易主时这次写会撞上 fencing，那是对的——接管者会写下真正的结果。
func (e *pageExecutor) failScene(ctx context.Context, sceneID uint64, cause error) {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), leaseWriteTimeout)
	defer cancel()

	message := truncateRunes(cause.Error(), 500)
	if err := e.deps.Scenes.UpdateStatus(writeCtx, sceneID, e.owner, entity.SceneStatusFailed, &message); err != nil {
		logger.Warn("置页面失败状态未生效",
			zap.Uint64("classroom_id", e.classroom.ID),
			zap.Uint64("scene_id", sceneID),
			zap.Error(err),
		)
		return
	}
	_ = e.setPhase(writeCtx, sceneID, entity.ScenePhaseFailed)
}

// releaseLease 主动放掉租约：这一页已经跑完，重投的执行者不必干等 TTL 过期。
func (e *pageExecutor) releaseLease(sceneID uint64) {
	writeCtx, cancel := context.WithTimeout(context.Background(), leaseWriteTimeout)
	defer cancel()

	if err := e.deps.Scenes.ReleaseLease(writeCtx, sceneID, e.owner); err != nil {
		logger.Warn("释放页面租约失败",
			zap.Uint64("classroom_id", e.classroom.ID),
			zap.Uint64("scene_id", sceneID),
			zap.Error(err),
		)
	}
}

// startLeaseHeartbeat 起租约心跳，返回的函数用于停表；页面结束时必须调用。
func (e *pageExecutor) startLeaseHeartbeat(ctx context.Context, sceneID uint64) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(pageLeaseHeartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !e.renewLease(ctx, sceneID) {
					return
				}
			}
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}

// renewLease 续一次租约，返回是否继续心跳。
//
// 续租失败只停心跳，不打断这一页：页面正跑在模型调用里，硬中断拿不到干净的落库点。
// 租约真丢了也不用靠心跳发现——页面内下一次进度写入就会撞上 ErrLeaseLost，那时再收手。
func (e *pageExecutor) renewLease(ctx context.Context, sceneID uint64) bool {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), leaseWriteTimeout)
	defer cancel()

	ok, err := e.deps.Scenes.RenewLease(writeCtx, sceneID, e.owner, pageLeaseTTL)
	if err != nil {
		logger.Warn("续页面租约失败",
			zap.Uint64("classroom_id", e.classroom.ID),
			zap.Uint64("scene_id", sceneID),
			zap.Error(err),
		)
		return true
	}
	if !ok {
		logger.Warn("页面租约已易主，停止续租",
			zap.Uint64("classroom_id", e.classroom.ID),
			zap.Uint64("scene_id", sceneID),
		)
		return false
	}
	return true
}

// plan 节点：定这一页的执行计划。
func (e *pageExecutor) plan(ctx context.Context, state *pageRunState) error {
	if state.skipForResume(pageNodePlan) {
		return nil
	}
	if !state.Budget.trySpend() {
		return fmt.Errorf("这一页的模型调用预算已用尽")
	}
	plan, err := planPage(ctx, e.rt, state.Context)
	if err != nil {
		return err
	}
	state.Plan = plan
	nextNode := pageNodeContent
	if plan.RequiresTools {
		nextNode = pageNodeResearch
	}
	return e.saveCheckpoint(ctx, state, nextNode)
}

// research 节点：按执行计划取证据，取不到就用空证据继续。
func (e *pageExecutor) research(ctx context.Context, state *pageRunState) error {
	if state.skipForResume(pageNodeResearch) {
		return nil
	}
	if err := e.setPhase(ctx, state.Scene.ID, entity.ScenePhaseResearching); err != nil {
		return err
	}
	var steps []ToolStep
	if state.Plan != nil {
		steps = state.Plan.ToolSteps
	}
	// 修订回到调研时，若执行计划没变（工具步骤指纹一致），上一轮的证据仍然成立，直接沿用：
	// 重查一遍只会多付一次联网和一次模型调用，拿回同一份结论。
	signature := toolStepsSignature(steps)
	if state.Evidence != nil && signature == state.ResearchSteps {
		logger.Info("沿用上一轮调研结果，跳过重复检索",
			zap.Uint64("classroom_id", e.classroom.ID),
			zap.Int32("sort_order", state.Scene.SortOrder),
		)
		return e.saveCheckpoint(ctx, state, pageNodeContent)
	}
	if !state.Budget.trySpend() {
		state.ReviewNote = "模型调用预算已用尽，跳过资料调研"
		return e.saveCheckpoint(ctx, state, pageNodeContent)
	}
	bundle, note := researchEvidence(ctx, e.rt, &researchInput{Page: state.Context, Steps: steps})
	state.Evidence = bundle
	state.ResearchNote = note
	state.ResearchSteps = signature
	return e.saveCheckpoint(ctx, state, pageNodeContent)
}

// toolStepsSignature 把工具步骤压成可比较的指纹；计划没变就不必重新取资料。
func toolStepsSignature(steps []ToolStep) string {
	if len(steps) == 0 {
		return ""
	}
	raw, err := json.Marshal(steps)
	if err != nil {
		return ""
	}
	return string(raw)
}

// content 节点：生成并校验内容，校验不过就在节点内重跑这条 Chain。
func (e *pageExecutor) content(ctx context.Context, state *pageRunState) error {
	if state.skipForResume(pageNodeContent) {
		return nil
	}
	if err := e.setPhase(ctx, state.Scene.ID, entity.ScenePhaseContent); err != nil {
		return err
	}
	content, err := generateContent(ctx, e.rt, state.Budget, &contentInput{
		Page:     state.Context,
		Evidence: state.Evidence,
		Revision: state.Revision,
		Feedback: state.ContentFeedback,
	}, state.Scene.Type)
	if err != nil {
		return err
	}
	state.Blocks = content.Blocks
	state.HTML = content.HTML
	invalidateReview(state)
	state.ContentFeedback = ""
	return e.saveCheckpoint(ctx, state, pageNodeNarration)
}

// narration 节点：生成并校验讲稿，校验不过就在节点内重跑这条 Chain。
func (e *pageExecutor) narration(ctx context.Context, state *pageRunState) error {
	if state.skipForResume(pageNodeNarration) {
		return nil
	}
	if err := e.setPhase(ctx, state.Scene.ID, entity.ScenePhaseNarration); err != nil {
		return err
	}
	items, err := generateNarration(ctx, e.rt, state.Budget, &narrationInput{
		Page:     state.Context,
		Blocks:   state.Blocks,
		Revision: state.Revision,
		Feedback: state.NarrationFeedback,
	})
	if err != nil {
		return err
	}
	state.Narration = items
	invalidateReview(state)
	state.NarrationFeedback = ""
	return e.saveCheckpoint(ctx, state, pageNodeReview)
}

// review 节点：审核内容与讲稿。审核没做成不否决这一页，只记一条说明。
func (e *pageExecutor) review(ctx context.Context, state *pageRunState) error {
	if state.skipForResume(pageNodeReview) {
		return nil
	}
	if err := e.setPhase(ctx, state.Scene.ID, entity.ScenePhaseReviewing); err != nil {
		return err
	}
	invalidateReview(state)
	if !state.Budget.trySpend() {
		state.ReviewNote = "模型调用预算已用尽，跳过审核"
		return e.saveCheckpoint(ctx, state, pageNodeRevision)
	}
	review, err := reviewPage(ctx, e.rt, &reviewInput{
		Page:      state.Context,
		Plan:      state.Plan,
		Blocks:    state.Blocks,
		Narration: state.Narration,
		HTML:      state.HTML,
	})
	if err != nil {
		state.ReviewNote = truncateRunes("审核未完成："+err.Error(), 300)
		logger.Warn("页面审核未完成",
			zap.Uint64("classroom_id", e.classroom.ID),
			zap.Int32("sort_order", state.Scene.SortOrder),
			zap.Error(err),
		)
		return e.saveCheckpoint(ctx, state, pageNodeRevision)
	}
	state.Review = review
	state.ReviewArtifactHash = pageArtifactHash(state)
	return e.saveCheckpoint(ctx, state, pageNodeRevision)
}

// route 节点：按审核意见决定回哪个节点，或就此收束。
func (e *pageExecutor) route(ctx context.Context, state *pageRunState) error {
	if state.skipForResume(pageNodeRevision) {
		return nil
	}
	review := state.Review
	if review == nil {
		restoreRevisionFallback(state, "修订后审核未完成，已恢复修订前版本")
		state.Route = pageRouteDone
		return e.saveCheckpoint(ctx, state, pageRouteDone)
	}
	if review.Approved {
		state.RevisionFallback = nil
		state.Route = pageRouteDone
		return e.saveCheckpoint(ctx, state, pageRouteDone)
	}
	if state.Rounds >= maxRevisionRounds || state.Budget.exhausted() {
		if !restoreRevisionFallback(state, "修订后仍未通过审核，已恢复修订前版本") {
			state.ReviewNote = "达到修订上限，按代码校验结果落库"
		}
		state.Route = pageRouteDone
		return e.saveCheckpoint(ctx, state, pageRouteDone)
	}
	state.Rounds++
	state.Revision = reviewFeedback(review)
	state.RevisionFallback = snapshotReviewedPage(state)
	invalidateReview(state)

	// both 与 content 都从内容专家重做：内容改完，讲稿会顺着图上的边重新生成。
	switch revisionTarget(review.Issues) {
	case reviewTargetPlan:
		state.Route = pageNodePlan
	case reviewTargetResearch:
		state.Route = pageNodeResearch
	case reviewTargetNarration:
		state.Route = pageNodeNarration
	default:
		state.Route = pageNodeContent
	}
	return e.saveCheckpoint(ctx, state, state.Route)
}

// persistResult 落库这一页的内容、讲稿与审核摘要，然后合成语音。
func (e *pageExecutor) persistResult(ctx context.Context, state *pageRunState) error {
	record := pageReviewRecord{Rounds: state.Rounds, Note: state.ReviewNote, ResearchNote: state.ResearchNote}
	artifactHash := pageArtifactHash(state)
	if state.Review != nil && state.ReviewArtifactHash == artifactHash {
		record.ReviewResult = *state.Review
		record.ArtifactHash = artifactHash
	} else if state.Review != nil || state.ReviewArtifactHash != "" {
		record.Note = appendReviewNote(record.Note, "审核结论与最终内容不一致，已丢弃旧审核")
	}
	review, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("编码审核摘要失败: %w", err)
	}

	textOnly := e.deps.TTS == nil
	segments, err := persistSceneWithRetry(ctx, e.deps, state.Scene.ID, e.owner, state.Blocks, state.Narration, review, state.HTML, textOnly)
	if err != nil {
		return err
	}
	if textOnly {
		return nil
	}
	if err := e.setPhase(ctx, state.Scene.ID, entity.ScenePhaseSynthesis); err != nil {
		return err
	}
	return synthesizeSegments(ctx, e.deps, e.classroom.ID, state.Scene.ID, e.owner, e.voice, segments, e.ttsPool)
}

func (e *pageExecutor) saveCheckpoint(ctx context.Context, state *pageRunState, nextNode string) error {
	// 纯节点单测会用不带仓储依赖的执行器；生产运行时 Scenes 始终存在。
	if e.deps.Scenes == nil {
		return nil
	}
	checkpoint := checkpointFromState(state, nextNode)
	raw, err := json.Marshal(checkpoint)
	if err != nil {
		return fmt.Errorf("编码页面生成断点失败: %w", err)
	}
	err = e.deps.Scenes.UpdateCheckpoint(ctx, state.Scene.ID, e.owner, raw)
	if err == nil {
		return nil
	}
	if errors.Is(err, repository.ErrLeaseLost) {
		return errLeaseLost
	}
	logger.Warn("保存页面生成断点失败，本次继续生成",
		zap.Uint64("classroom_id", e.classroom.ID),
		zap.Uint64("scene_id", state.Scene.ID),
		zap.String("next_node", nextNode),
		zap.Error(err),
	)
	return nil
}

func (e *pageExecutor) clearCheckpoint(ctx context.Context, sceneID uint64) error {
	if e.deps.Scenes == nil {
		return nil
	}
	err := e.deps.Scenes.ClearCheckpoint(ctx, sceneID, e.owner)
	if err == nil {
		return nil
	}
	if errors.Is(err, repository.ErrLeaseLost) {
		return errLeaseLost
	}
	logger.Warn("清理页面生成断点失败，保留断点供幂等恢复",
		zap.Uint64("classroom_id", e.classroom.ID),
		zap.Uint64("scene_id", sceneID),
		zap.Error(err),
	)
	return nil
}

func invalidateReview(state *pageRunState) {
	state.Review = nil
	state.ReviewArtifactHash = ""
}

func snapshotReviewedPage(state *pageRunState) *reviewedPageSnapshot {
	if state.Review == nil || state.ReviewArtifactHash == "" || state.ReviewArtifactHash != pageArtifactHash(state) {
		return nil
	}
	review := *state.Review
	review.Issues = append([]ReviewIssue(nil), state.Review.Issues...)
	return &reviewedPageSnapshot{
		Blocks:       append([]contentBlock(nil), state.Blocks...),
		Narration:    append([]narrationSegment(nil), state.Narration...),
		HTML:         state.HTML,
		Review:       review,
		ArtifactHash: state.ReviewArtifactHash,
		ReviewNote:   state.ReviewNote,
	}
}

func restoreRevisionFallback(state *pageRunState, note string) bool {
	fallback := state.RevisionFallback
	if fallback == nil {
		return false
	}
	state.Blocks = append([]contentBlock(nil), fallback.Blocks...)
	state.Narration = append([]narrationSegment(nil), fallback.Narration...)
	state.HTML = fallback.HTML
	review := fallback.Review
	review.Issues = append([]ReviewIssue(nil), fallback.Review.Issues...)
	state.Review = &review
	state.ReviewArtifactHash = fallback.ArtifactHash
	state.ReviewNote = appendReviewNote(fallback.ReviewNote, note)
	state.RevisionFallback = nil
	return true
}

func pageArtifactHash(state *pageRunState) string {
	raw, err := json.Marshal(struct {
		Blocks    []contentBlock     `json:"blocks"`
		Narration []narrationSegment `json:"narration"`
		HTML      string             `json:"html"`
	}{
		Blocks: state.Blocks, Narration: state.Narration, HTML: state.HTML,
	})
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func appendReviewNote(existing, note string) string {
	if existing == "" {
		return note
	}
	return existing + "\uFF1B" + note
}

// pageContext 组装这一页的分层上下文。
func (e *pageExecutor) pageContext(task pageTask) PageContext {
	return PageContext{
		Classroom: e.context,
		Outline:   e.outline,
		Current:   task.Page,
		Neighbor:  neighborOf(e.outline, task.Page.Order),
		Teacher:   e.teacher,
	}
}

// setPhase 更新这一页的进度标记，顺带校验租约；租约易主时返回 errLeaseLost 让节点停手。
//
// 进度标记本来就是每个节点都要写的一次库，用它校验租约不额外花代价，也正好是
// 「每次写库再校验」那条要求落地的位置——只在抢租约时校验等于没校验。
//
// 只有租约易主才中断这一页；其它写库失败仍只记账，一次数据库抖动不该废掉已经做出来的内容。
func (e *pageExecutor) setPhase(ctx context.Context, sceneID uint64, phase string) error {
	err := e.deps.Scenes.UpdatePhase(ctx, sceneID, e.owner, phase)
	if err == nil {
		return nil
	}
	if errors.Is(err, repository.ErrLeaseLost) {
		logger.Warn("页面租约已易主，本执行者停手",
			zap.Uint64("classroom_id", e.classroom.ID),
			zap.Uint64("scene_id", sceneID),
			zap.String("phase", phase),
		)
		return errLeaseLost
	}
	logger.Warn("更新页面进度标记失败",
		zap.Uint64("classroom_id", e.classroom.ID),
		zap.Uint64("scene_id", sceneID),
		zap.String("phase", phase),
		zap.Error(err),
	)
	return nil
}
