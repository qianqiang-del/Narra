package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	responsedto "narra/internal/model/dto/response"
	"narra/internal/service"
)

// stubConversationService 只实现 Exists 与 ListEventsAfter：
// 其余方法由嵌入的 nil 接口兜底，一旦处理器用了别的方法会以 panic 的形式当场暴露。
type stubConversationService struct {
	service.ConversationService
	exists    bool
	existsErr error
	list      func(ctx context.Context, conversationID uint64, after int64, limit int) ([]responsedto.ConversationEvent, error)
}

func (s *stubConversationService) Exists(context.Context, uint64) (bool, error) {
	return s.exists, s.existsErr
}

func (s *stubConversationService) ListEventsAfter(
	ctx context.Context,
	conversationID uint64,
	after int64,
	limit int,
) ([]responsedto.ConversationEvent, error) {
	return s.list(ctx, conversationID, after, limit)
}

// newEventsController 把秒级的推送节奏压到毫秒级，否则一次轮询要等半秒。
func newEventsController(svc service.ConversationService) *Controller {
	controller := NewController(svc)
	controller.pollInterval = 5 * time.Millisecond
	controller.heartbeatInterval = time.Minute
	return controller
}

func eventsContext(t *testing.T, target string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, target, nil)
	ctx.Params = gin.Params{{Key: "id", Value: "42"}}
	return ctx, recorder
}

func eventFrame(sequence int64, eventType string) responsedto.ConversationEvent {
	return responsedto.ConversationEvent{
		SequenceNo: sequence,
		EventType:  eventType,
		Payload:    json.RawMessage(`{}`),
		CreatedAt:  time.Now(),
	}
}

type streamFrame struct {
	id   string
	name string
	data string
}

// parseStream 把响应体拆成帧：按空行分段，逐行读 id / event / data。
// 只实现测试要用的规则：注释行（心跳）忽略，多行 data 用换行连接。
func parseStream(body string) []streamFrame {
	var frames []streamFrame
	for _, block := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n\n") {
		frame := streamFrame{}
		var data []string
		for _, line := range strings.Split(block, "\n") {
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}
			field, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")
			switch field {
			case "id":
				frame.id = value
			case "event":
				frame.name = value
			case "data":
				data = append(data, value)
			}
		}
		if frame.name != "" || len(data) > 0 {
			frame.data = strings.Join(data, "\n")
			frames = append(frames, frame)
		}
	}
	return frames
}

// 主路径：先重放积压事件，再增量推新事件，每条都带 id（= sequence_no）与事件名。
func TestEventsReplaysAndPushesInOrder(t *testing.T) {
	calls := 0
	svc := &stubConversationService{
		exists: true,
		list: func(context.Context, uint64, int64, int) ([]responsedto.ConversationEvent, error) {
			calls++
			switch calls {
			case 1:
				return []responsedto.ConversationEvent{
					eventFrame(1, "run.started"),
					eventFrame(2, "director.decision"),
				}, nil
			case 2:
				return []responsedto.ConversationEvent{eventFrame(3, "message.delta")}, nil
			default:
				return nil, errors.New("数据库连接已断开")
			}
		},
	}
	controller := newEventsController(svc)
	ctx, recorder := eventsContext(t, "/api/v1/conversations/42/events")

	controller.Events(ctx)

	if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/event-stream") {
		t.Fatalf("Content-Type = %q，期望 text/event-stream", contentType)
	}

	frames := parseStream(recorder.Body.String())
	// 最后一帧是传输层 error：查库失败，推完即关。
	last := frames[len(frames)-1]
	if last.name != streamEventError || !strings.Contains(last.data, "数据库连接已断开") {
		t.Fatalf("最后一帧 = %s %s，期望带上原因的 error 帧", last.name, last.data)
	}

	business := frames[:len(frames)-1]
	wantNames := []string{"run.started", "director.decision", "message.delta"}
	wantIDs := []string{"1", "2", "3"}
	if len(business) != len(wantNames) {
		t.Fatalf("业务帧数 = %d，期望 %d", len(business), len(wantNames))
	}
	for index, frame := range business {
		if frame.name != wantNames[index] || frame.id != wantIDs[index] {
			t.Errorf("第 %d 帧 = %s(id=%s)，期望 %s(id=%s)",
				index, frame.name, frame.id, wantNames[index], wantIDs[index])
		}
		var payload struct {
			SequenceNo int64  `json:"sequence_no"`
			EventType  string `json:"event_type"`
		}
		if err := json.Unmarshal([]byte(frame.data), &payload); err != nil {
			t.Fatalf("第 %d 帧 data 不是合法 JSON: %v", index, err)
		}
		if payload.EventType != wantNames[index] {
			t.Errorf("第 %d 帧 data.event_type = %q，期望 %q", index, payload.EventType, wantNames[index])
		}
	}
}

// 断线续传：Last-Event-ID 头优先于 ?after=（浏览器 EventSource 重连只会带头）。
func TestEventsResumePrefersLastEventID(t *testing.T) {
	var firstAfter int64 = -1
	svc := &stubConversationService{
		exists: true,
		list: func(_ context.Context, _ uint64, after int64, _ int) ([]responsedto.ConversationEvent, error) {
			if firstAfter < 0 {
				firstAfter = after
			}
			return nil, errors.New("结束测试")
		},
	}
	controller := newEventsController(svc)
	ctx, _ := eventsContext(t, "/api/v1/conversations/42/events?after=7")
	ctx.Request.Header.Set("Last-Event-ID", "41")

	controller.Events(ctx)

	if firstAfter != 41 {
		t.Fatalf("首次查询 after = %d，期望 41（头优先于查询参数）", firstAfter)
	}
}

// fetch 客户端用 ?after= 续传。
func TestEventsResumeFromQuery(t *testing.T) {
	var firstAfter int64 = -1
	svc := &stubConversationService{
		exists: true,
		list: func(_ context.Context, _ uint64, after int64, _ int) ([]responsedto.ConversationEvent, error) {
			if firstAfter < 0 {
				firstAfter = after
			}
			return nil, errors.New("结束测试")
		},
	}
	controller := newEventsController(svc)
	ctx, _ := eventsContext(t, "/api/v1/conversations/42/events?after=7")

	controller.Events(ctx)

	if firstAfter != 7 {
		t.Fatalf("首次查询 after = %d，期望 7", firstAfter)
	}
}

// 积压超过一批时立刻继续取，不回到轮询节奏 —— 断言所有批次都按序推完。
func TestEventsDrainsBacklogInBatches(t *testing.T) {
	calls := 0
	svc := &stubConversationService{
		exists: true,
		list: func(context.Context, uint64, int64, int) ([]responsedto.ConversationEvent, error) {
			calls++
			switch calls {
			case 1:
				return []responsedto.ConversationEvent{eventFrame(1, "run.started"), eventFrame(2, "agent.started")}, nil
			case 2:
				return []responsedto.ConversationEvent{eventFrame(3, "run.completed")}, nil
			default:
				return nil, errors.New("结束测试")
			}
		},
	}
	controller := newEventsController(svc)
	controller.batchSize = 2
	ctx, recorder := eventsContext(t, "/api/v1/conversations/42/events")

	controller.Events(ctx)

	frames := parseStream(recorder.Body.String())
	if len(frames) != 4 {
		t.Fatalf("帧数 = %d，期望 4（3 条业务 + 1 条 error）", len(frames))
	}
	for index, want := range []string{"run.started", "agent.started", "run.completed"} {
		if frames[index].name != want {
			t.Errorf("第 %d 帧 = %s，期望 %s", index, frames[index].name, want)
		}
	}
}

// 对话不存在：普通 JSON 错误，不能切成事件流。
func TestEventsRejectsUnknownConversation(t *testing.T) {
	svc := &stubConversationService{exists: false}
	controller := newEventsController(svc)
	ctx, recorder := eventsContext(t, "/api/v1/conversations/42/events")

	controller.Events(ctx)

	if contentType := recorder.Header().Get("Content-Type"); !strings.Contains(contentType, "application/json") {
		t.Fatalf("Content-Type = %q，期望普通 JSON 错误", contentType)
	}
	var envelope struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("响应不是 JSON: %v", err)
	}
	if envelope.Code == 0 || !strings.Contains(envelope.Message, "不存在") {
		t.Fatalf("信封 = %+v，期望非 0 code 且说明对话不存在", envelope)
	}
}

// 长时间没有事件时靠注释心跳保活；客户端断开后循环要退出。
func TestEventsSendsHeartbeatWhileIdle(t *testing.T) {
	svc := &stubConversationService{
		exists: true,
		list: func(context.Context, uint64, int64, int) ([]responsedto.ConversationEvent, error) {
			return nil, nil
		},
	}
	controller := NewController(svc)
	controller.pollInterval = time.Hour // 只让心跳触发
	controller.heartbeatInterval = 5 * time.Millisecond
	ctx, recorder := eventsContext(t, "/api/v1/conversations/42/events")

	requestCtx, cancel := context.WithCancel(context.Background())
	ctx.Request = ctx.Request.WithContext(requestCtx)

	done := make(chan struct{})
	go func() {
		controller.Events(ctx)
		close(done)
	}()

	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("客户端断开后处理器没有退出")
	}

	if !strings.Contains(recorder.Body.String(), ": ping") {
		t.Fatal("空闲期间没有发心跳")
	}
}
