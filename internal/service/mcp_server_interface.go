package service

import (
	"context"

	"narra/internal/model/dto/request"
	dto "narra/internal/model/dto/response"
)

// MCPServerService MCP 服务配置业务。
type MCPServerService interface {
	// List 返回所有 MCP 服务配置。
	List(ctx context.Context) ([]dto.MCPServerItem, error)
	// Create 新增一条 MCP 服务配置。
	Create(ctx context.Context, input request.MCPServer) (*dto.MCPServerItem, error)
	// Update 部分更新 MCP 服务配置。
	Update(ctx context.Context, id uint64, input request.MCPServerUpdate) (*dto.MCPServerItem, error)
	// Delete 删除一条 MCP 服务配置。
	Delete(ctx context.Context, id uint64) error
	// Test 测试与 MCP server 的连接，返回工具列表。
	Test(ctx context.Context, id uint64) (*dto.MCPServerTestResult, error)
}
