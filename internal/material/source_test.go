package material

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	requestdto "narra/internal/model/dto/request"
	responsedto "narra/internal/model/dto/response"
	"narra/internal/model/entity"
)

// 这组用例只测材料消费运行时自己的规则：预算怎么分、纲要怎么建、过滤条件怎么带。
// 检索质量与切片内容归知识库链路，不在这里重复测。

type fakeDocuments struct {
	documents map[uint64]*entity.KnowledgeDocument
	err       error
}

func (f *fakeDocuments) GetByID(_ context.Context, id uint64) (*entity.KnowledgeDocument, error) {
	if f.err != nil {
		return nil, f.err
	}
	document, ok := f.documents[id]
	if !ok {
		return nil, errors.New("文档不存在")
	}
	return document, nil
}

type fakeChunks struct {
	chunks map[uint64][]entity.KnowledgeChunk
	err    error
}

func (f *fakeChunks) ListChunksByDocument(_ context.Context, id uint64) ([]entity.KnowledgeChunk, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.chunks[id], nil
}

type fakeRetriever struct {
	input  requestdto.KnowledgeRetrieve
	result responsedto.KnowledgeRetrieveResult
	err    error
	calls  int
}

func (f *fakeRetriever) Retrieve(_ context.Context, input requestdto.KnowledgeRetrieve) (responsedto.KnowledgeRetrieveResult, error) {
	f.calls++
	f.input = input
	return f.result, f.err
}

func readyDocument(title, content string) *entity.KnowledgeDocument {
	return &entity.KnowledgeDocument{Title: title, Content: content, Status: entity.KnowledgeDocumentStatusReady, Enabled: true}
}

func chunk(index int32, sectionPath, content string) entity.KnowledgeChunk {
	item := entity.KnowledgeChunk{ChunkIndex: index, Content: content}
	if sectionPath != "" {
		path := sectionPath
		item.SectionPath = &path
	}
	return item
}

func newTestSource(t *testing.T, docs *fakeDocuments, chunks *fakeChunks, retriever *fakeRetriever) Source {
	t.Helper()
	source, err := NewSource(docs, chunks, retriever)
	if err != nil {
		t.Fatalf("构造材料源失败: %v", err)
	}
	return source
}

func TestNewSourceRequiresDependencies(t *testing.T) {
	if _, err := NewSource(nil, nil, nil); err == nil {
		t.Fatal("缺依赖时构造应当报错")
	}
}

func TestSnapshotSmallDocumentIsFullText(t *testing.T) {
	docs := &fakeDocuments{documents: map[uint64]*entity.KnowledgeDocument{
		7: readyDocument("讲义标题", "一小段正文"),
	}}
	source := newTestSource(t, docs, &fakeChunks{}, &fakeRetriever{})

	blocks := source.Snapshot(context.Background(), []Ref{{DocumentID: 7, Name: "讲义.md"}}, "", nil)
	if len(blocks) != 1 {
		t.Fatalf("期望 1 个材料块，实际 %d", len(blocks))
	}
	if blocks[0].Text != "一小段正文" || blocks[0].Truncated {
		t.Fatalf("小材料应全文注入: %+v", blocks[0])
	}
	if blocks[0].Name != "讲义.md" {
		t.Fatalf("材料名不对: %q", blocks[0].Name)
	}
}

func TestSnapshotFallsBackToTitleAndSkipsUnavailable(t *testing.T) {
	processing := readyDocument("处理中", "正文")
	processing.Status = entity.KnowledgeDocumentStatusProcessing
	disabled := readyDocument("已停用", "正文")
	disabled.Enabled = false

	docs := &fakeDocuments{documents: map[uint64]*entity.KnowledgeDocument{
		7: readyDocument("讲义标题", "正文"),
		8: processing,
		9: disabled,
	}}
	source := newTestSource(t, docs, &fakeChunks{}, &fakeRetriever{})

	blocks := source.Snapshot(context.Background(), []Ref{
		{DocumentID: 7},
		{DocumentID: 8},
		{DocumentID: 9},
	}, "", nil)
	if len(blocks) != 1 || blocks[0].DocumentID != 7 {
		t.Fatalf("只该保留可检索的材料: %+v", blocks)
	}
	if blocks[0].Name != "讲义标题" {
		t.Fatalf("名字为空应回落到标题: %+v", blocks[0])
	}
}

func TestSnapshotStructuredDocumentUsesSectionOutline(t *testing.T) {
	docs := &fakeDocuments{documents: map[uint64]*entity.KnowledgeDocument{
		7: readyDocument("书", strings.Repeat("正", SmallDocChars+1)),
	}}
	chunks := &fakeChunks{chunks: map[uint64][]entity.KnowledgeChunk{
		7: {
			chunk(0, "", "前言内容"),
			chunk(1, "第一章 背景", "背景正文"),
			chunk(2, "第一章 背景", strings.Repeat("补", 100)),
			chunk(3, "第二章 方法", "方法正文"),
		},
	}}
	retriever := &fakeRetriever{}
	source := newTestSource(t, docs, chunks, retriever)

	blocks := source.Snapshot(context.Background(), []Ref{{DocumentID: 7, Name: "书.md"}}, "", nil)
	if len(blocks) != 1 {
		t.Fatalf("期望 1 个材料块，实际 %d", len(blocks))
	}
	text := blocks[0].Text
	for _, want := range []string{"（前言）", "第一章 背景", "第二章 方法"} {
		if !strings.Contains(text, want) {
			t.Fatalf("纲要缺少 %q:\n%s", want, text)
		}
	}
	if retriever.calls != 0 {
		t.Fatalf("没有 query 时不该检索，实际调用 %d 次", retriever.calls)
	}
}

func TestSnapshotUnstructuredDocumentUsesWindowOutline(t *testing.T) {
	docs := &fakeDocuments{documents: map[uint64]*entity.KnowledgeDocument{
		7: readyDocument("散装文本", strings.Repeat("字", SmallDocChars+1)),
	}}
	chunks := &fakeChunks{chunks: map[uint64][]entity.KnowledgeChunk{}}
	items := make([]entity.KnowledgeChunk, 0, 6)
	for index := int32(0); index < 6; index++ {
		items = append(items, chunk(index, "", "第"+string(rune('一'+index))+"段正文"))
	}
	chunks.chunks[7] = items
	source := newTestSource(t, docs, chunks, &fakeRetriever{})

	blocks := source.Snapshot(context.Background(), []Ref{{DocumentID: 7}}, "", nil)
	if len(blocks) != 1 {
		t.Fatalf("期望 1 个材料块，实际 %d", len(blocks))
	}
	for _, want := range []string{"第 1 段", "第 2 段"} {
		if !strings.Contains(blocks[0].Text, want) {
			t.Fatalf("窗口纲要缺少 %q:\n%s", want, blocks[0].Text)
		}
	}
}

func TestSnapshotAddsRetrievalExcerptsForBigDocuments(t *testing.T) {
	docs := &fakeDocuments{documents: map[uint64]*entity.KnowledgeDocument{
		7: readyDocument("书", strings.Repeat("正", SmallDocChars+1)),
	}}
	chunks := &fakeChunks{chunks: map[uint64][]entity.KnowledgeChunk{
		7: {chunk(0, "第一章 背景", "背景正文")},
	}}
	retriever := &fakeRetriever{result: responsedto.KnowledgeRetrieveResult{
		Results: []responsedto.KnowledgeHit{{
			DocumentID:  7,
			Source:      "书.md",
			SectionPath: "第一章 背景",
			Content:     "切片原文",
			Context:     "整节上下文",
		}},
	}}
	source := newTestSource(t, docs, chunks, retriever)

	blocks := source.Snapshot(context.Background(), []Ref{{DocumentID: 7, Name: "书.md"}}, "令牌桶", nil)
	if len(blocks) != 2 {
		t.Fatalf("期望 纲要 + 节选 两个块，实际 %d", len(blocks))
	}
	excerpt := blocks[1]
	if excerpt.Name != "材料节选" || !strings.Contains(excerpt.Text, "整节上下文") || !strings.Contains(excerpt.Text, "书.md") {
		t.Fatalf("节选块不对: %+v", excerpt)
	}
	if retriever.input.Query != "令牌桶" || retriever.input.Filters == nil ||
		!slices.Equal(retriever.input.Filters.DocumentIDs, []uint64{7}) {
		t.Fatalf("定向检索参数不对: %+v", retriever.input)
	}
}

func TestSnapshotSharedBudgetAcrossSmallDocuments(t *testing.T) {
	docs := &fakeDocuments{documents: map[uint64]*entity.KnowledgeDocument{
		7: readyDocument("第一份", strings.Repeat("甲", 5000)),
		8: readyDocument("第二份", strings.Repeat("乙", 5000)),
	}}
	source := newTestSource(t, docs, &fakeChunks{}, &fakeRetriever{})

	blocks := source.Snapshot(context.Background(), []Ref{{DocumentID: 7}, {DocumentID: 8}}, "", nil)
	if len(blocks) != 2 {
		t.Fatalf("期望 2 个材料块，实际 %d", len(blocks))
	}
	if blocks[0].Truncated {
		t.Fatalf("第一份应完整放入: %+v", blocks[0])
	}
	if !blocks[1].Truncated || runeLen(blocks[1].Text) != PlannerBudgetChars-5000 {
		t.Fatalf("第二份应按剩余预算截断: truncated=%v chars=%d", blocks[1].Truncated, runeLen(blocks[1].Text))
	}
}

func TestRetrievePassesFilterAndPrefersContext(t *testing.T) {
	retriever := &fakeRetriever{result: responsedto.KnowledgeRetrieveResult{
		Results: []responsedto.KnowledgeHit{{
			DocumentID:  7,
			Source:      "书.md",
			SectionPath: "第一章 背景",
			Content:     "切片原文",
			Context:     "整节上下文",
			Score:       0.8,
		}},
	}}
	source := newTestSource(t, &fakeDocuments{}, &fakeChunks{}, retriever)

	hits, err := source.Retrieve(context.Background(), []uint64{7, 8}, "查询词", 3)
	if err != nil {
		t.Fatalf("检索不该报错: %v", err)
	}
	if len(hits) != 1 || hits[0].Content != "整节上下文" || hits[0].SectionPath != "第一章 背景" {
		t.Fatalf("命中映射不对: %+v", hits)
	}
	if retriever.input.Query != "查询词" || retriever.input.TopK != 3 ||
		retriever.input.Filters == nil || !slices.Equal(retriever.input.Filters.DocumentIDs, []uint64{7, 8}) {
		t.Fatalf("检索参数没有带上材料过滤: %+v", retriever.input)
	}
}

func TestRetrieveSkipsEmptyInputs(t *testing.T) {
	retriever := &fakeRetriever{}
	source := newTestSource(t, &fakeDocuments{}, &fakeChunks{}, retriever)

	if hits, err := source.Retrieve(context.Background(), nil, "查询", 3); err != nil || hits != nil {
		t.Fatalf("没有文档时应直接返回空: hits=%v err=%v", hits, err)
	}
	if hits, err := source.Retrieve(context.Background(), []uint64{7}, "  ", 3); err != nil || hits != nil {
		t.Fatalf("空查询应直接返回空: hits=%v err=%v", hits, err)
	}
	if retriever.calls != 0 {
		t.Fatalf("空输入不该调用检索，实际 %d 次", retriever.calls)
	}
}

func TestRetrievePropagatesError(t *testing.T) {
	source := newTestSource(t, &fakeDocuments{}, &fakeChunks{}, &fakeRetriever{err: errors.New("向量服务不可用")})
	if _, err := source.Retrieve(context.Background(), []uint64{7}, "查询", 3); err == nil {
		t.Fatal("检索失败应当把错误交给调用方")
	}
}

func TestSnapshotSummaryLayerKeepsEveryMaterial(t *testing.T) {
	documents := make(map[uint64]*entity.KnowledgeDocument, 10)
	refs := make([]Ref, 0, 10)
	outlines := make(map[uint64]*Outline, 10)
	for index := 0; index < 10; index++ {
		id := uint64(index + 1)
		documents[id] = readyDocument("材料", strings.Repeat("正", 5000))
		refs = append(refs, Ref{DocumentID: id, Name: "材料"})
		outlines[id] = &Outline{
			Version: OutlineVersion,
			Summary: "这份材料讲测试内容",
			Sections: []SectionOutline{
				{Path: "第一章", Chars: 5000, Summary: "讲测试"},
			},
		}
	}
	source := newTestSource(t, &fakeDocuments{documents: documents}, &fakeChunks{}, &fakeRetriever{})

	blocks := source.Snapshot(context.Background(), refs, "", outlines)
	seen := make(map[uint64]bool, len(blocks))
	for _, block := range blocks {
		if block.DocumentID != 0 {
			seen[block.DocumentID] = true
		}
	}
	for _, ref := range refs {
		if !seen[ref.DocumentID] {
			t.Fatalf("材料 %d 在规划输入里消失了: %+v", ref.DocumentID, blocks)
		}
	}
}

func TestSnapshotOutlineShareIsCapped(t *testing.T) {
	docs := &fakeDocuments{documents: map[uint64]*entity.KnowledgeDocument{
		7: readyDocument("书", strings.Repeat("正", SmallDocChars+1)),
	}}
	outlines := map[uint64]*Outline{
		7: {Version: OutlineVersion, Summary: strings.Repeat("摘", 2000)},
	}
	source := newTestSource(t, docs, &fakeChunks{}, &fakeRetriever{})

	blocks := source.Snapshot(context.Background(), []Ref{{DocumentID: 7}}, "", outlines)
	if len(blocks) != 1 {
		t.Fatalf("期望 1 个摘要块，实际 %d", len(blocks))
	}
	if runeLen(blocks[0].Text) != PerMaterialOutlineChars || !blocks[0].Truncated {
		t.Fatalf("摘要层应封顶到 %d 字并标记截断: truncated=%v chars=%d",
			PerMaterialOutlineChars, blocks[0].Truncated, runeLen(blocks[0].Text))
	}
}

func TestSnapshotSmallDocumentKeepsFullTextAfterSummary(t *testing.T) {
	content := strings.Repeat("正", 3000)
	docs := &fakeDocuments{documents: map[uint64]*entity.KnowledgeDocument{
		7: readyDocument("讲义", content),
	}}
	outlines := map[uint64]*Outline{
		7: {Version: OutlineVersion, Summary: "讲了测试内容"},
	}
	source := newTestSource(t, docs, &fakeChunks{}, &fakeRetriever{})

	blocks := source.Snapshot(context.Background(), []Ref{{DocumentID: 7}}, "", outlines)
	if len(blocks) != 2 {
		t.Fatalf("期望 摘要 + 全文 两块，实际 %d", len(blocks))
	}
	if !strings.Contains(blocks[0].Text, "文档摘要") {
		t.Fatalf("第一块应是摘要: %+v", blocks[0])
	}
	if blocks[1].Text != content || blocks[1].Truncated {
		t.Fatalf("第二块应是小材料全文: truncated=%v chars=%d", blocks[1].Truncated, runeLen(blocks[1].Text))
	}
}

func TestRenderGeneratedOutlineFallsBackToPreview(t *testing.T) {
	text := renderGeneratedOutline(&Outline{
		HasHeadings: false,
		Summary:     "整篇摘要",
		Sections: []SectionOutline{
			{Path: "第 1 段", Chars: 4000, Preview: "首片预览"},
			{Path: "第 2 段", Chars: 3800, Summary: "第二段摘要"},
		},
	})
	for _, want := range []string{
		"文档摘要：整篇摘要",
		"分段：",
		"第 1 段（约 4000 字）：首片预览",
		"第 2 段（约 3800 字）：第二段摘要",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("渲染缺少 %q:\n%s", want, text)
		}
	}
}
