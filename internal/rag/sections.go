package rag

// sections.go 是切分的"结构识别"层：把 Markdown 解析成一棵"节树"。
//
// 与 chunker.go 的分工：本文件只回答"文档里有哪些节、谁是谁的子节、每个块的原文是什么"；
// 字符预算、句子边界、重叠这些装填策略全在 chunker.go。两层之间的契约就是
// sectionNode 与 block 两个类型 —— 换解析器时只动本文件。
//
// 为什么不再自己扫行判结构：标题判定看着简单，实际要照顾缩进代码块（顶 4 格空格
// 意味着"这是代码"，里面的 # 不是标题）、下划线式标题、HTML 块、引用/列表里的嵌套
// 标题这些边角。手写扫描器每冒出一个新写法就得补一课，而 goldmark 按 CommonMark
// 规范实现、有人长期维护，结构判定一次到位。代价是多一个纯内存依赖：切分链路仍然
// 不读文件、不认识数据库、不发网络请求，"能离线测"这条性质没有变化。
//
// 本层刻意做了两个取舍，都体现在 parseSections 里：
//   - 只有**顶层**标题开节：引用块、列表项内部的标题按普通文字收进内容；
//   - 下划线式（setext）标题识别但**不当节边界**：降级为普通段落，只保留标题文字。
//
// front matter（文档头的 YAML 元数据）由 goldmark-meta 扩展在解析时消费掉，
// 不会漏进正文；标题回落要用的 title 由 FrontMatterTitle 单独读一次。

import (
	"bytes"
	"strings"

	"github.com/yuin/goldmark"
	meta "github.com/yuin/goldmark-meta"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// markdownParser 是切分链路共用的解析器：goldmark 本体 + 表格扩展 + front matter 扩展。
// 它无状态、可并发复用；扩展只开这两个 —— 每开一个扩展都是一份新的行为契约，
// 用不上的（删除线、任务列表、脚注……）一律不挂。
var markdownParser = goldmark.New(goldmark.WithExtensions(extension.Table, meta.Meta))

// sectionNode 是一个节，也是节树的节点。
//
// level 只在建树时用（新标题挂到"最近的、级别更小的节"下面）；path 是给切片的
// 节身份（从顶层到本级、用 / 连接），heading 是本级标题 —— 两者都按 rune 截断后落库。
type sectionNode struct {
	heading  string
	level    int
	path     string
	blocks   []block
	children []*sectionNode
}

// parseSections 把 Markdown 解析成根级节列表（按文档顺序）。
//
// 前言（第一个标题之前的内容）单独成为一个 heading 为空的节点，与改造前的行为一致；
// 空正文产出空列表，由收录门面判断"空正文算不算失败"。
func parseSections(markdown string) []*sectionNode {
	markdown = dropUnclosedFrontMatterDelimiter(markdown)
	source := []byte(markdown)
	document := markdownParser.Parser().Parse(text.NewReader(source))

	// 栈顶永远是"当前所在节"；根节点不产出切片，只承接前言与顶层节。
	root := &sectionNode{}
	stack := []*sectionNode{root}

	for child := document.FirstChild(); child != nil; child = child.NextSibling() {
		if heading, ok := child.(*ast.Heading); ok {
			if !headingIsATX(heading, source) {
				// 下划线式标题：识别出来但不当节，按段落收进当前节（下划线行不保留）。
				if title := strings.TrimSpace(string(heading.Text(source))); title != "" {
					current := stack[len(stack)-1]
					current.blocks = append(current.blocks, blockOf(title, false))
				}
				continue
			}
			// 弹出所有级别不小于它的节：剩下的栈顶就是父节（跳级不安全靠数字，
			// 这里按"最近的小级别"挂，## 下直接跟 #### 也能正确当子节）。
			for len(stack) > 1 && stack[len(stack)-1].level >= heading.Level {
				stack = stack[:len(stack)-1]
			}
			parent := stack[len(stack)-1]
			title := strings.TrimSpace(string(heading.Text(source)))
			node := &sectionNode{
				heading: title,
				level:   heading.Level,
				path:    joinSectionPath(parent.path, title),
			}
			parent.children = append(parent.children, node)
			stack = append(stack, node)
			continue
		}

		if item, ok := blockFromNode(child, source); ok {
			current := stack[len(stack)-1]
			current.blocks = append(current.blocks, item)
		}
	}

	sections := make([]*sectionNode, 0, len(root.children)+1)
	if len(root.blocks) > 0 {
		sections = append(sections, &sectionNode{blocks: root.blocks})
	}
	return append(sections, root.children...)
}

// headingIsATX 判断标题是 ATX（# 开头）还是下划线式。
//
// goldmark 不区分这两种（都解析成 Heading），但行段起点所在行的行首是不是 # 是可靠判据：
// 下划线式标题的文字行不可能以 # 开头（真以 # 开头就会被解析成 ATX）。
// 行段为空的空标题（如单独一行 "#"）没有可看的文字行，按 ATX 处理。
func headingIsATX(heading *ast.Heading, source []byte) bool {
	lines := heading.Lines()
	if lines.Len() == 0 {
		return true
	}
	start := lines.At(0).Start
	line := source[lineStartAt(source, start):lineEndFrom(source, start)]
	return bytes.HasPrefix(bytes.TrimLeft(line, " \t"), []byte("#"))
}

// blockFromNode 把一个顶层块节点翻成切分用的 block。
//
// 代码块与表格是原子块（切开就失去意义）；其余（段落、列表、引用、HTML 块）
// 按普通文本收。分隔线（ThematicBreak）没有可取的原文，直接跳过 —— 它是排版符号，
// 对检索没有价值。取原文一律走"行段范围 + 扩到整行"，这样列表符号、引用前缀、
// 代码缩进这些行首标记都保得住，与改造前的内容形态最接近。
func blockFromNode(node ast.Node, source []byte) (block, bool) {
	switch node.Kind() {
	case ast.KindThematicBreak:
		return block{}, false
	case ast.KindFencedCodeBlock, ast.KindCodeBlock, extast.KindTable:
		content := sourceOfNode(node, source)
		if node.Kind() == ast.KindFencedCodeBlock {
			// 行段只覆盖代码正文，围栏不在段里；不补的话切片里是一段没有边界的裸代码。
			content = sourceOfFencedCode(node, source)
		}
		if strings.TrimSpace(content) == "" {
			return block{}, false
		}
		return blockOf(content, true), true
	default:
		content := sourceOfNode(node, source)
		if strings.TrimSpace(content) == "" {
			return block{}, false
		}
		return blockOf(content, false), true
	}
}

// sourceOfNode 取一个块节点的原文：把节点与全部后代的文字行段合并成一个范围，
// 再扩到完整行（行首标记就在扩出来的那一段里）。
func sourceOfNode(node ast.Node, source []byte) string {
	start, stop, ok := segmentRange(node)
	if !ok {
		return ""
	}
	return string(source[lineStartAt(source, start):lineEndAfter(source, stop)])
}

// sourceOfFencedCode 取围栏代码块的原文，并把首尾围栏行一起带上。
//
// 围栏行不属于行段（段从代码正文开始），但它是"这是代码块"的边界标记，
// 切片里不能丢；这里按约定往上一行、往下一行各看一次是不是围栏行。
// 空代码块（没有正文行段）取不到位置，返回空串由调用方丢弃 —— 它没有可检索的内容。
func sourceOfFencedCode(node ast.Node, source []byte) string {
	start, stop, ok := segmentRange(node)
	if !ok {
		return ""
	}
	start = lineStartAt(source, start)
	stop = lineEndAfter(source, stop)

	if start > 0 {
		previousStart := lineStartAt(source, start-1)
		if isFenceLine(source[previousStart:start]) {
			start = previousStart
		}
	}
	if stop < len(source) {
		nextEnd := lineEndFrom(source, stop)
		if isFenceLine(source[stop:nextEnd]) {
			stop = nextEnd
		}
	}
	return string(source[start:stop])
}

// isFenceLine 判断一行是不是围栏行（``` 或 ~~~ 开头，允许前置空格）。
func isFenceLine(line []byte) bool {
	trimmed := bytes.TrimLeft(line, " \t")
	return bytes.HasPrefix(trimmed, []byte("```")) || bytes.HasPrefix(trimmed, []byte("~~~"))
}

// segmentRange 合并节点（含全部后代）所有文字行段的范围。
// 容器节点（列表、引用）自己没有行段，范围靠后代拼出来。
func segmentRange(node ast.Node) (int, int, bool) {
	start, stop := -1, -1
	_ = ast.Walk(node, func(current ast.Node, entering bool) (ast.WalkStatus, error) {
		// Lines() 只对块节点可用；行内节点（文本、强调……）调它会直接 panic。
		if !entering || current.Type() != ast.TypeBlock {
			return ast.WalkContinue, nil
		}
		lines := current.Lines()
		for index := 0; index < lines.Len(); index++ {
			segment := lines.At(index)
			if start < 0 || segment.Start < start {
				start = segment.Start
			}
			if segment.Stop > stop {
				stop = segment.Stop
			}
		}
		return ast.WalkContinue, nil
	})
	if start < 0 || stop <= start {
		return 0, 0, false
	}
	return start, stop, true
}

// lineStartAt 返回包含 offset 的那一行的起点。
func lineStartAt(source []byte, offset int) int {
	if offset <= 0 {
		return 0
	}
	if offset > len(source) {
		offset = len(source)
	}
	if index := bytes.LastIndexByte(source[:offset], '\n'); index >= 0 {
		return index + 1
	}
	return 0
}

// lineEndFrom 返回从 pos 开始的这一行的终点（含换行）；pos 必须落在行首或行内。
func lineEndFrom(source []byte, pos int) int {
	if pos >= len(source) {
		return len(source)
	}
	if index := bytes.IndexByte(source[pos:], '\n'); index >= 0 {
		return pos + index + 1
	}
	return len(source)
}

// lineEndAfter 返回"以 end 为终点的那一行"的完整终点（含换行）。
// 行段终点的口径不统一（有的含换行、有的不含），两种都要认：
// 前一个字节已经是换行说明该行本来就完整，否则往后找到本行结尾。
func lineEndAfter(source []byte, end int) int {
	if end <= 0 {
		return 0
	}
	if end > len(source) {
		return len(source)
	}
	if source[end-1] == '\n' {
		return end
	}
	return lineEndFrom(source, end)
}

// joinSectionPath 拼接节的完整路径。用 / 连接；父路径为空（顶层节）时就是本级标题。
func joinSectionPath(parent, title string) string {
	if parent == "" {
		return title
	}
	return parent + "/" + title
}

// FrontMatterTitle 读文档头 YAML front matter 里的 title，读不到返回空串。
//
// 只服务于"调用方没指定标题"时的回落链：没有 front matter、没有 title 键、
// title 不是字符串（列表/数字之类），都算读不到，由调用方走原来的回落顺序。
// 单独解析一次（而不是让 Split 带出来）是因为 Split 的签名被 Eino 适配器与收录链路
// 共用，不该为了一个可选的标题字段改它；收录是异步的，这点解析开销可以忽略。
func FrontMatterTitle(markdown string) string {
	markdown = normalize(markdown)
	if frontMatterRange(strings.Split(markdown, "\n")) == 0 {
		return ""
	}

	ctx := parser.NewContext()
	markdownParser.Parser().Parse(text.NewReader([]byte(markdown)), parser.WithContext(ctx))
	title, _ := meta.Get(ctx)["title"].(string)
	return strings.TrimSpace(title)
}

// frontMatterRange 返回 front matter 占用的行数（从第 0 行到闭合 --- 的下一行）；
// 没有 front matter、或者只有开头一条 --- 没有闭合，都返回 0。
//
// "第一行必须是 ---"是 YAML front matter 的约定；"没闭合就不认"是保守选择 ——
// 宁可把它当正文，也不能把整篇文档当元数据吞掉。
func frontMatterRange(lines []string) int {
	if len(lines) == 0 || strings.TrimRight(lines[0], " \t") != "---" {
		return 0
	}
	for index := 1; index < len(lines); index++ {
		if strings.TrimRight(lines[index], " \t") == "---" {
			return index + 1
		}
	}
	return 0
}

// dropUnclosedFrontMatterDelimiter 处理"开头是 ---、全篇却没有第二条 ---"的情况：
// 删掉这第一行。
//
// goldmark-meta 遇到这种"有开场没闭合"的文档，会把整篇当成 front matter 候选，
// 最终解析成一个纯文本块（实测节点只剩一个 TextBlock）—— 标题全部失效。而这一行
// 本来就只是一个分隔线（ThematicBreak，最终不会进切片），先删掉它，后面的解析
// 就恢复正常。真正的 front matter（有闭合）不受影响。
//
// 触发它的场景是真实存在的：纯文本笔记（.txt 直读，不过 Markdown 转换）开头常用
// 一条 --- 当分隔线；文档转换产物开头也可能带一条分隔线。
func dropUnclosedFrontMatterDelimiter(markdown string) string {
	lines := strings.Split(markdown, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], " \t") != "---" {
		return markdown
	}
	if frontMatterRange(lines) != 0 {
		return markdown
	}
	return strings.Join(lines[1:], "\n")
}
