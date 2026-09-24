package classroom

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"

	"narra/internal/repository"
	appcrypto "narra/pkg/crypto"
	"narra/pkg/llm"
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
}

// runtime 是一个课堂的运行环境：模型 + 工具。每个课堂建一套，用完即弃。
type runtime struct {
	chatModel model.ToolCallingChatModel
	tools     []tool.BaseTool
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

// outlineAgent 建大纲 Agent。工具集为空时同样能建。
func (r *runtime) outlineAgent(ctx context.Context) (*react.Agent, error) {
	config := &react.AgentConfig{
		ToolCallingModel: r.chatModel, // 不自己 WithTools：工具经 ToolsConfig 给
		MaxStep:          4,
	}
	if len(r.tools) > 0 {
		config.ToolsConfig = compose.ToolsNodeConfig{Tools: r.tools}
	}
	agent, err := react.NewAgent(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("构造大纲 Agent 失败: %w", err)
	}
	return agent, nil
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
