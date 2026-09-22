package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
)

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
	_ = ctx
	result := m.localToolList()
	if !webSearch {
		return result, nil
	}

	descriptors := m.ListTools()
	for _, descriptor := range descriptors {
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

// Info 返回工具的元信息供 Eino agent 使用。
func (t *einoTool) Info(context.Context) (*schema.ToolInfo, error) {
	return t.info, nil
}

// InvokableRun 将 Eino 的调用转发给 MCP Manager 并返回序列化结果。
func (t *einoTool) InvokableRun(ctx context.Context, arguments string, _ ...tool.Option) (string, error) {
	if !json.Valid([]byte(arguments)) {
		return "", fmt.Errorf("MCP tool %q 参数不是合法 JSON", t.info.Name)
	}
	result, err := t.manager.CallTool(ctx, t.info.Name, json.RawMessage(arguments))
	if err != nil && result == nil {
		return "", err
	}
	encoded, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		return "", fmt.Errorf("编码 MCP tool %q 结果: %w", t.info.Name, marshalErr)
	}
	if err != nil {
		return string(encoded), err
	}
	return string(encoded), nil
}
