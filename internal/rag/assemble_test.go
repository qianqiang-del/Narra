package rag

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"narra/internal/model/entity"
)

// assemble_test.go 盯住装配层的三件事：按组折叠与回填、按组展开拼接、失败降级。
// 用替身把"检索"和"持久化"都换成确定性的数据，整条链不碰数据库与网络。

// fakeAssembleSearcher 是内层检索的替身：返回固定结果，并记下收到的输入（验证候选深度）。
type fakeAssembleSearcher struct {
	result RetrieveResult
	err    error
	input  RetrieveInput
}

func (f *fakeAssembleSearcher) Retrieve(_ context.Context, input RetrieveInput) (RetrieveResult, error) {
	f.input = input
	return f.result, f.err
}

// fakeChunkAssembler 是切片读取的替身：按 (documentID, 组键) 返回预置的切片文本。
type fakeChunkAssembler struct {
	sections map[string][]entity.KnowledgeChunkText
	symbols  map[string][]entity.KnowledgeChunkText
	windows  map[string][]entity.KnowledgeChunkText
	err      error
}

func (f *fakeChunkAssembler) ListSectionChunkTexts(_ context.Context, documentID uint64, sectionPath string) ([]entity.KnowledgeChunkText, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.sections[groupFixtureKey(documentID, sectionPath)], nil
}

func (f *fakeChunkAssembler) ListSymbolChunkTexts(_ context.Context, documentID uint64, symbol string) ([]entity.KnowledgeChunkText, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.symbols[groupFixtureKey(documentID, symbol)], nil
}

func (f *fakeChunkAssembler) ListChunkTextWindow(_ context.Context, documentID uint64, from, to int32) ([]entity.KnowledgeChunkText, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.windows[fmt.Sprintf("%d|%d|%d", documentID, from, to)], nil
}

func groupFixtureKey(documentID uint64, value string) string {
	return fmt.Sprintf("%d|%s", documentID, value)
}

func textRows(contents ...string) []entity.KnowledgeChunkText {
	rows := make([]entity.KnowledgeChunkText, 0, len(contents))
	for index, content := range contents {
		rows = append(rows, entity.KnowledgeChunkText{ChunkIndex: int32(index), Content: content})
	}
	return rows
}

// TestAssembledFoldsSameSectionAndBackfills 校验：同一节只占一条、被跳过的名额
// 由后面的候选补上、顺序仍是精排名次，且内层收到的候选深度是 30。
func TestAssembledFoldsSameSectionAndBackfills(t *testing.T) {
	inner := &fakeAssembleSearcher{result: RetrieveResult{Hits: []Hit{
		{ChunkID: 1, DocumentID: 7, ChunkIndex: 1, SectionPath: "第一章", Content: "A1"},
		{ChunkID: 2, DocumentID: 7, ChunkIndex: 2, SectionPath: "第一章", Content: "A2"},
		{ChunkID: 3, DocumentID: 8, SectionPath: "B", Content: "B1"},
		{ChunkID: 4, DocumentID: 9, SectionPath: "C", Content: "C1"},
		{ChunkID: 5, DocumentID: 10, SectionPath: "D", Content: "D1"},
	}}}
	assembler := &fakeChunkAssembler{}
	layer, err := NewAssembled(inner, assembler)
	if err != nil {
		t.Fatal(err)
	}

	result, err := layer.Retrieve(context.Background(), RetrieveInput{Text: "查询", TopK: 3})
	if err != nil {
		t.Fatal(err)
	}

	if inner.input.TopK != rerankCandidateDepth {
		t.Errorf("内层候选深度 = %d，期望 %d", inner.input.TopK, rerankCandidateDepth)
	}
	got := make([]uint64, len(result.Hits))
	for index, hit := range result.Hits {
		got[index] = hit.ChunkID
	}
	want := []uint64{1, 3, 4}
	if len(got) != len(want) {
		t.Fatalf("折叠后应当有 %d 条，实际 %d 条: %v", len(want), len(got), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("第 %d 条 = %d，期望 %d（顺序应保持精排名次）", index, got[index], want[index])
		}
	}
}

// TestAssembledFoldsSameSymbol 校验代码按符号折叠。
func TestAssembledFoldsSameSymbol(t *testing.T) {
	inner := &fakeAssembleSearcher{result: RetrieveResult{Hits: []Hit{
		{ChunkID: 1, DocumentID: 7, Symbol: "Parse", Content: "func Parse() { ..."},
		{ChunkID: 2, DocumentID: 7, Symbol: "Parse", Content: "... }"},
		{ChunkID: 3, DocumentID: 8, Symbol: "Encode", Content: "func Encode() { ..."},
	}}}
	layer, err := NewAssembled(inner, &fakeChunkAssembler{})
	if err != nil {
		t.Fatal(err)
	}

	result, err := layer.Retrieve(context.Background(), RetrieveInput{Text: "查询", TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 2 || result.Hits[0].ChunkID != 1 || result.Hits[1].ChunkID != 3 {
		t.Fatalf("同符号应当折叠成一条: %+v", result.Hits)
	}
}

// TestAssembledKeepsUngroupedChunks 校验无节无符号的切片不折叠（各自占名额），
// 展开用邻域窗口。
func TestAssembledKeepsUngroupedChunks(t *testing.T) {
	inner := &fakeAssembleSearcher{result: RetrieveResult{Hits: []Hit{
		{ChunkID: 5, DocumentID: 7, ChunkIndex: 5, Content: "P5"},
		{ChunkID: 6, DocumentID: 7, ChunkIndex: 6, Content: "P6"},
	}}}
	assembler := &fakeChunkAssembler{windows: map[string][]entity.KnowledgeChunkText{
		"7|4|6": textRows("P4", "P5", "P6"),
		"7|5|7": textRows("P5", "P6", "P7"),
	}}
	layer, err := NewAssembled(inner, assembler)
	if err != nil {
		t.Fatal(err)
	}

	result, err := layer.Retrieve(context.Background(), RetrieveInput{Text: "查询", TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 2 {
		t.Fatalf("无组身份的切片不该折叠，实际 %d 条", len(result.Hits))
	}
	if !strings.Contains(result.Hits[0].Context, "P4") || !strings.Contains(result.Hits[0].Context, "P6") {
		t.Errorf("第 1 条的窗口上下文不对: %q", result.Hits[0].Context)
	}
	if !strings.Contains(result.Hits[1].Context, "P7") {
		t.Errorf("第 2 条的窗口上下文不对: %q", result.Hits[1].Context)
	}
}

// TestAssembledTrimsOverlapWhenAssembling 校验整节拼接会裁掉相邻切片的句首重叠。
func TestAssembledTrimsOverlapWhenAssembling(t *testing.T) {
	inner := &fakeAssembleSearcher{result: RetrieveResult{Hits: []Hit{
		{ChunkID: 2, DocumentID: 7, ChunkIndex: 1, SectionPath: "第一章", Content: "第二句。第三句。"},
	}}}
	assembler := &fakeChunkAssembler{sections: map[string][]entity.KnowledgeChunkText{
		"7|第一章": textRows("第一句。第二句。", "第二句。第三句。"),
	}}
	layer, err := NewAssembled(inner, assembler)
	if err != nil {
		t.Fatal(err)
	}

	result, err := layer.Retrieve(context.Background(), RetrieveInput{Text: "查询", TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Hits[0].Context; got != "第一句。第二句。第三句。" {
		t.Fatalf("拼接结果 = %q，期望裁掉重叠后连成一句", got)
	}
}

// TestAssembledSkipsContextWhenSameAsContent 校验单块组不重复交付上下文。
func TestAssembledSkipsContextWhenSameAsContent(t *testing.T) {
	inner := &fakeAssembleSearcher{result: RetrieveResult{Hits: []Hit{
		{ChunkID: 1, DocumentID: 7, ChunkIndex: 0, SectionPath: "第一章", Content: "只有这一片。"},
	}}}
	assembler := &fakeChunkAssembler{sections: map[string][]entity.KnowledgeChunkText{
		"7|第一章": textRows("只有这一片。"),
	}}
	layer, err := NewAssembled(inner, assembler)
	if err != nil {
		t.Fatal(err)
	}

	result, err := layer.Retrieve(context.Background(), RetrieveInput{Text: "查询", TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if result.Hits[0].Context != "" {
		t.Errorf("与命中切片相同的上下文不该重复交付: %q", result.Hits[0].Context)
	}
}

// TestAssembledDegradesWhenReaderFails 校验读取失败只丢上下文，不影响检索结果。
func TestAssembledDegradesWhenReaderFails(t *testing.T) {
	inner := &fakeAssembleSearcher{result: RetrieveResult{Hits: []Hit{
		{ChunkID: 1, DocumentID: 7, SectionPath: "第一章", Content: "正文。"},
	}}}
	layer, err := NewAssembled(inner, &fakeChunkAssembler{err: fmt.Errorf("数据库故障")})
	if err != nil {
		t.Fatal(err)
	}

	result, err := layer.Retrieve(context.Background(), RetrieveInput{Text: "查询", TopK: 5})
	if err != nil {
		t.Fatalf("装配失败不该让检索失败: %v", err)
	}
	if len(result.Hits) != 1 || result.Hits[0].Context != "" {
		t.Fatalf("失败时应只返回命中切片: %+v", result.Hits)
	}
}

// TestAssembledCapsLongSectionAroundHit 校验超长整节以命中片为中心截窗口：
// 命中片必须在上下文里，且总量不超单条上限（允许分隔符的少量余量）。
func TestAssembledCapsLongSectionAroundHit(t *testing.T) {
	rows := make([]entity.KnowledgeChunkText, 0, 10)
	for index := 0; index < 10; index++ {
		rows = append(rows, entity.KnowledgeChunkText{
			ChunkIndex: int32(index),
			Content:    fmt.Sprintf("第%d片。", index) + strings.Repeat("甲", 494),
		})
	}
	inner := &fakeAssembleSearcher{result: RetrieveResult{Hits: []Hit{
		{ChunkID: 6, DocumentID: 7, ChunkIndex: 5, SectionPath: "长节", Content: rows[5].Content},
	}}}
	assembler := &fakeChunkAssembler{sections: map[string][]entity.KnowledgeChunkText{"7|长节": rows}}
	layer, err := NewAssembled(inner, assembler)
	if err != nil {
		t.Fatal(err)
	}

	result, err := layer.Retrieve(context.Background(), RetrieveInput{Text: "查询", TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	context := result.Hits[0].Context
	if !strings.Contains(context, "第5片。") {
		t.Fatalf("命中片必须在上下文里: %q", context[:min(40, len(context))])
	}
	if strings.Contains(context, "第0片。") {
		t.Fatalf("超长节不该从头截：上下文里出现了第 0 片")
	}
	if got := len([]rune(context)); got > assemblePerHitMaxChars+64 {
		t.Errorf("上下文长度 = %d，超出单条上限 %d 太多", got, assemblePerHitMaxChars)
	}
}

// TestAssembledHonorsLargerTopK 校验 top_k 大于候选深度时按 top_k 取候选。
func TestAssembledHonorsLargerTopK(t *testing.T) {
	inner := &fakeAssembleSearcher{}
	layer, err := NewAssembled(inner, &fakeChunkAssembler{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := layer.Retrieve(context.Background(), RetrieveInput{Text: "查询", TopK: maxTopK}); err != nil {
		t.Fatal(err)
	}
	if inner.input.TopK != maxTopK {
		t.Errorf("内层候选深度 = %d，期望 %d", inner.input.TopK, maxTopK)
	}
}

// TestTrimOverlap 表驱动钉住裁重叠的边界：正常裁、无重叠、句边界不符、整片重叠。
func TestTrimOverlap(t *testing.T) {
	cases := []struct {
		name     string
		previous string
		next     string
		want     string
		trimmed  bool
	}{
		{
			name:     "正常裁掉句首对齐的重叠",
			previous: "第一句。第二句。",
			next:     "第二句。第三句。",
			want:     "第三句。",
			trimmed:  true,
		},
		{
			name:     "没有重叠时原样返回",
			previous: "完全无关的上一段。",
			next:     "新的段落从这里开始。",
			want:     "新的段落从这里开始。",
			trimmed:  false,
		},
		{
			name:     "匹配处不是句边界不裁",
			previous: "内容 xyzabcdef",
			next:     "xyzabcdef 继续",
			want:     "xyzabcdef 继续",
			trimmed:  false,
		},
		{
			name:     "整片都是重叠时不裁",
			previous: "第一句。第二句。",
			next:     "第二句。",
			want:     "第二句。",
			trimmed:  false,
		},
		{
			name:     "太短的可疑匹配不裁",
			previous: "句子结尾。好。",
			next:     "好。下一句。",
			want:     "好。下一句。",
			trimmed:  false,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, trimmed := trimOverlap(testCase.previous, testCase.next)
			if got != testCase.want || trimmed != testCase.trimmed {
				t.Errorf("trimOverlap(%q, %q) = (%q, %v)，期望 (%q, %v)",
					testCase.previous, testCase.next, got, trimmed, testCase.want, testCase.trimmed)
			}
		})
	}
}
