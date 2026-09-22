package discussion

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"gorm.io/gorm"

	"narra/internal/model/entity"
	"narra/internal/repository"
	"narra/pkg/config"
	"narra/pkg/database"
	"narra/pkg/logger"
)

// 编排链路端到端测试（真连本机 PostgreSQL）。
//
// 默认跳过，由 NARRA_INTEGRATION_TEST=1 打开。要跑它：
//
//	$env:NARRA_INTEGRATION_TEST = '1'
//	go test ./internal/agent/discussion/ -run TestOrchestrator -v
//
// 为什么必须连真库：这一层要验证的是"一次讨论有没有被如实记进库"——
// 运行建没建、每个回合有没有挂上产出消息、序号连不连续、失败时留没留下痕迹。
// 这些答案只在真实的表、约束和事务里，内存假仓储给不出来。
//
// 测试用的模型是 FakeModel（见 model.go），全程不调真实大模型：
// 编排写库对不对，和模型答得好不好，是必须分开验证的两件事。
const integrationEnv = "NARRA_INTEGRATION_TEST"

var (
	testDBOnce sync.Once
	testDB     *gorm.DB
	testDBErr  error
)

// openTestDB 返回共享的测试数据库连接。
//
// 与 internal/repository 的测试各留一份，是因为 Go 的测试辅助函数不能跨包引用；
// 为了这几十行去建一个测试支撑包，对这个规模的项目不划算。
func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	if os.Getenv(integrationEnv) == "" {
		t.Skipf("跳过集成测试：设置 %s=1 后重跑（需要本机 PostgreSQL 可用）", integrationEnv)
	}

	testDBOnce.Do(func() {
		// go test 的工作目录是包目录，而配置里的日志路径是相对仓库根写的，
		// 先切到仓库根再初始化，否则会在包目录下生成一个 logs/。
		root := findRepoRoot(t)
		configPath := filepath.Join(root, "configs", "config.yaml")
		if err := os.Chdir(root); err != nil {
			testDBErr = fmt.Errorf("切换工作目录失败: %w", err)
			return
		}

		cfg, err := config.Load(configPath)
		if err != nil {
			testDBErr = fmt.Errorf("加载配置失败: %w", err)
			return
		}
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

// findRepoRoot 从当前目录往上找，直到找到 go.mod。
//
// 不写死"往上两级"这种相对层数：那样包一挪位置就会静默失效，而且失效的表现是
// "配置文件找不到"，看不出是路径算错了。往上找 go.mod 则与包放在哪儿无关。
func findRepoRoot(t *testing.T) string {
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
			t.Fatalf("从 %s 一路往上都没找到 go.mod", dir)
		}
		dir = parent
	}
}

// fixture 是一套测试数据与各仓储句柄。
type fixture struct {
	classroom    *entity.Classroom
	conversation *entity.ClassroomConversation
	trigger      *entity.ConversationMessage

	// participants 是五个**已经落库**的课堂角色。测试按需取前 n 个上圆桌。
	participants []Participant

	presetAgentIDs []uint64

	conversations repository.ConversationRepository
	messages      repository.MessageRepository
	runs          repository.RunRepository
	turns         repository.TurnRepository
	compactions   repository.ContextCompactionRepository
	tx            repository.TransactionManager

	cleanup func()
}

// roleTemplates 是造角色用的模板。
//
// agent_key 与 sort_order 都带 test 前缀/大偏移：这两列上有唯一约束，
// 用固定的小值会和种子数据（0002_seed_preset_agents.sql 里的六个角色）撞上。
var roleTemplates = []struct {
	key, name, role, roleType, persona string
}{
	{"test-orch-teacher", "张老师", "主讲", entity.PresetAgentRoleTypeTeacher, "讲得慢，爱举生活里的例子"},
	{"test-orch-curious", "好奇的小明", "提问", entity.PresetAgentRoleTypeStudent, "爱追问为什么，不满足于第一个答案"},
	{"test-orch-rigorous", "严谨的小红", "质疑", entity.PresetAgentRoleTypeStudent, "爱挑漏洞，要求给出依据"},
	{"test-orch-associative", "爱联想的小刚", "联想", entity.PresetAgentRoleTypeStudent, "喜欢把新东西类比成旧经验"},
	{"test-orch-quiet", "沉默的小美", "补充", entity.PresetAgentRoleTypeStudent, "话少，但每次开口都补在别人漏掉的地方"},
}

// newFixture 造出"课程 → 对话 → 用户消息 → 五个角色"这条前缀链路。
//
// 角色必须真的落库：agent_turns.classroom_agent_id 和 conversation_messages.classroom_agent_id
// 都有指向 classroom_agents 的外键，用编造的 ID 会直接被数据库拒绝。
//
// 顺带说明本包的边界——编排**不认识**角色表，"圆桌上有谁"是上层（这里扮演上层）
// 查好、组装成 []Participant 再传进去的。将来接真实业务时，这一步由 service 层做。
func newFixture(t *testing.T) *fixture {
	t.Helper()

	db := openTestDB(t)
	ctx := context.Background()

	classroom := &entity.Classroom{
		Title:            "编排链路集成测试",
		Requirement:      "测试数据，跑完即删",
		Mode:             entity.ClassroomModeInteractive,
		Status:           entity.ClassroomStatusPlayable,
		GenerationConfig: json.RawMessage("{}"),
		AgentConfig:      json.RawMessage("{}"),
	}
	if err := db.WithContext(ctx).Create(classroom).Error; err != nil {
		t.Fatalf("建测试课程失败: %v", err)
	}

	f := &fixture{
		classroom:     classroom,
		conversations: repository.NewConversationRepository(db),
		messages:      repository.NewMessageRepository(db),
		runs:          repository.NewRunRepository(db),
		turns:         repository.NewTurnRepository(db),
		compactions:   repository.NewContextCompactionRepository(db),
		tx:            repository.NewTransactionManager(db),
	}

	f.cleanup = func() {
		// 按依赖顺序显式删除，不依赖外键级联：级联规则是另一层的东西，
		// 测试不该把"库里一定干净"建立在它没被改过这个假设上。
		// 顺序里有两处非删不可的先手：agent_turns 不先删，运行记录会被级联带走但看不出来；
		// classroom_agents 不先删，preset_agents 会因为 RESTRICT 删不掉。
		runIDs := db.Model(&entity.OrchestrationRun{}).Select("id").Where("conversation_id = ?", f.conversation.ID)
		db.Where("run_id IN (?)", runIDs).Delete(&entity.AgentTurn{})
		db.Where("conversation_id = ?", f.conversation.ID).Delete(&entity.OrchestrationRun{})
		db.Where("conversation_id = ?", f.conversation.ID).Delete(&entity.ConversationMessage{})
		// 摘要在对话之后才可能存在，删对话时数据库会把它级联带走；这里仍然显式删一次，
		// 理由和上面一样 —— 清理不该建立在"级联规则没被改过"这个假设上。
		db.Where("conversation_id = ?", f.conversation.ID).Delete(&entity.ContextCompaction{})
		db.Where("id = ?", f.conversation.ID).Delete(&entity.ClassroomConversation{})
		db.Where("classroom_id = ?", f.classroom.ID).Delete(&entity.ClassroomAgent{})
		if len(f.presetAgentIDs) > 0 {
			db.Where("id IN ?", f.presetAgentIDs).Delete(&entity.PresetAgent{})
		}
		db.Where("id = ?", f.classroom.ID).Delete(&entity.Classroom{})

		var leftovers int64
		db.Model(&entity.ConversationMessage{}).Where("conversation_id = ?", f.conversation.ID).Count(&leftovers)
		db.Model(&entity.OrchestrationRun{}).Where("conversation_id = ?", f.conversation.ID).Count(&leftovers)
		if leftovers != 0 {
			t.Errorf("测试数据未清理干净：对话 %d 下仍有 %d 条记录", f.conversation.ID, leftovers)
		}
	}

	conversation := &entity.ClassroomConversation{
		ClassroomID: classroom.ID,
		Title:       "编排测试对话",
		Type:        entity.ConversationTypeDiscussion,
		Status:      entity.ConversationStatusActive,
	}
	if err := f.conversations.Create(ctx, conversation); err != nil {
		f.cleanup()
		t.Fatalf("建测试对话失败: %v", err)
	}
	f.conversation = conversation

	trigger := &entity.ConversationMessage{
		ConversationID: conversation.ID,
		SenderType:     entity.MessageSenderUser,
		SenderSnapshot: json.RawMessage(`{"name":"用户"}`),
		Content:        "为什么天空是蓝色的？",
		Status:         entity.MessageStatusCompleted,
		Metadata:       json.RawMessage("{}"),
	}
	if err := f.tx.Run(ctx, func(ctx context.Context) error {
		return f.messages.AppendNext(ctx, trigger)
	}); err != nil {
		f.cleanup()
		t.Fatalf("建触发消息失败: %v", err)
	}
	f.trigger = trigger

	for index, template := range roleTemplates {
		preset := &entity.PresetAgent{
			AgentKey:  template.key,
			Name:      template.name,
			Role:      template.role,
			RoleType:  template.roleType,
			Persona:   template.persona,
			Avatar:    "user.png",
			Color:     "#722ed1",
			VoiceID:   "test-voice",
			SortOrder: int32(9000 + index),
			Enabled:   true,
		}
		if err := db.WithContext(ctx).Create(preset).Error; err != nil {
			f.cleanup()
			t.Fatalf("建预置角色 %s 失败: %v", template.key, err)
		}
		f.presetAgentIDs = append(f.presetAgentIDs, preset.ID)

		link := &entity.ClassroomAgent{
			ClassroomID: classroom.ID,
			AgentID:     preset.ID,
			VoiceID:     "test-voice",
		}
		if err := db.WithContext(ctx).Create(link).Error; err != nil {
			f.cleanup()
			t.Fatalf("把角色 %s 挂到课程上失败: %v", template.key, err)
		}

		f.participants = append(f.participants, Participant{
			ClassroomAgentID: link.ID,
			Name:             preset.Name,
			Role:             preset.RoleType,
			Persona:          preset.Persona,
		})
	}

	return f
}

// newOrchestrator 用假模型和轮流选人装配一个编排器。
func (f *fixture) newOrchestrator(t *testing.T, model Model) *Orchestrator {
	t.Helper()

	orchestrator, err := New(Deps{
		Tx:            f.tx,
		Conversations: f.conversations,
		Messages:      f.messages,
		Runs:          f.runs,
		Turns:         f.turns,
		Model:         model,
		Director:      RoundRobinDirector{},
	})
	if err != nil {
		t.Fatalf("装配编排器失败: %v", err)
	}
	return orchestrator
}

// TestOrchestratorRunsFullDiscussion 验证整条链路：全员说完 → 自然收尾。
func TestOrchestratorRunsFullDiscussion(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	const onStage = 3
	roundtable := f.participants[:onStage]
	orchestrator := f.newOrchestrator(t, FakeModel{})

	result, err := orchestrator.Run(ctx, Request{
		ConversationID:   f.conversation.ID,
		TriggerMessageID: f.trigger.ID,
		Participants:     roundtable,
		MaxTurns:         6,
	})
	if err != nil {
		t.Fatalf("跑讨论失败: %v", err)
	}

	// ---- 返回值 ----
	if result.Status != entity.RunStatusCompleted {
		t.Errorf("运行状态 = %s，期望 %s", result.Status, entity.RunStatusCompleted)
	}
	if result.StopReason != entity.RunStopCompleted {
		t.Errorf("停止原因 = %s，期望 %s（三个人各说一次后应自然结束，而不是撞上限）",
			result.StopReason, entity.RunStopCompleted)
	}
	if len(result.Turns) != onStage {
		t.Fatalf("回合数 = %d，期望 %d", len(result.Turns), onStage)
	}
	for i, turn := range result.Turns {
		if turn.AgentName != roundtable[i].Name {
			t.Errorf("第 %d 个发言的是 %s，期望 %s（轮流策略下顺序应与人选顺序一致）",
				i+1, turn.AgentName, roundtable[i].Name)
		}
		if turn.TurnNo != int16(i+1) {
			t.Errorf("第 %d 个回合的 turn_no = %d，期望 %d", i+1, turn.TurnNo, i+1)
		}
		if turn.MessageID == 0 {
			t.Errorf("第 %d 个回合没有产出消息", i+1)
		}
	}

	// ---- 库里的运行记录 ----
	run, err := f.runs.FindByID(ctx, result.RunID)
	if err != nil {
		t.Fatalf("回查运行失败: %v", err)
	}
	if run.AttemptNo != 1 {
		t.Errorf("attempt_no = %d，期望 1", run.AttemptNo)
	}
	if run.Status != entity.RunStatusCompleted {
		t.Errorf("库里运行状态 = %s，期望 %s", run.Status, entity.RunStatusCompleted)
	}
	if len(run.TraceID) != 32 {
		t.Errorf("trace_id = %q，长度 %d，期望 32 位十六进制", run.TraceID, len(run.TraceID))
	}
	if run.StartedAt == nil || run.FinishedAt == nil {
		t.Errorf("运行的开始/结束时间没有写全：started=%v finished=%v", run.StartedAt, run.FinishedAt)
	}
	if run.StopReason == nil || *run.StopReason != entity.RunStopCompleted {
		t.Errorf("库里 stop_reason = %v，期望 %s", run.StopReason, entity.RunStopCompleted)
	}

	// ---- 库里的回合记录 ----
	turns, err := f.turns.ListByRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("回查回合失败: %v", err)
	}
	if len(turns) != onStage {
		t.Fatalf("库里回合数 = %d，期望 %d", len(turns), onStage)
	}
	for i, turn := range turns {
		if turn.TurnNo != int16(i+1) {
			t.Errorf("库里第 %d 个回合 turn_no = %d", i+1, turn.TurnNo)
		}
		if turn.Status != entity.AgentTurnStatusCompleted {
			t.Errorf("第 %d 个回合状态 = %s，期望 %s", i+1, turn.Status, entity.AgentTurnStatusCompleted)
		}
		if turn.OutputMessageID == nil {
			t.Errorf("第 %d 个回合没有挂产出消息（前端点开消息就回溯不到是哪一轮说的）", i+1)
		}
		if turn.ClassroomAgentID == nil {
			t.Errorf("第 %d 个回合没有记发言角色", i+1)
		}
		if len(turn.AgentSnapshot) == 0 || string(turn.AgentSnapshot) == "{}" {
			t.Errorf("第 %d 个回合的角色快照是空的", i+1)
		}
	}

	// ---- 库里的消息：1 条用户 + N 条 Agent，序号必须连续 ----
	messages, err := f.messages.ListByConversation(ctx, f.conversation.ID, 0, 100)
	if err != nil {
		t.Fatalf("回查消息失败: %v", err)
	}
	if len(messages) != onStage+1 {
		t.Fatalf("消息数 = %d，期望 %d（1 条用户 + %d 条 Agent）", len(messages), onStage+1, onStage)
	}
	for i, message := range messages {
		if message.SequenceNo != int64(i+1) {
			t.Errorf("第 %d 条消息序号 = %d，期望 %d", i, message.SequenceNo, i+1)
		}
	}
	if messages[0].SenderType != entity.MessageSenderUser {
		t.Errorf("第 1 条应该是用户消息，实际是 %s", messages[0].SenderType)
	}
	for i, message := range messages[1:] {
		if message.SenderType != entity.MessageSenderAgent {
			t.Errorf("第 %d 条应该是 Agent 消息，实际是 %s", i+2, message.SenderType)
		}
		if message.TokenCount <= 0 {
			t.Errorf("第 %d 条 Agent 消息没有记 token 数", i+2)
		}
	}

	// ---- 对话的"最近一条消息时间"要被推上去，否则课堂列表排序会错 ----
	conversation, err := f.conversations.FindByID(ctx, f.conversation.ID)
	if err != nil {
		t.Fatalf("回查对话失败: %v", err)
	}
	if conversation.LastMessageAt == nil {
		t.Errorf("对话的 last_message_at 没有被更新")
	}
}

// TestOrchestratorStopsAtMaxTurns 验证"人比回合上限多"时由上限收尾。
func TestOrchestratorStopsAtMaxTurns(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	orchestrator := f.newOrchestrator(t, FakeModel{})

	result, err := orchestrator.Run(ctx, Request{
		ConversationID:   f.conversation.ID,
		TriggerMessageID: f.trigger.ID,
		Participants:     f.participants, // 五个人
		MaxTurns:         2,              // 但只允许说两轮
	})
	if err != nil {
		t.Fatalf("跑讨论失败: %v", err)
	}

	if result.StopReason != entity.RunStopMaxTurns {
		t.Errorf("停止原因 = %s，期望 %s", result.StopReason, entity.RunStopMaxTurns)
	}
	if len(result.Turns) != 2 {
		t.Errorf("回合数 = %d，期望 2（被上限截断）", len(result.Turns))
	}

	run, err := f.runs.FindByID(ctx, result.RunID)
	if err != nil {
		t.Fatalf("回查运行失败: %v", err)
	}
	if run.StopReason == nil || *run.StopReason != entity.RunStopMaxTurns {
		t.Errorf("库里 stop_reason = %v，期望 %s", run.StopReason, entity.RunStopMaxTurns)
	}
	// "被上限截断"仍然是一次正常结束，状态应是 completed 而不是 failed ——
	// 否则运维会把正常的长度限制当成故障。
	if run.Status != entity.RunStatusCompleted {
		t.Errorf("库里运行状态 = %s，期望 %s", run.Status, entity.RunStatusCompleted)
	}
}

// TestOrchestratorRejectsBadInput 验证参数在 Go 侧就被拦下，不用等到写库报约束冲突。
func TestOrchestratorRejectsBadInput(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	orchestrator := f.newOrchestrator(t, FakeModel{})

	cases := []struct {
		name    string
		request Request
	}{
		{
			name:    "没有参与者",
			request: Request{ConversationID: f.conversation.ID, TriggerMessageID: f.trigger.ID},
		},
		{
			name: "最大回合数超出数据库允许范围",
			request: Request{
				ConversationID:   f.conversation.ID,
				TriggerMessageID: f.trigger.ID,
				Participants:     f.participants[:1],
				MaxTurns:         99,
			},
		},
		{
			name:    "缺少对话 ID",
			request: Request{TriggerMessageID: f.trigger.ID, Participants: f.participants[:1]},
		},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if _, err := orchestrator.Run(ctx, item.request); err == nil {
				t.Errorf("期望报错，实际通过了")
			}
		})
	}

	// 被拒绝的请求不该在库里留下任何运行记录。
	runs, err := f.runs.ListByConversation(ctx, f.conversation.ID, 10)
	if err != nil {
		t.Fatalf("回查运行失败: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("参数校验失败却留下了 %d 条运行记录", len(runs))
	}
}

// failingModel 永远失败，用来验证失败路径。
type failingModel struct{}

func (failingModel) Generate(ctx context.Context, request GenerationRequest) (GenerationResponse, error) {
	return GenerationResponse{}, fmt.Errorf("模拟模型故障")
}

// TestOrchestratorMarksFailure 验证失败时留下可查的痕迹。
//
// 这一条比"跑通"更重要：真实环境里模型超时、网络抖动都会走到这里，
// 而"这次讨论为什么没跑完"必须能在库里查到 —— 否则前端一直转圈，运维无从下手。
func TestOrchestratorMarksFailure(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	orchestrator := f.newOrchestrator(t, failingModel{})

	result, err := orchestrator.Run(ctx, Request{
		ConversationID:   f.conversation.ID,
		TriggerMessageID: f.trigger.ID,
		Participants:     f.participants[:2],
		MaxTurns:         4,
	})
	if err == nil {
		t.Fatalf("模型失败时应当返回错误")
	}

	if result.Status != entity.RunStatusFailed {
		t.Errorf("返回值里的状态 = %s，期望 %s", result.Status, entity.RunStatusFailed)
	}

	run, loadErr := f.runs.FindByID(ctx, result.RunID)
	if loadErr != nil {
		t.Fatalf("回查运行失败: %v", loadErr)
	}
	if run.Status != entity.RunStatusFailed {
		t.Errorf("库里运行状态 = %s，期望 %s", run.Status, entity.RunStatusFailed)
	}
	if run.StopReason == nil || *run.StopReason != entity.RunStopError {
		t.Errorf("库里 stop_reason = %v，期望 %s", run.StopReason, entity.RunStopError)
	}
	if run.ErrorMessage == nil || *run.ErrorMessage == "" {
		t.Errorf("库里没有记下失败原因")
	}
	if run.FinishedAt == nil {
		t.Errorf("失败的运行没有结束时间，会一直显示成'进行中'")
	}

	// 失败发生在第一轮：那条回合应该被标成失败，而不是永远停在 running。
	turns, loadErr := f.turns.ListByRun(ctx, run.ID)
	if loadErr != nil {
		t.Fatalf("回查回合失败: %v", loadErr)
	}
	if len(turns) != 1 {
		t.Fatalf("回合数 = %d，期望 1（第一轮就失败了）", len(turns))
	}
	if turns[0].Status != entity.AgentTurnStatusFailed {
		t.Errorf("失败回合状态 = %s，期望 %s（停在 running 会让前端一直转圈）",
			turns[0].Status, entity.AgentTurnStatusFailed)
	}

	// 一轮都没成功，所以除了用户那条之外不该有别的消息。
	messages, loadErr := f.messages.ListByConversation(ctx, f.conversation.ID, 0, 100)
	if loadErr != nil {
		t.Fatalf("回查消息失败: %v", loadErr)
	}
	if len(messages) != 1 {
		t.Errorf("消息数 = %d，期望 1（只有用户那条）", len(messages))
	}
}

// TestOrchestratorSecondAttemptIsNumbered 验证重试会新增一次执行而不是覆盖旧的。
func TestOrchestratorSecondAttemptIsNumbered(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	orchestrator := f.newOrchestrator(t, FakeModel{})
	request := Request{
		ConversationID:   f.conversation.ID,
		TriggerMessageID: f.trigger.ID,
		Participants:     f.participants[:1],
		MaxTurns:         1,
	}

	first, err := orchestrator.Run(ctx, request)
	if err != nil {
		t.Fatalf("第一次讨论失败: %v", err)
	}
	second, err := orchestrator.Run(ctx, request)
	if err != nil {
		t.Fatalf("第二次讨论失败: %v", err)
	}
	if first.RunID == second.RunID {
		t.Fatalf("两次执行用了同一条运行记录")
	}

	firstRun, err := f.runs.FindByID(ctx, first.RunID)
	if err != nil {
		t.Fatalf("回查第一次运行失败: %v", err)
	}
	secondRun, err := f.runs.FindByID(ctx, second.RunID)
	if err != nil {
		t.Fatalf("回查第二次运行失败: %v", err)
	}
	if firstRun.AttemptNo != 1 || secondRun.AttemptNo != 2 {
		t.Errorf("attempt_no 依次是 %d、%d，期望 1、2", firstRun.AttemptNo, secondRun.AttemptNo)
	}
	if firstRun.TraceID == secondRun.TraceID {
		t.Errorf("两次执行的 trace_id 相同，无法区分两次链路")
	}

	// 同一批角色说了两轮，消息序号要继续往上涨而不是重新从 1 开始。
	messages, err := f.messages.ListByConversation(ctx, f.conversation.ID, 0, 100)
	if err != nil {
		t.Fatalf("回查消息失败: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("消息数 = %d，期望 3（1 条用户 + 两次执行各 1 条）", len(messages))
	}
	for i, message := range messages {
		if message.SequenceNo != int64(i+1) {
			t.Errorf("第 %d 条消息序号 = %d，期望 %d", i, message.SequenceNo, i+1)
		}
	}
}

// TestOrchestratorDefaultMaxTurns 验证不传上限时有默认值，且这个默认值本身合法。
func TestOrchestratorDefaultMaxTurns(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	orchestrator := f.newOrchestrator(t, FakeModel{})

	result, err := orchestrator.Run(ctx, Request{
		ConversationID:   f.conversation.ID,
		TriggerMessageID: f.trigger.ID,
		Participants:     f.participants[:2],
		MaxTurns:         0,
	})
	if err != nil {
		t.Fatalf("跑讨论失败: %v", err)
	}
	if result.StopReason != entity.RunStopCompleted {
		t.Errorf("停止原因 = %s，期望 %s", result.StopReason, entity.RunStopCompleted)
	}

	run, err := f.runs.FindByID(ctx, result.RunID)
	if err != nil {
		t.Fatalf("回查运行失败: %v", err)
	}
	if run.MaxTurns != defaultMaxTurns {
		t.Errorf("库里 max_turns = %d，期望默认值 %d", run.MaxTurns, defaultMaxTurns)
	}
	if run.FinishedAt == nil || run.StartedAt == nil {
		t.Errorf("运行的起止时间没写全")
	}
	if run.FinishedAt.Before(*run.StartedAt) {
		t.Errorf("结束时间早于开始时间")
	}
}
