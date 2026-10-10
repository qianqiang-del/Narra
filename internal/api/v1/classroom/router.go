package classroom

import (
	"narra/internal/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(g *gin.RouterGroup, c *Controller) {
	protected := g.Group("", middleware.Auth())
	protected.POST("/classrooms", c.Create)
	protected.GET("/classrooms", c.List)
	protected.DELETE("/classrooms/:id", c.Delete)
	protected.GET("/classrooms/:id/events", c.Events)
	protected.GET("/classrooms/:id/outline", c.GetOutline)
	protected.GET("/classrooms/:id/agents", c.GetAgents)
	protected.GET("/classrooms/:id/scenes", c.ListScenes)
	protected.POST("/scenes/:id/retry", c.RetryScene)
	protected.GET("/classrooms/:id", c.Get)
}
