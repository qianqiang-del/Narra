package conversation

import "github.com/gin-gonic/gin"

// RegisterRoutes 挂载对话事件流。
//
// 对话创建、列表和消息读取使用普通 JSON；事件流挂在对话资源下面，
// 与文档收录进度（/knowledge/documents/:id/events）采用同一种资源关系。
func RegisterRoutes(g *gin.RouterGroup, c *Controller) {
	g.GET("/classrooms/:id/conversations", c.List)
	g.POST("/classrooms/:id/conversations", c.Create)
	g.GET("/conversations/:id/events", c.Events)
	g.GET("/conversations/:id/messages", c.ListMessages)
}
