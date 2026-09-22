package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/cloudwego/eino/components/tool"
)

// local.go 本地工具：由本服务自己实现、直接挂给 Eino agent 的工具
// （目前只有知识库检索 rag_retrieve，见 knowledge_tool.go）。
//
// 为什么单独存而不是并进 Registry：注册表是**远端 MCP server 工具**的投影，
// 每次 RefreshTools（改配置、重连、启动）都从远端重新拉取并整体重建
// （见 Manager.RefreshTools 里的 NewRegistry(descriptors)）—— 本地工具放进去，
// 会被任何一次刷新无声地抹掉，而且只在"改过一次 MCP 配置"之后才失效，极难查。
// 所以它们各存一份，只在 EinoTools 里合并。
//
// 命名上**不加** mcp_ 前缀（远端工具是 mcp_<serverID>_<name>，见 toolID）：那个前缀是
// "能经 CallTool 走网络回源"的记号，本地工具用了会让调用方找错路 ——
// ListTools 与 CallTool 里都没有它们，那是刻意的：它们不是远端资源。
// 将来界面要给内置工具开一栏，应当另立一个只读接口，而不是混进这两个方法。

// RegisterLocalTool 注册一个本地工具，按 Info().Name 去重。
//
// 在应用启动时调用一次（见 internal/app/app.go）。名字重复是装配错误，
// 直接报错而不是覆盖：两个工具同名会让 agent 拿到哪一个变成注册顺序的函数。
func (m *Manager) RegisterLocalTool(local tool.BaseTool) error {
	if local == nil {
		return fmt.Errorf("本地工具不能为空")
	}
	info, err := local.Info(context.Background())
	if err != nil {
		return fmt.Errorf("读取本地工具信息失败: %w", err)
	}
	if info == nil {
		return fmt.Errorf("本地工具没有元信息")
	}
	name := strings.TrimSpace(info.Name)
	if name == "" {
		return fmt.Errorf("本地工具名称不能为空")
	}
	// 工具名会原样进大模型的 tools 声明，而那边的字符集只有 [A-Za-z0-9_-]。
	// 与其等接口报一句"tool name 非法"，不如在注册时就说清是哪个名字、为什么不行。
	if sanitized := sanitizeToolName(name); sanitized != name {
		return fmt.Errorf("本地工具名称 %q 含非法字符，只能使用字母、数字、下划线与短横线", name)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.localTools == nil {
		m.localTools = make(map[string]tool.BaseTool)
	}
	if _, exists := m.localTools[name]; exists {
		return fmt.Errorf("本地工具 %q 重复注册", name)
	}
	m.localTools[name] = local
	return nil
}

// EinoLocalTools 已在 adapter.go 的 EinoTools 里合并（内置工具始终返回），
// 这里不再单开一个出口：多一个入口就多一处"到底该用哪个"的疑问。

// localToolList 返回按名称排序的本地工具快照。
//
// 排序是为了让 EinoTools 的输出稳定：agent 的工具列表每次运行都不一样的话，
// "同样的输入为什么这次没调工具"会变成一件需要复现才能查的事。
func (m *Manager) localToolList() []tool.BaseTool {
	m.mu.RLock()
	names := make([]string, 0, len(m.localTools))
	for name := range m.localTools {
		names = append(names, name)
	}
	m.mu.RUnlock()
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)

	m.mu.RLock()
	defer m.mu.RUnlock()
	tools := make([]tool.BaseTool, 0, len(names))
	for _, name := range names {
		// 快照期间可能被并发移除；拿不到就跳过，不 panic。
		if local, ok := m.localTools[name]; ok {
			tools = append(tools, local)
		}
	}
	return tools
}
