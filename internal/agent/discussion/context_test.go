package discussion

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"go.uber.org/zap"

	"narra/internal/model/entity"
	"narra/internal/repository"
)

// 第 4 步（上下文组装 + token 预算 + 摘要压缩）的测试。
//
// 分两段：
//   - 组装与预算判断是纯逻辑，用内存替身测（TestContextAssemble*）—— 跑得快，
//     也不会因为"上一轮测试留下脏数据"而出现难以定位的失败。
//   - "触发压缩"必须验清"库里到底写没写、写的是哪一段"，所以用真库
//     （TestContextCompaction*，复用 orchestrator_test.go 的夹具）。

// ---- 纯逻辑用例用的替身 ----

// stubMessages 只实现 buildContext 用到的那一个读方法。
//
// 嵌入接口而不是逐个实现七个方法：本包用不到的留成 nil，真被误调用会当场 panic ——
// 比悄悄返回一份假数据更容易发现"用错了方法"。
type stubMessages struct {
	repository.MessageRepository

	gotAfterSequence int64
	gotLimit         int
	messages         []entity.ConversationMessage
}

func (s *stubMessages) ListByConversation(_ context.Context, _ uint64, afterSequence int64, limit int) ([]entity.ConversationMessage, error) {
	s.gotAfterSequence = afterSequence
	s.gotLimit = limit

	visible := make([]entity.ConversationMessage, 0, len(s.messages))
	for _, message := range s.messages {
		if message.SequenceNo > afterSequence {
			visible = append(visible, message)
		}
	}
	return visible, nil
}

// stubCompactions 记下写进来的摘要，让"有没有压、压的是哪一段"可以被断言。
type stubCompactions struct {
	repository.ContextCompactionRepository

	latest    *entity.ContextCompaction
	created   []*entity.ContextCompaction
	createErr error
}

func (s *stubCompactions) LatestByConversation(_ context.Context, _ uint64) (*entity.ContextCompaction, error) {
	return s.latest, nil
}

func (s *stubCompactions) Create(_ context.Context, compaction *entity.ContextCompaction) error {
	if s.createErr != nil {
		return s.createErr
	}
	s.created = append(s.created, compaction)
	if compaction.ID == 0 {
		compaction.ID = uint64(len(s.created))
	}
	return nil
}

// stubSummarizer 是"写摘要"的替身。
//
// 默认返回一段**远短于原文**的固定文案（这是正常路径）；tooLong 非空时改为返回长文，
// 用来验证"摘要没比原文短就放弃这次压缩"。
type stubSummarizer struct {
	tooLong string

	calls       int
	gotPrevious string
	gotCount    int
}

func (s *stubSummarizer) Summarize(_ context.Context, previous string, messages []HistoryMessage) (string, error) {
	s.calls++
	s.gotPrevious = previous
	s.gotCount = len(messages)

	if s.tooLong != "" {
		return s.tooLong, nil
	}
	return fmt.Sprintf("第 %d 版摘要", s.calls), nil
}

// newBareOrchestrator 只装配上下文组装需要的几个依赖。
//
// 不走 New()：那个函数会校验全部依赖一个不少，而事务管理器、运行仓储这些
// 组装逻辑根本用不到 —— 为它们准备替身只会让用例看不清重点。
//
// 共享记忆给一个空替身：第 4 步这些用例关心的是摘要与原文，记忆块为空时组装结果与第 4 步一致
// （"有没有记忆"的用例在 memory_test.go）。
func newBareOrchestrator(messages repository.MessageRepository, compactions repository.ContextCompactionRepository, summarizer Summarizer) *Orchestrator {
	return &Orchestrator{deps: Deps{
		Messages:    messages,
		Compactions: compactions,
		Summarizer:  summarizer,
		Memories:    &stubMemories{},
		Extractor:   &stubExtractor{},
		Logger:      zap.NewNop(),
	}}
}

// testMessages 造一批带序号的假消息，内容够长以便把 token 撑起来。
func testMessages(from, to int64) []entity.ConversationMessage {
	messages := make([]entity.ConversationMessage, 0, to-from+1)
	for sequence := from; sequence <= to; sequence++ {
		messages = append(messages, entity.ConversationMessage{
			ConversationID: 1,
			SequenceNo:     sequence,
			SenderType:     entity.MessageSenderAgent,
			SenderSnapshot: json.RawMessage(`{"name":"张老师"}`),
			Content:        fmt.Sprintf("第 %d 条发言：天空发蓝与光的散射有关，波长越短越容易被大气散射。", sequence),
			Status:         entity.MessageStatusCompleted,
		})
	}
	return messages
}

// ---- 组装与预算 ----

// TestContextAssembleSummaryComesFirst 验证顺序：摘要排在最前，之后的原文按序号升序。
//
// 顺序错了模型看到的对话就是乱序的：摘要夹在中间、或者消息倒着排，
// 都会让"当时聊到哪了"变得不可读，而且不会报任何错。
func TestContextAssembleSummaryComesFirst(t *testing.T) {
	messages := &stubMessages{messages: testMessages(31, 33)}
	compactions := &stubCompactions{latest: &entity.ContextCompaction{
		CoveredToSequence: 30,
		Summary:           "此前 30 条发言的摘要：确认了天空发蓝是散射造成的。",
	}}
	summarizer := &stubSummarizer{}
	orchestrator := newBareOrchestrator(messages, compactions, summarizer)

	history, err := orchestrator.buildContext(context.Background(), 1, 1, zap.NewNop())
	if err != nil {
		t.Fatalf("组装上下文失败: %v", err)
	}

	if len(history) != 4 {
		t.Fatalf("上下文条数 = %d，期望 4（1 条摘要 + 3 条消息）", len(history))
	}
	if history[0].Content != compactions.latest.Summary {
		t.Errorf("第一条应是摘要，实际是 %q", history[0].Content)
	}

	expected := testMessages(31, 33)
	for index, message := range expected {
		if history[index+1].Content != message.Content {
			t.Errorf("第 %d 条消息顺序不对，实际是 %q", index+1, history[index+1].Content)
		}
	}
	if summarizer.calls != 0 {
		t.Errorf("没超预算却调了摘要器 %d 次", summarizer.calls)
	}
}

// TestContextAssembleSkipsCoveredMessages 验证摘要覆盖过的那段不再以原文进上下文。
//
// 这是摘要存在的全部意义：如果压完还把原文一起塞进去，上下文反而更长，
// 白花一次模型调用，还多了一行记录。
func TestContextAssembleSkipsCoveredMessages(t *testing.T) {
	messages := &stubMessages{messages: testMessages(1, 40)}
	compactions := &stubCompactions{latest: &entity.ContextCompaction{
		CoveredToSequence: 30,
		Summary:           "此前 30 条发言的摘要。",
	}}
	orchestrator := newBareOrchestrator(messages, compactions, &stubSummarizer{})

	history, err := orchestrator.buildContext(context.Background(), 1, 1, zap.NewNop())
	if err != nil {
		t.Fatalf("组装上下文失败: %v", err)
	}

	if messages.gotAfterSequence != 30 {
		t.Errorf("取历史时传的起点 = %d，期望 30（摘要已覆盖的区间不该再取一遍）", messages.gotAfterSequence)
	}
	// 这条断言盯着一个容易踩的坑：仓储的 normalizeLimit 会把 0 规范成 50，
	// 想"取摘要之后的全部消息"就必须显式给上限，否则悄悄少拿一大截。
	if messages.gotLimit != contextMessageLimit {
		t.Errorf("取历史时传的条数上限 = %d，期望 %d", messages.gotLimit, contextMessageLimit)
	}

	if len(history) != 11 {
		t.Fatalf("上下文条数 = %d，期望 11（1 条摘要 + 第 31~40 条）", len(history))
	}
	expected := testMessages(31, 40)
	if history[1].Content != expected[0].Content {
		t.Errorf("第二条应是第 31 条消息，实际是 %q", history[1].Content)
	}
	if history[10].Content != expected[9].Content {
		t.Errorf("最后一条应是第 40 条消息，实际是 %q", history[10].Content)
	}
}

// TestContextAssembleNoCompactionUnderBudget 验证没超预算时完全不动摘要。
func TestContextAssembleNoCompactionUnderBudget(t *testing.T) {
	messages := &stubMessages{messages: testMessages(1, 30)}
	compactions := &stubCompactions{}
	summarizer := &stubSummarizer{}
	orchestrator := newBareOrchestrator(messages, compactions, summarizer)

	history, err := orchestrator.buildContext(context.Background(), 1, 1, zap.NewNop())
	if err != nil {
		t.Fatalf("组装上下文失败: %v", err)
	}

	if len(history) != 30 {
		t.Errorf("上下文条数 = %d，期望 30（没超预算应原样返回）", len(history))
	}
	if len(compactions.created) != 0 || summarizer.calls != 0 {
		t.Errorf("没超预算却做了压缩：写入 %d 版，调摘要器 %d 次",
			len(compactions.created), summarizer.calls)
	}
}

// TestContextCompactionSkippedWhenSummaryNotShorter 验证"压完反而更长"时放弃压缩。
//
// 库里有一条 CHECK 要求 summary_tokens < source_tokens。真让它撞上去，
// 报出来的只是一句约束冲突，而这里想要的行为是：放弃这次压缩、留条日志、
// 让讨论照常用完整上下文继续 —— 摘要没写好不该拖垮一场讨论。
func TestContextCompactionSkippedWhenSummaryNotShorter(t *testing.T) {
	messages := &stubMessages{messages: testMessages(1, 30)}
	compactions := &stubCompactions{}

	// 造一段比原文更长的"摘要"：原文约 30×33 个字，这里直接给 20000 字。
	long := make([]rune, 0, 20000)
	for index := 0; index < 20000; index++ {
		long = append(long, '冗')
	}
	summarizer := &stubSummarizer{tooLong: string(long)}

	orchestrator := newBareOrchestrator(messages, compactions, summarizer)
	orchestrator.deps.ContextBudget = 10 // 远小于实际长度，必然想压缩

	history, err := orchestrator.buildContext(context.Background(), 1, 1, zap.NewNop())
	if err != nil {
		t.Fatalf("压缩失败不该让组装也失败: %v", err)
	}

	if len(compactions.created) != 0 {
		t.Errorf("摘要比原文还长，却照样写进了库（真库上会被 CHECK 拒绝）")
	}
	if len(history) != 30 {
		t.Errorf("放弃压缩后应原样返回完整上下文，实际条数 = %d", len(history))
	}
	if summarizer.calls != 1 {
		t.Errorf("摘要器调用次数 = %d，期望 1（试过一次再放弃，不该反复试）", summarizer.calls)
	}
}

// ---- 压缩（真库）----

// appendMessages 往夹具的对话里追加快 count 条角色消息，返回最后一条的序号。
//
// 整批放在一个事务里：AppendNext 要先锁对话行再取号，逐条开事务既没必要也慢得多。
func (f *fixture) appendMessages(t *testing.T, count int) int64 {
	t.Helper()

	ctx := context.Background()
	var last int64
	err := f.tx.Run(ctx, func(ctx context.Context) error {
		for index := 0; index < count; index++ {
			message := &entity.ConversationMessage{
				ConversationID: f.conversation.ID,
				SenderType:     entity.MessageSenderAgent,
				SenderSnapshot: json.RawMessage(`{"name":"张老师"}`),
				Content: fmt.Sprintf(
					"第 %d 条发言：天空发蓝与光的散射有关，波长越短越容易被大气散射。",
					index+1,
				),
				Status:   entity.MessageStatusCompleted,
				Metadata: json.RawMessage("{}"),
			}
			if err := f.messages.AppendNext(ctx, message); err != nil {
				return err
			}
			last = message.SequenceNo
		}
		return nil
	})
	if err != nil {
		t.Fatalf("追加上下文测试消息失败: %v", err)
	}
	return last
}

// newContextOrchestrator 用真仓储 + 极小的预算装配一个编排器，把压缩逼出来。
//
// 预算调到 10：正常预算（6000）下要造几千条消息才会触发一次压缩，那样测试又慢又难读。
func (f *fixture) newContextOrchestrator(summarizer Summarizer) *Orchestrator {
	return &Orchestrator{deps: Deps{
		Messages:          f.messages,
		Compactions:       f.compactions,
		Summarizer:        summarizer,
		Memories:          f.memories,
		Extractor:         &stubExtractor{},
		Logger:            zap.NewNop(),
		ContextBudget:     10,
		ContextKeepRecent: 5,
	}}
}

// TestContextCompactionTriggersWhenOverBudget 验证超预算时真的写出一版摘要，
// 且覆盖的区间正是"被替掉的那段原文"。
func TestContextCompactionTriggersWhenOverBudget(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()

	ctx := context.Background()
	last := f.appendMessages(t, 25) // 触发消息占 1 号，所以这批是 2~26

	summarizer := &stubSummarizer{}
	orchestrator := f.newContextOrchestrator(summarizer)

	history, err := orchestrator.buildContext(ctx, f.classroom.ID, f.conversation.ID, zap.NewNop())
	if err != nil {
		t.Fatalf("组装上下文失败: %v", err)
	}

	latest, err := f.compactions.LatestByConversation(ctx, f.conversation.ID)
	if err != nil {
		t.Fatalf("回查摘要失败: %v", err)
	}
	if latest == nil {
		t.Fatal("上下文明显超预算，却没有写出任何摘要")
	}
	if latest.CoveredFromSequence != 1 {
		t.Errorf("覆盖起点 = %d，期望 1（该会话此前没有摘要，应从第一条开始）", latest.CoveredFromSequence)
	}
	if want := last - 5; latest.CoveredToSequence != want {
		t.Errorf("覆盖终点 = %d，期望 %d（保留最近 5 条，其余进摘要）", latest.CoveredToSequence, want)
	}
	if latest.SummaryTokens >= latest.SourceTokens {
		t.Errorf("摘要 token(%d) 未少于原文 token(%d)，这条压缩没有意义",
			latest.SummaryTokens, latest.SourceTokens)
	}
	if latest.PreviousCompactionID != nil {
		t.Errorf("这是第一版摘要，previous_compaction_id 应为空，实际 = %d", *latest.PreviousCompactionID)
	}

	// 压缩之后返回的上下文 = 新摘要 + 保留下来的那几条原文。
	if len(history) != 6 {
		t.Fatalf("上下文条数 = %d，期望 6（1 条摘要 + 保留的 5 条）", len(history))
	}
	if history[0].Content != "第 1 版摘要" {
		t.Errorf("第一条应是刚生成的摘要，实际是 %q", history[0].Content)
	}
}

// TestContextCompactionDoesNotRepeatSameRange 验证连续两次压缩不会覆盖同一段。
//
// 这条盯着两个后果：区间重叠会被 UNIQUE (conversation_id, covered_to_sequence) 拒绝，
// 讨论当场失败；区间跳跃则会永久丢掉中间那几条发言。
func TestContextCompactionDoesNotRepeatSameRange(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()

	ctx := context.Background()
	f.appendMessages(t, 35) // 序号 2~36

	summarizer := &stubSummarizer{}
	orchestrator := f.newContextOrchestrator(summarizer)

	if _, err := orchestrator.buildContext(ctx, f.classroom.ID, f.conversation.ID, zap.NewNop()); err != nil {
		t.Fatalf("第一次组装失败: %v", err)
	}
	first, err := f.compactions.LatestByConversation(ctx, f.conversation.ID)
	if err != nil || first == nil {
		t.Fatalf("第一次压缩没写出摘要 (err=%v)", err)
	}

	// 中间又聊了几句，制造出"新的一段可压缩内容"。
	f.appendMessages(t, 5)

	if _, err := orchestrator.buildContext(ctx, f.classroom.ID, f.conversation.ID, zap.NewNop()); err != nil {
		t.Fatalf("第二次组装失败（可能是把同一段重复压了，撞上唯一约束）: %v", err)
	}
	second, err := f.compactions.LatestByConversation(ctx, f.conversation.ID)
	if err != nil || second == nil {
		t.Fatalf("第二次压缩没写出摘要 (err=%v)", err)
	}

	if second.ID == first.ID {
		t.Fatal("第二次没有产出新摘要，取到的还是第一版")
	}
	if second.CoveredFromSequence != first.CoveredToSequence+1 {
		t.Errorf("第二版覆盖起点 = %d，期望 %d（必须紧接上一版，不能重叠也不能跳）",
			second.CoveredFromSequence, first.CoveredToSequence+1)
	}
	if second.PreviousCompactionID == nil || *second.PreviousCompactionID != first.ID {
		t.Errorf("版本链断了：previous_compaction_id = %v，期望 %d", second.PreviousCompactionID, first.ID)
	}
	// 累积：新摘要要吸收上一版的内容，否则第一次的讨论要点就此丢失。
	if summarizer.gotPrevious != first.Summary {
		t.Errorf("第二版摘要的输入 = %q，期望带上上一版 %q", summarizer.gotPrevious, first.Summary)
	}
}
