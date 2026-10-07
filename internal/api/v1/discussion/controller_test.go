package discussion

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	requestdto "narra/internal/model/dto/request"
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
	gotSceneID        uint64
	gotStyle          string
	called            bool

	result      *responsedto.DiscussionStart
	settings    *responsedto.DiscussionSettings
	gotSettings requestdto.DiscussionSettings
	err         error
}

func (f *fakeDiscussionService) GetSettings(_ context.Context, classroomID uint64) (*responsedto.DiscussionSettings, error) {
	f.called = true
	f.gotConversationID = classroomID
	return f.settings, f.err
}

func (f *fakeDiscussionService) UpdateSettings(ctx context.Context, classroomID uint64, input requestdto.DiscussionSettings) (*responsedto.DiscussionSettings, error) {
	f.gotSettings = input
	return f.GetSettings(ctx, classroomID)
}

func TestDiscussionSettingsRoutes(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			f := &fakeDiscussionService{settings: &responsedto.DiscussionSettings{ProviderID: 2, ModelID: "chat", Available: true, Source: "classroom"}}
			engine := newTestEngine(f)
			req := httptest.NewRequest(method, "/api/v1/classrooms/7/discussion-settings", bytes.NewBufferString(`{"llm_provider_id":2,"llm_model_id":"chat"}`))
			req.Header.Set("Content-Type", "application/json")
			out := httptest.NewRecorder()
			engine.ServeHTTP(out, req)
			var result struct {
				Code int                            `json:"code"`
				Data responsedto.DiscussionSettings `json:"data"`
			}
			if err := json.Unmarshal(out.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if out.Code != 200 || result.Code != 0 || result.Data.ProviderID != 2 || !f.called || f.gotConversationID != 7 {
				t.Fatalf("unexpected result %s", out.Body.String())
			}
			if method == http.MethodPatch && (f.gotSettings.ProviderID != 2 || f.gotSettings.ModelID != "chat") {
				t.Fatalf("input: %+v", f.gotSettings)
			}
		})
	}
}

func TestDiscussionSettingsInvalidRequests(t *testing.T) {
	for _, tc := range []struct{ method, id, body string }{
		{http.MethodGet, "0", ""}, {http.MethodPatch, "abc", `{}`}, {http.MethodPatch, "7", `{broken`},
	} {
		f := &fakeDiscussionService{}
		engine := newTestEngine(f)
		req := httptest.NewRequest(tc.method, "/api/v1/classrooms/"+tc.id+"/discussion-settings", bytes.NewBufferString(tc.body))
		req.Header.Set("Content-Type", "application/json")
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		var result struct {
			Code int `json:"code"`
		}
		if err := json.Unmarshal(out.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Code != apperrors.CodeBadRequest || f.called {
			t.Fatalf("unexpected response %s", out.Body.String())
		}
	}
}

func (f *fakeDiscussionService) Start(ctx context.Context, conversationID uint64, content string) (*responsedto.DiscussionStart, error) {
	return f.StartAtScene(ctx, conversationID, content, 0)
}

func (f *fakeDiscussionService) StartAtScene(ctx context.Context, conversationID uint64, content string, sceneID uint64) (*responsedto.DiscussionStart, error) {
	f.called = true
	f.gotConversationID = conversationID
	f.gotContent = content
	f.gotSceneID = sceneID
	return f.result, f.err
}

func (f *fakeDiscussionService) StartWithStyle(ctx context.Context, conversationID uint64, content string, sceneID uint64, style string) (*responsedto.DiscussionStart, error) {
	f.gotStyle = style
	return f.StartAtScene(ctx, conversationID, content, sceneID)
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

	_, envelope := doStart(t, engine, "/api/v1/conversations/7/discussions", `{"content":"为什么操作前要先确认枪口安全？","scene_id":42,"discussion_style":"multi_perspective"}`)

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
	if svc.gotSceneID != 42 {
		t.Errorf("交给业务层的课件页 ID = %d，期望 42", svc.gotSceneID)
	}
	if svc.gotStyle != "multi_perspective" {
		t.Errorf("交给业务层的讨论方式 = %q", svc.gotStyle)
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
