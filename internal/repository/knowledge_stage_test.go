package repository

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"narra/internal/model/entity"
)

// 分阶段写入的真库用例：阶段推进、租约守卫、最终事务的原子性。
// 这些语义只有 PostgreSQL 能证明 —— 条件 UPDATE 的 RowsAffected、jsonb 顶层合并、
// 外键级联与事务回滚，内存替身测不出来。
//
// 本地运行：
//
//	NARRA_TEST_DSN="host=localhost port=5432 user=postgres password=*** dbname=narra sslmode=disable" \
//	    go test ./internal/repository/ -run KnowledgeStaged -v

// stageTestChunks 造 n 个待落库的切片。
func stageTestChunks(documentID uint64, count int) []entity.KnowledgeChunk {
	chunks := make([]entity.KnowledgeChunk, count)
	for index := range chunks {
		content := strings.Repeat("甲", index+1) + "这一段正文"
		chunks[index] = entity.KnowledgeChunk{
			DocumentID:     documentID,
			ChunkIndex:     int32(index),
			Content:        content,
			CharacterCount: int32(len([]rune(content))),
			Metadata:       json.RawMessage(`{}`),
		}
	}
	return chunks
}

// stageTestEmbeddings 为已落库的切片造向量（ChunkID 用回读出来的真实主键）。
func stageTestEmbeddings(stored []entity.KnowledgeChunk, modelID uint64) []entity.KnowledgeEmbedding {
	values := "[" + strings.TrimSuffix(strings.Repeat("0.5,", knowledgeTestDimensions), ",") + "]"
	generatedAt := time.Now().UTC()

	embeddings := make([]entity.KnowledgeEmbedding, len(stored))
	for index, chunk := range stored {
		embeddings[index] = entity.KnowledgeEmbedding{
			ChunkID:     chunk.ID,
			ModelID:     modelID,
			Dimensions:  knowledgeTestDimensions,
			Embedding:   values,
			GeneratedAt: generatedAt,
		}
	}
	return embeddings
}

// 三个阶段依次推进：正文、切片、向量各自落库，阶段随数据一起前进；
// metadata 全程只做顶层合并（upload_path 必须活到成功清理原文件为止）。
func TestKnowledgeStagedWritesAdvanceStage(t *testing.T) {
	tx := testTx(t)
	repo := NewKnowledgeDocumentRepository(tx)
	ctx := context.Background()
	modelID := knowledgeTestModelID(t, tx)

	document := knowledgeTestDocument()
	if err := repo.Create(ctx, document); err != nil {
		t.Fatalf("创建文档失败: %v", err)
	}

	attempt, claimed, err := repo.ClaimAndReturnAttempt(ctx, document.ID)
	if err != nil || !claimed {
		t.Fatalf("认领失败: claimed=%v err=%v", claimed, err)
	}
	if attempt != 1 {
		t.Errorf("首次认领的租约编号 = %d，期望 1", attempt)
	}

	// 解析成功：正文与阶段一起落库。
	parsed := entity.ParsedContent{
		Title:    "阶段测试文档",
		Content:  strings.Repeat("正文内容。", 30),
		Checksum: strings.Repeat("b", 64),
		Metadata: json.RawMessage(`{"parser":"staged-test","upload_path":"/tmp/staged/upload.md"}`),
	}
	if err := repo.SaveParsedContent(ctx, document.ID, attempt, parsed); err != nil {
		t.Fatalf("保存解析产物失败: %v", err)
	}
	afterParse, err := repo.GetByID(ctx, document.ID)
	if err != nil {
		t.Fatalf("回读文档失败: %v", err)
	}
	if afterParse.IngestStage == nil || *afterParse.IngestStage != entity.KnowledgeDocumentStageChunk {
		t.Fatalf("解析成功后阶段应当推进到 chunk，实际 %v", afterParse.IngestStage)
	}
	if afterParse.Content != parsed.Content {
		t.Error("正文没有落库")
	}
	var metadata map[string]any
	if err := json.Unmarshal(afterParse.Metadata, &metadata); err != nil {
		t.Fatalf("解析 metadata 失败: %v", err)
	}
	if metadata["upload_path"] != "/tmp/staged/upload.md" {
		t.Errorf("阶段写入必须合并 metadata，upload_path 丢了: %v", metadata)
	}

	// 分块成功：切片落库、阶段推进到 embed。
	if err := repo.ReplaceStagedChunks(ctx, document.ID, attempt, stageTestChunks(document.ID, 2), json.RawMessage(`{"chunks":2}`)); err != nil {
		t.Fatalf("保存切片失败: %v", err)
	}
	stored, err := repo.ListChunksByDocument(ctx, document.ID)
	if err != nil {
		t.Fatalf("读取切片失败: %v", err)
	}
	if len(stored) != 2 {
		t.Fatalf("切片数 = %d，期望 2", len(stored))
	}
	if stored[0].ChunkIndex != 0 || stored[1].ChunkIndex != 1 {
		t.Errorf("切片顺序不对：%d, %d", stored[0].ChunkIndex, stored[1].ChunkIndex)
	}
	afterChunk, err := repo.GetByID(ctx, document.ID)
	if err != nil {
		t.Fatalf("回读文档失败: %v", err)
	}
	if afterChunk.IngestStage == nil || *afterChunk.IngestStage != entity.KnowledgeDocumentStageEmbed {
		t.Errorf("分块成功后阶段应当推进到 embed，实际 %v", afterChunk.IngestStage)
	}

	// 向量化成功：同一事务里写向量、置 ready、清空阶段。
	if err := repo.SaveEmbeddingsAndMarkReady(ctx, document.ID, attempt, stageTestEmbeddings(stored, modelID), json.RawMessage(`{"model":"test"}`)); err != nil {
		t.Fatalf("保存向量失败: %v", err)
	}
	final, err := repo.GetByID(ctx, document.ID)
	if err != nil {
		t.Fatalf("回读文档失败: %v", err)
	}
	if final.Status != entity.KnowledgeDocumentStatusReady {
		t.Errorf("状态 = %q，期望 ready", final.Status)
	}
	if final.IngestStage != nil {
		t.Errorf("ready 文档的阶段必须是 NULL，实际 %q", *final.IngestStage)
	}
	if got := countRows(t, tx, &entity.KnowledgeEmbedding{}, "model_id = ?", modelID); got != 2 {
		t.Errorf("向量数 = %d，期望 2", got)
	}
}

// 旧租约的迟到写入必须影响 0 行：正文不写、失败现场也不写。
// 行被回收并重新认领之后，旧执行者手里的编号已经对不上，这是它唯一的凭据。
func TestKnowledgeStagedWritesRejectStaleAttempt(t *testing.T) {
	tx := testTx(t)
	repo := NewKnowledgeDocumentRepository(tx)
	ctx := context.Background()

	document := knowledgeTestDocument()
	if err := repo.Create(ctx, document); err != nil {
		t.Fatalf("创建文档失败: %v", err)
	}

	attempt, claimed, err := repo.ClaimAndReturnAttempt(ctx, document.ID)
	if err != nil || !claimed {
		t.Fatalf("认领失败: claimed=%v err=%v", claimed, err)
	}

	stale := entity.ParsedContent{
		Title:    "旧租约的写入",
		Content:  strings.Repeat("正文内容。", 20),
		Checksum: strings.Repeat("c", 64),
		Metadata: json.RawMessage(`{}`),
	}
	if err := repo.SaveParsedContent(ctx, document.ID, attempt-1, stale); err == nil {
		t.Fatal("旧租约写正文必须报错（条件更新影响 0 行）")
	}
	after, err := repo.GetByID(ctx, document.ID)
	if err != nil {
		t.Fatalf("回读文档失败: %v", err)
	}
	if after.Content != "" {
		t.Error("旧租约不该把正文写进去")
	}
	if after.IngestStage == nil || *after.IngestStage != entity.KnowledgeDocumentStageParse {
		t.Errorf("阶段不该被旧租约推进，实际 %v", after.IngestStage)
	}

	// 旧租约换切片同样影响 0 行：先删后插整笔回滚，库里不会留下半批切片。
	if err := repo.ReplaceStagedChunks(ctx, document.ID, attempt-1, stageTestChunks(document.ID, 2), nil); err == nil {
		t.Fatal("旧租约换切片必须报错")
	}
	if got := countRows(t, tx, &entity.KnowledgeChunk{}, "document_id = ?", document.ID); got != 0 {
		t.Errorf("旧租约不该写入切片，实际 %d 条", got)
	}

	applied, err := repo.MarkFailed(ctx, document.ID, attempt-1, json.RawMessage(`{"error":"旧租约"}`), "旧租约")
	if err != nil || applied {
		t.Fatalf("旧租约的失败现场不该写入: applied=%v err=%v", applied, err)
	}
	applied, err = repo.MarkFailed(ctx, document.ID, attempt, json.RawMessage(`{"error":"当前租约"}`), "当前租约")
	if err != nil || !applied {
		t.Fatalf("当前租约的失败现场应当写入: applied=%v err=%v", applied, err)
	}
}

// 向量数与切片数对不上时整笔回滚：不能出现 ready 却没有向量的文档，
// 也不能留下半批向量。
func TestKnowledgeStagedEmbeddingsRollBackOnMismatch(t *testing.T) {
	tx := testTx(t)
	repo := NewKnowledgeDocumentRepository(tx)
	ctx := context.Background()
	modelID := knowledgeTestModelID(t, tx)

	document := knowledgeTestDocument()
	if err := repo.Create(ctx, document); err != nil {
		t.Fatalf("创建文档失败: %v", err)
	}
	attempt, claimed, err := repo.ClaimAndReturnAttempt(ctx, document.ID)
	if err != nil || !claimed {
		t.Fatalf("认领失败: claimed=%v err=%v", claimed, err)
	}
	if err := repo.SaveParsedContent(ctx, document.ID, attempt, entity.ParsedContent{
		Title:    "文档",
		Content:  strings.Repeat("正文内容。", 20),
		Checksum: strings.Repeat("d", 64),
		Metadata: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("保存解析产物失败: %v", err)
	}
	if err := repo.ReplaceStagedChunks(ctx, document.ID, attempt, stageTestChunks(document.ID, 2), nil); err != nil {
		t.Fatalf("保存切片失败: %v", err)
	}
	stored, err := repo.ListChunksByDocument(ctx, document.ID)
	if err != nil {
		t.Fatalf("读取切片失败: %v", err)
	}

	// 两个切片只给一个向量。
	broken := stageTestEmbeddings(stored[:1], modelID)
	if err := repo.SaveEmbeddingsAndMarkReady(ctx, document.ID, attempt, broken, nil); err == nil {
		t.Fatal("向量数与切片数不一致时必须报错")
	}

	after, err := repo.GetByID(ctx, document.ID)
	if err != nil {
		t.Fatalf("回读文档失败: %v", err)
	}
	if after.Status != entity.KnowledgeDocumentStatusProcessing {
		t.Errorf("状态 = %q，期望仍停在 processing", after.Status)
	}
	if after.IngestStage == nil || *after.IngestStage != entity.KnowledgeDocumentStageEmbed {
		t.Errorf("阶段应当仍停在 embed，实际 %v", after.IngestStage)
	}
	if got := countRows(t, tx, &entity.KnowledgeEmbedding{}, "model_id = ?", modelID); got != 0 {
		t.Errorf("失败时不该留下任何向量，实际 %d 条", got)
	}
}

// 认领要原子递增租约编号；被周期回收之后重新认领拿到更大的编号，
// 旧编号的心跳也随之失效。
func TestKnowledgeClaimReturnsIncrementedAttempt(t *testing.T) {
	tx := testTx(t)
	repo := NewKnowledgeDocumentRepository(tx)
	ctx := context.Background()

	document := knowledgeTestDocument()
	if err := repo.Create(ctx, document); err != nil {
		t.Fatalf("创建文档失败: %v", err)
	}

	first, claimed, err := repo.ClaimAndReturnAttempt(ctx, document.ID)
	if err != nil || !claimed {
		t.Fatalf("首次认领失败: claimed=%v err=%v", claimed, err)
	}
	if first != 1 {
		t.Errorf("首次租约编号 = %d，期望 1", first)
	}
	if _, claimed, err := repo.ClaimAndReturnAttempt(ctx, document.ID); err != nil || claimed {
		t.Errorf("同一行不该被认领两次: claimed=%v err=%v", claimed, err)
	}

	// 模拟僵尸回收：打回 pending 之后再认领，编号必须递增。
	if err := repo.ResetStale(ctx, time.Now().Add(time.Second)); err != nil {
		t.Fatalf("回收僵尸任务失败: %v", err)
	}
	second, claimed, err := repo.ClaimAndReturnAttempt(ctx, document.ID)
	if err != nil || !claimed {
		t.Fatalf("回收后重新认领失败: claimed=%v err=%v", claimed, err)
	}
	if second != first+1 {
		t.Errorf("重新认领的编号 = %d，期望 %d", second, first+1)
	}

	before, err := repo.GetByID(ctx, document.ID)
	if err != nil {
		t.Fatalf("回读文档失败: %v", err)
	}
	if err := repo.Touch(ctx, document.ID, first); err != nil {
		t.Fatalf("旧租约心跳报错: %v", err)
	}
	afterStale, err := repo.GetByID(ctx, document.ID)
	if err != nil {
		t.Fatalf("回读文档失败: %v", err)
	}
	if !afterStale.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("旧租约的心跳不该推进 updated_at: %v → %v", before.UpdatedAt, afterStale.UpdatedAt)
	}

	if err := repo.Touch(ctx, document.ID, second); err != nil {
		t.Fatalf("当前租约心跳报错: %v", err)
	}
	afterFresh, err := repo.GetByID(ctx, document.ID)
	if err != nil {
		t.Fatalf("回读文档失败: %v", err)
	}
	if !afterFresh.UpdatedAt.After(afterStale.UpdatedAt) {
		t.Errorf("当前租约的心跳应当推进 updated_at: %v → %v", afterStale.UpdatedAt, afterFresh.UpdatedAt)
	}
}
