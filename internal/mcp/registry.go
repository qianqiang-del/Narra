package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type ToolDescriptor struct {
	ID          string
	ServerID    string
	RemoteName  string
	Description string
	InputSchema json.RawMessage
}

type Registry struct {
	tools []ToolDescriptor
	byID  map[string]ToolDescriptor
}

// NewRegistry 从工具描述列表构建注册表，校验去重并按 ID 排序。
func NewRegistry(tools []ToolDescriptor) (*Registry, error) {
	items := append([]ToolDescriptor(nil), tools...)
	byID := make(map[string]ToolDescriptor, len(items))
	for index := range items {
		item := &items[index]
		item.ServerID = strings.TrimSpace(item.ServerID)
		item.RemoteName = strings.TrimSpace(item.RemoteName)
		if item.ServerID == "" || item.RemoteName == "" {
			return nil, fmt.Errorf("mcp tool 的 server id 和远端名称不能为空")
		}
		item.ID = toolID(item.ServerID, item.RemoteName)
		if len(item.InputSchema) == 0 || !json.Valid(item.InputSchema) {
			return nil, fmt.Errorf("mcp tool %q 的 input schema 非法", item.ID)
		}
		if _, exists := byID[item.ID]; exists {
			return nil, fmt.Errorf("mcp tool %q 重复", item.ID)
		}
		byID[item.ID] = *item
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return &Registry{tools: items, byID: byID}, nil
}

// toolID 生成命名空间格式的工具 ID：mcp_<serverID>_<remoteName>。
// 大模型接口只接受 [A-Za-z0-9_-]，其余字符一律换成下划线。
func toolID(serverID, remoteName string) string {
	return sanitizeToolName("mcp_" + serverID + "_" + remoteName)
}

// sanitizeToolName 把非 [A-Za-z0-9_-] 的字节换成下划线。
func sanitizeToolName(name string) string {
	var builder strings.Builder
	builder.Grow(len(name))
	for index := 0; index < len(name); index++ {
		char := name[index]
		switch {
		case char >= 'a' && char <= 'z',
			char >= 'A' && char <= 'Z',
			char >= '0' && char <= '9',
			char == '_',
			char == '-':
			builder.WriteByte(char)
		default:
			builder.WriteByte('_')
		}
	}
	return builder.String()
}

// List 返回注册表中所有工具的副本，nil 安全。
func (r *Registry) List() []ToolDescriptor {
	if r == nil {
		return nil
	}
	return append([]ToolDescriptor(nil), r.tools...)
}

// Find 按命名空间 ID 精确查找工具，nil 安全。
func (r *Registry) Find(id string) (ToolDescriptor, bool) {
	if r == nil {
		return ToolDescriptor{}, false
	}
	tool, ok := r.byID[id]
	return tool, ok
}

// selectTools 按启用名单过滤工具，返回保留的工具与名单里没匹配上的名字；名单为空或全是空白表示不过滤。
func selectTools(tools []ToolDescriptor, enabled []string) ([]ToolDescriptor, []string) {
	wanted := make(map[string]struct{}, len(enabled))
	for _, name := range enabled {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			wanted[trimmed] = struct{}{}
		}
	}
	if len(wanted) == 0 {
		return tools, nil
	}

	selected := make([]ToolDescriptor, 0, len(tools))
	for _, tool := range tools {
		if _, ok := wanted[tool.RemoteName]; !ok {
			continue
		}
		selected = append(selected, tool)
		delete(wanted, tool.RemoteName)
	}

	missing := make([]string, 0, len(wanted))
	for name := range wanted {
		missing = append(missing, name)
	}
	sort.Strings(missing)
	return selected, missing
}
