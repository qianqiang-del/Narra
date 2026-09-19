package api

import (
	"narra/internal/api/v1/embedding"
	knowledgev1 "narra/internal/api/v1/knowledge"
	"narra/internal/api/v1/llm"
	mcpv1 "narra/internal/api/v1/mcp"
	"narra/internal/api/v1/role"
	"narra/internal/api/v1/voice"
	"narra/internal/middleware"
	"narra/internal/service"
	"narra/pkg/documentparser"

	"github.com/gin-gonic/gin"
)

// Router 路由
type Router struct {
	roleCtrl      *role.Controller
	embeddingCtrl *embedding.Controller
	voiceCtrl     *voice.Controller
	mcpCtrl       *mcpv1.Controller
	llmCtrl       *llm.Controller
	knowledgeCtrl *knowledgev1.Controller
}

// NewRouter 创建路由
func NewRouter(roleSvc service.RoleService, embeddingSvc service.EmbeddingSettingService, voiceSvc service.VoiceService, mcpSvc service.MCPServerService, llmSvc service.LLMProviderService, knowledgeSvc service.KnowledgeService, uploadDir string, parser documentparser.Parser) *Router {
	return &Router{
		roleCtrl:      role.NewController(roleSvc),
		embeddingCtrl: embedding.NewController(embeddingSvc),
		voiceCtrl:     voice.NewController(voiceSvc),
		mcpCtrl:       mcpv1.NewController(mcpSvc),
		llmCtrl:       llm.NewController(llmSvc),
		knowledgeCtrl: knowledgev1.NewController(knowledgeSvc, uploadDir, parser),
	}
}

// Setup 设置路由
func (r *Router) Setup(engine *gin.Engine) {
	// 全局中间件
	engine.Use(middleware.Recovery())
	engine.Use(middleware.Logger())
	engine.Use(middleware.RequestLogger())
	engine.Use(middleware.CORS())

	// API 路由组
	v1 := engine.Group("/api/v1")
	{
		// 健康检查：不走统一响应体，运维探活只看 HTTP 200。
		v1.GET("/health", func(c *gin.Context) {
			c.JSON(200, gin.H{
				"status":  "ok",
				"message": "Narra API is running",
			})
		})
		// API 路由组
		role.RegisterRoutes(v1, r.roleCtrl)
		voice.RegisterRoutes(v1, r.voiceCtrl)
		embedding.RegisterRoutes(v1, r.embeddingCtrl)
		mcpv1.RegisterRoutes(v1, r.mcpCtrl)
		llm.RegisterRoutes(v1, r.llmCtrl)
		knowledgev1.RegisterRoutes(v1, r.knowledgeCtrl)
	}
}

// Close 关闭所有路由连接
func (r *Router) Close() error {
	return nil
}
