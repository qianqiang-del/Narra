package repository

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"narra/internal/model/entity"
	"narra/pkg/config"
	"narra/pkg/database"
	"narra/pkg/logger"
)

// 成员 C 仓储层的真实验收测试。
//
// 默认跳过。这些用例会连本机 PostgreSQL 并真的写入数据，而套件里其余测试都是纯内存的，
// 不该因为某台机器没起数据库就整片报红。要跑它们，显式给一个环境变量：
//
//	$env:NARRA_INTEGRATION_TEST = '1'
//	go test ./internal/repository/ -run TestMemberC -v
//
// 为什么非要写真实数据库：这一层要验证的正是"并发下序号会不会撞"，而答案只存在于
// 真实的行锁和唯一约束里 —— 内存里的假仓储无论如何都模拟不出 FOR UPDATE 的排队行为。
//
// 用例自己负责收尾：建出来的课程会在测试结束时按依赖顺序删掉，成员 C 的表随之清空。
const (
	integrationEnv      = "NARRA_INTEGRATION_TEST"
	testOrchestratorVer = "test-orchestrator-v1"
)

var (
	testDBOnce sync.Once
	testDB     *gorm.DB
	testDBErr  error
)

// openTestDB 返回共享的测试数据库连接。
//
// 用 sync.Once 让整个包共用一个连接池：每个用例各建一份会在测试结束时留下若干
// 没关闭的连接池，而 database 包只保留最后一次赋值的全局句柄，前面的无从关闭。
func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	if os.Getenv(integrationEnv) == "" {
		t.Skipf("跳过集成测试：设置 %s=1 后重跑（需要本机 PostgreSQL 可用）", integrationEnv)
	}

	testDBOnce.Do(func() {
		// go test 的工作目录是包目录，而配置里的日志路径是相对仓库根写的。
		// 先切到仓库根再初始化，否则会在 internal/repository/ 下生成一个 logs/ 目录。
		repoRoot, err := filepath.Abs("../..")
		if err != nil {
			testDBErr = fmt.Errorf("定位仓库根目录失败: %w", err)
			return
		}
		configPath := filepath.Join(repoRoot, "configs", "config.yaml")
		if err := os.Chdir(repoRoot); err != nil {
			testDBErr = fmt.Errorf("切换工作目录失败: %w", err)
			return
		}

		cfg, err := config.Load(configPath)
		if err != nil {
			testDBErr = fmt.Errorf("加载配置失败: %w", err)
			return
		}
		// 连接层会写日志，logger 没初始化时全局变量是空指针。
		if err := logger.Init(&cfg.Log); err != nil {
			testDBErr = fmt.Errorf("初始化日志失败: %w", err)
			return
		}
		testDB, testDBErr = database.InitPostgres(&cfg.Database.Postgres)
	})
	if testDBErr != nil {
		t.Fatalf("%v", testDBErr)
	}
	return testDB
}

// testFixture 是一套测试数据的句柄与收尾动作。
type testFixture struct {
	classroom     *entity.Classroom
	conversation  *entity.ClassroomConversation
	conversations ConversationRepository
	messages      MessageRepository
	runs          RunRepository
	turns         TurnRepository
	tx            TransactionManager
	cleanup       func()
}

// newTestFixture 造一条课程 + 对话，并登记好各仓储。
func newTestFixture(t *testing.T) *testFixture {
	t.Helper()

	db := openTestDB(t)
	ctx := context.Background()

	classroom := &entity.Classroom{
		Title:            "成员C仓储集成测试",
		Requirement:      "测试数据，跑完即删",
		Mode:             entity.ClassroomModeInteractive,
		Status:           entity.ClassroomStatusPlayable,
		GenerationConfig: json.RawMessage("{}"),
		AgentConfig:      json.RawMessage("{}"),
	}
	if err := db.WithContext(ctx).Create(classroom).Error; err != nil {
		t.Fatalf("建测试课程失败: %v", err)
	}

	f := &testFixture{
		classroom:     classroom,
		conversations: NewConversationRepository(db),
		messages:      NewMessageRepository(db),
		runs:          NewRunRepository(db),
		turns:         NewTurnRepository(db),
		tx:            NewTransactionManager(db),
	}

	conversation := &entity.ClassroomConversation{
		ClassroomID: classroom.ID,
		Title:       "序号分配测试",
		Type:        entity.ConversationTypeDiscussion,
		Status:      entity.ConversationStatusActive,
	}
	if err := f.conversations.Create(ctx, conversation); err != nil {
		db.WithContext(ctx).Delete(&entity.Classroom{}, classroom.ID)
		t.Fatalf("建测试对话失败: %v", err)
	}
	f.conversation = conversation

	f.cleanup = func() {
		// 按依赖顺序显式删除，而不是只删课程、指望外键级联。
		// 级联规则是另一层的东西（写在 migrations SQL 里），测试不该把"库里数据一定干净"
		// 建立在它没被人改过这个假设上；显式删还能在规则被改坏时立刻暴露出来。
		runIDs := db.Model(&entity.OrchestrationRun{}).Select("id").Where("conversation_id = ?", conversation.ID)
		db.Where("run_id IN (?)", runIDs).Delete(&entity.AgentTurn{})
		db.Where("conversation_id = ?", conversation.ID).Delete(&entity.OrchestrationRun{})
		db.Where("conversation_id = ?", conversation.ID).Delete(&entity.ConversationMessage{})
		db.Where("id = ?", conversation.ID).Delete(&entity.ClassroomConversation{})
		db.Where("id = ?", classroom.ID).Delete(&entity.Classroom{})

		var leftovers int64
		db.Model(&entity.ConversationMessage{}).Where("conversation_id = ?", conversation.ID).Count(&leftovers)
		db.Model(&entity.OrchestrationRun{}).Where("conversation_id = ?", conversation.ID).Count(&leftovers)
		if leftovers != 0 {
			t.Errorf("测试数据未清理干净：对话 %d 下仍有 %d 条记录", conversation.ID, leftovers)
		}
	}
	return f
}

// appendMessage 在事务里追加一条消息，返回分配到的序号。
func (f *testFixture) appendMessage(t *testing.T, ctx context.Context, content string) int64 {
	t.Helper()

	message := &entity.ConversationMessage{
		ConversationID: f.conversation.ID,
		SenderType:     entity.MessageSenderUser,
		SenderSnapshot: json.RawMessage("{}"),
		Content:        content,
		Status:         entity.MessageStatusCompleted,
		Metadata:       json.RawMessage("{}"),
	}
	if err := f.tx.Run(ctx, func(ctx context.Context) error {
		return f.messages.AppendNext(ctx, message)
	}); err != nil {
		t.Fatalf("追加消息失败: %v", err)
	}
	return message.SequenceNo
}

// randomTraceID 造一个 32 位十六进制 trace id，满足列宽与格式约定。
func randomTraceID(t *testing.T) string {
	t.Helper()

	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		t.Fatalf("生成 trace id 失败: %v", err)
	}
	return hex.EncodeToString(buf[:])
}

// newRun 造一次运行，返回它和分配到的 attempt_no。
func (f *testFixture) newRun(t *testing.T, ctx context.Context, triggerMessageID uint64) (*entity.OrchestrationRun, error) {
	t.Helper()

	run := &entity.OrchestrationRun{
		ConversationID:      f.conversation.ID,
		TriggerMessageID:    triggerMessageID,
		TraceID:             randomTraceID(t),
		Status:              entity.RunStatusQueued,
		MaxTurns:            6,
		OrchestratorVersion: testOrchestratorVer,
		ConfigSnapshot:      json.RawMessage("{}"),
	}
	err := f.tx.Run(ctx, func(ctx context.Context) error {
		return f.runs.CreateNextAttempt(ctx, run)
	})
	return run, err
}

// TestMemberCMessageSequenceIsSequential 验证序号从 1 开始严格递增。
func TestMemberCMessageSequenceIsSequential(t *testing.T) {
	f := newTestFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		if got := f.appendMessage(t, ctx, fmt.Sprintf("第 %d 条", i)); got != int64(i) {
			t.Fatalf("第 %d 条消息拿到序号 %d，期望 %d", i, got, i)
		}
	}

	messages, err := f.messages.ListByConversation(ctx, f.conversation.ID, 0, 100)
	if err != nil {
		t.Fatalf("读取消息列表失败: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("消息条数 = %d，期望 3", len(messages))
	}
	for i, message := range messages {
		if message.SequenceNo != int64(i+1) {
			t.Errorf("第 %d 条消息序号 = %d，期望 %d", i, message.SequenceNo, i+1)
		}
	}

	// 增量拉取：从第 1 条之后读，应该只剩第 2、3 条 —— 这是 SSE 断线重连走的那条路。
	after, err := f.messages.ListByConversation(ctx, f.conversation.ID, 1, 100)
	if err != nil {
		t.Fatalf("增量读取消息失败: %v", err)
	}
	if len(after) != 2 || after[0].SequenceNo != 2 || after[1].SequenceNo != 3 {
		t.Errorf("从序号 1 之后读取的结果 = %+v，期望序号 2、3", after)
	}
}

// TestMemberCMessageSequenceSurvivesConcurrency 是这一层最关键的用例：
// 多个 goroutine 同时往同一条对话里追加消息，序号必须是 1..N 的排列，不重不漏。
func TestMemberCMessageSequenceSurvivesConcurrency(t *testing.T) {
	f := newTestFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	const workers = 12
	start := make(chan struct{})
	errs := make([]error, workers)
	sequences := make([]int64, workers)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // 统一放行，尽量制造真实的并发争抢

			message := &entity.ConversationMessage{
				ConversationID: f.conversation.ID,
				SenderType:     entity.MessageSenderUser,
				SenderSnapshot: json.RawMessage("{}"),
				Content:        fmt.Sprintf("并发第 %d 条", i),
				Status:         entity.MessageStatusCompleted,
				Metadata:       json.RawMessage("{}"),
			}
			errs[i] = f.tx.Run(ctx, func(ctx context.Context) error {
				return f.messages.AppendNext(ctx, message)
			})
			sequences[i] = message.SequenceNo
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("第 %d 个并发写入失败: %v", i, err)
		}
	}

	seen := make(map[int64]int, workers)
	for _, seq := range sequences {
		if seq < 1 || seq > workers {
			t.Errorf("序号 %d 超出 1..%d 的范围", seq, workers)
		}
		seen[seq]++
	}
	for seq := int64(1); seq <= workers; seq++ {
		switch seen[seq] {
		case 0:
			t.Errorf("序号 %d 没有被分配（漏号）", seq)
		case 1:
			// 正常
		default:
			t.Errorf("序号 %d 被分配了 %d 次（重复）", seq, seen[seq])
		}
	}

	count, err := f.messages.CountByConversation(ctx, f.conversation.ID)
	if err != nil {
		t.Fatalf("统计消息条数失败: %v", err)
	}
	if count != workers {
		t.Errorf("库中消息条数 = %d，期望 %d", count, workers)
	}
}

// TestMemberCRunAttemptNoAndTurnNo 验证运行的 attempt_no 按触发消息递增、
// 回合的 turn_no 按运行递增，两条链路互不干扰。
func TestMemberCRunAttemptNoAndTurnNo(t *testing.T) {
	f := newTestFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	f.appendMessage(t, ctx, "帮我讲讲这道题")
	trigger, err := f.messages.ListByConversation(ctx, f.conversation.ID, 0, 1)
	if err != nil || len(trigger) != 1 {
		t.Fatalf("读取触发消息失败: %v (条数 %d)", err, len(trigger))
	}
	triggerMessageID := trigger[0].ID

	firstRun, err := f.newRun(t, ctx, triggerMessageID)
	if err != nil {
		t.Fatalf("建第一次运行失败: %v", err)
	}
	if firstRun.AttemptNo != 1 {
		t.Errorf("第一次运行 attempt_no = %d，期望 1", firstRun.AttemptNo)
	}

	// 同一条消息重新执行：新增一行 attempt_no = 2，第一次的记录仍然在。
	secondRun, err := f.newRun(t, ctx, triggerMessageID)
	if err != nil {
		t.Fatalf("建第二次运行失败: %v", err)
	}
	if secondRun.AttemptNo != 2 {
		t.Errorf("第二次运行 attempt_no = %d，期望 2", secondRun.AttemptNo)
	}
	runs, err := f.runs.ListByConversation(ctx, f.conversation.ID, 10)
	if err != nil {
		t.Fatalf("读取运行列表失败: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("运行条数 = %d，期望 2（重试不应覆盖旧记录）", len(runs))
	}

	// 回合号属于运行，两个运行各从 1 开始。
	for turnNo := 1; turnNo <= 2; turnNo++ {
		turn := &entity.AgentTurn{
			RunID:         firstRun.ID,
			AgentSnapshot: json.RawMessage("{}"),
			Status:        entity.AgentTurnStatusRunning,
		}
		if err := f.tx.Run(ctx, func(ctx context.Context) error {
			return f.turns.CreateNext(ctx, turn)
		}); err != nil {
			t.Fatalf("建第 %d 个回合失败: %v", turnNo, err)
		}
		if turn.TurnNo != int16(turnNo) {
			t.Errorf("第一个运行的第 %d 个回合 turn_no = %d", turnNo, turn.TurnNo)
		}
	}

	otherTurn := &entity.AgentTurn{
		RunID:         secondRun.ID,
		AgentSnapshot: json.RawMessage("{}"),
		Status:        entity.AgentTurnStatusRunning,
	}
	if err := f.tx.Run(ctx, func(ctx context.Context) error {
		return f.turns.CreateNext(ctx, otherTurn)
	}); err != nil {
		t.Fatalf("在第二个运行下建回合失败: %v", err)
	}
	if otherTurn.TurnNo != 1 {
		t.Errorf("第二个运行的第一个回合 turn_no = %d，期望 1", otherTurn.TurnNo)
	}

	count, err := f.turns.CountByRun(ctx, firstRun.ID)
	if err != nil {
		t.Fatalf("统计回合数失败: %v", err)
	}
	if count != 2 {
		t.Errorf("第一个运行的回合数 = %d，期望 2", count)
	}
}

// TestMemberCClosedConversationRejectsAppend 验证已结束的对话会被明确拒绝，
// 而不是悄悄把消息插进一个不再有人看的话题里。
func TestMemberCClosedConversationRejectsAppend(t *testing.T) {
	f := newTestFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	f.appendMessage(t, ctx, "结束前的最后一句")
	if err := f.conversations.Close(ctx, f.conversation.ID, time.Now().UTC()); err != nil {
		t.Fatalf("结束对话失败: %v", err)
	}

	message := &entity.ConversationMessage{
		ConversationID: f.conversation.ID,
		SenderType:     entity.MessageSenderUser,
		SenderSnapshot: json.RawMessage("{}"),
		Content:        "结束之后还想说",
		Status:         entity.MessageStatusCompleted,
		Metadata:       json.RawMessage("{}"),
	}
	err := f.tx.Run(ctx, func(ctx context.Context) error {
		return f.messages.AppendNext(ctx, message)
	})
	if !errors.Is(err, ErrConversationNotActive) {
		t.Fatalf("对已结束对话追加消息返回 %v，期望 ErrConversationNotActive", err)
	}

	count, err := f.messages.CountByConversation(ctx, f.conversation.ID)
	if err != nil {
		t.Fatalf("统计消息条数失败: %v", err)
	}
	if count != 1 {
		t.Errorf("被拒绝后消息条数 = %d，期望仍是 1", count)
	}
}
