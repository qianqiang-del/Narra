package repository

import (
	"context"
	"narra/internal/model/entity"
)

type LLMProviderRepository interface {
	List(ctx context.Context) ([]entity.LLMProvider, error)
	ListAvailable(ctx context.Context) ([]entity.LLMProvider, error)
	FindByID(ctx context.Context, id uint64) (*entity.LLMProvider, error)
	Create(ctx context.Context, provider *entity.LLMProvider) error
	Update(ctx context.Context, provider *entity.LLMProvider) error
	Delete(ctx context.Context, id uint64) error
}
