package folder

import (
	"github.com/gin-gonic/gin"
	"narra/internal/middleware"
)

func RegisterRoutes(g *gin.RouterGroup, c *Controller) {
	folders := g.Group("/folders", middleware.Auth())
	{
		folders.GET("", c.List)
		folders.POST("", c.Create)
		folders.GET("/:id", c.Get)
		folders.PATCH("/:id", c.Rename)
		folders.DELETE("/:id", c.Delete)
		folders.PUT("/:id/classrooms/:classroom_id", c.AddClassroom)
		folders.DELETE("/:id/classrooms/:classroom_id", c.RemoveClassroom)
	}
}
