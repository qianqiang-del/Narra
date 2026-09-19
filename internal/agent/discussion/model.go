package discussion

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Model 是本包对"大模型"的唯一依赖。
//
// 编排逻辑不关心背后是 DeepSeek、OpenAI 还是一个假模型，只关心"给一段上下文，换回一段话"。
// 把这一步抽成接口，是为了让整条链路能在不联网、不花钱、结果确定的前提下跑通和测试 ——
// 编排是否写对了库，跟模型答得好不好，是两件必须分开验证的事。
type Model interface {
	Generate(ctx context.Context, request GenerationRequest) (GenerationResponse, error)
}

// GenerationRequest 是一次发言请求。
//
// 只带"角色现在需要知道的"：他是谁、大家在聊什么、之前谁说过什么。
// 不带 trace_id、库句柄这类东西 —— 那些是编排层的事，模型不需要也不该看到。
type GenerationRequest struct {
	Participant Participant      // 这次轮到谁
	Topic       string           // 讨论主题（触发消息的正文）
	TurnNo      int16            // 这是第几轮发言
	History     []HistoryMessage // 此前的发言，按时间升序
}

// HistoryMessage 是喂给历史上下文的一条发言。
// Speaker 已经是"给人看的名字"而不是角色 ID：提示词里要的是"物理老师：…"这种形式。
type HistoryMessage struct {
	Speaker string
	Content string
}

// GenerationResponse 是一次发言结果。
type GenerationResponse struct {
	Content      string
	InputTokens  int32
	OutputTokens int32

	// NextAction 是模型对"接下来该怎么走"的判断，取值见 entity.AgentTurnAction*。
	//
	// 为什么让模型给这个：像"该不该停下来问用户"这种事，只有读得懂内容的一方才能判断；
	// 用轮数、关键字之类的规则去猜，猜不准。
	//
	// 允许为空、也允许是编出来的值 —— 调用方一律按"换人"兜底（见 TurnTakingDirector）。
	// 也就是说：模型不按格式回话，只会让讨论效果差一点，不会让讨论崩掉。
	NextAction string
}

// FakeModel 是不调用任何大模型的替身。
//
// 它存在的意义：第 2 步要验证的是"编排有没有把过程正确写进库"，而不是"模型答得对不对"。
// 用真模型会把这两件事混在一起 —— 慢了、贵了，出问题还分不清是流程错了还是模型瞎答。
// 输出里刻意带上角色名和轮次，方便人工核对"每个回合确实换了人、序号确实递增"。
type FakeModel struct{}

// Generate 返回一段格式固定、内容可辨识的假回复。
func (FakeModel) Generate(ctx context.Context, request GenerationRequest) (GenerationResponse, error) {
	if err := ctx.Err(); err != nil {
		return GenerationResponse{}, err
	}

	content := fmt.Sprintf(
		"【%s】关于「%s」，我的看法是：这是我在第 %d 轮发言。（假模型生成，未调用真实大模型）",
		request.Participant.Name,
		request.Topic,
		request.TurnNo,
	)

	return GenerationResponse{
		Content:      content,
		InputTokens:  estimateTokens(topicAndHistory(request)),
		OutputTokens: estimateTokens(content),
	}, nil
}

// topicAndHistory 把这次请求的输入拼成一段文本，只为估算 token 用。
func topicAndHistory(request GenerationRequest) string {
	var builder strings.Builder
	builder.WriteString(request.Topic)
	for _, message := range request.History {
		builder.WriteString(message.Speaker)
		builder.WriteString(message.Content)
	}
	return builder.String()
}

// estimateTokens 用字符数粗略估算 token 数。
//
// 这是**估算**，不是真实分词：中文一个字通常接近 1 个 token，英文一个词可能拆成多个，
// 所以这个值只能用来做"上下文大概多长了"的判断，不能拿来做计费对账。
// 等接入真实模型后，token 数一律以模型返回的用量为准 —— 那时这个函数只服务于假模型。
func estimateTokens(text string) int32 {
	return int32(utf8.RuneCountInString(text))
}
