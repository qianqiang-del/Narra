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
	input := "# 一\n\n" + strings.Repeat("甲。", 600) + "\n\n# 二\n\n" + strings.Repeat("乙。", 600)

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

// 小节内容不大时整节合成一片：标题归属到整棵子树上（结构优先的切法），
// 标题之前的前言仍然独立成片、归属为空。
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
	if len(chunks) != 2 {
		t.Fatalf("期望 2 片（前言 + 整节），实际 %d 片", len(chunks))
	}
	if chunks[0].Heading != "" || chunks[0].SectionPath != "" {
		t.Errorf("标题之前的内容不该有章节归属，实际 heading=%q path=%q",
			chunks[0].Heading, chunks[0].SectionPath)
	}
	if chunks[1].Heading != "第一章 背景" || chunks[1].SectionPath != "第一章 背景" {
		t.Errorf("整节合成的一片应归属一级标题，实际 heading=%q path=%q",
			chunks[1].Heading, chunks[1].SectionPath)
	}
	for _, want := range []string{"第一章的正文。", "细节的正文。"} {
		if !strings.Contains(chunks[1].Content, want) {
			t.Errorf("整节内容丢了 %q: %q", want, chunks[1].Content)
		}
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

// 缩进四格以上的 # 是缩进代码块里的注释，不是标题（扫描器的老 bug：先 Trim 再用 # 判标题）。
func TestSplitTreatsIndentedHeadingAsCode(t *testing.T) {
	input := strings.Join([]string{
		"# 配置",
		"",
		"示例：",
		"",
		"    # 启动命令",
		"    python parse.py --input a.pdf",
		"",
		"说明文字继续。",
	}, "\n")

	chunks := Split(input, ChunkOptions{})
	if len(chunks) != 1 {
		t.Fatalf("示例不该切出新的节，实际 %d 片", len(chunks))
	}
	if chunks[0].Heading != "配置" || chunks[0].SectionPath != "配置" {
		t.Errorf("缩进代码不该开新节，实际 heading=%q path=%q",
			chunks[0].Heading, chunks[0].SectionPath)
	}
	for _, want := range []string{"# 启动命令", "python parse.py", "说明文字继续。"} {
		if !strings.Contains(chunks[0].Content, want) {
			t.Errorf("内容丢了 %q: %q", want, chunks[0].Content)
		}
	}
}

// 下划线式标题识别但不当作节边界：降级为普通段落，下划线行不保留。
func TestSplitTreatsSetextHeadingAsText(t *testing.T) {
	input := strings.Join([]string{
		"文档标题",
		"========",
		"",
		"正文段落。",
		"",
		"小节标题",
		"--------",
		"",
		"小节的正文。",
	}, "\n")

	chunks := Split(input, ChunkOptions{})
	if len(chunks) != 1 {
		t.Fatalf("setext 不当节，应当整段 1 片，实际 %d 片", len(chunks))
	}
	if chunks[0].Heading != "" {
		t.Errorf("setext 不该产生章节归属，实际 %q", chunks[0].Heading)
	}
	for _, want := range []string{"文档标题", "正文段落。", "小节标题", "小节的正文。"} {
		if !strings.Contains(chunks[0].Content, want) {
			t.Errorf("内容丢了 %q: %q", want, chunks[0].Content)
		}
	}
	if strings.Contains(chunks[0].Content, "====") || strings.Contains(chunks[0].Content, "----") {
		t.Errorf("下划线行是排版符号，不该留在正文里: %q", chunks[0].Content)
	}
}

// 引用块里的 # 是引用内容里的文字，不是顶层标题，不该开新节。
func TestSplitIgnoresHeadingInsideBlockquote(t *testing.T) {
	input := "# 主标题\n\n> # 引用里的标题\n> 引用正文。\n\n正常正文。\n"

	chunks := Split(input, ChunkOptions{})
	if len(chunks) != 1 {
		t.Fatalf("引用里的标题不该开新节，实际 %d 片", len(chunks))
	}
	if chunks[0].Heading != "主标题" {
		t.Errorf("节归属应当是主标题，实际 %q", chunks[0].Heading)
	}
	for _, want := range []string{"引用里的标题", "正常正文。"} {
		if !strings.Contains(chunks[0].Content, want) {
			t.Errorf("内容丢了 %q: %q", want, chunks[0].Content)
		}
	}
}

// 节超预算时按子标题递归，路径带上完整层级；叶子才回到句子边界。
func TestSplitRecursesIntoSubsectionsWithPaths(t *testing.T) {
	input := strings.Join([]string{
		"# 第一章",
		"",
		strings.Repeat("章首甲。", 75), // 300 字
		"",
		"## 1.1 小节甲",
		"",
		strings.Repeat("乙。", 450), // 900 字，超预算
		"",
		"## 1.2 小节乙",
		"",
		strings.Repeat("丙。", 75), // 150 字
	}, "\n")

	chunks := Split(input, ChunkOptions{})
	if len(chunks) < 4 {
		t.Fatalf("超预算的节应当递归切出多片，实际 %d 片", len(chunks))
	}

	// 第 0 片是第一章自己的引文；1.1 超预算被切成两片；1.2 自成一片。
	if chunks[0].Heading != "第一章" || chunks[0].SectionPath != "第一章" {
		t.Errorf("第 0 片归属不对: heading=%q path=%q", chunks[0].Heading, chunks[0].SectionPath)
	}
	if !strings.Contains(chunks[0].Content, "章首甲。") {
		t.Errorf("第 0 片内容不对: %q", chunks[0].Content)
	}
	if chunks[1].SectionPath != "第一章/1.1 小节甲" || chunks[2].SectionPath != "第一章/1.1 小节甲" {
		t.Errorf("1.1 的两片路径不对: %q / %q", chunks[1].SectionPath, chunks[2].SectionPath)
	}
	last := chunks[len(chunks)-1]
	if last.Heading != "1.2 小节乙" || last.SectionPath != "第一章/1.2 小节乙" {
		t.Errorf("末片归属不对: heading=%q path=%q", last.Heading, last.SectionPath)
	}
	// 小节之间可以补重叠（同一父节内的相邻组），但重叠前缀必须从句首开始。
	for index := 1; index < len(chunks); index++ {
		if strings.HasPrefix(chunks[index].Content, "\n") {
			t.Errorf("第 %d 片以换行开头: %q", index, chunks[index].Content)
		}
	}
}

// 幻灯片式文档（每页一个小标题、内容一两行）应当把小节并成一片，
// 而不是每个小标题各出一个碎片。
func TestSplitMergesTinySiblingSections(t *testing.T) {
	input := strings.Join([]string{
		"# 第一页",
		"",
		"要点一。",
		"",
		"# 第二页",
		"",
		"要点二。",
		"",
		"# 第三页",
		"",
		"要点三。",
	}, "\n")

	chunks := Split(input, ChunkOptions{})
	if len(chunks) != 1 {
		t.Fatalf("三页都过小，应当并成 1 片，实际 %d 片", len(chunks))
	}
	if chunks[0].Heading != "第一页" || chunks[0].SectionPath != "第一页" {
		t.Errorf("合并后的归属应当是接收片（第一页），实际 heading=%q path=%q",
			chunks[0].Heading, chunks[0].SectionPath)
	}
	for _, want := range []string{"要点一。", "要点二。", "要点三。"} {
		if !strings.Contains(chunks[0].Content, want) {
			t.Errorf("合并后丢了 %q: %q", want, chunks[0].Content)
		}
	}
}
