package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"narra/pkg/config"
)

type Status string

const (
	StatusUnknown    Status = "unknown"
	StatusConnecting Status = "connecting"
	StatusConnected  Status = "connected"
	StatusFailed     Status = "failed"
)

type managedClient interface {
	ListTools(context.Context) ([]ToolDescriptor, error)
	CallTool(context.Context, string, json.RawMessage) (*sdk.CallToolResult, error)
	Close() error
}

type clientFactory func(context.Context, config.MCPServerConfig, config.AppConfig) (managedClient, error)

type serverState struct {
	config     config.MCPServerConfig
	client     managedClient
	status     Status
	lastError  error
	retryAfter time.Time
	done       chan struct{}
}

type Manager struct {
	mu       sync.RWMutex
	app      config.AppConfig
	servers  map[string]*serverState
	registry *Registry
	connect  clientFactory
}

// NewManager 创建空的 MCP 管理器，server 配置通过 LoadFromDB 从数据库加载。
func NewManager(app config.AppConfig) *Manager {
	return &Manager{app: app, servers: make(map[string]*serverState), connect: defaultClientFactory}
}

func defaultClientFactory(ctx context.Context, server config.MCPServerConfig, app config.AppConfig) (managedClient, error) {
	return Connect(ctx, server, app)
}

// Start 依次连接所有已注册的 MCP server，required 的连接失败则整体失败。
func (m *Manager) Start(ctx context.Context) error {
	ids := m.serverIDs()
	connected := make([]string, 0, len(ids))
	for _, id := range ids {
		if err := m.ensureConnected(ctx, id); err != nil {
			m.mu.RLock()
			required := m.servers[id].config.Required
			m.mu.RUnlock()
			if required {
				m.closeServers(connected)
				return err
			}
			continue
		}
		connected = append(connected, id)
	}
	_, err := m.RefreshTools(ctx)
	if err != nil {
		m.closeServers(connected)
		return err
	}
	return nil
}

// RefreshTools 从所有已连接的 server 重新拉取工具列表并重建注册表。
func (m *Manager) RefreshTools(ctx context.Context) (*Registry, error) {
	ids := m.serverIDs()
	var descriptors []ToolDescriptor
	for _, id := range ids {
		client, required, err := m.clientFor(ctx, id)
		if err != nil {
			if required {
				return nil, err
			}
			continue
		}
		tools, err := client.ListTools(ctx)
		if err != nil {
			if required {
				return nil, err
			}
			continue
		}
		descriptors = append(descriptors, tools...)
	}
	registry, err := NewRegistry(descriptors)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.registry = registry
	m.mu.Unlock()
	return registry, nil
}

// ListTools 返回当前注册表中所有工具的快照副本。
func (m *Manager) ListTools() []ToolDescriptor {
	m.mu.RLock()
	registry := m.registry
	m.mu.RUnlock()
	return registry.List()
}

// CallTool 根据命名空间 ID 找到对应 server 并发起工具调用。
func (m *Manager) CallTool(ctx context.Context, id string, arguments json.RawMessage) (*sdk.CallToolResult, error) {
	m.mu.RLock()
	registry := m.registry
	m.mu.RUnlock()
	descriptor, ok := registry.Find(id)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownTool, id)
	}
	client, _, err := m.clientFor(ctx, descriptor.ServerID)
	if err != nil {
		return nil, err
	}
	return client.CallTool(ctx, descriptor.RemoteName, arguments)
}

// Status 查询指定 server 的连接状态。
func (m *Manager) Status(id string) (Status, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state, ok := m.servers[id]
	if !ok {
		return StatusUnknown, ErrUnknownServer
	}
	return state.status, state.lastError
}

// Close 关闭所有已连接的 server，聚合所有错误返回。
func (m *Manager) Close(ctx context.Context) error {
	_ = ctx
	ids := m.serverIDs()
	return m.closeServers(ids)
}

// clientFor 返回指定 server 的已连接客户端，必要时自动触发连接。
func (m *Manager) clientFor(ctx context.Context, id string) (managedClient, bool, error) {
	if err := m.ensureConnected(ctx, id); err != nil {
		m.mu.RLock()
		state, ok := m.servers[id]
		m.mu.RUnlock()
		if !ok {
			return nil, false, err
		}
		return nil, state.config.Required, err
	}
	m.mu.RLock()
	state := m.servers[id]
	client, required := state.client, state.config.Required
	m.mu.RUnlock()
	return client, required, nil
}

// ensureConnected 保证指定 server 处于已连接状态，支持重试和并发等待。
func (m *Manager) ensureConnected(ctx context.Context, id string) error {
	for {
		m.mu.Lock()
		state, ok := m.servers[id]
		if !ok {
			m.mu.Unlock()
			return fmt.Errorf("%w: %s", ErrUnknownServer, id)
		}
		switch state.status {
		case StatusConnected:
			m.mu.Unlock()
			return nil
		case StatusConnecting:
			done := state.done
			m.mu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-done:
				continue
			}
		case StatusFailed:
			if time.Now().Before(state.retryAfter) {
				err := state.lastError
				m.mu.Unlock()
				return err
			}
		}
		state.status = StatusConnecting
		state.done = make(chan struct{})
		serverConfig := state.config
		done := state.done
		m.mu.Unlock()

		client, err := m.connect(ctx, serverConfig, m.app)
		m.mu.Lock()
		if err != nil {
			state.status = StatusFailed
			state.lastError = err
			state.retryAfter = time.Now().Add(5 * time.Second)
		} else {
			state.client = client
			state.status = StatusConnected
			state.lastError = nil
		}
		close(done)
		m.mu.Unlock()
		return err
	}
}

// closeServers 按逆序关闭指定 server 的连接并清空注册表。
func (m *Manager) closeServers(ids []string) error {
	clients := make([]managedClient, 0, len(ids))
	m.mu.Lock()
	for _, id := range ids {
		if state, ok := m.servers[id]; ok && state.client != nil {
			clients = append(clients, state.client)
			state.client = nil
			state.status = StatusUnknown
		}
	}
	m.registry = nil
	m.mu.Unlock()
	var joined error
	for index := len(clients) - 1; index >= 0; index-- {
		joined = errors.Join(joined, clients[index].Close())
	}
	return joined
}

// serverIDs 返回所有已注册 server 的 ID 列表（已排序）。
func (m *Manager) serverIDs() []string {
	m.mu.RLock()
	ids := make([]string, 0, len(m.servers))
	for id := range m.servers {
		ids = append(ids, id)
	}
	m.mu.RUnlock()
	slicesSort(ids)
	return ids
}

// slicesSort 对字符串切片执行插入排序（升序）。
func slicesSort(values []string) {
	for index := 1; index < len(values); index++ {
		for position := index; position > 0 && values[position] < values[position-1]; position-- {
			values[position], values[position-1] = values[position-1], values[position]
		}
	}
}
