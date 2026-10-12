package trace

import (
	"github.com/gin-gonic/gin"
	"narra/internal/middleware"
)

func RegisterRoutes(g *gin.RouterGroup, c *Controller) {
	g = g.Group("", middleware.Auth())
	g.GET("/conversations/:id/runs", c.ListRuns)
	g.GET("/conversations/:id/runs/:runId/trace", c.GetTrace)
}
