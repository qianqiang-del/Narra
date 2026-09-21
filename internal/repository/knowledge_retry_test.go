package repository

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"narra/internal/model/entity"
)

// 这几个用例必须跑真库：MarkFailed / SetUploadPath / Requeue 各自要做一次 jsonb
// 层面的改写（顶层合并、删键、只换一个键），SQL 写得对不对用替身测不出来。
//
// 其中"失败现场不能覆盖 upload_path"是最要紧的一条 —— 那条链路上真出过事：
// 覆盖写让 metadata 里的上传路径消失之后，失败原件既没被归档、也永远不会被清理，
// 磁盘上就这么攒出了谁也认领不了的孤儿文件。
//
// 本地运行：
//
//	NARRA_TEST_DSN="host=localhost port=5432 user=postgres password=*** dbname=narra sslmode=disable" \
//	    go test ./internal/repository/ -run KnowledgeRetry -v

// knowledgeTestStagedPath 造一个形如 upload_path 的暂存路径。
func knowledgeTestStagedPath(documentID uint64) string {
	return "/tmp/narra-uploads/pending/" + strconv.FormatUint(documentID, 10) + "/upload.md"
}

// knowledgeTestArchivedPath 造一个形如归档后 upload_path 的路径。
func knowledgeTestArchivedPath(documentID uint64) string {
	return "/tmp/narra-uploads/failed/" + strconv.FormatUint(documentID, 10) + "/upload.md"
}

// knowledgeTestFailure 造一份收录链路的失败现场。
func knowledgeTestFailure(t *testing.T, stage, reason string) json.RawMessage {
	t.Helper()

	payload, err := json.Marshal(map[string]any{
		"stage":     stage,
		"error":     reason,
		"failed_at": "2026-09-21T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("构造失败现场失败: %v", err)
	}
	return payload
}

// knowledgeTestMetadata 回读一篇文档的 metadata，解成 map 方便逐键断言。
func knowledgeTestMetadata(t *testing.T, repo KnowledgeDocumentRepository, id uint64) map[string]any {
	t.Helper()

	document, err := repo.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("回读文档失败: %v", err)
	}

	var metadata map[string]any
	if err := json.Unmarshal(document.Metadata, &metadata); err != nil {
		t.Fatalf("解析 metadata 失败: %v（原文 %s）", err, document.Metadata)
	}
	return metadata
}

// MarkFailed 是合并写回：失败现场进去，收录链路的输入（upload_path /
// explicit_title）必须原样留着 —— 归档失败原件、删除时的清理、重试都要靠它们。
func TestKnowledgeRetryMarkFailedMergesMetadata(t *testing.T) {
	tx := testTx(t)
	repo := NewKnowledgeDocumentRepository(tx)
	ctx := context.Background()

	document := knowledgeTestDocument()
	if err := repo.Create(ctx, document); err != nil {
		t.Fatalf("创建文档失败: %v", err)
	}

	staged := knowledgeTestStagedPath(document.ID)
	payload, err := json.Marshal(map[string]any{"upload_path": staged, "explicit_title": false})
	if err != nil {
		t.Fatalf("构造 metadata 失败: %v", err)
	}
	if err := repo.SetMetadata(ctx, document.ID, payload); err != nil {
		t.Fatalf("写入 metadata 失败: %v", err)
	}

	failure := knowledgeTestFailure(t, "parse", "No module named 'scipy'")
	if err := repo.MarkFailed(ctx, document.ID, failure, "解析失败"); err != nil {
		t.Fatalf("标记失败状态失败: %v", err)
	}

	metadata := knowledgeTestMetadata(t, repo, document.ID)
	if metadata["upload_path"] != staged {
		t.Errorf("失败现场不该抹掉 upload_path：期望 %q，实际 %v", staged, metadata["upload_path"])
	}
	if explicit, ok := metadata["explicit_title"]; !ok || explicit != false {
		t.Errorf("失败现场不该抹掉 explicit_title：实际 %v（键存在：%v）", explicit, ok)
	}
	if metadata["stage"] != "parse" {
		t.Errorf("失败阶段没写进 metadata: %v", metadata)
	}
	if metadata["error"] != "No module named 'scipy'" {
		t.Errorf("失败原因没写进 metadata: %v", metadata)
	}
}

// SetUploadPath 只换一个键：原件归档之后指针要挪过去，而失败现场得留着 ——
// 用户还要在界面上看"上次为什么失败"。
func TestKnowledgeRetrySetUploadPathKeepsFailureDetail(t *testing.T) {
	tx := testTx(t)
	repo := NewKnowledgeDocumentRepository(tx)
	ctx := context.Background()

	document := knowledgeTestDocument()
	if err := repo.Create(ctx, document); err != nil {
		t.Fatalf("创建文档失败: %v", err)
	}

	failure := knowledgeTestFailure(t, "parse", "解析器不可用")
	if err := repo.MarkFailed(ctx, document.ID, failure, "解析失败"); err != nil {
		t.Fatalf("标记失败状态失败: %v", err)
	}

	archived := knowledgeTestArchivedPath(document.ID)
	if err := repo.SetUploadPath(ctx, document.ID, archived); err != nil {
		t.Fatalf("更新 upload_path 失败: %v", err)
	}

	metadata := knowledgeTestMetadata(t, repo, document.ID)
	if metadata["upload_path"] != archived {
		t.Errorf("upload_path 没更新：期望 %q，实际 %v", archived, metadata["upload_path"])
	}
	if metadata["stage"] != "parse" || metadata["error"] != "解析器不可用" {
		t.Errorf("SetUploadPath 不该动失败现场，实际 %v", metadata)
	}
}

// Requeue 是原地重试的写入口，四件事一起发生：文档回到 pending、失败现场被清掉
// （否则处理期间前端读到的还是上一轮那句话）、记录也回到 pending（抽屉里显示的是它）、
// 而 upload_path 必须留着 —— 它就是这次重试的输入。
//
// 再点一次要安静地输掉：状态已经不是 failed 了（并发点两次，或它已经在跑）。
func TestKnowledgeRetryRequeueResetsFailedDocumentAndRecord(t *testing.T) {
	tx := testTx(t)
	repo := NewKnowledgeDocumentRepository(tx)
	records := NewKnowledgeUploadRecordRepository(tx)
	ctx := context.Background()

	document := knowledgeTestDocument()
	if err := repo.Create(ctx, document); err != nil {
		t.Fatalf("创建文档失败: %v", err)
	}

	staged := knowledgeTestStagedPath(document.ID)
	payload, err := json.Marshal(map[string]any{"upload_path": staged, "explicit_title": true})
	if err != nil {
		t.Fatalf("构造 metadata 失败: %v", err)
	}
	if err := repo.SetMetadata(ctx, document.ID, payload); err != nil {
		t.Fatalf("写入 metadata 失败: %v", err)
	}

	record := &entity.KnowledgeUploadRecord{
		DocumentID:   &document.ID,
		OriginalName: "test.md",
		Status:       entity.KnowledgeUploadRecordStatusPending,
	}
	if err := records.CreateUploadRecord(ctx, record); err != nil {
		t.Fatalf("创建上传记录失败: %v", err)
	}

	failure := knowledgeTestFailure(t, "parse", "解析器不可用")
	if err := repo.MarkFailed(ctx, document.ID, failure, "解析失败"); err != nil {
		t.Fatalf("标记失败状态失败: %v", err)
	}

	requeued, err := repo.Requeue(ctx, document.ID)
	if err != nil {
		t.Fatalf("重新排队失败: %v", err)
	}
	if !requeued {
		t.Fatal("失败状态的文档应当能被重新排队")
	}

	reloaded, err := repo.GetByID(ctx, document.ID)
	if err != nil {
		t.Fatalf("回读文档失败: %v", err)
	}
	if reloaded.Status != entity.KnowledgeDocumentStatusPending {
		t.Errorf("重试后状态应为 pending，实际 %s", reloaded.Status)
	}

	metadata := knowledgeTestMetadata(t, repo, document.ID)
	for _, key := range []string{"stage", "error", "failed_at"} {
		if _, ok := metadata[key]; ok {
			t.Errorf("上一次的失败现场 %q 应当被清掉，实际 metadata: %v", key, metadata)
		}
	}
	if metadata["upload_path"] != staged {
		t.Errorf("重试的输入就是它，upload_path 不该丢：期望 %q，实际 %v", staged, metadata["upload_path"])
	}

	stored, err := records.GetByID(ctx, record.ID)
	if err != nil {
		t.Fatalf("回读上传记录失败: %v", err)
	}
	if stored.Status != entity.KnowledgeUploadRecordStatusPending {
		t.Errorf("记录应当跟着回到 pending，实际 %s", stored.Status)
	}
	if stored.ErrorMessage != nil {
		t.Errorf("记录的失败原因应当被清空，实际 %q", *stored.ErrorMessage)
	}

	again, err := repo.Requeue(ctx, document.ID)
	if err != nil {
		t.Fatalf("重复重试不该报错: %v", err)
	}
	if again {
		t.Error("已经不是失败状态了，重复重试应当返回 false")
	}
}
