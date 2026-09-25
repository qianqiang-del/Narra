package scene

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"narra/internal/service"
	"narra/pkg/response"
)

type Controller struct{ svc service.SceneService }

func NewController(svc service.SceneService) *Controller { return &Controller{svc: svc} }

func (c *Controller) Narration(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 {
		response.BadRequest(ctx, "场景 ID 无效")
		return
	}
	items, err := c.svc.ListNarration(ctx.Request.Context(), id)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, items)
}

func (c *Controller) Content(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 {
		response.BadRequest(ctx, "场景 ID 无效")
		return
	}
	item, err := c.svc.GetContent(ctx.Request.Context(), id)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, item)
}

func (c *Controller) Detail(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 {
		response.BadRequest(ctx, "场景 ID 无效")
		return
	}
	item, err := c.svc.Get(ctx.Request.Context(), id)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, item)
}
