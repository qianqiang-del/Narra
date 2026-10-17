package repository

import (
	"context"

	"narra/internal/model/entity"
)

// EmbeddingSettingRepository 负责 Embedding 服务配置的持久化。
//
// 配置按账号（owner_id）分区：每个账号可以留多条历史配置，但同一时间只有一条
// IsActive 为 true（部分唯一索引 WHERE is_active 按 owner 生效）。因此写入口只开了
// SaveActiveForOwner，"切换生效配置"这件事由仓储自己保证原子性，调用方不需要先查后改。
//
// 没有不带 owner 的读写入口：平台默认配置来自 config.yaml，不落这张表 ——
// 这张表只存"用户自己配的那份"，混进全局语义会让"谁的配置生效"无法回答。
type EmbeddingSettingRepository interface {
	// ListByOwner 返回该账号的全部配置，生效中的排在最前。
	ListByOwner(ctx context.Context, ownerID uint64) ([]entity.EmbeddingSetting, error)

	// GetActiveByOwner 返回该账号当前生效的配置；该账号没有任何生效记录时，
	// 原样返回 gorm.ErrRecordNotFound，由上层决定是回退平台默认配置还是报错。
	GetActiveByOwner(ctx context.Context, ownerID uint64) (*entity.EmbeddingSetting, error)

	// SaveActiveForOwner 把 setting 落库并置为该账号唯一生效项，旧配置在同一次事务里取消生效。
	SaveActiveForOwner(ctx context.Context, ownerID uint64, setting *entity.EmbeddingSetting) error
}
