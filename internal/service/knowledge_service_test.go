package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"gorm.io/gorm"

	requestdto "narra/internal/model/dto/request"
	"narra/internal/model/entity"
	"narra/internal/rag"
)

// 这个文件只测服务层的本职：把 entity 翻成 DTO、分页归一化、错误措辞。
// 收录链路的编排（状态怎么推进、失败时留下什么、切片与向量怎么对应）在
// internal/rag/ingest_test.go 里测，那边用 rag 自己的窄接口替身，不经过这里。

const (
	testDocumentID     = uint64(7)
	testDocumentSource = entity.KnowledgeDocumentSourceImport
)

// fakeDocumentQuerier 是 documentQuerier 的内存替身。
//
// 只有三个方法 —— 服务层已经看不见状态推进和切片替换了，替身也就不必实现它们。
type fakeDocumentQuerier struct {
	document *entity.KnowledgeDocument
	total    int64
	counts   map[uint64]int64
}

var _ documentQuerier = (*fakeDocumentQuerier)(nil)

func (q *fakeDocumentQuerier) List(ctx context.Context, offset, limit int) ([]entity.KnowledgeDocument, int64, error) {
	if q.document == nil {
		return nil, 0, nil
	}
	return []entity.KnowledgeDocument{*q.document}, q.total, nil
}

func (q *fakeDocumentQuerier) GetByID(ctx context.Context, id uint64) (*entity.KnowledgeDocument, error) {
	if q.document == nil || q.document.ID != id {
		return nil, gorm.ErrRecordNotFound
	}
	return q.document, nil
}

func (q *fakeDocumentQuerier) CountChunksByDocument(ctx context.Context, ids []uint64) (map[uint64]int64, error) {
	if q.counts != nil {
		return q.counts, nil
	}
	out := map[uint64]int64{}
	for _, id := range ids {
		out[id] = 0
	}
	return out, nil
}

// fakeIngester 是 ingester 的替身：只记录收到的输入，不真的切分与向量化。
type fakeIngester struct {
	fileInput rag.FileInput
	textInput rag.TextInput
	result    rag.IngestResult
	err       error
}

var _ ingester = (*fakeIngester)(nil)

func (f *fakeIngester) IngestFile(ctx context.Context, input rag.FileInput) (rag.IngestResult, error) {
	f.fileInput = input
	return f.result, f.err
}

func (f *fakeIngester) IngestText(ctx context.Context, input rag.TextInput) (rag.IngestResult, error) {
	f.textInput = input
	return f.result, f.err
}

func newTestService(querier *fakeDocumentQuerier, ingestion *fakeIngester) KnowledgeService {
	return NewKnowledgeService(querier, ingestion)
}

// TestIngestFileMapsRequestAndResult 校验 DTO 到收录输入、entity 到响应的两次映射。
func TestIngestFileMapsRequestAndResult(t *testing.T) {
	querier := &fakeDocumentQuerier{}
	ingestion := &fakeIngester{result: rag.IngestResult{
		Document: &entity.KnowledgeDocument{
			BaseModel:  entity.BaseModel{ID: testDocumentID},
			Title:      "设计文档",
			SourceType: testDocumentSource,
			Status:     entity.KnowledgeDocumentStatusReady,
			Content:    "正文内容",
			Metadata:   json.RawMessage(`{}`),
		},
		Chunks: 51,
	}}
	svc := newTestService(querier, ingestion)

	document, err := svc.IngestFile(context.Background(), requestdto.KnowledgeIngestFile{
		Path:       "/tmp/upload.md",
		Title:      "设计文档",
		SourceType: testDocumentSource,
		SourceURI:  "设计.md",
	})
	if err != nil {
		t.Fatalf("收录失败: %v", err)
	}

	// 请求的四个字段一个都不能丢，否则收录端拿不到路径或标题。
	want := rag.FileInput{Path: "/tmp/upload.md", Title: "设计文档", SourceType: testDocumentSource, SourceURI: "设计.md"}
	if ingestion.fileInput != want {
		t.Errorf("收录输入 = %+v，期望 %+v", ingestion.fileInput, want)
	}

	if document.ID != testDocumentID || document.Title != "设计文档" || document.Status != entity.KnowledgeDocumentStatusReady {
		t.Errorf("响应映射不对: %+v", document)
	}
	if document.Chunks != 51 {
		t.Errorf("切片数 = %d，期望 51（来自收录结果，不是 entity 上的字段）", document.Chunks)
	}
	if document.Characters != 4 {
		t.Errorf("字符数 = %d，期望 4（按字符而不是字节）", document.Characters)
	}
}

// TestIngestFileReturnsErrorWithEmptyDocument 收录在"连文档行都没建起来"时失败，
// 响应体应当是零值而不是 panic。
func TestIngestFileReturnsErrorWithEmptyDocument(t *testing.T) {
	ingestion := &fakeIngester{err: errors.New("待收录的文件路径不能为空")}
	svc := newTestService(&fakeDocumentQuerier{}, ingestion)

	document, err := svc.IngestFile(context.Background(), requestdto.KnowledgeIngestFile{})
	if err == nil {
		t.Fatal("收录失败时必须把错误交回调用方")
	}
	if document.ID != 0 || document.Title != "" {
		t.Errorf("没有任何文档可返回时应当是零值，实际 %+v", document)
	}
}

// TestIngestFileKeepsFailedDocument 收录失败但文档行已落库时，响应里要带上它 ——
// 异步化之后这个 ID 就是查进度的入口。
func TestIngestFileKeepsFailedDocument(t *testing.T) {
	ingestion := &fakeIngester{
		result: rag.IngestResult{Document: &entity.KnowledgeDocument{
			BaseModel: entity.BaseModel{ID: testDocumentID},
			Title:     "坏文档",
			Status:    entity.KnowledgeDocumentStatusFailed,
			Metadata:  json.RawMessage(`{"stage":"embed","error":"上游返回 429"}`),
		}},
		err: errors.New("第 1~16 个切片向量化失败: 上游返回 429"),
	}
	svc := newTestService(&fakeDocumentQuerier{}, ingestion)

	document, err := svc.IngestFile(context.Background(), requestdto.KnowledgeIngestFile{Path: "/tmp/x.md"})
	if err == nil {
		t.Fatal("收录失败时必须报错")
	}
	if document.ID != testDocumentID {
		t.Errorf("失败时也应当带上文档 ID，实际 %+v", document)
	}
	// 失败原因从 metadata 的 error 键取出来，前端只需要这一句话。
	if document.Error != "上游返回 429" {
		t.Errorf("失败原因 = %q，期望从 metadata 取到", document.Error)
	}
}

// TestIngestTextMapsRequest 正文收录不经过文件路径。
func TestIngestTextMapsRequest(t *testing.T) {
	ingestion := &fakeIngester{result: rag.IngestResult{
		Document: &entity.KnowledgeDocument{
			BaseModel: entity.BaseModel{ID: testDocumentID},
			Status:    entity.KnowledgeDocumentStatusReady,
			Metadata:  json.RawMessage(`{}`),
		},
		Chunks: 2,
	}}
	svc := newTestService(&fakeDocumentQuerier{}, ingestion)

	if _, err := svc.IngestText(context.Background(), requestdto.KnowledgeIngestText{
		Title:   "手记",
		Content: "正文。",
	}); err != nil {
		t.Fatalf("收录失败: %v", err)
	}

	want := rag.TextInput{Title: "手记", Content: "正文。"}
	if ingestion.textInput != want {
		t.Errorf("收录输入 = %+v，期望 %+v", ingestion.textInput, want)
	}
}

func TestNormalizePageClampsValues(t *testing.T) {
	cases := []struct{ page, size, wantPage, wantSize int }{
		{0, 0, 1, defaultPageSize},
		{-3, -1, 1, defaultPageSize},
		{2, 50, 2, 50},
		{1, 5000, 1, maxPageSize},
	}
	for _, item := range cases {
		page, size := NormalizePage(item.page, item.size)
		if page != item.wantPage || size != item.wantSize {
			t.Errorf("NormalizePage(%d, %d) = (%d, %d)，期望 (%d, %d)",
				item.page, item.size, page, size, item.wantPage, item.wantSize)
		}
	}
}

func TestListReportsChunkCounts(t *testing.T) {
	querier := &fakeDocumentQuerier{
		document: &entity.KnowledgeDocument{
			BaseModel:  entity.BaseModel{ID: testDocumentID},
			Title:      "示例",
			SourceType: testDocumentSource,
			Status:     entity.KnowledgeDocumentStatusReady,
			Content:    "正文",
			Metadata:   json.RawMessage(`{}`),
		},
		total:  1,
		counts: map[uint64]int64{testDocumentID: 12},
	}
	svc := newTestService(querier, &fakeIngester{})

	documents, total, err := svc.List(context.Background(), 1, 20)
	if err != nil {
		t.Fatalf("列表查询失败: %v", err)
	}
	if total != 1 || len(documents) != 1 {
		t.Fatalf("total = %d, len = %d，期望都是 1", total, len(documents))
	}
	if documents[0].Chunks != 12 {
		t.Errorf("切片数 = %d，期望 12", documents[0].Chunks)
	}
	if documents[0].Characters != 2 {
		t.Errorf("字符数 = %d，期望 2", documents[0].Characters)
	}
}

func TestGetReportsMissingDocument(t *testing.T) {
	svc := newTestService(&fakeDocumentQuerier{}, &fakeIngester{})

	_, err := svc.Get(context.Background(), 999)
	if err == nil {
		t.Fatal("文档不存在时必须报错")
	}
	if !strings.Contains(err.Error(), "不存在") {
		t.Errorf("错误信息应当说明文档不存在，实际: %v", err)
	}
}
