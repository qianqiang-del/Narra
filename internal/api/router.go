package api

import (
	"narra/internal/api/v1/role"
	"narra/internal/api/v1/voice"
	"narra/internal/middleware"
	"narra/internal/service"

	"github.com/gin-gonic/gin"
)

// Router 路由
type Router struct {
	roleCtrl  *role.Controller
	voiceCtrl *voice.Controller
}

// NewRouter 创建路由
func NewRouter(roleSvc service.RoleService, voiceSvc service.VoiceService) *Router {
	return &Router{
		roleCtrl:  role.NewController(roleSvc),
		voiceCtrl: voice.NewController(voiceSvc),
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

		role.RegisterRoutes(v1, r.roleCtrl)
		voice.RegisterRoutes(v1, r.voiceCtrl)
	}
}

// Close 关闭所有路由连接
func (r *Router) Close() error {
	return nil
}
