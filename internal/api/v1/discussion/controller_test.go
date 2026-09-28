package discussion

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	responsedto "narra/internal/model/dto/response"
	apperrors "narra/pkg/errors"
)

// 讨论入口 HTTP 层的用例。
//
// 这一层用假服务：要验的是"参数有没有解析对、错有没有翻成对外的码、成功时回了什么"，
// 与"讨论到底有没有跑起来"是两件事 —— 后者由 internal/service 的真库用例覆盖。
// 混在一起测的话，一条 HTTP 参数解析的失败会表现得像"讨论跑不起来"，很难定位。
//
// 不连库、不需要 NARRA_INTEGRATION_TEST：这一层本来就不该碰数据库。

// fakeDiscussionService 记下收到什么、回什么由用例决定。
type fakeDiscussionService struct {
	gotConversationID uint64
	gotContent        string
	called            bool

	result *responsedto.DiscussionStart
	err    error
}

func (f *fakeDiscussionService) Start(ctx context.Context, conversationID uint64, content string) (*responsedto.DiscussionStart, error) {
	f.called = true
	f.gotConversationID = conversationID
	f.gotContent = content
	return f.result, f.err
}

// newTestEngine 挂上真实路由，返回引擎与假服务。
func newTestEngine(svc *fakeDiscussionService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	RegisterRoutes(engine.Group("/api/v1"), NewController(svc))
	return engine
}

// doStart 发一次请求并解出响应信封。
func doStart(t *testing.T, engine *gin.Engine, path string, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()

	request := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		// 项目的统一信封把业务错误也写成 HTTP 200，所以这里出现非 200 说明
		// 框架层出了问题（路由没挂上、panic 了），值得单独报出来。
		t.Fatalf("HTTP 状态 = %d，期望 200（统一信封里错误也走 200）", recorder.Code)
	}

	var envelope map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("响应不是合法 JSON: %v（原文 %s）", err, recorder.Body.String())
	}
	return recorder, envelope
}

// TestStartRejectsInvalidConversationID 对话 ID 不是正整数时，挡在业务层之前。
func TestStartRejectsInvalidConversationID(t *testing.T) {
	svc := &fakeDiscussionService{}
	engine := newTestEngine(svc)

	for _, id := range []string{"abc", "0", "-1"} {
		_, envelope := doStart(t, engine, "/api/v1/conversations/"+id+"/discussions", `{"content":"在吗"}`)
		if code := envelope["code"]; code != float64(apperrors.CodeBadRequest) {
			t.Errorf("对话 ID %q：响应码 = %v，期望 %d", id, code, apperrors.CodeBadRequest)
		}
	}
	if svc.called {
		t.Error("参数不合法时不该调到业务层")
	}
}

// TestStartRejectsMalformedBody 请求体不是 JSON 时给出明确的参数错误。
func TestStartRejectsMalformedBody(t *testing.T) {
	svc := &fakeDiscussionService{}
	engine := newTestEngine(svc)

	_, envelope := doStart(t, engine, "/api/v1/conversations/7/discussions", `{这不是 json`)
	if code := envelope["code"]; code != float64(apperrors.CodeBadRequest) {
		t.Errorf("响应码 = %v，期望 %d", code, apperrors.CodeBadRequest)
	}
	if svc.called {
		t.Error("请求体不合法时不该调到业务层")
	}
}

// TestStartPassesContentAndReturnsIDs 正常路径：正文原样交给业务层，返回两个 ID。
func TestStartPassesContentAndReturnsIDs(t *testing.T) {
	svc := &fakeDiscussionService{
		result: &responsedto.DiscussionStart{ConversationID: 7, MessageID: 42},
	}
	engine := newTestEngine(svc)

	_, envelope := doStart(t, engine, "/api/v1/conversations/7/discussions", `{"content":"为什么操作前要先确认枪口安全？"}`)

	if code := envelope["code"]; code != float64(apperrors.CodeSuccess) {
		t.Fatalf("响应码 = %v，期望 %d", code, apperrors.CodeSuccess)
	}
	if !svc.called {
		t.Fatal("业务层没有被调用")
	}
	if svc.gotConversationID != 7 {
		t.Errorf("交给业务层的对话 ID = %d，期望 7（取自路径参数）", svc.gotConversationID)
	}
	if svc.gotContent != "为什么操作前要先确认枪口安全？" {
		t.Errorf("交给业务层的正文 = %q，与请求里的不一致", svc.gotContent)
	}

	data, ok := envelope["data"].(map[string]any)
	if !ok {
		t.Fatalf("响应缺少 data 对象：%v", envelope["data"])
	}
	if data["conversation_id"] != float64(7) {
		t.Errorf("data.conversation_id = %v，期望 7", data["conversation_id"])
	}
	if data["message_id"] != float64(42) {
		t.Errorf("data.message_id = %v，期望 42", data["message_id"])
	}
}

// TestStartReportsBusinessError 业务错误的码与话原样透出。
//
// "正在讨论中"这类错误带的是 409：前端据此提示"稍后再试"，而不是让用户去改输入。
func TestStartReportsBusinessError(t *testing.T) {
	svc := &fakeDiscussionService{
		err: apperrors.New(apperrors.CodeConflict, "这条对话正在讨论中，请稍后再试"),
	}
	engine := newTestEngine(svc)

	_, envelope := doStart(t, engine, "/api/v1/conversations/7/discussions", `{"content":"在吗"}`)

	if code := envelope["code"]; code != float64(apperrors.CodeConflict) {
		t.Errorf("响应码 = %v，期望 %d", code, apperrors.CodeConflict)
	}
	if message, _ := envelope["message"].(string); message != "这条对话正在讨论中，请稍后再试" {
		t.Errorf("响应消息 = %q，期望把业务层那句话原样透出", message)
	}
}
