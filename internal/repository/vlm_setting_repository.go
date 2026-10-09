package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"narra/internal/model/entity"
)

// vlmSettingRepository 基于 GORM 的视觉模型配置仓储。
type vlmSettingRepository struct {
	db *gorm.DB
}

// NewVLMSettingRepository 创建视觉模型配置仓储。
func NewVLMSettingRepository(db *gorm.DB) VLMSettingRepository {
	return &vlmSettingRepository{db: db}
}

// List 查全部配置，启用中的排在最前，其余按 id 升序。
func (r *vlmSettingRepository) List(ctx context.Context) ([]entity.VLMSetting, error) {
	var settings []entity.VLMSetting
	if err := r.db.WithContext(ctx).Order("is_enabled DESC, id ASC").Find(&settings).Error; err != nil {
		return nil, err
	}
	return settings, nil
}

// GetEnabled 查当前启用的配置。查不到时不吞错，把 gorm.ErrRecordNotFound 透传给上层：
// "还没启用过任何配置"（视觉理解关闭）和"查库失败"是两回事。
func (r *vlmSettingRepository) GetEnabled(ctx context.Context) (*entity.VLMSetting, error) {
	var setting entity.VLMSetting
	if err := r.db.WithContext(ctx).Where("is_enabled = ?", true).First(&setting).Error; err != nil {
		return nil, err
	}
	return &setting, nil
}

// FindByID 按主键取配置。
func (r *vlmSettingRepository) FindByID(ctx context.Context, id uint64) (*entity.VLMSetting, error) {
	var setting entity.VLMSetting
	if err := r.db.WithContext(ctx).First(&setting, id).Error; err != nil {
		return nil, err
	}
	return &setting, nil
}

// Create 写入一条新配置。
func (r *vlmSettingRepository) Create(ctx context.Context, setting *entity.VLMSetting) error {
	return r.db.WithContext(ctx).Create(setting).Error
}

// Update 整行更新已有配置。
func (r *vlmSettingRepository) Update(ctx context.Context, setting *entity.VLMSetting) error {
	return r.db.WithContext(ctx).Save(setting).Error
}

// Delete 删除一条配置。删除启用中的配置不需要额外处理：
// 部分唯一索引只约束"最多一条 true"，没有启用项是合法状态（视觉理解关闭）。
func (r *vlmSettingRepository) Delete(ctx context.Context, id uint64) error {
	return r.db.WithContext(ctx).Delete(&entity.VLMSetting{}, id).Error
}

// SetEnabled 切换启用状态。
//
// 与重排配置同一套处理：启用是"改多行"（旧失效 + 新生效）的全局唯一状态，整个动作
// 放进一个事务，并先 SELECT ... FOR UPDATE 锁住当前生效行 —— 两个请求同时启用不同
// 配置时，后到的事务会阻塞等待，不会把部分唯一索引撞成 500。
//
// 目标配置不存在时返回 applied = false，并通过事务回滚撤销"清掉旧启用"这一步 ——
// 不能出现"启用失败、顺手把原来生效的也关了"。
func (r *vlmSettingRepository) SetEnabled(ctx context.Context, id uint64, enabled bool) (bool, error) {
	var applied bool
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if !enabled {
			result := tx.Model(&entity.VLMSetting{}).Where("id = ?", id).Update("is_enabled", false)
			if result.Error != nil {
				return result.Error
			}
			applied = result.RowsAffected > 0
			return nil
		}

		var current entity.VLMSetting
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("is_enabled = ?", true).First(&current).Error
		// 没有生效记录属于正常情况（第一次启用），不算错误。
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		if err := tx.Model(&entity.VLMSetting{}).
			Where("is_enabled = ?", true).Update("is_enabled", false).Error; err != nil {
			return err
		}
		result := tx.Model(&entity.VLMSetting{}).Where("id = ?", id).Update("is_enabled", true)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			// 目标不存在：返回 ErrRecordNotFound 触发整体回滚，
			// 把上面"清掉旧启用"的动作一起撤销。
			return gorm.ErrRecordNotFound
		}
		applied = true
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return applied, err
}
