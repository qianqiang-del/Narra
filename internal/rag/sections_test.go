package rag

import (
	"strings"
	"testing"
)

// front matter 是元数据不是正文：被解析器消费掉，不会漏进切片，也不会因为里面的
// "# 注释" 开出一个假节。
func TestSplitSkipsFrontMatter(t *testing.T) {
	input := strings.Join([]string{
		"---",
		"title: 第 3 章 讲义",
		"tags: [rag, 检索]",
		"# 内部备注：还没有定稿",
		"---",
		"",
		"# 第 3 章 正文标题",
		"",
		"正文内容。",
	}, "\n")

	chunks := Split(input, ChunkOptions{})
	if len(chunks) != 1 {
		t.Fatalf("期望 1 片，实际 %d 片", len(chunks))
	}
	if chunks[0].Heading != "第 3 章 正文标题" {
		t.Errorf("节标题应当是正文标题，实际 %q", chunks[0].Heading)
	}
	for _, unwanted := range []string{"title:", "tags:", "内部备注"} {
		if strings.Contains(chunks[0].Content, unwanted) {
			t.Errorf("front matter 漏进了正文: %q 出现在 %q", unwanted, chunks[0].Content)
		}
	}
}

// FrontMatterTitle 的口径：只在文档确实以 --- 开头且闭合时读 title，
// 其余情况（没有、没 title、没闭合）一律返回空串，交给调用方的回落链。
func TestFrontMatterTitle(t *testing.T) {
	withTitle := "---\ntitle: 讲义标题\ntags: [a]\n---\n\n# 正文标题\n\n正文。"
	if got := FrontMatterTitle(withTitle); got != "讲义标题" {
		t.Errorf("title = %q，期望 讲义标题", got)
	}

	withoutTitle := "---\ntags: [a]\n---\n\n# 正文标题"
	if got := FrontMatterTitle(withoutTitle); got != "" {
		t.Errorf("没有 title 应当返回空串，实际 %q", got)
	}

	if got := FrontMatterTitle("# 没有 front matter"); got != "" {
		t.Errorf("没有 front matter 应当返回空串，实际 %q", got)
	}

	// 没有闭合的 --- 不当作 front matter（保守：宁可当正文，也不吞掉正文）。
	unclosed := "---\ntitle: 讲义\n\n正文。"
	if got := FrontMatterTitle(unclosed); got != "" {
		t.Errorf("未闭合的 front matter 应当返回空串，实际 %q", got)
	}
}

// 开头只有一条 ---（没有闭合）不是 front matter：goldmark-meta 会把这种文档整篇
// 吞成一个纯文本块、标题全部失效，所以那一条分隔线要提前删掉。
func TestSplitKeepsHeadingsAfterUnclosedDashLine(t *testing.T) {
	input := "---\n\n# 标题\n\n正文。"
	chunks := Split(input, ChunkOptions{})
	if len(chunks) != 1 {
		t.Fatalf("期望 1 片，实际 %d 片", len(chunks))
	}
	if chunks[0].Heading != "标题" {
		t.Errorf("开头孤立的分隔线不该让标题失效，实际 heading=%q", chunks[0].Heading)
	}
	if !strings.Contains(chunks[0].Content, "正文。") {
		t.Errorf("正文丢了: %q", chunks[0].Content)
	}
}

// 未闭合的开场 --- 之后跟着元数据样式的文字与标题：分隔线丢弃，其余原样保留，
// 后面的标题仍然生效。
func TestSplitKeepsTextAfterUnclosedFrontMatter(t *testing.T) {
	input := "---\ntitle: x\n\n# 标题\n\n正文。"
	chunks := Split(input, ChunkOptions{})
	if len(chunks) != 2 {
		t.Fatalf("期望 2 片（前言 + 标题节），实际 %d 片", len(chunks))
	}
	if !strings.Contains(chunks[0].Content, "title: x") {
		t.Errorf("未闭合元数据样式的文字不该丢: %q", chunks[0].Content)
	}
	if chunks[1].Heading != "标题" || !strings.Contains(chunks[1].Content, "正文。") {
		t.Errorf("标题节不对: heading=%q content=%q", chunks[1].Heading, chunks[1].Content)
	}
}

// 标题回落链：显式标题由调用方负责，这里只验证"front matter 的 title 优先于
// 正文一级标题，正文一级标题优先于文件名"。
func TestPreferHeadingTitleUsesFrontMatterFirst(t *testing.T) {
	withFrontMatter := "---\ntitle: 讲义标题\n---\n\n# 正文标题\n\n正文。"
	if got := preferHeadingTitle("文件名.md", withFrontMatter); got != "讲义标题" {
		t.Errorf("标题 = %q，期望取 front matter 的 title", got)
	}

	// front matter 没有 title 时，要跳过整块去拿正文的一级标题。
	withoutTitle := "---\ntags: [a]\n---\n\n# 正文标题\n\n正文。"
	if got := preferHeadingTitle("文件名.md", withoutTitle); got != "正文标题" {
		t.Errorf("标题 = %q，期望跳过 front matter 后取正文一级标题", got)
	}

	// 开头一条未闭合的 ---（分隔线）不该挡住一级标题的回落。
	if got := preferHeadingTitle("文件名.md", "---\n\n# 正文标题\n\n正文。"); got != "正文标题" {
		t.Errorf("标题 = %q，期望跳过孤立分隔线后取正文一级标题", got)
	}

	if got := preferHeadingTitle("文件名.md", "# 正文标题\n\n正文。"); got != "正文标题" {
		t.Errorf("标题 = %q，期望取正文一级标题", got)
	}

	if got := preferHeadingTitle("文件名.md", "正文没有标题。"); got != "文件名.md" {
		t.Errorf("标题 = %q，期望回落到文件名", got)
	}
}
