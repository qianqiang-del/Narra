package classroom

import (
	"strconv"
	"time"

	"narra/internal/middleware"
	requestdto "narra/internal/model/dto/request"
	responsedto "narra/internal/model/dto/response"
	"narra/internal/model/entity"
	"narra/internal/service"
	"narra/pkg/response"
	"narra/pkg/sse"

	"github.com/gin-gonic/gin"
)

type Controller struct{ svc service.ClassroomService }

func NewController(svc service.ClassroomService) *Controller { return &Controller{svc: svc} }

func (c *Controller) Create(ctx *gin.Context) {
	var input requestdto.CreateClassroom
	if err := ctx.ShouldBindJSON(&input); err != nil {
		response.BadRequest(ctx, "请求参数错误")
		return
	}
	item, err := c.svc.Create(ctx.Request.Context(), middleware.GetUserID(ctx), input)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, item)
}

func (c *Controller) List(ctx *gin.Context) {
	items, err := c.svc.List(ctx.Request.Context(), middleware.GetUserID(ctx))
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, items)
}

func (c *Controller) Delete(ctx *gin.Context) {
	id, ok := parseID(ctx)
	if !ok {
		return
	}
	if err := c.svc.Delete(ctx.Request.Context(), middleware.GetUserID(ctx), id); err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, gin.H{"id": id})
}

func (c *Controller) Get(ctx *gin.Context) {
	id, ok := parseID(ctx)
	if !ok {
		return
	}
	item, err := c.svc.Get(ctx.Request.Context(), middleware.GetUserID(ctx), id)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, item)
}

func (c *Controller) GetOutline(ctx *gin.Context) {
	id, ok := parseID(ctx)
	if !ok {
		return
	}
	item, err := c.svc.GetOutline(ctx.Request.Context(), middleware.GetUserID(ctx), id)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, item)
}

func (c *Controller) GetAgents(ctx *gin.Context) {
	id, ok := parseID(ctx)
	if !ok {
		return
	}
	items, err := c.svc.GetAgents(ctx.Request.Context(), middleware.GetUserID(ctx), id)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, items)
}

func (c *Controller) ListScenes(ctx *gin.Context) {
	id, ok := parseID(ctx)
	if !ok {
		return
	}
	items, err := c.svc.ListScenes(ctx.Request.Context(), middleware.GetUserID(ctx), id)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, items)
}

func (c *Controller) RetryScene(ctx *gin.Context) {
	id, ok := parseID(ctx)
	if !ok {
		return
	}
	item, err := c.svc.RetryScene(ctx.Request.Context(), middleware.GetUserID(ctx), id)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, item)
}

// scenePhaseEvent 把「这一页刚进入的阶段」翻成对外事件名。
//
// 名字按"此刻发生了什么"起，而不是照抄 phase：进入 generating_narration 说明正文已经写完、
// 开始写讲稿，所以对外叫 scene.content.completed。页面级这一套名字设计文档里没有（它只列了
// 课堂级事件），是这里补齐的；前端原来的大纲/场景轮询就是等这套事件来替掉。
var scenePhaseEvent = map[string]string{
	entity.ScenePhasePlanning:    "scene.started",
	entity.ScenePhaseResearching: "scene.researching",
	entity.ScenePhaseContent:     "scene.content.started",
	entity.ScenePhaseNarration:   "scene.content.completed",
	entity.ScenePhaseReviewing:   "scene.narration.completed",
	entity.ScenePhaseSynthesis:   "scene.reviewing.completed",
	entity.ScenePhaseReady:       "scene.ready",
	entity.ScenePhaseFailed:      "scene.failed",
}

// eventsState 记住上一次推出去的各页进度与课堂状态，用来算下一轮的差分。
type eventsState struct {
	classroomStatus string
	planPersisted   bool
	scenes          map[uint64]responsedto.ClassroomSceneSummary
}

func newEventsState(item *responsedto.Classroom, scenes []responsedto.ClassroomSceneSummary) *eventsState {
	return &eventsState{
		classroomStatus: item.Status,
		planPersisted:   len(scenes) > 0,
		scenes:          indexScenes(scenes),
	}
}

func indexScenes(scenes []responsedto.ClassroomSceneSummary) map[uint64]responsedto.ClassroomSceneSummary {
	indexed := make(map[uint64]responsedto.ClassroomSceneSummary, len(scenes))
	for _, scene := range scenes {
		indexed[scene.ID] = scene
	}
	return indexed
}

// Events 推送课堂生成进度，是这条流唯一的出口。
//
// 生成结果本来就落库，SSE 只负责通知变化：首帧给一份课堂与全部页面的快照，之后每有新变化
// 才推一条具名事件。所以前端不必轮询，重连也不必回放——重连拿到的新快照就是最新状态。
//
// 流一直开到这门课不会再变为止。ready / failed 是终态；playable 要看情况——首页落库时就会
// 置 playable，那时后面的页面还在生成，流必须继续；只有带 generation_error 的 playable
// （首页就绪后流程中断）以及「所有页面都已收口」的 playable 才算结束。
func (c *Controller) Events(ctx *gin.Context) {
	id, ok := parseID(ctx)
	if !ok {
		return
	}
	requestCtx := ctx.Request.Context()
	ownerID := middleware.GetUserID(ctx)
	item, err := c.svc.Get(requestCtx, ownerID, id)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	scenes, err := c.svc.ListScenes(requestCtx, ownerID, id)
	if err != nil {
		response.BizError(ctx, err)
		return
	}

	sse.Start(ctx)

	state := newEventsState(item, scenes)
	if err := writeInitialFrames(ctx, item, scenes); err != nil {
		return
	}
	if classroomSettled(item, scenes) {
		return
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-requestCtx.Done():
			return
		case <-heartbeat.C:
			if sse.Heartbeat(ctx) != nil {
				return
			}
		case <-ticker.C:
			item, err := c.svc.Get(requestCtx, ownerID, id)
			if err != nil {
				_ = sse.Event(ctx, "error", gin.H{"message": err.Error()})
				return
			}
			scenes, err := c.svc.ListScenes(requestCtx, ownerID, id)
			if err != nil {
				_ = sse.Event(ctx, "error", gin.H{"message": err.Error()})
				return
			}
			if err := publishChanges(ctx, state, item, scenes); err != nil {
				return
			}
			if classroomSettled(item, scenes) {
				return
			}
		}
	}
}

// writeInitialFrames 打开流后先把当前状态整份发出去，前端不必等下一次变化才知道走到哪了。
//
// 事件名沿用旧的 classroom，只认它的老前端不受影响；scene.snapshot 一次给全页面进度，
// 替掉前端原来的定时轮询。
func writeInitialFrames(ctx *gin.Context, item *responsedto.Classroom, scenes []responsedto.ClassroomSceneSummary) error {
	if err := sse.Event(ctx, "classroom", item); err != nil {
		return err
	}
	if item.Status == entity.ClassroomStatusGenerating && len(scenes) == 0 {
		if err := sse.Event(ctx, "classroom.plan.started", gin.H{"status": item.Status}); err != nil {
			return err
		}
	}
	return sse.Event(ctx, "scene.snapshot", gin.H{"scenes": scenes})
}

// publishChanges 比对这次轮询到的状态与上次推出去的差异，只把变化写成事件。
func publishChanges(ctx *gin.Context, state *eventsState, item *responsedto.Classroom, scenes []responsedto.ClassroomSceneSummary) error {
	// 场景行从无到有说明大纲已经落库，这是「规划中」到「有课表」的分界。
	if !state.planPersisted && len(scenes) > 0 {
		state.planPersisted = true
		if err := sse.Event(ctx, "classroom.plan.completed", gin.H{"status": item.Status, "scene_count": len(scenes)}); err != nil {
			return err
		}
	}

	// 页面集合变了（大纲刚落库、或重新规划加删了页）就重发整份快照：
	// 单页事件表达不了「多出一页」，与其让前端自己拼，不如直接给一份完整的。
	if sceneSetChanged(state, scenes) {
		if err := sse.Event(ctx, "scene.snapshot", gin.H{"scenes": scenes}); err != nil {
			return err
		}
		state.scenes = indexScenes(scenes)
	} else {
		for _, scene := range scenes {
			previous, exists := state.scenes[scene.ID]
			state.scenes[scene.ID] = scene
			if !exists || (previous.Status == scene.Status && previous.Phase == scene.Phase) {
				continue
			}
			if err := publishSceneChange(ctx, previous, scene); err != nil {
				return err
			}
		}
	}

	if item.Status != state.classroomStatus {
		state.classroomStatus = item.Status
		return publishClassroomChange(ctx, item)
	}
	return nil
}

// sceneSetChanged 判断「有哪些页」是否与上次不同。
func sceneSetChanged(state *eventsState, scenes []responsedto.ClassroomSceneSummary) bool {
	if len(scenes) != len(state.scenes) {
		return true
	}
	for _, scene := range scenes {
		if _, ok := state.scenes[scene.ID]; !ok {
			return true
		}
	}
	return false
}

// publishSceneChange 按这一页这次进入的阶段决定事件名。
//
// 阶段没动而状态动了（例如直接从 generating 掉到 failed），就退回按状态取名。
func publishSceneChange(ctx *gin.Context, previous, current responsedto.ClassroomSceneSummary) error {
	name := ""
	if current.Phase != previous.Phase {
		name = scenePhaseEvent[current.Phase]
	}
	if name == "" {
		switch current.Status {
		case entity.SceneStatusReady:
			name = "scene.ready"
		case entity.SceneStatusFailed:
			name = "scene.failed"
		case entity.SceneStatusGenerating:
			name = "scene.started"
		}
	}
	if name == "" {
		return nil
	}
	return sse.Event(ctx, name, current)
}

// publishClassroomChange 把课堂状态变化翻成事件。
//
// 先补一帧 classroom 保住只认它的老前端，再按状态给一条语义化事件；generating 没有单独
// 事件名（受理时那一帧 classroom 就是它），所以只推前者。
func publishClassroomChange(ctx *gin.Context, item *responsedto.Classroom) error {
	if err := sse.Event(ctx, "classroom", item); err != nil {
		return err
	}
	name := ""
	switch item.Status {
	case entity.ClassroomStatusPlayable:
		name = "classroom.playable"
	case entity.ClassroomStatusReady:
		name = "classroom.ready"
	case entity.ClassroomStatusFailed:
		name = "classroom.failed"
	}
	if name == "" {
		return nil
	}
	return sse.Event(ctx, name, gin.H{"status": item.Status, "generation_error": item.GenerationError})
}

// classroomSettled 判断这门课是否已经不会再变，可以收流。
func classroomSettled(item *responsedto.Classroom, scenes []responsedto.ClassroomSceneSummary) bool {
	switch item.Status {
	case entity.ClassroomStatusReady, entity.ClassroomStatusFailed:
		return true
	case entity.ClassroomStatusPlayable:
		// 首页落库就会置 playable，此刻后面的页面还在生成，不能收流。
		if item.GenerationError != nil {
			return true
		}
		// 场景行还没建出来的那一瞬也放过，等它出现再判断。
		if len(scenes) == 0 {
			return false
		}
		for _, scene := range scenes {
			if scene.Status != entity.SceneStatusReady && scene.Status != entity.SceneStatusFailed {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func parseID(ctx *gin.Context) (uint64, bool) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(ctx, "无效的 ID")
		return 0, false
	}
	return id, true
}
