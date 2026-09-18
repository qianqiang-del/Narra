package knowledge

import "github.com/gin-gonic/gin"

// RegisterRoutes 挂载知识库文档接口。
//
// 路径前缀 /knowledge/documents 是资源式的：一篇文档一个资源，上传是"创建文档"，
// 列表和详情是"读文档"。将来补删除、启停、重建切片都能挂在这一组下面，
// 不用再开新的前缀。
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
