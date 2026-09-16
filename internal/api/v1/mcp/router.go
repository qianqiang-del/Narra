package mcp

import "github.com/gin-gonic/gin"

// RegisterRoutes 把 MCP 服务配置的路由挂到给定的路由组上。
func RegisterRoutes(g *gin.RouterGroup, c *Controller) {
	servers := g.Group("/mcp/servers")
	{
		servers.GET("", c.List)
		servers.POST("", c.Create)
		servers.PATCH("/:id", c.Update)
		servers.DELETE("/:id", c.Delete)
		servers.POST("/:id/test", c.Test)
	}
}
