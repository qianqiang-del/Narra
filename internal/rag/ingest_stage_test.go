package rag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"narra/internal/model/entity"
	"narra/pkg/documentparser"
)

// 分阶段收录的恢复用例：中间结果落库之后，重试该从哪一步继续、用的是什么输入。
// 这些用例不连数据库也不调向量服务，验的是编排本身：阶段推进、失败停在原地、
// metadata 合并、以及"不该重新解析原文件"。

// countingParser 记录解析被调用了几次。恢复用例靠它断言"没有重新读原文件"。
type countingParser struct {
	calls    int
	markdown string
	err      error
}

var _ documentparser.Parser = (*countingParser)(nil)

func (p *countingParser) Parse(context.Context, documentparser.Request) (*documentparser.Result, error) {
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	return &documentparser.Result{Markdown: p.markdown}, nil
}

func (p *countingParser) Status(context.Context) documentparser.Status {
	return documentparser.Status{Ready: true, Source: "counting"}
}

// stageTestDocument 造一条"正在处理中"的文档，带指定阶段与 metadata。
func stageTestDocument(t *testing.T, stage string, metadata map[string]any) entity.KnowledgeDocument {
	t.Helper()

	payload, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("构造 metadata 失败: %v", err)
	}
	stageCopy := stage
	document := entity.KnowledgeDocument{
		Title:       "阶段测试文档",
		SourceType:  testDocumentSource,
		Enabled:     true,
		Status:      entity.KnowledgeDocumentStatusProcessing,
		IngestStage: &stageCopy,
		Metadata:    payload,
	}
	document.ID = testDocumentID
	return document
}

// stageParserFile 造一份需要 Python 解析器的暂存文件（.docx）。
// md / txt 会走内置纯文本解析器、绕过测试桩，恢复用例要的是可控的解析器。
func stageParserFile(t *testing.T, root, name, content string) string {
	t.Helper()

	directory := filepath.Join(root, "pending", name)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("创建暂存目录失败: %v", err)
	}
	path := filepath.Join(directory, "upload.docx")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入暂存文件失败: %v", err)
	}
	return path
}

// newStageStore 造一个"库里已经有这一行"的替身：processing、租约编号 1，
// 且 metadata 与文档行同源（真库只有一份，替身也不能分成两处）。
func newStageStore(document *entity.KnowledgeDocument) *fakeDocumentStore {
	stage := entity.KnowledgeDocumentStageParse
	if document.IngestStage != nil {
		stage = *document.IngestStage
	}
	return &fakeDocumentStore{
		created:    document,
		processing: true,
		attempt:    1,
		metadata:   document.Metadata,
		stage:      stage,
	}
}

// stagedChunks 造 n 个"已经落库"的切片，带自增 ID（向量要挂上去）。
func stagedChunks(count int) []entity.KnowledgeChunk {
	chunks := make([]entity.KnowledgeChunk, count)
	for index := range chunks {
		content := fmt.Sprintf("第 %d 段已经落库的正文内容。", index+1)
		chunks[index] = entity.KnowledgeChunk{
			BaseModel:      entity.BaseModel{ID: uint64(index + 1)},
			DocumentID:     testDocumentID,
			ChunkIndex:     int32(index),
			Content:        content,
			CharacterCount: int32(len([]rune(content))),
			Metadata:       json.RawMessage(`{}`),
		}
	}
	return chunks
}

// 解析成功后写库失败（或进程被杀）：正文与阶段必须已经落库，恢复从 chunk 继续，
// 不再重新解析原文件；而 upload_path 必须活过这次阶段写入 ——
// 否则下一轮连原件都找不到（这正是阶段写入必须做 metadata 合并的原因）。
func TestStagedResumeFromChunkKeepsUploadPath(t *testing.T) {
	root := t.TempDir()
	staged := stageParserFile(t, root, "1", "内容由桩决定")
	document := stageTestDocument(t, entity.KnowledgeDocumentStageParse, map[string]any{
		"upload_path": staged,
	})
	store := newStageStore(&document)
	parser := &countingParser{markdown: "# 崩溃恢复\n\n" + strings.Repeat("正文段落。", 80)}
	ingester := newIngesterWithParser(store, parser)
	ctx := context.Background()

	// 第一次处理：解析成功、正文落库，但切片写入失败。
	store.chunksSaveErr = fmt.Errorf("数据库连接断开")
	if _, err := ingester.processExistingFile(ctx, &document, FileInput{Path: staged}, 1, entity.KnowledgeDocumentStageParse); err == nil {
		t.Fatal("切片写入失败时整体应当失败")
	}
	if parser.calls != 1 {
		t.Fatalf("第一次处理应当解析一次，实际 %d 次", parser.calls)
	}
	if store.stage != entity.KnowledgeDocumentStageChunk {
		t.Fatalf("解析成功后阶段应当推进到 chunk，实际 %q", store.stage)
	}
	if strings.TrimSpace(store.content) == "" {
		t.Fatal("解析产物应当已经落库")
	}
	if !strings.Contains(string(store.metadata), "upload_path") {
		t.Fatalf("阶段写入抹掉了 upload_path，崩溃后找不到原件：%s", store.metadata)
	}
	if store.failMeta == nil {
		t.Fatal("租约有效时失败现场应当写入")
	}

	// 用户点重试：清失败现场、重新认领（编号递增），从 chunk 继续。
	store.chunksSaveErr = nil
	store.failMeta = nil
	store.failReason = ""
	store.attempt = 2
	document.Status = entity.KnowledgeDocumentStatusProcessing
	chunkStage := entity.KnowledgeDocumentStageChunk
	document.IngestStage = &chunkStage

	result, err := ingester.processExistingFile(ctx, &document, FileInput{Path: staged}, 2, entity.KnowledgeDocumentStageChunk)
	if err != nil {
		t.Fatalf("从 chunk 恢复失败: %v", err)
	}
	if parser.calls != 1 {
		t.Errorf("从 chunk 恢复不该重新解析原文件，解析次数 = %d", parser.calls)
	}
	if result.Document.Status != entity.KnowledgeDocumentStatusReady {
		t.Errorf("状态 = %q，期望 ready", result.Document.Status)
	}
	if store.stage != "" {
		t.Errorf("成功后阶段应当清空，实际 %q", store.stage)
	}
	if len(store.embeddings) == 0 {
		t.Error("向量应当已经写入")
	}
}

// 切片已落库、向量化失败（如上游 429）：重试从 embed 继续。
// 原文件已经丢了也不影响 —— 输入在库里，这正是分阶段收录的核心收益。
func TestStagedResumeFromEmbedWithoutOriginalFile(t *testing.T) {
	// metadata 里没有 upload_path：原件已经不在服务器上。
	document := stageTestDocument(t, entity.KnowledgeDocumentStageEmbed, map[string]any{})
	store := newStageStore(&document)
	store.chunks = stagedChunks(3)
	parser := &countingParser{err: fmt.Errorf("embed 阶段不该调用解析器")}
	ingester := newIngesterWithParser(store, parser)
	ctx := context.Background()

	store.embedSaveErr = fmt.Errorf("上游返回 429")
	if _, err := ingester.processExistingFile(ctx, &document, FileInput{}, 1, entity.KnowledgeDocumentStageEmbed); err == nil {
		t.Fatal("向量写入失败时整体应当失败")
	}
	if store.stage != entity.KnowledgeDocumentStageEmbed {
		t.Errorf("失败应当停在 embed，实际 %q", store.stage)
	}

	store.embedSaveErr = nil
	store.failMeta = nil
	store.attempt = 2
	result, err := ingester.processExistingFile(ctx, &document, FileInput{}, 2, entity.KnowledgeDocumentStageEmbed)
	if err != nil {
		t.Fatalf("从 embed 恢复失败: %v", err)
	}
	if parser.calls != 0 {
		t.Errorf("从 embed 恢复一次都不该解析原文件，实际 %d 次", parser.calls)
	}
	if result.Document.Status != entity.KnowledgeDocumentStatusReady {
		t.Errorf("状态 = %q，期望 ready", result.Document.Status)
	}
	if len(store.embeddings) != len(store.chunks) {
		t.Errorf("向量数 = %d，期望 %d（与切片一一对应）", len(store.embeddings), len(store.chunks))
	}
}

// embed 阶段但切片缺失（人工删过、或数据异常）：必须明确失败，不能静默完成 ——
// 静默完成会写出一个"ready 但没有向量"的文档，检索永远漏掉它。
func TestStagedEmbedWithoutChunksFailsExplicitly(t *testing.T) {
	document := stageTestDocument(t, entity.KnowledgeDocumentStageEmbed, map[string]any{})
	store := newStageStore(&document)
	ingester := newIngesterWithParser(store, &countingParser{markdown: "不该被解析"})

	if _, err := ingester.processExistingFile(context.Background(), &document, FileInput{}, 1, entity.KnowledgeDocumentStageEmbed); err == nil {
		t.Fatal("没有切片时必须明确失败")
	}
	if store.ready {
		t.Error("不能静默把文档标成 ready")
	}
	if store.stage != entity.KnowledgeDocumentStageEmbed {
		t.Errorf("失败后阶段应当停在 embed，实际 %q", store.stage)
	}
	if store.failMeta == nil {
		t.Fatal("失败现场应当写入")
	}
	var payload struct {
		Stage string `json:"stage"`
	}
	if err := json.Unmarshal(store.failMeta, &payload); err != nil {
		t.Fatalf("失败现场不是合法 JSON: %v", err)
	}
	if payload.Stage != "embed" {
		t.Errorf("失败阶段 = %q，期望 embed", payload.Stage)
	}
}

// chunk 阶段但正文缺失：同样明确失败，提示重新上传。
func TestStagedChunkWithoutContentFailsExplicitly(t *testing.T) {
	document := stageTestDocument(t, entity.KnowledgeDocumentStageChunk, map[string]any{})
	store := newStageStore(&document)
	ingester := newIngesterWithParser(store, &countingParser{markdown: "不该被解析"})

	if _, err := ingester.processExistingFile(context.Background(), &document, FileInput{}, 1, entity.KnowledgeDocumentStageChunk); err == nil {
		t.Fatal("没有正文时必须明确失败")
	}
	if store.stage != entity.KnowledgeDocumentStageChunk {
		t.Errorf("失败后阶段应当停在 chunk，实际 %q", store.stage)
	}
	if store.content != "" {
		t.Error("失败时不该写入正文")
	}
}

// Worker 的路径校验只对 parse 阶段强制：embed 重试不需要原文件，
// 没有 upload_path 也必须能继续跑完。
func TestProcessOneSkipsPathCheckForEmbedRetry(t *testing.T) {
	root := t.TempDir()
	document := stageTestDocument(t, entity.KnowledgeDocumentStageEmbed, map[string]any{})
	store := newStageStore(&document)
	store.stage = entity.KnowledgeDocumentStageEmbed
	store.chunks = stagedChunks(2)
	ingester := newIngesterWithParser(store, &countingParser{err: fmt.Errorf("不该解析")})
	worker := NewWorker(store, ingester, root, 1)

	worker.processOne(context.Background(), document, 1)

	if store.failReason == unusablePathReason {
		t.Fatal("embed 阶段重试不该因为原件不在被判路径无效")
	}
	if store.status() != entity.KnowledgeDocumentStatusReady {
		t.Errorf("状态 = %q，期望 ready", store.status())
	}
}

// 失败现场没落库（这里是租约已失效）时，Worker 一个字节都不该动原件：
// 归档会把新一轮正在用的输入挪走，删除会让它彻底失去输入。
func TestProcessOneKeepsFileWhenFailureNotRecorded(t *testing.T) {
	root := t.TempDir()
	staged := stageParserFile(t, root, "1", "内容由桩决定")
	document := stageTestDocument(t, entity.KnowledgeDocumentStageParse, map[string]any{
		"upload_path": staged,
	})
	// attempt = 9 表示这一行已经被重新认领过：本次传入的 1 是旧租约。
	store := newStageStore(&document)
	store.attempt = 9
	ingester := newIngesterWithParser(store, &stubParser{err: fmt.Errorf("解析失败")})
	worker := NewWorker(store, ingester, root, 1)

	worker.processOne(context.Background(), document, 1)

	if store.failMeta != nil {
		t.Error("旧租约不该写入失败现场")
	}
	if _, err := os.Stat(staged); err != nil {
		t.Errorf("失败现场没落库时不该移动原件: %v", err)
	}
	if _, err := os.Stat(archiveDir(root, testDocumentID)); !os.IsNotExist(err) {
		t.Error("不该产生无人认领的归档目录")
	}
}

// failureRecorded 只看"失败现场有没有落库"，并且不破坏原始错误的可判定性。
func TestFailureRecorded(t *testing.T) {
	if !failureRecorded(&ingestFailure{cause: errors.New("x"), applied: true}) {
		t.Error("applied = true 时应当算已记录")
	}
	if failureRecorded(&ingestFailure{cause: errors.New("x"), applied: false}) {
		t.Error("applied = false 时不该算已记录")
	}
	if failureRecorded(errors.New("x")) {
		t.Error("普通错误不该算已记录")
	}
	if !errors.Is(&ingestFailure{cause: context.Canceled, applied: false}, context.Canceled) {
		t.Error("包装之后原始错误必须仍然可判定")
	}
}

// 同步录入仍然是一次事务落三张表：成功后阶段必须清空（ready ⟺ 阶段为 NULL）。
func TestIngestTextClearsStageOnSuccess(t *testing.T) {
	store := &fakeDocumentStore{}
	ingester := newTestIngester(store, &stubEmbedder{dimension: testVectorDims})

	result, err := ingester.IngestText(context.Background(), TextInput{
		Content: strings.Repeat("正文内容。", 40),
	})
	if err != nil {
		t.Fatalf("收录失败: %v", err)
	}
	if result.Document.Status != entity.KnowledgeDocumentStatusReady {
		t.Errorf("状态 = %q，期望 ready", result.Document.Status)
	}
	if store.stage != "" {
		t.Errorf("ready 文档的阶段应当清空，实际 %q", store.stage)
	}
	if store.replaced == nil {
		t.Error("同步链路应当走一次成型的 ReplaceChunks")
	}
}

// 恢复点规则只有一份：按现实材料从后往前找，找到哪一步就从哪一步继续。
func TestResolveRecoveryStage(t *testing.T) {
	cases := []struct {
		name     string
		material RecoveryMaterial
		want     string
		wantErr  error
	}{
		{
			name:     "三样都在：直接重向量化",
			material: RecoveryMaterial{HasChunks: true, HasContent: true, HasOriginal: true},
			want:     entity.KnowledgeDocumentStageEmbed,
		},
		{
			name:     "切片没了但有正文：退回分块",
			material: RecoveryMaterial{HasContent: true, HasOriginal: true},
			want:     entity.KnowledgeDocumentStageChunk,
		},
		{
			name:     "只剩原件：退回解析",
			material: RecoveryMaterial{HasOriginal: true},
			want:     entity.KnowledgeDocumentStageParse,
		},
		{
			name:     "都没了：请重新上传",
			material: RecoveryMaterial{},
			wantErr:  ErrRecoveryInputMissing,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveRecoveryStage(tc.material)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("期望 %v，实际 %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("解析恢复点失败: %v", err)
			}
			if got != tc.want {
				t.Errorf("恢复点 = %q，期望 %q", got, tc.want)
			}
		})
	}
}

// 阶段说 embed，但切片已经没了（人工动过库）：不回退就是死胡同，
// 应当退到 chunk 重新分块，不必重新解析原文件。
func TestProcessOneFallsBackFromEmbedToChunk(t *testing.T) {
	document := stageTestDocument(t, entity.KnowledgeDocumentStageEmbed, map[string]any{})
	document.Content = "# 标题\n\n" + strings.Repeat("已经落库的正文。", 60)
	store := newStageStore(&document)
	parser := &countingParser{err: fmt.Errorf("退回 chunk 不该重新解析")}
	ingester := newIngesterWithParser(store, parser)
	worker := NewWorker(store, ingester, t.TempDir(), 1)

	worker.processOne(context.Background(), document, 1)

	if store.status() != entity.KnowledgeDocumentStatusReady {
		t.Fatalf("状态 = %q，期望 ready（应当退回 chunk 并跑完）", store.status())
	}
	if parser.calls != 0 {
		t.Errorf("退回 chunk 不该解析原文件，实际 %d 次", parser.calls)
	}
	if len(store.embeddings) == 0 {
		t.Error("向量应当写入")
	}
}

// 阶段说 embed，但切片与正文都没了：还能退到 parse，重新读服务器上的原件 ——
// 比"请重新上传"对用户友好得多。
func TestProcessOneFallsBackFromEmbedToParse(t *testing.T) {
	root := t.TempDir()
	staged := stageParserFile(t, root, "1", "内容由桩决定")
	document := stageTestDocument(t, entity.KnowledgeDocumentStageEmbed, map[string]any{
		"upload_path": staged,
	})
	store := newStageStore(&document)
	parser := &countingParser{markdown: "# 兜底解析\n\n" + strings.Repeat("正文段落。", 60)}
	ingester := newIngesterWithParser(store, parser)
	worker := NewWorker(store, ingester, root, 1)

	worker.processOne(context.Background(), document, 1)

	if store.status() != entity.KnowledgeDocumentStatusReady {
		t.Fatalf("状态 = %q，期望 ready（应当退回 parse 并跑完）", store.status())
	}
	if parser.calls != 1 {
		t.Errorf("退回 parse 应当解析一次原文件，实际 %d 次", parser.calls)
	}
}

// 原件、正文、切片一个都不剩：回退链到底了，落 failed 并请用户重新上传。
func TestProcessOneFailsWhenNothingLeft(t *testing.T) {
	document := stageTestDocument(t, entity.KnowledgeDocumentStageEmbed, map[string]any{})
	store := newStageStore(&document)
	ingester := newIngesterWithParser(store, &countingParser{})
	worker := NewWorker(store, ingester, t.TempDir(), 1)

	worker.processOne(context.Background(), document, 1)

	if store.created == nil || store.created.Status != entity.KnowledgeDocumentStatusFailed {
		t.Fatalf("材料全无时应当落 failed，实际 %q", store.status())
	}
	if !strings.Contains(store.failReason, "重新上传") {
		t.Errorf("给用户的原因应当提示重新上传，实际 %q", store.failReason)
	}
}
