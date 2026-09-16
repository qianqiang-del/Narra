package embedding

import (
	requestdto "narra/internal/model/dto/request"
	"narra/internal/service"
	"narra/pkg/response"

	"github.com/gin-gonic/gin"
)

// Controller 负责 Embedding 配置接口。
type Controller struct {
	svc service.EmbeddingSettingService
}

func NewController(svc service.EmbeddingSettingService) *Controller {
	return &Controller{svc: svc}
}

// Current 返回当前正在使用的配置。
func (c *Controller) Current(ctx *gin.Context) {
	setting, err := c.svc.Current(ctx.Request.Context())
	if err != nil {
		response.InternalError(ctx, err.Error())
		return
	}
	response.Success(ctx, setting)
}

// Save 保存当前配置并将其设为唯一启用的配置。
func (c *Controller) Save(ctx *gin.Context) {
	var input requestdto.EmbeddingSetting
	if err := ctx.ShouldBindJSON(&input); err != nil {
		response.BadRequest(ctx, "向量服务配置格式无效")
		return
	}
	setting, err := c.svc.Save(ctx.Request.Context(), input)
	if err != nil {
		response.BadRequest(ctx, err.Error())
		return
	}
	response.Success(ctx, setting)
}

// Test 使用提交的配置测试一次 Embedding 请求，不修改已保存的配置。
func (c *Controller) Test(ctx *gin.Context) {
	var input requestdto.EmbeddingSetting
	if err := ctx.ShouldBindJSON(&input); err != nil {
		response.BadRequest(ctx, "向量服务配置格式无效")
		return
	}
	dimensions, err := c.svc.Test(ctx.Request.Context(), input)
	if err != nil {
		response.BadRequest(ctx, err.Error())
		return
	}
	response.SuccessWithMessage(ctx, "Embedding 服务连接成功", gin.H{"dimensions": dimensions})
}
