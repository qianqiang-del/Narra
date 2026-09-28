package classroom

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"

	"narra/internal/rag/einoretriever"
	"narra/internal/repository"
	appcrypto "narra/pkg/crypto"
	"narra/pkg/llm"
	"narra/pkg/logger"
)

type Synthesizer interface {
	Synthesize(ctx context.Context, text, voice string) ([]byte, error)
}

// ToolSource 提供 Eino 工具，webSearch 表示本次生成是否允许联网搜索。
//
// webSearch 只关系**远端**工具（V1 是联网搜索）；内置工具（知识库检索）与它无关，
// 始终会返回，闸门与理由见 internal/mcp/adapter.go 的 EinoTools。
type ToolSource interface {
	EinoTools(ctx context.Context, webSearch bool) ([]tool.BaseTool, error)
}

// Deps 是生成课堂需要的外部依赖。
type Deps struct {
	Providers     repository.LLMProviderRepository
	Classrooms    repository.ClassroomRepository
	Scenes        repository.SceneRepository
	Segments      repository.SceneSegmentRepository
	Agents        repository.ClassroomAgentRepository
	Roles         repository.RoleRepository
	Tx            repository.TransactionManager
	TTS           Synthesizer
	AudioDir      string
	Tools         ToolSource
	EncryptionKey []byte
	// PageConcurrency 是同时生成的页面数，TTSPoolSize 是同时发出的语音合成请求数；两者不大于 0 时按缺省值。
	PageConcurrency int
	TTSPoolSize     int
	// MaxDuration 是一堂课生成的总时长上限，不大于 0 表示不限制。
	MaxDuration time.Duration
}

// runtime 是一个课堂的运行环境：模型 + 工具。每个课堂建一套，用完即弃。
type runtime struct {
	chatModel model.ToolCallingChatModel
	tools     []tool.BaseTool

	mutex sync.Mutex
	// forcedUnsupported 记录本 Provider 拒绝过强制工具调用，之后不再重试那条路。
	forcedUnsupported bool
}

// newRuntime 读配置 → 解密 → 建模型 → 定工具集。
//
// allowSearch 为假时不挂**远端**工具（V1 是联网搜索）；内置工具（知识库检索）不受它影响。
func newRuntime(ctx context.Context, deps Deps, providerID uint64, modelID string, allowSearch bool) (*runtime, error) {
	if deps.Providers == nil {
		return nil, fmt.Errorf("建运行时：缺少大模型配置仓储")
	}
	if len(deps.EncryptionKey) == 0 {
		return nil, fmt.Errorf("建运行时：缺少解密密钥")
	}

	provider, err := deps.Providers.FindByID(ctx, providerID)
	if err != nil {
		return nil, fmt.Errorf("建运行时：读取大模型配置 #%d 失败: %w", providerID, err)
	}
	if modelID == "" {
		if modelID, err = firstModel(provider.Models); err != nil {
			return nil, fmt.Errorf("建运行时：%w", err)
		}
	}

	apiKey, err := appcrypto.Decrypt(provider.APIKeyEncrypted, deps.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("建运行时：解密 API Key 失败: %w", err)
	}

	client, err := llm.NewClient(llm.Config{
		BaseURL: provider.BaseURL,
		APIKey:  apiKey,
		Model:   modelID,
		Timeout: time.Duration(provider.TimeoutSeconds) * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("建运行时：创建大模型客户端失败: %w", err)
	}
	chatModel, err := llm.NewEinoChatModel(client)
	if err != nil {
		return nil, fmt.Errorf("建运行时：创建 Eino 适配器失败: %w", err)
	}

	var tools []tool.BaseTool
	if deps.Tools != nil {
		if tools, err = deps.Tools.EinoTools(ctx, allowSearch); err != nil {
			return nil, fmt.Errorf("建运行时：获取 MCP 工具失败: %w", err)
		}
	} else if allowSearch {
		return nil, fmt.Errorf("建运行时：需要联网但没有工具来源")
	}

	return &runtime{chatModel: chatModel, tools: tools}, nil
}

// plannerAgent 建规划 Agent：工具集是调研工具加交卷工具。
//
// 交卷工具进 ToolReturnDirectly，模型一调它 Agent 就返回，返回值即计划 JSON。
func (r *runtime) plannerAgent(ctx context.Context) (*react.Agent, error) {
	tools := make([]tool.BaseTool, 0, len(r.tools)+1)
	tools = append(tools, r.tools...)
	tools = append(tools, newEmitPlanTool())

	agent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel:   r.chatModel,
		MaxStep:            maxPlannerStep,
		ToolsConfig:        compose.ToolsNodeConfig{Tools: tools},
		ToolReturnDirectly: map[string]struct{}{toolNameEmitPlan: {}},
	})
	if err != nil {
		return nil, fmt.Errorf("构造规划 Agent 失败: %w", err)
	}
	return agent, nil
}

// retrievalContext 把本次课堂的模型注入检索链路：Agent 只给一条检索词时，
// 多查询门面会用它自动扩写（见 einoretriever.WithRewriteModel）。只影响知识库
// 检索工具，不改变 Agent 自己的模型调用。
//
// 规划与调研两个 Agent 都会调用带检索工具的生成入口，各自调用前包一层即可。
func (r *runtime) retrievalContext(ctx context.Context) context.Context {
	return einoretriever.WithRewriteModel(ctx, r.chatModel)
}

// generateToolCall 调一次模型，返回指定工具调用的参数。
//
// 默认按强制工具调用发；Provider 拒绝这条参数时（思考模式的模型只允许自动选择工具）
// 退回自动选择重发一次，并记住这个能力，同一堂课后面的调用不再白付一次失败请求。
func (r *runtime) generateToolCall(ctx context.Context, messages []*schema.Message, info *schema.ToolInfo) (string, error) {
	tools := []*schema.ToolInfo{info}
	choice := schema.ToolChoiceForced
	if !r.allowForcedToolChoice() {
		choice = schema.ToolChoiceAllowed
	}
	message, err := r.chatModel.Generate(ctx, messages, model.WithTools(tools), model.WithToolChoice(choice))
	if err != nil && choice == schema.ToolChoiceForced && isToolChoiceRejected(err) {
		logger.Warn("Provider 不支持强制工具调用，改用自动选择", zap.String("tool", info.Name), zap.Error(err))
		r.markForcedUnsupported()
		message, err = r.chatModel.Generate(ctx, messages, model.WithTools(tools), model.WithToolChoice(schema.ToolChoiceAllowed))
	}
	if err != nil {
		return "", err
	}
	arguments := toolCallArguments(message, info.Name)
	if arguments == "" {
		return "", fmt.Errorf("模型没有调用 %s 交卷", info.Name)
	}
	return arguments, nil
}

// allowForcedToolChoice 报告本 Provider 是否还能用强制工具调用。
func (r *runtime) allowForcedToolChoice() bool {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return !r.forcedUnsupported
}

// markForcedUnsupported 记住本 Provider 不支持强制工具调用。
func (r *runtime) markForcedUnsupported() {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.forcedUnsupported = true
}

// isToolChoiceRejected 判断错误是否是 Provider 不接受这个 tool_choice 参数。
func isToolChoiceRejected(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "tool_choice")
}

// toolCallArguments 从消息里取指定工具调用的参数。
func toolCallArguments(message *schema.Message, name string) string {
	if message == nil {
		return ""
	}
	for _, call := range message.ToolCalls {
		if call.Function.Name == name {
			return call.Function.Arguments
		}
	}
	return ""
}

// firstModel 取 llm_providers.models 里的第一个模型。
func firstModel(raw json.RawMessage) (string, error) {
	var models []string
	if err := json.Unmarshal(raw, &models); err != nil {
		return "", fmt.Errorf("解析模型列表失败: %w", err)
	}
	if len(models) == 0 {
		return "", fmt.Errorf("大模型配置的模型列表为空")
	}
	return models[0], nil
}
