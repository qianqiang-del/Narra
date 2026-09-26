package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	responsedto "narra/internal/model/dto/response"
	"narra/internal/model/entity"
	"narra/internal/service"
	"narra/pkg/config"
	"narra/pkg/documentparser"
)

// stubDocumentService 只实现 Get：进度流只走这一条查询路径。
// 其余方法由嵌入的 nil 接口兜底 —— 一旦流里用了别的方法，测试会以 panic 的形式当场暴露。
type stubDocumentService struct {
	service.KnowledgeService
	frames []responsedto.KnowledgeDocument
	errs   []error // 与 frames 同序；第 i 次调用返回 errs[i]（非 nil 时优先）
	calls  int
}

func (s *stubDocumentService) Get(context.Context, uint64) (responsedto.KnowledgeDocument, error) {
	index := s.calls
	s.calls++
	if index < len(s.errs) && s.errs[index] != nil {
		return responsedto.KnowledgeDocument{}, s.errs[index]
	}
	if index >= len(s.frames) {
		index = len(s.frames) - 1
	}
	return s.frames[index], nil
}

// stubParser 依次返回 statuses；用完之后重复最后一帧。
type stubParser struct {
	documentparser.Parser
	statuses []documentparser.Status
	calls    int
}

func (p *stubParser) Status(context.Context) documentparser.Status {
	index := p.calls
	p.calls++
	if index >= len(p.statuses) {
		index = len(p.statuses) - 1
	}
	return p.statuses[index]
}

// newEventsController 把秒级的推送节奏压到毫秒级，否则一条状态流转要等两三秒。
func newEventsController(t *testing.T, svc service.KnowledgeService, parser documentparser.Parser) *Controller {
	t.Helper()
	controller := NewController(svc, t.TempDir(), parser, config.KnowledgeIngestConfig{})
	controller.eventsQueryInterval = 5 * time.Millisecond
	controller.eventsHeartbeatInterval = time.Minute
	controller.eventsMaxDuration = time.Minute
	return controller
}

func eventsContext(t *testing.T, id string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/knowledge/documents/"+id+"/events", nil)
	ctx.Params = gin.Params{{Key: "id", Value: id}}
	return ctx, recorder
}

func documentFrame(id uint64, status string) responsedto.KnowledgeDocument {
	return responsedto.KnowledgeDocument{ID: id, Status: status}
}

type streamEvent struct {
	name string
	data string
}

// parseStream 把响应体拆成事件序列：按空行分段，逐行读 event / data。
// 只实现测试要用的规则：注释行（心跳）忽略，多行 data 用换行连接。
func parseStream(body string) []streamEvent {
	var events []streamEvent
	for _, block := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n\n") {
		name := ""
		var data []string
		for _, line := range strings.Split(block, "\n") {
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}
			field, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")
			switch field {
			case "event":
				name = value
			case "data":
				data = append(data, value)
			}
		}
		if name != "" || len(data) > 0 {
			events = append(events, streamEvent{name: name, data: strings.Join(data, "\n")})
		}
	}
	return events
}

func documentStatuses(t *testing.T, events []streamEvent) []string {
	t.Helper()
	var statuses []string
	for _, event := range events {
		if event.name != eventDocument {
			continue
		}
		var payload struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal([]byte(event.data), &payload); err != nil {
			t.Fatalf("document 事件不是合法 JSON: %v", err)
		}
		statuses = append(statuses, payload.Status)
	}
	return statuses
}

func countEvents(events []streamEvent, name string) int {
	count := 0
	for _, event := range events {
		if event.name == name {
			count++
		}
	}
	return count
}

// 主路径：pending → processing → ready 各推一帧，ready 之后流必须停。
func TestEventsStreamsUntilReady(t *testing.T) {
	svc := &stubDocumentService{frames: []responsedto.KnowledgeDocument{
		documentFrame(7, entity.KnowledgeDocumentStatusPending),
		documentFrame(7, entity.KnowledgeDocumentStatusProcessing),
		{ID: 7, Status: entity.KnowledgeDocumentStatusReady, Chunks: 12},
	}}
	controller := newEventsController(t, svc, &stubParser{statuses: []documentparser.Status{{Enabled: true, Ready: true}}})
	ctx, recorder := eventsContext(t, "7")

	controller.Events(ctx)

	if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/event-stream") {
		t.Fatalf("Content-Type = %q，期望 text/event-stream", contentType)
	}

	events := parseStream(recorder.Body.String())
	if got, want := documentStatuses(t, events), []string{"pending", "processing", "ready"}; !slices.Equal(got, want) {
		t.Fatalf("document 事件状态 = %v，期望 %v", got, want)
	}
	// parser 状态整段没变，只在开头推一帧 —— 每秒一次的空查询不该变成每秒一条事件。
	if got := countEvents(events, eventParser); got != 1 {
		t.Errorf("parser 事件 %d 帧，期望 1（只在变化时推）", got)
	}
	last := events[len(events)-1]
	if last.name != eventDocument || !strings.Contains(last.data, `"status":"ready"`) {
		t.Errorf("最后一帧 = %s %s，期望 ready 的 document", last.name, last.data)
	}
}

// 订阅时已经是终态（例如刷新页面后重连）：补一帧当前状态就关流，不等下一次轮询。
func TestEventsClosesWhenAlreadySettled(t *testing.T) {
	svc := &stubDocumentService{frames: []responsedto.KnowledgeDocument{
		{ID: 7, Status: entity.KnowledgeDocumentStatusFailed, Error: "上游 429"},
	}}
	controller := newEventsController(t, svc, nil)
	ctx, recorder := eventsContext(t, "7")

	controller.Events(ctx)

	events := parseStream(recorder.Body.String())
	if len(events) != 2 {
		t.Fatalf("事件数 = %d，期望 2（document + parser 各一帧）", len(events))
	}
	if events[0].name != eventDocument || !strings.Contains(events[0].data, "上游 429") {
		t.Errorf("首帧 = %s %s，期望带上失败原因的 document", events[0].name, events[0].data)
	}
}

// 解析环境准备进度也会进流：只在 progress 变化时推新帧，而不是整段准备期反复推同一帧。
func TestEventsPushesParserProgressOnChange(t *testing.T) {
	svc := &stubDocumentService{frames: []responsedto.KnowledgeDocument{
		documentFrame(7, entity.KnowledgeDocumentStatusPending),
		documentFrame(7, entity.KnowledgeDocumentStatusPending),
		documentFrame(7, entity.KnowledgeDocumentStatusPending),
		documentFrame(7, entity.KnowledgeDocumentStatusReady),
	}}
	controller := newEventsController(t, svc, &stubParser{statuses: []documentparser.Status{
		{Enabled: true, Preparing: true, Progress: "下载解释器"},
		{Enabled: true, Preparing: true, Progress: "下载解释器"},
		{Enabled: true, Ready: true},
	}})
	ctx, recorder := eventsContext(t, "7")

	controller.Events(ctx)

	events := parseStream(recorder.Body.String())
	if got := countEvents(events, eventParser); got != 2 {
		t.Fatalf("parser 事件 %d 帧，期望 2（初始一帧 + 变化一帧）", got)
	}
	lastParser := ""
	for _, event := range events {
		if event.name == eventParser {
			lastParser = event.data
		}
	}
	if !strings.Contains(lastParser, `"ready":true`) {
		t.Errorf("最后一帧 parser = %s，期望 ready = true", lastParser)
	}
}

// 中途取不到文档（用户删了记录）：推一帧 error 说明原因，然后关流。
func TestEventsReportsErrorWhenGetFails(t *testing.T) {
	svc := &stubDocumentService{
		frames: []responsedto.KnowledgeDocument{documentFrame(7, entity.KnowledgeDocumentStatusPending)},
		errs:   []error{nil, errors.New("知识文档 7 不存在")},
	}
	controller := newEventsController(t, svc, nil)
	ctx, recorder := eventsContext(t, "7")

	controller.Events(ctx)

	events := parseStream(recorder.Body.String())
	last := events[len(events)-1]
	if last.name != eventError || !strings.Contains(last.data, "不存在") {
		t.Fatalf("最后一帧 = %s %s，期望带上原因的 error 帧", last.name, last.data)
	}
}

// 文档压根不存在：这是一次普通请求，该收到统一信封的 JSON 错误，而不是事件流。
func TestEventsRejectsMissingDocumentWithJSON(t *testing.T) {
	svc := &stubDocumentService{
		frames: []responsedto.KnowledgeDocument{{}},
		errs:   []error{errors.New("知识文档 9 不存在")},
	}
	controller := newEventsController(t, svc, nil)
	ctx, recorder := eventsContext(t, "9")

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
		t.Fatalf("信封 = %+v，期望非 0 code 且带上原因", envelope)
	}
}
