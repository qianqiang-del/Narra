package embedding

import "github.com/gin-gonic/gin"

// RegisterRoutes 保持设置页现有接口路径，后续扩展多配置列表时可继续挂在此分组。
func RegisterRoutes(g *gin.RouterGroup, c *Controller) {
	settings := g.Group("/settings/embedding")
	settings.GET("", c.Current)
	settings.PUT("", c.Save)
	settings.POST("/test", c.Test)
}
