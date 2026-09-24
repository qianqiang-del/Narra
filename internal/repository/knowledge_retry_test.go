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
	// 真实链路里 MarkFailed 只会作用在 processing 的行上（worker Claim 之后），
	// 仓储的守卫据此把"已删除/已推走"的行挡在外面。
	if err := repo.MarkProcessing(ctx, document.ID); err != nil {
		t.Fatalf("推进到 processing 失败: %v", err)
	}

	failure := knowledgeTestFailure(t, "parse", "No module named 'scipy'")
	if _, err := repo.MarkFailed(ctx, document.ID, 0, failure, "解析失败"); err != nil {
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

	if err := repo.MarkProcessing(ctx, document.ID); err != nil {
		t.Fatalf("推进到 processing 失败: %v", err)
	}
	failure := knowledgeTestFailure(t, "parse", "解析器不可用")
	if _, err := repo.MarkFailed(ctx, document.ID, 0, failure, "解析失败"); err != nil {
		t.Fatalf("标记失败状态失败: %v", err)
	}

	archived := knowledgeTestArchivedPath(document.ID)
	applied, err := repo.SetUploadPath(ctx, document.ID, 0, archived)
	if err != nil {
		t.Fatalf("更新 upload_path 失败: %v", err)
	}
	if !applied {
		t.Fatal("仍持有失败租约时应当更新成功")
	}

	metadata := knowledgeTestMetadata(t, repo, document.ID)
	if metadata["upload_path"] != archived {
		t.Errorf("upload_path 没更新：期望 %q，实际 %v", archived, metadata["upload_path"])
	}
	if metadata["stage"] != "parse" || metadata["error"] != "解析器不可用" {
		t.Errorf("SetUploadPath 不该动失败现场，实际 %v", metadata)
	}
}

// 用户点了重试（或新一轮已经接手）之后，旧执行者的归档不得再改指针。
// 两个判据各验一次：租约编号对不上、状态已经不是 failed。
func TestKnowledgeRetrySetUploadPathRejectsStaleLease(t *testing.T) {
	tx := testTx(t)
	repo := NewKnowledgeDocumentRepository(tx)
	ctx := context.Background()

	document := knowledgeTestDocument()
	if err := repo.Create(ctx, document); err != nil {
		t.Fatalf("创建文档失败: %v", err)
	}

	staged := knowledgeTestStagedPath(document.ID)
	payload, err := json.Marshal(map[string]any{"upload_path": staged})
	if err != nil {
		t.Fatalf("构造 metadata 失败: %v", err)
	}
	if err := repo.SetMetadata(ctx, document.ID, payload); err != nil {
		t.Fatalf("写入 metadata 失败: %v", err)
	}
	if err := repo.MarkProcessing(ctx, document.ID); err != nil {
		t.Fatalf("推进到 processing 失败: %v", err)
	}
	failure := knowledgeTestFailure(t, "parse", "解析器不可用")
	if _, err := repo.MarkFailed(ctx, document.ID, 0, failure, "解析失败"); err != nil {
		t.Fatalf("标记失败状态失败: %v", err)
	}

	owned, err := repo.FailedLeaseOwned(ctx, document.ID, 0)
	if err != nil || !owned {
		t.Fatalf("失败现场写下后应当持有租约: owned=%v err=%v", owned, err)
	}

	// 编号对不上：拒绝。
	archived := knowledgeTestArchivedPath(document.ID)
	applied, err := repo.SetUploadPath(ctx, document.ID, 7, archived)
	if err != nil {
		t.Fatalf("更新 upload_path 出错: %v", err)
	}
	if applied {
		t.Error("租约编号不符时不该更新 upload_path")
	}

	// 用户重试：状态回到 pending，租约不再属于旧执行者。
	requeued, err := repo.Requeue(ctx, document.ID, entity.KnowledgeDocumentStageParse)
	if err != nil || !requeued {
		t.Fatalf("重新排队失败: requeued=%v err=%v", requeued, err)
	}
	owned, err = repo.FailedLeaseOwned(ctx, document.ID, 0)
	if err != nil {
		t.Fatalf("核对失败租约出错: %v", err)
	}
	if owned {
		t.Error("重试之后不该再持有失败租约")
	}

	applied, err = repo.SetUploadPath(ctx, document.ID, 0, archived)
	if err != nil {
		t.Fatalf("更新 upload_path 出错: %v", err)
	}
	if applied {
		t.Error("重试之后不该再更新 upload_path")
	}

	metadata := knowledgeTestMetadata(t, repo, document.ID)
	if metadata["upload_path"] != staged {
		t.Errorf("指针不该被旧执行者改动：期望 %q，实际 %v", staged, metadata["upload_path"])
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

	if err := repo.MarkProcessing(ctx, document.ID); err != nil {
		t.Fatalf("推进到 processing 失败: %v", err)
	}
	failure := knowledgeTestFailure(t, "parse", "解析器不可用")
	if _, err := repo.MarkFailed(ctx, document.ID, 0, failure, "解析失败"); err != nil {
		t.Fatalf("标记失败状态失败: %v", err)
	}

	requeued, err := repo.Requeue(ctx, document.ID, entity.KnowledgeDocumentStageChunk)
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
	// 阶段被改写成本次算出的恢复点，而不是保留失败时的那个。
	if reloaded.IngestStage == nil || *reloaded.IngestStage != entity.KnowledgeDocumentStageChunk {
		t.Errorf("重试后阶段应为 chunk，实际 %v", reloaded.IngestStage)
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

	again, err := repo.Requeue(ctx, document.ID, entity.KnowledgeDocumentStageParse)
	if err != nil {
		t.Fatalf("重复重试不该报错: %v", err)
	}
	if again {
		t.Error("已经不是失败状态了，重复重试应当返回 false")
	}
}

// 不在 processing 的行不能被写成 failed：用户在处理期间把文档/记录删了时，
// 这里必须整笔回滚并报错，而不是留下一份"失败现场"（那行已经不存在了），
// 或者把一份已经 ready 的文档改回 failed。
func TestKnowledgeMarkFailedRejectsDocumentNotProcessing(t *testing.T) {
	tx := testTx(t)
	repo := NewKnowledgeDocumentRepository(tx)
	ctx := context.Background()

	document := knowledgeTestDocument() // 停在 pending
	if err := repo.Create(ctx, document); err != nil {
		t.Fatalf("创建文档失败: %v", err)
	}
	before, err := repo.GetByID(ctx, document.ID)
	if err != nil {
		t.Fatalf("回读文档失败: %v", err)
	}

	failure := knowledgeTestFailure(t, "parse", "解析器不可用")
	applied, err := repo.MarkFailed(ctx, document.ID, 0, failure, "解析失败")
	if err != nil {
		t.Fatalf("未命中处理中的行不该返回数据库错误: %v", err)
	}
	if applied {
		t.Fatal("文档不在 processing 时不该写入失败现场")
	}

	after, err := repo.GetByID(ctx, document.ID)
	if err != nil {
		t.Fatalf("再次回读文档失败: %v", err)
	}
	if after.Status != before.Status {
		t.Errorf("状态被改动了：%q → %q", before.Status, after.Status)
	}

	var metadata map[string]any
	if err := json.Unmarshal(after.Metadata, &metadata); err != nil {
		t.Fatalf("解析 metadata 失败: %v", err)
	}
	if _, ok := metadata["stage"]; ok {
		t.Errorf("失败现场不该被写入，实际 metadata: %v", metadata)
	}
}
