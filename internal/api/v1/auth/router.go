package auth

import (
	"narra/internal/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(g *gin.RouterGroup, c *Controller) {
	auth := g.Group("/auth")
	auth.POST("/codes", c.SendCode)
	auth.POST("/register", c.Register)
	auth.POST("/login/password", c.LoginPassword)
	auth.POST("/login/code", c.LoginCode)
	auth.GET("/me", middleware.Auth(), c.Me)
}
