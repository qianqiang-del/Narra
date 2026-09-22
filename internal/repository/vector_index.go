package repository

import (
	"context"
	"database/sql"
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
			"模型 %s 是 %d 维，超过 pgvector 的 HNSW 上限 %d 维，建不了向量索引；"+
				"检索会走精确顺序扫描（结果不受影响，向量多时会慢）",
			model.Name, model.Dimensions, maxHNSWDimensions)
	}
	return nil
}

// EnsureVectorIndex 为该模型的向量补建 HNSW 余弦索引，幂等。
//
// 为什么要有这一步：不建索引也能检索，只是每次都要顺序扫描整个 knowledge_embeddings
// 并逐行算余弦距离 —— 几万条向量时检索会从毫秒级退化到秒级，而这种退化**没有任何报错**，
// 表现为"知识库检索越来越慢"。所以索引要由服务自己补齐，而不是留一段需要手工执行的 SQL。
//
// 三条设计约束：
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
//
// 维度自愈：模型的维度可能被改过（该模型下还没有向量时允许改，见 checkDimensionsChange），
// 旧索引建在 `embedding::vector(旧维度)` 这个表达式上，而检索用的是另一个表达式，
// 规划器永远不会选它 —— 留着只是白吃写入开销。所以发现索引定义里的维度对不上时，
// 删掉重建，而不是带着一条死索引继续跑。
func (r *embeddingModelRepository) EnsureVectorIndex(ctx context.Context, model *entity.EmbeddingModel) error {
	if err := hnswDimensionLimitError(model); err != nil {
		return err
	}
	name := vectorIndexName(model.ID)

	definition, err := r.currentVectorIndexDef(ctx, name)
	if err != nil {
		return err
	}
	if definition != "" {
		if strings.Contains(definition, fmt.Sprintf("vector(%d)", model.Dimensions)) {
			return nil // 已经按当前维度建好了，什么都不用做
		}
		// 维度对不上（例如 1536 → 3072）：旧索引已经不可能被检索用上。
		// 用名字判断维度而不是比对整条定义：pg_get_indexdef 的输出带 schema 限定与
		// 自动加的括号，逐字比对会随着 PostgreSQL 版本变化而误判。
		if err := r.db.WithContext(ctx).Exec("DROP INDEX CONCURRENTLY IF EXISTS " + name).Error; err != nil {
			return fmt.Errorf("删除维度已过期的向量索引 %s 失败: %w", name, err)
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

// currentVectorIndexDef 取同名索引的定义，索引不存在时返回空串。
//
// 查 pg_indexes 而不是去试 CREATE INDEX IF NOT EXISTS：后者在"索引存在但维度过期"时
// 什么都不做，正好绕过了这里要解决的问题。
func (r *embeddingModelRepository) currentVectorIndexDef(ctx context.Context, name string) (string, error) {
	var definition string
	err := r.db.WithContext(ctx).Raw(
		"SELECT indexdef FROM pg_indexes WHERE schemaname = current_schema() AND indexname = ?",
		name,
	).Row().Scan(&definition)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("查询向量索引 %s 的定义失败: %w", name, err)
	}
	return definition, nil
}
