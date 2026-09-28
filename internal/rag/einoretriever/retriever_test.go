package einoretriever

import (
	"context"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/retriever"

	"narra/internal/rag"
)

// fakeSearcher 是本包的离线替身：按检索词返回预设结果，并记录收到的全部输入。
// 多查询会并发调用它，记录必须加锁（-race 跑得出来）。
type fakeSearcher struct {
	results map[string]rag.RetrieveResult
	errs    map[string]error

	mu     sync.Mutex
	inputs []rag.RetrieveInput
}

var _ Searcher = (*fakeSearcher)(nil)

func (f *fakeSearcher) Retrieve(_ context.Context, input rag.RetrieveInput) (rag.RetrieveResult, error) {
	f.mu.Lock()
	f.inputs = append(f.inputs, input)
	f.mu.Unlock()

	if err := f.errs[input.Text]; err != nil {
		return rag.RetrieveResult{}, err
	}
	return f.results[input.Text], nil
}

// recordedTexts 返回所有被检索过的文本（顺序按完成先后，调用方自行排序）。
func (f *fakeSearcher) recordedTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	texts := make([]string, 0, len(f.inputs))
	for _, input := range f.inputs {
		texts = append(texts, input.Text)
	}
	return texts
}

// mockHit 造一条命中；带向量成分的来源（vector / hybrid）同时带上相似度。
func mockHit(chunkID uint64, method string) rag.Hit {
	hit := rag.Hit{
		ChunkID:       chunkID,
		DocumentID:    chunkID / 10,
		ChunkIndex:    int32(chunkID % 10),
		Heading:       "章节",
		Content:       "命中正文",
		DocumentTitle: "文档",
		SourceType:    "import",
		SourceURI:     "来源.md",
		Score:         0.03,
		Method:        method,
	}
	if methodHasVector(method) {
		similarity := 0.9
		hit.Similarity = &similarity
	}
	return hit
}

// TestRetrieverMapsHitsAndAppliesTopK 校验适配器把命中映射成 Eino 的 Document，
// 并把 WithTopK 透传给底层检索。
func TestRetrieverMapsHitsAndAppliesTopK(t *testing.T) {
	similarity := 0.81
	searcher := &fakeSearcher{results: map[string]rag.RetrieveResult{
		"令牌桶算法": {
			Model: "bge-m3",
			Terms: []string{"令牌", "算法"},
			Hits: []rag.Hit{{
				ChunkID:       11,
				DocumentID:    3,
				ChunkIndex:    2,
				Heading:       "限流",
				Content:       "令牌桶是一种限流算法",
				DocumentTitle: "限流讲义",
				SourceType:    "import",
				SourceURI:     "限流讲义.md",
				Score:         0.032,
				Similarity:    &similarity,
				Method:        rag.MethodHybrid,
			}},
		},
	}}
	adapter, err := New(searcher, 5)
	if err != nil {
		t.Fatalf("构造适配器失败: %v", err)
	}

	docs, err := adapter.Retrieve(context.Background(), "令牌桶算法", retriever.WithTopK(3))
	if err != nil {
		t.Fatalf("检索失败: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("应当返回 1 条文档，实际 %d 条", len(docs))
	}

	doc := docs[0]
	if doc.ID != "11" || doc.Content != "令牌桶是一种限流算法" {
		t.Fatalf("文档映射不对: %+v", doc)
	}
	if doc.Score() != 0.032 {
		t.Fatalf("分数应当来自 RRF 融合分: %v", doc.Score())
	}
	if doc.MetaData[metaTitle] != "限流讲义" || doc.MetaData[metaSource] != "限流讲义.md" {
		t.Fatalf("标题与来源映射不对: %+v", doc.MetaData)
	}
	if doc.MetaData[metaMethod] != rag.MethodHybrid || doc.MetaData[metaSimilarity] != similarity {
		t.Fatalf("命中方式与相似度映射不对: %+v", doc.MetaData)
	}
	if doc.MetaData[metaDocumentID] != uint64(3) {
		t.Fatalf("文档 ID 映射不对: %+v", doc.MetaData)
	}

	if len(searcher.inputs) != 1 || searcher.inputs[0].TopK != 3 {
		t.Fatalf("WithTopK 没有透传: %+v", searcher.inputs)
	}
}

// TestRetrieverFiltersByScoreThreshold 校验阈值过滤作用在对外暴露的融合分上。
func TestRetrieverFiltersByScoreThreshold(t *testing.T) {
	high := mockHit(11, rag.MethodVector)
	high.Score = 0.03
	low := mockHit(12, rag.MethodLexical)
	low.Score = 0.01

	searcher := &fakeSearcher{results: map[string]rag.RetrieveResult{
		"q": {Hits: []rag.Hit{high, low}},
	}}
	adapter, err := New(searcher, 5)
	if err != nil {
		t.Fatalf("构造适配器失败: %v", err)
	}
	docs, err := adapter.Retrieve(context.Background(), "q", retriever.WithScoreThreshold(0.02))
	if err != nil {
		t.Fatalf("检索失败: %v", err)
	}
	if len(docs) != 1 || docs[0].ID != "11" {
		t.Fatalf("阈值过滤不对: %+v", docs)
	}
}

// TestHitToDocumentRoundTrip 校验命中 → Document → 命中的往返不丢字段 ——
// 多查询融合就是靠这条往返把结果还原回项目侧的。
func TestHitToDocumentRoundTrip(t *testing.T) {
	original := mockHit(21, rag.MethodHybrid)

	doc := hitToDocument(original, "bge-m3", []string{"词项"})
	restored := documentToHit(doc)

	if restored.ChunkID != original.ChunkID || restored.DocumentID != original.DocumentID {
		t.Fatalf("ID 往返不一致: %+v", restored)
	}
	if restored.ChunkIndex != original.ChunkIndex || restored.Heading != original.Heading {
		t.Fatalf("位置字段往返不一致: %+v", restored)
	}
	if restored.DocumentTitle != original.DocumentTitle || restored.SourceURI != original.SourceURI {
		t.Fatalf("标题与来源往返不一致: %+v", restored)
	}
	if restored.Method != original.Method || restored.Score != original.Score {
		t.Fatalf("来源与分数往返不一致: %+v", restored)
	}
	if restored.Similarity == nil || *restored.Similarity != *original.Similarity {
		t.Fatalf("相似度往返不一致: %+v", restored)
	}
}
