package repository

import (
	"context"

	"narra/internal/model/entity"
)

// MCPServerRepository MCP 服务配置的持久化。
type MCPServerRepository interface {
	// List 返回所有 MCP 服务，按 sort_order 升序。
	List(ctx context.Context) ([]entity.MCPServer, error)
	// FindByID 按主键查找。
	FindByID(ctx context.Context, id uint64) (*entity.MCPServer, error)
	// Create 新增一条记录。
	Create(ctx context.Context, server *entity.MCPServer) error
	// Update 更新一条记录。
	Update(ctx context.Context, server *entity.MCPServer) error
	// Delete 按主键删除。
	Delete(ctx context.Context, id uint64) error
}
