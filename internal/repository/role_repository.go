package repository

import (
	"context"

	"gorm.io/gorm"

	"narra/internal/model/entity"
)

// roleRepository 基于 GORM 的角色池仓储。
type roleRepository struct {
	db *gorm.DB
}

// NewRoleRepository 创建角色池仓储。
func NewRoleRepository(db *gorm.DB) RoleRepository {
	return &roleRepository{db: db}
}

// ListEnabled 查未下架的角色，按 sort_order 升序。
//
// WithContext 让它跟着请求走：客户端断开时这次查询会被取消，不会白占着连接。
func (r *roleRepository) ListEnabled(ctx context.Context) ([]entity.PresetAgent, error) {
	var agents []entity.PresetAgent

	err := r.db.WithContext(ctx).
		Where("enabled = ?", true).
		Order("sort_order ASC").
		Find(&agents).Error
	if err != nil {
		return nil, err
	}

	return agents, nil
}

// ListByIDs 按 ID 批量读取角色，按 sort_order 升序；不过滤是否上架。
func (r *roleRepository) ListByIDs(ctx context.Context, ids []uint64) ([]entity.PresetAgent, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	var agents []entity.PresetAgent
	err := r.db.WithContext(ctx).
		Where("id IN ?", ids).
		Order("sort_order ASC").
		Find(&agents).Error
	if err != nil {
		return nil, err
	}

	return agents, nil
}
