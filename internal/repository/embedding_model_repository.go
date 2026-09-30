package repository

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"narra/internal/model/entity"
)

// embeddingModelRepository 基于 GORM 的向量模型仓储。
type embeddingModelRepository struct {
	db *gorm.DB

	// halfvec 缓存"数据库有没有 halfvec 类型"：2001–4000 维的索引要用它决定能不能建。
	halfvec halfvecSupport
}

// NewEmbeddingModelRepository 创建向量模型仓储。
func NewEmbeddingModelRepository(db *gorm.DB) EmbeddingModelRepository {
	return &embeddingModelRepository{db: db}
}

// GetDefault 返回当前唯一那个默认模型。
//
// 用 is_default = true 查而不是按名字查：写入知识库向量的一方手里只有"当前生效的
// 配置"，它需要的是"该把这个向量挂在哪一行"，而这两件事在用户改过设置页之后
// 可能指向不同的行 —— 以默认标记为准，检索才能用同一个模型把向量取出来。
//
// 查询带 Limit(1)：数据库上有部分唯一索引保证至多一条，但指纹库里那个索引不一定
// 存在（只跑过 AutoMigrate、没执行过 migrations/0002 的库就没有），所以这里不依赖它。
func (r *embeddingModelRepository) GetDefault(ctx context.Context) (*entity.EmbeddingModel, error) {
	var model entity.EmbeddingModel
	err := r.db.WithContext(ctx).
		Where("is_default = ?", true).
		Order("id DESC").
		Limit(1).
		First(&model).Error
	if err != nil {
		return nil, err
	}
	return &model, nil
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
		// ID / CreatedAt 沿用旧值，只覆盖由入参推导出来的两列（维度与默认标记）。
		if err := clearOtherDefaults(tx, existing.ID); err != nil {
			return err
		}
		existing.Dimensions = model.Dimensions
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

// CountVectorsByModel 统计每个已登记模型名下的向量数（左连接，没有向量的模型计 0）。
//
// 不限定默认模型：体检要同时看到"当前模型下有没有"和"别的模型下还有多少"，
// 只看默认模型那一行的话，就把"换过模型"与"全库都还没有向量"混成同一种形态了。
func (r *embeddingModelRepository) CountVectorsByModel(ctx context.Context) ([]entity.ModelVectorCount, error) {
	var rows []entity.ModelVectorCount
	err := r.db.WithContext(ctx).Raw(`
SELECT m.id AS model_id, m.name AS name, count(e.id) AS vectors
FROM embedding_models m
LEFT JOIN knowledge_embeddings e ON e.model_id = m.id
GROUP BY m.id, m.name
ORDER BY m.id`).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("统计各模型向量数失败: %w", err)
	}
	return rows, nil
}

// DeleteUnusedModels 删除名下已无向量的非默认模型行，返回被删掉的名字。
//
// 判定与删除放在一条 SQL 里（NOT is_default + NOT EXISTS 向量），不先查后删：
// 两次查询之间可能有并发写入让某一行重新变得"有用"，条件交给数据库一次性判定。
// RETURNING 让调用方能把删了什么写进日志 —— 静默删除会让人事后完全无从对账。
//
// 与 EnsureDefault 的竞争由行锁兜底：对方会先锁住目标行再改，DELETE 拿到锁后
// 会重新核对 is_default，不会把刚被设为默认的那一行删掉；反过来若它先删，
// EnsureDefault 查不到行会走插入分支，结果同样正确。
func (r *embeddingModelRepository) DeleteUnusedModels(ctx context.Context) ([]string, error) {
	var deleted []string
	err := r.db.WithContext(ctx).Raw(`
DELETE FROM embedding_models m
WHERE NOT m.is_default
  AND NOT EXISTS (
	SELECT 1 FROM knowledge_embeddings e WHERE e.model_id = m.id
  )
RETURNING m.name`).Scan(&deleted).Error
	if err != nil {
		return nil, fmt.Errorf("清理无向量的旧模型失败: %w", err)
	}
	return deleted, nil
}
