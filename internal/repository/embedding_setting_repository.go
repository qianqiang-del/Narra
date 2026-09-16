package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"narra/internal/model/entity"
)

// embeddingSettingRepository 基于 GORM 的 Embedding 配置仓储。
type embeddingSettingRepository struct {
	db *gorm.DB
}

// NewEmbeddingSettingRepository 创建 Embedding 配置仓储。
func NewEmbeddingSettingRepository(db *gorm.DB) EmbeddingSettingRepository {
	return &embeddingSettingRepository{db: db}
}

// List 查全部配置，生效中的排在最前，其余按 id 升序。
// 排序放在 SQL 里而不是让上层再排一次：顺序就是设置页的展示顺序，
// 由数据库保证比在业务层补一遍更不容易漏。
func (r *embeddingSettingRepository) List(ctx context.Context) ([]entity.EmbeddingSetting, error) {
	var settings []entity.EmbeddingSetting
	if err := r.db.WithContext(ctx).Order("is_active DESC, id ASC").Find(&settings).Error; err != nil {
		return nil, err
	}
	return settings, nil
}

// GetActive 查当前生效的配置。
// 查不到时不吞错，把 gorm.ErrRecordNotFound 透传给上层：
// "还没保存过配置"和"查库失败"对业务是两回事，前者可以回退默认配置，后者只能报错，
// 在这里替上层做决定会把这两种情况混成一种。
func (r *embeddingSettingRepository) GetActive(ctx context.Context) (*entity.EmbeddingSetting, error) {
	var setting entity.EmbeddingSetting
	if err := r.db.WithContext(ctx).Where("is_active = ?", true).First(&setting).Error; err != nil {
		return nil, err
	}
	return &setting, nil
}

// SaveActive 把 setting 置为唯一生效配置。
// "生效"是个全局唯一状态，而这里要先让旧记录失效、再让新记录生效，是两次写，
// 所以三步都放进一个事务：中间任何一步失败都整体回滚，
// 不会留下两条 is_active = true、或者一条都不生效的中间态。
func (r *embeddingSettingRepository) SaveActive(ctx context.Context, setting *entity.EmbeddingSetting) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// SELECT ... FOR UPDATE 先锁住当前生效行。两个请求同时保存时，
		// 后到的事务会阻塞在这条查询上，等前一个提交后才读到最新的 ID，
		// 而不是两边都拿着旧 ID 去覆盖。
		var current entity.EmbeddingSetting
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("is_active = ?", true).First(&current).Error
		// 没有生效记录属于正常首次保存，不算错误，下面按插入处理。
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		// 先把所有生效行置为失效，保证更新完最多只有一条 true。
		if err := tx.Model(&entity.EmbeddingSetting{}).Where("is_active = ?", true).Update("is_active", false).Error; err != nil {
			return err
		}
		setting.IsActive = true
		// 之前没生效记录：setting 是全新的，直接插入。
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Create(setting).Error
		}

		// 之前有生效记录：复用它的 ID 整行覆盖，而不是插新行。
		// Name 上有唯一约束，每次保存都新增会因为重名而写不进去。
		setting.ID = current.ID
		return tx.Save(setting).Error
	})
}
