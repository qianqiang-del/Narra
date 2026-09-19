package repository

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"narra/internal/model/entity"
)

// 这些用例必须跑真库：ReplaceChunks 里有三件只有 PostgreSQL 才能证明的事 ——
// 硬删除（实体没有 DeletedAt）、外键级联（删切片要连带删向量）、
// 以及复合唯一约束 UNIQUE (document_id, chunk_index) 下的"先删后插"。
// 用内存替身测这些等于什么都没测。
//
// 本地运行：
//
//	NARRA_TEST_DSN="host=localhost port=5432 user=postgres password=*** dbname=narra sslmode=disable" \
//	    go test ./internal/repository/ -run Knowledge -v
const knowledgeTestDimensions = 4

// knowledgeTestModelID 登记一个测试专用向量模型，返回它的 ID。
// 向量表的外键指向 embedding_models，没有它插不进向量。
func knowledgeTestModelID(t *testing.T, tx *gorm.DB) uint64 {
	t.Helper()

	model, err := NewEmbeddingModelRepository(tx).EnsureDefault(context.Background(), entity.EmbeddingModel{
		Name:       uniqueName("kb"),
		Provider:   "openai-compatible",
		Dimensions: knowledgeTestDimensions,
	})
	if err != nil {
		t.Fatalf("登记测试向量模型失败: %v", err)
	}
	return model.ID
}

func knowledgeTestDocument() *entity.KnowledgeDocument {
	uri := "test.md"
	return &entity.KnowledgeDocument{
		Title:      "测试文档",
		SourceType: entity.KnowledgeDocumentSourceImport,
		SourceURI:  &uri,
		Enabled:    true,
		Status:     entity.KnowledgeDocumentStatusPending,
		Metadata:   json.RawMessage(`{}`),
	}
}

// knowledgeTestReplacement 造一份"n 个切片 + n 个向量"的替换输入。
func knowledgeTestReplacement(documentID, modelID uint64, count int) entity.ChunkReplacement {
	chunks := make([]entity.KnowledgeChunk, count)
	embeddings := make([]entity.KnowledgeEmbedding, count)

	for index := range chunks {
		content := strings.Repeat("甲", index+1) + "这一段正文"
		chunks[index] = entity.KnowledgeChunk{
			DocumentID:     documentID,
			ChunkIndex:     int32(index),
			Content:        content,
			CharacterCount: int32(len([]rune(content))),
			Metadata:       json.RawMessage(`{}`),
		}

		values := make([]string, knowledgeTestDimensions)
		for position := range values {
			values[position] = "0.5"
		}
		embeddings[index] = entity.KnowledgeEmbedding{
			ModelID:     modelID,
			Dimensions:  knowledgeTestDimensions,
			Embedding:   "[" + strings.Join(values, ",") + "]",
			GeneratedAt: time.Now().UTC(),
		}
	}

	return entity.ChunkReplacement{
		Title:      "测试文档",
		Content:    strings.Repeat("正文内容。", 10),
		Checksum:   strings.Repeat("a", 64),
		Metadata:   json.RawMessage(`{"parser":"test"}`),
		Chunks:     chunks,
		Embeddings: embeddings,
	}
}

func countRows(t *testing.T, tx *gorm.DB, model any, where string, args ...any) int64 {
	t.Helper()

	var total int64
	if err := tx.Model(model).Where(where, args...).Count(&total).Error; err != nil {
		t.Fatalf("统计行数失败: %v", err)
	}
	return total
}

func TestKnowledgeReplaceChunksWritesThreeTables(t *testing.T) {
	tx := testTx(t)
	repo := NewKnowledgeDocumentRepository(tx)
	ctx := context.Background()

	modelID := knowledgeTestModelID(t, tx)
	document := knowledgeTestDocument()
	if err := repo.Create(ctx, document); err != nil {
		t.Fatalf("创建文档失败: %v", err)
	}
	if document.ID == 0 {
		t.Fatal("创建后应当回填主键")
	}

	input := knowledgeTestReplacement(document.ID, modelID, 3)
	if err := repo.ReplaceChunks(ctx, document.ID, input); err != nil {
		t.Fatalf("替换切片失败: %v", err)
	}

	reloaded, err := repo.GetByID(ctx, document.ID)
	if err != nil {
		t.Fatalf("回读文档失败: %v", err)
	}
	if reloaded.Status != entity.KnowledgeDocumentStatusReady {
		t.Errorf("状态 = %q，期望 ready", reloaded.Status)
	}
	if reloaded.Content != input.Content {
		t.Error("正文没有写进 knowledge_documents")
	}
	if reloaded.ContentChecksum == nil || *reloaded.ContentChecksum != input.Checksum {
		t.Errorf("摘要没有写对: %v", reloaded.ContentChecksum)
	}

	if got := countRows(t, tx, &entity.KnowledgeChunk{}, "document_id = ?", document.ID); got != 3 {
		t.Errorf("切片数 = %d，期望 3", got)
	}
	if got := countRows(t, tx, &entity.KnowledgeEmbedding{}, "model_id = ?", modelID); got != 3 {
		t.Errorf("向量数 = %d，期望 3", got)
	}

	// 每个向量的 chunk_id 必须指向同序号那个切片 —— 这是仓储回填主键的结果，
	// 错一位就是"检索召回 A、返回 B"，而且不会报任何错。
	var rows []struct {
		ChunkIndex     int32
		CharacterCount int32
	}
	err = tx.Raw(`
		SELECT c.chunk_index, c.character_count
		FROM knowledge_chunks c
		JOIN knowledge_embeddings e ON e.chunk_id = c.id
		WHERE c.document_id = ?
		ORDER BY c.chunk_index`, document.ID).Scan(&rows).Error
	if err != nil {
		t.Fatalf("联表查询失败: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("联表查出 %d 行，期望每个切片都有一条向量", len(rows))
	}
	for index, row := range rows {
		if row.ChunkIndex != int32(index) {
			t.Errorf("第 %d 行的 chunk_index = %d，向量与切片可能错位", index, row.ChunkIndex)
		}
		want := int32(len([]rune(strings.Repeat("甲", index+1) + "这一段正文")))
		if row.CharacterCount != want {
			t.Errorf("第 %d 行对应切片的 character_count = %d，期望 %d", index, row.CharacterCount, want)
		}
	}
}

// 换掉切片时旧切片和它的向量都必须消失：留着的旧向量会在检索里被召回，
// 而它对应的正文早已不在原文里了。
func TestKnowledgeReplaceChunksRemovesPreviousChunksAndVectors(t *testing.T) {
	tx := testTx(t)
	repo := NewKnowledgeDocumentRepository(tx)
	ctx := context.Background()

	modelID := knowledgeTestModelID(t, tx)
	document := knowledgeTestDocument()
	if err := repo.Create(ctx, document); err != nil {
		t.Fatalf("创建文档失败: %v", err)
	}

	if err := repo.ReplaceChunks(ctx, document.ID, knowledgeTestReplacement(document.ID, modelID, 4)); err != nil {
		t.Fatalf("首次替换失败: %v", err)
	}
	if got := countRows(t, tx, &entity.KnowledgeEmbedding{}, "model_id = ?", modelID); got != 4 {
		t.Fatalf("首次替换后向量数 = %d，期望 4", got)
	}

	if err := repo.ReplaceChunks(ctx, document.ID, knowledgeTestReplacement(document.ID, modelID, 1)); err != nil {
		t.Fatalf("二次替换失败: %v", err)
	}

	if got := countRows(t, tx, &entity.KnowledgeChunk{}, "document_id = ?", document.ID); got != 1 {
		t.Errorf("二次替换后切片数 = %d，期望 1（旧切片必须被删掉）", got)
	}
	if got := countRows(t, tx, &entity.KnowledgeEmbedding{}, "model_id = ?", modelID); got != 1 {
		t.Errorf("二次替换后向量数 = %d，期望 1（旧向量应当随切片级联删除）", got)
	}
}

// 切片的插入与文档的 ready 标记必须同生共死：
// 中间失败后库里要么全是旧数据，要么全是新数据，不能一半。
func TestKnowledgeReplaceChunksRollsBackOnVectorCountMismatch(t *testing.T) {
	tx := testTx(t)
	repo := NewKnowledgeDocumentRepository(tx)
	ctx := context.Background()

	modelID := knowledgeTestModelID(t, tx)
	document := knowledgeTestDocument()
	if err := repo.Create(ctx, document); err != nil {
		t.Fatalf("创建文档失败: %v", err)
	}
	if err := repo.ReplaceChunks(ctx, document.ID, knowledgeTestReplacement(document.ID, modelID, 3)); err != nil {
		t.Fatalf("首次替换失败: %v", err)
	}

	broken := knowledgeTestReplacement(document.ID, modelID, 3)
	broken.Embeddings = broken.Embeddings[:2]
	if err := repo.ReplaceChunks(ctx, document.ID, broken); err == nil {
		t.Fatal("切片与向量数量不一致时必须报错")
	}

	if got := countRows(t, tx, &entity.KnowledgeChunk{}, "document_id = ?", document.ID); got != 3 {
		t.Errorf("失败后切片数 = %d，期望仍是 3", got)
	}
	if got := countRows(t, tx, &entity.KnowledgeEmbedding{}, "model_id = ?", modelID); got != 3 {
		t.Errorf("失败后向量数 = %d，期望仍是 3", got)
	}
}

// 没有切片的文档不能被标成 ready：那会在列表里显示"已入库"，检索却永远搜不到。
func TestKnowledgeReplaceChunksRejectsEmptyChunks(t *testing.T) {
	tx := testTx(t)
	repo := NewKnowledgeDocumentRepository(tx)
	ctx := context.Background()

	document := knowledgeTestDocument()
	if err := repo.Create(ctx, document); err != nil {
		t.Fatalf("创建文档失败: %v", err)
	}

	empty := knowledgeTestReplacement(document.ID, 0, 0)
	empty.Chunks, empty.Embeddings = nil, nil
	if err := repo.ReplaceChunks(ctx, document.ID, empty); err == nil {
		t.Fatal("没有切片时必须报错")
	}

	reloaded, err := repo.GetByID(ctx, document.ID)
	if err != nil {
		t.Fatalf("回读文档失败: %v", err)
	}
	if reloaded.Status == entity.KnowledgeDocumentStatusReady {
		t.Error("没有切片的文档不能被标成 ready")
	}
}

// updated_at 没有数据库触发器兜底，全靠 GORM 的 autoUpdateTime。
// 这条用例是它的回归哨兵：状态推进用的是 Updates(map)，一旦有人改成
// UpdateColumn 或裸 Exec，这个字段会静默停在创建时间。
func TestKnowledgeStatusUpdatesBumpUpdatedAt(t *testing.T) {
	tx := testTx(t)
	repo := NewKnowledgeDocumentRepository(tx)
	ctx := context.Background()

	document := knowledgeTestDocument()
	if err := repo.Create(ctx, document); err != nil {
		t.Fatalf("创建文档失败: %v", err)
	}
	created := document.UpdatedAt

	if err := repo.MarkProcessing(ctx, document.ID); err != nil {
		t.Fatalf("推进状态失败: %v", err)
	}
	after, err := repo.GetByID(ctx, document.ID)
	if err != nil {
		t.Fatalf("回读文档失败: %v", err)
	}
	if after.Status != entity.KnowledgeDocumentStatusProcessing {
		t.Errorf("状态 = %q，期望 processing", after.Status)
	}
	if !after.UpdatedAt.After(created) {
		t.Errorf("updated_at 没有前进: %v → %v", created, after.UpdatedAt)
	}

	metadata := json.RawMessage(`{"stage":"embed","error":"上游 429"}`)
	if err := repo.MarkFailed(ctx, document.ID, metadata); err != nil {
		t.Fatalf("标记失败状态出错: %v", err)
	}
	failed, err := repo.GetByID(ctx, document.ID)
	if err != nil {
		t.Fatalf("回读文档失败: %v", err)
	}
	if failed.Status != entity.KnowledgeDocumentStatusFailed {
		t.Errorf("状态 = %q，期望 failed", failed.Status)
	}
	var payload struct {
		Stage string `json:"stage"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(failed.Metadata, &payload); err != nil {
		t.Fatalf("失败原因不是合法 JSON: %v", err)
	}
	if payload.Stage != "embed" || payload.Error != "上游 429" {
		t.Errorf("失败原因写错了: %+v", payload)
	}
}

func TestKnowledgeListAndCountChunks(t *testing.T) {
	tx := testTx(t)
	repo := NewKnowledgeDocumentRepository(tx)
	ctx := context.Background()

	modelID := knowledgeTestModelID(t, tx)
	var ids []uint64
	for index := 0; index < 3; index++ {
		document := knowledgeTestDocument()
		document.Title = uniqueName("list")
		if err := repo.Create(ctx, document); err != nil {
			t.Fatalf("创建文档失败: %v", err)
		}
		if err := repo.ReplaceChunks(ctx, document.ID, knowledgeTestReplacement(document.ID, modelID, index+1)); err != nil {
			t.Fatalf("替换切片失败: %v", err)
		}
		ids = append(ids, document.ID)
	}

	counts, err := repo.CountChunksByDocument(ctx, ids)
	if err != nil {
		t.Fatalf("统计切片数失败: %v", err)
	}
	for index, id := range ids {
		if counts[id] != int64(index+1) {
			t.Errorf("文档 %d 的切片数 = %d，期望 %d", id, counts[id], index+1)
		}
	}

	// 同一个事务里能看到自己写入的行，分页与排序也能正常走。
	documents, total, err := repo.List(ctx, 0, 2)
	if err != nil {
		t.Fatalf("分页查询失败: %v", err)
	}
	if total < 3 {
		t.Errorf("总数 = %d，期望至少 3", total)
	}
	if len(documents) != 2 {
		t.Errorf("本页返回 %d 条，期望 2", len(documents))
	}
}
