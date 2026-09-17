package repository

import (
	"context"
	"narra/internal/model/entity"

	"gorm.io/gorm"
)

type llmProviderRepository struct{ db *gorm.DB }

func NewLLMProviderRepository(db *gorm.DB) LLMProviderRepository {
	return &llmProviderRepository{db: db}
}

func (r *llmProviderRepository) List(ctx context.Context) ([]entity.LLMProvider, error) {
	var providers []entity.LLMProvider
	err := r.db.WithContext(ctx).Order("id ASC").Find(&providers).Error
	return providers, err
}

func (r *llmProviderRepository) ListAvailable(ctx context.Context) ([]entity.LLMProvider, error) {
	var providers []entity.LLMProvider
	err := r.db.WithContext(ctx).
		Where("is_enabled = ? AND test_status = ?", true, entity.LLMTestStatusSuccess).
		Order("id ASC").
		Find(&providers).Error
	return providers, err
}

func (r *llmProviderRepository) FindByID(ctx context.Context, id uint64) (*entity.LLMProvider, error) {
	var provider entity.LLMProvider
	if err := r.db.WithContext(ctx).First(&provider, id).Error; err != nil {
		return nil, err
	}
	return &provider, nil
}

func (r *llmProviderRepository) Create(ctx context.Context, provider *entity.LLMProvider) error {
	return r.db.WithContext(ctx).Create(provider).Error
}

func (r *llmProviderRepository) Update(ctx context.Context, provider *entity.LLMProvider) error {
	return r.db.WithContext(ctx).Save(provider).Error
}

func (r *llmProviderRepository) Delete(ctx context.Context, id uint64) error {
	return r.db.WithContext(ctx).Delete(&entity.LLMProvider{}, id).Error
}
