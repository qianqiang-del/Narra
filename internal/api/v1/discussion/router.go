package discussion

import "github.com/gin-gonic/gin"

// RegisterRoutes 挂载讨论触发入口。
//
// 只有这一条：发一条消息、开一趟讨论。讨论的**过程**（谁开始说话了、说了什么、
// 什么时候结束）不在这里 —— 它走 GET /conversations/:id/events 那条事件流，
// 由 SSE 那一层按序号读出来推给前端（先落库、再推送）。
//
// 路径挂在 conversations 下面而不是单开一个 resources 根：讨论是从属于一条对话的一次活动，
// 与事件流挂同一个资源下是同一种资源关系。代码放在自己的包里，是为了让这块的改动
// 不与 SSE 那条链路互相牵扯。
func RegisterRoutes(g *gin.RouterGroup, c *Controller) {
	g.POST("/conversations/:id/discussions", c.Start)
}
