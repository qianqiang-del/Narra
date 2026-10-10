package vlm

import (
	"github.com/gin-gonic/gin"
	"narra/internal/middleware"
)

// RegisterRoutes 挂载视觉模型（VLM）配置的读写路由。
// 与 /settings/rerank/models 同形：列表、增、改、删、测试、启停；
// 另有不落库的"测试连接"（表单保存前试跑）。
func RegisterRoutes(g *gin.RouterGroup, c *Controller) {
	settings := g.Group("/settings/vlm", middleware.Auth())
	settings.POST("/test", c.TestConnection)

	models := settings.Group("/models")
	models.GET("", c.List)
	models.POST("", c.Create)
	models.PUT("/:id", c.Update)
	models.DELETE("/:id", c.Delete)
	models.POST("/:id/test", c.Test)
	models.PATCH("/:id/enabled", c.SetEnabled)
}
