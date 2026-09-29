package repository

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"narra/internal/model/entity"
)

// knowledgeSearchRepository 基于 GORM（含少量手写 SQL）的检索仓储。
//
// 手写 SQL 的地方只有两条召回路：pgvector 的距离算子、表达式索引的匹配写法，
// 以及"命中词项数"这种逐项打分，用查询构造器拼出来反而更难读。
type knowledgeSearchRepository struct {
	db *gorm.DB

	// halfvec 缓存"数据库有没有 halfvec 类型"：>2000 维的查询 cast 要用它决定。
	halfvec halfvecSupport
}

// NewKnowledgeSearchRepository 创建检索仓储。
func NewKnowledgeSearchRepository(db *gorm.DB) KnowledgeSearchRepository {
	return &knowledgeSearchRepository{db: db}
}

// chunkViewColumns 是两条召回路共用的投影列。
//
// 文档侧用 INNER JOIN：一个切片必然属于一篇文档，而检索的过滤条件（ready、enabled）
// 就挂在文档上 —— 用 LEFT JOIN 再在 Go 里过滤，等于把"能不能被召回"这条底线
// 从 SQL 挪到调用方，迟早会漏掉一处。
const chunkViewColumns = `c.id AS chunk_id,
	c.document_id,
	c.chunk_index,
	c.heading,
	c.content,
	c.character_count,
	d.title AS document_title,
	d.source_type,
	d.source_uri`

// chunkHaystack 是词法匹配的对象：切片正文 + 章节标题 + 文档标题。
//
// 三者拼成一段再匹配，而不是各写一个 LIKE 再 OR 起来：一个词项命中任意一处都算命中，
// 打分时只加 1 分（同一词项重复出现不加分），语义上就是"这个词项有没有出现在这条切片周围"。
const chunkHaystack = `(c.content || ' ' || coalesce(c.heading, '') || ' ' || d.title)`

// SearchVector 按余弦相似度召回候选，见 KnowledgeSearchRepository 的说明。
//
// ⚠️ 模型 ID、精度类型与维度**拼**进 SQL 而不是用占位符，这不是 SQL 注入的口子
// （两者都是本服务自己生成的整数/枚举），而是规划器的硬约束：默认模型的 HNSW 是
// 部分索引（WHERE model_id = 1）且建在表达式 (embedding::vector(1536)) 或
// (embedding::halfvec(3072)) 上，查询里的模型 ID 只要是个参数，规划器就无法证明
// "model_id = $1 蕴含 model_id = 1"（pgx 走预处理语句，通用计划下参数不是常量），
// 于是部分索引被跳过；表达式里的精度与维度同理，必须与索引定义逐字一致。
// 结果就是一条永远用不上索引的检索 —— 数据量一大才暴露，而 EXPLAIN 之外看不出原因。
//
// 相似度写成 1 - 余弦距离：pgvector 的 `<=>` 给的是距离（越小越像），
// 对外的"得分"统一成越大越好，免得每个调用方都要记一次方向。
func (r *knowledgeSearchRepository) SearchVector(
	ctx context.Context,
	query entity.KnowledgeVectorQuery,
) ([]entity.KnowledgeChunkView, error) {
	if query.ModelID == 0 {
		return nil, fmt.Errorf("向量召回缺少模型 ID")
	}
	if query.Dimensions <= 0 {
		return nil, fmt.Errorf("向量召回的维度非法: %d", query.Dimensions)
	}
	if strings.TrimSpace(query.Vector) == "" {
		return nil, fmt.Errorf("向量召回缺少查询向量")
	}
	if query.Limit <= 0 {
		return nil, fmt.Errorf("向量召回的条数上限非法: %d", query.Limit)
	}

	// >2000 维的 cast 要与索引表达式一致，且只在数据库真的提供 halfvec 时才用：
	// 老 pgvector 上退成全精度 vector，用不上索引，但检索照常正确（见 useHalfvec）。
	cast := vectorSearchCastType(query.Dimensions, r.useHalfvec(ctx, query.Dimensions))
	filterClause, filterArgs := chunkFilterClause(query.Filter)
	statement := vectorSearchStatement(query.Dimensions, query.ModelID, query.Limit, cast, filterClause)

	// 参数必须按 SQL 文本里占位符出现的顺序排：
	// SELECT 里的查询向量 → WHERE 里的状态 → WHERE 里的过滤参数 → ORDER BY 里的查询向量。
	// 过滤参数夹在中间，不能顺手追加到末尾 —— ORDER BY 的占位符在文本里排在它们后面。
	args := []any{query.Vector, entity.KnowledgeDocumentStatusReady}
	args = append(args, filterArgs...)
	args = append(args, query.Vector)

	var rows []entity.KnowledgeChunkView
	if err := r.db.WithContext(ctx).Raw(statement, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("向量召回查询失败: %w", err)
	}
	return rows, nil
}

// useHalfvec 判断这次检索要不要用半精度 cast。
//
// 只有维度落在 (vector 上限, halfvec 上限] 才需要探测数据库能力，其余维度直接回答
// "不用"，一次探测都不做：≤2000 维用单精度，>4000 维本来就没有索引可用。
func (r *knowledgeSearchRepository) useHalfvec(ctx context.Context, dimensions int) bool {
	if dimensions <= maxVectorIndexDimensions || dimensions > maxHalfvecIndexDimensions {
		return false
	}
	return r.halfvec.available(ctx, r.db)
}

// vectorSearchStatement 拼向量召回的 SQL。
//
// cast 由调用方按维度选定（见 vectorSearchCastType），必须与 EnsureVectorIndex 建出的
// 索引表达式**逐字一致**（含精度与维度），否则规划器匹配不上，索引静默失效。
//
// filterClause 由 chunkFilterClause 生成（形如 ` AND d.source_type IN (?,?)`），
// 没有过滤条件时是空串。它只往 WHERE 里追加普通条件，不碰 model_id 与 cast 表达式，
// 所以不影响部分索引的匹配。
//
// 单独成函数是为了让用例能 EXPLAIN 这条**真实的**语句（见 TestSearchVectorCanUseHNSWIndex
// 与 TestSearchVectorCanUseHalfvecIndex）：把 SQL 抄一份到测试里，改了实现却忘了改测试，
// 那条断言就成了安慰剂。占位符顺序见 SearchVector。
func vectorSearchStatement(dimensions int, modelID uint64, limit int, cast vectorIndexType, filterClause string) string {
	return fmt.Sprintf(`
SELECT %s,
	1 - ((e.embedding::%s(%d)) <=> (?::%s(%d))) AS raw_score
FROM knowledge_embeddings e
JOIN knowledge_chunks c ON c.id = e.chunk_id
JOIN knowledge_documents d ON d.id = c.document_id
WHERE e.model_id = %d
	AND d.enabled
	AND d.status = ?%s
ORDER BY ((e.embedding::%s(%d)) <=> (?::%s(%d))) ASC, c.id ASC
LIMIT %d`,
		chunkViewColumns,
		cast, dimensions, cast, dimensions,
		modelID,
		filterClause,
		cast, dimensions, cast, dimensions,
		limit)
}

// SearchLexical 按词项命中召回候选，见 KnowledgeSearchRepository 的说明。
//
// 命中数在 SQL 里算（而不是把候选拉回来在 Go 里数）：词法匹配是 OR 拼起来的顺序扫描，
// 命中的行可能远多于 limit，先按命中数排序再截断，才不会把"只蹭到一个噪声词项"的行
// 占满候选窗口。同一词项命中多处只算 1 分，所以在 SQL 里是"每个词项一个 CASE"求和，
// 而不是"每出现一次加 1 分" —— 后者会让长切片永远排在前面（它更容易重复出现某个词）。
//
// 排序带 id 兜底：命中数相同的行很多（尤其是全是 1 分的时候），不给定顺序的话
// 两次检索会返回不同的候选，融合出来的结果也就跟着抖。
//
// 过滤条件来自 query.Filter，与向量路共用同一个构造函数，口径只有一份。
func (r *knowledgeSearchRepository) SearchLexical(
	ctx context.Context,
	query entity.KnowledgeLexicalQuery,
) ([]entity.KnowledgeChunkView, error) {
	if query.Limit <= 0 {
		return nil, fmt.Errorf("词法召回的条数上限非法: %d", query.Limit)
	}

	patterns := make([]string, 0, len(query.Terms))
	for _, term := range query.Terms {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		// 转义交给 likePattern：% 与 _ 是用户的正文，不是通配符。
		patterns = append(patterns, likePattern(term))
	}
	if len(patterns) == 0 {
		return nil, nil
	}

	cases := make([]string, len(patterns))
	for index := range patterns {
		cases[index] = fmt.Sprintf("CASE WHEN %s ILIKE ? THEN 1 ELSE 0 END", chunkHaystack)
	}
	hits := "(" + strings.Join(cases, " + ") + ")"

	filterClause, filterArgs := chunkFilterClause(query.Filter)
	statement := fmt.Sprintf(`
SELECT %s,
	%s AS raw_score
FROM knowledge_chunks c
JOIN knowledge_documents d ON d.id = c.document_id
WHERE d.enabled
	AND d.status = ?
	AND %s > 0%s
ORDER BY raw_score DESC, c.id ASC
LIMIT %d`,
		chunkViewColumns, hits, hits, filterClause, query.Limit)

	// 占位符顺序：SELECT 里的命中数（每词项一个）→ WHERE 里的状态 →
	// WHERE 里的命中数（同一批模式）→ 过滤参数。过滤条件拼在 WHERE 末尾，
	// 参数也必须跟着排在最后。
	args := make([]any, 0, len(patterns)*2+1+len(filterArgs))
	args = append(args, patternsToArgs(patterns)...)
	args = append(args, entity.KnowledgeDocumentStatusReady)
	args = append(args, patternsToArgs(patterns)...)
	args = append(args, filterArgs...)

	var rows []entity.KnowledgeChunkView
	if err := r.db.WithContext(ctx).Raw(statement, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("词法召回查询失败: %w", err)
	}
	return rows, nil
}

// patternsToArgs 把字符串参数表复制成 any 切片。
//
// 同一批模式要在 SELECT 与 WHERE 各出现一次，占位符不能复用同一个参数，
// 所以参数也要备两份。复制而不是共享底层数组：append 到共享数组上会让两段参数互相踩。
func patternsToArgs(patterns []string) []any {
	args := make([]any, len(patterns))
	for index, pattern := range patterns {
		args[index] = pattern
	}
	return args
}

// chunkFilterClause 把文档侧的过滤条件拼成两条召回路共用的 SQL 片段与参数。
//
// 返回的片段以 ` AND ` 开头、没有条件时为空串，调用方可以直接接在原有的
// WHERE 条件后面。两路共用这一处实现是刻意的：过滤条件只加在一路时，
// 融合的两份名单搜的范围不一样，结果会以很难解释的方式偏移。
//
// 参数按"片段里占位符出现的顺序"返回；调用方负责把自己的参数与它拼对位置
// （见 SearchVector 与 SearchLexical 各自的参数顺序说明）。
func chunkFilterClause(filter entity.KnowledgeChunkFilter) (string, []any) {
	clauses := make([]string, 0, 4)
	args := make([]any, 0, 4)

	if len(filter.SourceTypes) > 0 {
		clauses = append(clauses, "d.source_type IN ("+sqlPlaceholders(len(filter.SourceTypes))+")")
		for _, sourceType := range filter.SourceTypes {
			args = append(args, sourceType)
		}
	}
	if len(filter.DocumentIDs) > 0 {
		clauses = append(clauses, "d.id IN ("+sqlPlaceholders(len(filter.DocumentIDs))+")")
		for _, id := range filter.DocumentIDs {
			args = append(args, id)
		}
	}
	if filter.CreatedFrom != nil {
		// 下界含、上界不含：半开区间，见 entity.KnowledgeChunkFilter 的注释。
		clauses = append(clauses, "d.created_at >= ?")
		args = append(args, filter.CreatedFrom.UTC())
	}
	if filter.CreatedTo != nil {
		clauses = append(clauses, "d.created_at < ?")
		args = append(args, filter.CreatedTo.UTC())
	}

	if len(clauses) == 0 {
		return "", nil
	}
	return " AND " + strings.Join(clauses, " AND "), args
}

// sqlPlaceholders 返回 n 个逗号分隔的占位符（n ≥ 1 时是 "?" / "?,?" / "?,?,?"…）。
//
// 用逐项占位符而不是 IN (?) 加数组参数：这条 SQL 是手写原文，
// 参数按位置传给驱动；数组参数的编码方式取决于驱动，逐项展开的行为最确定。
func sqlPlaceholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
