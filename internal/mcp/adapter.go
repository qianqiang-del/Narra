package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
)

// EinoTools 将 MCP 注册表中的工具转换为 Eino 的 BaseTool 列表。
func (m *Manager) EinoTools(ctx context.Context) ([]tool.BaseTool, error) {
	_ = ctx
	descriptors := m.ListTools()
	result := make([]tool.BaseTool, 0, len(descriptors))
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
