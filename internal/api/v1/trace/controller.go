package trace

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"narra/internal/service"
	"narra/pkg/response"
)

type Controller struct{ svc service.TraceService }

func NewController(svc service.TraceService) *Controller { return &Controller{svc: svc} }

func (c *Controller) ListRuns(ctx *gin.Context) {
	conversationID, ok := parseID(ctx, "id")
	if !ok {
		return
	}
	limit := 0
	if raw := ctx.Query("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			response.BadRequest(ctx, "limit 参数无效")
			return
		}
		limit = parsed
	}
	items, err := c.svc.ListRuns(ctx.Request.Context(), conversationID, limit)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, items)
}

func (c *Controller) GetTrace(ctx *gin.Context) {
	conversationID, ok := parseID(ctx, "id")
	if !ok {
		return
	}
	runID, ok := parseID(ctx, "runId")
	if !ok {
		return
	}
	item, err := c.svc.GetTrace(ctx.Request.Context(), conversationID, runID)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, item)
}

func parseID(ctx *gin.Context, name string) (uint64, bool) {
	id, err := strconv.ParseUint(ctx.Param(name), 10, 64)
	if err != nil || id == 0 {
		response.BadRequest(ctx, "ID 无效")
		return 0, false
	}
	return id, true
}
