package repository

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
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

// testOwnerSequence 让同一个测试进程里的测试手机号严格递增。
// 只靠时间戳会在 Windows 上撞车（time.Now 的分辨率达不到纳秒），
// 同一个事务里连建两个用户时必须自带序号。
var testOwnerSequence atomic.Uint64

// testOwnerID 在一个事务里建一个测试用户并返回 ID。
//
// 鉴权改造后 owner_id 是 NOT NULL 且指向 users 的外键，模型行、向量模型、
// 知识文档都必须先有一个归属账号；手机号用 +8600 前缀 + 递增序号，
// 既不撞 users 的唯一索引，也不会误伤真实用户在用的号码段。
func testOwnerID(t *testing.T, tx *gorm.DB) uint64 {
	t.Helper()

	seed := time.Now().UnixNano()/1_000 + int64(testOwnerSequence.Add(1))
	phone := fmt.Sprintf("+8600%09d", seed%1_000_000_000)
	var user struct {
		ID uint64
	}
	err := tx.Raw(`
		INSERT INTO users (phone, password_hash, phone_verified_at, created_at, updated_at)
		VALUES (?, 'test-password-hash', now(), now(), now())
		RETURNING id`, phone).Scan(&user).Error
	if err != nil {
		t.Fatalf("创建测试用户失败: %v", err)
	}
	return user.ID
}

// uniqueName 给每次运行造互不相同的模型名。
// 模型名在账号内唯一（UNIQUE (owner_id, name)），用固定名字会和开发库里的真实数据撞车。
func uniqueName(suffix string) string {
	return fmt.Sprintf("narra-test-%d-%s", time.Now().UnixNano(), suffix)
}

func model(ownerID uint64, name string, dimensions int) entity.EmbeddingModel {
	return entity.EmbeddingModel{
		OwnerID:    ownerID,
		Name:       name,
		Dimensions: int32(dimensions),
	}
}

// countDefaults 数该账号当前有几个默认模型。任何时刻都必须 ≤ 1。
func countDefaults(t *testing.T, tx *gorm.DB, ownerID uint64) int64 {
	t.Helper()

	var count int64
	if err := tx.Raw("SELECT count(*) FROM embedding_models WHERE owner_id = ? AND is_default", ownerID).Scan(&count).Error; err != nil {
		t.Fatalf("统计默认模型数量失败: %v", err)
	}
	return count
}

// seedVector 造一份"文档 + 切片 + 向量"的最小数据，用于验证"有向量后维度不可改"。
//
// 全部用原生 SQL：knowledge_embeddings.embedding 是 vector 类型，实体层刻意用字符串
// 承载它（见 entity.KnowledgeEmbedding 注释），而 created_at / updated_at 这类列由
// AutoMigrate 建成了 NOT NULL 且没有默认值，必须在 INSERT 里显式给。
// 文档的 owner_id 也必须给：它是 NOT NULL + 指向 users 的外键。
//
// 向量按模型的真实维度生成，这样将来 migrations/0002 的
// CHECK (vector_dims(embedding) = dimensions) 生效后这些用例仍然成立。
func seedVector(t *testing.T, tx *gorm.DB, ownerID, modelID uint64, dimensions int) {
	t.Helper()

	var document struct {
		ID uint64
	}
	err := tx.Raw(`
		INSERT INTO knowledge_documents (created_at, updated_at, owner_id, title, content, source_type, enabled, metadata)
		VALUES (now(), now(), ?, ?, ?, 'import', true, '{}'::jsonb)
		RETURNING id`,
		ownerID, "桥接测试文档", "正文内容").Scan(&document).Error
	if err != nil {
		t.Fatalf("插入测试文档失败: %v", err)
	}

	var chunk struct {
		ID uint64
	}
	err = tx.Raw(`
		INSERT INTO knowledge_chunks (created_at, updated_at, document_id, chunk_index, content, character_count)
		VALUES (now(), now(), ?, 0, ?, ?)
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
	ownerID := testOwnerID(t, tx)

	name := uniqueName("create")
	input := model(ownerID, name, 1536)

	created, err := repo.EnsureDefault(ctx, input)
	if err != nil {
		t.Fatalf("首次登记模型失败: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("登记后应当带回主键，知识库要靠它写 model_id")
	}
	if created.OwnerID != ownerID {
		t.Errorf("模型行归属 = %d，期望 %d", created.OwnerID, ownerID)
	}
	if !created.IsDefault {
		t.Error("登记的模型必须是默认模型")
	}
	if created.Dimensions != 1536 {
		t.Errorf("维度 = %d, 期望 1536", created.Dimensions)
	}
	if got := countDefaults(t, tx, ownerID); got != 1 {
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
	if got := countDefaults(t, tx, ownerID); got != 1 {
		t.Errorf("重复登记后默认模型数量 = %d, 期望 1", got)
	}
}

// 两个账号可以有各自的默认模型与同名模型行：名字只在账号内唯一。
func TestEmbeddingModelEnsureDefaultIsScopedByOwner(t *testing.T) {
	tx := testTx(t)
	repo := NewEmbeddingModelRepository(tx)
	ctx := context.Background()
	firstOwner, secondOwner := testOwnerID(t, tx), testOwnerID(t, tx)

	name := uniqueName("shared")
	first, err := repo.EnsureDefault(ctx, model(firstOwner, name, 1536))
	if err != nil {
		t.Fatalf("账号一登记失败: %v", err)
	}
	second, err := repo.EnsureDefault(ctx, model(secondOwner, name, 1536))
	if err != nil {
		t.Fatalf("另一个账号登记同名模型失败: %v", err)
	}
	if first.ID == second.ID {
		t.Fatal("不同账号的同名模型必须是两行，不能互相复用")
	}
	if got := countDefaults(t, tx, firstOwner); got != 1 {
		t.Errorf("账号一默认模型数量 = %d, 期望 1", got)
	}
	if got := countDefaults(t, tx, secondOwner); got != 1 {
		t.Errorf("账号二默认模型数量 = %d, 期望 1", got)
	}
}

func TestEmbeddingModelEnsureDefaultMovesDefaultAndKeepsOldRow(t *testing.T) {
	tx := testTx(t)
	repo := NewEmbeddingModelRepository(tx)
	ctx := context.Background()
	ownerID := testOwnerID(t, tx)

	firstName, secondName := uniqueName("first"), uniqueName("second")
	first := model(ownerID, firstName, 1536)
	second := model(ownerID, secondName, 1024)

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
	if got := countDefaults(t, tx, ownerID); got != 1 {
		t.Errorf("默认模型数量 = %d, 期望恰好 1 个", got)
	}
}

func TestEmbeddingModelEnsureDefaultUpdatesDimensionsWithoutVectors(t *testing.T) {
	tx := testTx(t)
	repo := NewEmbeddingModelRepository(tx)
	ctx := context.Background()
	ownerID := testOwnerID(t, tx)

	name := uniqueName("dims")
	first := model(ownerID, name, 1536)
	saved, err := repo.EnsureDefault(ctx, first)
	if err != nil {
		t.Fatalf("首次登记失败: %v", err)
	}

	// 还没有任何向量，改维度不留后患，应当就地更新而不是报错
	changed := model(ownerID, name, 4096)
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
	ownerID := testOwnerID(t, tx)

	name := uniqueName("locked")
	first := model(ownerID, name, 1536)
	saved, err := repo.EnsureDefault(ctx, first)
	if err != nil {
		t.Fatalf("首次登记失败: %v", err)
	}
	seedVector(t, tx, ownerID, saved.ID, 1536)

	changed := model(ownerID, name, 4096)
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

// TestCountVectorsByModel 校验体检统计的口径：左连接让没有向量的模型计 0，
// 有向量的模型逐条计数 —— "默认模型下 0 条、其他模型下有"正是它要暴露的形态；
// 但统计只覆盖该账号自己的模型，别的账号的同名/异名模型不能混进来。
func TestCountVectorsByModel(t *testing.T) {
	tx := testTx(t)
	repo := NewEmbeddingModelRepository(tx)
	ctx := context.Background()
	ownerID := testOwnerID(t, tx)

	// 两个模型：旧模型下造向量，新模型留空（模拟"换了默认模型还没重新收录"）。
	withVectors, err := repo.EnsureDefault(ctx, model(ownerID, uniqueName("census-old"), searchTestDimensions))
	if err != nil {
		t.Fatalf("登记旧模型失败: %v", err)
	}
	empty, err := repo.EnsureDefault(ctx, model(ownerID, uniqueName("census-new"), searchTestDimensions))
	if err != nil {
		t.Fatalf("登记新模型失败: %v", err)
	}

	documentID := seedSearchDocument(t, tx, "向量体检", entity.KnowledgeDocumentStatusReady, true)
	seedSearchChunk(t, tx, documentID, withVectors.ID, 0, "", "第一条", vectorLiteral(searchTestDimensions))
	seedSearchChunk(t, tx, documentID, withVectors.ID, 1, "", "第二条", vectorLiteral(searchTestDimensions))

	rows, err := repo.CountVectorsByModel(ctx, ownerID)
	if err != nil {
		t.Fatalf("统计各模型向量数失败: %v", err)
	}
	counts := make(map[uint64]int64, len(rows))
	for _, row := range rows {
		counts[row.ModelID] = row.Vectors
	}
	if counts[withVectors.ID] != 2 {
		t.Fatalf("旧模型的向量数应为 2，实际 %d（%+v）", counts[withVectors.ID], rows)
	}
	if counts[empty.ID] != 0 {
		t.Fatalf("没有向量的模型也应当出现且计 0，实际 %d（%+v）", counts[empty.ID], rows)
	}
}

// 自动清理只认"同账号、非默认且无向量"：有向量的旧模型留着；向量被替换光之后才删；
// 默认模型即使暂时没有向量也不动 —— 刚切换过去时就是它。别的账号的模型也不能动。
func TestEmbeddingModelDeleteUnusedModels(t *testing.T) {
	tx := testTx(t)
	repo := NewEmbeddingModelRepository(tx)
	ctx := context.Background()
	ownerID := testOwnerID(t, tx)
	otherOwner := testOwnerID(t, tx)

	// 另一个账号的空模型行：本账号的清理不能碰到它。
	otherRow, err := repo.EnsureDefault(ctx, model(otherOwner, uniqueName("prune-other"), searchTestDimensions))
	if err != nil {
		t.Fatalf("登记其它账号模型失败: %v", err)
	}

	oldName := uniqueName("prune-old")
	old, err := repo.EnsureDefault(ctx, model(ownerID, oldName, searchTestDimensions))
	if err != nil {
		t.Fatalf("登记旧模型失败: %v", err)
	}
	documentID := seedSearchDocument(t, tx, "待清理的旧模型", entity.KnowledgeDocumentStatusReady, true)
	seedSearchChunk(t, tx, documentID, old.ID, 0, "", "旧向量", vectorLiteral(searchTestDimensions))

	// 再登记当前模型：它成为默认，旧模型退为非默认。
	current, err := repo.EnsureDefault(ctx, model(ownerID, uniqueName("prune-current"), 1536))
	if err != nil {
		t.Fatalf("登记当前模型失败: %v", err)
	}

	// 旧模型名下有向量：不能删。
	deleted, err := repo.DeleteUnusedModels(ctx, ownerID)
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	for _, name := range deleted {
		if name == oldName {
			t.Fatalf("名下有向量的旧模型 %s 不该被清理", oldName)
		}
	}
	var keptOld entity.EmbeddingModel
	if err := tx.Where("id = ?", old.ID).First(&keptOld).Error; err != nil {
		t.Fatalf("有向量的旧模型应当保留: %v", err)
	}

	// 向量被替换光之后，旧行才该消失。
	if err := tx.Where("model_id = ?", old.ID).Delete(&entity.KnowledgeEmbedding{}).Error; err != nil {
		t.Fatalf("清空旧模型向量失败: %v", err)
	}
	deleted, err = repo.DeleteUnusedModels(ctx, ownerID)
	if err != nil {
		t.Fatalf("再次清理失败: %v", err)
	}
	pruned := false
	for _, name := range deleted {
		if name == oldName {
			pruned = true
		}
	}
	if !pruned {
		t.Errorf("清空向量后应当删掉旧模型，实际删除: %v", deleted)
	}
	// 查询目标用新变量：GORM 会把目标结构体上已有的主键拼进条件，
	// 复用变量会让第二次 First 变成 "id = 新 AND id = 旧"，什么都查不到。
	var goneOld entity.EmbeddingModel
	if err := tx.Where("id = ?", old.ID).First(&goneOld).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("旧模型行应当已被删除，实际 err=%v", err)
	}

	// 默认模型即使没有向量也必须留着。
	var keptCurrent entity.EmbeddingModel
	if err := tx.Where("id = ?", current.ID).First(&keptCurrent).Error; err != nil {
		t.Errorf("默认模型不该被清理: %v", err)
	}

	// 别的账号的行不受影响。
	var keptOther entity.EmbeddingModel
	if err := tx.Where("id = ?", otherRow.ID).First(&keptOther).Error; err != nil {
		t.Errorf("其它账号的模型不该被清理: %v", err)
	}
}
