package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"narra/internal/model/entity"
)

// vector_index.go：检索用的向量索引维护。
//
// 挂在 embeddingModelRepository 上是因为索引**按模型分片**：pgvector 的索引必须建在
// 单一维度上，而不同模型可以有不同维度（见 docs/rag-database.md「向量索引」），
// 所以一个模型一条索引（WHERE model_id = ?），"谁被定为默认模型"这件事只有模型仓储知道。

// vectorIndexName 是某个模型的向量索引名。
//
// 名字里带模型 ID 是为了能被反查：换模型之后新旧两条索引并存，各自跟着自己的 model_id。
// 不带维度是因为维度的变化在 EnsureVectorIndex 里会被发现并重建（见那里的注释），
// 而同名重建正好省掉"旧索引越攒越多"。
func vectorIndexName(modelID uint64) string {
	return fmt.Sprintf("knowledge_embeddings_model_%d_hnsw_idx", modelID)
}

// ErrVectorIndexUnsupported 表示这个模型的维度建不出 HNSW 索引（超过 pgvector 的上限）。
//
// 单独一个哨兵值是因为它与"建索引失败"要分开对待：这是模型的**长期属性** ——
// 不会被修好，检索也照常正确（走精确顺序扫描），所以调用方应当提示而不是告警。
// 其余的失败（DDL 权限、数据库故障）才是异常。
var ErrVectorIndexUnsupported = errors.New("模型维度超过向量索引上限")

// maxHNSWDimensions 是 pgvector 的 HNSW 索引对单列的维度上限（含）。
//
// 这是 pgvector 的硬限制，不是配置项：超过它**建不出索引** —— 数据库只回一句
// "column cannot have more than 2000 dimensions for hnsw index"，看上去像哪条 SQL 写错了。
// 4096 维的向量模型就撞在这上面。此时检索仍然完全**正确**，只是退化成精确顺序扫描：
// 结果与有索引时一模一样，向量多了才会慢。所以这道闸门要给人话，而不是把数据库的报错
// 原样抛上去。
const maxHNSWDimensions = 2000

// hnswDimensionLimitError 判断这个模型能不能建 HNSW 索引，不能时说明白为什么。
// 纯函数：它不碰数据库，是 EnsureVectorIndex 的第一道闸门。
func hnswDimensionLimitError(model *entity.EmbeddingModel) error {
	if model == nil || model.ID == 0 {
		return fmt.Errorf("向量索引需要一个已落库的模型")
	}
	if model.Dimensions <= 0 {
		return fmt.Errorf("模型 %d 登记的维度非法: %d", model.ID, model.Dimensions)
	}
	if int(model.Dimensions) > maxHNSWDimensions {
		return fmt.Errorf(
			"%w：模型 %s 是 %d 维，超过 pgvector 的 HNSW 上限 %d 维，建不了向量索引；"+
				"检索会走精确顺序扫描（结果不受影响，向量多时会慢）",
			ErrVectorIndexUnsupported, model.Name, model.Dimensions, maxHNSWDimensions)
	}
	return nil
}

// vectorIndexAction 是 EnsureVectorIndex 对"同名索引"要采取的处置。
type vectorIndexAction int

const (
	// indexActionCreate 没有同名索引，正常建。
	indexActionCreate vectorIndexAction = iota
	// indexActionKeep 已有同名且**有效**的索引，维度也对得上，什么都不用做。
	indexActionKeep
	// indexActionRebuild 同名索引在，但不能用，必须删掉重建。
	indexActionRebuild
)

// vectorIndexDisposition 判断同名索引该怎么处置。
//
// 纯函数（不碰数据库）是刻意的：真库里造一条**无效索引**很麻烦（要让一次
// CREATE INDEX CONCURRENTLY 中途失败），而这正是最该被测到的边界 —— 见过一次真实事故：
// 一次建索引失败留下 indisvalid = false 的空壳占着名字，之后每次 IF NOT EXISTS 都直接跳过，
// 索引再也建不出来，而且没有任何报错，表现为"检索就是慢，说不出为什么"。
func vectorIndexDisposition(definition string, valid bool, dimensions int32) vectorIndexAction {
	if strings.TrimSpace(definition) == "" {
		return indexActionCreate
	}
	// 有效期与维度都要过：无效的索引规划器永远不会用，维度对不上的索引（模型改过维度）
	// 用的又是另一个表达式。两者都只能删掉重建。
	if valid && strings.Contains(definition, fmt.Sprintf("vector(%d)", dimensions)) {
		return indexActionKeep
	}
	return indexActionRebuild
}

// EnsureVectorIndex 为该模型的向量补建 HNSW 余弦索引，幂等。
//
// 为什么要有这一步：不建索引也能检索，只是每次都要顺序扫描整个 knowledge_embeddings
// 并逐行算余弦距离 —— 几万条向量时检索会从毫秒级退化到秒级，而这种退化**没有任何报错**，
// 表现为"知识库检索越来越慢"。所以索引要由服务自己补齐，而不是留一段需要手工执行的 SQL。
//
// 四条设计约束：
//
//   - 建成**部分**索引（WHERE model_id = ?）：不同模型的向量维度可能不同，pgvector 的
//     索引必须建在单一维度上；部分索引还能让索引只覆盖这一个模型的行，一直很小。
//     相应地，检索 SQL 里的模型 ID 必须写成字面量，否则规划器用不上它
//     （见 knowledge_search_repository.SearchVector 的说明）。
//   - 用 CONCURRENTLY：建索引期间这张表照常被收录写入，不能拿几分钟的写锁。
//     代价是**不能在事务里调用**（PostgreSQL 不允许 CONCURRENTLY 跑在事务块内），
//     调用方必须在事务外调它。
//   - 尽力而为：失败只影响速度（退回顺序扫描），不影响检索的正确性，
//     所以它单独返回错误，由调用方决定告警 —— 不该把一次"保存配置"判成失败。
//     维度超过 pgvector 的 HNSW 上限（maxHNSWDimensions）就属于这种情况，
//     那是建不出索引的，报错要说清"结果不受影响，只是会慢"（见 hnswDimensionLimitError）。
//   - 自愈：维度变过（该模型下还没有向量时允许改，见 checkDimensionsChange）或上次建到
//     一半失败的索引都不能留 —— 前者规划器不会选，后者规划器不会用，而且都会因为占着
//     名字让 `IF NOT EXISTS` 永远跳过。两者的处置相同：删掉重建（见 vectorIndexDisposition）。
func (r *embeddingModelRepository) EnsureVectorIndex(ctx context.Context, model *entity.EmbeddingModel) error {
	if model == nil || model.ID == 0 {
		return fmt.Errorf("向量索引需要一个已落库的模型")
	}
	name := vectorIndexName(model.ID)

	definition, valid, err := r.currentVectorIndex(ctx, name)
	if err != nil {
		return err
	}

	// 超过 HNSW 上限的模型建不出索引 —— 但同名残留要清掉：它只可能是上次失败留下的
	// 无效索引（有效索引在这种维度下根本建不出来），留着会让库里看起来"有索引"。
	if err := hnswDimensionLimitError(model); err != nil {
		if definition != "" {
			if dropErr := r.dropVectorIndex(ctx, name); dropErr != nil {
				// 清理失败也不能盖掉真正的原因：调用方要看到的是"为什么建不了索引"。
				return fmt.Errorf("%w；另外清理同名残留索引失败: %v", err, dropErr)
			}
		}
		return err
	}

	switch vectorIndexDisposition(definition, valid, model.Dimensions) {
	case indexActionKeep:
		return nil
	case indexActionRebuild:
		if err := r.dropVectorIndex(ctx, name); err != nil {
			return err
		}
	}

	statement := fmt.Sprintf(
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS %s ON knowledge_embeddings USING hnsw ((embedding::vector(%d)) vector_cosine_ops) WHERE model_id = %d",
		name, model.Dimensions, model.ID,
	)
	if err := r.db.WithContext(ctx).Exec(statement).Error; err != nil {
		return fmt.Errorf("创建向量索引 %s 失败: %w", name, err)
	}
	return nil
}

// dropVectorIndex 删掉同名索引。与建索引同一个约束：CONCURRENTLY 不能跑在事务里，
// 所以调用方必须在事务外调它（见 EnsureVectorIndex）。
func (r *embeddingModelRepository) dropVectorIndex(ctx context.Context, name string) error {
	if err := r.db.WithContext(ctx).Exec("DROP INDEX CONCURRENTLY IF EXISTS " + name).Error; err != nil {
		return fmt.Errorf("删除向量索引 %s 失败: %w", name, err)
	}
	return nil
}

// currentVectorIndex 取同名索引的定义与有效性；索引不存在时是空串与 false。
//
// 查 pg_index 而不是 pg_indexes 视图：有效性（indisvalid）只在前者里，
// 而"有索引但无效"正是要处理的两种情况之一。比较范围限定在当前 schema。
func (r *embeddingModelRepository) currentVectorIndex(ctx context.Context, name string) (string, bool, error) {
	var row struct {
		Definition string
		Valid      bool
	}
	err := r.db.WithContext(ctx).Raw(`
SELECT pg_get_indexdef(i.indexrelid) AS definition, i.indisvalid AS valid
FROM pg_index i
JOIN pg_class c ON c.oid = i.indexrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relname = ? AND n.nspname = current_schema()`, name).Scan(&row).Error
	if err != nil {
		return "", false, fmt.Errorf("查询向量索引 %s 的定义失败: %w", name, err)
	}
	return row.Definition, row.Valid, nil
}

// 索引的读取在 currentVectorIndex（见上），这里曾经用 pg_indexes 取定义 —— 那个视图里
// 没有 indisvalid，看不到"有索引但无效"这一种，而它恰恰是最难查的一种。
