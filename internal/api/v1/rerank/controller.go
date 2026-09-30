package rerank

import (
	"strconv"

	"github.com/gin-gonic/gin"

	requestdto "narra/internal/model/dto/request"
	"narra/internal/service"
	"narra/pkg/response"
)

// Controller 是重排配置的 HTTP 处理器；与 api/v1/llm 的控制器同形。
type Controller struct{ svc service.RerankSettingService }

func NewController(svc service.RerankSettingService) *Controller { return &Controller{svc: svc} }

func (c *Controller) List(ctx *gin.Context) {
	items, err := c.svc.List(ctx.Request.Context())
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, items)
}

func (c *Controller) Create(ctx *gin.Context) {
	var input requestdto.RerankSetting
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

func (c *Controller) Update(ctx *gin.Context) {
	id, ok := parseID(ctx)
	if !ok {
		return
	}
	var input requestdto.RerankSetting
	if err := ctx.ShouldBindJSON(&input); err != nil {
		response.BadRequest(ctx, "请求参数错误")
		return
	}
	item, err := c.svc.Update(ctx.Request.Context(), id, input)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, item)
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
	response.Success(ctx, nil)
}

func (c *Controller) Test(ctx *gin.Context) {
	id, ok := parseID(ctx)
	if !ok {
		return
	}
	result, err := c.svc.Test(ctx.Request.Context(), id)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, result)
}

func (c *Controller) SetEnabled(ctx *gin.Context) {
	id, ok := parseID(ctx)
	if !ok {
		return
	}
	var input requestdto.RerankSettingEnabled
	if err := ctx.ShouldBindJSON(&input); err != nil {
		response.BadRequest(ctx, "请求参数错误")
		return
	}
	item, err := c.svc.SetEnabled(ctx.Request.Context(), id, input.Enabled)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, item)
}

func parseID(ctx *gin.Context) (uint64, bool) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(ctx, "无效的 ID")
		return 0, false
	}
	return id, true
}
