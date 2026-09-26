package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"narra/internal/model/entity"
)

// 这些用例跑真库，验证收录提交的"三行同进同出"：
// rag.Ingester.SubmitFile 通过 TransactionManager 在一个事务里调用三个仓储方法，
// 事务句柄靠 ctx 传递（repository.conn）。用内存替身测不出"某个方法漏掉 conn 会怎样"，
// 那正是这里要钉住的：
//   - 全部提交：文档、metadata、上传记录都落库；
//   - 中途失败：三张表一行都不留，不会产生没有 upload_path 的 pending 孤儿行；
//   - 队列锁：AcquireIngestQueueLock 在事务内可执行（pg_advisory_xact_lock 的真实语法）。
//
// 本地运行：
//
//	NARRA_TEST_DSN="host=localhost port=5432 user=postgres password=*** dbname=narra sslmode=disable" \
//	    go test ./internal/repository/ -run KnowledgeIngest -v
func TestKnowledgeIngestCommitWritesAllRows(t *testing.T) {
	tx := testTx(t)
	docRepo := NewKnowledgeDocumentRepository(tx)
	recordRepo := NewKnowledgeUploadRecordRepository(tx)
	txManager := NewTransactionManager(tx)
	ctx := context.Background()

	var documentID uint64
	err := txManager.Run(ctx, func(ctx context.Context) error {
		if err := docRepo.AcquireIngestQueueLock(ctx); err != nil {
			return fmt.Errorf("取得队列锁失败: %w", err)
		}
		document := knowledgeTestDocument()
		if err := docRepo.Create(ctx, document); err != nil {
			return err
		}
		metadata, err := json.Marshal(map[string]any{
			"upload_path":    "C:/uploads/pending/abc/upload.md",
			"explicit_title": true,
		})
		if err != nil {
			return err
		}
		if err := docRepo.SetMetadata(ctx, document.ID, metadata); err != nil {
			return err
		}
		record := &entity.KnowledgeUploadRecord{
			DocumentID:   &document.ID,
			OriginalName: "设计.md",
			Status:       entity.KnowledgeUploadRecordStatusPending,
		}
		if err := recordRepo.CreateUploadRecord(ctx, record); err != nil {
			return err
		}
		documentID = document.ID
		return nil
	})
	if err != nil {
		t.Fatalf("提交事务失败: %v", err)
	}

	document, err := docRepo.GetByID(ctx, documentID)
	if err != nil {
		t.Fatalf("回读文档失败: %v", err)
	}
	var metadata map[string]any
	if err := json.Unmarshal(document.Metadata, &metadata); err != nil {
		t.Fatalf("metadata 不是 JSON: %v", err)
	}
	if metadata["upload_path"] == nil {
		t.Errorf("提交成功后 upload_path 必须落库，实际 metadata=%s", document.Metadata)
	}

	if got := countRows(t, tx, &entity.KnowledgeUploadRecord{}, "document_id = ?", documentID); got != 1 {
		t.Errorf("上传记录数 = %d，期望 1", got)
	}
}

// 事务中途失败时必须整体回滚：文档行、metadata、上传记录一行都不留。
// 这正是"建记录失败会让整个提交失败"的数据库侧证据。
func TestKnowledgeIngestRollbackLeavesNothing(t *testing.T) {
	tx := testTx(t)
	docRepo := NewKnowledgeDocumentRepository(tx)
	recordRepo := NewKnowledgeUploadRecordRepository(tx)
	txManager := NewTransactionManager(tx)
	ctx := context.Background()

	title := uniqueName("kb-rollback")
	sentinel := fmt.Errorf("模拟上传记录写入失败")

	var attemptedID uint64
	err := txManager.Run(ctx, func(ctx context.Context) error {
		if err := docRepo.AcquireIngestQueueLock(ctx); err != nil {
			return err
		}
		document := knowledgeTestDocument()
		document.Title = title
		if err := docRepo.Create(ctx, document); err != nil {
			return err
		}
		attemptedID = document.ID
		if err := docRepo.SetMetadata(ctx, document.ID, json.RawMessage(`{"upload_path":"x"}`)); err != nil {
			return err
		}
		record := &entity.KnowledgeUploadRecord{
			DocumentID:   &document.ID,
			OriginalName: "设计.md",
			Status:       entity.KnowledgeUploadRecordStatusPending,
		}
		if err := recordRepo.CreateUploadRecord(ctx, record); err != nil {
			return err
		}
		// 三行都写完之后再失败：回滚必须把记录也带走。
		return sentinel
	})
	if err == nil {
		t.Fatal("模拟失败时事务必须返回错误")
	}

	if got := countRows(t, tx, &entity.KnowledgeDocument{}, "id = ?", attemptedID); got != 0 {
		t.Errorf("回滚后不该留下文档行，实际 %d 行", got)
	}
	if got := countRows(t, tx, &entity.KnowledgeDocument{}, "title = ?", title); got != 0 {
		t.Errorf("回滚后不该留下任何同标题文档，实际 %d 行", got)
	}
	if got := countRows(t, tx, &entity.KnowledgeUploadRecord{}, "document_id = ?", attemptedID); got != 0 {
		t.Errorf("回滚后不该留下上传记录，实际 %d 行", got)
	}
}
