package middleware

import (
	"net/http"
	"strconv"
	"strings"

	"narra/pkg/config"

	"github.com/gin-gonic/gin"
)

// CORS 跨域中间件。
//
// 部署约定：前端与 API 同域（nginx 反代 / vite 代理）时本就不需要跨域，
// 配置里 enabled 关掉即可；只有浏览器跨域直连 API 的场景才需要开启，
// 并把 allow_origins 配成真实域名。
//
// 开启时各响应头严格按配置生成：
//   - 只对命中 allow_origins 的 Origin 回 Access-Control-Allow-Origin，并附加
//     Vary: Origin，防止 CDN / 反向代理按 URL 缓存串出错误的跨域头；
//   - allow_origins 含 "*" 且启用凭证时，回具体 Origin —— 浏览器规范不接受
//     "*" 与凭证的组合，回 "*" 会被直接拒绝；
//   - allow_credentials / expose_headers / max_age 都取配置值，不再写死。
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg := config.Get().CORS

		// 如果未启用 CORS，直接放行
		if !cfg.Enabled {
			c.Next()
			return
		}

		origin := c.Request.Header.Get("Origin")
		if allowed := matchOrigin(cfg.AllowOrigins, origin); allowed != "" {
			if allowed == "*" && cfg.AllowCredentials {
				allowed = origin
			}
			c.Writer.Header().Set("Access-Control-Allow-Origin", allowed)
			c.Writer.Header().Add("Vary", "Origin")
		}

		if len(cfg.AllowMethods) > 0 {
			c.Writer.Header().Set("Access-Control-Allow-Methods", strings.Join(cfg.AllowMethods, ", "))
		}
		if len(cfg.AllowHeaders) > 0 {
			c.Writer.Header().Set("Access-Control-Allow-Headers", strings.Join(cfg.AllowHeaders, ", "))
		}
		if cfg.AllowCredentials {
			c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		if len(cfg.ExposeHeaders) > 0 {
			c.Writer.Header().Set("Access-Control-Expose-Headers", strings.Join(cfg.ExposeHeaders, ", "))
		}
		if cfg.MaxAge > 0 {
			c.Writer.Header().Set("Access-Control-Max-Age", strconv.Itoa(cfg.MaxAge))
		}

		// 只有真正的 CORS 预检（浏览器会带 Access-Control-Request-Method）才短路，
		// 其它 OPTIONS 请求交还给路由处理，避免吞掉正常接口。
		if c.Request.Method == http.MethodOptions && c.Request.Header.Get("Access-Control-Request-Method") != "" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

// matchOrigin 在允许列表中匹配请求 Origin，支持 "*" 通配与精确匹配，返回命中的模式；
// 未命中或 Origin 为空（非跨域的普通请求）返回空串。
func matchOrigin(allowOrigins []string, origin string) string {
	if origin == "" {
		return ""
	}
	for _, allowed := range allowOrigins {
		if allowed == "*" || allowed == origin {
			return allowed
		}
	}
	return ""
}
