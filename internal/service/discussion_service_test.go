package service

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

	"narra/internal/agent/discussion"
	"narra/internal/model/entity"
	"narra/internal/repository"
	"narra/pkg/config"
	"narra/pkg/database"
	apperrors "narra/pkg/errors"
	"narra/pkg/logger"
)

// 讨论触发入口的端到端测试（真连本机 PostgreSQL）。
//
// 默认跳过，由 NARRA_INTEGRATION_TEST=1 打开。要跑它：
//
//	$env:NARRA_INTEGRATION_TEST = '1'
//	go test ./internal/service/ -run TestDiscussionStart -v
//
// 为什么必须连真库：这里要验证的是"用户发一句话之后，那条消息到底有没有落到库里、
// 讨论到底有没有真的跑起来、跑出来的东西是不是记在了它该在的对话上"。
// 这三件事的答案只在真实的表、约束和事务里 —— 尤其"参与者是从课堂角色表里拼出来的"
// 这一条，假仓储只能证明自己写对了自己。
//
// 讨论用的模型是替身（不调真实大模型）：本用例要验的是入口链路，不是模型答得好不好。
const discussionIntegrationEnv = "NARRA_INTEGRATION_TEST"

var (
	discussionTestDBOnce sync.Once
	discussionTestDB     *gorm.DB
	discussionTestDBErr  error
)

// openDiscussionTestDB 返回共享的测试数据库连接。
//
// 与 internal/repository、internal/agent/discussion 的测试各留一份：Go 的测试辅助函数
// 不能跨包引用，而这个项目还没有测试支撑包。
func openDiscussionTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	if os.Getenv(discussionIntegrationEnv) == "" {
		t.Skipf("跳过集成测试：设置 %s=1 后重跑（需要本机 PostgreSQL 可用）", discussionIntegrationEnv)
	}

	discussionTestDBOnce.Do(func() {
		// go test 的工作目录是包目录，而配置里的日志路径相对仓库根写，先切过去，
		// 否则会在包目录下生成一个 logs/。
		root := findDiscussionRepoRoot(t)
		configPath := filepath.Join(root, "configs", "config.yaml")
		if err := os.Chdir(root); err != nil {
			discussionTestDBErr = fmt.Errorf("切换工作目录失败: %w", err)
			return
		}

		cfg, err := config.Load(configPath)
		if err != nil {
			discussionTestDBErr = fmt.Errorf("加载配置失败: %w", err)
			return
		}
		if err := logger.Init(&cfg.Log); err != nil {
			discussionTestDBErr = fmt.Errorf("初始化日志失败: %w", err)
			return
		}
		discussionTestDB, discussionTestDBErr = database.InitPostgres(&cfg.Database.Postgres)
	})
	if discussionTestDBErr != nil {
		t.Fatalf("%v", discussionTestDBErr)
	}
	return discussionTestDB
}

// findDiscussionRepoRoot 从当前目录往上找，直到找到 go.mod。
func findDiscussionRepoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("取当前工作目录失败: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("向上找不到 go.mod，无法定位仓库根目录")
		}
		dir = parent
	}
}

// discussionFixture 是一堂课 + 三个角色 + 一条对话的现成场景，外加全部仓储。
type discussionFixture struct {
	db *gorm.DB

	classroom    *entity.Classroom
	conversation *entity.ClassroomConversation
	roleNames    []string // 与 linkIDs 一一对应，顺序即角色池的 sort_order
	linkIDs      []uint64 // classroom_agents.id，写进消息与回合的归属字段

	conversations repository.ConversationRepository
	classrooms    repository.ClassroomRepository
	scenes        repository.SceneRepository
	segments      repository.SceneSegmentRepository
	agents        repository.ClassroomAgentRepository
	roles         repository.RoleRepository
	messages      repository.MessageRepository
	runs          repository.RunRepository
	turns         repository.TurnRepository
	compactions   repository.ContextCompactionRepository
	memories      repository.SharedMemoryRepository
	events        repository.ConversationEventRepository
	tx            repository.TransactionManager

	cleanup func()
}

// newDiscussionFixture 建一套可用数据并装配全部依赖。
func newDiscussionFixture(t *testing.T) *discussionFixture {
	t.Helper()

	db := openDiscussionTestDB(t)
	suffix := randomSuffix(t)
	// 课程先入库，角色的全局排序号随后用它的 ID 分段。这样每套夹具的三个
	// sort_order 都不同，即使 go test 并行跑 service 与 discussion 两个包也不会撞。
	classroom := &entity.Classroom{
		Title:            "擦枪安全讨论课",
		Requirement:      "讲清楚操作前为什么要先确认枪口安全",
		Mode:             entity.ClassroomModeVocational,
		Status:           entity.ClassroomStatusPlayable,
		GenerationConfig: json.RawMessage(`{"llm_provider_id":1,"llm_model_id":"qwen-max","web_search":false,"bio":""}`),
		AgentConfig:      json.RawMessage(`{"agent_mode":"preset"}`),
	}
	if err := db.Create(classroom).Error; err != nil {
		t.Fatalf("建测试课程失败: %v", err)
	}

	// 角色用固定的三种身份，名字带随机后缀：preset_agents.agent_key 有唯一约束，
	// 用例并行或上次异常退出留下残留时，随机后缀能保证每次都建得出来。
	roleSpecs := []struct {
		key      string
		name     string
		role     string
		roleType string
		persona  string
	}{
		{"test-c-teacher-" + suffix, "陈老师", "主讲", entity.PresetAgentRoleTypeTeacher, "说话稳，先给结论再讲理由"},
		{"test-c-curious-" + suffix, "好奇宝宝", "提问", entity.PresetAgentRoleTypeStudent, "爱追问为什么，从不放过含糊的说法"},
		{"test-c-notetaker-" + suffix, "笔记君", "记录", entity.PresetAgentRoleTypeStudent, "把讨论收敛成几条可执行的结论"},
	}

	roles := make([]entity.PresetAgent, 0, len(roleSpecs))
	for index, spec := range roleSpecs {
		roles = append(roles, entity.PresetAgent{
			AgentKey: spec.key,
			Name:     spec.name,
			Role:     spec.role,
			RoleType: spec.roleType,
			Persona:  spec.persona,
			Avatar:   "/avatars/teacher-2.png",
			Color:    "#722ed1",
			VoiceID:  "zh-CN-XiaoxiaoNeural",
			// sort_order 全局唯一。课堂 ID 由数据库分配，故跨包并行测试时每套夹具占的
			// 三个位置也不同；同一堂课内 index 保证角色顺序稳定。
			SortOrder: int32(classroom.ID*10) + int32(index),
			Enabled:   true,
		})
	}
	if err := db.Create(&roles).Error; err != nil {
		t.Fatalf("建测试角色失败: %v", err)
	}

	links := make([]entity.ClassroomAgent, 0, len(roles))
	for _, role := range roles {
		links = append(links, entity.ClassroomAgent{
			ClassroomID: classroom.ID,
			AgentID:     role.ID,
			VoiceID:     role.VoiceID,
		})
	}
	// 刻意打乱写入顺序：读取侧必须按角色池的 sort_order 排，不能依赖插入顺序。
	if err := db.Create(&links[2]).Error; err != nil {
		t.Fatalf("建课堂角色关联失败: %v", err)
	}
	if err := db.Create(&links[0]).Error; err != nil {
		t.Fatalf("建课堂角色关联失败: %v", err)
	}
	if err := db.Create(&links[1]).Error; err != nil {
		t.Fatalf("建课堂角色关联失败: %v", err)
	}

	conversation := &entity.ClassroomConversation{
		ClassroomID: classroom.ID,
		Title:       "擦枪安全",
		Type:        entity.ConversationTypeDiscussion,
		Status:      entity.ConversationStatusActive,
	}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("建测试对话失败: %v", err)
	}

	byRole := map[uint64]uint64{} // preset_agents.id → classroom_agents.id
	for _, link := range links {
		byRole[link.AgentID] = link.ID
	}
	names := make([]string, 0, len(roles))
	linkIDs := make([]uint64, 0, len(roles))
	for _, role := range roles { // roles 已按 sort_order 排列
		names = append(names, role.Name)
		linkIDs = append(linkIDs, byRole[role.ID])
	}

	f := &discussionFixture{
		db:            db,
		classroom:     classroom,
		conversation:  conversation,
		roleNames:     names,
		linkIDs:       linkIDs,
		conversations: repository.NewConversationRepository(db),
		classrooms:    repository.NewClassroomRepository(db),
		scenes:        repository.NewSceneRepository(db),
		segments:      repository.NewSceneSegmentRepository(db),
		agents:        repository.NewClassroomAgentRepository(db),
		roles:         repository.NewRoleRepository(db),
		messages:      repository.NewMessageRepository(db),
		runs:          repository.NewRunRepository(db),
		turns:         repository.NewTurnRepository(db),
		compactions:   repository.NewContextCompactionRepository(db),
		memories:      repository.NewSharedMemoryRepository(db),
		events:        repository.NewConversationEventRepository(db),
		tx:            repository.NewTransactionManager(db),
	}

	f.cleanup = func() {
		// 先删挂在对话上的过程数据，再删对话与课程：外键顺序反了会被数据库拒绝。
		db.Where("conversation_id = ?", conversation.ID).Delete(&entity.ConversationEvent{})
		db.Exec("DELETE FROM agent_turns WHERE run_id IN (SELECT id FROM orchestration_runs WHERE conversation_id = ?)", conversation.ID)
		db.Where("conversation_id = ?", conversation.ID).Delete(&entity.ContextCompaction{})
		db.Where("classroom_id = ?", classroom.ID).Delete(&entity.SharedContextMemory{})
		db.Where("conversation_id = ?", conversation.ID).Delete(&entity.ConversationMessage{})
		db.Where("conversation_id = ?", conversation.ID).Delete(&entity.OrchestrationRun{})
		db.Where("id = ?", conversation.ID).Delete(&entity.ClassroomConversation{})
		db.Where("classroom_id = ?", classroom.ID).Delete(&entity.ClassroomAgent{})
		db.Where("id = ?", classroom.ID).Delete(&entity.Classroom{})
		roleIDs := make([]uint64, 0, len(roles))
		for _, role := range roles {
			roleIDs = append(roleIDs, role.ID)
		}
		db.Where("id IN ?", roleIDs).Delete(&entity.PresetAgent{})
	}

	return f
}

// discussionTestModel 是测试需要的三种模型能力合起来。
//
// 讨论要三样能力（发言 / 摘要 / 提炼），这里合成一个约束，用例只传一个替身就够了。
// 生产装配里这三样可以来自不同的实现 —— 那正是把它们拆成三个接口的意义。
type discussionTestModel interface {
	discussion.Model
	discussion.Summarizer
	discussion.MemoryExtractor
}

// staticDiscussionModelFactory 是模型工厂的替身：不管课程配了什么，都给同一套模型。
//
// 记录被问了哪一组 ID，是为了验证"入口确实把课程快照里的配置传下去了"。
type staticDiscussionModelFactory struct {
	model discussionTestModel

	// errsByCall 按调用次序给出每次要报的错；短了（或为 nil）就一律成功。
	//
	// 做成"按次"而不是一个固定的 err：验证"失败之后锁有没有放开"需要第一次失败、
	// 第二次成功 —— 两次都给同一个错的话，第二次被拒的原因既可能是锁没放，
	// 也可能是模型又没建起来，用例就分不清了。
	errsByCall []error

	askedProviderID uint64
	askedModelID    string
	calls           int
}

func (f *staticDiscussionModelFactory) Build(_ context.Context, providerID uint64, modelID string) (discussion.Models, error) {
	call := f.calls
	f.calls++
	f.askedProviderID = providerID
	f.askedModelID = modelID

	if call < len(f.errsByCall) && f.errsByCall[call] != nil {
		return discussion.Models{}, f.errsByCall[call]
	}
	return discussion.Models{Model: f.model, Summarizer: f.model, Extractor: f.model}, nil
}

// newService 用给定模型装配一个讨论入口。
func (f *discussionFixture) newService(t *testing.T, model discussionTestModel) DiscussionService {
	t.Helper()

	return f.newServiceWithFactory(t, model, &staticDiscussionModelFactory{model: model})
}

// newServiceWithFactory 用指定的模型工厂装配一个讨论入口。
//
// 工厂单独传是为了让用例能换成一个"注定失败"的工厂，验证配置错误时库里的状态。
func (f *discussionFixture) newServiceWithFactory(t *testing.T, model discussionTestModel, factory DiscussionModelFactory) DiscussionService {
	t.Helper()

	orchestrator, err := discussion.New(discussion.Deps{
		Tx:            f.tx,
		Conversations: f.conversations,
		Messages:      f.messages,
		Runs:          f.runs,
		Turns:         f.turns,
		Compactions:   f.compactions,
		Memories:      f.memories,
		Events:        f.events,
		Spans:         repository.NewAgentTraceSpanRepository(f.db),
		Model:         model,
		Summarizer:    model,
		Extractor:     model,
		Director:      discussion.TurnTakingDirector{},
	})
	if err != nil {
		t.Fatalf("装配编排器失败: %v", err)
	}

	return NewDiscussionService(DiscussionDeps{
		Conversations: f.conversations,
		Classrooms:    f.classrooms,
		Scenes:        f.scenes,
		Segments:      f.segments,
		Agents:        f.agents,
		Roles:         f.roles,
		Messages:      f.messages,
		Tx:            f.tx,
		Orchestrator:  orchestrator,
		ModelFactory:  factory,
		Timeout:       30 * time.Second,
	})
}

// countMessages 数这条对话下的消息条数。
func (f *discussionFixture) countMessages(t *testing.T) int64 {
	t.Helper()
	var count int64
	f.db.Model(&entity.ConversationMessage{}).Where("conversation_id = ?", f.conversation.ID).Count(&count)
	return count
}

// waitUntil 轮询等待条件成立，超时即判定失败。
//
// 讨论是异步跑的，用例要等它落库；固定 sleep 要么白等要么偶发失败，
// 轮询到条件成立就立刻继续，既快又稳。
func waitUntil(t *testing.T, timeout time.Duration, condition func() bool, message string) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("等待超时（%s）：%s", timeout, message)
}

// randomSuffix 生成一小段随机后缀，避免与库里已有数据撞唯一约束。
func randomSuffix(t *testing.T) string {
	t.Helper()

	var buffer [4]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		t.Fatalf("生成随机后缀失败: %v", err)
	}
	return hex.EncodeToString(buffer[:])
}

// slowModel 是一个故意慢的替身：给"同一对话不能同时跑两趟"的用例留出撞车窗口。
//
// 用固定 sleep 而不是阻塞通道：用例只需"第二个请求进来时第一个还在跑"，
// 不需要精确控制先后。
type slowModel struct {
	discussion.FakeModel
	delay time.Duration
}

// markedModel 是"会自报家门"的替身：发言正文里带上一个记号。
//
// 用来验证"这一趟讨论跑的确实是工厂刚建出来的那套模型"—— 光看工厂被调用过不够，
// 还得在落库的内容里找到它的痕迹。
type markedModel struct {
	discussion.FakeModel
	marker string
}

type lessonRecordingModel struct {
	discussion.FakeModel
	materials chan string
}

func (m lessonRecordingModel) Generate(ctx context.Context, request discussion.GenerationRequest) (discussion.GenerationResponse, error) {
	select {
	case m.materials <- request.LessonMaterial:
	default:
	}
	return m.FakeModel.Generate(ctx, request)
}

// Generate 在假回复前面盖上记号。
func (m markedModel) Generate(ctx context.Context, request discussion.GenerationRequest) (discussion.GenerationResponse, error) {
	response, err := m.FakeModel.Generate(ctx, request)
	if err != nil {
		return response, err
	}
	response.Content = m.marker + response.Content
	return response, nil
}

// Generate 睡一会儿再交给假模型。
func (m slowModel) Generate(ctx context.Context, request discussion.GenerationRequest) (discussion.GenerationResponse, error) {
	select {
	case <-ctx.Done():
		return discussion.GenerationResponse{}, ctx.Err()
	case <-time.After(m.delay):
	}
	return m.FakeModel.Generate(ctx, request)
}

// TestDiscussionStartRejectsEmptyContent 空内容不该开跑。
func TestDiscussionStartRejectsEmptyContent(t *testing.T) {
	f := newDiscussionFixture(t)
	defer f.cleanup()

	svc := f.newService(t, discussion.FakeModel{})
	if _, err := svc.Start(context.Background(), f.conversation.ID, "   "); err == nil {
		t.Fatal("空内容应当被拒绝，实际却开跑了")
	}
	if got := f.countMessages(t); got != 0 {
		t.Errorf("被拒绝的请求不该落下消息，实际落了 %d 条", got)
	}
}

// TestDiscussionStartRejectsMissingConversation 对话不存在时返回"找不到"，而不是 500。
func TestDiscussionStartRejectsMissingConversation(t *testing.T) {
	f := newDiscussionFixture(t)
	defer f.cleanup()

	svc := f.newService(t, discussion.FakeModel{})
	_, err := svc.Start(context.Background(), f.conversation.ID+999999, "在吗")
	if err == nil {
		t.Fatal("对话不存在时应当报错")
	}
	var biz *apperrors.BizError
	if !errors.As(err, &biz) || biz.Code != apperrors.CodeNotFound {
		t.Fatalf("期望 404 业务错误，实际是 %v", err)
	}
}

// TestDiscussionStartRejectsClosedConversation 已结束的对话不该再开讨论。
func TestDiscussionStartRejectsClosedConversation(t *testing.T) {
	f := newDiscussionFixture(t)
	defer f.cleanup()

	if err := f.db.Model(&entity.ClassroomConversation{}).
		Where("id = ?", f.conversation.ID).
		Update("status", entity.ConversationStatusClosed).Error; err != nil {
		t.Fatalf("关闭对话失败: %v", err)
	}

	svc := f.newService(t, discussion.FakeModel{})
	_, err := svc.Start(context.Background(), f.conversation.ID, "在吗")
	if err == nil {
		t.Fatal("已结束的对话应当拒绝开讨论")
	}
	if got := f.countMessages(t); got != 0 {
		t.Errorf("被拒绝的请求不该落下消息，实际落了 %d 条", got)
	}
}

// TestDiscussionStartRejectsConversationWithoutAgents 圆桌上没人时不该开跑。
//
// 这一条挡的是"跑起来才发现没参与者"：那时运行记录已经建了，
// 用户看到的是一次莫名其妙失败的讨论，而不是一句"这堂课还没有角色"。
func TestDiscussionStartRejectsConversationWithoutAgents(t *testing.T) {
	f := newDiscussionFixture(t)
	defer f.cleanup()

	if err := f.db.Where("classroom_id = ?", f.classroom.ID).
		Delete(&entity.ClassroomAgent{}).Error; err != nil {
		t.Fatalf("清空课堂角色失败: %v", err)
	}

	svc := f.newService(t, discussion.FakeModel{})
	_, err := svc.Start(context.Background(), f.conversation.ID, "在吗")
	if err == nil {
		t.Fatal("没有角色时应当拒绝开讨论")
	}
	if got := f.countMessages(t); got != 0 {
		t.Errorf("被拒绝的请求不该落下消息，实际落了 %d 条", got)
	}
}

// TestDiscussionStartRejectsMissingModelConfig 课程快照里没有模型时不该开跑。
//
// 现在用的还是替身模型，看不出差别 —— 但接上真实大模型之后，没有模型配置就是
// "跑起来必定失败"。提前在这里拦住，用户收到的是一句明确的话，而不是等到中间才崩。
func TestDiscussionStartRejectsMissingModelConfig(t *testing.T) {
	f := newDiscussionFixture(t)
	defer f.cleanup()

	if err := f.db.Model(&entity.Classroom{}).
		Where("id = ?", f.classroom.ID).
		Update("generation_config", json.RawMessage(`{}`)).Error; err != nil {
		t.Fatalf("清空课程模型快照失败: %v", err)
	}

	svc := f.newService(t, discussion.FakeModel{})
	if _, err := svc.Start(context.Background(), f.conversation.ID, "在吗"); err == nil {
		t.Fatal("课程没有模型配置时应当拒绝开讨论")
	}
}

// TestDiscussionStartPersistsUserMessage 用户那句话必须落库，并返回它的 ID。
//
// 这条消息有两个身份：它是用户屏幕上看到的那句话，也是这次运行的触发消息
// （orchestration_runs.trigger_message_id 指向它）。没落库就没有触发点。
func TestDiscussionStartPersistsUserMessage(t *testing.T) {
	f := newDiscussionFixture(t)
	defer f.cleanup()

	svc := f.newService(t, discussion.FakeModel{})
	result, err := svc.Start(context.Background(), f.conversation.ID, "为什么操作前要先确认枪口安全？")
	if err != nil {
		t.Fatalf("发起讨论失败: %v", err)
	}

	if result.ConversationID != f.conversation.ID {
		t.Errorf("返回的对话 ID = %d，期望 %d", result.ConversationID, f.conversation.ID)
	}

	var message entity.ConversationMessage
	if err := f.db.Where("id = ?", result.MessageID).First(&message).Error; err != nil {
		t.Fatalf("按返回的消息 ID 查不到消息: %v", err)
	}
	if message.SenderType != entity.MessageSenderUser {
		t.Errorf("触发消息的发言方 = %q，期望 %q", message.SenderType, entity.MessageSenderUser)
	}
	if message.ClassroomAgentID != nil {
		t.Errorf("用户消息不该归属任何角色，实际是 %d", *message.ClassroomAgentID)
	}
	if message.Status != entity.MessageStatusCompleted {
		t.Errorf("触发消息状态 = %q，期望 %q", message.Status, entity.MessageStatusCompleted)
	}
	if message.Content != "为什么操作前要先确认枪口安全？" {
		t.Errorf("落库的正文 = %q，与提交的不一致", message.Content)
	}
	// 序号从 1 开始：这条是对话里的第一条消息。
	if message.SequenceNo != 1 {
		t.Errorf("第一条消息的序号 = %d，期望 1", message.SequenceNo)
	}

	// 对话的"最近消息时间"要跟着推：课堂列表按它排序，漏掉活跃对话会沉底。
	var conversation entity.ClassroomConversation
	if err := f.db.Where("id = ?", f.conversation.ID).First(&conversation).Error; err != nil {
		t.Fatalf("重查对话失败: %v", err)
	}
	if conversation.LastMessageAt == nil {
		t.Error("对话的 last_message_at 没有更新，课堂列表会把这条活跃对话排到后面")
	}
}

// TestDiscussionStartRunsDiscussionToCompletion 发起之后，讨论要真的在后台跑完。
//
// 这是整个入口的存在意义：一条用户消息 → 一趟多 Agent 讨论 → 每个角色各说一句，
// 全部落库、事件齐备。前端订阅事件流看到的正是这个过程。
func TestDiscussionStartRunsDiscussionToCompletion(t *testing.T) {
	f := newDiscussionFixture(t)
	defer f.cleanup()

	svc := f.newService(t, discussion.FakeModel{})
	if _, err := svc.Start(context.Background(), f.conversation.ID, "为什么操作前要先确认枪口安全？"); err != nil {
		t.Fatalf("发起讨论失败: %v", err)
	}

	// 三个角色各说一句（替身模型不给"下一步动作"，选人策略走默认轮换，全员说完即收尾）。
	waitUntil(t, 20*time.Second, func() bool {
		return f.countMessages(t) == 1+int64(len(f.roleNames))
	}, "等待三个角色各说一句")
	if got := f.countMessages(t); got != 1+int64(len(f.roleNames)) {
		t.Fatalf("消息数 = %d，期望 %d（1 条用户消息 + %d 条角色发言）", got, 1+len(f.roleNames), len(f.roleNames))
	}

	// 角色发言必须挂到这条对话上、且归属到具体的课堂角色。
	var agentMessages []entity.ConversationMessage
	f.db.Where("conversation_id = ? AND sender_type = ?", f.conversation.ID, entity.MessageSenderAgent).
		Order("sequence_no ASC").Find(&agentMessages)
	if len(agentMessages) != len(f.roleNames) {
		t.Fatalf("角色消息 = %d 条，期望 %d 条", len(agentMessages), len(f.roleNames))
	}
	for index, message := range agentMessages {
		if message.ClassroomAgentID == nil {
			t.Fatalf("第 %d 条角色消息没有归属角色", index+1)
		}
		if message.SequenceNo != int64(index+2) {
			t.Errorf("第 %d 条角色消息的序号 = %d，期望 %d（紧接用户消息之后）", index+1, message.SequenceNo, index+2)
		}
	}

	// 运行记录收尾为"完成"：这一趟活真的跑完了，而不是挂在 running 上。
	var runs []entity.OrchestrationRun
	f.db.Where("conversation_id = ?", f.conversation.ID).Find(&runs)
	if len(runs) != 1 {
		t.Fatalf("运行记录 = %d 条，期望 1 条", len(runs))
	}
	waitUntil(t, 10*time.Second, func() bool {
		var current entity.OrchestrationRun
		f.db.Where("id = ?", runs[0].ID).First(&current)
		return current.Status == entity.RunStatusCompleted
	}, "等待运行收尾为 completed")

	// 事件要齐：前端靠它们把过程画出来。
	var eventCount int64
	f.db.Model(&entity.ConversationEvent{}).Where("conversation_id = ?", f.conversation.ID).Count(&eventCount)
	if eventCount == 0 {
		t.Fatal("事件表里一条都没有，前端订阅这条流会什么都看不到")
	}
}

// TestDiscussionParticipantsFollowRoleOrder 圆桌上的参与者按角色池顺序拼出来。
//
// 直接从 run.started 事件的载荷里读：那是前端拿到"桌上有谁"的同一份数据，
// 用事件来验，等于同时验了"拼得对"和"发得对"。
func TestDiscussionParticipantsFollowRoleOrder(t *testing.T) {
	f := newDiscussionFixture(t)
	defer f.cleanup()

	svc := f.newService(t, discussion.FakeModel{})
	if _, err := svc.Start(context.Background(), f.conversation.ID, "在吗"); err != nil {
		t.Fatalf("发起讨论失败: %v", err)
	}

	var event entity.ConversationEvent
	waitUntil(t, 10*time.Second, func() bool {
		return f.db.Where("conversation_id = ? AND event_type = ?", f.conversation.ID, entity.ConversationEventRunStarted).
			First(&event).Error == nil
	}, "等待 run.started 事件落库")

	var payload struct {
		Participants []struct {
			AgentID uint64 `json:"agent_id"`
			Name    string `json:"name"`
			Role    string `json:"role"`
		} `json:"participants"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("解析 run.started 载荷失败: %v", err)
	}
	if len(payload.Participants) != len(f.roleNames) {
		t.Fatalf("参与者 = %d 人，期望 %d 人", len(payload.Participants), len(f.roleNames))
	}
	for index, participant := range payload.Participants {
		if participant.Name != f.roleNames[index] {
			t.Errorf("第 %d 位是 %q，期望 %q（参与者必须按角色池的展示顺序排）",
				index+1, participant.Name, f.roleNames[index])
		}
		// agent_id 必须是 classroom_agents.id（本课程的角色实例），不是 preset_agents.id：
		// 消息与回合的归属字段用的就是它，给错了直接写不进库。
		if participant.AgentID != f.linkIDs[index] {
			t.Errorf("第 %d 位的 agent_id = %d，期望 %d（classroom_agents.id）",
				index+1, participant.AgentID, f.linkIDs[index])
		}
		if participant.Role == "" {
			t.Errorf("第 %d 位没有带身份（role）", index+1)
		}
	}
}

// TestDiscussionStartRejectsWhileAnotherRunInFlight 同一条对话同时只跑一趟。
//
// 两趟讨论并行写同一条对话，前端看到的会是两场讨论交错在一起。
// 拦在入口，用户收到的是"稍后再试"，而不是一屏看不懂的内容。
func TestDiscussionStartRejectsWhileAnotherRunInFlight(t *testing.T) {
	f := newDiscussionFixture(t)
	defer f.cleanup()

	// 模型故意慢：让第一趟还没跑完时第二个请求就进来。
	svc := f.newService(t, slowModel{delay: 300 * time.Millisecond})

	if _, err := svc.Start(context.Background(), f.conversation.ID, "第一句"); err != nil {
		t.Fatalf("第一次发起讨论失败: %v", err)
	}
	if _, err := svc.Start(context.Background(), f.conversation.ID, "第二句"); err == nil {
		t.Fatal("上一趟还在跑时，第二个请求应当被拒绝")
	}

	// 拒绝的那次不该落下消息：库里的第一条消息内容仍是"第一句"。
	var messages []entity.ConversationMessage
	f.db.Where("conversation_id = ?", f.conversation.ID).Order("sequence_no ASC").Find(&messages)
	if len(messages) == 0 || messages[0].Content != "第一句" {
		t.Fatalf("库里的消息不对：%+v", messages)
	}
	for _, message := range messages {
		if message.Content == "第二句" {
			t.Error("被拒绝的请求不该把消息写进库")
		}
	}
}

// TestDiscussionStartReleasesLockAfterFinish 跑完之后要能再发起下一轮。
//
// 与上一条配对：那条证明"跑的时候拦住"，这条证明"跑完必须放开"。
// 少了这条，一个忘了释放的锁会让对话只能讨论一次。
//
// 注意这里等的是"整趟活彻底结束"，不只是"最后一条消息落库"：
// 消息写完还有收尾（写运行终态、提炼记忆）要做，锁在那之后才放开。
// 所以用重试代替固定等待 —— 中途被拒是正常的（还没跑完），成功即说明锁已放开。
func TestDiscussionStartReleasesLockAfterFinish(t *testing.T) {
	f := newDiscussionFixture(t)
	defer f.cleanup()

	svc := f.newService(t, discussion.FakeModel{})
	ctx := context.Background()

	if _, err := svc.Start(ctx, f.conversation.ID, "第一句"); err != nil {
		t.Fatalf("第一次发起讨论失败: %v", err)
	}
	// 等这一趟把三条角色发言都写完（说明主循环已经跑完了）。
	waitUntil(t, 20*time.Second, func() bool {
		return f.countMessages(t) == 1+int64(len(f.roleNames))
	}, "等待第一趟讨论的主循环跑完")

	var secondErr error
	waitUntil(t, 20*time.Second, func() bool {
		_, secondErr = svc.Start(ctx, f.conversation.ID, "第二句")
		return secondErr == nil
	}, "等待第一趟彻底结束、锁被放开")
	if secondErr != nil {
		t.Fatalf("上一趟跑完后应当可以再发起讨论，实际失败: %v", secondErr)
	}
}

// TestDiscussionStartRejectsModelBuildFailure 模型建不出来时不落库、不留痕。
//
// 这是"配置错误必须挡在落库之前"那条规矩的用例：模型列表空、密钥换了环境解不开、
// 地址是空的，都属于"每次都会失败"的错。要是先落库再建模型，用户会先看到自己那句话
// 出现在屏幕上，然后一场注定失败的讨论转半天圈 —— 而且库里还多了一条没人理的记录。
//
// 第二次发起**必须真的跑起来**：让它同样撞在 Build 失败上的话，"第二次被拒"就同时
// 有两种解释（锁没放开 / 模型又没建起来），那条断言等于什么都没证明。
// 所以工厂按调用次序放行：第一次报错，之后成功。
func TestDiscussionStartRejectsModelBuildFailure(t *testing.T) {
	f := newDiscussionFixture(t)
	defer f.cleanup()

	factory := &staticDiscussionModelFactory{
		model:      discussion.FakeModel{},
		errsByCall: []error{errors.New("大模型配置的模型列表为空")},
	}
	svc := f.newServiceWithFactory(t, discussion.FakeModel{}, factory)

	_, err := svc.Start(context.Background(), f.conversation.ID, "在吗")
	if err == nil {
		t.Fatal("模型建不出来时应当拒绝开讨论")
	}
	var biz *apperrors.BizError
	if !errors.As(err, &biz) || biz.Code != apperrors.CodeInternalError {
		t.Fatalf("期望 500 业务错误，实际是 %v", err)
	}

	if got := f.countMessages(t); got != 0 {
		t.Errorf("失败时不该落下消息，实际落了 %d 条", got)
	}
	var runCount int64
	f.db.Model(&entity.OrchestrationRun{}).Where("conversation_id = ?", f.conversation.ID).Count(&runCount)
	if runCount != 0 {
		t.Errorf("失败时不该留下运行记录，实际有 %d 条", runCount)
	}
	var eventCount int64
	f.db.Model(&entity.ConversationEvent{}).Where("conversation_id = ?", f.conversation.ID).Count(&eventCount)
	if eventCount != 0 {
		t.Errorf("失败时不该发事件，实际有 %d 条", eventCount)
	}

	// 锁要放开：这一趟根本没跑起来，不放的话这条对话就永远开不了讨论了。
	// 第二次一定成功（工厂已放行），所以能走到"三个角色各说一句"就同时证明了
	// 锁已放开、且这趟走的是完整的正常链路。
	if _, err := svc.Start(context.Background(), f.conversation.ID, "再试一次"); err != nil {
		t.Fatalf("上一趟失败后应当可以再发起讨论，实际失败: %v", err)
	}
	waitUntil(t, 20*time.Second, func() bool {
		return f.countMessages(t) == 1+int64(len(f.roleNames))
	}, "等待第二次讨论跑完三个角色的发言")

	// 第二次的那条用户消息也落了库，说明它确实进了正常链路而不是被静默丢弃。
	var messages []entity.ConversationMessage
	f.db.Where("conversation_id = ? AND sender_type = ?", f.conversation.ID, entity.MessageSenderUser).
		Order("sequence_no ASC").Find(&messages)
	if len(messages) != 1 || messages[0].Content != "再试一次" {
		t.Fatalf("库里的用户消息应当是第二次那句，实际是 %+v", messages)
	}
}

// TestDiscussionStartBuildsModelFromSnapshot 入口要把课程快照里的模型配置交给工厂。
//
// 快照里的两个 ID 是"这堂课该用哪个模型"的唯一来源（fixture 里是 1 与 qwen-max）。
// 传错或传空的话，几堂课会共用同一个模型 —— 而且不会报任何错。
func TestDiscussionStartBuildsModelFromSnapshot(t *testing.T) {
	f := newDiscussionFixture(t)
	defer f.cleanup()

	factory := &staticDiscussionModelFactory{model: discussion.FakeModel{}}
	svc := f.newServiceWithFactory(t, discussion.FakeModel{}, factory)

	if _, err := svc.Start(context.Background(), f.conversation.ID, "在吗"); err != nil {
		t.Fatalf("发起讨论失败: %v", err)
	}

	if factory.calls != 1 {
		t.Errorf("模型工厂被调用了 %d 次，期望 1 次（每趟讨论建一套）", factory.calls)
	}
	if factory.askedProviderID != 1 {
		t.Errorf("传给工厂的服务商 ID = %d，期望 1（课程快照里的 llm_provider_id）", factory.askedProviderID)
	}
	if factory.askedModelID != "qwen-max" {
		t.Errorf("传给工厂的模型 ID = %q，期望 qwen-max（课程快照里的 llm_model_id）", factory.askedModelID)
	}
}

// TestDiscussionRunsWithBuiltModel 后台那趟讨论跑的必须是工厂刚建出来的模型。
//
// 光验证"工厂被调用了"不够：还得证明建出来的那套真的被用上了。
// 这里让工厂返回的模型在发言里留个记号，再从落库的角色消息里把它找出来。
func TestDiscussionRunsWithBuiltModel(t *testing.T) {
	f := newDiscussionFixture(t)
	defer f.cleanup()

	factory := &staticDiscussionModelFactory{model: markedModel{marker: "工厂出品"}}
	svc := f.newServiceWithFactory(t, discussion.FakeModel{}, factory)

	if _, err := svc.Start(context.Background(), f.conversation.ID, "在吗"); err != nil {
		t.Fatalf("发起讨论失败: %v", err)
	}

	waitUntil(t, 20*time.Second, func() bool {
		return f.countMessages(t) == 1+int64(len(f.roleNames))
	}, "等待三个角色各说一句")

	var messages []entity.ConversationMessage
	f.db.Where("conversation_id = ? AND sender_type = ?", f.conversation.ID, entity.MessageSenderAgent).
		Order("sequence_no ASC").Find(&messages)
	if len(messages) == 0 {
		t.Fatal("一条角色消息都没有")
	}
	for index, message := range messages {
		if !strings.Contains(message.Content, "工厂出品") {
			t.Errorf("第 %d 条角色发言不是工厂建的模型说的：%q", index+1, message.Content)
		}
	}
}

func TestDiscussionStartPassesGeneratedSceneToModel(t *testing.T) {
	f := newDiscussionFixture(t)
	defer f.cleanup()

	scene := &entity.Scene{
		ClassroomID: f.classroom.ID,
		SortOrder:   0,
		Type:        entity.SceneTypeSlide,
		Title:       "认识 Agent",
		Status:      entity.SceneStatusReady,
		Content:     json.RawMessage(`{"blocks":[{"key":"agent-definition","type":"paragraph","content":"Agent 会观察环境反馈并调整行动。"}]}`),
	}
	if err := f.db.Create(scene).Error; err != nil {
		t.Fatalf("写入测试课件失败: %v", err)
	}
	materials := make(chan string, 1)
	model := lessonRecordingModel{materials: materials}
	svc := f.newService(t, model)
	if _, err := svc.StartAtScene(context.Background(), f.conversation.ID, "第一页讲什么？", scene.ID); err != nil {
		t.Fatalf("发起课件讨论失败: %v", err)
	}
	select {
	case material := <-materials:
		if !strings.Contains(material, "Agent 会观察环境反馈并调整行动") {
			t.Fatalf("模型没有收到真实课件正文: %s", material)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("讨论模型没有收到课件请求")
	}
	waitUntil(t, 20*time.Second, func() bool {
		var count int64
		f.db.Model(&entity.OrchestrationRun{}).Where("conversation_id = ? AND status <> ?", f.conversation.ID, entity.RunStatusRunning).Count(&count)
		return count > 0
	}, "等待讨论结束后清理测试课堂")
}

func TestDiscussionStartRejectsSceneOutsideClassroom(t *testing.T) {
	f := newDiscussionFixture(t)
	defer f.cleanup()
	other := newDiscussionFixture(t)
	defer other.cleanup()
	foreignScene := &entity.Scene{
		ClassroomID: other.classroom.ID,
		SortOrder:   0,
		Type:        entity.SceneTypeSlide,
		Title:       "另一堂课的第一页",
		Status:      entity.SceneStatusReady,
		Content:     json.RawMessage(`{"blocks":[]}`),
	}
	if err := other.db.Create(foreignScene).Error; err != nil {
		t.Fatalf("写入另一堂课的课件失败: %v", err)
	}
	svc := f.newService(t, discussion.FakeModel{})
	if _, err := svc.StartAtScene(context.Background(), f.conversation.ID, "当前页讲什么？", foreignScene.ID); err == nil {
		t.Fatal("其他课堂的课件页 ID 应被拒绝")
	}
	if count := f.countMessages(t); count != 0 {
		t.Fatalf("无效课件页不应落用户消息，实际有 %d 条", count)
	}
}
