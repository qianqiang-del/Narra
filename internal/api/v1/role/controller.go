package role

import (
	"narra/internal/service"
	"narra/pkg/response"

	"github.com/gin-gonic/gin"
)

// Controller 角色池接口。
type Controller struct {
	svc service.RoleService
}

// NewController 创建角色池控制器。
func NewController(svc service.RoleService) *Controller {
	return &Controller{svc: svc}
}

// List 获取角色池列表。
//
//	GET /api/v1/roles
//
// 控制器只做两件事：把请求上下文交给业务层，把结果写成响应。不拼字段、不碰数据库。
func (c *Controller) List(ctx *gin.Context) {
	items, err := c.svc.List(ctx.Request.Context())
	if err != nil {
		response.BizError(ctx, err)
		return
	}

	response.Success(ctx, items)
}
