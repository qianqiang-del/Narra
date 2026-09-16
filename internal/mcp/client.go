package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"narra/pkg/config"
)

const maxToolResultBytes = 256 * 1024

type session interface {
	ListTools(context.Context, *sdk.ListToolsParams) (*sdk.ListToolsResult, error)
	CallTool(context.Context, *sdk.CallToolParams) (*sdk.CallToolResult, error)
	Close() error
}

type Client struct {
	config  config.MCPServerConfig
	session session
	mu      sync.Mutex
	closed  bool
}

// Connect 建立与远端 MCP server 的 streamable-http 连接。
func Connect(ctx context.Context, server config.MCPServerConfig, app config.AppConfig) (*Client, error) {
	token := server.APIKey
	if token == "" && server.AuthEnv != "" {
		token = os.Getenv(server.AuthEnv)
	}
	httpClient := &http.Client{Transport: authTransport{base: http.DefaultTransport, token: token}}
	transport := &sdk.StreamableClientTransport{
		Endpoint:             server.Endpoint,
		HTTPClient:           httpClient,
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
	}
	client := sdk.NewClient(&sdk.Implementation{Name: app.Name, Version: app.Version}, nil)
	connectCtx, cancel := withTimeout(ctx, server.StartupTimeout)
	defer cancel()
	connected, err := client.Connect(connectCtx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("连接 MCP server %q: %w", server.ID, err)
	}
	return &Client{config: server, session: connected}, nil
}

// ListTools 从远端 MCP server 拉取工具列表并转换为内部描述格式。
func (c *Client) ListTools(ctx context.Context) ([]ToolDescriptor, error) {
	callCtx, cancel := withTimeout(ctx, c.config.DiscoveryTimeout)
	defer cancel()
	result, err := c.session.ListTools(callCtx, nil)
	if err != nil {
		return nil, fmt.Errorf("发现 MCP server %q 工具: %w", c.config.ID, err)
	}
	tools := make([]ToolDescriptor, 0, len(result.Tools))
	for _, remote := range result.Tools {
		schema, err := json.Marshal(remote.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("编码 MCP tool %q schema: %w", remote.Name, err)
		}
		tools = append(tools, ToolDescriptor{ServerID: c.config.ID, RemoteName: remote.Name, Description: remote.Description, InputSchema: schema})
	}
	return tools, nil
}

// CallTool 向远端 MCP server 发起一次工具调用。
func (c *Client) CallTool(ctx context.Context, name string, arguments json.RawMessage) (*sdk.CallToolResult, error) {
	callCtx, cancel := withTimeout(ctx, c.config.CallTimeout)
	defer cancel()
	result, err := c.session.CallTool(callCtx, &sdk.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		return nil, fmt.Errorf("调用 MCP tool %q: %w", name, err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("编码 MCP tool %q 结果: %w", name, err)
	}
	if len(encoded) > maxToolResultBytes {
		return nil, fmt.Errorf("%w: %d bytes", ErrResultTooLarge, len(encoded))
	}
	if result.IsError {
		return result, ErrToolFailed
	}
	return result, nil
}

// Close 关闭与远端 MCP server 的连接，重复调用安全。
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	return c.session.Close()
}

type authTransport struct {
	base  http.RoundTripper
	token string
}

// RoundTrip 在请求头注入 Bearer token 后交给底层 transport 处理。
func (t authTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	if t.token != "" {
		clone.Header.Set("Authorization", "Bearer "+t.token)
	}
	return t.base.RoundTrip(clone)
}

// withTimeout 创建带超时的子 context，若父 context 已有更短的 deadline 则直接继承。
func withTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= timeout {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}
