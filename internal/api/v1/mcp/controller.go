package mcp

import (
	"strconv"

	"narra/internal/model/dto/request"
	"narra/internal/service"
	"narra/pkg/response"

	"github.com/gin-gonic/gin"
)

// Controller MCP 服务配置接口。
type Controller struct {
	svc service.MCPServerService
}

// NewController 创建 MCP 服务配置控制器。
func NewController(svc service.MCPServerService) *Controller {
	return &Controller{svc: svc}
}

// List 获取 MCP 服务列表。
//
//	GET /api/v1/mcp/servers
func (c *Controller) List(ctx *gin.Context) {
	items, err := c.svc.List(ctx.Request.Context())
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, items)
}

// Create 创建 MCP 服务。
//
//	POST /api/v1/mcp/servers
func (c *Controller) Create(ctx *gin.Context) {
	var input request.MCPServer
	if err := ctx.ShouldBindJSON(&input); err != nil {
		response.BadRequest(ctx, "请求参数错误: "+err.Error())
		return
	}
	item, err := c.svc.Create(ctx.Request.Context(), input)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, item)
}

// Update 部分更新 MCP 服务。
//
//	PATCH /api/v1/mcp/servers/:id
func (c *Controller) Update(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(ctx, "无效的 ID")
		return
	}
	var input request.MCPServerUpdate
	if err := ctx.ShouldBindJSON(&input); err != nil {
		response.BadRequest(ctx, "请求参数错误: "+err.Error())
		return
	}
	item, err := c.svc.Update(ctx.Request.Context(), id, input)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, item)
}

// Delete 删除 MCP 服务。
//
//	DELETE /api/v1/mcp/servers/:id
func (c *Controller) Delete(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(ctx, "无效的 ID")
		return
	}
	if err := c.svc.Delete(ctx.Request.Context(), id); err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, nil)
}

// Test 测试 MCP 服务连接。
//
//	POST /api/v1/mcp/servers/:id/test
func (c *Controller) Test(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(ctx, "无效的 ID")
		return
	}
	result, err := c.svc.Test(ctx.Request.Context(), id)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, result)
}
