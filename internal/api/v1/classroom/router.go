package classroom

import "github.com/gin-gonic/gin"

func RegisterRoutes(g *gin.RouterGroup, c *Controller) {
	g.POST("/classrooms", c.Create)
	g.GET("/classrooms", c.List)
	g.DELETE("/classrooms/:id", c.Delete)
	g.GET("/classrooms/:id/events", c.Events)
	g.GET("/classrooms/:id/outline", c.GetOutline)
	g.GET("/classrooms/:id/agents", c.GetAgents)
	g.GET("/classrooms/:id/scenes", c.ListScenes)
	g.GET("/classrooms/:id", c.Get)
}
