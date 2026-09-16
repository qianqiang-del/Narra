package repository

import (
	"context"

	"narra/internal/model/entity"
)

// EmbeddingSettingRepository 负责 Embedding 服务配置的持久化。
// 表里可以留多条历史配置，但同一时间只有一条 IsActive 为 true。
// 因此写入口只开了一个 SaveActive，"切换生效配置"这件事由仓储自己保证原子性，
// 调用方不需要先查后改。
type EmbeddingSettingRepository interface {
	// List 返回全部配置，生效中的排在最前。
	List(ctx context.Context) ([]entity.EmbeddingSetting, error)

	// GetActive 返回当前生效的配置；没有任何生效记录时，
	// 原样返回 gorm.ErrRecordNotFound，由上层决定是回退默认配置还是报错。
	GetActive(ctx context.Context) (*entity.EmbeddingSetting, error)

	// SaveActive 把 setting 落库并置为唯一生效项，旧配置在同一次事务里取消生效。
	SaveActive(ctx context.Context, setting *entity.EmbeddingSetting) error
}
