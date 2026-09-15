package role

import "github.com/gin-gonic/gin"

// RegisterRoutes 把角色池的路由挂到给定的路由组上。
//
// 只接收 *gin.RouterGroup 而不是 *gin.Engine：这样它不关心自己挂在 /api/v1 还是
// 别处，将来换前缀、加版本都不用改这里。
func RegisterRoutes(g *gin.RouterGroup, c *Controller) {
	g.GET("/roles", c.List)
}
