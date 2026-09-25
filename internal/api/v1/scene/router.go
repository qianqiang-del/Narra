package scene

import "github.com/gin-gonic/gin"

func RegisterRoutes(g *gin.RouterGroup, c *Controller) {
	g.GET("/scenes/:id/narration", c.Narration)
	g.GET("/scenes/:id/content", c.Content)
	g.GET("/scenes/:id", c.Detail)
}
