package vlm

import (
	"narra/internal/middleware"
	"strconv"

	"github.com/gin-gonic/gin"

	requestdto "narra/internal/model/dto/request"
	"narra/internal/service"
	"narra/pkg/response"
)

// Controller 是视觉模型（VLM）配置的 HTTP 处理器；与 api/v1/rerank 的控制器同形。
type Controller struct{ svc service.VLMSettingService }

func NewController(svc service.VLMSettingService) *Controller { return &Controller{svc: svc} }

func (c *Controller) List(ctx *gin.Context) {
	items, err := c.svc.List(ctx.Request.Context(), middleware.GetUserID(ctx))
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, items)
}

func (c *Controller) Create(ctx *gin.Context) {
	var input requestdto.VLMSetting
	if err := ctx.ShouldBindJSON(&input); err != nil {
		response.BadRequest(ctx, "请求参数错误")
		return
	}
	item, err := c.svc.Create(ctx.Request.Context(), input, middleware.GetUserID(ctx))
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
	var input requestdto.VLMSetting
	if err := ctx.ShouldBindJSON(&input); err != nil {
		response.BadRequest(ctx, "请求参数错误")
		return
	}
	item, err := c.svc.Update(ctx.Request.Context(), id, input, middleware.GetUserID(ctx))
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
	if err := c.svc.Delete(ctx.Request.Context(), id, middleware.GetUserID(ctx)); err != nil {
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
	result, err := c.svc.Test(ctx.Request.Context(), id, middleware.GetUserID(ctx))
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, result)
}

// TestConnection 用表单里还没保存的值发一次探测：设置页的"测试连接"按钮走这里，
// 不写任何状态；失败按 400 返回，前端把原因显示在表单里。
func (c *Controller) TestConnection(ctx *gin.Context) {
	var input requestdto.VLMSettingProbe
	if err := ctx.ShouldBindJSON(&input); err != nil {
		response.BadRequest(ctx, "请求参数错误")
		return
	}
	result, err := c.svc.TestConnection(ctx.Request.Context(), input, middleware.GetUserID(ctx))
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
	var input requestdto.VLMSettingEnabled
	if err := ctx.ShouldBindJSON(&input); err != nil {
		response.BadRequest(ctx, "请求参数错误")
		return
	}
	item, err := c.svc.SetEnabled(ctx.Request.Context(), id, input.Enabled, middleware.GetUserID(ctx))
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
