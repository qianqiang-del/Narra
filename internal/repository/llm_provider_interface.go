package repository

import (
	"context"
	"narra/internal/model/entity"
)

type LLMProviderRepository interface {
	List(ctx context.Context) ([]entity.LLMProvider, error)
	ListByOwner(ctx context.Context, ownerID uint64) ([]entity.LLMProvider, error)
	ListAvailable(ctx context.Context) ([]entity.LLMProvider, error)
	ListAvailableByOwner(ctx context.Context, ownerID uint64) ([]entity.LLMProvider, error)
	FindByID(ctx context.Context, id uint64) (*entity.LLMProvider, error)
	FindByIDAndOwner(ctx context.Context, id, ownerID uint64) (*entity.LLMProvider, error)
	Create(ctx context.Context, provider *entity.LLMProvider) error
	Update(ctx context.Context, provider *entity.LLMProvider) error
	Delete(ctx context.Context, id uint64) error
	DeleteByIDAndOwner(ctx context.Context, id, ownerID uint64) error
}
