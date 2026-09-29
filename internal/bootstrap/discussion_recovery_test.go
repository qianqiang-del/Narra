package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"narra/internal/model/entity"
	"narra/internal/repository"
	"narra/pkg/config"
	"narra/pkg/database"
	"narra/pkg/logger"
)

type recoveryFixture struct {
	conversationID uint64
	tx             repository.TransactionManager
	messages       repository.MessageRepository
	runs           repository.RunRepository
	turns          repository.TurnRepository
	events         repository.ConversationEventRepository
}

func newRecoveryFixture(t *testing.T) *recoveryFixture {
	t.Helper()
	if os.Getenv("NARRA_INTEGRATION_TEST") == "" {
		t.Skip("设置 NARRA_INTEGRATION_TEST=1 后运行 PostgreSQL 集成测试")
	}
	path, err := filepath.Abs("../../configs/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := logger.Init(&cfg.Log); err != nil {
		t.Fatal(err)
	}
	db, err := database.InitPostgres(&cfg.Database.Postgres)
	if err != nil {
		t.Fatal(err)
	}
	classroom := &entity.Classroom{
		Title: "讨论启动对账测试", Requirement: "测试数据", Mode: entity.ClassroomModeInteractive,
		Status: entity.ClassroomStatusPlayable, GenerationConfig: json.RawMessage(`{}`), AgentConfig: json.RawMessage(`{}`),
	}
	if err := db.Create(classroom).Error; err != nil {
		t.Fatal(err)
	}
	conversation := &entity.ClassroomConversation{
		ClassroomID: classroom.ID, Title: "重启对账", Type: entity.ConversationTypeDiscussion,
		Status: entity.ConversationStatusActive,
	}
	if err := db.Create(conversation).Error; err != nil {
		db.Delete(classroom)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		runIDs := db.Model(&entity.OrchestrationRun{}).Select("id").Where("conversation_id = ?", conversation.ID)
		db.Where("conversation_id = ?", conversation.ID).Delete(&entity.ConversationEvent{})
		db.Where("run_id IN (?)", runIDs).Delete(&entity.AgentTurn{})
		db.Where("conversation_id = ?", conversation.ID).Delete(&entity.OrchestrationRun{})
		db.Where("conversation_id = ?", conversation.ID).Delete(&entity.ConversationMessage{})
		db.Delete(conversation)
		db.Delete(classroom)
	})
	return &recoveryFixture{
		conversationID: conversation.ID,
		tx:             repository.NewTransactionManager(db),
		messages:       repository.NewMessageRepository(db),
		runs:           repository.NewRunRepository(db),
		turns:          repository.NewTurnRepository(db),
		events:         repository.NewConversationEventRepository(db),
	}
}

func (f *recoveryFixture) newRun(t *testing.T, status string) (*entity.OrchestrationRun, *entity.AgentTurn) {
	t.Helper()
	ctx := context.Background()
	message := &entity.ConversationMessage{
		ConversationID: f.conversationID, SenderType: entity.MessageSenderUser,
		SenderSnapshot: json.RawMessage(`{}`), Content: "测试重启", Status: entity.MessageStatusCompleted,
	}
	run := &entity.OrchestrationRun{
		ConversationID: f.conversationID, TraceID: fmt.Sprintf("%032x", time.Now().UnixNano()),
		Status: entity.RunStatusQueued, MaxTurns: 2, OrchestratorVersion: "recovery-test",
		ConfigSnapshot: json.RawMessage(`{}`),
	}
	if err := f.tx.Run(ctx, func(ctx context.Context) error {
		if err := f.messages.AppendNext(ctx, message); err != nil {
			return err
		}
		run.TriggerMessageID = message.ID
		return f.runs.CreateNextAttempt(ctx, run)
	}); err != nil {
		t.Fatal(err)
	}
	if status != entity.RunStatusQueued {
		if err := f.runs.MarkRunning(ctx, run.ID, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	turn := &entity.AgentTurn{RunID: run.ID, AgentSnapshot: json.RawMessage(`{}`), Status: entity.AgentTurnStatusRunning}
	if err := f.tx.Run(ctx, func(ctx context.Context) error { return f.turns.CreateNext(ctx, turn) }); err != nil {
		t.Fatal(err)
	}
	if status == entity.RunStatusCompleted {
		reason := entity.RunStopCompleted
		if err := f.runs.Finish(ctx, run.ID, repository.RunResult{
			Status: status, StopReason: &reason, FinishedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	return run, turn
}

func TestReconcileDiscussionsFinishesInterruptedRunOnce(t *testing.T) {
	f := newRecoveryFixture(t)
	ctx := context.Background()
	interrupted, turn := f.newRun(t, entity.RunStatusRunning)
	completed, _ := f.newRun(t, entity.RunStatusCompleted)
	partial := &entity.ConversationMessage{
		ConversationID: f.conversationID, SenderType: entity.MessageSenderAgent,
		SenderSnapshot: json.RawMessage(`{}`), Content: "已生成一部分", Status: entity.MessageStatusStreaming,
	}
	if err := f.tx.Run(ctx, func(ctx context.Context) error {
		if err := f.messages.AppendNext(ctx, partial); err != nil {
			return err
		}
		return f.turns.AttachOutputMessage(ctx, turn.ID, partial.ID)
	}); err != nil {
		t.Fatal(err)
	}

	count, err := ReconcileDiscussions(ctx, f.tx, f.runs, f.turns, f.messages, f.events)
	if err != nil || count != 1 {
		t.Fatalf("启动对账 = (%d, %v)，期望只收尾一个运行", count, err)
	}
	got, err := f.runs.FindByID(ctx, interrupted.ID)
	if err != nil || got.Status != entity.RunStatusFailed || got.StopReason == nil || *got.StopReason != entity.RunStopError || got.FinishedAt == nil {
		t.Fatalf("中断的运行未被正确收尾: %#v, %v", got, err)
	}
	gotTurn, err := f.turns.FindByID(ctx, turn.ID)
	if err != nil || gotTurn.Status != entity.AgentTurnStatusFailed || gotTurn.FinishedAt == nil {
		t.Fatalf("中断的回合未被正确收尾: %#v, %v", gotTurn, err)
	}
	gotMessage, err := f.messages.FindByID(ctx, partial.ID)
	if err != nil || gotMessage.Status != entity.MessageStatusFailed || gotMessage.Content != partial.Content {
		t.Fatalf("中断的流式消息未保留正文并标为失败: %#v, %v", gotMessage, err)
	}
	unchanged, err := f.runs.FindByID(ctx, completed.ID)
	if err != nil || unchanged.Status != entity.RunStatusCompleted {
		t.Fatalf("已完成的运行被改动: %#v, %v", unchanged, err)
	}
	events, err := f.events.ListAfter(ctx, f.conversationID, 0, 10)
	if err != nil || len(events) != 1 || events[0].EventType != entity.ConversationEventRunFailed || events[0].RunID == nil || *events[0].RunID != interrupted.ID || !strings.Contains(string(events[0].Payload), "服务重启") {
		t.Fatalf("失败事件不正确: %#v, %v", events, err)
	}
	count, err = ReconcileDiscussions(ctx, f.tx, f.runs, f.turns, f.messages, f.events)
	if err != nil || count != 0 {
		t.Fatalf("重复对账 = (%d, %v)，期望无变更", count, err)
	}
	events, err = f.events.ListAfter(ctx, f.conversationID, 0, 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("重复对账写了额外事件: %#v, %v", events, err)
	}
}

type failingRecoveryEvents struct {
	repository.ConversationEventRepository
}

func (f failingRecoveryEvents) AppendNext(context.Context, *entity.ConversationEvent) error {
	return errors.New("写事件失败")
}

func TestReconcileDiscussionsRollsBackOnEventFailure(t *testing.T) {
	f := newRecoveryFixture(t)
	ctx := context.Background()
	run, turn := f.newRun(t, entity.RunStatusRunning)
	_, err := ReconcileDiscussions(ctx, f.tx, f.runs, f.turns, f.messages, failingRecoveryEvents{f.events})
	if err == nil || !strings.Contains(err.Error(), "写事件失败") {
		t.Fatalf("事件写入失败应中止对账，实际: %v", err)
	}
	got, err := f.runs.FindByID(ctx, run.ID)
	if err != nil || got.Status != entity.RunStatusRunning {
		t.Fatalf("事务回滚后运行状态 = %#v, %v", got, err)
	}
	gotTurn, err := f.turns.FindByID(ctx, turn.ID)
	if err != nil || gotTurn.Status != entity.AgentTurnStatusRunning {
		t.Fatalf("事务回滚后回合状态 = %#v, %v", gotTurn, err)
	}
}
