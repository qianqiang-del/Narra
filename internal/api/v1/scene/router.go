package scene

import (
	"github.com/gin-gonic/gin"
	"narra/internal/middleware"
)

func RegisterRoutes(g *gin.RouterGroup, c *Controller) {
	g = g.Group("", middleware.Auth())
	g.GET("/scenes/:id/narration", c.Narration)
	g.GET("/scenes/:id/content", c.Content)
	g.GET("/scenes/:id", c.Detail)
}
