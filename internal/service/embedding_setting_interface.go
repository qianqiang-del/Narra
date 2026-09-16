package service

import (
	"context"

	requestdto "narra/internal/model/dto/request"
	responsedto "narra/internal/model/dto/response"
)

// EmbeddingSettingService 管理当前生效的 Embedding 服务配置。
type EmbeddingSettingService interface {
	LoadActive(ctx context.Context) error
	Current(ctx context.Context) (responsedto.EmbeddingSetting, error)
	Save(ctx context.Context, input requestdto.EmbeddingSetting) (responsedto.EmbeddingSetting, error)
	Test(ctx context.Context, input requestdto.EmbeddingSetting) (int, error)
}
