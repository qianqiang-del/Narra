package discussion

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	"narra/internal/model/entity"
)

// 上下文组装（第 4 步）。
//
// 每次让某个角色发言之前，都要先把"他现在需要知道的"拼出来。顺序由设计文档 §5.5 定死：
//
//	系统提示词 → 最新摘要 → 摘要之后的完整消息 → 共享记忆 → 当前用户消息
//
// 其中系统提示词在提示词那一层拼（不在本文件），共享记忆是第 5 步的事（这里留位），
// 所以本文件负责中间两段：**最新摘要 + 摘要之后的完整消息**。
//
// 拼完还要做预算判断：总长度超过阈值就把老消息压成摘要。
// 不这么做的后果不是"慢一点"，而是某一轮突然超出模型窗口、整场讨论直接报错。

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
)

// buildContext 组装这次发言要带的上下文，必要时就地压一次。
//
// 压缩失败不返回错误：这里是讨论的必经之路，一次摘要没写好不该让整场讨论失败。
// 三种"没压成"的情况（可压内容为空、摘要没比原文短、落库失败）都会记日志并按原样送出 ——
// 代价只是这一轮上下文长一点。
func (o *Orchestrator) buildContext(ctx context.Context, conversationID uint64, log *zap.Logger) ([]HistoryMessage, error) {
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

	history := assembleHistory(previousSummary, messages)
	if estimateHistoryTokens(history) <= o.contextBudget() {
		return history, nil
	}

	compacted, ok := o.compact(ctx, conversationID, previous, messages, log)
	if !ok {
		return history, nil
	}
	return compacted, nil
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
