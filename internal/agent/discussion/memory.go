package discussion

// 讨论收尾后提炼共享记忆（第 5 步）。
//
// 和 context.go 的分工是一条读一条写：
//
//	context.go  发言**前**把已经记下的记忆带进上下文（读）
//	memory.go   讨论**结束后**把这次聊出的结论记下来（写）
//
// 两件事的时机、触发点、失败后果都不一样，所以分两个文件。
//
// 为什么要有记忆这层东西：聊天记录会被压缩、会被截断，用户随口一句"我叫熊大"压进摘要后细节就丢了；
// 而记忆是专门挑出来、单独保存、不参与压缩的几条，所以每次发言都还带得上。

import (
	"context"
	"strings"

	"go.uber.org/zap"

	"narra/internal/model/entity"
)

const (
	// maxMemoriesPerExtraction 是一次提炼最多写几条。
	//
	// 与"每次带进上下文几条"同值（都是 10），少一个要记的数字。设上限是为了防一次讨论
	// 灌进十几条噪声——它们会在后面每一轮里反复占位置。
	maxMemoriesPerExtraction = 10

	// minMemoryImportance / maxMemoryImportance 与库上的 CHECK (importance BETWEEN 1 AND 5) 对齐。
	// Go 侧先夹一道：否则要到 INSERT 才报错，而那时报出来的只是一句约束冲突，
	// 看不出是模型给的值越界了。
	minMemoryImportance = 1
	maxMemoryImportance = 5

	// defaultMemoryImportance 是模型没给重要度时用的值，与库上 default 3 一致。
	defaultMemoryImportance = 3

	// maxMemoryContentRunes 是一条记忆正文的字数上限，超了就截断。
	//
	// 记忆**不参与压缩**：一条几千字的"记忆"会每轮都把上下文预算整口吃掉，而且谁都压不掉它。
	// 200 字足够说清一件事。
	maxMemoryContentRunes = 200

	// userSpeaker 是用户发言的显示名，与 speakerName 给用户消息的兜底称呼保持一致。
	userSpeaker = "用户"
)

// extractMemories 在讨论正常收尾后提炼一次记忆。
//
// 三件事按顺序做，任何一步失败都只记日志、直接返回：
//
//	① 叫提炼器（模型调用，在事务外）
//	② 归一化候选（挡住库里 CHECK 不接受的内容、截断超长正文、限制条数）
//	③ 一笔事务写入
//
// 为什么失败不往上抛：记忆是这场讨论的**副产品**。为了它把已经跑完、已经落库的讨论
// 变成"失败"，代价太大 —— 调用方会以为用户没得到回答，而实际上回答早写进消息表了。
//
// 为什么要一笔事务：一次提炼出的几条记忆属于同一批结论，写一半留一半，
// 下一轮看到的就是"只记得半件事"，比一条都没记更误导。
func (o *Orchestrator) extractMemories(
	ctx context.Context,
	classroomID uint64,
	request Request,
	topic string,
	outcomes []TurnOutcome,
	log *zap.Logger,
) {
	candidates, err := o.deps.Extractor.Extract(ctx, extractionMaterial(topic, outcomes))
	if err != nil {
		log.Warn("提炼共享记忆失败，本次不记记忆", zap.Error(err))
		return
	}

	memories := toMemories(normalizeCandidates(candidates), classroomID, request.ConversationID)
	if len(memories) == 0 {
		return
	}

	if err := o.deps.Tx.Run(ctx, func(ctx context.Context) error {
		for _, memory := range memories {
			if err := o.deps.Memories.Create(ctx, memory); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		log.Warn("共享记忆落库失败，本次不记记忆",
			zap.Int("count", len(memories)),
			zap.Error(err),
		)
		return
	}

	log.Info("已提炼共享记忆",
		zap.Uint64("conversation_id", request.ConversationID),
		zap.Int("count", len(memories)),
	)
}

// extractionMaterial 把"这场讨论发生了什么"整理成提炼器的输入：用户那句话 + 每个回合的发言。
//
// 直接用回合结果里的正文，不再回库查一遍消息：正文就在 outcomes 里，多查一次只是多一次查询，
// 而且查回来的还可能比这次运行实际产出的多（比如中间插进了别的消息）。
func extractionMaterial(topic string, outcomes []TurnOutcome) []HistoryMessage {
	material := make([]HistoryMessage, 0, len(outcomes)+1)
	material = append(material, HistoryMessage{Speaker: userSpeaker, Content: topic})
	for _, outcome := range outcomes {
		material = append(material, HistoryMessage{Speaker: outcome.AgentName, Content: outcome.Content})
	}
	return material
}

// normalizeCandidates 把模型给的候选过一遍筛子：丢掉库上不接受的内容，并限制条数。
//
// 为什么在 Go 侧判一遍而不是等数据库报错：库上那几条 CHECK 只说得清"某条约束没过"，
// 看不出是哪条候选、哪一项不合法；更要紧的是，一条坏候选会让**整批写入回滚** ——
// 模型偶尔编一个类型出来，不该把这次讨论其他合法的记忆一起赔进去。
func normalizeCandidates(candidates []MemoryCandidate) []MemoryCandidate {
	normalized := make([]MemoryCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if len(normalized) >= maxMemoriesPerExtraction {
			break
		}

		content := strings.TrimSpace(candidate.Content)
		if content == "" {
			continue
		}
		// 认不出的类型直接丢：写进去会被 CHECK 拦，显示出来模型也读不懂它。
		if _, ok := memoryTypeLabels[candidate.MemoryType]; !ok {
			continue
		}

		normalized = append(normalized, MemoryCandidate{
			MemoryType: candidate.MemoryType,
			Content:    truncateMemoryContent(content),
			Importance: normalizeImportance(candidate.Importance),
		})
	}
	return normalized
}

// normalizeImportance 把重要度夹进 [1,5]；没给（0 或负数）取默认值。
//
// 越界只夹不丢：重要度只是个排序权重，"夹到 5"和"丢掉这条"相比，前者保住了信息，
// 后者让一条本来有价值的结论凭空消失。
func normalizeImportance(importance int16) int16 {
	switch {
	case importance < minMemoryImportance:
		return defaultMemoryImportance
	case importance > maxMemoryImportance:
		return maxMemoryImportance
	default:
		return importance
	}
}

// truncateMemoryContent 按字数截断记忆正文，并留一个省略号。
//
// 按**字数**（rune）而不是字节数切：中文一个字三个字节，按字节切会出现"三个字变两个字"，
// 而且切在汉字中间会留下无效编码，PostgreSQL 会直接拒绝。
func truncateMemoryContent(content string) string {
	runes := []rune(content)
	if len(runes) <= maxMemoryContentRunes {
		return content
	}
	return string(runes[:maxMemoryContentRunes]) + "…"
}

// toMemories 把候选变成可以落库的记忆行。
//
// scope 一律 conversation（决策清单 S5-4，用户按参考项目拍板）：记忆是从**单场讨论**里提炼的，
// "它对整堂课都成立"是一个更强的断言，凭一次提炼下不了 —— 而且参考项目的外部表现就是"一场归一场"。
// 将来要让记忆跨对话复用，改的就是这一行。
//
// source_message_id / source_turn_id 先留空（决策 S5-9）：提炼是整场一次，一条记忆往往综合了
// 多条发言，硬指其中一条反而是错的。两列可空，将来要做"点记忆跳回原话"时再补。
func toMemories(candidates []MemoryCandidate, classroomID uint64, conversationID uint64) []*entity.SharedContextMemory {
	memories := make([]*entity.SharedContextMemory, 0, len(candidates))
	for _, candidate := range candidates {
		scopeConversationID := conversationID
		memories = append(memories, &entity.SharedContextMemory{
			ClassroomID:    classroomID,
			ConversationID: &scopeConversationID,
			Scope:          entity.MemoryScopeConversation,
			MemoryType:     candidate.MemoryType,
			Content:        candidate.Content,
			Importance:     candidate.Importance,
			Status:         entity.MemoryStatusActive,
		})
	}
	return memories
}
