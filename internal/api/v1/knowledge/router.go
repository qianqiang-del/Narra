package knowledge

import "github.com/gin-gonic/gin"

// RegisterRoutes 挂载知识库文档接口。
// /knowledge/upload-records 是**另一组资源**，不是文档的子资源：它记的是"投递"这个动作
// （谁在什么时候传了什么文件），而文档是那次投递的成果。两者弱关联、各自删除 ——
// 删记录不动已收录的文档，删文档也不会让记录消失（那条记录会显示成"已收录后删除"）。
//
// 注意 /parser/status 与 /:id/preview 都是两段路径：gin 的路由树里静态段优先于参数段，
// 所以 "parser" 不会被当成一个文档 ID，两个接口可以共存，注册顺序也不影响。
func RegisterRoutes(g *gin.RouterGroup, c *Controller) {
	documents := g.Group("/knowledge/documents")
	documents.POST("", c.Upload)
	documents.POST("/text", c.IngestText)
	// 重试是"让这一篇再跑一遍"，所以它是文档的子动作，不是新的一次上传（POST 而非 PUT）：
	// 收的是状态流转，不是内容。路由段与 /:id/preview 同一形状，能共存。
	documents.POST("/:id/retry", c.Retry)
	documents.GET("", c.List)
	documents.GET("/parser/status", c.ParserStatus)
	// 收录进度走 SSE：上传/重试返回后前端订阅这条流，直到文档到终态。
	// 它挂在文档下面而不是另开一组资源 —— 推的就是这一篇的状态变化。
	documents.GET("/:id/events", c.Events)
	documents.GET("/:id/preview", c.Preview)
	documents.DELETE("/:id", c.Delete)
	documents.GET("/:id", c.Get)

	records := g.Group("/knowledge/upload-records")
	records.GET("", c.ListUploadRecords)
	records.DELETE("/:id", c.DeleteUploadRecord)

	// 检索是第三组资源，与上面两组并列（不进 /knowledge/documents）：它一次跨整库
	// 召回一批切片，命中的不是某一篇文档，塞进文档组会让"文档的子资源"这个语义说不清。
	g.POST("/knowledge/retrieve", c.Retrieve)
}
