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
	classroom     *entity.Classroom
	conversation  *entity.ClassroomConversation
	conversations ConversationRepository
	messages      MessageRepository
	runs          RunRepository
	turns         TurnRepository
	compactions   ContextCompactionRepository
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
		classroom:     classroom,
		conversations: NewConversationRepository(db),
		messages:      NewMessageRepository(db),
		runs:          NewRunRepository(db),
		turns:         NewTurnRepository(db),
		compactions:   NewContextCompactionRepository(db),
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
