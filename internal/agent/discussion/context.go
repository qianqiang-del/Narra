package discussion

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"narra/internal/model/entity"
)

// 上下文组装（第 4 步 + 第 5 步）。
//
// 每次让某个角色发言之前，都要先把"他现在需要知道的"拼出来。顺序由设计文档 §5.5 定死：
//
//	系统提示词 → 最新摘要 → 摘要之后的完整消息 → 共享记忆 → 当前用户消息
//
// 其中系统提示词在提示词那一层拼（不在本文件），所以本文件负责中间三段：
// **最新摘要 + 摘要之后的完整消息 + 共享记忆**。
//
// 拼完还要做预算判断：总长度超过阈值就把老消息压成摘要。
// 不这么做的后果不是"慢一点"，而是某一轮突然超出模型窗口、整场讨论直接报错。
//
// 本文件只管"发言前把记忆带进来"（读）；"讨论结束后把结论记下来"（写）在 memory.go。

const (
	// defaultMaxContextTokens 是没配时使用的上下文 token 上限。
	//
	// 6000 是个保守值：真模型的窗口普遍远大于它，压早一点只是少带点原文，
	// 压晚一点则可能在某一轮突然超窗口、让讨论直接失败。宁可早压。
	defaultMaxContextTokens = 6000

	// defaultKeepRecentMessages 是压缩时保留多少条最近消息的原文。
	//
	// 讨论的相关性集中在最近几轮：更老的只留结论就够，最近这几条要留原文 ——
	// 模型需要看到具体的措辞，才能接着上面的话往下说。
	defaultKeepRecentMessages = 20

	// contextMessageLimit 是一次最多取多少条消息参与组装。
	//
	// 必须显式给值：仓储的 normalizeLimit 会把 0 规范成 50，而这里要的是
	// "摘要之后的全部消息"，给 0 会悄悄少拿一大截，且完全不会报错。
	contextMessageLimit = 500

	// summarySpeaker 是摘要进入上下文时用的说话人名字。
	//
	// 让它以"一条历史发言"的形式出现，是为了模型能看出这段不是谁刚说的话，
	// 而是更早内容的浓缩 —— 否则它可能会把摘要当成新观点去回应。
	summarySpeaker = "历史摘要"

	// defaultMaxMemoriesInContext 是每次发言最多带几条共享记忆进上下文。
	//
	// 记忆同样要占提示词的位置，带太多就把最近的原文挤掉了。10 条是当前约定的量
	// （决策清单 S5-2，用户定的），可用 Deps.ContextMaxMemories 覆盖。
	//
	// ⚠️ 这是"每次带进去几条"，不是"库里只能存几条"：同一场讨论聊上几轮会不断累积新的记忆，
	// 每次只挑最重要的那几条带上。
	defaultMaxMemoriesInContext = 10

	// sharedMemorySpeaker 是共享记忆进入上下文时用的说话人名字。
	//
	// 和摘要一样，让它以"一条发言"的形式出现，模型才不会把记忆误当成谁刚说的话。
	sharedMemorySpeaker = "共享记忆"
)

// buildContext 组装这次发言要带的上下文，必要时就地压一次。
//
// 压缩失败不返回错误：这里是讨论的必经之路，一次摘要没写好不该让整场讨论失败。
// 三种"没压成"的情况（可压内容为空、摘要没比原文短、落库失败）都会记日志并按原样送出 ——
// 代价只是这一轮上下文长一点。
//
// 读共享记忆失败**同样不返回错误**，理由一样：记忆是辅助信息，数据库抖一下不该让讨论哑掉。
//
// 两个 ID 都要传：共享记忆分"对话级"和"课堂级"（设计文档 §5.6），
// 只给对话 ID 就查不出课堂级那一半 —— 而课堂级记忆恰恰是跨对话共享的载体。
func (o *Orchestrator) buildContext(ctx context.Context, classroomID uint64, conversationID uint64, log *zap.Logger) ([]HistoryMessage, error) {
	previous, err := o.deps.Compactions.LatestByConversation(ctx, conversationID)
	if err != nil {
		return nil, fmt.Errorf("读取历史摘要失败: %w", err)
	}

	// 上一版摘要覆盖到哪，就从哪之后开始取原文 —— 已经压过的那段不再逐条塞进去，
	// 否则摘要白做，上下文反而更长。仓储那边的条件是严格大于，正好接得上，不漏不重。
	coveredTo := int64(0)
	previousSummary := ""
	if previous != nil {
		coveredTo = previous.CoveredToSequence
		previousSummary = previous.Summary
	}

	messages, err := o.deps.Messages.ListByConversation(ctx, conversationID, coveredTo, contextMessageLimit)
	if err != nil {
		return nil, fmt.Errorf("读取对话历史失败: %w", err)
	}

	memories := o.loadMemories(ctx, classroomID, conversationID, log)

	history := withMemories(assembleHistory(previousSummary, messages), memories)
	if estimateHistoryTokens(history) <= o.contextBudget() {
		return history, nil
	}

	compacted, ok := o.compact(ctx, conversationID, previous, messages, log)
	if !ok {
		return history, nil
	}
	// 压缩只动"摘要 + 原文"那两段；记忆块原样接回去 ——
	// 它本来就只有几条，而且是挑出来的结论，正是最不该被压掉的东西。
	return withMemories(compacted, memories), nil
}

// loadMemories 读这次发言要带的共享记忆；读不到就返回 nil（等于"这次不带记忆"）。
//
// 失败只记日志、不返回错误：记忆是辅助信息，一次查询没成功不该让整场讨论发不出话。
// 真返回错误会让调用方在"带不带记忆"和"要不要中断讨论"之间做选择，而那个选择是错的 ——
// 正确答案永远是"继续讨论，只是这次少带几条"。
func (o *Orchestrator) loadMemories(ctx context.Context, classroomID uint64, conversationID uint64, log *zap.Logger) []entity.SharedContextMemory {
	memories, err := o.deps.Memories.ListForContext(ctx, classroomID, conversationID, o.memoriesInContext())
	if err != nil {
		log.Warn("读取共享记忆失败，这次发言不带记忆",
			zap.Uint64("classroom_id", classroomID),
			zap.Uint64("conversation_id", conversationID),
			zap.Error(err),
		)
		return nil
	}
	return memories
}

// memoriesInContext 取生效的"一次带几条记忆"，没配就用默认值。
func (o *Orchestrator) memoriesInContext() int {
	if o.deps.ContextMaxMemories > 0 {
		return o.deps.ContextMaxMemories
	}
	return defaultMaxMemoriesInContext
}

// withMemories 把记忆块接在上下文最末。
//
// 位置由设计文档 §5.5 定死：系统提示词 → 摘要 → 摘要之后的原文 → **共享记忆** → 当前用户消息。
// 当前用户消息不进 history（它是 Topic 单独传给模型的），所以记忆接在 history 末尾，
// 正好落在它前面。
//
// 这里**新建一个切片**再拼，而不是直接在传进来的那份上 append：追加会复用底层数组，
// 万一调用方还留着原切片，就会看到一段悄悄多出来的内容。
func withMemories(history []HistoryMessage, memories []entity.SharedContextMemory) []HistoryMessage {
	block := assembleMemoryBlock(memories)
	if len(block) == 0 {
		return history
	}

	combined := make([]HistoryMessage, 0, len(history)+len(block))
	combined = append(combined, history...)
	return append(combined, block...)
}

// assembleMemoryBlock 把记忆拼成一条汇总块；没有记忆时返回 nil（调用方据此"不加这一块"）。
//
// 拼成一条而不是每条一块：每条各带一个发言人标签纯属浪费位置，而且块头说明一次来源，
// 模型更不容易把某条记忆当成"某人刚说过的话"（决策清单 S5-6）。
//
// ⚠️ 这里**不重排**：仓储的 SQL 已经按重要度排好序了，编排再排一遍就是同一套规则写两处，
// 早晚会不一致。
func assembleMemoryBlock(memories []entity.SharedContextMemory) []HistoryMessage {
	if len(memories) == 0 {
		return nil
	}

	var builder strings.Builder
	builder.WriteString("同一场讨论里已经确认过的信息：")
	for _, memory := range memories {
		builder.WriteString("\n- ")
		builder.WriteString(memoryTypeLabel(memory.MemoryType))
		builder.WriteString("：")
		builder.WriteString(memory.Content)
	}
	return []HistoryMessage{{Speaker: sharedMemorySpeaker, Content: builder.String()}}
}

// memoryTypeLabels 把记忆类型翻成中文标签。
//
// 为什么要翻：类型本身是给程序看的（五个英文取值），而这行文字是给模型看的 ——
// "事实"和"待解问题"对它意味着完全不同的东西，认得出标签它才知道该怎么用这条信息。
var memoryTypeLabels = map[string]string{
	entity.MemoryTypeFact:          "事实",
	entity.MemoryTypeDecision:      "决定",
	entity.MemoryTypeLearningState: "学习状态",
	entity.MemoryTypePreference:    "偏好",
	entity.MemoryTypeOpenQuestion:  "待解问题",
}

// memoryTypeLabel 取中文标签；认不出来的类型原样返回。
//
// 兜底而不是丢掉：能走到这里说明数据已经在库里了（写入时校验过），
// 显示原始取值总比"这条记忆凭空消失"好 —— 而"凭空消失"正是最难查的那种问题。
func memoryTypeLabel(memoryType string) string {
	if label, ok := memoryTypeLabels[memoryType]; ok {
		return label
	}
	return memoryType
}

// compact 把"除最近几条之外"的原文压成一版新摘要，返回压缩后的上下文。
//
// ok 为 false 表示这次没有压缩（原因都记在日志里）。三条不肯压的理由：
//   - 没有可压的内容：剩下的全是准备保留的原文。硬压会在"压完还超预算"和"再压一次"
//     之间来回打转，把一次发言变成一串模型调用。
//   - 摘要没比原文短：库里那条 CHECK 会拦住，而且这次压缩本身就没有意义。
//   - 落库失败：例如同一段已被并发压过、撞上唯一约束。这一轮上下文长一点没关系，
//     但把错误抛出去会让整场讨论在这里失败。
func (o *Orchestrator) compact(
	ctx context.Context,
	conversationID uint64,
	previous *entity.ContextCompaction,
	messages []entity.ConversationMessage,
	log *zap.Logger,
) ([]HistoryMessage, bool) {
	keepRecent := o.keepRecentMessages()
	if len(messages) <= keepRecent {
		log.Warn("上下文超预算，但可压缩的消息不足，本轮按原样送出",
			zap.Uint64("conversation_id", conversationID),
			zap.Int("messages", len(messages)),
			zap.Int("keep_recent", keepRecent),
		)
		return nil, false
	}

	compressible := messages[:len(messages)-keepRecent]
	keep := messages[len(messages)-keepRecent:]

	sourceTokens := estimateHistoryTokens(toHistory(compressible))

	previousSummary := ""
	if previous != nil {
		previousSummary = previous.Summary
	}
	summary, err := o.deps.Summarizer.Summarize(ctx, previousSummary, toHistory(compressible))
	if err != nil {
		log.Warn("摘要生成失败，本轮按原样送出",
			zap.Uint64("conversation_id", conversationID),
			zap.Int("compressed_messages", len(compressible)),
			zap.Error(err),
		)
		return nil, false
	}

	summaryTokens := estimateTokens(summary)
	if summaryTokens >= sourceTokens {
		// 就地判一次而不是等数据库报错：约束被违反时给出的信息只有"某条约束没过"，
		// 看不出是"摘要写长了"还是"数据算错了"。
		log.Warn("摘要不比原文短，放弃这次压缩",
			zap.Uint64("conversation_id", conversationID),
			zap.Int32("source_tokens", sourceTokens),
			zap.Int32("summary_tokens", summaryTokens),
		)
		return nil, false
	}

	compaction := &entity.ContextCompaction{
		ConversationID: conversationID,
		// 覆盖区间取的是**真实消息的序号**，而不是"上一版覆盖到哪 + 1"：
		// 消息允许被单独删除，序号会有空洞，用算出来的值可能出现 covered_from 指向
		// 一个不存在的序号，看起来像压过一段其实没压的东西。
		CoveredFromSequence: compressible[0].SequenceNo,
		CoveredToSequence:   compressible[len(compressible)-1].SequenceNo,
		Summary:             summary,
		KeyPoints:           json.RawMessage("{}"),
		SourceTokens:        sourceTokens,
		SummaryTokens:       summaryTokens,
	}
	if previous != nil {
		compaction.PreviousCompactionID = &previous.ID
	}

	if err := o.deps.Compactions.Create(ctx, compaction); err != nil {
		log.Warn("摘要落库失败，本轮按原样送出",
			zap.Uint64("conversation_id", conversationID),
			zap.Int64("covered_from", compaction.CoveredFromSequence),
			zap.Int64("covered_to", compaction.CoveredToSequence),
			zap.Error(err),
		)
		return nil, false
	}

	log.Info("上下文已压缩",
		zap.Uint64("conversation_id", conversationID),
		zap.Int64("covered_from", compaction.CoveredFromSequence),
		zap.Int64("covered_to", compaction.CoveredToSequence),
		zap.Int32("source_tokens", sourceTokens),
		zap.Int32("summary_tokens", summaryTokens),
	)

	return assembleHistory(summary, keep), true
}

// assembleHistory 把摘要与消息拼成上下文，顺序固定：摘要在前，消息按序号升序在后。
func assembleHistory(summary string, messages []entity.ConversationMessage) []HistoryMessage {
	history := make([]HistoryMessage, 0, len(messages)+1)
	if summary != "" {
		history = append(history, HistoryMessage{Speaker: summarySpeaker, Content: summary})
	}
	return append(history, toHistory(messages)...)
}

// toHistory 把库里的消息转成给模型的上下文，发言人取当时的显示名。
func toHistory(messages []entity.ConversationMessage) []HistoryMessage {
	history := make([]HistoryMessage, 0, len(messages))
	for _, message := range messages {
		history = append(history, HistoryMessage{
			Speaker: speakerName(message),
			Content: message.Content,
		})
	}
	return history
}

// estimateHistoryTokens 估算整段上下文的 token 数（含发言人名字）。
//
// 名字也要算：它会跟着一起进提示词，占的位置和正文一样真实。
func estimateHistoryTokens(history []HistoryMessage) int32 {
	var total int32
	for _, message := range history {
		total += estimateTokens(message.Speaker) + estimateTokens(message.Content)
	}
	return total
}

// contextBudget 取生效的 token 上限，没配就用默认值。
func (o *Orchestrator) contextBudget() int32 {
	if o.deps.ContextBudget > 0 {
		return int32(o.deps.ContextBudget)
	}
	return defaultMaxContextTokens
}

// keepRecentMessages 取生效的"保留最近几条"，没配就用默认值。
func (o *Orchestrator) keepRecentMessages() int {
	if o.deps.ContextKeepRecent > 0 {
		return o.deps.ContextKeepRecent
	}
	return defaultKeepRecentMessages
}
