package repository

import (
	"context"
	"testing"

	"gorm.io/gorm"

	"narra/internal/model/entity"
)

// 这几个用例必须跑真库：判据是一个 EXISTS + NOT EXISTS 谓词，取的是"存在切片缺
// 该模型向量"的文档；"计数"与"取 ID"共用同一份谓词，SQL 写得对不对用替身测不出来。
//
// 本地运行：
//
//	NARRA_TEST_DSN="host=localhost port=5432 user=postgres password=*** dbname=narra sslmode=disable" \
//	    go test ./internal/repository/ -run KnowledgeReembed -v

// seedBareChunk 造一个没有向量的切片，用来构造"缺向量"的文档。
// seedSearchChunk 总会带一条向量，这里刻意分开，两种数据才好分别摆。
func seedBareChunk(t *testing.T, tx *gorm.DB, documentID uint64, index int, content string) uint64 {
	t.Helper()

	var chunk struct {
		ID uint64
	}
	err := tx.Raw(`
		INSERT INTO knowledge_chunks (created_at, updated_at, document_id, chunk_index, content, character_count)
		VALUES (now(), now(), ?, ?, ?, ?)
		RETURNING id`,
		documentID, index, content, len([]rune(content))).Scan(&chunk).Error
	if err != nil {
		t.Fatalf("插入无向量切片失败: %v", err)
	}
	return chunk.ID
}

// 谓词覆盖三种形态：全部齐（不算缺）、只有旧模型向量（缺）、部分缺失（也算缺）、
// 以及非 ready 文档（不算 —— 收录还没成功的文档由收录链路负责，不是换模型的问题）。
func TestKnowledgeReembedQueriesFindStaleDocuments(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	current := knowledgeTestModelID(t, tx)
	old := knowledgeTestModelID(t, tx)

	fresh := seedSearchDocument(t, tx, "最新向量", entity.KnowledgeDocumentStatusReady, true)
	seedSearchChunk(t, tx, fresh, current, 0, "", "甲", "[0,1,0,0]")
	seedSearchChunk(t, tx, fresh, current, 1, "", "乙", "[0,0,1,0]")

	stale := seedSearchDocument(t, tx, "旧向量", entity.KnowledgeDocumentStatusReady, true)
	seedSearchChunk(t, tx, stale, old, 0, "", "丙", "[0,1,0,0]")
	seedSearchChunk(t, tx, stale, old, 1, "", "丁", "[0,0,1,0]")

	partial := seedSearchDocument(t, tx, "缺一片", entity.KnowledgeDocumentStatusReady, true)
	seedSearchChunk(t, tx, partial, current, 0, "", "戊", "[0,1,0,0]")
	seedBareChunk(t, tx, partial, 1, "己")

	notReady := seedSearchDocument(t, tx, "还没收录完", entity.KnowledgeDocumentStatusPending, true)
	seedBareChunk(t, tx, notReady, 0, "庚")

	repo := NewKnowledgeDocumentRepository(tx)

	count, err := repo.CountDocumentsMissingModelVectors(ctx, current, []string{entity.KnowledgeDocumentStatusReady})
	if err != nil {
		t.Fatalf("统计缺向量文档失败: %v", err)
	}
	ids, err := repo.ListDocumentIDsMissingModelVectors(ctx, current)
	if err != nil {
		t.Fatalf("列出缺向量文档失败: %v", err)
	}
	if count != int64(len(ids)) {
		t.Errorf("计数 %d 与列表长度 %d 不一致（两者必须共用同一个判据）", count, len(ids))
	}

	// 开发库里本来就有别的文档挂在旧模型下，全局总数无法做精确断言；
	// 精确性落在"这篇该不该被选中"上：
	//   - 只有旧模型向量的（stale）与部分缺失的（partial）必须出现；
	//   - 当前模型下齐备的（fresh）与还没收录完的（notReady）必须不出现。
	selected := make(map[uint64]bool, len(ids))
	for _, id := range ids {
		selected[id] = true
	}
	if !selected[stale] {
		t.Errorf("只有旧模型向量的文档 %d 应当被选中", stale)
	}
	if !selected[partial] {
		t.Errorf("部分缺失的文档 %d 也应当被选中", partial)
	}
	if selected[fresh] {
		t.Errorf("当前模型下向量齐备的文档 %d 不该被选中", fresh)
	}
	if selected[notReady] {
		t.Errorf("还没收录完的文档 %d 不该被选中（由收录链路负责，不是换模型的问题）", notReady)
	}

	// 同一判据换一个状态口径：pending 里缺向量的文档应当被"正在补"的计数看到 ——
	// 重新向量化入队后的文档正是落在这个口径里（进度读数）。
	pendingCount, err := repo.CountDocumentsMissingModelVectors(ctx, current, []string{
		entity.KnowledgeDocumentStatusPending, entity.KnowledgeDocumentStatusProcessing,
	})
	if err != nil {
		t.Fatalf("统计正在补的文档失败: %v", err)
	}
	if pendingCount < 1 {
		t.Errorf("正在补的计数 = %d，至少应包含 notReady（%d）这一篇", pendingCount, notReady)
	}
}

// 重新排队的写入口只认 ready：成功后回到 pending、阶段写成 embed，
// metadata 与上传记录都原样保留 —— 这不是一次新的投递。再点一次要安静地输掉。
func TestKnowledgeReembedRequeueOnlyFromReady(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	repo := NewKnowledgeDocumentRepository(tx)
	records := NewKnowledgeUploadRecordRepository(tx)

	ready := seedSearchDocument(t, tx, "待重算", entity.KnowledgeDocumentStatusReady, true)
	if err := tx.Exec(`
		UPDATE knowledge_documents SET metadata = '{"upload_path":"/tmp/smoke/upload.md","explicit_title":false}'::jsonb
		WHERE id = ?`, ready).Error; err != nil {
		t.Fatalf("写入 metadata 失败: %v", err)
	}

	record := &entity.KnowledgeUploadRecord{
		DocumentID:   &ready,
		OriginalName: "smoke.md",
		Status:       entity.KnowledgeUploadRecordStatusReady,
	}
	if err := records.CreateUploadRecord(ctx, record); err != nil {
		t.Fatalf("创建上传记录失败: %v", err)
	}

	requeued, err := repo.RequeueForReembed(ctx, ready, entity.KnowledgeDocumentStageEmbed)
	if err != nil {
		t.Fatalf("重新排队失败: %v", err)
	}
	if !requeued {
		t.Fatal("ready 文档应当能被重新排队")
	}

	reloaded, err := repo.GetByID(ctx, ready)
	if err != nil {
		t.Fatalf("回读文档失败: %v", err)
	}
	if reloaded.Status != entity.KnowledgeDocumentStatusPending {
		t.Errorf("状态应为 pending，实际 %s", reloaded.Status)
	}
	if reloaded.IngestStage == nil || *reloaded.IngestStage != entity.KnowledgeDocumentStageEmbed {
		t.Errorf("阶段应为 embed，实际 %v", reloaded.IngestStage)
	}

	metadata := knowledgeTestMetadata(t, repo, ready)
	if metadata["upload_path"] != "/tmp/smoke/upload.md" || metadata["explicit_title"] != false {
		t.Errorf("metadata 不该被改动，实际 %v", metadata)
	}

	stored, err := records.GetByID(ctx, record.ID)
	if err != nil {
		t.Fatalf("回读上传记录失败: %v", err)
	}
	if stored.Status != entity.KnowledgeUploadRecordStatusReady {
		t.Errorf("上传记录不该被改动（这不是一次新的投递），实际 %s", stored.Status)
	}

	// 已经不是 ready 了：重复点击安静输掉。
	again, err := repo.RequeueForReembed(ctx, ready, entity.KnowledgeDocumentStageEmbed)
	if err != nil {
		t.Fatalf("重复入队不该报错: %v", err)
	}
	if again {
		t.Error("已经不是 ready 状态，重复入队应当返回 false")
	}

	// 非 ready 的文档同样不满足条件。
	pending := seedSearchDocument(t, tx, "还没就绪", entity.KnowledgeDocumentStatusPending, true)
	applied, err := repo.RequeueForReembed(ctx, pending, entity.KnowledgeDocumentStageEmbed)
	if err != nil {
		t.Fatalf("对 pending 文档入队出错: %v", err)
	}
	if applied {
		t.Error("pending 文档不该被重新向量化入口命中")
	}
}
