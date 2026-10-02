package repository

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"gorm.io/gorm"

	"narra/internal/model/entity"
)

// knowledgeSearchRepository 基于 GORM（含少量手写 SQL）的检索仓储。
//
// 手写 SQL 的地方只有两条召回路：pgvector 的距离算子、表达式索引的匹配写法，
// 以及"加权命中分"这种逐项打分，用查询构造器拼出来反而更难读。
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
	c.section_path,
	c.symbol,
	c.content,
	c.character_count,
	d.title AS document_title,
	d.source_type,
	d.source_uri`

// chunkTextHaystack 是词法匹配与词法索引的匹配面：切片正文 + 章节标题。
//
// 文档标题**不在**其中：它在另一张表，跨表表达式建不了索引（见 lexical_index.go）。
// 标题由 titleHaystack 的独立小查询覆盖，两边各自打分、合并时取较高分，
// 保持改造前"一个词项在正文与标题各出现也只算命中一次"的语义。
const chunkTextHaystack = `(c.content || ' ' || coalesce(c.heading, ''))`

// titleHaystack 是标题查询的匹配面。
const titleHaystack = `d.title`

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
// 两条候选查询 + 一次合并，是"索引能用得上"逼出来的形状：
//
//   - 正文查询：匹配面是切片正文 + 章节标题，与词法表达式索引的定义一致，
//     两字以上的词项都能走索引（见 lexical_index.go）；
//   - 标题查询：文档标题在另一张表，拼进同一个匹配面会让表达式索引永远匹配不上，
//     所以它单独查一遍，命中的文档下所有切片照常进入候选；
//   - 合并时取两条路的**较高分**：一个词项在正文与标题各出现一次也只算命中一次，
//     保持改造前"拼成一段再匹配"的语义（见 mergeLexicalRows）。
//
// 打分在 SQL 里算（而不是把候选拉回来在 Go 里算）：命中的行可能远多于 limit，
// 先按分数排序再截断，才不会把"只蹭到一个噪声词项"的行占满候选窗口。每个词项一个
// CASE：命中得权重分、不命中 0 分，同一词项命中多处只算一次 —— 按出现次数加分会让
// 长切片永远排在前面（它更容易重复出现某个词）。
//
// 权重是**参数**而不是写死的数字：档位口径在检索侧的分词层（internal/rag/tokenize），
// 这里只是执行者，改权重不用改 SQL。
//
// 短语只加分、不参与准入：能进入 WHERE 的仍是"命中至少一个词项"的行，短语在这些行上
// 额外加一笔权重。
//
// 排序带 id 兜底：命中分相同的行很多（尤其是全是 1 分的时候），不给定顺序的话
// 两次检索会返回不同的候选，融合出来的结果也就跟着抖。
//
// 过滤条件来自 query.Filter，两条候选查询共用同一个构造函数，口径只有一份。
func (r *knowledgeSearchRepository) SearchLexical(
	ctx context.Context,
	query entity.KnowledgeLexicalQuery,
) ([]entity.KnowledgeChunkView, error) {
	if query.Limit <= 0 {
		return nil, fmt.Errorf("词法召回的条数上限非法: %d", query.Limit)
	}

	terms, err := cleanLexicalTerms(query.Terms)
	if err != nil {
		return nil, err
	}
	if len(terms) == 0 {
		return nil, nil
	}
	phrases, err := cleanLexicalTerms(query.Phrases)
	if err != nil {
		return nil, err
	}

	pairs := make([]lexicalPair, 0, len(terms)+len(phrases))
	match := make([]string, 0, len(terms))
	for _, term := range terms {
		// 转义交给 likePattern：% 与 _ 是用户的正文，不是通配符。
		pattern := likePattern(term.Text)
		pairs = append(pairs, lexicalPair{pattern: pattern, weight: term.Weight})
		match = append(match, pattern)
	}
	for _, phrase := range phrases {
		pairs = append(pairs, lexicalPair{pattern: likePattern(phrase.Text), weight: phrase.Weight})
	}

	filterClause, filterArgs := chunkFilterClause(query.Filter)
	contentRows, err := r.runLexicalCandidate(ctx, chunkTextHaystack, pairs, match, query.Limit, filterClause, filterArgs)
	if err != nil {
		return nil, err
	}
	titleRows, err := r.runLexicalCandidate(ctx, titleHaystack, pairs, match, query.Limit, filterClause, filterArgs)
	if err != nil {
		return nil, err
	}
	return mergeLexicalRows(contentRows, titleRows, query.Limit), nil
}

// lexicalPair 是一个匹配模式（已经过 likePattern 转义）与它的权重。
type lexicalPair struct {
	pattern string
	weight  float64
}

// runLexicalCandidate 执行一条词法候选查询。
func (r *knowledgeSearchRepository) runLexicalCandidate(
	ctx context.Context,
	haystack string,
	scoring []lexicalPair,
	match []string,
	limit int,
	filterClause string,
	filterArgs []any,
) ([]entity.KnowledgeChunkView, error) {
	statement, args := lexicalStatement(haystack, scoring, match, limit, filterClause, filterArgs)
	var rows []entity.KnowledgeChunkView
	if err := r.db.WithContext(ctx).Raw(statement, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("词法召回查询失败: %w", err)
	}
	return rows, nil
}

// lexicalStatement 拼一条词法候选查询：命中任意词项的行按加权分降序返回。
//
// haystack 决定匹配面（切片正文+章节标题，或文档标题），两条查询共用同一套 CASE
// 打分与参数顺序。单独成函数是为了让用例能 EXPLAIN **真实的**语句 —— 索引用不用
// 得上全靠它，抄一份到测试里就成安慰剂了（见 TestSearchLexicalCanUseIndex）。
//
// 两处必须分开写，不能图省事共用一份表达式：**WHERE 里必须是普通的 ILIKE 之 OR**，
// 把打分的 CASE 复制进 WHERE（写成 `CASE ... > 0`）规划器就识别不出可以走索引的
// 条件，表达式索引静默失效 —— 这条是 EXPLAIN 用例抓出来的。
//
// scoring 与 match 也是因此分开的：scoring 含短语（只加分），match 只放词项
// （准入条件）。短语命中必然蕴含词项命中（短语由词项所在的片段拼成），
// 所以准入只看词项与"短语只加分"的语义完全一致。
//
// 占位符顺序：SELECT 里的 (模式, 权重) 对 → WHERE 里的状态 →
// WHERE 里的准入模式 → 过滤参数。过滤条件拼在 WHERE 末尾，参数也必须跟着排在最后。
func lexicalStatement(
	haystack string,
	scoring []lexicalPair,
	match []string,
	limit int,
	filterClause string,
	filterArgs []any,
) (string, []any) {
	cases := make([]string, 0, len(scoring))
	selectArgs := make([]any, 0, len(scoring)*2)
	for _, pair := range scoring {
		cases = append(cases, fmt.Sprintf("CASE WHEN %s ILIKE ? THEN ?::float8 ELSE 0 END", haystack))
		selectArgs = append(selectArgs, pair.pattern, pair.weight)
	}
	hits := "(" + strings.Join(cases, " + ") + ")"

	matchClauses := make([]string, 0, len(match))
	for range match {
		matchClauses = append(matchClauses, haystack+" ILIKE ?")
	}
	matchClause := "(" + strings.Join(matchClauses, " OR ") + ")"

	statement := fmt.Sprintf(`
SELECT %s,
	%s AS raw_score
FROM knowledge_chunks c
JOIN knowledge_documents d ON d.id = c.document_id
WHERE d.enabled
	AND d.status = ?
	AND %s%s
ORDER BY raw_score DESC, c.id ASC
LIMIT %d`,
		chunkViewColumns, hits, matchClause, filterClause, limit)

	args := make([]any, 0, len(selectArgs)+1+len(match)+len(filterArgs))
	args = append(args, selectArgs...)
	args = append(args, entity.KnowledgeDocumentStatusReady)
	for _, pattern := range match {
		args = append(args, pattern)
	}
	args = append(args, filterArgs...)
	return statement, args
}

// mergeLexicalRows 合并正文/标题两条候选：同一个切片取较高分。
//
// 取最大值而不是相加，是为了保持改造前"拼成一段再匹配"的语义：一个词项在正文与
// 标题各出现一次，也只算命中一次。再按分数降序、切片 ID 升序截断 —— 与单条 SQL 里
// ORDER BY raw_score DESC, c.id ASC 同序，同一句检索词的结果可复算。
func mergeLexicalRows(content, title []entity.KnowledgeChunkView, limit int) []entity.KnowledgeChunkView {
	if len(title) == 0 {
		return content
	}

	merged := make(map[uint64]entity.KnowledgeChunkView, len(content)+len(title))
	for _, row := range content {
		merged[row.ChunkID] = row
	}
	for _, row := range title {
		if existing, ok := merged[row.ChunkID]; !ok || row.RawScore > existing.RawScore {
			merged[row.ChunkID] = row
		}
	}

	rows := make([]entity.KnowledgeChunkView, 0, len(merged))
	for _, row := range merged {
		rows = append(rows, row)
	}
	slices.SortFunc(rows, func(left, right entity.KnowledgeChunkView) int {
		if difference := cmp.Compare(right.RawScore, left.RawScore); difference != 0 {
			return difference
		}
		return cmp.Compare(left.ChunkID, right.ChunkID)
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows
}

// cleanLexicalTerms 去掉空词项并校验权重。
//
// 权重非正是本层的硬约定（见 entity.KnowledgeLexicalTerm）：0 会让词项只贡献
// "入选资格"而不贡献分数，负权重更是会把命中往下压，都是调用方没想清楚，
// 与其静默修正不如当场报错。
func cleanLexicalTerms(terms []entity.KnowledgeLexicalTerm) ([]entity.KnowledgeLexicalTerm, error) {
	out := make([]entity.KnowledgeLexicalTerm, 0, len(terms))
	for _, term := range terms {
		text := strings.TrimSpace(term.Text)
		if text == "" {
			continue
		}
		if term.Weight <= 0 {
			return nil, fmt.Errorf("词法词项权重非法: %q → %v（必须为正）", text, term.Weight)
		}
		out = append(out, entity.KnowledgeLexicalTerm{Text: text, Weight: term.Weight})
	}
	return out, nil
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

// ---------------------------------------------------------------------------
// 上下文装配的只读查询（见 KnowledgeSearchRepository 接口上的说明）
// ---------------------------------------------------------------------------

// listChunkTexts 是三个装配查询的公共实现：条件不同，投影与底线过滤完全相同。
//
// 条件由本包的三个包装方法拼好（都是固定字符串，没有外部输入），参数跟在状态值后面。
// 只取序号与正文：装配不需要标题、来源、得分那些召回字段，也就不 JOIN 文档表的其它列。
func (r *knowledgeSearchRepository) listChunkTexts(ctx context.Context, condition string, args ...any) ([]entity.KnowledgeChunkText, error) {
	statement := `
SELECT c.chunk_index, c.content
FROM knowledge_chunks c
JOIN knowledge_documents d ON d.id = c.document_id
WHERE d.enabled
	AND d.status = ?
	AND ` + condition + `
ORDER BY c.chunk_index ASC`

	queryArgs := make([]any, 0, len(args)+1)
	queryArgs = append(queryArgs, entity.KnowledgeDocumentStatusReady)
	queryArgs = append(queryArgs, args...)

	var rows []entity.KnowledgeChunkText
	if err := r.db.WithContext(ctx).Raw(statement, queryArgs...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("读取装配切片失败: %w", err)
	}
	return rows, nil
}

// ListSectionChunkTexts 取一个节的全部切片。参数非法时返回空而不是报错：
// 装配层在空路径时根本不会调它，这里只是兜底。
func (r *knowledgeSearchRepository) ListSectionChunkTexts(ctx context.Context, documentID uint64, sectionPath string) ([]entity.KnowledgeChunkText, error) {
	if documentID == 0 || strings.TrimSpace(sectionPath) == "" {
		return nil, nil
	}
	return r.listChunkTexts(ctx, "c.document_id = ? AND c.section_path = ?", documentID, sectionPath)
}

// ListSymbolChunkTexts 取一个代码符号的全部切片。
func (r *knowledgeSearchRepository) ListSymbolChunkTexts(ctx context.Context, documentID uint64, symbol string) ([]entity.KnowledgeChunkText, error) {
	if documentID == 0 || strings.TrimSpace(symbol) == "" {
		return nil, nil
	}
	return r.listChunkTexts(ctx, "c.document_id = ? AND c.symbol = ?", documentID, symbol)
}

// ListChunkTextWindow 取序号区间内的切片；from > to 时返回空（非法区间没有意义）。
func (r *knowledgeSearchRepository) ListChunkTextWindow(ctx context.Context, documentID uint64, from, to int32) ([]entity.KnowledgeChunkText, error) {
	if documentID == 0 || from > to {
		return nil, nil
	}
	return r.listChunkTexts(ctx, "c.document_id = ? AND c.chunk_index BETWEEN ? AND ?", documentID, from, to)
}
