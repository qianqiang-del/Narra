package llm

import (
	"narra/internal/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(g *gin.RouterGroup, c *Controller) {
	settings := g.Group("/settings/llm/providers", middleware.Auth())
	settings.GET("", c.List)
	settings.POST("", c.Create)
	settings.PUT("/:id", c.Update)
	settings.DELETE("/:id", c.Delete)
	settings.POST("/:id/test", c.Test)
	settings.POST("/:id/pricing/suggestions", c.SuggestPricing)
	settings.PATCH("/:id/enabled", c.SetEnabled)

	g.GET("/llm/models/available", middleware.Auth(), c.AvailableModels)
}
