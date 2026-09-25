package repository

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"narra/internal/model/entity"
)

// 这些用例必须跑真库：两条召回路的正确性全在 SQL 里 —— pgvector 的余弦算子与
// 表达式索引、ILIKE 的转义与大小写、按命中词项数排序，这些用替身测等于没测。
//
// 向量索引（EnsureVectorIndex）是 DDL，不能跑在事务里，所以它单独用 openTestDB +
// 显式清理，其余用例仍然用 testTx 回滚，不往开发库里留任何数据。
//
// 本地运行：
//
//	NARRA_TEST_DSN="host=localhost port=5432 user=postgres password=*** dbname=narra sslmode=disable" \
//	    go test ./internal/repository/ -run Search -v
const searchTestDimensions = 4

// seedSearchDocument 造一篇指定状态与启用标记的文档。
func seedSearchDocument(t *testing.T, tx *gorm.DB, title string, status string, enabled bool) uint64 {
	t.Helper()

	var document struct {
		ID uint64
	}
	err := tx.Raw(`
		INSERT INTO knowledge_documents (created_at, updated_at, title, content, source_type, enabled, status, metadata)
		VALUES (now(), now(), ?, ?, 'import', ?, ?, '{}'::jsonb)
		RETURNING id`,
		title, "原文正文", enabled, status).Scan(&document).Error
	if err != nil {
		t.Fatalf("插入测试文档失败: %v", err)
	}
	return document.ID
}

// seedSearchChunk 造一个切片与它在指定模型下的向量，返回切片 ID。
//
// 向量由调用方给具体数值（而不是全 0）：余弦相似度在零向量上没有定义，
// 全 0 的数据会让"谁更近"这类断言变成碰运气。
func seedSearchChunk(
	t *testing.T,
	tx *gorm.DB,
	documentID, modelID uint64,
	index int,
	heading, content, vector string,
) uint64 {
	t.Helper()

	var chunk struct {
		ID uint64
	}
	err := tx.Raw(`
		INSERT INTO knowledge_chunks (created_at, updated_at, document_id, chunk_index, heading, content, character_count)
		VALUES (now(), now(), ?, ?, ?, ?, ?)
		RETURNING id`,
		documentID, index, heading, content, len([]rune(content))).Scan(&chunk).Error
	if err != nil {
		t.Fatalf("插入测试切片失败: %v", err)
	}

	err = tx.Exec(`
		INSERT INTO knowledge_embeddings (created_at, chunk_id, model_id, dimensions, embedding, generated_at)
		VALUES (now(), ?, ?, ?, ?::vector, now())`,
		chunk.ID, modelID, searchTestDimensions, vector).Error
	if err != nil {
		t.Fatalf("插入测试向量失败: %v", err)
	}
	return chunk.ID
}

// TestSearchVectorRanksByCosineSimilarity 校验向量路按余弦相似度降序返回，
// 且带上把切片放回原文所需的定位信息（标题、章节、来源）。
func TestSearchVectorRanksByCosineSimilarity(t *testing.T) {
	tx := testTx(t)
	modelID := knowledgeTestModelID(t, tx)
	documentID := seedSearchDocument(t, tx, "向量检索调研", entity.KnowledgeDocumentStatusReady, true)

	nearest := seedSearchChunk(t, tx, documentID, modelID, 0, "排序", "讲余弦排序的那一篇", "[0,1,0,0]")
	middle := seedSearchChunk(t, tx, documentID, modelID, 1, "召回", "沾一点边的那一篇", "[0,0.7,0.7,0]")
	farthest := seedSearchChunk(t, tx, documentID, modelID, 2, "", "毫不相干的那一篇", "[1,0,0,0]")

	repo := NewKnowledgeSearchRepository(tx)
	rows, err := repo.SearchVector(context.Background(), entity.KnowledgeVectorQuery{
		ModelID:    modelID,
		Dimensions: searchTestDimensions,
		Vector:     "[0,1,0,0]",
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("向量召回失败: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("应当召回 3 条，实际 %d 条", len(rows))
	}

	got := []uint64{rows[0].ChunkID, rows[1].ChunkID, rows[2].ChunkID}
	want := []uint64{nearest, middle, farthest}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("相似度排序不对: %v（期望 %v）", got, want)
		}
	}
	if rows[0].RawScore <= rows[1].RawScore {
		t.Fatalf("相似度应当降序: %v", rows)
	}
	if rows[0].DocumentTitle != "向量检索调研" || rows[0].Heading == nil || *rows[0].Heading != "排序" {
		t.Fatalf("召回应当带上文档标题与章节: %+v", rows[0])
	}
}

// TestSearchVectorHidesUnsearchableChunks 校验召回的底线过滤：
// 别的模型的向量（语义空间不同，不可比）、停用文档的切片、还没 ready 的文档 ——
// 一个都不能被召回。
func TestSearchVectorHidesUnsearchableChunks(t *testing.T) {
	tx := testTx(t)
	modelID := knowledgeTestModelID(t, tx)
	otherModelID := knowledgeTestModelID(t, tx)

	documentID := seedSearchDocument(t, tx, "可检索", entity.KnowledgeDocumentStatusReady, true)
	visible := seedSearchChunk(t, tx, documentID, modelID, 0, "", "这一条能被召回", "[1,0,0,0]")

	otherDocument := seedSearchDocument(t, tx, "别的模型", entity.KnowledgeDocumentStatusReady, true)
	seedSearchChunk(t, tx, otherDocument, otherModelID, 0, "", "别的模型的向量", "[1,0,0,0]")

	disabled := seedSearchDocument(t, tx, "已停用", entity.KnowledgeDocumentStatusReady, false)
	seedSearchChunk(t, tx, disabled, modelID, 0, "", "停用文档的切片", "[1,0,0,0]")

	pending := seedSearchDocument(t, tx, "还没好", entity.KnowledgeDocumentStatusProcessing, true)
	seedSearchChunk(t, tx, pending, modelID, 0, "", "处理中的切片", "[1,0,0,0]")

	repo := NewKnowledgeSearchRepository(tx)
	rows, err := repo.SearchVector(context.Background(), entity.KnowledgeVectorQuery{
		ModelID:    modelID,
		Dimensions: searchTestDimensions,
		Vector:     "[1,0,0,0]",
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("向量召回失败: %v", err)
	}
	if len(rows) != 1 || rows[0].ChunkID != visible {
		t.Fatalf("只有同模型下、已启用且 ready 的切片能被召回，实际 %+v", rows)
	}
}

// searchTokenSequence 让同一时刻的两次取号也不重复。
// 光靠时间戳不够：Windows 的时钟粒度下连续两次 Now() 可能拿到同一个值，
// 于是两个"唯一"词项变成同一个，命中数的断言就跟着错位。
var searchTokenSequence atomic.Uint64

// searchTestToken 造一个每次运行都不重复的词项。
//
// 词法匹配是**全库**的子串匹配（检索本来就该全库），用例的断言却只想谈自己造的那几行 ——
// 开发库里躺着项目自己的文档，写死 "knowledge" 这种词就会连它们一起命中，
// 断言随机变红。唯一的词项把这件事一次性解决。
//
// 固定宽度的序号放在末尾还保证了另一个性质：两个词项互不为子串
// （否则搜短的那个会把长的那个一起命中，"命中词项数"就没法断言了）。
func searchTestToken() string {
	return fmt.Sprintf("narra%dq%06dxk", time.Now().UnixNano(), searchTokenSequence.Add(1))
}

// TestSearchLexicalRanksByMatchedTerms 校验词法路按命中词项数降序，
// 且匹配大小写不敏感、切片正文与文档标题都在命中面上。
func TestSearchLexicalRanksByMatchedTerms(t *testing.T) {
	tx := testTx(t)
	modelID := knowledgeTestModelID(t, tx)

	firstToken := searchTestToken()
	secondToken := searchTestToken()
	titleToken := searchTestToken()
	documentID := seedSearchDocument(t, tx, "周报 "+titleToken, entity.KnowledgeDocumentStatusReady, true)

	both := seedSearchChunk(t, tx, documentID, modelID, 0, "进展",
		"同时提到 "+firstToken+" 与 "+secondToken+" 的那一篇", vectorLiteral(searchTestDimensions))
	one := seedSearchChunk(t, tx, documentID, modelID, 1, "",
		"只提到 "+secondToken+" 的那一篇", vectorLiteral(searchTestDimensions))

	repo := NewKnowledgeSearchRepository(tx)
	rows, err := repo.SearchLexical(context.Background(), []string{firstToken, secondToken}, 10)
	if err != nil {
		t.Fatalf("词法召回失败: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("应当只召回造出来的 2 条，实际 %d 条: %+v", len(rows), rows)
	}
	if rows[0].ChunkID != both || rows[1].ChunkID != one {
		t.Fatalf("命中词项多的应当排前面: %+v", rows)
	}
	if rows[0].RawScore != 2 || rows[1].RawScore != 1 {
		t.Fatalf("命中数不对: %v / %v", rows[0].RawScore, rows[1].RawScore)
	}

	// 标题也算命中面：把词项换成小写，顺带验一次大小写不敏感。
	byTitle, err := repo.SearchLexical(context.Background(), []string{strings.ToLower(titleToken)}, 10)
	if err != nil {
		t.Fatalf("词法召回失败: %v", err)
	}
	if len(byTitle) != 2 {
		t.Fatalf("文档标题里的词项应当能命中这篇文档的全部切片，实际 %+v", byTitle)
	}
}

// TestSearchLexicalTreatsWildcardsAsLiterals 校验 % 与 _ 是用户的正文而不是通配符 ——
// 漏掉转义时，搜 "narra123%" 会把 "narra123 没有百分号" 也命中，
// 表现为"检索结果莫名其妙多了一堆不相干的"。
func TestSearchLexicalTreatsWildcardsAsLiterals(t *testing.T) {
	tx := testTx(t)
	modelID := knowledgeTestModelID(t, tx)
	documentID := seedSearchDocument(t, tx, "转义", entity.KnowledgeDocumentStatusReady, true)

	token := searchTestToken()
	withPercent := seedSearchChunk(t, tx, documentID, modelID, 0, "",
		"完成率 "+token+"% 的那一篇", vectorLiteral(searchTestDimensions))
	withoutPercent := seedSearchChunk(t, tx, documentID, modelID, 1, "",
		token+" 没有百分号的那一篇", vectorLiteral(searchTestDimensions))

	repo := NewKnowledgeSearchRepository(tx)
	rows, err := repo.SearchLexical(context.Background(), []string{token + "%"}, 10)
	if err != nil {
		t.Fatalf("词法召回失败: %v", err)
	}
	if len(rows) != 1 || rows[0].ChunkID != withPercent {
		t.Fatalf("%% 应当按字面量匹配，实际 %+v", rows)
	}

	// 下划线同理：它是 LIKE 的单字符通配符，漏掉转义就会少一堵墙。
	rows, err = repo.SearchLexical(context.Background(), []string{token + "_"}, 10)
	if err != nil {
		t.Fatalf("词法召回失败: %v", err)
	}
	for _, row := range rows {
		if row.ChunkID == withoutPercent {
			t.Fatalf("_ 应当按字面量匹配，实际命中了不含该符号的切片: %+v", rows)
		}
	}
}

// TestSearchLexicalSkipsUnsearchableChunks 校验词法路与向量路共用同一条底线过滤。
func TestSearchLexicalSkipsUnsearchableChunks(t *testing.T) {
	tx := testTx(t)
	modelID := knowledgeTestModelID(t, tx)
	token := searchTestToken()

	documentID := seedSearchDocument(t, tx, "可检索", entity.KnowledgeDocumentStatusReady, true)
	visible := seedSearchChunk(t, tx, documentID, modelID, 0, "", "命中词项 "+token, vectorLiteral(searchTestDimensions))

	disabled := seedSearchDocument(t, tx, "已停用", entity.KnowledgeDocumentStatusReady, false)
	seedSearchChunk(t, tx, disabled, modelID, 0, "", "命中词项 "+token, vectorLiteral(searchTestDimensions))

	pending := seedSearchDocument(t, tx, "还没好", entity.KnowledgeDocumentStatusPending, true)
	seedSearchChunk(t, tx, pending, modelID, 0, "", "命中词项 "+token, vectorLiteral(searchTestDimensions))

	repo := NewKnowledgeSearchRepository(tx)
	rows, err := repo.SearchLexical(context.Background(), []string{token}, 10)
	if err != nil {
		t.Fatalf("词法召回失败: %v", err)
	}
	if len(rows) != 1 || rows[0].ChunkID != visible {
		t.Fatalf("只有已启用且 ready 的切片能被召回，实际 %+v", rows)
	}
}

// TestSearchLexicalWithoutTermsSkipsDatabase 校验没有词项时直接返回空 ——
// 空词项拼出来的 SQL 会退化成"取任意 limit 行"，那是纯粹的噪声。
func TestSearchLexicalWithoutTermsSkipsDatabase(t *testing.T) {
	tx := testTx(t)
	repo := NewKnowledgeSearchRepository(tx)

	rows, err := repo.SearchLexical(context.Background(), nil, 10)
	if err != nil {
		t.Fatalf("词法召回失败: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("没有词项时不该返回任何候选: %+v", rows)
	}
}

// TestSearchVectorCanUseHNSWIndex 校验检索 SQL 的形状与 HNSW 索引定义一致 ——
// 表达式里的 CAST、部分索引的谓词（模型 ID 必须是字面量）、算子类，
// 任何一处写法变了索引就**静默失效**、退回顺序扫描，没有任何报错。
// 所以用 EXPLAIN 把"用得上索引"变成一条断言，而不是留给数据量变大时慢慢发现。
//
// 小表上规划器当然选顺序扫描，所以关掉排序与顺序扫描，逼它必须从索引里取有序结果。
// SET LOCAL 的作用域正好是这条用例的事务；探针索引也在事务里，回滚后不留痕迹。
func TestSearchVectorCanUseHNSWIndex(t *testing.T) {
	tx := testTx(t)
	modelID := knowledgeTestModelID(t, tx)
	documentID := seedSearchDocument(t, tx, "索引探针", entity.KnowledgeDocumentStatusReady, true)
	seedSearchChunk(t, tx, documentID, modelID, 0, "", "探针切片", "[1,0,0,0]")

	name := "narra_test_hnsw_probe_idx"
	statement := fmt.Sprintf(
		"CREATE INDEX %s ON knowledge_embeddings USING hnsw ((embedding::vector(%d)) vector_cosine_ops) WHERE model_id = %d",
		name, searchTestDimensions, modelID)
	if err := tx.Exec(statement).Error; err != nil {
		t.Fatalf("建探针索引失败: %v", err)
	}
	if err := tx.Exec("SET LOCAL enable_seqscan = off; SET LOCAL enable_sort = off").Error; err != nil {
		t.Fatalf("调整执行计划开关失败: %v", err)
	}

	query := entity.KnowledgeVectorQuery{
		ModelID:    modelID,
		Dimensions: searchTestDimensions,
		Vector:     "[1,0,0,0]",
		Limit:      5,
	}
	args := []any{query.Vector, entity.KnowledgeDocumentStatusReady, query.Vector}
	rows, err := tx.Raw("EXPLAIN "+vectorSearchStatement(query.Dimensions, query.ModelID, query.Limit), args...).Rows()
	if err != nil {
		t.Fatalf("EXPLAIN 失败: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("读取执行计划失败: %v", err)
		}
		plan.WriteString(line)
		plan.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("读取执行计划失败: %v", err)
	}

	if !strings.Contains(plan.String(), name) {
		t.Fatalf("检索用不上向量索引，会退化成顺序扫描。执行计划:\n%s", plan.String())
	}
}

// TestEnsureVectorIndexExplainsDimensionCeiling 校验超过 pgvector HNSW 维度上限时
// 给的是一句人话（结果不受影响、只是会慢），而不是数据库那句 "column cannot have
// more than 2000 dimensions for hnsw index" —— 后者看上去像哪条 SQL 写错了。
func TestEnsureVectorIndexExplainsDimensionCeiling(t *testing.T) {
	db := openTestDB(t)
	model := &entity.EmbeddingModel{
		BaseModel:  entity.BaseModel{ID: uint64(time.Now().UnixNano())},
		Name:       uniqueName("dims"),
		Dimensions: maxHNSWDimensions + 1,
	}

	err := NewEmbeddingModelRepository(db).EnsureVectorIndex(context.Background(), model)
	if err == nil {
		t.Fatalf("超过 %d 维时不该建出索引", maxHNSWDimensions)
	}
	for _, want := range []string{"HNSW", "顺序扫描"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("错误说明里应当提到 %q，实际是 %v", want, err)
		}
	}
	// 调用方要靠这个哨兵值把"模型维度就这样"与"建索引真失败了"分开：
	// 前者提示（Info），后者告警（Warn）。
	if !errors.Is(err, ErrVectorIndexUnsupported) {
		t.Fatalf("错误应当包装 ErrVectorIndexUnsupported: %v", err)
	}
}

// TestVectorIndexDisposition 校验同名索引的处置规则 —— 尤其是**无效索引必须重建**：
// 一次建索引失败留下的空壳会占着名字，让之后的 `IF NOT EXISTS` 永远跳过，
// 索引再也建不出来且没有任何报错。真库里造这种索引很麻烦，所以规则被提成了纯函数。
func TestVectorIndexDisposition(t *testing.T) {
	const dimensions = int32(1536)
	example := "CREATE INDEX x ON public.knowledge_embeddings USING hnsw (((embedding)::vector(1536)) vector_cosine_ops) WHERE (model_id = 6)"
	outdated := "CREATE INDEX x ON public.knowledge_embeddings USING hnsw (((embedding)::vector(3072)) vector_cosine_ops) WHERE (model_id = 6)"

	cases := []struct {
		name       string
		definition string
		valid      bool
		want       vectorIndexAction
	}{
		{"没有索引时新建", "", false, indexActionCreate},
		{"有效且维度一致的索引保留", example, true, indexActionKeep},
		{"维度过期的索引要重建", outdated, true, indexActionRebuild},
		{"无效索引即使维度一致也要重建", example, false, indexActionRebuild},
		{"无效且维度过期同样重建", outdated, false, indexActionRebuild},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := vectorIndexDisposition(testCase.definition, testCase.valid, dimensions); got != testCase.want {
				t.Fatalf("处置判断不对: got=%d want=%d", got, testCase.want)
			}
		})
	}
}

// TestEnsureVectorIndexCreatesAndIsIdempotent 校验索引建得出来、重复建无副作用，
// 且索引定义与检索 SQL 的表达式一致（不一致就永远用不上索引，而这没有任何报错）。
//
// 这条用例是 DDL，不走事务：CREATE INDEX CONCURRENTLY 不能跑在事务块里。
// 索引挂在**不存在的模型 ID** 上（部分索引的谓词不要求那行数据存在），
// 于是整条用例不往任何表里写数据；索引本身在用例结束时显式删掉。
func TestEnsureVectorIndexCreatesAndIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	model := &entity.EmbeddingModel{
		BaseModel:  entity.BaseModel{ID: uint64(time.Now().UnixNano())},
		Dimensions: searchTestDimensions,
	}
	name := vectorIndexName(model.ID)
	t.Cleanup(func() {
		_ = db.Exec("DROP INDEX CONCURRENTLY IF EXISTS " + name).Error
	})

	// 取具体类型是为了读 unexported 的 currentVectorIndexDef（同包可见）——
	// 索引定义对不对是这条用例的核心断言，值得直接问实现而不是自己再写一遍查询。
	repo := NewEmbeddingModelRepository(db).(*embeddingModelRepository)
	if err := repo.EnsureVectorIndex(context.Background(), model); err != nil {
		t.Fatalf("建向量索引失败: %v", err)
	}

	definition, valid, err := repo.currentVectorIndex(context.Background(), name)
	if err != nil {
		t.Fatalf("查询索引定义失败: %v", err)
	}
	if !valid {
		t.Fatalf("刚建好的索引应当是有效的: %q", definition)
	}
	if !strings.Contains(definition, "hnsw") || !strings.Contains(definition, "vector_cosine_ops") {
		t.Fatalf("索引应当是 HNSW 余弦索引: %q", definition)
	}
	if !strings.Contains(definition, "vector("+strconv.Itoa(searchTestDimensions)+")") {
		t.Fatalf("索引维度与模型登记值不一致，检索永远用不上它: %q", definition)
	}

	if err := repo.EnsureVectorIndex(context.Background(), model); err != nil {
		t.Fatalf("重复建索引应当是幂等的: %v", err)
	}
	after, _, err := repo.currentVectorIndex(context.Background(), name)
	if err != nil {
		t.Fatalf("查询索引定义失败: %v", err)
	}
	if after != definition {
		t.Fatalf("幂等重建不该改索引定义: %q vs %q", after, definition)
	}
}

// TestEnsureVectorIndexRebuildsOnDimensionChange 校验维度自愈：
// 模型的维度改过之后，旧索引挂在旧表达式上永远不会被检索用上，必须删掉重建。
func TestEnsureVectorIndexRebuildsOnDimensionChange(t *testing.T) {
	db := openTestDB(t)
	model := &entity.EmbeddingModel{
		BaseModel:  entity.BaseModel{ID: uint64(time.Now().UnixNano())},
		Dimensions: searchTestDimensions,
	}
	name := vectorIndexName(model.ID)
	t.Cleanup(func() {
		_ = db.Exec("DROP INDEX CONCURRENTLY IF EXISTS " + name).Error
	})

	repo := NewEmbeddingModelRepository(db).(*embeddingModelRepository)
	if err := repo.EnsureVectorIndex(context.Background(), model); err != nil {
		t.Fatalf("建向量索引失败: %v", err)
	}

	model.Dimensions = searchTestDimensions * 2
	if err := repo.EnsureVectorIndex(context.Background(), model); err != nil {
		t.Fatalf("按新维度重建失败: %v", err)
	}

	definition, _, err := repo.currentVectorIndex(context.Background(), name)
	if err != nil {
		t.Fatalf("查询索引定义失败: %v", err)
	}
	if !strings.Contains(definition, "vector("+strconv.Itoa(searchTestDimensions*2)+")") {
		t.Fatalf("索引应当按新维度重建: %q", definition)
	}
}

// itoa 是 strconv.Itoa 的最小替身，只为让上面的断言读起来紧凑。
