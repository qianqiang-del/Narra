package rerank

import (
	"github.com/gin-gonic/gin"
	"narra/internal/middleware"
)

// RegisterRoutes 挂载重排配置的读写路由。
// 与 /settings/llm/providers 同形：列表、增、改、删、测试、启停。
func RegisterRoutes(g *gin.RouterGroup, c *Controller) {
	settings := g.Group("/settings/rerank/models", middleware.Auth())
	settings.GET("", c.List)
	settings.POST("", c.Create)
	settings.PUT("/:id", c.Update)
	settings.DELETE("/:id", c.Delete)
	settings.POST("/:id/test", c.Test)
	settings.PATCH("/:id/enabled", c.SetEnabled)
}
