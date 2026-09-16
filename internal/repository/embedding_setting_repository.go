package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"narra/internal/model/entity"
)

type embeddingSettingRepository struct {
	db *gorm.DB
}

func NewEmbeddingSettingRepository(db *gorm.DB) EmbeddingSettingRepository {
	return &embeddingSettingRepository{db: db}
}

func (r *embeddingSettingRepository) List(ctx context.Context) ([]entity.EmbeddingSetting, error) {
	var settings []entity.EmbeddingSetting
	if err := r.db.WithContext(ctx).Order("is_active DESC, id ASC").Find(&settings).Error; err != nil {
		return nil, err
	}
	return settings, nil
}

func (r *embeddingSettingRepository) GetActive(ctx context.Context) (*entity.EmbeddingSetting, error) {
	var setting entity.EmbeddingSetting
	if err := r.db.WithContext(ctx).Where("is_active = ?", true).First(&setting).Error; err != nil {
		return nil, err
	}
	return &setting, nil
}

func (r *embeddingSettingRepository) SaveActive(ctx context.Context, setting *entity.EmbeddingSetting) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current entity.EmbeddingSetting
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("is_active = ?", true).First(&current).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		if err := tx.Model(&entity.EmbeddingSetting{}).Where("is_active = ?", true).Update("is_active", false).Error; err != nil {
			return err
		}
		setting.IsActive = true
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Create(setting).Error
		}

		setting.ID = current.ID
		return tx.Save(setting).Error
	})
}
