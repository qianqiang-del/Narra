package classroom

import (
	"fmt"
	"sort"
	"strings"

	"golang.org/x/net/html"
)

// maxReviewVisibleTextRunes 是送进审核的交互页可见文本长度上限。
//
// 可见文本本来就远小于原始 HTML（没有标签、样式与脚本），这里再兜一道，
// 防止数据探索类页面把整张表都塞进审核提示词。
const maxReviewVisibleTextRunes = 24000

// reviewVisibleSkipTags 是抽取可见文本时要整棵跳过的子树。
// 它们要么是脚本/样式这类非正文，要么是内联图形，审核看正文与交互清单就够。
var reviewVisibleSkipTags = map[string]struct{}{
	"script":   {},
	"style":    {},
	"noscript": {},
	"template": {},
	"svg":      {},
}

// reviewHeadingTags 是抽取正文时用来标层级的标题标签。
var reviewHeadingTags = map[string]struct{}{
	"h1": {}, "h2": {}, "h3": {}, "h4": {}, "h5": {}, "h6": {},
}

// summarizeHTMLForReview 把一份交互 HTML 压成审核看得懂、又比原文省得多的一份摘要。
//
// 审核对交互页的专属要求是"确实能操作、不是静态文字"，这一点由可交互元素与事件绑定清单直接给出；
// 其余维度（信息密度、内容与讲稿是否一致）判的是页面讲了什么，由全部可见文本承载。
// 原始标签、CSS 与脚本对审核没有价值，只是白占 token，因此不送。
//
// 解析失败时退回旧的"截断原文"行为，绝不因为摘要失败而少给审核信息。
func summarizeHTMLForReview(document string) string {
	root, err := html.Parse(strings.NewReader(document))
	if err != nil {
		return fallbackRawHTMLReview(document)
	}

	var (
		texts      []string
		controls   = map[string]int{}
		inputTypes = map[string]int{}
		events     int
	)
	walkReviewHTML(root, &texts, controls, inputTypes, &events)

	var builder strings.Builder
	fmt.Fprintf(&builder, "（原文档 %d 字节，下面给出全部可见文本与可交互元素清单，原始标签、样式与脚本已省略）\n", len(document))
	builder.WriteString("可交互元素：")
	builder.WriteString(renderControlDigest(controls, inputTypes))
	fmt.Fprintf(&builder, "；脚本事件绑定 %d 处。\n", events)
	builder.WriteString("可见文本：\n")
	builder.WriteString(joinReviewText(texts))
	return builder.String()
}

// walkReviewHTML 深度优先遍历文档树，收集可见文本、可交互元素与事件绑定。
func walkReviewHTML(n *html.Node, texts *[]string, controls, inputTypes map[string]int, events *int) {
	if n.Type == html.ElementNode {
		if n.Data == "script" {
			// 脚本不进可见文本，但事件绑定数要从脚本里数出来：
			// 内联 on* 属性只是绑定的一种写法，交互页更常用 addEventListener，
			// 漏数会让审核误以为页面是静态文字。
			countScriptEvents(n, events)
			return
		}
		if _, skip := reviewVisibleSkipTags[n.Data]; skip {
			return
		}
		switch n.Data {
		case "button", "select", "textarea", "canvas":
			controls[n.Data]++
		case "input":
			controls["input"]++
			typ := strings.TrimSpace(attrValue(n, "type"))
			if typ == "" {
				typ = "text"
			}
			inputTypes[typ]++
		}
		for _, attr := range n.Attr {
			if strings.HasPrefix(attr.Key, "on") && len(attr.Key) > 2 {
				*events++
			}
		}
		if _, isHeading := reviewHeadingTags[n.Data]; isHeading {
			if text := nodeText(n); text != "" {
				*texts = append(*texts, "# "+text)
			}
			return
		}
	}
	if n.Type == html.TextNode {
		if text := collapseHTMLSpace(n.Data); text != "" {
			*texts = append(*texts, text)
		}
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		walkReviewHTML(child, texts, controls, inputTypes, events)
	}
}

// countScriptEvents 数一段脚本里的事件绑定。
//
// 只看 addEventListener：它覆盖交互页的绝大多数绑定写法。数出来的是"绑了几处"这个量级，
// 供审核判断"页面确实挂了事件"，不需要精确到每一种写法。
func countScriptEvents(script *html.Node, events *int) {
	var builder strings.Builder
	for child := script.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.TextNode {
			builder.WriteString(child.Data)
		}
	}
	*events += strings.Count(strings.ToLower(builder.String()), "addeventlistener")
}

// nodeText 取一个元素子树里的全部可见文本，跳过脚本与样式。
func nodeText(n *html.Node) string {
	var builder strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.TextNode {
			builder.WriteString(x.Data)
			builder.WriteByte(' ')
		}
		for child := x.FirstChild; child != nil; child = child.NextSibling {
			if child.Type == html.ElementNode {
				if _, skip := reviewVisibleSkipTags[child.Data]; skip {
					continue
				}
			}
			walk(child)
		}
	}
	walk(n)
	return collapseHTMLSpace(builder.String())
}

// renderControlDigest 把可交互元素统计排成一行，顺序固定，便于审核比对。
func renderControlDigest(controls, inputTypes map[string]int) string {
	parts := make([]string, 0, len(controls)+1)
	for _, tag := range []string{"button", "input", "select", "textarea", "canvas"} {
		if count := controls[tag]; count > 0 {
			parts = append(parts, fmt.Sprintf("%s×%d", tag, count))
		}
	}
	if len(inputTypes) > 0 {
		types := make([]string, 0, len(inputTypes))
		for typ := range inputTypes {
			types = append(types, typ)
		}
		sort.Strings(types)
		detail := make([]string, 0, len(types))
		for _, typ := range types {
			detail = append(detail, fmt.Sprintf("%s×%d", typ, inputTypes[typ]))
		}
		parts = append(parts, "输入框类型("+strings.Join(detail, "、")+")")
	}
	if len(parts) == 0 {
		return "无（这份文档里没有任何可操作元素，注意核对它是不是静态文字）"
	}
	return strings.Join(parts, "，")
}

// joinReviewText 把可见文本按行连起来，超出上限按字符截断并如实说明。
func joinReviewText(texts []string) string {
	joined := strings.Join(texts, "\n")
	runes := []rune(joined)
	if len(runes) <= maxReviewVisibleTextRunes {
		return joined
	}
	return string(runes[:maxReviewVisibleTextRunes]) +
		fmt.Sprintf("\n（可见文本过长，这里只给前 %d 个字符）", maxReviewVisibleTextRunes)
}

// fallbackRawHTMLReview 是解析失败时的退路：沿用旧的"截断原文"行为。
func fallbackRawHTMLReview(document string) string {
	if len(document) > maxReviewHTMLBytes {
		trimmed := strings.ToValidUTF8(document[:maxReviewHTMLBytes], "")
		return fmt.Sprintf("（原文档 %d 字节，这里只给前 %d 字节）\n%s", len(document), maxReviewHTMLBytes, trimmed)
	}
	return fmt.Sprintf("（原文档 %d 字节）\n%s", len(document), document)
}

// attrValue 取一个元素某个属性的值，没有则返回空串。
func attrValue(n *html.Node, key string) string {
	for _, attr := range n.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

// collapseHTMLSpace 把连续空白折成一个空格并去掉首尾空白。
func collapseHTMLSpace(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
