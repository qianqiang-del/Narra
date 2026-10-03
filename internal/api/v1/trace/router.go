package trace

import "github.com/gin-gonic/gin"

func RegisterRoutes(g *gin.RouterGroup, c *Controller) {
	g.GET("/conversations/:id/runs", c.ListRuns)
	g.GET("/conversations/:id/runs/:runId/trace", c.GetTrace)
}
