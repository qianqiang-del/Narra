package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"narra/pkg/config"
)

// local_test.go 盯着两件事：
//
//  1. 内置工具**活着穿过注册表刷新** —— Registry 每次 RefreshTools 都整体重建
//     （见 Manager.RefreshTools），内置工具要是存在那里，改一次 MCP 配置就会被无声抹掉，
//     而"工具突然没了"只会在模型下一次生成时表现成"它不再查知识库了"，几乎不可能定位。
//  2. 联网开关**只拦远端工具** —— 关了搜索不等于不查自己的资料。

// stubLocalTool 是最小的 Eino 工具替身。
type stubLocalTool struct {
	name    string
	infoErr error
}

var _ tool.InvokableTool = (*stubLocalTool)(nil)

func (s *stubLocalTool) Info(context.Context) (*schema.ToolInfo, error) {
	if s.infoErr != nil {
		return nil, s.infoErr
	}
	return &schema.ToolInfo{Name: s.name}, nil
}

func (s *stubLocalTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	return "ok", nil
}

// fakeManagedClient 是远端 MCP 连接的替身，只提供一份工具列表。
type fakeManagedClient struct {
	tools []ToolDescriptor
}

var _ managedClient = (*fakeManagedClient)(nil)

func (f *fakeManagedClient) ListTools(context.Context) ([]ToolDescriptor, error) {
	return f.tools, nil
}

func (f *fakeManagedClient) CallTool(context.Context, string, json.RawMessage) (*sdk.CallToolResult, error) {
	return nil, nil
}

func (f *fakeManagedClient) Close() error { return nil }

// newManagerWithRemote 造一个"配了一个远端 server"的管理器，并记下它连了几次。
func newManagerWithRemote(t *testing.T, connector func() managedClient) (*Manager, *int) {
	t.Helper()

	manager := NewManager(config.AppConfig{})
	connects := 0
	manager.connect = func(context.Context, config.MCPServerConfig, config.AppConfig) (managedClient, error) {
		connects++
		return connector(), nil
	}
	if err := manager.Load([]config.MCPServerConfig{{ID: "search", Enabled: true}}); err != nil {
		t.Fatalf("加载 MCP server 配置失败: %v", err)
	}
	return manager, &connects
}

// einoToolNames 取 EinoTools 暴露出来的工具名。
func einoToolNames(t *testing.T, manager *Manager, webSearch bool) []string {
	t.Helper()

	tools, err := manager.EinoTools(context.Background(), webSearch)
	if err != nil {
		t.Fatalf("获取 Eino 工具失败: %v", err)
	}
	names := make([]string, 0, len(tools))
	for _, item := range tools {
		info, err := item.Info(context.Background())
		if err != nil {
			t.Fatalf("读取工具信息失败: %v", err)
		}
		names = append(names, info.Name)
	}
	return names
}

// TestEinoToolsKeepsLocalToolsAcrossRefresh 校验内置工具出现在工具集里，
// 且刷新注册表（重建远端工具集）之后仍然在。
func TestEinoToolsKeepsLocalToolsAcrossRefresh(t *testing.T) {
	manager, _ := newManagerWithRemote(t, func() managedClient {
		return &fakeManagedClient{tools: []ToolDescriptor{{
			ServerID:    "search",
			RemoteName:  "web_search",
			Description: "联网搜索",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}}}
	})
	if err := manager.RegisterLocalTool(&stubLocalTool{name: "rag_retrieve"}); err != nil {
		t.Fatalf("注册内置工具失败: %v", err)
	}

	if names := einoToolNames(t, manager, true); !containsString(names, "rag_retrieve") {
		t.Fatalf("内置工具应当出现在工具集里: %v", names)
	}

	if _, err := manager.RefreshTools(context.Background()); err != nil {
		t.Fatalf("刷新工具注册表失败: %v", err)
	}
	if names := einoToolNames(t, manager, true); !containsString(names, "rag_retrieve") {
		t.Fatalf("刷新注册表之后内置工具还在: %v", names)
	}
}

// TestEinoToolsGatesOnlyRemoteTools 校验联网开关只拦远端工具。
//
// 关掉时只有内置工具，而且连远端工具列表都不去拉（拉列表要为每个 server 建连接）；
// 打开时内置 + 远端都在。
func TestEinoToolsGatesOnlyRemoteTools(t *testing.T) {
	manager, connects := newManagerWithRemote(t, func() managedClient {
		return &fakeManagedClient{tools: []ToolDescriptor{{
			ServerID:    "search",
			RemoteName:  "web_search",
			Description: "联网搜索",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}}}
	})
	if err := manager.RegisterLocalTool(&stubLocalTool{name: "rag_retrieve"}); err != nil {
		t.Fatalf("注册内置工具失败: %v", err)
	}

	// 还没连过任何 server：关掉联网时只该看到内置工具，也不该因此触发连接。
	names := einoToolNames(t, manager, false)
	if len(names) != 1 || names[0] != "rag_retrieve" {
		t.Fatalf("关掉联网时应当只有内置工具: %v", names)
	}
	if *connects != 0 {
		t.Fatalf("关掉联网时不该去连远端 server，实际连了 %d 次", *connects)
	}

	// 打开联网：远端工具也进来（连接由启动时的 LoadRuntime 建立，这里手动补一次）。
	if _, err := manager.RefreshTools(context.Background()); err != nil {
		t.Fatalf("刷新工具注册表失败: %v", err)
	}
	names = einoToolNames(t, manager, true)
	remoteName := toolID("search", "web_search")
	if len(names) != 2 || names[0] != "rag_retrieve" || names[1] != remoteName {
		t.Fatalf("打开联网时应当是内置 + 远端的全集且内置在前: %v", names)
	}
}

// TestRegisterLocalToolRejectsInvalid 校验装配期的三类错误都被挡住：
// 空工具、无名工具、重名工具 —— 它们都会让 agent 侧的工具集变得不可预测。
func TestRegisterLocalToolRejectsInvalid(t *testing.T) {
	manager := NewManager(config.AppConfig{})

	if err := manager.RegisterLocalTool(nil); err == nil {
		t.Fatal("空工具应当被拒绝")
	}
	if err := manager.RegisterLocalTool(&stubLocalTool{name: "   "}); err == nil {
		t.Fatal("没有名字的工具应当被拒绝")
	}
	if err := manager.RegisterLocalTool(&stubLocalTool{name: "broken", infoErr: errors.New("坏掉的工具")}); err == nil {
		t.Fatal("读不出元信息的工具应当被拒绝")
	}
	// 工具名会原样进大模型的 tools 声明，那边的字符集只有 [A-Za-z0-9_-]。
	if err := manager.RegisterLocalTool(&stubLocalTool{name: "rag retrieve"}); err == nil {
		t.Fatal("含非法字符的工具名应当被拒绝")
	}

	if err := manager.RegisterLocalTool(&stubLocalTool{name: "rag_retrieve"}); err != nil {
		t.Fatalf("首次注册失败: %v", err)
	}
	if err := manager.RegisterLocalTool(&stubLocalTool{name: "rag_retrieve"}); err == nil {
		t.Fatal("重名注册应当被拒绝：两个同名工具会让模型拿到哪一个取决于注册顺序")
	}
}
