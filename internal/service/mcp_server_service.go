package service

import (
	"context"
	"fmt"
	"time"

	"narra/internal/mcp"
	"narra/internal/model/dto/request"
	dto "narra/internal/model/dto/response"
	"narra/internal/model/entity"
	"narra/internal/repository"
	"narra/pkg/config"
	"narra/pkg/crypto"
	"narra/pkg/errors"
)

// mcpServerService MCP 服务配置业务实现。
type mcpServerService struct {
	repo          repository.MCPServerRepository
	app           config.AppConfig
	encryptionKey []byte
	runtime       mcpRuntime
}

type mcpRuntime interface {
	Load([]config.MCPServerConfig) error
	Start(context.Context) error
	Upsert(context.Context, config.MCPServerConfig) error
	Remove(context.Context, string) error
}

// NewMCPServerService 创建 MCP 服务配置业务服务。
func NewMCPServerService(repo repository.MCPServerRepository, app config.AppConfig, encryptionKey []byte, runtime mcpRuntime) MCPServerService {
	return &mcpServerService{repo: repo, app: app, encryptionKey: encryptionKey, runtime: runtime}
}

// LoadRuntime loads enabled persisted servers and starts their runtime connections.
func (s *mcpServerService) LoadRuntime(ctx context.Context) error {
	servers, err := s.repo.List(ctx)
	if err != nil {
		return errors.NewWithErr(errors.CodeInternalError, "查询 MCP 服务列表失败", err)
	}
	configs := make([]config.MCPServerConfig, 0, len(servers))
	for index := range servers {
		if !servers[index].Enabled {
			continue
		}
		serverConfig, err := s.toServerConfig(&servers[index])
		if err != nil {
			return errors.NewWithErr(errors.CodeInternalError, "解密 MCP API Key 失败", err)
		}
		configs = append(configs, serverConfig)
	}
	if err := s.runtime.Load(configs); err != nil {
		return errors.NewWithErr(errors.CodeInternalError, "加载 MCP 运行时配置失败", err)
	}
	if err := s.runtime.Start(ctx); err != nil {
		return errors.NewWithErr(errors.CodeInternalError, "启动 MCP 运行时失败", err)
	}
	return nil
}

// List 查所有 MCP 服务配置，转成对外结构。
func (s *mcpServerService) List(ctx context.Context) ([]dto.MCPServerItem, error) {
	servers, err := s.repo.List(ctx)
	if err != nil {
		return nil, errors.NewWithErr(errors.CodeInternalError, "查询 MCP 服务列表失败", err)
	}

	items := make([]dto.MCPServerItem, 0, len(servers))
	for i := range servers {
		items = append(items, s.toItem(&servers[i]))
	}

	return items, nil
}

// Create 新增一条 MCP 服务配置。
func (s *mcpServerService) Create(ctx context.Context, input request.MCPServer) (*dto.MCPServerItem, error) {
	timeout, err := s.parseTimeouts(input)
	if err != nil {
		return nil, errors.NewWithErr(errors.CodeInvalidParam, err.Error(), nil)
	}
	srv := &entity.MCPServer{
		ServerID:         input.ServerID,
		Name:             input.Name,
		Enabled:          input.Enabled,
		Required:         input.Required,
		Transport:        input.Transport,
		Endpoint:         input.Endpoint,
		AuthEnv:          input.AuthEnv,
		StartupTimeout:   timeout[0],
		DiscoveryTimeout: timeout[1],
		CallTimeout:      timeout[2],
		EnabledTools:     input.EnabledTools,
		SortOrder:        input.SortOrder,
	}
	if input.APIKey != "" {
		encrypted, err := crypto.Encrypt(input.APIKey, s.encryptionKey)
		if err != nil {
			return nil, errors.NewWithErr(errors.CodeInternalError, "加密 API Key 失败", err)
		}
		srv.APIKey = encrypted
	}
	if err := s.repo.Create(ctx, srv); err != nil {
		return nil, errors.NewWithErr(errors.CodeInternalError, "创建 MCP 服务失败", err)
	}
	if err := s.syncRuntime(ctx, srv); err != nil {
		return nil, err
	}
	item := s.toItem(srv)
	return &item, nil
}

// Update 部分更新 MCP 服务配置。只有非 nil 的字段会被更新。
func (s *mcpServerService) Update(ctx context.Context, id uint64, input request.MCPServerUpdate) (*dto.MCPServerItem, error) {
	srv, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, errors.NewWithErr(errors.CodeResourceNotFound, "MCP 服务不存在", err)
	}
	if input.Enabled != nil {
		srv.Enabled = *input.Enabled
	}
	if input.EnabledTools != nil {
		srv.EnabledTools = *input.EnabledTools
	}
	if err := s.repo.Update(ctx, srv); err != nil {
		return nil, errors.NewWithErr(errors.CodeInternalError, "更新 MCP 服务失败", err)
	}
	if err := s.syncRuntime(ctx, srv); err != nil {
		return nil, err
	}
	item := s.toItem(srv)
	return &item, nil
}

// Delete 删除一条 MCP 服务配置。
func (s *mcpServerService) Delete(ctx context.Context, id uint64) error {
	srv, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return errors.NewWithErr(errors.CodeResourceNotFound, "MCP 服务不存在", err)
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return errors.NewWithErr(errors.CodeInternalError, "删除 MCP 服务失败", err)
	}
	if err := s.runtime.Remove(ctx, srv.ServerID); err != nil {
		return errors.NewWithErr(errors.CodeInternalError, "移除 MCP 运行时配置失败", err)
	}
	return nil
}

func (s *mcpServerService) syncRuntime(ctx context.Context, srv *entity.MCPServer) error {
	serverConfig, err := s.toServerConfig(srv)
	if err != nil {
		return errors.NewWithErr(errors.CodeInternalError, "解密 MCP API Key 失败", err)
	}
	if err := s.runtime.Upsert(ctx, serverConfig); err != nil {
		return errors.NewWithErr(errors.CodeInternalError, "同步 MCP 运行时配置失败", err)
	}
	return nil
}

// Test 测试与 MCP server 的连接。
func (s *mcpServerService) Test(ctx context.Context, id uint64) (*dto.MCPServerTestResult, error) {
	srv, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, errors.NewWithErr(errors.CodeResourceNotFound, "MCP 服务不存在", err)
	}
	cfg, err := s.toServerConfig(srv)
	if err != nil {
		return nil, errors.NewWithErr(errors.CodeInternalError, "解密 API Key 失败", err)
	}
	client, err := mcp.Connect(ctx, cfg, s.app)
	if err != nil {
		return &dto.MCPServerTestResult{Success: false, Message: fmt.Sprintf("连接失败: %v", err)}, nil
	}
	defer client.Close()
	tools, err := client.ListTools(ctx)
	if err != nil {
		return &dto.MCPServerTestResult{Success: false, Message: fmt.Sprintf("发现工具失败: %v", err)}, nil
	}
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.RemoteName)
	}
	return &dto.MCPServerTestResult{Success: true, Message: fmt.Sprintf("连接成功，发现 %d 个工具", len(tools)), Tools: names}, nil
}

func (s *mcpServerService) toServerConfig(srv *entity.MCPServer) (config.MCPServerConfig, error) {
	apiKey := srv.APIKey
	if apiKey != "" {
		decrypted, err := crypto.Decrypt(apiKey, s.encryptionKey)
		if err != nil {
			return config.MCPServerConfig{}, fmt.Errorf("解密 API Key 失败: %w", err)
		}
		apiKey = decrypted
	}
	return config.MCPServerConfig{
		ID:               srv.ServerID,
		Enabled:          srv.Enabled,
		Required:         srv.Required,
		Transport:        srv.Transport,
		Endpoint:         srv.Endpoint,
		APIKey:           apiKey,
		AuthEnv:          srv.AuthEnv,
		StartupTimeout:   srv.StartupTimeout,
		DiscoveryTimeout: srv.DiscoveryTimeout,
		CallTimeout:      srv.CallTimeout,
		EnabledTools:     srv.EnabledTools,
	}, nil
}

func (s *mcpServerService) parseTimeouts(input request.MCPServer) ([3]time.Duration, error) {
	startup, err := time.ParseDuration(input.StartupTimeout)
	if err != nil {
		return [3]time.Duration{}, fmt.Errorf("startup_timeout 格式错误: %w", err)
	}
	discovery, err := time.ParseDuration(input.DiscoveryTimeout)
	if err != nil {
		return [3]time.Duration{}, fmt.Errorf("discovery_timeout 格式错误: %w", err)
	}
	call, err := time.ParseDuration(input.CallTimeout)
	if err != nil {
		return [3]time.Duration{}, fmt.Errorf("call_timeout 格式错误: %w", err)
	}
	return [3]time.Duration{startup, discovery, call}, nil
}

func (s *mcpServerService) toItem(srv *entity.MCPServer) dto.MCPServerItem {
	return dto.MCPServerItem{
		ID:               srv.ID,
		ServerID:         srv.ServerID,
		Name:             srv.Name,
		Enabled:          srv.Enabled,
		Required:         srv.Required,
		Transport:        srv.Transport,
		Endpoint:         srv.Endpoint,
		HasAPIKey:        srv.APIKey != "",
		StartupTimeout:   srv.StartupTimeout.String(),
		DiscoveryTimeout: srv.DiscoveryTimeout.String(),
		CallTimeout:      srv.CallTimeout.String(),
		EnabledTools:     srv.EnabledTools,
		SortOrder:        srv.SortOrder,
		CreatedAt:        srv.CreatedAt.Format(time.RFC3339),
		UpdatedAt:        srv.UpdatedAt.Format(time.RFC3339),
	}
}
