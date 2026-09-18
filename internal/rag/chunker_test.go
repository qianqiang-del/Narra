package rag

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// runeLength 与切片长度的口径保持一致（PostgreSQL 的 char_length 也按字符计）。
func runeLength(text string) int { return utf8.RuneCountInString(text) }

func TestSplitDropsBlankInput(t *testing.T) {
	for _, input := range []string{"", "   ", "\n\n\n", "  \n\t\n  ", "\ufeff"} {
		if chunks := Split(input, ChunkOptions{}); len(chunks) != 0 {
			t.Errorf("输入 %q 应当切不出任何切片，实际 %d 片", input, len(chunks))
		}
	}
}

// 数据库的 character_count > 0 是硬约束，任何一片空白都会让整篇文档入库失败。
func TestSplitNeverProducesEmptyContent(t *testing.T) {
	input := strings.Join([]string{
		"# 标题",
		"",
		"## 只有标题的小节",
		"",
		"### 另一个只有标题的小节",
		"",
		"正文第一段。",
		"",
		"正文第二段。",
	}, "\n")

	chunks := Split(input, ChunkOptions{})
	if len(chunks) == 0 {
		t.Fatal("应当切出至少一片")
	}
	for _, chunk := range chunks {
		if strings.TrimSpace(chunk.Content) == "" {
			t.Errorf("第 %d 片内容为空白，会被 character_count > 0 拒绝", chunk.Index)
		}
		if chunk.Content != strings.TrimSpace(chunk.Content) {
			t.Errorf("第 %d 片两端有多余空白: %q", chunk.Index, chunk.Content)
		}
	}
}

// chunk_index 在同一篇文章内唯一且从 0 连续，序号必须由切分器自己保证。
func TestSplitNumbersChunksContiguously(t *testing.T) {
	input := "# 一\n\n" + strings.Repeat("甲。", 400) + "\n\n# 二\n\n" + strings.Repeat("乙。", 400)

	chunks := Split(input, ChunkOptions{})
	if len(chunks) < 3 {
		t.Fatalf("这份输入应当切出多片，实际 %d 片", len(chunks))
	}
	for index, chunk := range chunks {
		if chunk.Index != index {
			t.Errorf("第 %d 片的 Index = %d，期望 %d", index, chunk.Index, index)
		}
	}
}

func TestSplitCarriesHeadingToItsSection(t *testing.T) {
	input := strings.Join([]string{
		"开场白，没有标题。",
		"",
		"# 第一章 背景",
		"",
		"第一章的正文。",
		"",
		"## 1.1 细节",
		"",
		"细节的正文。",
	}, "\n")

	chunks := Split(input, ChunkOptions{})
	if len(chunks) != 3 {
		t.Fatalf("期望 3 片，实际 %d 片", len(chunks))
	}
	if chunks[0].Heading != "" {
		t.Errorf("标题之前的内容不该有章节归属，实际 %q", chunks[0].Heading)
	}
	if chunks[1].Heading != "第一章 背景" {
		t.Errorf("第 2 片的章节 = %q，期望 第一章 背景", chunks[1].Heading)
	}
	if chunks[2].Heading != "1.1 细节" {
		t.Errorf("第 3 片的章节 = %q，期望 1.1 细节", chunks[2].Heading)
	}
}

// "#标签" 不是标题（CommonMark 要求 # 后有空格），"C#" 也不该被当成闭合式标题剥掉。
func TestSplitOnlyTreatsRealHeadingsAsHeadings(t *testing.T) {
	input := "#标签不是标题\n\nC# 是一门语言\n\n####### 七个井号不是标题"

	chunks := Split(input, ChunkOptions{})
	if len(chunks) != 1 {
		t.Fatalf("期望整段合成 1 片，实际 %d 片", len(chunks))
	}
	if chunks[0].Heading != "" {
		t.Errorf("这些都不是标题，章节归属应为空，实际 %q", chunks[0].Heading)
	}
	for _, want := range []string{"#标签不是标题", "C#", "####### 七个井号不是标题"} {
		if !strings.Contains(chunks[0].Content, want) {
			t.Errorf("切片内容丢了 %q: %q", want, chunks[0].Content)
		}
	}
}

// varchar(300) 按字符计数，中文按字节截断会截出半个字符并直接写不进去。
func TestSplitTruncatesOverlongHeadingByRunes(t *testing.T) {
	input := "# " + strings.Repeat("长", 400) + "\n\n正文。"
	chunks := Split(input, ChunkOptions{})
	if len(chunks) == 0 {
		t.Fatal("应当切出至少一片")
	}
	if got := runeLength(chunks[0].Heading); got != MaxHeadingRunes {
		t.Errorf("标题长度 = %d 个字符，期望截断到 %d", got, MaxHeadingRunes)
	}
	if !strings.HasPrefix(chunks[0].Heading, "长") {
		t.Errorf("截断后的标题不对: %q", chunks[0].Heading)
	}
}

// 超长段落必须被切开，且每一片都不超预算 —— 预算是给向量化上下文留的。
func TestSplitSplitsOversizedParagraphWithinBudget(t *testing.T) {
	options := ChunkOptions{MaxChars: 800, MinChars: 120, Overlap: 120}
	input := strings.Repeat("这是一句用来测试切分的长句子。", 120)

	chunks := Split(input, options)
	if len(chunks) < 3 {
		t.Fatalf("2800 字的单段应当切出多片，实际 %d 片", len(chunks))
	}
	for _, chunk := range chunks {
		if got := runeLength(chunk.Content); got > options.MaxChars {
			t.Errorf("第 %d 片 %d 字，超出预算 %d", chunk.Index, got, options.MaxChars)
		}
	}
}

// 无标点的长串没有语义边界可用，只能硬切，但结果同样不能超预算、更不能丢掉内容。
func TestSplitHardSplitsTextWithoutSentenceEnds(t *testing.T) {
	options := ChunkOptions{MaxChars: 200}
	input := strings.Repeat("甲", 1000)

	chunks := Split(input, options)
	if len(chunks) < 5 {
		t.Fatalf("期待硬切成多片，实际 %d 片", len(chunks))
	}
	var joined strings.Builder
	for _, chunk := range chunks {
		if got := runeLength(chunk.Content); got > options.MaxChars {
			t.Errorf("第 %d 片 %d 字，超出预算 %d", chunk.Index, got, options.MaxChars)
		}
		joined.WriteString(chunk.Content)
	}
	if joined.String() != input {
		t.Error("硬切过程中丢字或串位了")
	}
}

// 相邻切片必须带重叠：表格被切断、概念跨页时，没有重叠会让边界处的信息两边都取不到。
func TestSplitOverlapsAdjacentChunks(t *testing.T) {
	options := ChunkOptions{MaxChars: 400, MinChars: 120, Overlap: 100}
	input := strings.Repeat("这是用来验证重叠的句子。", 100)

	chunks := Split(input, options)
	if len(chunks) < 2 {
		t.Fatalf("期望切出多片，实际 %d 片", len(chunks))
	}

	previous := []rune(chunks[0].Content)
	suffix := string(previous[len(previous)-12:])
	if !strings.HasPrefix(chunks[1].Content, suffix) {
		t.Errorf("第 2 片开头应当承接第 1 片结尾的重叠内容\n第 1 片结尾: %q\n第 2 片开头: %q",
			suffix, string([]rune(chunks[1].Content)[:min(36, runeLength(chunks[1].Content))]))
	}
}

// 过短的收尾碎片单独成片没有检索价值，应当并回上一片，但不能因此丢内容。
func TestSplitMergesTinyTail(t *testing.T) {
	options := ChunkOptions{MaxChars: 800, MinChars: 120, Overlap: 120}
	head := strings.Repeat("甲", 700)
	tail := strings.Repeat("乙", 50)
	input := head + "\n\n" + tail

	chunks := Split(input, options)
	if len(chunks) != 1 {
		t.Fatalf("50 字的收尾应当并回上一片，实际 %d 片", len(chunks))
	}
	// 按字符数核对而不是找子串：head 本身超预算、被硬切过一次，
	// 合并回去时断口处会留下一个段落分隔，用子串比对会误判成"丢内容"。
	if got := strings.Count(chunks[0].Content, "甲"); got != 700 {
		t.Errorf("合并后甲字 %d 个，期望 700", got)
	}
	if got := strings.Count(chunks[0].Content, "乙"); got != 50 {
		t.Errorf("合并后乙字 %d 个，期望 50", got)
	}
}

// 代码块从中间断开就失去意义，即使超预算也要整块保留。
func TestSplitKeepsCodeFenceIntact(t *testing.T) {
	code := strings.Repeat("fmt.Println(\"hello world\")\n", 25)
	input := "## 示例\n\n```go\n" + strings.TrimRight(code, "\n") + "\n```\n"

	chunks := Split(input, ChunkOptions{MaxChars: 400, MinChars: 120, Overlap: 100})
	if len(chunks) != 1 {
		t.Fatalf("代码块应当整块保留为一片，实际 %d 片", len(chunks))
	}
	if strings.Count(chunks[0].Content, "```") != 2 {
		t.Errorf("代码块围栏被切坏了: %q", chunks[0].Content)
	}
	if !strings.Contains(chunks[0].Content, "fmt.Println") {
		t.Error("代码块内容丢失")
	}
}

// 表格同理：断开就没有对齐关系了。
func TestSplitKeepsTableIntact(t *testing.T) {
	rows := []string{"| 字段 | 说明 |", "| --- | --- |"}
	for index := 0; index < 40; index++ {
		rows = append(rows, "| 字段"+string(rune('a'+index%26))+" | 说明文字 |")
	}
	input := "## 表结构\n\n" + strings.Join(rows, "\n") + "\n"

	chunks := Split(input, ChunkOptions{MaxChars: 400, MinChars: 120, Overlap: 100})
	if len(chunks) != 1 {
		t.Fatalf("表格应当整块保留为一片，实际 %d 片", len(chunks))
	}
	for _, row := range rows {
		if !strings.Contains(chunks[0].Content, row) {
			t.Errorf("表格丢了行 %q", row)
		}
	}
}

// 夸张超长的原子块仍然要被硬切，否则单片会把上下文预算吃光。
func TestSplitHardSplitsGrosslyOversizedTable(t *testing.T) {
	options := ChunkOptions{MaxChars: 200, MinChars: 0, Overlap: 0}
	rows := []string{"| 字段 | 说明 |", "| --- | --- |"}
	for index := 0; index < 200; index++ {
		rows = append(rows, "| 字段 | 说明文字 |")
	}

	chunks := Split(strings.Join(rows, "\n"), options)
	if len(chunks) < 2 {
		t.Fatalf("远超预算的表格应当被切开，实际 %d 片", len(chunks))
	}
	for _, chunk := range chunks {
		if got := runeLength(chunk.Content); got > options.MaxChars {
			t.Errorf("第 %d 片 %d 字，超出预算 %d", chunk.Index, got, options.MaxChars)
		}
	}
}

// CRLF 与 BOM 不处理会在切片里留下看不见的脏字符。
func TestSplitNormalizesLineEndingsAndBOM(t *testing.T) {
	chunks := Split("\ufeff# 标题\r\n\r\n正文内容。\r\n", ChunkOptions{})
	if len(chunks) != 1 {
		t.Fatalf("期望 1 片，实际 %d 片", len(chunks))
	}
	if strings.ContainsAny(chunks[0].Content, "\r\ufeff") {
		t.Errorf("切片里残留了 CR 或 BOM: %q", chunks[0].Content)
	}
	if strings.Contains(chunks[0].Heading, "\r") {
		t.Errorf("标题里残留了 CR: %q", chunks[0].Heading)
	}
}

// 段落被句子切开后重新装填，拼回去必须还是原来那段话（不能凭空多出空行）。
func TestSplitDoesNotInsertBlankLinesInsideParagraph(t *testing.T) {
	options := ChunkOptions{MaxChars: 800, MinChars: 120, Overlap: 0}
	paragraph := "第一句。第二句。第三句。第四句。第五句。"
	chunks := Split(paragraph, options)

	if len(chunks) != 1 {
		t.Fatalf("短段落应当只有 1 片，实际 %d 片", len(chunks))
	}
	if chunks[0].Content != paragraph {
		t.Errorf("内容被改动了\n期望: %q\n实际: %q", paragraph, chunks[0].Content)
	}
}
