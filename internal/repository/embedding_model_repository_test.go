package repository

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"narra/internal/model/entity"
)

// 这些用例需要真实的 PostgreSQL：向量列是 pgvector 的 vector 类型，
// 唯一性还牵涉部分唯一索引，用 sqlite 或 mock 都测不出真实行为。
// 没配置 DSN 时整体跳过，与 pkg/documentparser 在没有 Python 环境时跳过保持一致。
//
// 本地运行：
//
//	NARRA_TEST_DSN="host=localhost port=5432 user=postgres password=*** dbname=narra sslmode=disable" \
//	    go test ./internal/repository/ -run EmbeddingModel -v
func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := strings.TrimSpace(os.Getenv("NARRA_TEST_DSN"))
	if dsn == "" {
		t.Skip("未设置 NARRA_TEST_DSN，跳过需要 PostgreSQL 的仓储测试")
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("连接测试数据库失败: %v", err)
	}
	return db
}

// testTx 开一个用完就回滚的事务。
// 用例可以随意造数据而不污染开发库 —— 这类测试最怕的就是把开发库改花。
func testTx(t *testing.T) *gorm.DB {
	t.Helper()

	tx := openTestDB(t).Begin()
	if tx.Error != nil {
		t.Fatalf("开启事务失败: %v", tx.Error)
	}
	t.Cleanup(func() { _ = tx.Rollback().Error })
	return tx
}

// uniqueName 给每次运行造互不相同的模型名。
// 模型名上有全局唯一索引，用固定名字会和开发库里的真实数据撞车。
func uniqueName(suffix string) string {
	return fmt.Sprintf("narra-test-%d-%s", time.Now().UnixNano(), suffix)
}

func model(name string, dimensions int) entity.EmbeddingModel {
	baseURL := "https://example.test/v1"
	return entity.EmbeddingModel{
		Name:       name,
		Provider:   "openai-compatible",
		BaseURL:    &baseURL,
		Dimensions: int32(dimensions),
	}
}

// countDefaults 数当前有几个默认模型。任何时刻都必须 ≤ 1。
func countDefaults(t *testing.T, tx *gorm.DB) int64 {
	t.Helper()

	var count int64
	if err := tx.Raw("SELECT count(*) FROM embedding_models WHERE is_default").Scan(&count).Error; err != nil {
		t.Fatalf("统计默认模型数量失败: %v", err)
	}
	return count
}

// seedVector 造一份"文档 + 切片 + 向量"的最小数据，用于验证"有向量后维度不可改"。
//
// 全部用原生 SQL：knowledge_embeddings.embedding 是 vector 类型，实体层刻意用字符串
// 承载它（见 entity.KnowledgeEmbedding 注释），而 created_at / updated_at 这类列由
// AutoMigrate 建成了 NOT NULL 且没有默认值，必须在 INSERT 里显式给。
// 向量按模型的真实维度生成，这样将来 migrations/0002 的
// CHECK (vector_dims(embedding) = dimensions) 生效后这些用例仍然成立。
func seedVector(t *testing.T, tx *gorm.DB, modelID uint64, dimensions int) {
	t.Helper()

	var document struct {
		ID uint64
	}
	err := tx.Raw(`
		INSERT INTO knowledge_documents (created_at, updated_at, title, content, source_type, enabled, metadata)
		VALUES (now(), now(), ?, ?, 'import', true, '{}'::jsonb)
		RETURNING id`,
		"桥接测试文档", "正文内容").Scan(&document).Error
	if err != nil {
		t.Fatalf("插入测试文档失败: %v", err)
	}

	var chunk struct {
		ID uint64
	}
	err = tx.Raw(`
		INSERT INTO knowledge_chunks (created_at, updated_at, document_id, chunk_index, content, character_count, metadata)
		VALUES (now(), now(), ?, 0, ?, ?, '{}'::jsonb)
		RETURNING id`,
		document.ID, "正文内容", len([]rune("正文内容"))).Scan(&chunk).Error
	if err != nil {
		t.Fatalf("插入测试切片失败: %v", err)
	}

	err = tx.Exec(`
		INSERT INTO knowledge_embeddings (created_at, chunk_id, model_id, dimensions, embedding, generated_at)
		VALUES (now(), ?, ?, ?, ?::vector, now())`,
		chunk.ID, modelID, dimensions, vectorLiteral(dimensions)).Error
	if err != nil {
		t.Fatalf("插入测试向量失败: %v", err)
	}
}

// vectorLiteral 生成 [0,0,...] 形式的向量字面量，长度与维度一致。
func vectorLiteral(dimensions int) string {
	values := make([]string, dimensions)
	for i := range values {
		values[i] = "0"
	}
	return "[" + strings.Join(values, ",") + "]"
}

func TestEmbeddingModelEnsureDefaultCreatesSoleDefault(t *testing.T) {
	tx := testTx(t)
	repo := NewEmbeddingModelRepository(tx)
	ctx := context.Background()

	name := uniqueName("create")
	input := model(name, 1536)

	created, err := repo.EnsureDefault(ctx, input)
	if err != nil {
		t.Fatalf("首次登记模型失败: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("登记后应当带回主键，知识库要靠它写 model_id")
	}
	if !created.IsDefault {
		t.Error("登记的模型必须是默认模型")
	}
	if !created.Enabled {
		t.Error("登记的模型必须是启用状态")
	}
	if created.Dimensions != 1536 {
		t.Errorf("维度 = %d, 期望 1536", created.Dimensions)
	}
	if created.BaseURL == nil || *created.BaseURL != "https://example.test/v1" {
		t.Errorf("base_url 应当回填配置里的地址，实际 %v", created.BaseURL)
	}
	if got := countDefaults(t, tx); got != 1 {
		t.Errorf("默认模型数量 = %d, 期望 1", got)
	}

	// 再登记一次同名模型：必须复用原行，不能插出第二行
	again, err := repo.EnsureDefault(ctx, input)
	if err != nil {
		t.Fatalf("重复登记同名模型失败: %v", err)
	}
	if again.ID != created.ID {
		t.Errorf("同名模型应当复用同一行，ID %d → %d", created.ID, again.ID)
	}
	if got := countDefaults(t, tx); got != 1 {
		t.Errorf("重复登记后默认模型数量 = %d, 期望 1", got)
	}
}

func TestEmbeddingModelEnsureDefaultMovesDefaultAndKeepsOldRow(t *testing.T) {
	tx := testTx(t)
	repo := NewEmbeddingModelRepository(tx)
	ctx := context.Background()

	firstName, secondName := uniqueName("first"), uniqueName("second")
	first := model(firstName, 1536)
	second := model(secondName, 1024)

	saved, err := repo.EnsureDefault(ctx, first)
	if err != nil {
		t.Fatalf("登记第一个模型失败: %v", err)
	}
	if _, err := repo.EnsureDefault(ctx, second); err != nil {
		t.Fatalf("登记第二个模型失败: %v", err)
	}

	// 换模型时旧行必须留着：它名下的向量还在，model_id 外键是 ON DELETE RESTRICT，
	// 删了会连带把这些向量变成孤儿。
	var reloaded entity.EmbeddingModel
	if err := tx.Where("id = ?", saved.ID).First(&reloaded).Error; err != nil {
		t.Fatalf("旧模型行应当保留，查询失败: %v", err)
	}
	if reloaded.IsDefault {
		t.Error("旧模型行应当退出默认，否则检索时不知道用哪个模型")
	}
	if got := countDefaults(t, tx); got != 1 {
		t.Errorf("默认模型数量 = %d, 期望恰好 1 个", got)
	}
}

func TestEmbeddingModelEnsureDefaultUpdatesDimensionsWithoutVectors(t *testing.T) {
	tx := testTx(t)
	repo := NewEmbeddingModelRepository(tx)
	ctx := context.Background()

	name := uniqueName("dims")
	first := model(name, 1536)
	saved, err := repo.EnsureDefault(ctx, first)
	if err != nil {
		t.Fatalf("首次登记失败: %v", err)
	}

	// 还没有任何向量，改维度不留后患，应当就地更新而不是报错
	changed := model(name, 4096)
	updated, err := repo.EnsureDefault(ctx, changed)
	if err != nil {
		t.Fatalf("无向量时改维度应当被允许，实际报错: %v", err)
	}
	if updated.ID != saved.ID {
		t.Errorf("应当复用原行，ID %d → %d", saved.ID, updated.ID)
	}
	if updated.Dimensions != 4096 {
		t.Errorf("维度 = %d, 期望更新为 4096", updated.Dimensions)
	}
}

func TestEmbeddingModelEnsureDefaultRejectsDimensionsChangeWithVectors(t *testing.T) {
	tx := testTx(t)
	repo := NewEmbeddingModelRepository(tx)
	ctx := context.Background()

	name := uniqueName("locked")
	first := model(name, 1536)
	saved, err := repo.EnsureDefault(ctx, first)
	if err != nil {
		t.Fatalf("首次登记失败: %v", err)
	}
	seedVector(t, tx, saved.ID, 1536)

	changed := model(name, 4096)
	_, err = repo.EnsureDefault(ctx, changed)
	if err == nil {
		t.Fatal("该模型下已有向量时改维度必须被拒绝，否则新旧向量会归到同一个 model_id 下")
	}

	var mismatch *DimensionsMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("期望 *DimensionsMismatchError，实际 %T: %v", err, err)
	}
	if mismatch.Recorded != 1536 || mismatch.Requested != 4096 || mismatch.Vectors != 1 {
		t.Errorf("错误信息里的数据不对: recorded=%d requested=%d vectors=%d",
			mismatch.Recorded, mismatch.Requested, mismatch.Vectors)
	}

	// 被拒之后原行必须原封不动，不能留下"报错了但已经改了"的中间态
	var reloaded entity.EmbeddingModel
	if err := tx.Where("id = ?", saved.ID).First(&reloaded).Error; err != nil {
		t.Fatalf("查询原行失败: %v", err)
	}
	if reloaded.Dimensions != 1536 {
		t.Errorf("被拒绝后维度不应变化，实际 %d", reloaded.Dimensions)
	}
	if !reloaded.IsDefault {
		t.Error("被拒绝后默认标记不应丢失")
	}
}
