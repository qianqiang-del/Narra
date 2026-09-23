package conversation

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"narra/internal/model/entity"
	"narra/internal/repository"
	"narra/internal/service"
	"narra/pkg/config"
	"narra/pkg/database"
	"narra/pkg/logger"
)

// 对话事件流的端到端集成测试：真库 → 仓储 → 服务 → HTTP SSE。
//
// 默认跳过，与成员 C 的仓储用例同一条约定（需要本机 PostgreSQL）：
//
//	$env:NARRA_INTEGRATION_TEST = '1'
//	go test ./internal/api/v1/conversation/ -run TestEventsEndToEnd -v
//
// 单元测试用假服务验证了帧的组装，这里补上另一半：序号真的从库里来、
// 重放与增量推送真的能通过 HTTP 读到、断线续传真的接得上。
const conversationIntegrationEnv = "NARRA_INTEGRATION_TEST"

var (
	integrationDBOnce sync.Once
	integrationDB     *gorm.DB
	integrationDBErr  error
)

// openIntegrationDB 打开共享的测试库连接（同包只建一次）。
func openIntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()

	if os.Getenv(conversationIntegrationEnv) == "" {
		t.Skipf("跳过集成测试：设置 %s=1 后重跑（需要本机 PostgreSQL 可用）", conversationIntegrationEnv)
	}

	integrationDBOnce.Do(func() {
		// go test 的工作目录是包目录（internal/api/v1/conversation），配置里的日志路径
		// 是相对仓库根写的；先切回仓库根再初始化，否则会在包目录下生成 logs/。
		repoRoot, err := filepath.Abs("../../../..")
		if err != nil {
			integrationDBErr = fmt.Errorf("定位仓库根目录失败: %w", err)
			return
		}
		if err := os.Chdir(repoRoot); err != nil {
			integrationDBErr = fmt.Errorf("切换工作目录失败: %w", err)
			return
		}
		cfg, err := config.Load(filepath.Join(repoRoot, "configs", "config.yaml"))
		if err != nil {
			integrationDBErr = fmt.Errorf("加载配置失败: %w", err)
			return
		}
		if err := logger.Init(&cfg.Log); err != nil {
			integrationDBErr = fmt.Errorf("初始化日志失败: %w", err)
			return
		}
		integrationDB, integrationDBErr = database.InitPostgres(&cfg.Database.Postgres)
	})
	if integrationDBErr != nil {
		t.Fatalf("%v", integrationDBErr)
	}
	return integrationDB
}

// newIntegrationFixture 造一条课程 + 对话，并返回可用的处理器与收尾动作。
func newIntegrationFixture(t *testing.T) (*Controller, repository.TransactionManager, repository.ConversationEventRepository, uint64, func()) {
	t.Helper()

	db := openIntegrationDB(t)
	ctx := context.Background()

	classroom := &entity.Classroom{
		Title:            "对话事件流集成测试",
		Requirement:      "测试数据，跑完即删",
		Mode:             entity.ClassroomModeInteractive,
		Status:           entity.ClassroomStatusPlayable,
		GenerationConfig: json.RawMessage("{}"),
		AgentConfig:      json.RawMessage("{}"),
	}
	if err := db.WithContext(ctx).Create(classroom).Error; err != nil {
		t.Fatalf("建测试课程失败: %v", err)
	}

	conversation := &entity.ClassroomConversation{
		ClassroomID: classroom.ID,
		Title:       "事件流测试",
		Type:        entity.ConversationTypeDiscussion,
		Status:      entity.ConversationStatusActive,
	}
	if err := db.WithContext(ctx).Create(conversation).Error; err != nil {
		db.WithContext(ctx).Delete(&entity.Classroom{}, classroom.ID)
		t.Fatalf("建测试对话失败: %v", err)
	}

	eventRepo := repository.NewConversationEventRepository(db)
	conversationRepo := repository.NewConversationRepository(db)
	txManager := repository.NewTransactionManager(db)
	svc := service.NewConversationService(conversationRepo, eventRepo)

	controller := NewController(svc)
	controller.pollInterval = 20 * time.Millisecond
	controller.heartbeatInterval = time.Minute

	cleanup := func() {
		db.Where("conversation_id = ?", conversation.ID).Delete(&entity.ConversationEvent{})
		db.Where("id = ?", conversation.ID).Delete(&entity.ClassroomConversation{})
		db.Where("id = ?", classroom.ID).Delete(&entity.Classroom{})
	}
	return controller, txManager, eventRepo, conversation.ID, cleanup
}

// appendEvent 在事务里追加一条事件，返回它的序号。
func appendEvent(t *testing.T, tx repository.TransactionManager, repo repository.ConversationEventRepository, conversationID uint64, eventType string) int64 {
	t.Helper()

	event := &entity.ConversationEvent{
		ConversationID: conversationID,
		EventType:      eventType,
		Payload:        json.RawMessage(`{}`),
	}
	if err := tx.Run(context.Background(), func(ctx context.Context) error {
		return repo.AppendNext(ctx, event)
	}); err != nil {
		t.Fatalf("追加事件失败: %v", err)
	}
	return event.SequenceNo
}

// readFrames 从 SSE 流里读满 count 条业务帧（跳过注释心跳）。
func readFrames(t *testing.T, reader *bufio.Reader, count int) []streamFrame {
	t.Helper()

	var frames []streamFrame
	for len(frames) < count {
		frame := streamFrame{}
		var data []string
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatalf("读流失败（已收 %d 帧）: %v", len(frames), err)
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			if strings.HasPrefix(line, ":") {
				continue // 心跳
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
		if frame.name == "" {
			continue
		}
		frame.data = strings.Join(data, "\n")
		frames = append(frames, frame)
	}
	return frames
}

// 端到端：连上时补历史、连上后推增量，帧的 id 就是库里的序号。
func TestEventsEndToEndReplayAndPush(t *testing.T) {
	controller, tx, eventRepo, conversationID, cleanup := newIntegrationFixture(t)
	defer cleanup()

	// 先落两条：客户端连上时应该被重放出来。
	appendEvent(t, tx, eventRepo, conversationID, entity.ConversationEventRunStarted)
	appendEvent(t, tx, eventRepo, conversationID, entity.ConversationEventMessageDelta)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/api/v1/conversations/:id/events", controller.Events)
	server := httptest.NewServer(engine)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	url := fmt.Sprintf("%s/api/v1/conversations/%d/events", server.URL, conversationID)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("连接事件流失败: %v", err)
	}
	defer response.Body.Close()

	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/event-stream") {
		t.Fatalf("Content-Type = %q，期望 text/event-stream", contentType)
	}

	// 连接建立之后才写第三条：验证增量推送，而不是只重放一次。
	appendEvent(t, tx, eventRepo, conversationID, entity.ConversationEventRunCompleted)

	frames := readFrames(t, bufio.NewReader(response.Body), 3)
	wantNames := []string{
		entity.ConversationEventRunStarted,
		entity.ConversationEventMessageDelta,
		entity.ConversationEventRunCompleted,
	}
	for index, frame := range frames {
		if frame.name != wantNames[index] {
			t.Errorf("第 %d 帧 event = %q，期望 %q", index, frame.name, wantNames[index])
		}
		if frame.id != strconv.Itoa(index+1) {
			t.Errorf("第 %d 帧 id = %q，期望 %q（序号来自数据库）", index, frame.id, strconv.Itoa(index+1))
		}
	}
}

// 端到端续传：带 Last-Event-ID 重连，只收到它之后的事件。
func TestEventsEndToEndResume(t *testing.T) {
	controller, tx, eventRepo, conversationID, cleanup := newIntegrationFixture(t)
	defer cleanup()

	for index := 0; index < 3; index++ {
		appendEvent(t, tx, eventRepo, conversationID, entity.ConversationEventMessageDelta)
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/api/v1/conversations/:id/events", controller.Events)
	server := httptest.NewServer(engine)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	url := fmt.Sprintf("%s/api/v1/conversations/%d/events", server.URL, conversationID)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	request.Header.Set("Last-Event-ID", "2")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("连接事件流失败: %v", err)
	}
	defer response.Body.Close()

	frames := readFrames(t, bufio.NewReader(response.Body), 1)
	if frames[0].id != "3" {
		t.Fatalf("续传首帧 id = %q，期望 3（跳过已收到的 1、2）", frames[0].id)
	}
}
