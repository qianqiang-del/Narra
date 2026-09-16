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

// toolID 生成命名空间格式的工具 ID：mcp.<serverID>.<remoteName>。
func toolID(serverID, remoteName string) string {
	return "mcp." + serverID + "." + remoteName
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
