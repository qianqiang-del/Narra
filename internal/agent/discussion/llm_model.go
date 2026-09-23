package discussion

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"narra/internal/agent"
	"narra/internal/model/entity"
	"narra/pkg/llm"
)

// chatClient 是本模型对"对话补全客户端"的最小依赖面，由 *llm.Client 满足。
//
// 定义小接口而不是直接收 *llm.Client：单测可以塞一个替身，断言提示词拼得对不对、
// 协议解析兜不兜得住，不必起 HTTP 服务；将来换成流式客户端也只动这里。
type chatClient interface {
	Chat(ctx context.Context, req llm.ChatRequest) (*llm.Completion, error)
}

// LLMModel 是 Model 的真实实现：拼提示词 → 调大模型 → 解析出正文与下一步动作。
//
// 它只负责"一次发言"：谁发言、什么时候停是 Director 与 Orchestrator 的事。
// 提示词分两层——身份层（你是谁）来自 internal/agent 的 BuildRoundtablePrompt，
// 输入层（这次该你说什么）在 buildUserPrompt 里拼。
type LLMModel struct {
	client chatClient
}

var _ Model = (*LLMModel)(nil)

// NewLLMModel 创建真实模型。client 由装配方按 provider 配置建好（读配置 → 解密 → 建 client）。
func NewLLMModel(client chatClient) (*LLMModel, error) {
	if client == nil {
		return nil, fmt.Errorf("圆桌模型缺少对话客户端")
	}
	return &LLMModel{client: client}, nil
}

// Generate 生成一个角色这一轮的发言。
//
// 提示词拼不出来、模型调用失败、回复为空都算失败（由编排层收尾成 run.failed）；
// 但"模型没按 JSON 协议回话"不算失败——整段当正文、动作留空，让讨论继续往下走。
func (m *LLMModel) Generate(ctx context.Context, request GenerationRequest) (GenerationResponse, error) {
	system, ok := buildSystemPrompt(request.Participant)
	if !ok {
		return GenerationResponse{}, fmt.Errorf(
			"角色 %q 的提示词拼不出来（role_type = %q 不在角色层里）",
			request.Participant.Name, request.Participant.Role,
		)
	}

	completion, err := m.client.Chat(ctx, llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "system", Content: system},
			{Role: "user", Content: buildUserPrompt(request)},
		},
	})
	if err != nil {
		return GenerationResponse{}, fmt.Errorf("圆桌发言调用模型失败: %w", err)
	}

	response, err := parseReply(completion.Content)
	if err != nil {
		return GenerationResponse{}, err
	}
	if completion.Usage != nil {
		response.InputTokens = int32(completion.Usage.PromptTokens)
		response.OutputTokens = int32(completion.Usage.CompletionTokens)
	}
	return response, nil
}

// buildSystemPrompt 拼这个角色的圆桌提示词。
//
// Participant 里带的正好是拼提示词需要的三样（名字、role_type、persona），
// 这里组装成 entity.PresetAgent 只是为了复用既有的拼装函数——不落库、不查库。
func buildSystemPrompt(participant Participant) (string, bool) {
	return agent.BuildRoundtablePrompt(entity.PresetAgent{
		Name:     participant.Name,
		RoleType: participant.Role,
		Persona:  participant.Persona,
	})
}

// buildUserPrompt 组装"这次该你说什么"的输入：主题、此前的发言、轮次。
func buildUserPrompt(request GenerationRequest) string {
	var builder strings.Builder

	builder.WriteString("# 讨论主题\n")
	builder.WriteString(strings.TrimSpace(request.Topic))
	builder.WriteString("\n\n# 此前的发言\n")
	if len(request.History) == 0 {
		builder.WriteString("（还没有人发言，你是第一个）\n")
	} else {
		for _, message := range request.History {
			builder.WriteString(message.Speaker)
			builder.WriteString("：")
			builder.WriteString(message.Content)
			builder.WriteString("\n")
		}
	}

	fmt.Fprintf(&builder,
		"\n# 你的任务\n这是第 %d 轮，轮到「%s」发言。请按输出协议只回一个 JSON 对象。\n",
		request.TurnNo, request.Participant.Name,
	)
	return builder.String()
}

// replyEnvelope 是方案 A 的输出协议：模型回一个 JSON，正文与下一步动作各一个字段。
type replyEnvelope struct {
	Content    string `json:"content"`
	NextAction string `json:"next_action"`
}

// parseReply 解析模型的回复。
//
// 协议要求 JSON，但真模型不一定听话：解析失败时把整段当正文、动作留空——
// 调用方（TurnTakingDirector）会按"换人"兜底。于是"格式没守规矩"只影响讨论效果，
// 不会让讨论崩掉；真正致命的只有"什么都没回"。
func parseReply(raw string) (GenerationResponse, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return GenerationResponse{}, fmt.Errorf("模型返回了空回复")
	}

	var envelope replyEnvelope
	if err := json.Unmarshal([]byte(stripCodeFence(trimmed)), &envelope); err != nil {
		return GenerationResponse{Content: trimmed}, nil
	}

	content := strings.TrimSpace(envelope.Content)
	if content == "" {
		// JSON 解出来了但正文是空的（模型把话写进了别的字段）：退回整段，
		// 至少别让用户看到一条空白消息。
		content = trimmed
	}
	return GenerationResponse{
		Content:    content,
		NextAction: strings.TrimSpace(envelope.NextAction),
	}, nil
}

// stripCodeFence 去掉模型爱加的 ```json 围栏：协议里写了不要围栏，但它经常不听。
func stripCodeFence(raw string) string {
	if !strings.HasPrefix(raw, "```") {
		return raw
	}
	// 去掉第一行（``` 或 ```json），再去掉结尾的 ```
	index := strings.Index(raw, "\n")
	if index < 0 {
		return raw
	}
	body := raw[index+1:]
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(body), "```"))
}
