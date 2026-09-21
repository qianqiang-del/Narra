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
// 只有四个方法 —— 服务层已经看不见状态推进和切片替换了，替身也就不必实现它们。
type fakeDocumentQuerier struct {
	document *entity.KnowledgeDocument
	total    int64
	counts   map[uint64]int64

	// active 是 CountActive 的返回值，用它模拟"库里还有没有没跑完的收录任务"。
	active int64

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

func (q *fakeDocumentQuerier) CountActive(ctx context.Context) (int64, error) {
	return q.active, nil
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
// 它也实现了 asyncIngester（SubmitFile）—— 服务层的上传闸门正是走那条路。
type fakeIngester struct {
	fileInput rag.FileInput
	textInput rag.TextInput
	result    rag.IngestResult
	err       error

	// submitted 累计 SubmitFile 被调用的次数，用来断言"被拒时根本没提交任务"。
	submitted int
}

var (
	_ ingester      = (*fakeIngester)(nil)
	_ asyncIngester = (*fakeIngester)(nil)
)

func (f *fakeIngester) SubmitFile(ctx context.Context, input rag.FileInput) (rag.IngestResult, error) {
	f.submitted++
	f.fileInput = input
	return f.result, f.err
}

func (f *fakeIngester) IngestFile(ctx context.Context, input rag.FileInput) (rag.IngestResult, error) {
	f.fileInput = input
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
	return NewKnowledgeService(querier, records, ingestion)
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

// 列表条件解析：分页钳到合法区间、关键字去首尾空白、status 按逗号拆开并去重。
func TestParseDocumentListQueryNormalizes(t *testing.T) {
	query, err := ParseDocumentListQuery(0, 5000, " pending , processing , pending ", "  设计  ")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if query.Page != 1 || query.Size != maxPageSize {
		t.Errorf("分页 = (%d, %d)，期望 (1, %d)", query.Page, query.Size, maxPageSize)
	}
	if got := strings.Join(query.Statuses, ","); got != "pending,processing" {
		t.Errorf("状态 = %q，期望 pending,processing（去重且保序）", got)
	}
	if query.Keyword != "设计" {
		t.Errorf("关键字 = %q，期望去掉首尾空白", query.Keyword)
	}

	// 不传 status 就是不限状态，不能变成"只看某个默认值"。
	empty, err := ParseDocumentListQuery(1, 20, "  ", "")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(empty.Statuses) != 0 {
		t.Errorf("空 status 应当解析成不限，实际 %v", empty.Statuses)
	}
}

// 不合法状态必须报错，不能静默当成"不限" —— 后者在界面上和"确实没有数据"长得一样，
// 排查时会白绕一圈。
func TestParseDocumentListQueryRejectsUnknownStatus(t *testing.T) {
	if _, err := ParseDocumentListQuery(1, 20, "pending,归档", ""); err == nil {
		t.Fatal("非法状态必须报错")
	} else if !strings.Contains(err.Error(), "无效") {
		t.Errorf("错误信息应当说明状态无效，实际: %v", err)
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

// 一次只收一份：库里还有没跑完的行时直接拒绝，而且**不能**再往下建行。
func TestSubmitFileRejectsWhileQueueBusy(t *testing.T) {
	querier := &fakeDocumentQuerier{active: 1}
	ingestion := &fakeIngester{}
	svc := newTestService(querier, ingestion)

	document, err := svc.SubmitFile(context.Background(), requestdto.KnowledgeIngestFile{Path: "/tmp/a.md"})
	if !errors.Is(err, ErrIngestBusy) {
		t.Fatalf("期望 ErrIngestBusy，实际 %v", err)
	}
	if document.ID != 0 {
		t.Errorf("被拒时不该有文档返回，实际 %+v", document)
	}
	if ingestion.submitted != 0 {
		t.Errorf("被拒时不该提交收录任务，实际提交了 %d 次", ingestion.submitted)
	}
}

// 队列空时正常提交，并把输入原样透传（上传闸门不能改坏提交本身）。
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
