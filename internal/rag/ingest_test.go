package rag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"narra/internal/model/entity"
	"narra/pkg/config"
	"narra/pkg/embedding"
)

// 这些用例故意不连数据库、不调向量服务：收录链路的编排逻辑（状态怎么推进、
// 失败时留下什么、切片与向量怎么对应）全都是纯逻辑，用几个替身就能测干净。
// 真库与真向量服务由仓储层的用例和真实上传覆盖 —— 那些测的是"和外部世界对不对得上"，
// 和这里测的不是一回事，混在一起的结果通常是两边都测不细。

const (
	testModelName      = "qwen-embedding"
	testModelID        = uint64(6)
	testDocumentID     = uint64(7)
	testVectorDims     = 4
	testDocumentSource = entity.KnowledgeDocumentSourceImport
)

// fakeDocumentStore 是 DocumentStore 的内存替身。
//
// 它只有五个方法 —— 接口窄的好处在这里很直观：替身不需要实现列表、计数、删除，
// 那些方法收录链路根本调不到。
type fakeDocumentStore struct {
	created    *entity.KnowledgeDocument
	replaced   *entity.ChunkReplacement
	metadata   json.RawMessage
	failMeta   json.RawMessage
	failReason string
	processing bool
	replaceErr error
	chunkCount int64
}

var _ DocumentStore = (*fakeDocumentStore)(nil)

func (s *fakeDocumentStore) Create(ctx context.Context, document *entity.KnowledgeDocument) error {
	if document.Metadata == nil || len(document.Metadata) == 0 {
		return fmt.Errorf("metadata 是 NOT NULL 的 jsonb，创建时必须给出 '{}'")
	}
	document.ID = testDocumentID
	document.CreatedAt = time.Now().UTC()
	document.UpdatedAt = document.CreatedAt
	s.created = document
	return nil
}

func (s *fakeDocumentStore) GetByID(ctx context.Context, id uint64) (*entity.KnowledgeDocument, error) {
	if s.created == nil || s.created.ID != id {
		return nil, gorm.ErrRecordNotFound
	}
	return s.created, nil
}

func (s *fakeDocumentStore) MarkProcessing(ctx context.Context, id uint64) error {
	s.processing = true
	return nil
}

func (s *fakeDocumentStore) MarkFailed(ctx context.Context, id uint64, metadata json.RawMessage, reason string) error {
	s.failMeta = metadata
	s.failReason = reason
	return nil
}

// 下面四个方法让替身同时满足 FileTaskStore —— SubmitFile 是异步收录的入口，
// 它要先断言存储支持任务队列。队列本身（ListPending / Claim / ResetStale）
// 归 Worker 用，这里给最简实现即可，用例断言的是"任务被记下来了"。
func (s *fakeDocumentStore) SetMetadata(ctx context.Context, id uint64, metadata json.RawMessage) error {
	s.metadata = metadata
	return nil
}

func (s *fakeDocumentStore) ListPending(ctx context.Context, limit int) ([]entity.KnowledgeDocument, error) {
	return nil, nil
}

func (s *fakeDocumentStore) Claim(ctx context.Context, id uint64) (bool, error) {
	return true, nil
}

func (s *fakeDocumentStore) ResetStale(ctx context.Context, olderThan time.Time) error {
	return nil
}

func (s *fakeDocumentStore) ReplaceChunks(ctx context.Context, id uint64, input entity.ChunkReplacement) error {
	if s.replaceErr != nil {
		return s.replaceErr
	}
	s.replaced = &input
	s.chunkCount = int64(len(input.Chunks))

	// 替身也要像真仓储那样把变化体现在"库里"：收录成功后链路会回读一次组装结果，
	// 替身不更新的话，回读拿到的是创建时的旧值，用例会以"状态还是 pending"失败 ——
	// 那测的是替身偷懒，不是业务逻辑。
	if s.created != nil {
		s.created.Title = input.Title
		s.created.Content = input.Content
		s.created.Status = entity.KnowledgeDocumentStatusReady
		s.created.Metadata = input.Metadata
		s.created.UpdatedAt = time.Now().UTC()
	}
	return nil
}

// status 返回这篇文档最终停在哪个状态，用于断言状态机走对了。
func (s *fakeDocumentStore) status() string {
	if s.failMeta != nil {
		return entity.KnowledgeDocumentStatusFailed
	}
	if s.replaced != nil {
		return entity.KnowledgeDocumentStatusReady
	}
	if s.created != nil {
		return s.created.Status
	}
	return ""
}

// fakeUploadRecordStore 是 UploadRecordStore 的内存替身。
//
// 只有建记录这一个方法 —— 记录的状态推进不从这里走：置 ready 与置 failed 由
// DocumentStore 的 ReplaceChunks / MarkFailed 在事务里顺带完成，替身也就不必实现它们。
type fakeUploadRecordStore struct {
	created *entity.KnowledgeUploadRecord
	err     error
}

var _ UploadRecordStore = (*fakeUploadRecordStore)(nil)

func (s *fakeUploadRecordStore) CreateUploadRecord(ctx context.Context, record *entity.KnowledgeUploadRecord) error {
	if s.err != nil {
		return s.err
	}
	s.created = record
	return nil
}

// fakeModelRegistry 是 ModelRegistry 的内存替身。
type fakeModelRegistry struct {
	model      *entity.EmbeddingModel
	ensureCall int
	findErr    error
}

var _ ModelRegistry = (*fakeModelRegistry)(nil)

func (r *fakeModelRegistry) EnsureDefault(ctx context.Context, model entity.EmbeddingModel) (*entity.EmbeddingModel, error) {
	r.ensureCall++
	model.BaseModel = entity.BaseModel{ID: testModelID}
	r.model = &model
	return &model, nil
}

func (r *fakeModelRegistry) GetDefault(ctx context.Context) (*entity.EmbeddingModel, error) {
	if r.findErr != nil {
		return nil, r.findErr
	}
	if r.model == nil {
		return nil, gorm.ErrRecordNotFound
	}
	return r.model, nil
}

// stubEmbedder 是 Embedder 的确定性替身。
//
// 返回的向量把首维设成文本长度，这样"向量与文本有没有错位"可以直接从数值上看出来 ——
// 真实向量服务不可用时会返回同一份结果，那种测试等于没测。
type stubEmbedder struct {
	batches   [][]string
	dimension int
	err       error
	shortBy   int
}

func (s *stubEmbedder) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	s.batches = append(s.batches, append([]string(nil), inputs...))
	if s.err != nil {
		return nil, s.err
	}

	vectors := make([][]float32, len(inputs))
	for index, text := range inputs {
		vector := make([]float32, s.dimension)
		vector[0] = float32(utf8.RuneCountInString(text))
		vectors[index] = vector
	}
	if s.shortBy > 0 && len(vectors) > s.shortBy {
		vectors = vectors[:len(vectors)-s.shortBy]
	}
	return vectors, nil
}

// testEmbeddingConfig 是一份合法且已启用的向量配置。
func testEmbeddingConfig() config.EmbeddingConfig {
	return config.EmbeddingConfig{
		Enabled:    true,
		APIKey:     "test-key",
		BaseURL:    "https://example.test/v1",
		Model:      testModelName,
		Timeout:    5 * time.Second,
		Dimensions: testVectorDims,
	}
}

// newFakeModels 造一个已经登记好默认模型的登记簿。
func newFakeModels() *fakeModelRegistry {
	return &fakeModelRegistry{model: &entity.EmbeddingModel{
		BaseModel:  entity.BaseModel{ID: testModelID},
		Name:       testModelName,
		Provider:   entity.EmbeddingProviderOpenAICompatible,
		Dimensions: testVectorDims,
	}}
}

// newIngesterWith 是测试用的完整构造：parser 固定传 nil（只走纯文本，完全离线），
// 向量化换成桩 —— 走真实的 newEmbedder 会去连 example.test。
//
// 上传记录先挂一个空的替身。需要断言记录内容的用例，在拿到 ingester 之后
// 直接换掉 ingester.records（同包可见）即可。
func newIngesterWith(store DocumentStore, models ModelRegistry, embedder Embedder, cfg config.EmbeddingConfig) *Ingester {
	ingester := NewIngester(store, &fakeUploadRecordStore{}, models, embedding.NewManager(cfg), nil)
	ingester.newEmbedder = func() (Embedder, error) {
		if embedder == nil {
			return nil, fmt.Errorf("用例没有提供向量桩")
		}
		return embedder, nil
	}
	return ingester
}

// newTestIngester 是正常路径的收录器：向量服务已启用，库里有一个默认模型。
func newTestIngester(store *fakeDocumentStore, embedder Embedder) *Ingester {
	return newIngesterWith(store, newFakeModels(), embedder, testEmbeddingConfig())
}

func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写入测试文件失败: %v", err)
	}
	return path
}

// TestIngestFileStoresChunksAndVectors 走一遍完整的正常路径。
func TestIngestFileStoresChunksAndVectors(t *testing.T) {
	store := &fakeDocumentStore{}
	embedder := &stubEmbedder{dimension: testVectorDims}
	ingester := newTestIngester(store, embedder)

	markdown := "# 数据库设计\n\n" + strings.Repeat("这是一段用于测试收录链路的正文。", 40) + "\n\n## 索引\n\n另一个小节的内容。"
	path := writeTempFile(t, "设计.md", markdown)

	result, err := ingester.IngestFile(context.Background(), FileInput{
		Path:       path,
		SourceType: testDocumentSource,
		SourceURI:  "设计.md",
	})
	if err != nil {
		t.Fatalf("收录失败: %v", err)
	}
	if result.Document == nil {
		t.Fatal("收录成功时必须给出文档")
	}

	if result.Document.Status != entity.KnowledgeDocumentStatusReady {
		t.Errorf("状态 = %q，期望 ready", result.Document.Status)
	}
	// 调用方没给标题时应当采用正文里的一级标题，而不是文件名。
	if result.Document.Title != "数据库设计" {
		t.Errorf("标题 = %q，期望取自正文一级标题", result.Document.Title)
	}
	if !store.processing {
		t.Error("收录过程中应当把文档推进到 processing")
	}
	if store.replaced == nil {
		t.Fatal("没有写入切片与向量")
	}

	replacement := store.replaced
	if len(replacement.Chunks) != len(replacement.Embeddings) {
		t.Fatalf("切片数 %d 与向量数 %d 不一致", len(replacement.Chunks), len(replacement.Embeddings))
	}
	if len(replacement.Chunks) == 0 {
		t.Fatal("应当切出至少一片")
	}
	if result.Chunks != len(replacement.Chunks) {
		t.Errorf("结果里的切片数 = %d，实际写入 %d", result.Chunks, len(replacement.Chunks))
	}

	if len(replacement.Checksum) != 64 {
		t.Errorf("摘要长度 = %d，期望 64（列类型是 char(64)）", len(replacement.Checksum))
	}
	if replacement.Content != markdown {
		t.Error("落库的正文应当与解析出的 Markdown 完全一致")
	}
	if len(replacement.Metadata) == 0 || replacement.Metadata[0] != '{' {
		t.Errorf("metadata 应当是 JSON 对象，实际 %s", replacement.Metadata)
	}

	for index, chunk := range replacement.Chunks {
		if chunk.DocumentID != testDocumentID {
			t.Errorf("第 %d 片的 document_id = %d，期望 %d", index, chunk.DocumentID, testDocumentID)
		}
		if chunk.ChunkIndex != int32(index) {
			t.Errorf("第 %d 片的 chunk_index = %d，序号必须从 0 连续", index, chunk.ChunkIndex)
		}
		if chunk.CharacterCount <= 0 {
			t.Errorf("第 %d 片的 character_count = %d，check 约束要求大于 0", index, chunk.CharacterCount)
		}
		if got := int32(utf8.RuneCountInString(chunk.Content)); got != chunk.CharacterCount {
			t.Errorf("第 %d 片的 character_count = %d，实际字符数 %d（数据库按字符计数）",
				index, chunk.CharacterCount, got)
		}
		if len(chunk.Metadata) == 0 || chunk.Metadata[0] != '{' {
			t.Errorf("第 %d 片的 metadata 应当是 JSON 对象，实际 %s", index, chunk.Metadata)
		}

		vectorRow := replacement.Embeddings[index]
		if vectorRow.ModelID != testModelID {
			t.Errorf("第 %d 个向量的 model_id = %d，期望 %d", index, vectorRow.ModelID, testModelID)
		}
		if vectorRow.Dimensions != int32(testVectorDims) {
			t.Errorf("第 %d 个向量的维度 = %d，期望 %d", index, vectorRow.Dimensions, testVectorDims)
		}
		if vectorRow.GeneratedAt.IsZero() {
			t.Errorf("第 %d 个向量缺少生成时间", index)
		}
		if !strings.HasPrefix(vectorRow.Embedding, "[") || !strings.HasSuffix(vectorRow.Embedding, "]") {
			t.Errorf("第 %d 个向量的文本格式不对: %s", index, vectorRow.Embedding)
		}
	}

	// 向量必须与切片一一对应，不能错位：首维存的是文本长度，可以直接核对。
	for index, chunk := range replacement.Chunks {
		literal := replacement.Embeddings[index].Embedding
		head, _, _ := strings.Cut(strings.TrimPrefix(literal, "["), ",")
		if want := strconv.Itoa(utf8.RuneCountInString(chunk.Content)); head != want {
			t.Fatalf("第 %d 个向量与切片错位：向量首维 %s，切片长度 %s", index, head, want)
		}
	}
}

// TestSubmitFileCreatesUploadRecord 提交时除了建 pending 文档，还要建一条 pending 记录 ——
// 抽屉里那条"处理中"就是从记录读出来的。
func TestSubmitFileCreatesUploadRecord(t *testing.T) {
	store := &fakeDocumentStore{}
	records := &fakeUploadRecordStore{}
	ingester := newIngesterWith(store, newFakeModels(), nil, testEmbeddingConfig())
	ingester.records = records

	path := writeTempFile(t, "设计.md", "# 标题\n\n正文。")
	result, err := ingester.SubmitFile(context.Background(), FileInput{
		Path:      path,
		SourceURI: "设计文档.md",
		SizeBytes: 4096,
	})
	if err != nil {
		t.Fatalf("提交失败: %v", err)
	}
	if result.Document == nil || result.Document.Status != entity.KnowledgeDocumentStatusPending {
		t.Fatalf("提交后应当是 pending 文档，实际 %+v", result.Document)
	}
	// 暂存路径必须记进 metadata —— worker 靠它找到要解析的文件。
	var taskMetadata map[string]any
	if err := json.Unmarshal(store.metadata, &taskMetadata); err != nil {
		t.Fatalf("任务元数据不是合法 JSON: %v", err)
	}
	if taskMetadata["upload_path"] != path {
		t.Errorf("upload_path = %v，期望 %q", taskMetadata["upload_path"], path)
	}
	if records.created == nil {
		t.Fatal("提交时必须建一条上传记录")
	}

	record := records.created
	if record.DocumentID == nil || *record.DocumentID != result.Document.ID {
		t.Errorf("记录的 document_id = %v，期望指向文档 %d", record.DocumentID, result.Document.ID)
	}
	if record.OriginalName != "设计文档.md" {
		t.Errorf("原始文件名 = %q，期望取 SourceURI", record.OriginalName)
	}
	if record.SizeBytes != 4096 {
		t.Errorf("字节数 = %d，期望 4096", record.SizeBytes)
	}
	if record.Status != entity.KnowledgeUploadRecordStatusPending {
		t.Errorf("记录状态 = %q，期望 pending", record.Status)
	}
}

// TestSubmitFileSurvivesRecordFailure 建记录失败不该让提交跟着失败。
//
// 记录只是历史：在这里把错误交回去，用户会看到"上传失败"，而文档行其实已经建好、
// worker 也照样会把它收录成功 —— 一个"报错但其实成功了"的假象更难解释。
func TestSubmitFileSurvivesRecordFailure(t *testing.T) {
	store := &fakeDocumentStore{}
	ingester := newIngesterWith(store, newFakeModels(), nil, testEmbeddingConfig())
	ingester.records = &fakeUploadRecordStore{err: fmt.Errorf("写库失败")}

	path := writeTempFile(t, "设计.md", "# 标题\n\n正文。")
	result, err := ingester.SubmitFile(context.Background(), FileInput{Path: path})
	if err != nil {
		t.Fatalf("建记录失败不该阻断提交: %v", err)
	}
	if result.Document == nil || result.Document.ID == 0 {
		t.Errorf("文档行已经建好，应当照常返回，实际 %+v", result.Document)
	}
}

// TestIngestFileBatchesEmbeddingCalls 校验分批：一次收录不能变成几百次请求。
func TestIngestFileBatchesEmbeddingCalls(t *testing.T) {
	store := &fakeDocumentStore{}
	embedder := &stubEmbedder{dimension: testVectorDims}
	ingester := newTestIngester(store, embedder)

	// 每段都足够长，确保切出远超 embedBatchSize 的切片数。
	paragraphs := make([]string, 0, 200)
	for index := 0; index < 200; index++ {
		paragraphs = append(paragraphs, strings.Repeat("测试正文内容。", 30))
	}
	path := writeTempFile(t, "长文.md", strings.Join(paragraphs, "\n\n"))

	result, err := ingester.IngestFile(context.Background(), FileInput{Path: path})
	if err != nil {
		t.Fatalf("收录失败: %v", err)
	}
	if result.Chunks <= embedBatchSize {
		t.Fatalf("这份输入应当切出多于 %d 片，实际 %d 片", embedBatchSize, result.Chunks)
	}

	if len(embedder.batches) < 2 {
		t.Fatalf("应当分成多批请求，实际 %d 批", len(embedder.batches))
	}
	total := 0
	for index, batch := range embedder.batches {
		if index < len(embedder.batches)-1 && len(batch) != embedBatchSize {
			t.Errorf("第 %d 批 %d 条，除最后一批外都应当是 %d 条", index, len(batch), embedBatchSize)
		}
		total += len(batch)
	}
	if total != result.Chunks {
		t.Errorf("请求的切片总数 = %d，实际切出 %d", total, result.Chunks)
	}
}

// TestIngestFileFailsWhenEmbeddingFails 向量化失败必须留下 failed 与失败阶段。
func TestIngestFileFailsWhenEmbeddingFails(t *testing.T) {
	store := &fakeDocumentStore{}
	embedder := &stubEmbedder{dimension: testVectorDims, err: fmt.Errorf("上游返回 429")}
	ingester := newTestIngester(store, embedder)

	path := writeTempFile(t, "正常.md", "# 标题\n\n"+strings.Repeat("正文。", 100))

	result, err := ingester.IngestFile(context.Background(), FileInput{Path: path})
	if err == nil {
		t.Fatal("向量化失败时收录必须报错")
	}

	// 失败时也必须能拿到文档行 —— 调用方要靠它的 ID 说清是哪一次上传出了问题。
	if result.Document == nil || result.Document.ID != testDocumentID {
		t.Errorf("失败时应当返回那份已置为 failed 的文档，实际 %+v", result.Document)
	}
	if store.replaced != nil {
		t.Error("向量化失败时不该写入任何切片")
	}
	if store.status() != entity.KnowledgeDocumentStatusFailed {
		t.Errorf("状态 = %q，期望 failed", store.status())
	}

	var payload struct {
		Stage string `json:"stage"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(store.failMeta, &payload); err != nil {
		t.Fatalf("失败现场不是合法 JSON: %v", err)
	}
	if payload.Stage != "embed" {
		t.Errorf("失败阶段 = %q，期望 embed", payload.Stage)
	}
	if !strings.Contains(payload.Error, "429") {
		t.Errorf("失败原因应当带上上游的错误，实际 %q", payload.Error)
	}
}

// TestIngestFileFailsWhenVectorCountMismatch 上游少返向量时必须失败，不能错位落库。
func TestIngestFileFailsWhenVectorCountMismatch(t *testing.T) {
	store := &fakeDocumentStore{}
	embedder := &stubEmbedder{dimension: testVectorDims, shortBy: 1}
	ingester := newTestIngester(store, embedder)

	// 必须切出不止一片：只有一片时"少返一条"的桩根本没有生效的余地，
	// 用例会以"收录成功"结局，看起来像功能坏了。
	path := writeTempFile(t, "正常.md", strings.Repeat("这是一段用来验证向量条数校验的正文。", 200))
	_, err := ingester.IngestFile(context.Background(), FileInput{Path: path})
	if err == nil {
		t.Fatal("向量条数与切片数不一致时必须失败")
	}
	if !errors.Is(err, ErrEmbeddingMismatch) {
		t.Errorf("错误应当能被 errors.Is 判成 ErrEmbeddingMismatch，实际: %v", err)
	}
	if store.status() != entity.KnowledgeDocumentStatusFailed {
		t.Errorf("状态 = %q，期望 failed", store.status())
	}
}

// TestIngestFileFailsOnEmptyFile 空文件不能在库里留下一个永远检索不到的空文档。
func TestIngestFileFailsOnEmptyFile(t *testing.T) {
	store := &fakeDocumentStore{}
	ingester := newTestIngester(store, &stubEmbedder{dimension: testVectorDims})

	path := writeTempFile(t, "空.md", "   \n\n  \n")
	_, err := ingester.IngestFile(context.Background(), FileInput{Path: path})
	if err == nil {
		t.Fatal("空文件必须报错")
	}
	if store.replaced != nil {
		t.Error("空文件不该写入切片")
	}
	if store.status() != entity.KnowledgeDocumentStatusFailed {
		t.Errorf("状态 = %q，期望 failed", store.status())
	}
	// 失败阶段是 parse 而不是 chunk：空文件在解析器那一关就被挡下了
	// （PlainTextParser 拒收空文件），根本走不到切分。这是防御纵深的正常结果 ——
	// 两道闸门都拦同一件事，先拦到的那道报出来。
	var payload struct {
		Stage string `json:"stage"`
	}
	_ = json.Unmarshal(store.failMeta, &payload)
	if payload.Stage != "parse" {
		t.Errorf("失败阶段 = %q，期望 parse（空文件在解析阶段就该被拒）", payload.Stage)
	}
}

// TestIngestFileWithoutParserFailsWithActionableMessage 没启用解析器时要有明确指引。
func TestIngestFileWithoutParserFailsWithActionableMessage(t *testing.T) {
	store := &fakeDocumentStore{}
	// parser 传 nil —— 相当于 document_parser.enabled = false。
	ingester := newIngesterWith(store, newFakeModels(), &stubEmbedder{dimension: testVectorDims}, testEmbeddingConfig())

	path := writeTempFile(t, "报告.pdf", "%PDF-1.4 假装是 PDF")
	_, err := ingester.IngestFile(context.Background(), FileInput{Path: path})
	if err == nil {
		t.Fatal("没有解析器时应当报错")
	}
	if !strings.Contains(err.Error(), "document_parser.enabled") {
		t.Errorf("错误信息应当指向如何修复，实际: %v", err)
	}
	if store.status() != entity.KnowledgeDocumentStatusFailed {
		t.Errorf("状态 = %q，期望 failed", store.status())
	}
}

// TestIngestFileFallsBackToFilenameWithoutHeading 正文没有一级标题时用文件名。
func TestIngestFileFallsBackToFilenameWithoutHeading(t *testing.T) {
	store := &fakeDocumentStore{}
	ingester := newTestIngester(store, &stubEmbedder{dimension: testVectorDims})

	path := writeTempFile(t, "会议纪要.md", "开场白没有标题。\n\n还有第二段。")
	result, err := ingester.IngestFile(context.Background(), FileInput{Path: path})
	if err != nil {
		t.Fatalf("收录失败: %v", err)
	}
	if result.Document.Title != "会议纪要.md" {
		t.Errorf("标题 = %q，期望回落到文件名", result.Document.Title)
	}
}

// TestIngestFileHonoursExplicitTitle 调用方给了标题就不能被正文标题顶掉。
func TestIngestFileHonoursExplicitTitle(t *testing.T) {
	store := &fakeDocumentStore{}
	ingester := newTestIngester(store, &stubEmbedder{dimension: testVectorDims})

	path := writeTempFile(t, "x.md", "# 正文里的标题\n\n正文。")
	result, err := ingester.IngestFile(context.Background(), FileInput{
		Path:  path,
		Title: "我指定的标题",
	})
	if err != nil {
		t.Fatalf("收录失败: %v", err)
	}
	if result.Document.Title != "我指定的标题" {
		t.Errorf("标题 = %q，期望保留调用方指定的标题", result.Document.Title)
	}
}

// TestIngestTextSkipsParsing 直接收录正文时不经过文件解析。
func TestIngestTextSkipsParsing(t *testing.T) {
	store := &fakeDocumentStore{}
	ingester := newTestIngester(store, &stubEmbedder{dimension: testVectorDims})

	result, err := ingester.IngestText(context.Background(), TextInput{
		Content: "# 直接收录\n\n" + strings.Repeat("正文内容。", 60),
	})
	if err != nil {
		t.Fatalf("收录失败: %v", err)
	}
	if result.Document.Status != entity.KnowledgeDocumentStatusReady {
		t.Errorf("状态 = %q，期望 ready", result.Document.Status)
	}
	if result.Document.Title != "直接收录" {
		t.Errorf("标题 = %q，期望取自正文一级标题", result.Document.Title)
	}
	if result.Document.SourceType != entity.KnowledgeDocumentSourceManual {
		t.Errorf("来源类型 = %q，期望 manual", result.Document.SourceType)
	}
}

func TestIngestTextRejectsBlankContent(t *testing.T) {
	store := &fakeDocumentStore{}
	ingester := newTestIngester(store, &stubEmbedder{dimension: testVectorDims})

	_, err := ingester.IngestText(context.Background(), TextInput{Content: "  \n "})
	if err == nil {
		t.Fatal("空正文必须报错")
	}
	if !errors.Is(err, ErrEmptyContent) {
		t.Errorf("错误应当能被 errors.Is 判成 ErrEmptyContent，实际: %v", err)
	}
	if store.created != nil {
		t.Error("空正文不该建出文档行")
	}
}

// TestIngestRejectsUnknownSourceType 来源类型非法时要在建行之前挡住，
// 否则用户拿到的是数据库的 check 约束报错。
func TestIngestRejectsUnknownSourceType(t *testing.T) {
	store := &fakeDocumentStore{}
	ingester := newTestIngester(store, &stubEmbedder{dimension: testVectorDims})

	_, err := ingester.IngestText(context.Background(), TextInput{
		Content:    "正文。",
		SourceType: "telepathy",
	})
	if err == nil {
		t.Fatal("非法来源类型必须报错")
	}
	if !strings.Contains(err.Error(), "manual") {
		t.Errorf("错误信息应当列出合法取值，实际: %v", err)
	}
	if store.created != nil {
		t.Error("校验失败不该建出文档行")
	}
}

// TestIngestFailsWhenEmbeddingDisabled 向量服务没启用时要给出可操作的提示。
func TestIngestFailsWhenEmbeddingDisabled(t *testing.T) {
	store := &fakeDocumentStore{}
	ingester := newIngesterWith(store, newFakeModels(), &stubEmbedder{dimension: testVectorDims},
		config.EmbeddingConfig{})

	path := writeTempFile(t, "x.md", "正文。")
	_, err := ingester.IngestFile(context.Background(), FileInput{Path: path})
	if err == nil {
		t.Fatal("向量服务未启用时必须报错")
	}
	if !errors.Is(err, ErrEmbeddingDisabled) {
		t.Errorf("错误应当能被 errors.Is 判成 ErrEmbeddingDisabled，实际: %v", err)
	}
	if !strings.Contains(err.Error(), "设置页") {
		t.Errorf("错误信息应当指向设置页，实际: %v", err)
	}
	if store.status() != entity.KnowledgeDocumentStatusFailed {
		t.Errorf("状态 = %q，期望 failed", store.status())
	}
}

// TestIngestFailsWhenNoEmbeddingModel 一条模型都没有时要指向设置页而不是抛原始错误。
func TestIngestFailsWhenNoEmbeddingModel(t *testing.T) {
	store := &fakeDocumentStore{}
	ingester := newIngesterWith(store, &fakeModelRegistry{}, &stubEmbedder{dimension: testVectorDims},
		testEmbeddingConfig())

	path := writeTempFile(t, "x.md", "正文。")
	_, err := ingester.IngestFile(context.Background(), FileInput{Path: path})
	if err == nil {
		t.Fatal("没有可用向量模型时必须报错")
	}
	if !errors.Is(err, ErrNoEmbeddingModel) {
		t.Errorf("错误应当能被 errors.Is 判成 ErrNoEmbeddingModel，实际: %v", err)
	}
	if !strings.Contains(err.Error(), "设置页") {
		t.Errorf("错误信息应当指向设置页，实际: %v", err)
	}
}

// TestIngestFailsWhenReplaceFails 落库失败时要留下 store 这个失败阶段。
func TestIngestFailsWhenReplaceFails(t *testing.T) {
	store := &fakeDocumentStore{replaceErr: fmt.Errorf("写库失败")}
	ingester := newTestIngester(store, &stubEmbedder{dimension: testVectorDims})

	path := writeTempFile(t, "x.md", strings.Repeat("正文。", 100))
	_, err := ingester.IngestFile(context.Background(), FileInput{Path: path})
	if err == nil {
		t.Fatal("写库失败时收录必须报错")
	}
	if store.status() != entity.KnowledgeDocumentStatusFailed {
		t.Errorf("状态 = %q，期望 failed", store.status())
	}

	var payload struct {
		Stage string `json:"stage"`
	}
	_ = json.Unmarshal(store.failMeta, &payload)
	if payload.Stage != "store" {
		t.Errorf("失败阶段 = %q，期望 store", payload.Stage)
	}
}

// TestIngestRejectsTooManyChunks 切片数超上限时要明确失败，
// 而不是把上游额度烧在一份传错的文件上。
func TestIngestRejectsTooManyChunks(t *testing.T) {
	store := &fakeDocumentStore{}
	ingester := newTestIngester(store, &stubEmbedder{dimension: testVectorDims})

	// 每段都刚好超过字符预算，逼出远超 ingestMaxChunks 的切片数。
	paragraphs := make([]string, 0, ingestMaxChunks+50)
	for index := 0; index < ingestMaxChunks+50; index++ {
		paragraphs = append(paragraphs, strings.Repeat("填满这一片的内容。", 80))
	}
	path := writeTempFile(t, "超大.md", strings.Join(paragraphs, "\n\n"))

	_, err := ingester.IngestFile(context.Background(), FileInput{Path: path})
	if err == nil {
		t.Fatal("超过单篇切片上限时必须报错")
	}
	if !errors.Is(err, ErrTooManyChunks) {
		t.Errorf("错误应当能被 errors.Is 判成 ErrTooManyChunks，实际: %v", err)
	}
	if store.replaced != nil {
		t.Error("超限时不该写入任何切片")
	}
}

func TestVectorLiteralFormatsAndRejectsBadValues(t *testing.T) {
	literal, err := vectorLiteral([]float32{0.5, -1, 2})
	if err != nil {
		t.Fatalf("正常向量不该报错: %v", err)
	}
	if literal != "[0.5,-1,2]" {
		t.Errorf("向量字面量 = %q，期望 [0.5,-1,2]", literal)
	}

	if _, err := vectorLiteral(nil); !errors.Is(err, ErrInvalidVector) {
		t.Errorf("空向量必须报成 ErrInvalidVector，实际: %v", err)
	}
	if _, err := vectorLiteral([]float32{float32(math.NaN())}); !errors.Is(err, ErrInvalidVector) {
		t.Errorf("NaN 必须被拦下（pgvector 不认这个字面量），实际: %v", err)
	}
	if _, err := vectorLiteral([]float32{float32(math.Inf(1))}); !errors.Is(err, ErrInvalidVector) {
		t.Errorf("Inf 必须被拦下，实际: %v", err)
	}
}
