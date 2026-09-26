package repository

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"gorm.io/gorm"

	"narra/internal/model/entity"
)

// 这些用例必须跑真库：上传记录表的几件关键行为都由 PostgreSQL 保证 ——
// 外键的 ON DELETE SET NULL（文档没了、投递历史留着）、切片的级联删除、
// 以及 DeleteRecord 那个事务里"按文档状态决定要不要连带删"的取舍。
//
// 本地运行：
//
//	NARRA_TEST_DSN="host=localhost port=5432 user=postgres password=*** dbname=narra sslmode=disable" \
//	    go test ./internal/repository/ -run UploadRecord -v

// uploadRecordTestDocument 建一篇测试文档，状态由调用方指定。
func uploadRecordTestDocument(t *testing.T, tx *gorm.DB, status string) *entity.KnowledgeDocument {
	t.Helper()

	document := knowledgeTestDocument()
	document.Status = status
	if err := NewKnowledgeDocumentRepository(tx).Create(context.Background(), document); err != nil {
		t.Fatalf("创建文档失败: %v", err)
	}
	return document
}

// uploadRecordTestRecord 给一篇文档建一条 pending 记录。
func uploadRecordTestRecord(t *testing.T, tx *gorm.DB, documentID uint64) *entity.KnowledgeUploadRecord {
	t.Helper()

	record := &entity.KnowledgeUploadRecord{
		DocumentID:   &documentID,
		OriginalName: "设计文档.md",
		SizeBytes:    4096,
		Status:       entity.KnowledgeUploadRecordStatusPending,
	}
	if err := NewKnowledgeUploadRecordRepository(tx).CreateUploadRecord(context.Background(), record); err != nil {
		t.Fatalf("创建上传记录失败: %v", err)
	}
	return record
}

// 列表要带出关联文档的标题（LEFT JOIN），并且最新的排在最前。
//
// 用增量断言而不是绝对值：开发库里本来就有历史记录。
// 新记录一定排第一 —— 排序是 created_at DESC, id DESC，它的 id 最大。
func TestUploadRecordListJoinsDocumentTitle(t *testing.T) {
	tx := testTx(t)
	records := NewKnowledgeUploadRecordRepository(tx)
	ctx := context.Background()

	_, totalBefore, err := records.List(ctx, 0, 1)
	if err != nil {
		t.Fatalf("列表查询失败: %v", err)
	}

	document := uploadRecordTestDocument(t, tx, entity.KnowledgeDocumentStatusPending)
	record := uploadRecordTestRecord(t, tx, document.ID)

	views, total, err := records.List(ctx, 0, 10)
	if err != nil {
		t.Fatalf("列表查询失败: %v", err)
	}
	if total != totalBefore+1 {
		t.Errorf("总数 = %d，期望 %d", total, totalBefore+1)
	}
	if len(views) == 0 || views[0].ID != record.ID {
		t.Fatalf("最新的记录应当排在第一页第一条，实际 %+v", views)
	}

	view := views[0]
	if view.DocumentTitle != document.Title {
		t.Errorf("联表标题 = %q，期望 %q", view.DocumentTitle, document.Title)
	}
	if view.DocumentID == nil || *view.DocumentID != document.ID {
		t.Errorf("document_id = %v，期望 %d", view.DocumentID, document.ID)
	}
	if view.OriginalName != "设计文档.md" {
		t.Errorf("原始文件名 = %q，期望 设计文档.md", view.OriginalName)
	}
	if view.SizeBytes != 4096 {
		t.Errorf("字节数 = %d，期望 4096", view.SizeBytes)
	}
}

// 状态同步：收录成功时记录跟着到 ready，失败时到 failed 并带上原因。
//
// 两处都在文档仓储的事务里完成 —— 分开写会出现"文档已可检索、记录还停在处理中"
// 这种自相矛盾的两行。顺带验一下没有记录的手动录入走同一条路径不会出错。
func TestUploadRecordStatusSyncsWithDocument(t *testing.T) {
	tx := testTx(t)
	documents := NewKnowledgeDocumentRepository(tx)
	records := NewKnowledgeUploadRecordRepository(tx)
	ctx := context.Background()

	modelID := knowledgeTestModelID(t, tx)
	document := uploadRecordTestDocument(t, tx, entity.KnowledgeDocumentStatusPending)
	record := uploadRecordTestRecord(t, tx, document.ID)

	// ReplaceChunks 与 MarkFailed 只接受**处理中**的文档（见 ReplaceChunks 的注释），
	// 真实链路里这一步由 rag.Ingester 做，用例直接调仓储，所以要自己补上。
	if err := documents.MarkProcessing(ctx, document.ID); err != nil {
		t.Fatalf("推进状态失败: %v", err)
	}
	if err := documents.ReplaceChunks(ctx, document.ID, knowledgeTestReplacement(document.ID, modelID, 2)); err != nil {
		t.Fatalf("替换切片失败: %v", err)
	}

	succeeded, err := records.GetByID(ctx, record.ID)
	if err != nil {
		t.Fatalf("回读记录失败: %v", err)
	}
	if succeeded.Status != entity.KnowledgeUploadRecordStatusReady {
		t.Errorf("收录成功后记录状态 = %q，期望 ready", succeeded.Status)
	}
	if succeeded.ErrorMessage != nil {
		t.Errorf("成功时不该留下失败原因，实际 %q", *succeeded.ErrorMessage)
	}

	// 失败路径。
	failing := uploadRecordTestDocument(t, tx, entity.KnowledgeDocumentStatusPending)
	failingRecord := uploadRecordTestRecord(t, tx, failing.ID)
	if err := documents.MarkProcessing(ctx, failing.ID); err != nil {
		t.Fatalf("推进状态失败: %v", err)
	}
	if _, err := documents.MarkFailed(ctx, failing.ID, 0, json.RawMessage(`{"stage":"embed"}`), "上游返回 429"); err != nil {
		t.Fatalf("标记失败状态出错: %v", err)
	}

	failed, err := records.GetByID(ctx, failingRecord.ID)
	if err != nil {
		t.Fatalf("回读记录失败: %v", err)
	}
	if failed.Status != entity.KnowledgeUploadRecordStatusFailed {
		t.Errorf("失败后记录状态 = %q，期望 failed", failed.Status)
	}
	if failed.ErrorMessage == nil || *failed.ErrorMessage != "上游返回 429" {
		t.Errorf("记录的失败原因 = %v，期望 上游返回 429", failed.ErrorMessage)
	}

	// 手动录入（没有上传记录）走同一条路径时不该报错，也不该改到别人的记录。
	manual := uploadRecordTestDocument(t, tx, entity.KnowledgeDocumentStatusPending)
	if err := documents.MarkProcessing(ctx, manual.ID); err != nil {
		t.Fatalf("推进状态失败: %v", err)
	}
	if _, err := documents.MarkFailed(ctx, manual.ID, 0, json.RawMessage(`{"stage":"chunk"}`), "空正文"); err != nil {
		t.Fatalf("没有记录时标记失败不该出错: %v", err)
	}
	again, err := records.GetByID(ctx, record.ID)
	if err != nil || again.Status != entity.KnowledgeUploadRecordStatusReady {
		t.Errorf("别的记录被误改: %+v (err=%v)", again, err)
	}
}

// 删记录不该动已经收录成功的文档：那条记录从此成为"已收录后删除"的历史。
func TestUploadRecordDeleteKeepsReadyDocument(t *testing.T) {
	tx := testTx(t)
	documents := NewKnowledgeDocumentRepository(tx)
	records := NewKnowledgeUploadRecordRepository(tx)
	ctx := context.Background()

	modelID := knowledgeTestModelID(t, tx)
	document := uploadRecordTestDocument(t, tx, entity.KnowledgeDocumentStatusPending)
	record := uploadRecordTestRecord(t, tx, document.ID)
	if err := documents.MarkProcessing(ctx, document.ID); err != nil {
		t.Fatalf("推进状态失败: %v", err)
	}
	if err := documents.ReplaceChunks(ctx, document.ID, knowledgeTestReplacement(document.ID, modelID, 2)); err != nil {
		t.Fatalf("替换切片失败: %v", err)
	}

	if err := records.DeleteRecord(ctx, record.ID); err != nil {
		t.Fatalf("删除记录失败: %v", err)
	}

	if _, err := records.GetByID(ctx, record.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("记录应当已被删除，实际 err = %v", err)
	}
	if _, err := documents.GetByID(ctx, document.ID); err != nil {
		t.Errorf("已收录的文档不该被连带删除: %v", err)
	}
	if left := countRows(t, tx, &entity.KnowledgeChunk{}, "document_id = ?", document.ID); left != 2 {
		t.Errorf("切片不该受影响，实际剩 %d 条", left)
	}
}

// 删记录要连带删掉那份还没收录成功的文档：它占着收录队列的名额（CountActive），
// 留着会让容量迟迟不释放，而删掉记录之后用户再也没有入口清掉它。
func TestUploadRecordDeleteCascadesUnfinishedDocument(t *testing.T) {
	tx := testTx(t)
	documents := NewKnowledgeDocumentRepository(tx)
	records := NewKnowledgeUploadRecordRepository(tx)
	ctx := context.Background()

	modelID := knowledgeTestModelID(t, tx)
	// 先收录成功、再打回 processing：这样库里有一份"未就绪但带着切片"的文档，
	// 顺手验证级联删除把切片也带走了。
	document := uploadRecordTestDocument(t, tx, entity.KnowledgeDocumentStatusPending)
	record := uploadRecordTestRecord(t, tx, document.ID)
	if err := documents.MarkProcessing(ctx, document.ID); err != nil {
		t.Fatalf("推进状态失败: %v", err)
	}
	if err := documents.ReplaceChunks(ctx, document.ID, knowledgeTestReplacement(document.ID, modelID, 2)); err != nil {
		t.Fatalf("替换切片失败: %v", err)
	}
	if err := documents.MarkProcessing(ctx, document.ID); err != nil {
		t.Fatalf("推进状态失败: %v", err)
	}

	if err := records.DeleteRecord(ctx, record.ID); err != nil {
		t.Fatalf("删除记录失败: %v", err)
	}

	if _, err := documents.GetByID(ctx, document.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("未就绪的文档应当被连带删除，实际 err = %v", err)
	}
	if left := countRows(t, tx, &entity.KnowledgeChunk{}, "document_id = ?", document.ID); left != 0 {
		t.Errorf("文档删了，切片还剩 %d 条", left)
	}
}

// 文档被删后记录留着、document_id 被置空 —— 这正是界面上「已收录后删除」
// 这个展示态的来源。靠的是外键的 ON DELETE SET NULL，只有真库能证明。
func TestUploadRecordSurvivesDocumentDeletion(t *testing.T) {
	tx := testTx(t)
	documents := NewKnowledgeDocumentRepository(tx)
	records := NewKnowledgeUploadRecordRepository(tx)
	ctx := context.Background()

	modelID := knowledgeTestModelID(t, tx)
	document := uploadRecordTestDocument(t, tx, entity.KnowledgeDocumentStatusPending)
	record := uploadRecordTestRecord(t, tx, document.ID)
	if err := documents.MarkProcessing(ctx, document.ID); err != nil {
		t.Fatalf("推进状态失败: %v", err)
	}
	if err := documents.ReplaceChunks(ctx, document.ID, knowledgeTestReplacement(document.ID, modelID, 1)); err != nil {
		t.Fatalf("替换切片失败: %v", err)
	}

	// 从主页删掉这份知识，而不是从上传记录删。
	if err := documents.Delete(ctx, document.ID); err != nil {
		t.Fatalf("删除文档失败: %v", err)
	}

	survived, err := records.GetByID(ctx, record.ID)
	if err != nil {
		t.Fatalf("文档删了，投递历史不该跟着消失: %v", err)
	}
	if survived.DocumentID != nil {
		t.Errorf("document_id 应当被外键置空，实际 %d", *survived.DocumentID)
	}
	// 状态刻意留在 ready：它是"当时收录成功了"这个事实，与文档还在不在无关。
	if survived.Status != entity.KnowledgeUploadRecordStatusReady {
		t.Errorf("状态应当仍停在 ready，实际 %q", survived.Status)
	}

	views, _, err := records.List(ctx, 0, 10)
	if err != nil {
		t.Fatalf("列表查询失败: %v", err)
	}
	for _, view := range views {
		if view.ID == record.ID && view.DocumentTitle != "" {
			t.Errorf("文档已删除，联表标题应当为空（由服务层回落到原始文件名），实际 %q", view.DocumentTitle)
		}
	}
}
