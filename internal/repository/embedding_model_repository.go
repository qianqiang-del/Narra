package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"narra/internal/model/entity"
)

// embeddingModelRepository 基于 GORM 的向量模型仓储。
type embeddingModelRepository struct {
	db *gorm.DB
}

// NewEmbeddingModelRepository 创建向量模型仓储。
func NewEmbeddingModelRepository(db *gorm.DB) EmbeddingModelRepository {
	return &embeddingModelRepository{db: db}
}

// EnsureDefault 把 model 落成唯一默认模型。
//
// 整个过程在一个事务里，因为"让本行成为默认"和"让别的行退出默认"是两次写：
// 中间失败若不复位，会留下两个 is_default = true（数据库有部分唯一索引时直接写不进去）
// 或者一个都没有（检索时找不到模型）。
func (r *embeddingModelRepository) EnsureDefault(ctx context.Context, model entity.EmbeddingModel) (*entity.EmbeddingModel, error) {
	var result entity.EmbeddingModel

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		existing, err := lockByName(tx, model.Name)
		if err != nil {
			return err
		}

		if existing == nil {
			// 首次登记这个模型名。先清默认再插入：is_default 上有部分唯一索引
			// （embedding_models_one_default_idx），两行同时为 true 会被索引拒绝。
			if err := clearOtherDefaults(tx, 0); err != nil {
				return err
			}
			model.IsDefault = true
			model.Enabled = true
			if err := tx.Create(&model).Error; err != nil {
				return err
			}
			result = model
			return nil
		}

		if err := checkDimensionsChange(tx, existing, model.Dimensions); err != nil {
			return err
		}

		// 已存在则复用原行：Name 上有唯一约束，同名再插一行会直接失败。
		// ID / CreatedAt 沿用旧值，只覆盖由配置推导出来的字段。
		if err := clearOtherDefaults(tx, existing.ID); err != nil {
			return err
		}
		existing.Provider = model.Provider
		existing.BaseURL = model.BaseURL
		existing.Dimensions = model.Dimensions
		existing.ModelVersion = model.ModelVersion
		existing.Enabled = true
		existing.IsDefault = true
		if err := tx.Save(existing).Error; err != nil {
			return err
		}
		result = *existing
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &result, nil
}

// lockByName 按名字查模型行并加行锁，查不到返回 nil（不算错误）。
//
// 加锁是因为"读一次、判断、再写"这三步跨了多次语句：两个请求同时保存配置时，
// 后到的会阻塞在这条查询上，等前一个提交后再读到最新值，而不是两边都拿着旧值去覆盖。
func lockByName(tx *gorm.DB, name string) (*entity.EmbeddingModel, error) {
	var model entity.EmbeddingModel
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("name = ?", name).
		First(&model).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// 首次登记。注意这种情况下列锁不生效（没有行可锁），并发首次保存会各自插入、
		// 由 uni_embedding_models_name 唯一索引拒掉后到的那个 —— 报错而不是写脏数据，
		// 对"同一个设置页被同时点两次保存"这种自伤场景是可以接受的结局。
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &model, nil
}

// clearOtherDefaults 把所有其它行的 is_default 置为 false，keepID 为 0 表示全都清。
//
// 不依赖数据库的部分唯一索引来兜底：本机数据库可能只跑过 AutoMigrate 而没执行
// migrations/0002_knowledge_base.sql，那个索引未必存在（见仓库里 migrations/README.md）。
// 唯一性由这里自己做实，索引存在时它只是多一层保险。
func clearOtherDefaults(tx *gorm.DB, keepID uint64) error {
	query := tx.Model(&entity.EmbeddingModel{}).Where("is_default = ?", true)
	if keepID != 0 {
		query = query.Where("id <> ?", keepID)
	}
	return query.Update("is_default", false).Error
}

// checkDimensionsChange 判断同名模型能否就地改维度。
//
// 模型下还没有任何向量时允许改：此时没有任何数据依赖旧维度，改了不留后患。
//
// 已经有向量时必须拒绝，而且两种失败形态都很难查：
//   - 维度不同（1536 → 3072）：检索时向量比较直接报维度错误，炸得明白但已经写了半份脏数据；
//   - 维度相同、模型不同：向量能存能算、不报任何错，但两者语义空间不同，
//     表现为检索结果莫名变差 —— 这类静默劣化是最难排查的一种。
//
// 正确做法是换一个模型名登记成新行（UNIQUE (chunk_id, model_id) 允许新旧向量共存），
// 而不是就地改这一行。所以这里的拒绝不是保守，而是逼出正确路径。
func checkDimensionsChange(tx *gorm.DB, existing *entity.EmbeddingModel, requested int32) error {
	if existing.Dimensions == requested {
		return nil
	}

	vectors, err := countVectors(tx, existing.ID)
	if err != nil {
		return err
	}
	if vectors == 0 {
		return nil
	}

	return &DimensionsMismatchError{
		ModelName: existing.Name,
		ModelID:   existing.ID,
		Recorded:  existing.Dimensions,
		Requested: requested,
		Vectors:   vectors,
	}
}

// countVectors 统计某模型下已有的向量数量。
func countVectors(tx *gorm.DB, modelID uint64) (int64, error) {
	var count int64
	err := tx.Model(&entity.KnowledgeEmbedding{}).
		Where("model_id = ?", modelID).
		Count(&count).Error
	return count, err
}
