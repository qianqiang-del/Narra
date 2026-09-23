package conversation

import "github.com/gin-gonic/gin"

// RegisterRoutes 挂载对话事件流。
//
// 只有这一条路由：对话本身的创建、列表、关闭属于编排与工作台链路，等那条链路
// 落地时再往这组下面加。事件流挂在对话这一资源下面 —— 推的是这条对话里发生的事，
// 与文档的收录进度（/knowledge/documents/:id/events）是同一种资源关系。
func RegisterRoutes(g *gin.RouterGroup, c *Controller) {
	g.GET("/conversations/:id/events", c.Events)
}
