package repository

import (
	"context"

	"narra/internal/model/entity"
)

// VLMSettingRepository 负责视觉模型配置的持久化。
//
// 与 RerankSettingRepository 同形：表里可以留多条历史配置，同一时间只有一条
// IsEnabled 为 true（部分唯一索引）。"切换启用"由 SetEnabled 在事务里保证原子性，
// 调用方不需要先查后改。
type VLMSettingRepository interface {
	// List 返回全部配置，启用中的排在最前，其余按 id 升序。
	// 顺序就是设置页的展示顺序，放在 SQL 里由数据库保证。
	List(ctx context.Context) ([]entity.VLMSetting, error)

	// GetEnabled 返回当前启用的配置；没有启用项时原样透传 gorm.ErrRecordNotFound，
	// 由上层决定是"视觉理解关闭"还是"查库失败"。
	GetEnabled(ctx context.Context) (*entity.VLMSetting, error)

	// FindByID 按主键取配置，查不到返回 gorm.ErrRecordNotFound。
	FindByID(ctx context.Context, id uint64) (*entity.VLMSetting, error)

	// Create 写入一条新配置（默认不启用）。
	Create(ctx context.Context, setting *entity.VLMSetting) error

	// Update 整行更新已有配置。
	Update(ctx context.Context, setting *entity.VLMSetting) error

	// Delete 删除一条配置；删的正好是启用中的那条时，视觉理解随之为"无生效配置"。
	Delete(ctx context.Context, id uint64) error

	// SetEnabled 切换启用状态：启用时在同一事务里把其它配置置为失效，保证最多一条生效。
	// 目标不存在时返回 applied = false，且**不改动任何现有启用状态**（事务整体回滚）。
	SetEnabled(ctx context.Context, id uint64, enabled bool) (applied bool, err error)
}
