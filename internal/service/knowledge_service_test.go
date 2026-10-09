package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	requestdto "narra/internal/model/dto/request"
	responsedto "narra/internal/model/dto/response"
	"narra/internal/model/entity"
	"narra/internal/rag"
	"narra/internal/rag/documentimage"
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
// 只实现这几个方法 —— 服务层已经看不见状态推进和切片替换了，替身也就不必实现它们。
type fakeDocumentQuerier struct {
	document *entity.KnowledgeDocument
	total    int64
	counts   map[uint64]int64

	// 向量体检 / 重新向量化：missingIDs 是"缺当前模型向量"的文档 ID，
	// missingCount 是 ready 下的计数、missingInFlight 是 pending/processing 下的计数；
	// missingModelID 记录最近一次查询用的模型 ID，missingStatuses 累计每次的状态口径。
	missingIDs      []uint64
	missingCount    int64
	missingInFlight int64
	missingModelID  uint64
	missingStatuses [][]string

	// query 记录最近一次收到的查询条件，供断言筛选与分页换算是否透传。
	query entity.KnowledgeDocumentQuery
}

var _ documentQuerier = (*fakeDocumentQuerier)(nil)

func (q *fakeDocumentQuerier) List(ctx context.Context, query entity.KnowledgeDocumentQuery) ([]entity.KnowledgeDocument, int64, error) {
	q.query = query
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

// SetEnabled 直接改内存里那篇文档，模拟仓储"只改一列"的行为。
func (q *fakeDocumentQuerier) SetEnabled(ctx context.Context, id uint64, enabled bool) (bool, error) {
	if q.document == nil || q.document.ID != id {
		return false, nil
	}
	q.document.Enabled = enabled
	return true, nil
}

// CountDocumentsMissingModelVectors / ListDocumentIDsMissingModelVectors 是向量体检与
// 重新向量化的数据源，返回用例预设的计数与 ID 列表；顺手记下查询用的模型 ID 与状态口径。
func (q *fakeDocumentQuerier) CountDocumentsMissingModelVectors(ctx context.Context, modelID uint64, statuses []string) (int64, error) {
	q.missingModelID = modelID
	q.missingStatuses = append(q.missingStatuses, statuses)
	if slices.Contains(statuses, entity.KnowledgeDocumentStatusPending) {
		return q.missingInFlight, nil
	}
	return q.missingCount, nil
}

func (q *fakeDocumentQuerier) ListDocumentIDsMissingModelVectors(ctx context.Context, modelID uint64) ([]uint64, error) {
	q.missingModelID = modelID
	return q.missingIDs, nil
}

// fakeIngester 是 ingester 的替身：只记录收到的输入，不真的切分与向量化。
// 它也实现了 asyncIngester（SubmitFile）与 fileRetrier（Retry）—— 服务层的
// 上传与重试入口正是走那两条路。
type fakeIngester struct {
	fileInput rag.FileInput
	textInput rag.TextInput
	result    rag.IngestResult
	err       error

	// results / errs 非空时按调用次序返回，用来构造"同批里有的入队、有的被拒"。
	// 空的调用按 result / err 兜底。
	results []rag.IngestResult
	errs    []error

	// submitted 累计 SubmitFile 被调用的次数。
	submitted int

	// retried 累计 Retry 被调用的次数，retriedStage 记录最后一次收到的恢复点；
	// retryResult 是入队是否被接受。
	retried      int
	retriedStage string
	retryResult  bool
	retryErr     error

	// reembeds 累计 Reembed 被调用次数，reembedStages 记录每次传入的恢复点；
	// reembedResults / reembedErrs 非空时按调用次序返回，用来构造"前几篇入队、
	// 第 N 篇撞上队列满"的部分成功场景。
	reembeds       int
	reembedStages  []string
	reembedResults []bool
	reembedErrs    []error
	reembedResult  bool
	reembedErr     error
}

var (
	_ ingester      = (*fakeIngester)(nil)
	_ asyncIngester = (*fakeIngester)(nil)
	_ fileRetrier   = (*fakeIngester)(nil)
)

// Retry 记录重试入队被调用过，以及收到的恢复点。服务层的材料检查必须先通过，才会走到这里。
func (f *fakeIngester) Retry(ctx context.Context, id uint64, stage string) (bool, error) {
	f.retried++
	f.retriedStage = stage
	return f.retryResult, f.retryErr
}

// Reembed 记录重新向量化入队被调用过，以及收到的恢复点；结果按次序回放。
func (f *fakeIngester) Reembed(ctx context.Context, id uint64, stage string) (bool, error) {
	index := f.reembeds
	f.reembeds++
	f.reembedStages = append(f.reembedStages, stage)
	if index < len(f.reembedErrs) && f.reembedErrs[index] != nil {
		return false, f.reembedErrs[index]
	}
	if index < len(f.reembedResults) {
		return f.reembedResults[index], nil
	}
	return f.reembedResult, f.reembedErr
}

func (f *fakeIngester) SubmitFile(ctx context.Context, input rag.FileInput) (rag.IngestResult, error) {
	index := f.submitted
	f.submitted++
	f.fileInput = input
	if len(f.results) > 0 || len(f.errs) > 0 {
		var result rag.IngestResult
		if index < len(f.results) {
			result = f.results[index]
		}
		var err error
		if index < len(f.errs) {
			err = f.errs[index]
		}
		return result, err
	}
	return f.result, f.err
}

func (f *fakeIngester) IngestText(ctx context.Context, input rag.TextInput) (rag.IngestResult, error) {
	f.textInput = input
	return f.result, f.err
}

// fakeUploadRecordStore 是 uploadRecordStore 的内存替身。
//
// 只有三个方法：建记录（UploadRecordStore）与状态推进（仓储在事务里顺带做）
// 都看不见，服务层对记录的职责就只有"列出来"和"删掉它"。
type fakeUploadRecordStore struct {
	views   []entity.KnowledgeUploadRecordView
	total   int64
	record  *entity.KnowledgeUploadRecord
	deleted []uint64

	// offset / limit 记录最近一次的查询窗口，供断言分页换算是否透传。
	offset int
	limit  int
}

var _ uploadRecordStore = (*fakeUploadRecordStore)(nil)

func (s *fakeUploadRecordStore) List(ctx context.Context, offset, limit int) ([]entity.KnowledgeUploadRecordView, int64, error) {
	s.offset, s.limit = offset, limit
	return s.views, s.total, nil
}

func (s *fakeUploadRecordStore) GetByID(ctx context.Context, id uint64) (*entity.KnowledgeUploadRecord, error) {
	if s.record == nil || s.record.ID != id {
		return nil, gorm.ErrRecordNotFound
	}
	return s.record, nil
}

func (s *fakeUploadRecordStore) DeleteRecord(ctx context.Context, id uint64) error {
	s.deleted = append(s.deleted, id)
	return nil
}

func newTestService(querier *fakeDocumentQuerier, ingestion *fakeIngester) KnowledgeService {
	return newTestServiceWithRecords(querier, &fakeUploadRecordStore{}, ingestion)
}

func newTestServiceWithRecords(
	querier *fakeDocumentQuerier,
	records *fakeUploadRecordStore,
	ingestion *fakeIngester,
) KnowledgeService {
	return NewKnowledgeService(querier, records, ingestion, &fakeRetriever{}, &fakeEmbeddingModels{}, "", nil, nil)
}

// fakeRetriever 是 retriever 的替身：记录最近一次的输入，返回预设的结果或错误。
type fakeRetriever struct {
	result rag.RetrieveResult
	err    error

	// input 记录最近一次收到的检索输入，供断言 DTO 到 rag 的映射是否透传。
	input rag.RetrieveInput
}

var _ retriever = (*fakeRetriever)(nil)

func (f *fakeRetriever) Retrieve(ctx context.Context, input rag.RetrieveInput) (rag.RetrieveResult, error) {
	f.input = input
	return f.result, f.err
}

// fakeEmbeddingModels 是 embeddingModels 的替身：model 为 nil 时返回
// gorm.ErrRecordNotFound，模拟"还没配置默认模型"。
type fakeEmbeddingModels struct {
	model *entity.EmbeddingModel
	err   error
}

var _ embeddingModels = (*fakeEmbeddingModels)(nil)

func (m *fakeEmbeddingModels) GetDefault(ctx context.Context) (*entity.EmbeddingModel, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.model == nil {
		return nil, gorm.ErrRecordNotFound
	}
	return m.model, nil
}

// TestRetrieveMapsRequestAndHits 校验检索的 DTO 映射：请求字段透传，
// 命中翻成对外结构，其中 source 回落到文档标题（手工录入的文档没有来源标识）。
func TestRetrieveMapsRequestAndHits(t *testing.T) {
	similarity := 0.75
	retrieval := &fakeRetriever{result: rag.RetrieveResult{
		Model: "bge-m3",
		Terms: []string{"向量", "检索"},
		Hits: []rag.Hit{
			{
				ChunkID:       11,
				DocumentID:    testDocumentID,
				ChunkIndex:    2,
				Heading:       "排序",
				SectionPath:   "第一章/1.1 排序",
				Content:       "命中正文",
				Context:       "第一章/1.1 排序的完整上下文",
				DocumentTitle: "向量检索调研",
				SourceType:    testDocumentSource,
				SourceURI:     "向量检索调研.md",
				Score:         0.031,
				Similarity:    &similarity,
				Method:        rag.MethodHybrid,
			},
			{
				ChunkID:       12,
				DocumentID:    testDocumentID + 1,
				ChunkIndex:    0,
				Content:       "手工录入的一段",
				DocumentTitle: "课堂笔记",
				SourceType:    entity.KnowledgeDocumentSourceManual,
				Score:         0.016,
				Method:        rag.MethodLexical,
			},
		},
	}}
	svc := NewKnowledgeService(&fakeDocumentQuerier{}, &fakeUploadRecordStore{}, &fakeIngester{}, retrieval, &fakeEmbeddingModels{}, "", nil, nil)

	result, err := svc.Retrieve(context.Background(), requestdto.KnowledgeRetrieve{Query: "  向量检索 ", TopK: 2})
	if err != nil {
		t.Fatalf("检索失败: %v", err)
	}

	if retrieval.input.Text != "  向量检索 " || retrieval.input.TopK != 2 {
		t.Fatalf("检索输入没有透传: %+v", retrieval.input)
	}
	if result.Model != "bge-m3" || len(result.Terms) != 2 {
		t.Fatalf("模型与词项没有透传: %+v", result)
	}
	if len(result.Results) != 2 {
		t.Fatalf("命中条数不对: %d", len(result.Results))
	}

	first := result.Results[0]
	if first.Source != "向量检索调研.md" || first.Heading != "排序" || first.Method != rag.MethodHybrid {
		t.Fatalf("第一条命中映射不对: %+v", first)
	}
	if first.Similarity == nil || *first.Similarity != similarity {
		t.Fatalf("相似度没有带出来: %+v", first)
	}
	if first.SectionPath != "第一章/1.1 排序" || first.Context != "第一章/1.1 排序的完整上下文" {
		t.Fatalf("节路径与装配上下文没有带出来: %+v", first)
	}

	second := result.Results[1]
	if second.Source != "课堂笔记" {
		t.Fatalf("没有来源标识时应当回落到文档标题，实际是 %q", second.Source)
	}
	if second.Similarity != nil {
		t.Fatalf("纯词法命中不该有相似度，否则 0 与正交就分不开了: %+v", second)
	}
}

// TestRetrieveRejectsEmptyQuery 校验空检索词是服务层的可判定错误 ——
// 接口层要按它翻 400，所以必须在进 rag 之前就拦下来。
func TestRetrieveRejectsEmptyQuery(t *testing.T) {
	retrieval := &fakeRetriever{}
	svc := NewKnowledgeService(&fakeDocumentQuerier{}, &fakeUploadRecordStore{}, &fakeIngester{}, retrieval, &fakeEmbeddingModels{}, "", nil, nil)

	if _, err := svc.Retrieve(context.Background(), requestdto.KnowledgeRetrieve{Query: "   "}); !errors.Is(err, ErrEmptyQuery) {
		t.Fatalf("空检索词应当返回 ErrEmptyQuery，实际是 %v", err)
	}
	if retrieval.input.Text != "" {
		t.Fatalf("空检索词不该被转发给检索链路: %+v", retrieval.input)
	}
}

// TestRetrievePassesQueryVariants 校验 queries 变体原样透传给检索链路 ——
// 多路召回的编排与融合在外层门面（internal/rag/einoretriever），服务层只做透传。
func TestRetrievePassesQueryVariants(t *testing.T) {
	retrieval := &fakeRetriever{}
	svc := NewKnowledgeService(&fakeDocumentQuerier{}, &fakeUploadRecordStore{}, &fakeIngester{}, retrieval, &fakeEmbeddingModels{}, "", nil, nil)

	if _, err := svc.Retrieve(context.Background(), requestdto.KnowledgeRetrieve{
		Query:   "原查询",
		Queries: []string{"变体一", "变体二"},
	}); err != nil {
		t.Fatalf("检索失败: %v", err)
	}
	if retrieval.input.Text != "原查询" || !slices.Equal(retrieval.input.Variants, []string{"变体一", "变体二"}) {
		t.Fatalf("变体没有透传: %+v", retrieval.input)
	}
}

// TestRetrievePassesFilters 校验过滤条件的校验与归一化：来源与 ID 去重、
// 时间统一转 UTC，最后原样进入 rag 层。
func TestRetrievePassesFilters(t *testing.T) {
	retrieval := &fakeRetriever{}
	svc := NewKnowledgeService(&fakeDocumentQuerier{}, &fakeUploadRecordStore{}, &fakeIngester{}, retrieval, &fakeEmbeddingModels{}, "", nil, nil)

	from := time.Date(2026, 9, 1, 8, 0, 0, 0, time.FixedZone("CST", 8*3600))
	to := time.Date(2026, 10, 1, 8, 0, 0, 0, time.FixedZone("CST", 8*3600))
	if _, err := svc.Retrieve(context.Background(), requestdto.KnowledgeRetrieve{
		Query: "向量检索",
		Filters: &requestdto.KnowledgeRetrieveFilters{
			SourceTypes: []string{" import ", "import", "manual"},
			DocumentIDs: []uint64{11, 11, 22},
			CreatedFrom: &from,
			CreatedTo:   &to,
		},
	}); err != nil {
		t.Fatalf("检索失败: %v", err)
	}

	wantFrom, wantTo := from.UTC(), to.UTC()
	want := entity.KnowledgeChunkFilter{
		SourceTypes: []string{entity.KnowledgeDocumentSourceImport, entity.KnowledgeDocumentSourceManual},
		DocumentIDs: []uint64{11, 22},
		CreatedFrom: &wantFrom,
		CreatedTo:   &wantTo,
	}
	if !reflect.DeepEqual(retrieval.input.Filter, want) {
		t.Fatalf("过滤条件映射不对: %+v（期望 %+v）", retrieval.input.Filter, want)
	}
}

// TestRetrieveRejectsInvalidFilters 校验非法过滤条件在进 rag 之前就被拦下，
// 而不是静默丢弃 —— 静默丢弃在界面上与"过滤没生效"长得一模一样。
func TestRetrieveRejectsInvalidFilters(t *testing.T) {
	early := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	tooManyIDs := make([]uint64, maxFilterDocumentIDs+1)
	for index := range tooManyIDs {
		tooManyIDs[index] = uint64(index + 1)
	}

	cases := []struct {
		name    string
		filters *requestdto.KnowledgeRetrieveFilters
	}{
		{name: "未知来源类型", filters: &requestdto.KnowledgeRetrieveFilters{SourceTypes: []string{"web"}}},
		{name: "文档 ID 为 0", filters: &requestdto.KnowledgeRetrieveFilters{DocumentIDs: []uint64{0}}},
		{name: "文档 ID 超量", filters: &requestdto.KnowledgeRetrieveFilters{DocumentIDs: tooManyIDs}},
		{name: "时间倒挂", filters: &requestdto.KnowledgeRetrieveFilters{CreatedFrom: &late, CreatedTo: &early}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			retrieval := &fakeRetriever{}
			svc := NewKnowledgeService(&fakeDocumentQuerier{}, &fakeUploadRecordStore{}, &fakeIngester{}, retrieval, &fakeEmbeddingModels{}, "", nil, nil)

			_, err := svc.Retrieve(context.Background(), requestdto.KnowledgeRetrieve{
				Query:   "向量检索",
				Filters: testCase.filters,
			})
			if !errors.Is(err, ErrInvalidFilter) {
				t.Fatalf("应当返回 ErrInvalidFilter，实际是 %v", err)
			}
			if retrieval.input.Text != "" {
				t.Fatalf("非法过滤条件不该被转发给检索链路: %+v", retrieval.input)
			}
		})
	}
}

// TestRetrieveReportsUnavailableSearcher 校验没接检索能力时给的是一句明确说明，
// 而不是空指针 —— retriever 可以为 nil（见 service.retriever 的注释）。
func TestRetrieveReportsUnavailableSearcher(t *testing.T) {
	svc := NewKnowledgeService(&fakeDocumentQuerier{}, &fakeUploadRecordStore{}, &fakeIngester{}, nil, &fakeEmbeddingModels{}, "", nil, nil)

	if _, err := svc.Retrieve(context.Background(), requestdto.KnowledgeRetrieve{Query: "向量检索"}); err == nil {
		t.Fatal("没接检索能力时应当报错")
	}
}

// TestSubmitFileMapsRequestAndResult 校验 DTO 到收录输入、entity 到响应的两次映射。
func TestSubmitFileMapsRequestAndResult(t *testing.T) {
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

	document, err := svc.SubmitFile(context.Background(), requestdto.KnowledgeIngestFile{
		Path:       "/tmp/upload.md",
		Title:      "设计文档",
		SourceType: testDocumentSource,
		SourceURI:  "设计.md",
	})
	if err != nil {
		t.Fatalf("提交失败: %v", err)
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

	documents, total, err := svc.List(context.Background(), requestdto.KnowledgeListQuery{Page: 1, Size: 20})
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

// SetEnabled 只改检索开关：响应回读自存储，形状与详情接口一致（带切片数）。
func TestSetEnabledTogglesRetrievalFlag(t *testing.T) {
	querier := &fakeDocumentQuerier{
		document: &entity.KnowledgeDocument{
			BaseModel:  entity.BaseModel{ID: testDocumentID},
			Title:      "示例",
			SourceType: testDocumentSource,
			Status:     entity.KnowledgeDocumentStatusReady,
			Enabled:    true,
			Content:    "正文",
			Metadata:   json.RawMessage(`{}`),
		},
		counts: map[uint64]int64{testDocumentID: 3},
	}
	svc := newTestService(querier, &fakeIngester{})

	document, err := svc.SetEnabled(context.Background(), testDocumentID, false)
	if err != nil {
		t.Fatalf("停用文档失败: %v", err)
	}
	if document.Enabled {
		t.Error("停用后响应里 enabled 仍为 true")
	}
	if querier.document.Enabled {
		t.Error("停用后存储里的 enabled 没有被改掉")
	}
	if document.Chunks != 3 {
		t.Errorf("切片数 = %d，期望 3（响应形状应当与详情接口一致）", document.Chunks)
	}

	if _, err := svc.SetEnabled(context.Background(), testDocumentID+1, false); err == nil {
		t.Error("文档不存在时应当报错")
	}
}

// 列表条件解析：分页钳到合法区间、关键字去首尾空白、status 按逗号拆开并去重。
func TestParseDocumentListQueryNormalizes(t *testing.T) {
	query, err := ParseDocumentListQuery(0, 5000, " pending , processing , pending ", "", "  设计  ")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if query.Page != 1 || query.Size != maxPageSize {
		t.Errorf("分页 = (%d, %d)，期望 (1, %d)", query.Page, query.Size, maxPageSize)
	}
	if got := strings.Join(query.Statuses, ","); got != "pending,processing" {
		t.Errorf("状态 = %q，期望 pending,processing（去重且保序）", got)
	}
	if query.Kind != entity.KnowledgeDocumentKindKnowledge {
		t.Errorf("空 kind 应缺省为知识库文档，实际 %q", query.Kind)
	}
	if query.Keyword != "设计" {
		t.Errorf("关键字 = %q，期望去掉首尾空白", query.Keyword)
	}

	// 不传 status 就是不限状态，不能变成"只看某个默认值"。
	empty, err := ParseDocumentListQuery(1, 20, "  ", entity.KnowledgeDocumentKindMaterial, "")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(empty.Statuses) != 0 {
		t.Errorf("空 status 应当解析成不限，实际 %v", empty.Statuses)
	}
	if empty.Kind != entity.KnowledgeDocumentKindMaterial {
		t.Errorf("kind = %q，期望 material", empty.Kind)
	}
}

// 不合法状态必须报错，不能静默当成"不限" —— 后者在界面上和"确实没有数据"长得一样，
// 排查时会白绕一圈。
func TestParseDocumentListQueryRejectsUnknownStatus(t *testing.T) {
	if _, err := ParseDocumentListQuery(1, 20, "pending,归档", "", ""); err == nil {
		t.Fatal("非法状态必须报错")
	} else if !strings.Contains(err.Error(), "无效") {
		t.Errorf("错误信息应当说明状态无效，实际: %v", err)
	}
}

// 非法 kind 同样必须报错。
func TestParseDocumentListQueryRejectsUnknownKind(t *testing.T) {
	if _, err := ParseDocumentListQuery(1, 20, "", "文件", ""); err == nil {
		t.Fatal("非法文档类型必须报错")
	} else if !strings.Contains(err.Error(), "无效") {
		t.Errorf("错误信息应当说明文档类型无效，实际: %v", err)
	}
}

// 查询条件要原样落到仓储上：分页窗口由 (page-1)*size 换算，关键字去掉空白。
func TestListPassesQueryToRepository(t *testing.T) {
	querier := &fakeDocumentQuerier{document: &entity.KnowledgeDocument{
		BaseModel:  entity.BaseModel{ID: testDocumentID},
		Title:      "示例",
		SourceType: testDocumentSource,
		Status:     entity.KnowledgeDocumentStatusReady,
		Content:    "正文",
		Metadata:   json.RawMessage(`{}`),
	}, total: 1}
	svc := newTestService(querier, &fakeIngester{})

	if _, _, err := svc.List(context.Background(), requestdto.KnowledgeListQuery{
		Page:     3,
		Size:     20,
		Statuses: []string{entity.KnowledgeDocumentStatusReady},
		Keyword:  " 设计 ",
	}); err != nil {
		t.Fatalf("列表查询失败: %v", err)
	}

	if querier.query.Offset != 40 || querier.query.Limit != 20 {
		t.Errorf("分页窗口 = (%d, %d)，期望 (40, 20)", querier.query.Offset, querier.query.Limit)
	}
	if querier.query.Keyword != "设计" {
		t.Errorf("关键字 = %q，期望去掉空白", querier.query.Keyword)
	}
	if got := strings.Join(querier.query.Statuses, ","); got != entity.KnowledgeDocumentStatusReady {
		t.Errorf("状态 = %q，期望 %q", got, entity.KnowledgeDocumentStatusReady)
	}
}

// 队列满时服务层原样上抛可判定的错误：容量判定已经下沉到收录链路的事务里
// （见 rag.Ingester.SubmitFile），服务层只把它交给接口层翻 409，不再自己预检。
func TestSubmitFileSurfacesQueueFull(t *testing.T) {
	querier := &fakeDocumentQuerier{}
	ingestion := &fakeIngester{err: rag.ErrIngestQueueFull}
	svc := newTestService(querier, ingestion)

	document, err := svc.SubmitFile(context.Background(), requestdto.KnowledgeIngestFile{Path: "/tmp/a.md"})
	if !errors.Is(err, ErrIngestQueueFull) {
		t.Fatalf("期望 ErrIngestQueueFull，实际 %v", err)
	}
	if document.ID != 0 {
		t.Errorf("被拒时不该有文档返回，实际 %+v", document)
	}
}

// SubmitFiles 是批量入口：逐项结果、顺序与入参一致，某一项被拒不影响其它项。
// 被拒项带上原因且没有 document_id；已入队项带上 document_id。
func TestSubmitFilesReportsPerItemResults(t *testing.T) {
	accepted := &entity.KnowledgeDocument{
		BaseModel:  entity.BaseModel{ID: testDocumentID},
		Title:      "设计文档",
		SourceType: testDocumentSource,
		Status:     entity.KnowledgeDocumentStatusPending,
		Metadata:   json.RawMessage(`{}`),
	}
	ingestion := &fakeIngester{
		results: []rag.IngestResult{{Document: accepted}, {}},
		errs:    []error{nil, rag.ErrIngestQueueFull},
	}
	svc := newTestService(&fakeDocumentQuerier{}, ingestion)

	items := svc.SubmitFiles(context.Background(), []requestdto.KnowledgeIngestFile{
		{Path: "/tmp/a.md", SourceURI: "a.md"},
		{Path: "/tmp/b.md", SourceURI: "b.md"},
	})
	if len(items) != 2 {
		t.Fatalf("逐项结果数 = %d，期望 2", len(items))
	}

	if items[0].OriginalName != "a.md" || items[0].Status != responsedto.KnowledgeIngestItemStatusPending {
		t.Errorf("第 1 项 = %+v，期望 a.md / pending", items[0])
	}
	if items[0].DocumentID == nil || *items[0].DocumentID != testDocumentID {
		t.Errorf("第 1 项应当带上文档 ID，实际 %+v", items[0].DocumentID)
	}

	if items[1].Status != responsedto.KnowledgeIngestItemStatusRejected {
		t.Errorf("第 2 项状态 = %q，期望 rejected", items[1].Status)
	}
	if items[1].DocumentID != nil {
		t.Errorf("被拒项不该有文档 ID，实际 %v", *items[1].DocumentID)
	}
	if !strings.Contains(items[1].Error, "队列已满") {
		t.Errorf("被拒原因 = %q，期望说明队列已满", items[1].Error)
	}
}

// 重试的起点按现实材料计算：有切片直接重向量化，切片没了有正文就重分块，
// 正文也没了还有原件就重新解析；三者都不在才拒绝（ErrRecoveryInputMissing）。
// 这里钉住四种组合算出来的恢复点，以及"有没有真的入队"。
func TestRetryResolvesRecoveryStageFromMaterial(t *testing.T) {
	root := t.TempDir()
	staged := filepath.Join(root, "pending", "1", "upload.md")
	if err := os.MkdirAll(filepath.Dir(staged), 0o755); err != nil {
		t.Fatalf("创建暂存目录失败: %v", err)
	}
	if err := os.WriteFile(staged, []byte("内容"), 0o600); err != nil {
		t.Fatalf("写入暂存文件失败: %v", err)
	}
	withFile, err := json.Marshal(map[string]any{"upload_path": staged})
	if err != nil {
		t.Fatalf("构造 metadata 失败: %v", err)
	}
	// 原件已被清掉：metadata 里的路径不再存在。
	missingFile, err := json.Marshal(map[string]any{"upload_path": filepath.Join(root, "pending", "gone", "upload.md")})
	if err != nil {
		t.Fatalf("构造 metadata 失败: %v", err)
	}

	parseStage := entity.KnowledgeDocumentStageParse
	embedStage := entity.KnowledgeDocumentStageEmbed

	cases := []struct {
		name      string
		stage     *string
		content   string
		chunks    int64
		metadata  json.RawMessage
		wantStage string
		wantErr   error
	}{
		{name: "有切片：直接重向量化", stage: &embedStage, chunks: 3, metadata: missingFile, wantStage: entity.KnowledgeDocumentStageEmbed},
		{name: "切片没了有正文：退回分块", stage: &embedStage, content: "已落库的正文", metadata: missingFile, wantStage: entity.KnowledgeDocumentStageChunk},
		{name: "正文没了有原件：退回解析", stage: &embedStage, metadata: withFile, wantStage: entity.KnowledgeDocumentStageParse},
		{name: "只剩原件（普通解析失败）", stage: &parseStage, metadata: withFile, wantStage: entity.KnowledgeDocumentStageParse},
		{name: "三者全无：请重新上传", stage: &embedStage, metadata: missingFile, wantErr: ErrRecoveryInputMissing},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			querier := &fakeDocumentQuerier{
				document: &entity.KnowledgeDocument{
					BaseModel:   entity.BaseModel{ID: testDocumentID},
					Title:       "重试文档",
					SourceType:  testDocumentSource,
					Status:      entity.KnowledgeDocumentStatusFailed,
					IngestStage: tc.stage,
					Content:     tc.content,
					Metadata:    tc.metadata,
				},
				counts: map[uint64]int64{testDocumentID: tc.chunks},
			}
			ingestion := &fakeIngester{retryResult: true}
			svc := NewKnowledgeService(querier, &fakeUploadRecordStore{}, ingestion, &fakeRetriever{}, &fakeEmbeddingModels{}, root, nil, nil)

			_, err := svc.Retry(context.Background(), testDocumentID)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("期望 %v，实际 %v", tc.wantErr, err)
				}
				if ingestion.retried != 0 {
					t.Error("材料检查没过时不该入队")
				}
				return
			}

			if err != nil {
				t.Fatalf("期望重试被接受，实际 %v", err)
			}
			if ingestion.retried != 1 {
				t.Fatalf("应当入队一次，实际 %d 次", ingestion.retried)
			}
			if ingestion.retriedStage != tc.wantStage {
				t.Errorf("恢复点 = %q，期望 %q", ingestion.retriedStage, tc.wantStage)
			}
		})
	}
}

// 空闲时正常提交，并把输入原样透传（服务层不再参与队列判定，只做映射）。
func TestSubmitFilePassesThroughWhenIdle(t *testing.T) {
	querier := &fakeDocumentQuerier{}
	ingestion := &fakeIngester{result: rag.IngestResult{Document: &entity.KnowledgeDocument{
		BaseModel:  entity.BaseModel{ID: testDocumentID},
		Title:      "设计文档",
		SourceType: testDocumentSource,
		Status:     entity.KnowledgeDocumentStatusPending,
		Metadata:   json.RawMessage(`{}`),
	}}}
	svc := newTestService(querier, ingestion)

	document, err := svc.SubmitFile(context.Background(), requestdto.KnowledgeIngestFile{
		Path:       "/tmp/a.md",
		Title:      "设计文档",
		SourceType: testDocumentSource,
		SourceURI:  "设计.md",
		SizeBytes:  2048,
	})
	if err != nil {
		t.Fatalf("提交失败: %v", err)
	}
	if ingestion.submitted != 1 {
		t.Errorf("SubmitFile 调用次数 = %d，期望 1", ingestion.submitted)
	}
	// 字节数也要透传：上传记录靠它显示"这份文件有多大"。
	want := rag.FileInput{Path: "/tmp/a.md", Title: "设计文档", SourceType: testDocumentSource, SourceURI: "设计.md", SizeBytes: 2048}
	if ingestion.fileInput != want {
		t.Errorf("收录输入 = %+v，期望 %+v", ingestion.fileInput, want)
	}
	if document.ID != testDocumentID || document.Status != entity.KnowledgeDocumentStatusPending {
		t.Errorf("响应映射不对: %+v", document)
	}
}

// 上传记录的标题优先取关联文档的标题；文档已被删除时回落到原始文件名 ——
// 记录留着、成果没了，界面据此显示"已收录后删除"。
func TestListUploadRecordsFallsBackToOriginalName(t *testing.T) {
	records := &fakeUploadRecordStore{
		views: []entity.KnowledgeUploadRecordView{
			{
				KnowledgeUploadRecord: entity.KnowledgeUploadRecord{
					BaseModel:    entity.BaseModel{ID: 901},
					OriginalName: "架构说明.md",
					Status:       entity.KnowledgeUploadRecordStatusReady,
					SizeBytes:    2048,
				},
				DocumentTitle: "Narra 架构说明",
			},
			{
				KnowledgeUploadRecord: entity.KnowledgeUploadRecord{
					BaseModel:    entity.BaseModel{ID: 902},
					OriginalName: "投标文件-技术标.docx",
					Status:       entity.KnowledgeUploadRecordStatusFailed,
				},
				// 文档已经不在了：LEFT JOIN 取不到标题。
				DocumentTitle: "",
			},
		},
		total: 2,
	}
	svc := newTestServiceWithRecords(&fakeDocumentQuerier{}, records, &fakeIngester{})

	out, total, err := svc.ListUploadRecords(context.Background(), 1, 20)
	if err != nil {
		t.Fatalf("列表查询失败: %v", err)
	}
	if total != 2 || len(out) != 2 {
		t.Fatalf("total = %d, len(out) = %d，期望都是 2", total, len(out))
	}
	if out[0].Title != "Narra 架构说明" {
		t.Errorf("标题 = %q，期望取关联文档的标题", out[0].Title)
	}
	if out[0].SizeBytes != 2048 {
		t.Errorf("字节数 = %d，期望 2048", out[0].SizeBytes)
	}
	if out[1].Title != "投标文件-技术标.docx" {
		t.Errorf("文档没了时标题 = %q，期望回落到原始文件名", out[1].Title)
	}
	if out[0].OriginalName != "架构说明.md" {
		t.Errorf("原始文件名 = %q，应当始终保留（界面要显示它）", out[0].OriginalName)
	}
	// 分页换算与文档列表同一套口径：第 1 页、每页 20 → 偏移 0。
	if records.offset != 0 || records.limit != 20 {
		t.Errorf("分页窗口 = (%d, %d)，期望 (0, 20)", records.offset, records.limit)
	}
}

// 失败原因从记录自己的 error_message 列取，而不是去解析文档的 metadata。
func TestListUploadRecordsReportsFailureReason(t *testing.T) {
	reason := "解析失败：No module named 'scipy'"
	records := &fakeUploadRecordStore{
		views: []entity.KnowledgeUploadRecordView{{
			KnowledgeUploadRecord: entity.KnowledgeUploadRecord{
				BaseModel:    entity.BaseModel{ID: 902},
				OriginalName: "产品需求文档.docx",
				Status:       entity.KnowledgeUploadRecordStatusFailed,
				ErrorMessage: &reason,
			},
		}},
		total: 1,
	}
	svc := newTestServiceWithRecords(&fakeDocumentQuerier{}, records, &fakeIngester{})

	out, _, err := svc.ListUploadRecords(context.Background(), 1, 20)
	if err != nil {
		t.Fatalf("列表查询失败: %v", err)
	}
	if out[0].Error != reason {
		t.Errorf("失败原因 = %q，期望 %q", out[0].Error, reason)
	}
}

// 删记录时关联的文档可能早就不在了（记录留着、成果被删了）。
// 那不是错误，是"已收录后删除"这条历史正在被清理。
func TestDeleteUploadRecordToleratesMissingDocument(t *testing.T) {
	documentID := uint64(42)
	records := &fakeUploadRecordStore{record: &entity.KnowledgeUploadRecord{
		BaseModel:  entity.BaseModel{ID: 903},
		DocumentID: &documentID,
	}}
	// 文档仓储里什么都没有，GetByID 会返回 gorm.ErrRecordNotFound。
	svc := newTestServiceWithRecords(&fakeDocumentQuerier{}, records, &fakeIngester{})

	if err := svc.DeleteUploadRecord(context.Background(), 903); err != nil {
		t.Fatalf("文档已不存在时删除记录不该失败: %v", err)
	}
	if len(records.deleted) != 1 || records.deleted[0] != 903 {
		t.Errorf("应当删掉记录 903，实际 %v", records.deleted)
	}
}

// 记录不存在时要给出能看懂的说明，而不是把 gorm 的原始错误抛出去。
func TestDeleteUploadRecordReportsMissingRecord(t *testing.T) {
	svc := newTestService(&fakeDocumentQuerier{}, &fakeIngester{})

	err := svc.DeleteUploadRecord(context.Background(), 999)
	if err == nil {
		t.Fatal("记录不存在时必须报错")
	}
	if !strings.Contains(err.Error(), "不存在") {
		t.Errorf("错误信息应当说明记录不存在，实际: %v", err)
	}
}

// 清理暂存目录时的边界：只有"比上传根目录深两层的文件"才允许删它的父目录。
//
// 放宽到根目录的直接子文件时，filepath.Dir 就是根目录本身，
// RemoveAll 会把整棵上传目录（包括其他正在处理的原件）一起清空 ——
// 而 upload_path 来自 metadata，写入者不受约束。
func TestRemovableStagingDir(t *testing.T) {
	root := t.TempDir()
	svc := &knowledgeService{uploadDir: root}
	outside := t.TempDir()

	cases := map[string]bool{
		filepath.Join(root, "pending", "1789807643538534200", "upload.pdf"): true,
		filepath.Join(root, "failed", "7", "upload.docx"):                   true,
		filepath.Join(root, "upload.md"):                                    false,
		filepath.Join(root, "pending", "upload.md"):                         false,
		filepath.Join(root, "failed"):                                       false,
		root:                                                                false,
		filepath.Join(outside, "pending", "1", "upload.md"):                 false,
	}
	for path, want := range cases {
		if got := svc.removableStagingDir(path); got != want {
			t.Errorf("removableStagingDir(%q) = %v，期望 %v", path, got, want)
		}
	}

	// uploadDir 为空（早期调用点没传）时一律不清理，避免误删。
	empty := &knowledgeService{}
	if empty.removableStagingDir(filepath.Join(root, "pending", "1", "upload.md")) {
		t.Error("uploadDir 为空时不该放行任何清理")
	}
}

// deletableDocumentQuerier 在 fakeDocumentQuerier 基础上补上删除能力，
// 让用例能走通 Delete 的完整路径（真仓储靠类型断言提供这个方法）。
type deletableDocumentQuerier struct {
	fakeDocumentQuerier
	deleted []uint64
}

func (q *deletableDocumentQuerier) Delete(_ context.Context, id uint64) error {
	q.deleted = append(q.deleted, id)
	return nil
}

// 删除文档要连带清掉它发布出去的图片，而且只清自己那一份 ——
// 图片目录按文档 ID 拼出，不存在 upload_path 那种"metadata 写什么就删什么"的风险。
func TestDeleteRemovesDocumentImages(t *testing.T) {
	knowledgeDir := t.TempDir()
	for _, name := range []string{"7", "8"} {
		dir := filepath.Join(knowledgeDir, "images", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("创建图片目录失败: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "img.png"), []byte("bytes"), 0o600); err != nil {
			t.Fatalf("写入图片失败: %v", err)
		}
	}

	querier := &deletableDocumentQuerier{}
	querier.document = &entity.KnowledgeDocument{
		BaseModel: entity.BaseModel{ID: testDocumentID},
		Title:     "图文档",
		Metadata:  json.RawMessage(`{}`),
	}
	// 图片清理走注入的存储实现（本地目录或对象存储）；这里注入本地实现，
	// 验证"只删自己那一份"的语义没变。上传暂存目录为空（本用例不涉及）。
	images, err := documentimage.NewStore(knowledgeDir)
	if err != nil {
		t.Fatalf("初始化图片存储失败: %v", err)
	}
	svc := NewKnowledgeService(querier, &fakeUploadRecordStore{}, &fakeIngester{}, &fakeRetriever{}, &fakeEmbeddingModels{}, "", images, nil)

	if err := svc.Delete(context.Background(), testDocumentID); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if !slices.Equal(querier.deleted, []uint64{testDocumentID}) {
		t.Errorf("删除调用 = %v，期望只删 %d", querier.deleted, testDocumentID)
	}
	if _, err := os.Stat(filepath.Join(knowledgeDir, "images", "7")); !os.IsNotExist(err) {
		t.Errorf("被删文档的图片目录应当已被清理，实际: %v", err)
	}
	if _, err := os.Stat(filepath.Join(knowledgeDir, "images", "8")); err != nil {
		t.Errorf("其他文档的图片目录不该受影响: %v", err)
	}
}

// 向量体检回答两个数字：ready 里"待修复"的文档数，以及已经回队列"正在补"的文档数；
// 两者都必须按默认模型的 ID 去统计 —— 换模型后的提示条与进度就靠它们。
func TestEmbeddingStatusCountsStaleDocuments(t *testing.T) {
	querier := &fakeDocumentQuerier{missingCount: 18, missingInFlight: 3}
	models := &fakeEmbeddingModels{model: &entity.EmbeddingModel{
		BaseModel:  entity.BaseModel{ID: 666},
		Name:       "Qwen/Qwen3-Embedding-0.6B",
		Dimensions: 1024,
	}}
	svc := NewKnowledgeService(querier, &fakeUploadRecordStore{}, &fakeIngester{}, &fakeRetriever{}, models, "", nil, nil)

	status, err := svc.EmbeddingStatus(context.Background())
	if err != nil {
		t.Fatalf("向量体检失败: %v", err)
	}
	if status.Model != "Qwen/Qwen3-Embedding-0.6B" || status.Dimensions != 1024 {
		t.Errorf("模型信息不对: %+v", status)
	}
	if status.StaleDocuments != 18 {
		t.Errorf("待修复的文档数 = %d，期望 18", status.StaleDocuments)
	}
	if status.PendingDocuments != 3 {
		t.Errorf("正在补的文档数 = %d，期望 3", status.PendingDocuments)
	}
	if querier.missingModelID != 666 {
		t.Errorf("统计必须按默认模型 ID %d 进行，实际用了 %d", 666, querier.missingModelID)
	}
	if len(querier.missingStatuses) != 2 {
		t.Fatalf("应当查询两种状态口径（待修复 / 正在补），实际 %v", querier.missingStatuses)
	}
	if !slices.Equal(querier.missingStatuses[0], []string{entity.KnowledgeDocumentStatusReady}) {
		t.Errorf("第一个口径应是 ready，实际 %v", querier.missingStatuses[0])
	}
	if !slices.Contains(querier.missingStatuses[1], entity.KnowledgeDocumentStatusPending) ||
		!slices.Contains(querier.missingStatuses[1], entity.KnowledgeDocumentStatusProcessing) {
		t.Errorf("第二个口径应含 pending 与 processing，实际 %v", querier.missingStatuses[1])
	}
}

// 还没有默认模型时返回零值而不是错误：那是"还没配置"，不是"有文档要重算"，
// 提示条据此不出现（引导配置是设置页的事）。
func TestEmbeddingStatusWithoutDefaultModelIsZero(t *testing.T) {
	svc := NewKnowledgeService(&fakeDocumentQuerier{}, &fakeUploadRecordStore{}, &fakeIngester{}, &fakeRetriever{}, &fakeEmbeddingModels{}, "", nil, nil)

	status, err := svc.EmbeddingStatus(context.Background())
	if err != nil {
		t.Fatalf("没有默认模型不该报错: %v", err)
	}
	if status != (responsedto.KnowledgeEmbeddingStatus{}) {
		t.Errorf("没有默认模型时应为零值，实际 %+v", status)
	}
}

// 重新向量化逐篇入队，起点阶段固定 embed：切片已落库，不重新解析、不重新切分。
func TestReembedQueuesStaleDocuments(t *testing.T) {
	querier := &fakeDocumentQuerier{missingIDs: []uint64{9, 27, 28}}
	ingestion := &fakeIngester{reembedResult: true}
	models := &fakeEmbeddingModels{model: &entity.EmbeddingModel{BaseModel: entity.BaseModel{ID: 666}, Name: "m", Dimensions: 1024}}
	svc := NewKnowledgeService(querier, &fakeUploadRecordStore{}, ingestion, &fakeRetriever{}, models, "", nil, nil)

	result, err := svc.Reembed(context.Background())
	if err != nil {
		t.Fatalf("重新向量化失败: %v", err)
	}
	if result.Total != 3 || result.Queued != 3 || result.Skipped != 0 || result.QueueFull {
		t.Errorf("受理结果不对: %+v", result)
	}
	if ingestion.reembeds != 3 {
		t.Errorf("应当逐篇入队 3 次，实际 %d", ingestion.reembeds)
	}
	for _, stage := range ingestion.reembedStages {
		if stage != entity.KnowledgeDocumentStageEmbed {
			t.Errorf("起点阶段应为 embed，实际 %q", stage)
		}
	}
	if querier.missingModelID != 666 {
		t.Errorf("列表必须按默认模型 ID 查，实际用了 %d", querier.missingModelID)
	}
}

// 队列满中途停下：已入队的如实计数，剩余留给下一次点击，而不是整体失败。
func TestReembedStopsAtQueueFullWithPartialResult(t *testing.T) {
	querier := &fakeDocumentQuerier{missingIDs: []uint64{1, 2, 3}}
	ingestion := &fakeIngester{
		reembedResults: []bool{true, true, false},
		reembedErrs:    []error{nil, nil, rag.ErrIngestQueueFull},
	}
	models := &fakeEmbeddingModels{model: &entity.EmbeddingModel{BaseModel: entity.BaseModel{ID: 666}}}
	svc := NewKnowledgeService(querier, &fakeUploadRecordStore{}, ingestion, &fakeRetriever{}, models, "", nil, nil)

	result, err := svc.Reembed(context.Background())
	if err != nil {
		t.Fatalf("部分入队不该整体报错: %v", err)
	}
	if result.Queued != 2 || result.Total != 3 || !result.QueueFull {
		t.Errorf("部分结果不对: %+v", result)
	}
	if ingestion.reembeds != 3 {
		t.Errorf("撞上队列满后应当停止，实际尝试 %d 次", ingestion.reembeds)
	}
}

// 队列满且一篇都没进去：返回可判定的 ErrIngestQueueFull，接口层翻 409。
func TestReembedAllRejectedByQueueFull(t *testing.T) {
	querier := &fakeDocumentQuerier{missingIDs: []uint64{1}}
	ingestion := &fakeIngester{reembedErrs: []error{rag.ErrIngestQueueFull}}
	models := &fakeEmbeddingModels{model: &entity.EmbeddingModel{BaseModel: entity.BaseModel{ID: 666}}}
	svc := NewKnowledgeService(querier, &fakeUploadRecordStore{}, ingestion, &fakeRetriever{}, models, "", nil, nil)

	_, err := svc.Reembed(context.Background())
	if !errors.Is(err, ErrIngestQueueFull) {
		t.Fatalf("应当返回 ErrIngestQueueFull，实际 %v", err)
	}
}

// 并发下文档已不再是 ready（别人抢先入队）：安静跳过，不算失败。
func TestReembedSkipsDocumentsAlreadyRequeued(t *testing.T) {
	querier := &fakeDocumentQuerier{missingIDs: []uint64{1, 2}}
	ingestion := &fakeIngester{reembedResults: []bool{true, false}}
	models := &fakeEmbeddingModels{model: &entity.EmbeddingModel{BaseModel: entity.BaseModel{ID: 666}}}
	svc := NewKnowledgeService(querier, &fakeUploadRecordStore{}, ingestion, &fakeRetriever{}, models, "", nil, nil)

	result, err := svc.Reembed(context.Background())
	if err != nil {
		t.Fatalf("重新向量化失败: %v", err)
	}
	if result.Queued != 1 || result.Skipped != 1 {
		t.Errorf("跳过计数不对: %+v", result)
	}
}

// 没有默认模型时重新向量化无从谈起：返回 ErrNoEmbeddingModel（接口层翻 409）。
func TestReembedWithoutDefaultModel(t *testing.T) {
	svc := NewKnowledgeService(&fakeDocumentQuerier{}, &fakeUploadRecordStore{}, &fakeIngester{}, &fakeRetriever{}, &fakeEmbeddingModels{}, "", nil, nil)

	if _, err := svc.Reembed(context.Background()); !errors.Is(err, ErrNoEmbeddingModel) {
		t.Fatalf("应当返回 ErrNoEmbeddingModel，实际 %v", err)
	}
}
