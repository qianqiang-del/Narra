package rag

import (
	"context"
	"fmt"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// transformer_test.go 盯着一件事：适配器与 Split 是同一份切分逻辑的两种形状。
// 形状转换本身不难，难的是"两边别漂移" —— 所以最关键的用例是等价性：
// 适配器转一圈再转回来，必须与直接调 Split 的结果逐字段相同。

const transformerTestMarkdown = `# 让检索更快

第一段正文。这一段讲为什么要给向量建索引：不建也能查，只是每次都要把整张表扫一遍，
向量一多就从毫秒变成秒，而且没有任何报错，表现成"检索越来越慢"，很难查。

## 排序

第二段正文。这一段讲排序：向量路管意思相近，词法路管精确命中，两条路的得分不在一个
量纲上，所以融合只看名次，不看分数。

### 细节

第三段正文，足够短。
`

// TestMarkdownChunkerMatchesSplit 是本文件的核心断言：适配器不能改变切分结果。
func TestMarkdownChunkerMatchesSplit(t *testing.T) {
	options := ChunkOptions{MaxChars: 120, MinChars: 30, Overlap: 20}
	chunker := NewMarkdownChunker(options)

	documents, err := chunker.Transform(context.Background(), []*schema.Document{
		{ID: "doc-7", Content: transformerTestMarkdown},
	})
	if err != nil {
		t.Fatalf("切分失败: %v", err)
	}
	chunks, err := DocumentsToChunks(documents)
	if err != nil {
		t.Fatalf("逆转换失败: %v", err)
	}

	want := Split(transformerTestMarkdown, options)
	if len(chunks) != len(want) {
		t.Fatalf("切片数不一致: 适配器 %d 片，Split %d 片", len(chunks), len(want))
	}
	for index := range want {
		if chunks[index] != want[index] {
			t.Fatalf("第 %d 片不一致:\n适配器 %+v\nSplit  %+v", index, chunks[index], want[index])
		}
	}
}

// TestMarkdownChunkerWritesMetadataAndIDs 校验切片文档带上了回填知识库所需的元数据、
// ID 稳定唯一，并且父文档的元数据被保留（Eino 对 Transformer 的约定）。
func TestMarkdownChunkerWritesMetadataAndIDs(t *testing.T) {
	documents, err := NewMarkdownChunker(ChunkOptions{MaxChars: 120, MinChars: 30, Overlap: 20}).Transform(
		context.Background(),
		[]*schema.Document{{
			ID:       "doc-7",
			Content:  transformerTestMarkdown,
			MetaData: map[string]any{"source": "调研.md"},
		}},
	)
	if err != nil {
		t.Fatalf("切分失败: %v", err)
	}
	if len(documents) < 2 {
		t.Fatalf("样例应当切出多片，实际 %d 片", len(documents))
	}

	for position, item := range documents {
		if want := fmt.Sprintf("doc-7#%d", position); item.ID != want {
			t.Fatalf("第 %d 片的 ID = %q，期望 %q", position, item.ID, want)
		}
		if item.MetaData[ChunkIndexMetaKey] != position {
			t.Fatalf("第 %d 片的 %s = %v", position, ChunkIndexMetaKey, item.MetaData[ChunkIndexMetaKey])
		}
		if item.MetaData["source"] != "调研.md" {
			t.Fatalf("父文档的元数据必须带下去，实际 %+v", item.MetaData)
		}
	}

	// 标题归属来自正文里的 Markdown 标题。
	if heading, _ := documents[0].MetaData[HeadingMetaKey].(string); heading != "让检索更快" {
		t.Fatalf("第一片应当归属一级标题，实际 %+v", documents[0].MetaData)
	}
}

// TestMarkdownChunkerSkipsBlankDocuments 校验空白正文不产出切片、也不报错：
// "空正文算不算失败"由收录门面判断，切分器不该替它下结论。
func TestMarkdownChunkerSkipsBlankDocuments(t *testing.T) {
	documents, err := NewMarkdownChunker(ChunkOptions{}).Transform(context.Background(), []*schema.Document{
		nil,
		{ID: "blank", Content: "   \n\n"},
		{ID: "fine", Content: "有正文的一段。"},
	})
	if err != nil {
		t.Fatalf("切分失败: %v", err)
	}
	if len(documents) != 1 || documents[0].ID != "fine#0" {
		t.Fatalf("只该留下那篇有正文的文档: %+v", documents)
	}
	if _, exists := documents[0].MetaData[HeadingMetaKey]; exists {
		t.Fatal("没有章节标题时不该写 heading 键：调用方用键在不在判断有没有标题")
	}
}

// TestMarkdownChunkerHonorsChunkOptions 校验构造时给的参数真的传进了 Split。
func TestMarkdownChunkerHonorsChunkOptions(t *testing.T) {
	parent := []*schema.Document{{ID: "doc-1", Content: transformerTestMarkdown}}

	wide, err := NewMarkdownChunker(ChunkOptions{MaxChars: 4000}).Transform(context.Background(), parent)
	if err != nil {
		t.Fatalf("切分失败: %v", err)
	}
	narrow, err := NewMarkdownChunker(ChunkOptions{MaxChars: 80}).Transform(context.Background(), parent)
	if err != nil {
		t.Fatalf("切分失败: %v", err)
	}
	if len(narrow) <= len(wide) {
		t.Fatalf("预算变小应当切出更多片，实际 %d 片 vs %d 片", len(narrow), len(wide))
	}
}

// TestDocumentsToChunksReadsLengthsAndRejectsBadIndex 校验逆转换对序号的严格程度：
// 缺序号、负数、小数、非数字一律报错，**不按出现顺序补号** ——
// 补号会把"元数据丢了"变成"顺序悄悄换了"，而顺序错了只会表现为引用读起来串了，几乎查不出来。
func TestDocumentsToChunksReadsLengthsAndRejectsBadIndex(t *testing.T) {
	// 经过一次 JSON 往返的序号是 float64，应当照收（整数）。
	chunks, err := DocumentsToChunks([]*schema.Document{
		{Content: "正文", MetaData: map[string]any{ChunkIndexMetaKey: float64(2)}},
		{Content: "另一段", MetaData: map[string]any{ChunkIndexMetaKey: int32(3), HeadingMetaKey: "排序"}},
	})
	if err != nil {
		t.Fatalf("合法序号不该报错: %v", err)
	}
	if len(chunks) != 2 || chunks[0].Index != 2 || chunks[1].Index != 3 || chunks[1].Heading != "排序" {
		t.Fatalf("逆转换结果不对: %+v", chunks)
	}

	broken := map[string]*schema.Document{
		"缺少序号":   {Content: "正文", MetaData: map[string]any{}},
		"序号是负数":  {Content: "正文", MetaData: map[string]any{ChunkIndexMetaKey: -1}},
		"序号是小数":  {Content: "正文", MetaData: map[string]any{ChunkIndexMetaKey: 1.5}},
		"序号不是数字": {Content: "正文", MetaData: map[string]any{ChunkIndexMetaKey: "0"}},
	}
	for name, item := range broken {
		if _, err := DocumentsToChunks([]*schema.Document{item}); err == nil {
			t.Fatalf("%s：应当报错而不是按出现顺序补号", name)
		}
	}
}
