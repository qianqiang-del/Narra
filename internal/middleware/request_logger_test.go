package middleware

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func newTestContext(method, path, contentType, body string) *gin.Context {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	c.Request = req
	return c
}

// 超长 body 应截断进日志，但还原给下游的必须是完整原文。
func TestCaptureBodyForLog_RestoresFullBody(t *testing.T) {
	long := strings.Repeat("a", maxBodyLogBytes*3)
	c := newTestContext("POST", "/api/v1/classrooms", "application/json", long)

	got := captureBodyForLog(c)
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("超出上限的 body 应被截断并以省略号结尾, got %q", got)
	}
	restored, err := io.ReadAll(c.Request.Body)
	if err != nil {
		t.Fatalf("读还原后的 body 失败: %v", err)
	}
	if string(restored) != long {
		t.Fatalf("还原后的 body 与原始不一致: len=%d, want len=%d", len(restored), len(long))
	}
}

func TestCaptureBodyForLog_ShortBody(t *testing.T) {
	c := newTestContext("POST", "/api/v1/classrooms", "application/json", `{"a":1}`)

	if got := captureBodyForLog(c); got != `{"a":1}` {
		t.Fatalf("短 body 应原样返回, got %q", got)
	}
	restored, _ := io.ReadAll(c.Request.Body)
	if string(restored) != `{"a":1}` {
		t.Fatalf("还原失败: %q", restored)
	}
}

// 敏感路径不记录 body，但也必须把原文留给下游 handler。
func TestCaptureBodyForLog_SensitivePath(t *testing.T) {
	c := newTestContext("PUT", "/api/v1/settings/llm/providers/1", "application/json", `{"api_key":"sk-secret"}`)

	if got := captureBodyForLog(c); got != "[已脱敏]" {
		t.Fatalf("敏感路径应脱敏, got %q", got)
	}
	restored, _ := io.ReadAll(c.Request.Body)
	if string(restored) != `{"api_key":"sk-secret"}` {
		t.Fatalf("脱敏不得影响下游读取: %q", restored)
	}
}

func TestCaptureBodyForLog_Multipart(t *testing.T) {
	c := newTestContext("POST", "/api/v1/knowledge/documents", "multipart/form-data; boundary=xyz", "ignored")

	if got := captureBodyForLog(c); got != "[文件上传]" {
		t.Fatalf("multipart 应标记为文件上传, got %q", got)
	}
}

func TestCaptureBodyForLog_GetNotRead(t *testing.T) {
	c := newTestContext("GET", "/api/v1/classrooms", "", "ignored")

	if got := captureBodyForLog(c); got != "" {
		t.Fatalf("GET 不应读取 body, got %q", got)
	}
}
