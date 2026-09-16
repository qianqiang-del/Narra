package repository

import (
	"context"

	"gorm.io/gorm"

	"narra/internal/model/entity"
)

// mcpServerRepository 基于 GORM 的 MCP 服务配置仓储。
type mcpServerRepository struct {
	db *gorm.DB
}

// NewMCPServerRepository 创建 MCP 服务配置仓储。
func NewMCPServerRepository(db *gorm.DB) MCPServerRepository {
	return &mcpServerRepository{db: db}
}

// List 查所有 MCP 服务，按 sort_order 升序。
func (r *mcpServerRepository) List(ctx context.Context) ([]entity.MCPServer, error) {
	var servers []entity.MCPServer
	err := r.db.WithContext(ctx).
		Order("sort_order ASC, id ASC").
		Find(&servers).Error
	if err != nil {
		return nil, err
	}
	return servers, nil
}

// FindByID 按主键查找。
func (r *mcpServerRepository) FindByID(ctx context.Context, id uint64) (*entity.MCPServer, error) {
	var server entity.MCPServer
	if err := r.db.WithContext(ctx).First(&server, id).Error; err != nil {
		return nil, err
	}
	return &server, nil
}

// Create 新增一条记录。
func (r *mcpServerRepository) Create(ctx context.Context, server *entity.MCPServer) error {
	return r.db.WithContext(ctx).Create(server).Error
}

// Update 更新一条记录。
func (r *mcpServerRepository) Update(ctx context.Context, server *entity.MCPServer) error {
	return r.db.WithContext(ctx).Save(server).Error
}

// Delete 按主键删除。
func (r *mcpServerRepository) Delete(ctx context.Context, id uint64) error {
	return r.db.WithContext(ctx).Delete(&entity.MCPServer{}, id).Error
}
