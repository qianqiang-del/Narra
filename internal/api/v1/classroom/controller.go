package classroom

import (
	"encoding/json"
	"strconv"
	"time"

	requestdto "narra/internal/model/dto/request"
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
	item, err := c.svc.Create(ctx.Request.Context(), input)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, item)
}

func (c *Controller) List(ctx *gin.Context) {
	items, err := c.svc.List(ctx.Request.Context())
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
	if err := c.svc.Delete(ctx.Request.Context(), id); err != nil {
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
	item, err := c.svc.Get(ctx.Request.Context(), id)
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
	item, err := c.svc.GetOutline(ctx.Request.Context(), id)
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
	items, err := c.svc.GetAgents(ctx.Request.Context(), id)
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
	items, err := c.svc.ListScenes(ctx.Request.Context(), id)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, items)
}

// Events 推送课堂大纲阶段的状态变化。生成结果已经落库，SSE 只负责通知状态。
func (c *Controller) Events(ctx *gin.Context) {
	id, ok := parseID(ctx)
	if !ok {
		return
	}
	requestCtx := ctx.Request.Context()
	item, err := c.svc.Get(requestCtx, id)
	if err != nil {
		response.BizError(ctx, err)
		return
	}

	sse.Start(ctx)
	lastStatus := item.Status
	if err := sse.Event(ctx, "classroom", item); err != nil {
		return
	}
	if item.Status == "playable" || item.Status == "ready" || item.Status == "failed" {
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
			item, err := c.svc.Get(requestCtx, id)
			if err != nil {
				_ = sse.Event(ctx, "error", gin.H{"message": err.Error()})
				return
			}
			if item.Status == lastStatus {
				continue
			}
			lastStatus = item.Status
			payload, marshalErr := json.Marshal(item)
			if marshalErr != nil || sse.EventJSON(ctx, "classroom", payload) != nil {
				return
			}
			if item.Status == "playable" || item.Status == "ready" || item.Status == "failed" {
				return
			}
		}
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
