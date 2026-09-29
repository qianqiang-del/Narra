package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"gorm.io/gorm"

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
// 不带维度与精度类型是因为两者的变化在 EnsureVectorIndex 里会被发现并重建（见那里的注释），
// 而同名重建正好省掉"旧索引越攒越多"。
func vectorIndexName(modelID uint64) string {
	return fmt.Sprintf("knowledge_embeddings_model_%d_hnsw_idx", modelID)
}

// ErrVectorIndexUnsupported 表示这个模型的维度建不出 HNSW 索引（超过 pgvector 的上限）。
//
// 单独一个哨兵值是因为它与"建索引失败"要分开对待：这是模型的**长期属性** ——
// 不会被修好，检索也照常正确（走精确顺序扫描），所以调用方应当提示而不是告警。
// 其余的失败（DDL 权限、数据库故障、pgvector 太老没有 halfvec）才是异常。
var ErrVectorIndexUnsupported = errors.New("模型维度超过向量索引上限")

// 索引精度分两段，这是 pgvector 对 HNSW 的硬限制，不是配置项：
//
//   - vector：单精度，上限 2000 维。≤2000 维一律用它，索引精度最高；
//   - halfvec：半精度，上限 4000 维。2001–4000 维只能用它 —— 单精度建不出来，
//     数据库只回一句 "column cannot have more than 2000 dimensions for hnsw index"，
//     看上去像哪条 SQL 写错了。半精度是 pgvector 官方给的高维出路，召回损失通常可忽略。
//
// 超过 4000 维（halfvec 也打不住）时检索退化成精确顺序扫描：结果与有索引时一模一样
// （而且是 100% 召回），只是向量多了会慢。
const (
	maxVectorIndexDimensions  = 2000
	maxHalfvecIndexDimensions = 4000
)

// vectorIndexType 是索引与检索两处共用的精度类型。
type vectorIndexType string

const (
	vectorIndexTypeVector  vectorIndexType = "vector"
	vectorIndexTypeHalfvec vectorIndexType = "halfvec"
)

// opclass 返回该精度的余弦距离算子类，如 vector_cosine_ops。
func (t vectorIndexType) opclass() string {
	return string(t) + "_cosine_ops"
}

// expression 是索引定义与检索 SQL 必须**逐字一致**的表达式片段，如 vector(1536)。
//
// 带精度类型是刻意的：halfvec(3072) 并不包含子串 vector(3072)（反过来也一样），
// 只比维度不比类型的处置会把"换过精度"误判成"定义没变"，自愈变成每次启动都重建。
func (t vectorIndexType) expression(dimensions int32) string {
	return fmt.Sprintf("%s(%d)", t, dimensions)
}

// vectorIndexTypeFor 判断这个模型该建哪种精度的索引，建不了时说明白为什么。
// 纯函数：它只按维度分段与校验，不碰数据库 —— 数据库有没有 halfvec（pgvector 0.7+）
// 是另一道探测（见 halfvecSupport）。
func vectorIndexTypeFor(model *entity.EmbeddingModel) (vectorIndexType, error) {
	if model == nil || model.ID == 0 {
		return "", fmt.Errorf("向量索引需要一个已落库的模型")
	}
	if model.Dimensions <= 0 {
		return "", fmt.Errorf("模型 %d 登记的维度非法: %d", model.ID, model.Dimensions)
	}
	if model.Dimensions <= maxVectorIndexDimensions {
		return vectorIndexTypeVector, nil
	}
	if model.Dimensions <= maxHalfvecIndexDimensions {
		return vectorIndexTypeHalfvec, nil
	}
	return "", fmt.Errorf(
		"%w：模型 %s 是 %d 维，超过 pgvector 的 halfvec 索引上限 %d 维（HNSW 建不出索引）；"+
			"检索会走精确顺序扫描（结果不受影响，向量多时会慢）",
		ErrVectorIndexUnsupported, model.Name, model.Dimensions, maxHalfvecIndexDimensions)
}

// vectorSearchCastType 返回检索 SQL 该把列与查询向量 cast 成什么精度。
//
// 与索引侧共用同一套维度分段：≤2000 维用 vector；2001–4000 维在数据库支持 halfvec
// 时用它（与索引表达式一致，才用得上索引），否则退回 vector —— 用不上索引，
// 但检索照常正确、还是全精度的。>4000 维没有索引可用，同样退回 vector。
func vectorSearchCastType(dimensions int, halfvecAvailable bool) vectorIndexType {
	if dimensions > maxVectorIndexDimensions && dimensions <= maxHalfvecIndexDimensions && halfvecAvailable {
		return vectorIndexTypeHalfvec
	}
	return vectorIndexTypeVector
}

// halfvecSupport 是"当前数据库有没有 halfvec 类型（pgvector ≥ 0.7）"的懒探测缓存。
//
// 索引侧用它决定 2001–4000 维能不能建半精度索引；检索侧用它决定高维查询该 cast 成
// halfvec 还是退回全精度 vector —— 老环境上 cast 一个不存在的类型会让检索直接失败，
// 而"没有索引"只该是慢，不该是错。两个仓储各持一个（见它们的构造）。
type halfvecSupport struct {
	mu       sync.Mutex
	resolved bool
	ok       bool
}

// available 探测（并缓存）halfvec 类型是否可用。
//
// 走 to_regtype 而不是查 pg_type：索引与查询解析类型时同样看 search_path，
// 探测的口径必须和它们一致。探测失败不缓存 —— 数据库故障是暂时的，下次调用再试；
// 在试出来之前按"没有"处理，行为最保守（退回全精度顺序扫描）。
func (s *halfvecSupport) available(ctx context.Context, db *gorm.DB) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resolved {
		return s.ok
	}
	var ok bool
	if err := db.WithContext(ctx).Raw("SELECT to_regtype('halfvec') IS NOT NULL").Scan(&ok).Error; err != nil {
		return false
	}
	s.resolved, s.ok = true, ok
	return ok
}

// vectorIndexAction 是 EnsureVectorIndex 对"同名索引"要采取的处置。
type vectorIndexAction int

const (
	// indexActionCreate 没有同名索引，正常建。
	indexActionCreate vectorIndexAction = iota
	// indexActionKeep 已有同名且**有效**的索引，精度与维度也对得上，什么都不用做。
	indexActionKeep
	// indexActionRebuild 同名索引在，但不能用，必须删掉重建。
	indexActionRebuild
)

// vectorIndexDisposition 判断同名索引该怎么处置。
//
// expression 是期望的表达式片段（vector(1536) / halfvec(3072)，见 vectorIndexType.expression）——
// 精度类型与维度都要对上：模型改过维度、或在 2000 维边界两边换过精度，旧索引挂的就是
// 另一个表达式，规划器永远用不上它，只能删掉重建。
//
// 纯函数（不碰数据库）是刻意的：真库里造一条**无效索引**很麻烦（要让一次
// CREATE INDEX CONCURRENTLY 中途失败），而这正是最该被测到的边界 —— 见过一次真实事故：
// 一次建索引失败留下 indisvalid = false 的空壳占着名字，之后每次 IF NOT EXISTS 都直接跳过，
// 索引再也建不出来，而且没有任何报错，表现为"检索就是慢，说不出为什么"。
func vectorIndexDisposition(definition string, valid bool, expression string) vectorIndexAction {
	if strings.TrimSpace(definition) == "" {
		return indexActionCreate
	}
	// 有效期与表达式都要过：无效的索引规划器永远不会用；表达式对不上的索引（模型改过
	// 维度或精度）用的又是另一套距离计算。两者都只能删掉重建。
	if valid && strings.Contains(definition, expression) {
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
// 六条设计约束：
//
//   - 按维度选精度（见 vectorIndexTypeFor）：≤2000 维用单精度 vector；2001–4000 维用
//     半精度 halfvec（单精度建不出来，halfvec 是 pgvector 官方给的高维出路）；
//     >4000 维建不出索引，检索退化为精确顺序扫描（100% 召回，只是会慢）。
//   - 建成**部分**索引（WHERE model_id = ?）：不同模型的向量维度可能不同，pgvector 的
//     索引必须建在单一维度上；部分索引还能让索引只覆盖这一个模型的行，一直很小。
//     相应地，检索 SQL 里的模型 ID 必须写成字面量，否则规划器用不上它
//     （见 knowledge_search_repository.SearchVector 的说明）。
//   - 用 CONCURRENTLY：建索引期间这张表照常被收录写入，不能拿几分钟的写锁。
//     代价是**不能在事务里调用**（PostgreSQL 不允许 CONCURRENTLY 跑在事务块内），
//     调用方必须在事务外调它。
//   - 尽力而为：失败只影响速度（退回顺序扫描），不影响检索的正确性，
//     所以它单独返回错误，由调用方决定告警 —— 不该把一次"保存配置"判成失败。
//     维度超过 halfvec 上限（>4000）就属于这种情况，那是建不出索引的，报错要说清
//     "结果不受影响，只是会慢"（见 vectorIndexTypeFor）。
//   - 环境门槛：halfvec 需要 pgvector ≥ 0.7。数据库没有它时不硬上，直接报错说明
//     "升级后会自动补建" —— 这是可修复的环境问题，不是模型的长期属性，调用方按告警处理。
//   - 自愈：维度或精度变过（该模型下还没有向量时允许改，见 checkDimensionsChange）
//     或上次建到一半失败的索引都不能留 —— 前者规划器不会选，后者规划器不会用，
//     而且都会因为占着名字让 `IF NOT EXISTS` 永远跳过。两者的处置相同：删掉重建
//     （见 vectorIndexDisposition）。
func (r *embeddingModelRepository) EnsureVectorIndex(ctx context.Context, model *entity.EmbeddingModel) error {
	if model == nil || model.ID == 0 {
		return fmt.Errorf("向量索引需要一个已落库的模型")
	}
	name := vectorIndexName(model.ID)

	definition, valid, err := r.currentVectorIndex(ctx, name)
	if err != nil {
		return err
	}

	indexType, err := vectorIndexTypeFor(model)
	if err == nil && indexType == vectorIndexTypeHalfvec && !r.halfvec.available(ctx, r.db) {
		err = fmt.Errorf(
			"模型 %s 是 %d 维，需要 pgvector 的 halfvec 类型（0.7 起提供），当前数据库没有；"+
				"建不了向量索引，检索会走精确顺序扫描（结果不受影响，向量多时会慢）；"+
				"升级 pgvector 后重启即可自动补建",
			model.Name, model.Dimensions)
	}

	// 建不出索引的两种情况（维度超过 halfvec 上限 / 当前 pgvector 没有 halfvec）
	// 都要清掉同名残留：它只可能是上次失败留下的无效索引（有效索引在这种情况下
	// 根本建不出来），留着会让库里看起来"有索引"。
	if err != nil {
		if definition != "" {
			if dropErr := r.dropVectorIndex(ctx, name); dropErr != nil {
				// 清理失败也不能盖掉真正的原因：调用方要看到的是"为什么建不了索引"。
				return fmt.Errorf("%w；另外清理同名残留索引失败: %v", err, dropErr)
			}
		}
		return err
	}

	switch vectorIndexDisposition(definition, valid, indexType.expression(model.Dimensions)) {
	case indexActionKeep:
		return nil
	case indexActionRebuild:
		if err := r.dropVectorIndex(ctx, name); err != nil {
			return err
		}
	}

	statement := fmt.Sprintf(
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS %s ON knowledge_embeddings USING hnsw ((embedding::%s(%d)) %s) WHERE model_id = %d",
		name, indexType, model.Dimensions, indexType.opclass(), model.ID,
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
