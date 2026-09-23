package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"narra/internal/model/entity"
)

// 对话事件仓储的真实验收：序号分配、增量读取、并发下不撞号。
//
// 与成员 C 的仓储用例同一条约定：默认跳过，显式给环境变量后跑：
//
//	$env:NARRA_INTEGRATION_TEST = '1'
//	go test ./internal/repository/ -run TestConversationEvent -v
//
// 必须用真库的原因也和消息仓储一样：要验证的正是 FOR UPDATE 的排队行为，
// 内存替身模拟不出来。用例自己收尾，按依赖顺序显式删除。
func newConversationEventFixture(t *testing.T) *conversationEventFixture {
	t.Helper()

	db := openMemberCTestDB(t)
	ctx := context.Background()

	classroom := &entity.Classroom{
		Title:            "对话事件仓储集成测试",
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

	fixture := &conversationEventFixture{
		db:            db,
		classroom:     classroom,
		conversation:  conversation,
		events:        NewConversationEventRepository(db),
		conversations: NewConversationRepository(db),
		tx:            NewTransactionManager(db),
	}
	fixture.cleanup = func() {
		// 显式删除，不指望外键级联：级联规则是另一层的东西，
		// 测试不该把"库里一定干净"建立在它没被人改过这个假设上。
		db.Where("conversation_id = ?", conversation.ID).Delete(&entity.ConversationEvent{})
		db.Where("id = ?", conversation.ID).Delete(&entity.ClassroomConversation{})
		db.Where("id = ?", classroom.ID).Delete(&entity.Classroom{})

		var leftovers int64
		db.Model(&entity.ConversationEvent{}).Where("conversation_id = ?", conversation.ID).Count(&leftovers)
		if leftovers != 0 {
			t.Errorf("测试数据未清理干净：对话 %d 下仍有 %d 条事件", conversation.ID, leftovers)
		}
	}
	return fixture
}

type conversationEventFixture struct {
	db            *gorm.DB
	classroom     *entity.Classroom
	conversation  *entity.ClassroomConversation
	events        ConversationEventRepository
	conversations ConversationRepository
	tx            TransactionManager
	cleanup       func()
}

// append 在事务里追加一条事件（与生产者的调用姿势一致）。
func (f *conversationEventFixture) append(t *testing.T, ctx context.Context, eventType, payload string) *entity.ConversationEvent {
	t.Helper()

	event := &entity.ConversationEvent{
		ConversationID: f.conversation.ID,
		EventType:      eventType,
		Payload:        json.RawMessage(payload),
	}
	if err := f.tx.Run(ctx, func(ctx context.Context) error {
		return f.events.AppendNext(ctx, event)
	}); err != nil {
		t.Fatalf("追加事件失败: %v", err)
	}
	return event
}

// 顺序追加时序号从 1 开始逐个递增；ListAfter 只取严格大于起点的部分。
func TestConversationEventAppendAndListAfter(t *testing.T) {
	fixture := newConversationEventFixture(t)
	defer fixture.cleanup()
	ctx := context.Background()

	for index := 1; index <= 3; index++ {
		event := fixture.append(t, ctx, entity.ConversationEventMessageDelta, fmt.Sprintf(`{"delta":"第%d批"}`, index))
		if event.SequenceNo != int64(index) {
			t.Fatalf("第 %d 次追加拿到序号 %d，期望 %d", index, event.SequenceNo, index)
		}
	}

	all, err := fixture.events.ListAfter(ctx, fixture.conversation.ID, 0, 100)
	if err != nil {
		t.Fatalf("ListAfter(0) 失败: %v", err)
	}
	if len(all) != 3 || all[0].SequenceNo != 1 || all[2].SequenceNo != 3 {
		t.Fatalf("ListAfter(0) = %d 条（首 %d、末 %d），期望 3 条 1..3", len(all), all[0].SequenceNo, all[2].SequenceNo)
	}

	afterFirst, err := fixture.events.ListAfter(ctx, fixture.conversation.ID, 1, 100)
	if err != nil {
		t.Fatalf("ListAfter(1) 失败: %v", err)
	}
	if len(afterFirst) != 2 || afterFirst[0].SequenceNo != 2 {
		t.Fatalf("ListAfter(1) = %d 条（首 %d），期望 2 条且从 2 开始", len(afterFirst), afterFirst[0].SequenceNo)
	}

	none, err := fixture.events.ListAfter(ctx, fixture.conversation.ID, 3, 100)
	if err != nil {
		t.Fatalf("ListAfter(3) 失败: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("ListAfter(3) = %d 条，期望 0 条", len(none))
	}
}

// 并发追加不能撞号：多个 Agent 同时产出、加上 100ms 合批的增量，是这个仓储的常态。
func TestConversationEventConcurrentAppendDoesNotCollide(t *testing.T) {
	fixture := newConversationEventFixture(t)
	defer fixture.cleanup()
	ctx := context.Background()

	const (
		writers       = 4
		perWriter     = 5
		expectedTotal = writers * perWriter
	)

	var (
		waitGroup sync.WaitGroup
		mu        sync.Mutex
		seen      = make(map[int64]bool, expectedTotal)
		failures  []error
	)
	for writer := 0; writer < writers; writer++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for index := 0; index < perWriter; index++ {
				event := &entity.ConversationEvent{
					ConversationID: fixture.conversation.ID,
					EventType:      entity.ConversationEventMessageDelta,
					Payload:        json.RawMessage(`{"delta":"x"}`),
				}
				if err := fixture.tx.Run(ctx, func(ctx context.Context) error {
					return fixture.events.AppendNext(ctx, event)
				}); err != nil {
					mu.Lock()
					failures = append(failures, err)
					mu.Unlock()
					return
				}
				mu.Lock()
				if seen[event.SequenceNo] {
					failures = append(failures, fmt.Errorf("序号 %d 被分配了两次", event.SequenceNo))
				}
				seen[event.SequenceNo] = true
				mu.Unlock()
			}
		}()
	}
	waitGroup.Wait()

	if len(failures) > 0 {
		t.Fatalf("并发追加出现失败: %v", failures)
	}
	// 行锁把并发排成队，所以拿到的应该恰好是 1..N，既无重复也无空洞。
	for sequence := int64(1); sequence <= expectedTotal; sequence++ {
		if !seen[sequence] {
			t.Fatalf("缺少序号 %d（共 %d 条：%v）", sequence, len(seen), seen)
		}
	}
}

// 到期清理只删过期的：没到期的事件原样留着。
func TestConversationEventDeleteExpired(t *testing.T) {
	fixture := newConversationEventFixture(t)
	defer fixture.cleanup()
	ctx := context.Background()

	fresh := fixture.append(t, ctx, entity.ConversationEventMessageDelta, `{"delta":"新"}`)

	expired := &entity.ConversationEvent{
		ConversationID: fixture.conversation.ID,
		EventType:      entity.ConversationEventMessageDelta,
		Payload:        json.RawMessage(`{"delta":"旧"}`),
		ExpiresAt:      time.Now().Add(-time.Hour), // 显式写一个已经过期的保留期
	}
	if err := fixture.tx.Run(ctx, func(ctx context.Context) error {
		return fixture.events.AppendNext(ctx, expired)
	}); err != nil {
		t.Fatalf("追加过期事件失败: %v", err)
	}

	deleted, err := fixture.events.DeleteExpired(ctx, time.Now())
	if err != nil {
		t.Fatalf("清理过期事件失败: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("清理条数 = %d，期望 1（只删过期那条）", deleted)
	}

	left, err := fixture.events.ListAfter(ctx, fixture.conversation.ID, 0, 100)
	if err != nil {
		t.Fatalf("回查事件失败: %v", err)
	}
	if len(left) != 1 || left[0].SequenceNo != fresh.SequenceNo {
		t.Fatalf("剩余事件 = %v，期望只剩序号 %d 那条", left, fresh.SequenceNo)
	}
}

// 已结束的对话不能再追加事件 —— 与消息同一条规则，复用同一把锁里的状态校验。
func TestConversationEventAppendRejectsClosedConversation(t *testing.T) {
	fixture := newConversationEventFixture(t)
	defer fixture.cleanup()
	ctx := context.Background()

	if err := fixture.conversations.Close(ctx, fixture.conversation.ID, time.Now()); err != nil {
		t.Fatalf("关闭对话失败: %v", err)
	}

	event := &entity.ConversationEvent{
		ConversationID: fixture.conversation.ID,
		EventType:      entity.ConversationEventRunCompleted,
		Payload:        json.RawMessage(`{}`),
	}
	err := fixture.tx.Run(ctx, func(ctx context.Context) error {
		return fixture.events.AppendNext(ctx, event)
	})
	if !errors.Is(err, ErrConversationNotActive) {
		t.Fatalf("追加到已结束对话的错误 = %v，期望 ErrConversationNotActive", err)
	}
}
