package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"

	"narra/internal/ownership"
	"narra/internal/toolcall"
	"narra/pkg/logger"
)

// maxCallRetry 是一次工具调用失败后的重试次数，只用于超时这类临时故障。
const maxCallRetry = 1

// callRetryBackoff 是两次工具调用之间的固定等待，与模型调用的重试间隔一致。
const callRetryBackoff = time.Second

// EinoTools 把**内置工具**与 MCP 注册表中的**远端工具**合并成 Eino 的 BaseTool 列表。
//
// webSearch 只拦远端工具：为假时不返回它们，但内置工具（本服务自己实现的，
// 目前是知识库检索 rag_retrieve）照常返回 —— 用户没开联网搜索，不等于不希望
// agent 去查自己的资料。
//
// 闸门放在这里而不是调用方，是因为"哪些工具能上"取决于 Manager 手里的东西：
// 内置工具已经在内存里，远端工具要先连过 server 才有。调用方只该回答"这次允不允许联网"。
//
// 内置工具排在前面并已排序（见 localToolList）：它们是本服务的核心能力，
// 而远端工具取决于用户配了哪些 server，顺序不该随配置变化。
func (m *Manager) EinoTools(ctx context.Context, webSearch bool) ([]tool.BaseTool, error) {
	result := m.localToolList()
	result = bindKnowledgeOwner(result, ownership.FromContext(ctx))
	if !webSearch {
		return result, nil
	}

	descriptors := m.ListTools()
	for _, descriptor := range descriptors {
		if !visibleToOwner(ctx, descriptor.ServerID) {
			continue
		}
		inputSchema := new(jsonschema.Schema)
		if err := json.Unmarshal(descriptor.InputSchema, inputSchema); err != nil {
			return nil, fmt.Errorf("解析 MCP tool %q schema: %w", descriptor.ID, err)
		}
		result = append(result, &einoTool{
			manager: m,
			info: &schema.ToolInfo{
				Name:        descriptor.ID,
				Desc:        descriptor.Description,
				ParamsOneOf: schema.NewParamsOneOfByJSONSchema(inputSchema),
			},
		})
	}
	return result, nil
}

type einoTool struct {
	manager *Manager
	info    *schema.ToolInfo
}

// DiscussionTools narrows permissions without changing the classroom worker API.
// MCP annotations are trusted only for user-configured servers; unannotated or
// writable tools are not exposed to discussion agents.
func (m *Manager) DiscussionTools(ctx context.Context, webSearch bool) ([]tool.BaseTool, error) {
	result := m.localToolList()
	result = bindKnowledgeOwner(result, ownership.FromContext(ctx))
	if !webSearch {
		return result, nil
	}
	for _, descriptor := range m.ListTools() {
		if !visibleToOwner(ctx, descriptor.ServerID) {
			continue
		}
		if !descriptor.ReadOnly {
			continue
		}
		params := new(jsonschema.Schema)
		if err := json.Unmarshal(descriptor.InputSchema, params); err != nil {
			return nil, err
		}
		result = append(result, &einoTool{manager: m, info: &schema.ToolInfo{Name: descriptor.ID, Desc: descriptor.Description, ParamsOneOf: schema.NewParamsOneOfByJSONSchema(params)}})
	}
	return result, nil
}

type ownedKnowledgeTool struct {
	*knowledgeRetrieveTool
	ownerID uint64
}

func (t *ownedKnowledgeTool) InvokableRun(ctx context.Context, arguments string, opts ...tool.Option) (string, error) {
	return t.knowledgeRetrieveTool.InvokableRun(ownership.WithOwner(ctx, t.ownerID), arguments, opts...)
}

func bindKnowledgeOwner(tools []tool.BaseTool, ownerID uint64) []tool.BaseTool {
	if ownerID == 0 {
		return tools
	}
	for index, candidate := range tools {
		if knowledge, ok := candidate.(*knowledgeRetrieveTool); ok {
			tools[index] = &ownedKnowledgeTool{knowledgeRetrieveTool: knowledge, ownerID: ownerID}
		}
	}
	return tools
}

// Info 返回工具的元信息供 Eino agent 使用。
func (t *einoTool) Info(context.Context) (*schema.ToolInfo, error) {
	return t.info, nil
}

// InvokableRun 将 Eino 的调用转发给 MCP Manager 并返回序列化结果。
//
// 运行期失败（连不上、超时、工具自己报错）不返回 error：Eino 的工具节点遇到 error 会终止
// 整个 Agent，一次超时就够让这一页的调研整块作废。失败原因改成作为工具结果交回模型，模型
// 可以按它自己的提示换一个查询词重试；原因同时写进 ctx 上的记录器，供调用方说清"这一页
// 为什么没有外部资料"。参数不是合法 JSON 属协议级错误，与内置工具的约定一致，仍直接报错。
func (t *einoTool) InvokableRun(ctx context.Context, arguments string, _ ...tool.Option) (string, error) {
	if !json.Valid([]byte(arguments)) {
		return "", fmt.Errorf("MCP tool %q 参数不是合法 JSON", t.info.Name)
	}

	start := time.Now()
	result, attempts, err := callTool(ctx, t.manager, t.info.Name, json.RawMessage(arguments))
	elapsed := time.Since(start)
	if result == nil {
		if err == nil {
			err = fmt.Errorf("MCP tool %q 没有返回结果", t.info.Name)
		}
		logger.Warn("MCP 工具调用失败",
			zap.String("tool", t.info.Name), zap.Int("attempts", attempts), zap.Duration("elapsed", elapsed), zap.Error(err))
		return t.fail(ctx, err), nil
	}

	encoded, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		logger.Warn("MCP 工具结果编码失败", zap.String("tool", t.info.Name), zap.Duration("elapsed", elapsed), zap.Error(marshalErr))
		return t.fail(ctx, fmt.Errorf("编码 MCP tool %q 结果: %w", t.info.Name, marshalErr)), nil
	}
	if err != nil {
		logger.Warn("MCP 工具返回错误结果",
			zap.String("tool", t.info.Name), zap.Int("attempts", attempts), zap.Duration("elapsed", elapsed), zap.Error(err))
		toolcall.FromContext(ctx).Fail(err)
		return string(encoded), nil
	}
	encoded, compressed, compactErr := compactToolResult(encoded)
	if compactErr != nil {
		logger.Warn("压缩 MCP 工具结果失败", zap.String("tool", t.info.Name), zap.Error(compactErr))
		return t.fail(ctx, fmt.Errorf("压缩 MCP tool %q 结果: %w", t.info.Name, compactErr)), nil
	} else if compressed {
		logger.Info("MCP 工具结果已压缩", zap.String("tool", t.info.Name), zap.Int("bytes", len(encoded)))
	}
	logger.Info("MCP 工具调用完成",
		zap.String("tool", t.info.Name), zap.Int("attempts", attempts), zap.Duration("elapsed", elapsed))
	return string(encoded), nil
}

// fail 记下失败原因，并返回给模型看的工具结果。
func (t *einoTool) fail(ctx context.Context, err error) string {
	toolcall.FromContext(ctx).Fail(err)
	return fmt.Sprintf("工具 %s 调用失败：%s。可以换一个查询词重试一次，仍然失败就按任务要求交回空结果。",
		t.info.Name, err.Error())
}

// callTool 调用远端工具，临时故障重试一次，返回最终结果与真实调用次数。
func callTool(ctx context.Context, manager *Manager, name string, arguments json.RawMessage) (*sdk.CallToolResult, int, error) {
	for attempt := 1; ; attempt++ {
		result, err := manager.CallTool(ctx, name, arguments)
		if err == nil || result != nil || attempt > maxCallRetry || !retryableToolError(err) {
			return result, attempt, err
		}
		// 上游如果已经不等了，再试一次只是白等一个超时。
		select {
		case <-ctx.Done():
			return result, attempt, err
		case <-time.After(callRetryBackoff):
		}
	}
}

// retryableToolError 判断工具调用错误是否属于值得重试一次的临时故障。
//
// 判定的结果有两个去处：调用方要不要再试一次，以及 Manager 要不要把这条连接作废
// （见 Manager.CallTool）。所以只认连接级的故障，工具自己报的错（结果过大、工具返回
// isError）不算——那些换一条连接也没用。
func retryableToolError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	// 会话在服务端已经不存在，重连一次就能拿到新会话，属于该重试的临时故障。
	if errors.Is(err, sdk.ErrSessionMissing) {
		return true
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "deadline") || strings.Contains(text, "timeout") ||
		strings.Contains(text, "connection") || strings.Contains(text, "eof") ||
		strings.Contains(text, "session not found")
}
