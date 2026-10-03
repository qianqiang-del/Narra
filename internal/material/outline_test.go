package material

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"narra/internal/model/entity"
)

// 这组用例只测摘要构建器自己的规则：缓存命中/失效、生成、写回与失败兜底。
// 模型输出的措辞质量不在这里测。

// fakeSummarizer 是 Summarizer 的内存替身：按路径回显摘要，便于断言。
type fakeSummarizer struct {
	sectionCalls  int
	documentCalls int
	sectionsErr   error
	documentErr   error
}

func (f *fakeSummarizer) SummarizeSections(_ context.Context, _ string, sections []SectionSample) ([]string, error) {
	f.sectionCalls++
	if f.sectionsErr != nil {
		return nil, f.sectionsErr
	}
	summaries := make([]string, 0, len(sections))
	for _, section := range sections {
		summaries = append(summaries, "摘要："+section.Path)
	}
	return summaries, nil
}

func (f *fakeSummarizer) SummarizeDocument(_ context.Context, _ string, _ []SectionSample) (string, error) {
	f.documentCalls++
	if f.documentErr != nil {
		return "", f.documentErr
	}
	return "整篇摘要", nil
}

// fakeOutlineWriter 捕获写回的摘要。
type fakeOutlineWriter struct {
	saved map[uint64]json.RawMessage
	err   error
}

func (f *fakeOutlineWriter) SaveMaterialOutline(_ context.Context, id uint64, outline json.RawMessage) error {
	if f.err != nil {
		return f.err
	}
	if f.saved == nil {
		f.saved = make(map[uint64]json.RawMessage)
	}
	f.saved[id] = outline
	return nil
}

func newTestBuilder(t *testing.T, documents *fakeDocuments, chunks *fakeChunks, writer *fakeOutlineWriter) OutlineBuilder {
	t.Helper()
	builder, err := NewOutlineBuilder(documents, chunks, writer)
	if err != nil {
		t.Fatalf("构造摘要构建器失败: %v", err)
	}
	return builder
}

// outlineDocument 造一份带校验和的 ready 材料。
func outlineDocument(content string) *entity.KnowledgeDocument {
	document := readyDocument("材料", content)
	checksum := "checksum-1"
	document.ContentChecksum = &checksum
	return document
}

func cachedDocument(outline Outline) *entity.KnowledgeDocument {
	raw, err := json.Marshal(outline)
	if err != nil {
		panic(err)
	}
	document := outlineDocument(strings.Repeat("正", SmallDocChars+1))
	document.MaterialOutline = raw
	return document
}

func TestNewOutlineBuilderRequiresDependencies(t *testing.T) {
	if _, err := NewOutlineBuilder(nil, nil, nil); err == nil {
		t.Fatal("缺依赖时构造应当报错")
	}
}

func TestOutlineBuilderGeneratesAndSaves(t *testing.T) {
	docs := &fakeDocuments{documents: map[uint64]*entity.KnowledgeDocument{
		7: outlineDocument(strings.Repeat("正", SmallDocChars+1)),
	}}
	chunks := &fakeChunks{chunks: map[uint64][]entity.KnowledgeChunk{
		7: {
			chunk(0, "第一章 背景", "背景正文"),
			chunk(1, "第二章 方法", "方法正文"),
		},
	}}
	writer := &fakeOutlineWriter{}
	summarizer := &fakeSummarizer{}
	builder := newTestBuilder(t, docs, chunks, writer)

	outlines := builder.Build(context.Background(), []Ref{{DocumentID: 7}}, summarizer, "model-x")
	outline := outlines[7]
	if outline == nil {
		t.Fatal("应生成一份摘要")
	}
	if !outline.HasHeadings || outline.Summary != "整篇摘要" || len(outline.Sections) != 2 {
		t.Fatalf("摘要结构不对: %+v", outline)
	}
	if outline.Sections[0].Summary != "摘要：第一章 背景" {
		t.Fatalf("章节摘要不对: %+v", outline.Sections[0])
	}
	if outline.Model != "model-x" || outline.Checksum != "checksum-1" || outline.Version != OutlineVersion {
		t.Fatalf("版本/模型/校验和没写对: %+v", outline)
	}
	if writer.saved[7] == nil {
		t.Fatal("摘要应写回缓存")
	}
	if summarizer.sectionCalls != 1 || summarizer.documentCalls != 1 {
		t.Fatalf("调用次数不对: sections=%d document=%d", summarizer.sectionCalls, summarizer.documentCalls)
	}
}

func TestOutlineBuilderHitsCache(t *testing.T) {
	docs := &fakeDocuments{documents: map[uint64]*entity.KnowledgeDocument{
		7: cachedDocument(Outline{
			Version:  OutlineVersion,
			Checksum: "checksum-1",
			Summary:  "旧摘要",
			Sections: []SectionOutline{{Path: "第一章", Chars: 10, Summary: "旧章节摘要"}},
		}),
	}}
	writer := &fakeOutlineWriter{}
	summarizer := &fakeSummarizer{}
	builder := newTestBuilder(t, docs, &fakeChunks{}, writer)

	outlines := builder.Build(context.Background(), []Ref{{DocumentID: 7}}, summarizer, "model-x")
	if outlines[7] == nil || outlines[7].Summary != "旧摘要" {
		t.Fatalf("应命中缓存: %+v", outlines[7])
	}
	if summarizer.sectionCalls != 0 || summarizer.documentCalls != 0 {
		t.Fatal("命中缓存不该再调模型")
	}
	if len(writer.saved) != 0 {
		t.Fatal("命中缓存不该重写")
	}
}

func TestOutlineBuilderVersionMismatchRegenerates(t *testing.T) {
	docs := &fakeDocuments{documents: map[uint64]*entity.KnowledgeDocument{
		7: cachedDocument(Outline{Version: OutlineVersion + 1, Checksum: "checksum-1", Summary: "旧摘要"}),
	}}
	chunks := &fakeChunks{chunks: map[uint64][]entity.KnowledgeChunk{
		7: {chunk(0, "第一章", "正文")},
	}}
	summarizer := &fakeSummarizer{}
	builder := newTestBuilder(t, docs, chunks, &fakeOutlineWriter{})

	outlines := builder.Build(context.Background(), []Ref{{DocumentID: 7}}, summarizer, "model-x")
	if outlines[7] == nil || outlines[7].Summary == "旧摘要" {
		t.Fatalf("版本不一致应重生成: %+v", outlines[7])
	}
	if summarizer.sectionCalls == 0 {
		t.Fatal("应调用模型重新生成")
	}
}

func TestOutlineBuilderChecksumMismatchRegenerates(t *testing.T) {
	docs := &fakeDocuments{documents: map[uint64]*entity.KnowledgeDocument{
		7: cachedDocument(Outline{Version: OutlineVersion, Checksum: "old-checksum", Summary: "旧摘要"}),
	}}
	chunks := &fakeChunks{chunks: map[uint64][]entity.KnowledgeChunk{
		7: {chunk(0, "第一章", "正文")},
	}}
	summarizer := &fakeSummarizer{}
	builder := newTestBuilder(t, docs, chunks, &fakeOutlineWriter{})

	outlines := builder.Build(context.Background(), []Ref{{DocumentID: 7}}, summarizer, "model-x")
	if outlines[7] == nil || outlines[7].Summary == "旧摘要" {
		t.Fatalf("校验和不一致应重生成: %+v", outlines[7])
	}
	if summarizer.sectionCalls == 0 {
		t.Fatal("应调用模型重新生成")
	}
}

func TestOutlineBuilderNoHeadingsSkipsSectionSummaries(t *testing.T) {
	docs := &fakeDocuments{documents: map[uint64]*entity.KnowledgeDocument{
		7: outlineDocument(strings.Repeat("正", SmallDocChars+1)),
	}}
	chunks := &fakeChunks{chunks: map[uint64][]entity.KnowledgeChunk{
		7: {chunk(0, "", "第一片"), chunk(1, "", "第二片")},
	}}
	summarizer := &fakeSummarizer{}
	builder := newTestBuilder(t, docs, chunks, &fakeOutlineWriter{})

	outlines := builder.Build(context.Background(), []Ref{{DocumentID: 7}}, summarizer, "model-x")
	outline := outlines[7]
	if outline == nil {
		t.Fatal("应生成摘要")
	}
	if outline.HasHeadings || len(outline.Sections) != 1 || outline.Sections[0].Path != "第 1 段" {
		t.Fatalf("无标题材料应出伪分段: %+v", outline)
	}
	if outline.Sections[0].Summary != "" || outline.Sections[0].Preview == "" {
		t.Fatalf("伪分段只该有预览: %+v", outline.Sections[0])
	}
	if summarizer.sectionCalls != 0 || summarizer.documentCalls != 1 {
		t.Fatalf("只该调一次文档级摘要: sections=%d document=%d", summarizer.sectionCalls, summarizer.documentCalls)
	}
}

func TestOutlineBuilderModelFailureFallsBack(t *testing.T) {
	docs := &fakeDocuments{documents: map[uint64]*entity.KnowledgeDocument{
		7: outlineDocument(strings.Repeat("正", SmallDocChars+1)),
	}}
	chunks := &fakeChunks{chunks: map[uint64][]entity.KnowledgeChunk{
		7: {chunk(0, "第一章", "正文")},
	}}
	summarizer := &fakeSummarizer{
		sectionsErr: errors.New("模型不可用"),
		documentErr: errors.New("模型不可用"),
	}
	writer := &fakeOutlineWriter{}
	builder := newTestBuilder(t, docs, chunks, writer)

	outlines := builder.Build(context.Background(), []Ref{{DocumentID: 7}}, summarizer, "model-x")
	if len(outlines) != 0 {
		t.Fatalf("模型失败应返回空 map: %+v", outlines)
	}
	if len(writer.saved) != 0 {
		t.Fatal("失败不该写缓存")
	}
}

func TestOutlineBuilderSkipsUnavailableDocument(t *testing.T) {
	processing := readyDocument("处理中", "正文")
	processing.Status = entity.KnowledgeDocumentStatusProcessing
	docs := &fakeDocuments{documents: map[uint64]*entity.KnowledgeDocument{7: processing}}
	summarizer := &fakeSummarizer{}
	builder := newTestBuilder(t, docs, &fakeChunks{}, &fakeOutlineWriter{})

	outlines := builder.Build(context.Background(), []Ref{{DocumentID: 7}}, summarizer, "model-x")
	if len(outlines) != 0 || summarizer.documentCalls != 0 {
		t.Fatalf("不可用材料应跳过: %+v", outlines)
	}
}
