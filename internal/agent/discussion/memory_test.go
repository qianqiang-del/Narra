package discussion

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"go.uber.org/zap"

	"narra/internal/model/entity"
	"narra/internal/repository"
)

// 第 5 步（跨对话共享记忆）的测试。
//
// 分两段，和 context_test.go 一样：
//   - 组装与归一化是纯逻辑，用内存替身测（TestMemory*，不连库、跑得快）；
//   - "记忆到底有没有落库、有没有真的进上下文"只能连真库看
//     （TestMemoryIntegration*，复用 orchestrator_test.go 的夹具，要 NARRA_INTEGRATION_TEST=1）。

// ---- 替身 ----

// stubMemories 只实现读记忆那一个方法。
//
// 嵌入接口而不是逐个实现：本包用不到的留成 nil，真被误调用会当场 panic ——
// 比悄悄返回一份假数据更容易发现"用错了方法"。
type stubMemories struct {
	repository.SharedMemoryRepository

	memories []entity.SharedContextMemory
	err      error

	calls             int
	gotClassroomID    uint64
	gotConversationID uint64
	gotLimit          int
}

func (s *stubMemories) ListForContext(_ context.Context, classroomID uint64, conversationID uint64, limit int) ([]entity.SharedContextMemory, error) {
	s.calls++
	s.gotClassroomID = classroomID
	s.gotConversationID = conversationID
	s.gotLimit = limit
	if s.err != nil {
		return nil, s.err
	}
	return s.memories, nil
}

// stubExtractor 是"提炼记忆"的替身：返回预设的候选，并记下收到的材料。
type stubExtractor struct {
	candidates []MemoryCandidate
	err        error

	calls      int
	gotHistory []HistoryMessage
}

func (s *stubExtractor) Extract(_ context.Context, history []HistoryMessage) ([]MemoryCandidate, error) {
	s.calls++
	s.gotHistory = history
	if s.err != nil {
		return nil, s.err
	}
	return s.candidates, nil
}

// recordingModel 记下最后一次发言请求的上下文，供"记忆有没有真的进上下文"断言。
//
// 刻意**不嵌入** FakeModel：嵌入会把 Summarize / Extract 一起带进来，
// 那样就分不清"这次是用它当模型，还是当摘要器/提炼器"了。
type recordingModel struct {
	calls       int
	lastRequest GenerationRequest
}

func (m *recordingModel) Generate(ctx context.Context, request GenerationRequest) (GenerationResponse, error) {
	m.calls++
	m.lastRequest = request
	return FakeModel{}.Generate(ctx, request)
}

// newStep5Orchestrator 只装配共享记忆相关的那几个依赖。
//
// 不走 New()：那个函数会校验全部依赖一个不少，而组装逻辑用不到事务管理器、运行仓储那些 ——
// 为它们准备替身只会让用例看不清重点。
func newStep5Orchestrator(messages repository.MessageRepository, memories repository.SharedMemoryRepository) *Orchestrator {
	return &Orchestrator{deps: Deps{
		Messages:    messages,
		Compactions: &stubCompactions{},
		Summarizer:  &stubSummarizer{},
		Memories:    memories,
		Logger:      zap.NewNop(),
	}}
}

// testMemories 造几条"已经按重要度排好"的记忆。
//
// 排序是仓储的活（SQL 里的 ORDER BY importance DESC, id DESC），这里按排好的顺序给，
// 用来验证"编排不会自作主张重排"。
func testMemories() []entity.SharedContextMemory {
	return []entity.SharedContextMemory{
		{BaseModel: entity.BaseModel{ID: 2}, MemoryType: entity.MemoryTypeDecision, Content: "操作前先确认枪口安全", Importance: 5},
		{BaseModel: entity.BaseModel{ID: 1}, MemoryType: entity.MemoryTypeFact, Content: "用户叫熊大", Importance: 3},
	}
}

// ---- 注入（纯逻辑）----

// TestMemoryBlockComesLast 验证记忆块排在上下文最末。
//
// 位置是设计文档 §5.5 定死的（… → 共享记忆 → 当前用户消息）：当前用户消息不进 history
// （它是 Topic 单独传给模型的），所以记忆必须排在 history 末尾，才能正好落在它前面。
// 排在前面的话，模型会把记忆当成"很早以前说过的话"，容易和最近几轮混着看。
func TestMemoryBlockComesLast(t *testing.T) {
	messages := testMessages(31, 33)
	orchestrator := newStep5Orchestrator(
		&stubMessages{messages: messages},
		&stubMemories{memories: testMemories()},
	)

	history, err := orchestrator.buildContext(context.Background(), 1, 1, zap.NewNop())
	if err != nil {
		t.Fatalf("组装上下文失败: %v", err)
	}

	if len(history) != len(messages)+1 {
		t.Fatalf("上下文条数 = %d，期望 %d（3 条消息 + 1 条记忆块）", len(history), len(messages)+1)
	}
	last := history[len(history)-1]
	if last.Speaker != sharedMemorySpeaker {
		t.Errorf("最后一条应是记忆块（说话人 %q），实际是 %q", sharedMemorySpeaker, last.Speaker)
	}
	if !strings.Contains(last.Content, "操作前先确认枪口安全") {
		t.Errorf("记忆块里没有带上记忆正文，实际内容: %q", last.Content)
	}
}

// TestMemoryBlockLabelsAndOrder 验证记忆块带中文类型标签、且保持仓储给的顺序。
//
// 标签是给模型看的（"事实"和"待办"对它的意义完全不同）；
// 顺序**不在这里重排** —— 仓储的 SQL 已经按重要度排好了，编排再排一次就是同一套逻辑写两遍，
// 早晚会不一致。真库用例会验证 SQL 那侧的顺序。
func TestMemoryBlockLabelsAndOrder(t *testing.T) {
	orchestrator := newStep5Orchestrator(
		&stubMessages{messages: testMessages(2, 2)},
		&stubMemories{memories: testMemories()},
	)

	history, err := orchestrator.buildContext(context.Background(), 1, 1, zap.NewNop())
	if err != nil {
		t.Fatalf("组装上下文失败: %v", err)
	}
	block := history[len(history)-1].Content

	decisionAt := strings.Index(block, "决定：操作前先确认枪口安全")
	factAt := strings.Index(block, "事实：用户叫熊大")
	if decisionAt < 0 || factAt < 0 {
		t.Fatalf("记忆块里缺少带标签的记忆行，实际内容: %q", block)
	}
	if decisionAt > factAt {
		t.Errorf("记忆块把仓储给的顺序打乱了：决定（重要度 5）应排在事实（重要度 3）前面\n实际内容: %q", block)
	}
}

// TestMemoryBlockAbsentWhenNoMemories 验证一条记忆都没有时，上下文与第 4 步完全一样。
//
// 这条盯着"降级"：新加的这层不能在任何情况下改变原有行为 ——
// 没记忆就不该多出一条空块（空块会白占位置，还会让模型以为"这里本来有话"）。
func TestMemoryBlockAbsentWhenNoMemories(t *testing.T) {
	messages := testMessages(31, 33)
	orchestrator := newStep5Orchestrator(&stubMessages{messages: messages}, &stubMemories{})

	history, err := orchestrator.buildContext(context.Background(), 1, 1, zap.NewNop())
	if err != nil {
		t.Fatalf("组装上下文失败: %v", err)
	}
	if len(history) != len(messages) {
		t.Fatalf("上下文条数 = %d，期望 %d（没有记忆就不该多出块）", len(history), len(messages))
	}
	for _, message := range history {
		if message.Speaker == sharedMemorySpeaker {
			t.Errorf("没有记忆却出现了记忆块: %q", message.Content)
		}
	}
}

// TestMemoryReadFailureDegradesQuietly 验证读记忆失败时照常组装、不算错误。
//
// 记忆是辅助信息。数据库抖一下就让整场讨论发不出话，是拿主链路去赔辅链路。
func TestMemoryReadFailureDegradesQuietly(t *testing.T) {
	messages := testMessages(31, 33)
	orchestrator := newStep5Orchestrator(
		&stubMessages{messages: messages},
		&stubMemories{err: errors.New("数据库连接断了")},
	)

	history, err := orchestrator.buildContext(context.Background(), 1, 1, zap.NewNop())
	if err != nil {
		t.Fatalf("读记忆失败不该让组装也失败，实际返回: %v", err)
	}
	if len(history) != len(messages) {
		t.Errorf("降级后应只剩消息，实际条数 = %d，期望 %d", len(history), len(messages))
	}
}

// TestMemoryQueryUsesBothIDsAndLimit 验证查记忆时的三个参数都对。
//
// 两个 ID 用的是**不同的值**（7 和 9），这样万一实现把两个参数传反了，
// 这条用例会当场抓住 —— 传成一样的值就白测了。
func TestMemoryQueryUsesBothIDsAndLimit(t *testing.T) {
	memories := &stubMemories{}
	orchestrator := newStep5Orchestrator(&stubMessages{}, memories)

	if _, err := orchestrator.buildContext(context.Background(), 7, 9, zap.NewNop()); err != nil {
		t.Fatalf("组装上下文失败: %v", err)
	}
	if memories.gotClassroomID != 7 {
		t.Errorf("传给仓储的课堂 ID = %d，期望 7", memories.gotClassroomID)
	}
	if memories.gotConversationID != 9 {
		t.Errorf("传给仓储的对话 ID = %d，期望 9", memories.gotConversationID)
	}
	if memories.gotLimit != defaultMaxMemoriesInContext {
		t.Errorf("传给仓储的条数上限 = %d，期望 %d", memories.gotLimit, defaultMaxMemoriesInContext)
	}

	// 配置能覆盖默认值：将来嫌 10 条少，改配置就行，不用改代码。
	orchestrator.deps.ContextMaxMemories = 3
	if _, err := orchestrator.buildContext(context.Background(), 7, 9, zap.NewNop()); err != nil {
		t.Fatalf("组装上下文失败: %v", err)
	}
	if memories.gotLimit != 3 {
		t.Errorf("配了 ContextMaxMemories=3 之后上限 = %d，期望 3", memories.gotLimit)
	}
}

// ---- 提炼前的归一化（纯逻辑）----

// TestMemoryNormalizeCandidates 验证模型给的脏数据会被挡住。
//
// 为什么必须在 Go 侧判一遍：库上那几条 CHECK 只说"某条约束没过"，看不出是哪条候选哪一项；
// 而且一条坏候选会让整批写入回滚 —— 模型偶尔编一个类型出来，不该把这次讨论的其他记忆一起赔进去。
func TestMemoryNormalizeCandidates(t *testing.T) {
	candidates := []MemoryCandidate{
		{MemoryType: entity.MemoryTypeFact, Content: "  用户叫熊大  ", Importance: 3},
		{MemoryType: "bogus", Content: "类型不在五个取值里", Importance: 5},
		{MemoryType: entity.MemoryTypeDecision, Content: " \n\t ", Importance: 5},
		{MemoryType: entity.MemoryTypePreference, Content: "重要度超上限", Importance: 99},
		{MemoryType: entity.MemoryTypeOpenQuestion, Content: "重要度没给", Importance: 0},
		{MemoryType: entity.MemoryTypeLearningState, Content: "重要度是负数", Importance: -2},
	}
	for index := 0; index < 10; index++ {
		candidates = append(candidates, MemoryCandidate{
			MemoryType: entity.MemoryTypeFact,
			Content:    fmt.Sprintf("凑数的第 %d 条", index+1),
			Importance: 3,
		})
	}

	normalized := normalizeCandidates(candidates)

	// 13 条合法里只留前 10 条。
	if len(normalized) != maxMemoriesPerExtraction {
		t.Fatalf("归一化后 %d 条，期望 %d 条（未知类型与空正文被丢掉、超出部分截断）",
			len(normalized), maxMemoriesPerExtraction)
	}
	if normalized[0].Content != "用户叫熊大" {
		t.Errorf("第 1 条正文 = %q，期望去掉首尾空白后的 %q", normalized[0].Content, "用户叫熊大")
	}
	if normalized[1].Importance != maxMemoryImportance {
		t.Errorf("重要度 99 被夹成 %d，期望 %d", normalized[1].Importance, maxMemoryImportance)
	}
	if normalized[2].Importance != defaultMemoryImportance {
		t.Errorf("重要度没给（0）被取成 %d，期望默认值 %d", normalized[2].Importance, defaultMemoryImportance)
	}
	if normalized[3].Importance != defaultMemoryImportance {
		t.Errorf("重要度负数被取成 %d，期望默认值 %d", normalized[3].Importance, defaultMemoryImportance)
	}
	if normalized[4].Content != "凑数的第 1 条" {
		t.Errorf("第 5 条应是 %q，实际 %q", "凑数的第 1 条", normalized[4].Content)
	}
	if last := normalized[len(normalized)-1]; last.Content != "凑数的第 6 条" {
		t.Errorf("最后一条 = %q，期望 %q（超出上限的应从尾部截掉）", last.Content, "凑数的第 6 条")
	}
	for _, candidate := range normalized {
		if candidate.MemoryType == "bogus" {
			t.Error("未知类型没被挡住，它会撞上库里的 CHECK，让整批写入回滚")
		}
		if strings.TrimSpace(candidate.Content) == "" {
			t.Error("空正文没被挡住")
		}
	}
}

// TestMemoryNormalizeTruncatesLongContent 验证超长正文会被截断。
//
// 记忆**不参与压缩**：一条几千字的"记忆"会每轮都把上下文预算整口吃掉，
// 而且谁都压不掉它。截断比"带一条长文进去"安全。
func TestMemoryNormalizeTruncatesLongContent(t *testing.T) {
	long := strings.Repeat("冗", maxMemoryContentRunes+50)

	normalized := normalizeCandidates([]MemoryCandidate{
		{MemoryType: entity.MemoryTypeFact, Content: long, Importance: 3},
	})

	if len(normalized) != 1 {
		t.Fatalf("归一化后 %d 条，期望 1 条", len(normalized))
	}
	runes := []rune(normalized[0].Content)
	if len(runes) > maxMemoryContentRunes+1 {
		t.Errorf("正文长度 = %d 字，期望不超过 %d 字（截断后会补一个省略号）",
			len(runes), maxMemoryContentRunes+1)
	}
	if !strings.HasSuffix(normalized[0].Content, "…") {
		t.Errorf("截断后应留一个省略号提示内容不全，实际结尾: %q", normalized[0].Content[len(normalized[0].Content)-3:])
	}
}

// ---- 真库 ----

// newMemoryOrchestrator 用真仓储装配一个"能跑完整趟活"的编排器，提炼器由调用方指定。
func (f *fixture) newMemoryOrchestrator(t *testing.T, model Model, extractor MemoryExtractor, maxMemories int) *Orchestrator {
	t.Helper()

	orchestrator, err := New(Deps{
		Tx:                 f.tx,
		Conversations:      f.conversations,
		Messages:           f.messages,
		Runs:               f.runs,
		Turns:              f.turns,
		Compactions:        f.compactions,
		Memories:           f.memories,
		Model:              model,
		Summarizer:         FakeModel{},
		Extractor:          extractor,
		ContextMaxMemories: maxMemories,
		Director:           RoundRobinDirector{},
	})
	if err != nil {
		t.Fatalf("装配编排器失败: %v", err)
	}
	return orchestrator
}

// runOnce 跑一趟最短的讨论（一个角色说一次），返回结果。
func (f *fixture) runOnce(t *testing.T, orchestrator *Orchestrator) Result {
	t.Helper()

	result, err := orchestrator.Run(context.Background(), Request{
		ConversationID:   f.conversation.ID,
		TriggerMessageID: f.trigger.ID,
		Participants:     f.participants[:1],
		MaxTurns:         1,
	})
	if err != nil {
		t.Fatalf("跑讨论失败: %v", err)
	}
	return result
}

// TestMemoryIntegrationStoresExtracted 验证讨论收尾后记忆真的落了库，且字段都对。
//
// 重点看三个"选择"有没有被落地：作用域是对话级（S5-4）、状态是生效（S5-3 不做取代）、
// 来源两列与 last_used_at 都留空（S5-9 / S5-10）。
func TestMemoryIntegrationStoresExtracted(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	orchestrator := f.newOrchestrator(t, FakeModel{})
	f.runOnce(t, orchestrator)

	memories, err := f.memories.ListForContext(ctx, f.classroom.ID, f.conversation.ID, 10)
	if err != nil {
		t.Fatalf("读取共享记忆失败: %v", err)
	}
	if len(memories) == 0 {
		t.Fatal("讨论跑完了，却没提炼出任何记忆")
	}
	for _, memory := range memories {
		if memory.Scope != entity.MemoryScopeConversation {
			t.Errorf("记忆 %d 的作用域 = %s，期望 %s（一场归一场，见决策 S5-4）",
				memory.ID, memory.Scope, entity.MemoryScopeConversation)
		}
		if memory.ConversationID == nil || *memory.ConversationID != f.conversation.ID {
			t.Errorf("记忆 %d 的对话 ID = %v，期望 %d", memory.ID, memory.ConversationID, f.conversation.ID)
		}
		if memory.ClassroomID != f.classroom.ID {
			t.Errorf("记忆 %d 的课堂 ID = %d，期望 %d", memory.ID, memory.ClassroomID, f.classroom.ID)
		}
		if memory.Status != entity.MemoryStatusActive {
			t.Errorf("记忆 %d 的状态 = %s，期望 %s", memory.ID, memory.Status, entity.MemoryStatusActive)
		}
		if memory.SourceMessageID != nil || memory.SourceTurnID != nil {
			t.Errorf("记忆 %d 不该带来源（提炼是整场一次，硬指一条来源反而是错的），实际 = %v / %v",
				memory.ID, memory.SourceMessageID, memory.SourceTurnID)
		}
		if memory.LastUsedAt != nil {
			t.Errorf("记忆 %d 的 last_used_at 不该被写（读路径不写库），实际 = %v", memory.ID, memory.LastUsedAt)
		}
	}
}

// TestMemoryIntegrationReachesNextRun 验证上一趟提炼的记忆，真的进了下一趟的上下文。
//
// 这是这一整步的**唯一目的**：记下来、下次发言要用上。只测"库里有没有"是不够的 ——
// 存得再对，没接到组装那条路上，功能等于零。
func TestMemoryIntegrationReachesNextRun(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()

	// 第一趟：把记忆提炼出来。
	f.runOnce(t, f.newOrchestrator(t, FakeModel{}))

	// 第二趟：这一趟每次发言都该带上第一趟的记忆。
	recorder := &recordingModel{}
	f.runOnce(t, f.newOrchestrator(t, recorder))

	if recorder.calls == 0 {
		t.Fatal("第二趟的模型一次都没被调用")
	}
	history := recorder.lastRequest.History
	if len(history) == 0 {
		t.Fatal("第二趟拿到的上下文是空的")
	}
	last := history[len(history)-1]
	if last.Speaker != sharedMemorySpeaker {
		t.Errorf("上下文最后一条应是记忆块，实际说话人 = %q，内容 = %q", last.Speaker, last.Content)
	}
	if !strings.Contains(last.Content, "先确认枪口安全") {
		t.Errorf("记忆块里没看到上一趟记下的内容，实际 = %q", last.Content)
	}
}

// TestMemoryIntegrationOnlyActiveInScopeReachContext 验证真库那一侧的作用域与状态过滤。
//
// 用**真仓储**插三条记忆，然后直接组装上下文看谁进来了：
//   - 本对话的对话级记忆 → 该进来
//   - 本门课的课堂级记忆 → 该进来（设计文档 §5.6）
//   - 已被取代的记忆（重要度最高的一条）→ 不该进来
//
// 被取代的那条重要度故意设成最高：过滤一旦失效，它会排在最前面，失败信息一眼就能看出问题。
func TestMemoryIntegrationOnlyActiveInScopeReachContext(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	conversationID := f.conversation.ID
	insert := func(memory *entity.SharedContextMemory) {
		t.Helper()
		if err := f.memories.Create(ctx, memory); err != nil {
			t.Fatalf("插入测试记忆失败: %v", err)
		}
	}
	insert(&entity.SharedContextMemory{
		ClassroomID: f.classroom.ID, ConversationID: &conversationID,
		Scope: entity.MemoryScopeConversation, MemoryType: entity.MemoryTypeFact,
		Content: "本对话记下的事", Importance: 2, Status: entity.MemoryStatusActive,
	})
	insert(&entity.SharedContextMemory{
		ClassroomID: f.classroom.ID,
		Scope:       entity.MemoryScopeClassroom, MemoryType: entity.MemoryTypeDecision,
		Content: "本门课通用的事", Importance: 2, Status: entity.MemoryStatusActive,
	})
	insert(&entity.SharedContextMemory{
		ClassroomID: f.classroom.ID, ConversationID: &conversationID,
		Scope: entity.MemoryScopeConversation, MemoryType: entity.MemoryTypeFact,
		Content: "已经被取代的事", Importance: 5, Status: entity.MemoryStatusSuperseded,
	})

	orchestrator := newStep5Orchestrator(f.messages, f.memories)
	history, err := orchestrator.buildContext(ctx, f.classroom.ID, f.conversation.ID, zap.NewNop())
	if err != nil {
		t.Fatalf("组装上下文失败: %v", err)
	}

	block := history[len(history)-1].Content
	if !strings.Contains(block, "本对话记下的事") {
		t.Errorf("本对话的对话级记忆没进上下文，实际 = %q", block)
	}
	if !strings.Contains(block, "本门课通用的事") {
		t.Errorf("本门课的课堂级记忆没进上下文，实际 = %q", block)
	}
	if strings.Contains(block, "已经被取代的事") {
		t.Errorf("已被取代的记忆不该进上下文，实际 = %q", block)
	}
}

// TestMemoryIntegrationExtractFailureKeepsRunSuccessful 验证提炼失败不影响讨论本身。
//
// 记忆是这场讨论的副产品。为了它把已经跑完、已经落库的讨论变成"失败"，代价太大（决策 S5-8）。
func TestMemoryIntegrationExtractFailureKeepsRunSuccessful(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	orchestrator := f.newMemoryOrchestrator(t,
		FakeModel{},
		&stubExtractor{err: errors.New("提炼器挂了")},
		0,
	)
	result := f.runOnce(t, orchestrator)

	if result.Status != entity.RunStatusCompleted {
		t.Errorf("提炼失败时讨论状态 = %s，期望 %s", result.Status, entity.RunStatusCompleted)
	}
	memories, err := f.memories.ListForContext(ctx, f.classroom.ID, f.conversation.ID, 10)
	if err != nil {
		t.Fatalf("读取共享记忆失败: %v", err)
	}
	if len(memories) != 0 {
		t.Errorf("提炼失败了却写进了 %d 条记忆", len(memories))
	}
}

// TestMemoryIntegrationSkipsInvalidCandidates 验证脏候选不会连累好候选。
//
// 提炼器一次给回 14 条（2 条不合法 + 12 条合法）：库里应该正好留下 10 条合法的，
// 而不是"因为有一条坏了，整批都没写进去"。
func TestMemoryIntegrationSkipsInvalidCandidates(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	candidates := []MemoryCandidate{
		{MemoryType: "bogus", Content: "类型不认识", Importance: 5},
		{MemoryType: entity.MemoryTypeFact, Content: "   ", Importance: 5},
	}
	for index := 0; index < 12; index++ {
		candidates = append(candidates, MemoryCandidate{
			MemoryType: entity.MemoryTypeFact,
			Content:    fmt.Sprintf("合法的第 %d 条", index+1),
			Importance: 3,
		})
	}

	orchestrator := f.newMemoryOrchestrator(t, FakeModel{}, &stubExtractor{candidates: candidates}, 0)
	f.runOnce(t, orchestrator)

	memories, err := f.memories.ListForContext(ctx, f.classroom.ID, f.conversation.ID, 20)
	if err != nil {
		t.Fatalf("读取共享记忆失败: %v", err)
	}
	if len(memories) != maxMemoriesPerExtraction {
		t.Fatalf("库里留下 %d 条，期望 %d 条（2 条不合法被丢、12 条合法里只留前 10 条）",
			len(memories), maxMemoriesPerExtraction)
	}
	for _, memory := range memories {
		if memory.MemoryType != entity.MemoryTypeFact {
			t.Errorf("库里出现了不该存在的类型: %s", memory.MemoryType)
		}
		if strings.TrimSpace(memory.Content) == "" {
			t.Error("库里出现了空正文的记忆")
		}
	}
}

// TestMemoryIntegrationExtractorSeesWholeDiscussion 验证提炼器拿到的材料是"这场讨论的全貌"。
//
// 缺了用户那句话，提炼器就不知道大家在围绕什么聊；缺了某个回合，结论可能就少一条。
func TestMemoryIntegrationExtractorSeesWholeDiscussion(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()

	extractor := &stubExtractor{}
	orchestrator := f.newMemoryOrchestrator(t, FakeModel{}, extractor, 0)

	result, err := orchestrator.Run(context.Background(), Request{
		ConversationID:   f.conversation.ID,
		TriggerMessageID: f.trigger.ID,
		Participants:     f.participants[:2],
		MaxTurns:         2,
	})
	if err != nil {
		t.Fatalf("跑讨论失败: %v", err)
	}

	if extractor.calls != 1 {
		t.Fatalf("提炼器被调用了 %d 次，期望 1 次（整场只在收尾时提炼一次）", extractor.calls)
	}
	if got, want := len(extractor.gotHistory), len(result.Turns)+1; got != want {
		t.Errorf("提炼材料有 %d 条，期望 %d 条（用户那句话 + %d 个回合的发言）",
			got, want, len(result.Turns))
	}
	if extractor.gotHistory[0].Content != f.trigger.Content {
		t.Errorf("提炼材料第一条应是用户那句话 %q，实际 %q", f.trigger.Content, extractor.gotHistory[0].Content)
	}
	for index, outcome := range result.Turns {
		if extractor.gotHistory[index+1].Content != outcome.Content {
			t.Errorf("提炼材料第 %d 条应是回合 %d 的发言", index+2, index+1)
		}
	}
}
