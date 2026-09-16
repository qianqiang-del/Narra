package repository

import (
	"context"

	"narra/internal/model/entity"
)

// EmbeddingSettingRepository 负责 Embedding 服务配置的持久化。
type EmbeddingSettingRepository interface {
	List(ctx context.Context) ([]entity.EmbeddingSetting, error)
	GetActive(ctx context.Context) (*entity.EmbeddingSetting, error)
	SaveActive(ctx context.Context, setting *entity.EmbeddingSetting) error
}
