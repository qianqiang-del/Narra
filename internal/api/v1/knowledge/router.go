package knowledge

import "github.com/gin-gonic/gin"

// RegisterRoutes 挂载知识库文档接口。
//
// 路径前缀 /knowledge/documents 是资源式的：一篇文档一个资源，上传是"创建文档"，
// 列表与详情是"读文档"，删除是"删文档"，预览是它的子资源。启停、重建切片这类
// 后续能力也挂在这一组下面，不用再开新前缀。
//
// 注意 /parser/status 与 /:id/preview 都是两段路径：gin 的路由树里静态段优先于参数段，
// 所以 "parser" 不会被当成一个文档 ID，两个接口可以共存，注册顺序也不影响。
func RegisterRoutes(g *gin.RouterGroup, c *Controller) {
	documents := g.Group("/knowledge/documents")
	documents.POST("", c.Upload)
	documents.POST("/text", c.IngestText)
	documents.GET("", c.List)
	documents.GET("/parser/status", c.ParserStatus)
	documents.GET("/:id/preview", c.Preview)
	documents.DELETE("/:id", c.Delete)
	documents.GET("/:id", c.Get)
}
