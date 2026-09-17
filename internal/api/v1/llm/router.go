package llm

import "github.com/gin-gonic/gin"

func RegisterRoutes(g *gin.RouterGroup, c *Controller) {
	settings := g.Group("/settings/llm/providers")
	settings.GET("", c.List)
	settings.POST("", c.Create)
	settings.PUT("/:id", c.Update)
	settings.DELETE("/:id", c.Delete)
	settings.POST("/:id/test", c.Test)
	settings.PATCH("/:id/enabled", c.SetEnabled)

	g.GET("/llm/models/available", c.AvailableModels)
}
