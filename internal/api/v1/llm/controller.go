package llm

import (
	"narra/internal/middleware"
	requestdto "narra/internal/model/dto/request"
	"narra/internal/service"
	"narra/pkg/response"
	"strconv"

	"github.com/gin-gonic/gin"
)

type Controller struct{ svc service.LLMProviderService }

func NewController(svc service.LLMProviderService) *Controller { return &Controller{svc: svc} }

func (c *Controller) List(ctx *gin.Context) {
	items, err := c.svc.List(ctx.Request.Context(), middleware.GetUserID(ctx))
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, items)
}

func (c *Controller) AvailableModels(ctx *gin.Context) {
	items, err := c.svc.AvailableModelsForOwner(ctx.Request.Context(), middleware.GetUserID(ctx))
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, items)
}

func (c *Controller) Create(ctx *gin.Context) {
	var input requestdto.LLMProvider
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

func (c *Controller) Update(ctx *gin.Context) {
	id, ok := parseID(ctx)
	if !ok {
		return
	}
	var input requestdto.LLMProvider
	if err := ctx.ShouldBindJSON(&input); err != nil {
		response.BadRequest(ctx, "请求参数错误")
		return
	}
	item, err := c.svc.Update(ctx.Request.Context(), middleware.GetUserID(ctx), id, input)
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
	if err := c.svc.Delete(ctx.Request.Context(), middleware.GetUserID(ctx), id); err != nil {
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
	result, err := c.svc.Test(ctx.Request.Context(), middleware.GetUserID(ctx), id)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, result)
}

func (c *Controller) SuggestPricing(ctx *gin.Context) {
	id, ok := parseID(ctx)
	if !ok {
		return
	}
	var input requestdto.LLMPriceSuggestion
	if err := ctx.ShouldBindJSON(&input); err != nil {
		response.BadRequest(ctx, "请求参数错误")
		return
	}
	item, err := c.svc.SuggestPricing(ctx.Request.Context(), middleware.GetUserID(ctx), id, input.ModelID)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, item)
}

func (c *Controller) SetEnabled(ctx *gin.Context) {
	id, ok := parseID(ctx)
	if !ok {
		return
	}
	var input requestdto.LLMProviderEnabled
	if err := ctx.ShouldBindJSON(&input); err != nil {
		response.BadRequest(ctx, "请求参数错误")
		return
	}
	item, err := c.svc.SetEnabled(ctx.Request.Context(), middleware.GetUserID(ctx), id, input.Enabled)
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
