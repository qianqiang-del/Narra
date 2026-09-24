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
	"strings"
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
	memberCIntegrationEnv      = "NARRA_INTEGRATION_TEST"
	memberCTestOrchestratorVer = "test-orchestrator-v1"
)

var (
	memberCTestDBOnce sync.Once
	memberCTestDB     *gorm.DB
	memberCTestDBErr  error
)

// openMemberCTestDB 返回共享的测试数据库连接。
//
// 用 sync.Once 让整个包共用一个连接池：每个用例各建一份会在测试结束时留下若干
// 没关闭的连接池，而 database 包只保留最后一次赋值的全局句柄，前面的无从关闭。
func openMemberCTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	if os.Getenv(memberCIntegrationEnv) == "" {
		t.Skipf("跳过集成测试：设置 %s=1 后重跑（需要本机 PostgreSQL 可用）", memberCIntegrationEnv)
	}

	memberCTestDBOnce.Do(func() {
		// go test 的工作目录是包目录，而配置里的日志路径是相对仓库根写的。
		// 先切到仓库根再初始化，否则会在 internal/repository/ 下生成一个 logs/ 目录。
		repoRoot, err := filepath.Abs("../..")
		if err != nil {
			memberCTestDBErr = fmt.Errorf("定位仓库根目录失败: %w", err)
			return
		}
		configPath := filepath.Join(repoRoot, "configs", "config.yaml")
		if err := os.Chdir(repoRoot); err != nil {
			memberCTestDBErr = fmt.Errorf("切换工作目录失败: %w", err)
			return
		}

		cfg, err := config.Load(configPath)
		if err != nil {
			memberCTestDBErr = fmt.Errorf("加载配置失败: %w", err)
			return
		}
		// 连接层会写日志，logger 没初始化时全局变量是空指针。
		if err := logger.Init(&cfg.Log); err != nil {
			memberCTestDBErr = fmt.Errorf("初始化日志失败: %w", err)
			return
		}
		memberCTestDB, memberCTestDBErr = database.InitPostgres(&cfg.Database.Postgres)
	})
	if memberCTestDBErr != nil {
		t.Fatalf("%v", memberCTestDBErr)
	}
	return memberCTestDB
}

// memberCTestFixture 是一套测试数据的句柄与收尾动作。
type memberCTestFixture struct {
	db            *gorm.DB
	classroom     *entity.Classroom
	conversation  *entity.ClassroomConversation
	conversations ConversationRepository
	messages      MessageRepository
	runs          RunRepository
	turns         TurnRepository
	compactions   ContextCompactionRepository
	memories      SharedMemoryRepository
	events        ConversationEventRepository
	tx            TransactionManager
	cleanup       func()
}

// newMemberCTestFixture 造一条课程 + 对话，并登记好各仓储。
func newMemberCTestFixture(t *testing.T) *memberCTestFixture {
	t.Helper()

	db := openMemberCTestDB(t)
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

	f := &memberCTestFixture{
		db:            db,
		classroom:     classroom,
		conversations: NewConversationRepository(db),
		messages:      NewMessageRepository(db),
		runs:          NewRunRepository(db),
		turns:         NewTurnRepository(db),
		compactions:   NewContextCompactionRepository(db),
		memories:      NewSharedMemoryRepository(db),
		events:        NewConversationEventRepository(db),
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
		// 摘要也要显式删。它自引用（previous_compaction_id → 自己），而且有
		// UNIQUE (conversation_id, covered_to_sequence)：留着不仅算残留，
		// 还会让下一次用例存同一个 covered_to 时撞唯一约束，症状看起来像"实现写错了"。
		db.Where("conversation_id = ?", conversation.ID).Delete(&entity.ContextCompaction{})
		// 共享记忆按 classroom_id 删：课堂级记忆的 conversation_id 是空的，
		// 只按 conversation_id 删会留下一堆课堂级残留（这一列两种作用域都有值）。
		db.Where("classroom_id = ?", classroom.ID).Delete(&entity.SharedContextMemory{})
		// 事件先删：它引用 run 与 turn，虽然外键是 SET NULL，但显式删掉更干净。
		db.Where("conversation_id = ?", conversation.ID).Delete(&entity.ConversationEvent{})
		db.Where("id = ?", conversation.ID).Delete(&entity.ClassroomConversation{})
		db.Where("id = ?", classroom.ID).Delete(&entity.Classroom{})

		var leftovers int64
		db.Model(&entity.ConversationMessage{}).Where("conversation_id = ?", conversation.ID).Count(&leftovers)
		db.Model(&entity.OrchestrationRun{}).Where("conversation_id = ?", conversation.ID).Count(&leftovers)
		if leftovers != 0 {
			t.Errorf("测试数据未清理干净：对话 %d 下仍有 %d 条记录", conversation.ID, leftovers)
		}

		var leftoverMemories int64
		db.Model(&entity.SharedContextMemory{}).Where("classroom_id = ?", classroom.ID).Count(&leftoverMemories)
		if leftoverMemories != 0 {
			t.Errorf("测试数据未清理干净：课堂 %d 下仍有 %d 条共享记忆", classroom.ID, leftoverMemories)
		}

		var leftoverEvents int64
		db.Model(&entity.ConversationEvent{}).Where("conversation_id = ?", conversation.ID).Count(&leftoverEvents)
		if leftoverEvents != 0 {
			t.Errorf("测试数据未清理干净：对话 %d 下仍有 %d 条事件", conversation.ID, leftoverEvents)
		}
	}
	return f
}

// appendMessage 在事务里追加一条消息，返回分配到的序号。
func (f *memberCTestFixture) appendMessage(t *testing.T, ctx context.Context, content string) int64 {
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

// memberCRandomTraceID 造一个 32 位十六进制 trace id，满足列宽与格式约定。
func memberCRandomTraceID(t *testing.T) string {
	t.Helper()

	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		t.Fatalf("生成 trace id 失败: %v", err)
	}
	return hex.EncodeToString(buf[:])
}

// newRun 造一次运行，返回它和分配到的 attempt_no。
func (f *memberCTestFixture) newRun(t *testing.T, ctx context.Context, triggerMessageID uint64) (*entity.OrchestrationRun, error) {
	t.Helper()

	run := &entity.OrchestrationRun{
		ConversationID:      f.conversation.ID,
		TriggerMessageID:    triggerMessageID,
		TraceID:             memberCRandomTraceID(t),
		Status:              entity.RunStatusQueued,
		MaxTurns:            6,
		OrchestratorVersion: memberCTestOrchestratorVer,
		ConfigSnapshot:      json.RawMessage("{}"),
	}
	err := f.tx.Run(ctx, func(ctx context.Context) error {
		return f.runs.CreateNextAttempt(ctx, run)
	})
	return run, err
}

// TestMemberCMessageSequenceIsSequential 验证序号从 1 开始严格递增。
func TestMemberCMessageSequenceIsSequential(t *testing.T) {
	f := newMemberCTestFixture(t)
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
	f := newMemberCTestFixture(t)
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
	f := newMemberCTestFixture(t)
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
	f := newMemberCTestFixture(t)
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

// newCompaction 存一版摘要。
//
// 不走事务：这张表没有任何需要原子分配的东西（没有序号列），
// 单条 INSERT 本身就是一个隐式事务。
//
// previous 传 nil 表示这是该会话的第一版摘要。sourceTokens / summaryTokens 由调用方手填 ——
// 正常路径下它们来自真实估算，这里手填是为了能把"摘要必须比原文短"那条 CHECK 精确压到边界上。
func (f *memberCTestFixture) newCompaction(
	t *testing.T,
	ctx context.Context,
	previous *entity.ContextCompaction,
	from, to int64,
	summary string,
	sourceTokens, summaryTokens int32,
) *entity.ContextCompaction {
	t.Helper()

	compaction := &entity.ContextCompaction{
		ConversationID:      f.conversation.ID,
		CoveredFromSequence: from,
		CoveredToSequence:   to,
		Summary:             summary,
		KeyPoints:           json.RawMessage("{}"),
		SourceTokens:        sourceTokens,
		SummaryTokens:       summaryTokens,
	}
	if previous != nil {
		compaction.PreviousCompactionID = &previous.ID
	}
	if err := f.compactions.Create(ctx, compaction); err != nil {
		t.Fatalf("存摘要失败: %v", err)
	}
	return compaction
}

// TestMemberCContextCompactionLatestIsNilWhenAbsent 验证"还没有任何摘要"不是错误。
//
// 这是本仓储唯一一个"查不到不算异常"的方法：编排每次组装上下文都会先问一句
// "有没有上一版摘要"，答"没有"是完全正常的第一次调用。若返回 error，调用方就得用
// errors.Is(gorm.ErrRecordNotFound) 去分辨 —— 等于把 GORM 的细节漏到业务层。
func TestMemberCContextCompactionLatestIsNilWhenAbsent(t *testing.T) {
	f := newMemberCTestFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	latest, err := f.compactions.LatestByConversation(ctx, f.conversation.ID)
	if err != nil {
		t.Fatalf("没有摘要时不该报错，实际返回: %v", err)
	}
	if latest != nil {
		t.Fatalf("没有摘要时应返回 nil，实际拿到 id=%d", latest.ID)
	}
}

// TestMemberCContextCompactionLatestPicksHighestCoveredTo 验证取到的是"覆盖得最远"的那一版。
//
// 判据用 covered_to_sequence，而不是 created_at 或 id：摘要被修正后可能重存，
// 而"覆盖到第几条消息"才是版本新旧的事实依据。
func TestMemberCContextCompactionLatestPicksHighestCoveredTo(t *testing.T) {
	f := newMemberCTestFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	first := f.newCompaction(t, ctx, nil, 1, 10, "第一版摘要：讨论了天空为什么是蓝的", 200, 20)
	second := f.newCompaction(t, ctx, first, 11, 30, "第二版摘要：在第一版基础上补充了散射的细节", 400, 40)

	latest, err := f.compactions.LatestByConversation(ctx, f.conversation.ID)
	if err != nil {
		t.Fatalf("取最新摘要失败: %v", err)
	}
	if latest == nil {
		t.Fatal("明明有两版摘要，却返回 nil")
	}
	if latest.ID != second.ID {
		t.Errorf("取到 id=%d，期望最新那版 id=%d", latest.ID, second.ID)
	}
	if latest.CoveredToSequence != 30 {
		t.Errorf("covered_to_sequence = %d，期望 30", latest.CoveredToSequence)
	}
	// 版本链必须接得上：编排靠 previous_compaction_id 判断"上一版覆盖到哪、这次从哪继续压"。
	if latest.PreviousCompactionID == nil || *latest.PreviousCompactionID != first.ID {
		t.Errorf("版本链断了：previous_compaction_id = %v，期望 %d", latest.PreviousCompactionID, first.ID)
	}
}

// TestMemberCContextCompactionRejectsSummaryLongerThanSource 验证
// "摘要必须比原文短"这条规则由数据库兜底，而不是只靠调用方自觉。
//
// 为什么专门测它：压缩完反而更长，这次压缩就毫无意义 —— 上下文没变小，
// 还白白多了一行记录、多一次模型调用。这种错必须在写入那一刻就炸，而不是留到线上。
func TestMemberCContextCompactionRejectsSummaryLongerThanSource(t *testing.T) {
	f := newMemberCTestFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	bad := &entity.ContextCompaction{
		ConversationID:      f.conversation.ID,
		CoveredFromSequence: 1,
		CoveredToSequence:   5,
		Summary:             "这段摘要比被替掉的原文还长，属于无效压缩",
		KeyPoints:           json.RawMessage("{}"),
		SourceTokens:        10,
		SummaryTokens:       20,
	}
	err := f.compactions.Create(ctx, bad)
	if err == nil {
		t.Fatal("摘要 token 比原文多，竟然存进去了 —— 数据库那条 CHECK 没生效")
	}
	if !strings.Contains(err.Error(), "token_count_check") {
		t.Errorf("报错里没带上约束名，排查时看不出根因: %v", err)
	}
}

// ---- 共享记忆（shared_context_memories，第 5 步）----

// newMemory 存一条记忆，落库后回填 ID。
//
// 不走事务：这张表没有需要原子分配的东西（没有序号列），单条 INSERT 本身就是一个隐式事务。
func (f *memberCTestFixture) newMemory(t *testing.T, ctx context.Context, memory *entity.SharedContextMemory) *entity.SharedContextMemory {
	t.Helper()

	if err := f.memories.Create(ctx, memory); err != nil {
		t.Fatalf("存共享记忆失败: %v", err)
	}
	return memory
}

// conversationMemory 造一条"对话级"记忆：只在本条对话里有效。
//
// conversation_id 必须填：库上有一条 CHECK 要求 scope = 'conversation' 时它不能为空。
func (f *memberCTestFixture) conversationMemory(memoryType, content string, importance int16) *entity.SharedContextMemory {
	return &entity.SharedContextMemory{
		ClassroomID:    f.classroom.ID,
		ConversationID: &f.conversation.ID,
		Scope:          entity.MemoryScopeConversation,
		MemoryType:     memoryType,
		Content:        content,
		Importance:     importance,
		Status:         entity.MemoryStatusActive,
	}
}

// classroomMemory 造一条"课堂级"记忆：同一门课的其他对话也能用。
//
// conversation_id 必须留空：反过来那条 CHECK 要求 scope = 'classroom' 时它必须为空。
func (f *memberCTestFixture) classroomMemory(content string, importance int16) *entity.SharedContextMemory {
	return &entity.SharedContextMemory{
		ClassroomID: f.classroom.ID,
		Scope:       entity.MemoryScopeClassroom,
		MemoryType:  entity.MemoryTypeFact,
		Content:     content,
		Importance:  importance,
		Status:      entity.MemoryStatusActive,
	}
}

// newOtherConversation 造同一门课下的第二条对话，用来验证"对话级记忆不会串到别的话题去"。
//
// 收尾用 t.Cleanup 而不是 defer：Cleanup 在用例函数返回之后才执行，正好排在 defer f.cleanup()
// 后面 —— 先删主体、再删这条附属数据，互不干扰。
func (f *memberCTestFixture) newOtherConversation(t *testing.T) *entity.ClassroomConversation {
	t.Helper()

	conversation := &entity.ClassroomConversation{
		ClassroomID: f.classroom.ID,
		Title:       "另一条对话（隔离性验证）",
		Type:        entity.ConversationTypeDiscussion,
		Status:      entity.ConversationStatusActive,
	}
	if err := f.conversations.Create(context.Background(), conversation); err != nil {
		t.Fatalf("建第二条对话失败: %v", err)
	}

	t.Cleanup(func() {
		f.db.Where("conversation_id = ?", conversation.ID).Delete(&entity.SharedContextMemory{})
		f.db.Where("id = ?", conversation.ID).Delete(&entity.ClassroomConversation{})
	})
	return conversation
}

// newOtherClassroom 造另一门课，用来验证"课堂级记忆不会串到别的课去"。
func (f *memberCTestFixture) newOtherClassroom(t *testing.T) *entity.Classroom {
	t.Helper()

	classroom := &entity.Classroom{
		Title:            "另一门课（隔离性验证）",
		Requirement:      "测试数据，跑完即删",
		Mode:             entity.ClassroomModeInteractive,
		Status:           entity.ClassroomStatusPlayable,
		GenerationConfig: json.RawMessage("{}"),
		AgentConfig:      json.RawMessage("{}"),
	}
	if err := f.db.WithContext(context.Background()).Create(classroom).Error; err != nil {
		t.Fatalf("建第二门课失败: %v", err)
	}

	t.Cleanup(func() {
		f.db.Where("classroom_id = ?", classroom.ID).Delete(&entity.SharedContextMemory{})
		f.db.Where("id = ?", classroom.ID).Delete(&entity.Classroom{})
	})
	return classroom
}

// memoryIDs 把记忆列表压成 ID 列表，方便断言顺序。
func memoryIDs(memories []entity.SharedContextMemory) []uint64 {
	ids := make([]uint64, 0, len(memories))
	for _, memory := range memories {
		ids = append(ids, memory.ID)
	}
	return ids
}

// equalIDs 比较两个 ID 列表是否完全一致（含顺序）。
func equalIDs(got, want []uint64) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

// TestMemberCSharedMemoryEmptyReturnsEmptyList 验证"一条记忆都没有"是空列表而不是错误。
//
// 编排每次组装上下文都会问一句"有没有记忆"，第一次当然没有 —— 那是正常流程，不是异常。
func TestMemberCSharedMemoryEmptyReturnsEmptyList(t *testing.T) {
	f := newMemberCTestFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	memories, err := f.memories.ListForContext(ctx, f.classroom.ID, f.conversation.ID, 10)
	if err != nil {
		t.Fatalf("没有任何记忆时不该报错，实际返回: %v", err)
	}
	if len(memories) != 0 {
		t.Fatalf("没有任何记忆时应返回空列表，实际 %d 条", len(memories))
	}
}

// TestMemberCSharedMemoryListCoversBothScopes 验证"该带上的带上、不该带上的都不带"。
//
// 两条正例：本条对话的对话级记忆 + 同一门课的课堂级记忆。
// 两条反例（重要度都填 5，比正例高）：同一门课**另条对话**的对话级记忆、**另一门课**的课堂级记忆。
// 反例的重要度故意设得最高 —— 过滤一旦写错，它们会排在最前面，一眼就能看出是"串了"。
//
// ⚠️ 这个用例的分辨力**全靠那两条反例**，不是靠正例：
// 夹具每次只建一间课、一条对话，两张表的序列同步递增，所以 `classroom.ID == conversation.ID` 恒成立。
// 也就是说，"按对话过滤"和"按课堂过滤"两种写法对**本条对话**的对话级记忆都会命中，单看正例分不出来。
// 而两条反例的 id 是错开的（另一条对话 451 / 另一门课 451，都不等于本课 450）：
//   - 把对话级误写成按 classroom_id 过滤 → 反例会露出来（它的 classroom_id 本课有值）；
//   - 把课堂级误写成按 conversation_id 过滤 → 正例里的课堂级那条会消失（它的 conversation_id 是空的）；
//   - 干脆漏掉一条 ID 条件 → 两条反例都会露出来。
//
// 三种写法都逃不掉。
func TestMemberCSharedMemoryListCoversBothScopes(t *testing.T) {
	f := newMemberCTestFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	own := f.newMemory(t, ctx, f.conversationMemory(entity.MemoryTypeFact, "用户叫熊大", 3))
	sameClassroom := f.newMemory(t, ctx, f.classroomMemory("这门课统一先确认枪口安全", 4))

	otherConversation := f.newOtherConversation(t)
	f.newMemory(t, ctx, &entity.SharedContextMemory{
		ClassroomID:    f.classroom.ID,
		ConversationID: &otherConversation.ID,
		Scope:          entity.MemoryScopeConversation,
		MemoryType:     entity.MemoryTypeFact,
		Content:        "在另一条对话里说的话",
		Importance:     5,
		Status:         entity.MemoryStatusActive,
	})

	otherClassroom := f.newOtherClassroom(t)
	f.newMemory(t, ctx, &entity.SharedContextMemory{
		ClassroomID: otherClassroom.ID,
		Scope:       entity.MemoryScopeClassroom,
		MemoryType:  entity.MemoryTypeFact,
		Content:     "在另一门课里确认的事",
		Importance:  5,
		Status:      entity.MemoryStatusActive,
	})

	memories, err := f.memories.ListForContext(ctx, f.classroom.ID, f.conversation.ID, 10)
	if err != nil {
		t.Fatalf("读取共享记忆失败: %v", err)
	}
	if len(memories) != 2 {
		t.Fatalf("取到 %d 条记忆，期望 2 条（本条对话的对话级 + 本门课的课堂级），实际 ID = %v",
			len(memories), memoryIDs(memories))
	}
	// 重要度 4 排在 3 前面。
	if memories[0].ID != sameClassroom.ID || memories[1].ID != own.ID {
		t.Errorf("顺序不对：ID = %v，期望 [%d %d]", memoryIDs(memories), sameClassroom.ID, own.ID)
	}
}

// TestMemberCSharedMemorySkipsInactiveAndExpired 验证只有"生效且没过期"的记忆才会被读出来。
//
// 三条反例都填重要度 5（最高），过滤写错时它们会排在合法记忆前面 —— 失败信息一眼可见。
// 撤回（retracted）与取代（superseded）都覆盖：表上允许三种状态，但只有 active 是"现在还成立"。
func TestMemberCSharedMemorySkipsInactiveAndExpired(t *testing.T) {
	f := newMemberCTestFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	active := f.newMemory(t, ctx, f.conversationMemory(entity.MemoryTypeFact, "现在仍成立的结论", 3))

	superseded := f.conversationMemory(entity.MemoryTypeFact, "已经被新结论取代", 5)
	superseded.Status = entity.MemoryStatusSuperseded
	f.newMemory(t, ctx, superseded)

	retracted := f.conversationMemory(entity.MemoryTypeFact, "已经被撤回", 5)
	retracted.Status = entity.MemoryStatusRetracted
	f.newMemory(t, ctx, retracted)

	expired := f.conversationMemory(entity.MemoryTypeFact, "已经过期", 5)
	past := time.Now().UTC().Add(-time.Hour)
	expired.ExpiresAt = &past
	f.newMemory(t, ctx, expired)

	notYetExpired := f.conversationMemory(entity.MemoryTypeFact, "还没到期", 1)
	later := time.Now().UTC().Add(time.Hour)
	notYetExpired.ExpiresAt = &later
	f.newMemory(t, ctx, notYetExpired)

	memories, err := f.memories.ListForContext(ctx, f.classroom.ID, f.conversation.ID, 10)
	if err != nil {
		t.Fatalf("读取共享记忆失败: %v", err)
	}
	if len(memories) != 2 {
		t.Fatalf("取到 %d 条记忆，期望 2 条（生效的 + 没过期的），实际 ID = %v",
			len(memories), memoryIDs(memories))
	}
	if memories[0].ID != active.ID || memories[1].ID != notYetExpired.ID {
		t.Errorf("取到的记忆 ID = %v，期望 [%d %d]", memoryIDs(memories), active.ID, notYetExpired.ID)
	}
}

// TestMemberCSharedMemoryOrdersByImportanceAndRespectsLimit 验证按重要度倒序取、且真的按 limit 截断。
//
// 同样重要度时用 id 倒序兜底：没有这个兜底，同重要度的行顺序由数据库随意决定，
// 每次跑出来可能不一样（"这次带了这几条、下次带了那几条"），行为不可复现。
func TestMemberCSharedMemoryOrdersByImportanceAndRespectsLimit(t *testing.T) {
	f := newMemberCTestFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	low := f.newMemory(t, ctx, f.conversationMemory(entity.MemoryTypeFact, "次要的", 2))
	highFirst := f.newMemory(t, ctx, f.conversationMemory(entity.MemoryTypePreference, "最重要的（先写）", 5))
	middle := f.newMemory(t, ctx, f.conversationMemory(entity.MemoryTypeDecision, "中间的", 3))
	highSecond := f.newMemory(t, ctx, f.conversationMemory(entity.MemoryTypeOpenQuestion, "最重要的（后写）", 5))

	wantAll := []uint64{highSecond.ID, highFirst.ID, middle.ID, low.ID}

	all, err := f.memories.ListForContext(ctx, f.classroom.ID, f.conversation.ID, 10)
	if err != nil {
		t.Fatalf("读取共享记忆失败: %v", err)
	}
	if !equalIDs(memoryIDs(all), wantAll) {
		t.Errorf("全部记忆的 ID = %v，期望 %v（按重要度倒序，同重要度按 id 倒序）", memoryIDs(all), wantAll)
	}

	limited, err := f.memories.ListForContext(ctx, f.classroom.ID, f.conversation.ID, 2)
	if err != nil {
		t.Fatalf("读取共享记忆失败: %v", err)
	}
	if !equalIDs(memoryIDs(limited), wantAll[:2]) {
		t.Errorf("limit=2 时取到的 ID = %v，期望 %v（只留重要度最高的两条）", memoryIDs(limited), wantAll[:2])
	}
}
