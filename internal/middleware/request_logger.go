package middleware

import (
	"bytes"
	"io"
	"strings"
	"time"

	"narra/pkg/logger"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// maxBodyLogBytes 日志里最多截取的请求体字节数。
// 日志是排障辅助，不需要完整 body，更不该让大请求整体进内存。
const maxBodyLogBytes = 1000

// sensitiveBodyPrefixes 命中的路径不记录请求体：这些接口的请求体携带密钥
// （模型 / 向量 / 重排 / 视觉 / MCP 的配置）或登录凭据，明文落盘等于泄密。
var sensitiveBodyPrefixes = []string{
	"/api/v1/settings/",
	"/api/v1/mcp/",
	"/api/v1/auth/", // 登录注册（开发中）：密码、令牌不能进日志
}

// RequestLogger 请求日志中间件
func RequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		body := captureBodyForLog(c)

		c.Next()

		end := time.Now()
		latency := end.Sub(start)

		logger.Info("HTTP 请求",
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.String("query", query),
			zap.String("body", body),
			zap.Int("status", c.Writer.Status()),
			zap.String("ip", c.ClientIP()),
			zap.String("user_agent", c.Request.UserAgent()),
			zap.Duration("latency", latency),
		)
	}
}

// captureBodyForLog 截取一小段请求体用于日志，并把请求体完整还原给后续处理。
//
// 三个要点：
//   - 只读前 maxBodyLogBytes 字节：此前 io.ReadAll 不设上限，大 JSON 会整体进内存；
//   - 还原用"已读部分 + 剩余流"拼接，下游 handler 收到的 body 与中间件不介入时一致；
//   - multipart 判断用前缀匹配：真实 Content-Type 形如
//     "multipart/form-data; boundary=..."，此前的全等比较永远为假，上传请求会被整体读入。
func captureBodyForLog(c *gin.Context) string {
	if c.Request.Body == nil || (c.Request.Method != "POST" && c.Request.Method != "PUT") {
		return ""
	}

	for _, prefix := range sensitiveBodyPrefixes {
		if strings.HasPrefix(c.Request.URL.Path, prefix) {
			return "[已脱敏]"
		}
	}

	if strings.HasPrefix(c.GetHeader("Content-Type"), "multipart/form-data") {
		return "[文件上传]"
	}

	orig := c.Request.Body
	buf, err := io.ReadAll(io.LimitReader(orig, maxBodyLogBytes+1))
	// 先还回已读部分，再拼上剩余流，保证 handler 拿到完整 body
	c.Request.Body = io.NopCloser(io.MultiReader(bytes.NewReader(buf), orig))
	if err != nil {
		return ""
	}
	if len(buf) > maxBodyLogBytes {
		return string(buf[:maxBodyLogBytes]) + "..."
	}
	return string(buf)
}
