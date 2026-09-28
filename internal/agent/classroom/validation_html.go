package classroom

import (
	"fmt"
	"regexp"
	"strings"
)

// interactiveAnchorKey 是交互页在内容块里的讲解锚点，讲稿段落挂在它上面。
const interactiveAnchorKey = "interactive-root"

// maxAnchorRunes 是讲解锚点正文的长度上限。
//
// 锚点只是讲稿与旧渲染器的落点，不需要写全；完整的一页要讲什么由计划里的 brief 承载，
// 讲稿提示词本来就带上了它。
const maxAnchorRunes = 200

// maxInteractiveHTMLBytes 是一份交互 HTML 的字节上限。
//
// 比整页内容块（maxPageContentBytes）宽得多：一份带内联样式与脚本的完整文档本来就大。
// 但也不能不设限——这一列每次读页面都要整份传给前端。
const maxInteractiveHTMLBytes = 256 * 1024

// htmlForbiddenRule 是一条安全禁令，命中即判这一页不通过。
//
// 字符串检查只能提前拦掉明显不合规的内容，真正的安全边界是浏览器的 iframe sandbox
// （交互场景设计稿 §6.2）：这里漏过去的写法，到了前端也读不到宿主页面。
type htmlForbiddenRule struct {
	pattern *regexp.Regexp
	reason  string
}

var htmlForbiddenRules = []htmlForbiddenRule{
	{regexp.MustCompile(`(?i)window\s*\.\s*parent`), "访问 window.parent"},
	{regexp.MustCompile(`(?i)\bparent\s*\.\s*document`), "访问 parent.document"},
	{regexp.MustCompile(`(?i)\btop\s*\.\s*document`), "访问 top.document"},
	{regexp.MustCompile(`(?i)\beval\s*\(`), "使用 eval"},
	{regexp.MustCompile(`(?i)new\s+Function\s*\(`), "使用 new Function"},
	{regexp.MustCompile(`(?i)\bfetch\s*\(`), "发起 fetch 请求"},
	{regexp.MustCompile(`(?i)XMLHttpRequest`), "发起 XMLHttpRequest 请求"},
	{regexp.MustCompile(`(?i)WebSocket`), "建立 WebSocket 连接"},
	{regexp.MustCompile(`(?i)EventSource`), "建立 EventSource 连接"},
	{regexp.MustCompile(`(?i)document\s*\.\s*cookie`), "访问 cookie"},
	{regexp.MustCompile(`(?i)\bwindow\s*\.\s*open\s*\(`), "自动打开窗口"},
	{regexp.MustCompile(`(?i)\blocalStorage\b`), "使用 localStorage"},
	{regexp.MustCompile(`(?i)\bsessionStorage\b`), "使用 sessionStorage"},
	{regexp.MustCompile(`(?i)\blocation\s*\.\s*(?:href|assign|replace)`), "跳转页面"},
	{regexp.MustCompile(`(?i)<form\b`), "使用 form 表单"},
	{regexp.MustCompile(`(?i)@import`), "引入外部样式"},
	{regexp.MustCompile(`(?i)https?://`), "引用外部网络地址"},
	{regexp.MustCompile(`(?i)(?:src|href)\s*=\s*["']\s*//`), "引用外部网络地址"},
}

// htmlNamespaceURIs 是允许出现的命名空间地址。内联 SVG 的 xmlns 就是一个 http 地址，
// 它是命名空间标识而不是网络资源，必须放行，否则模型写的每个 SVG 都要被判违规重来。
var htmlNamespaceURIs = []string{
	"http://www.w3.org/2000/svg",
	"http://www.w3.org/1999/xlink",
	"http://www.w3.org/1999/xhtml",
}

// htmlInteractivePattern 是「页面上有可操作元素」的判据。
var htmlInteractivePattern = regexp.MustCompile(`(?i)<(?:button|input|select|textarea|canvas)\b`)

// htmlScriptPattern 是「页面里真的有脚本」的判据。
var htmlScriptPattern = regexp.MustCompile(`(?i)<script\b`)

// htmlEventPattern 是「脚本里有事件绑定」的判据。
var htmlEventPattern = regexp.MustCompile(`(?i)\bon[a-z]+\s*=|\baddEventListener\s*\(`)

// extractHTMLDocument 从模型输出里取出一份完整 HTML 文档。
//
// 文档前后的解释文字与代码围栏一律剥掉：出现这些多半只是模型的书写习惯，不影响内容本身。
// 但输出里出现两份文档就是没按要求做，属于要回灌给模型的错误，不替它猜想给哪一份。
func extractHTMLDocument(raw string) (string, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", fmt.Errorf("模型没有输出任何内容")
	}
	if strings.HasPrefix(text, "```") {
		if newline := strings.IndexByte(text, '\n'); newline >= 0 {
			text = text[newline+1:]
		}
		if end := strings.LastIndex(text, "```"); end >= 0 {
			text = text[:end]
		}
		text = strings.TrimSpace(text)
	}

	lower := strings.ToLower(text)
	start := strings.Index(lower, "<!doctype")
	if start < 0 {
		start = strings.Index(lower, "<html")
	}
	if start < 0 {
		return "", fmt.Errorf("输出里没有 HTML 文档（找不到 <!doctype html> 或 <html>）")
	}
	end := strings.LastIndex(lower, "</html>")
	if end < 0 {
		return "", fmt.Errorf("HTML 文档不完整（没有 </html>）")
	}
	document := strings.TrimSpace(text[start : end+len("</html>")])

	lowerDocument := strings.ToLower(document)
	if count := strings.Count(lowerDocument, "<!doctype"); count > 1 {
		return "", fmt.Errorf("输出里有 %d 份 HTML 文档，只能输出一份", count)
	}
	if count := strings.Count(lowerDocument, "<html"); count > 1 {
		return "", fmt.Errorf("文档里出现了 %d 次 <html>，只能有一份文档", count)
	}
	return document, nil
}

// validateHTMLDocument 校验一份交互 HTML：体积、结构、安全与交互质量。
func validateHTMLDocument(document string) error {
	if size := len(document); size > maxInteractiveHTMLBytes {
		return fmt.Errorf("HTML 过大（%d 字节，上限 %d 字节）：把内联样式与脚本压紧，去掉重复的示例数据",
			size, maxInteractiveHTMLBytes)
	}
	lower := strings.ToLower(document)
	if !strings.Contains(lower, "<head") {
		return fmt.Errorf("文档缺少 <head>")
	}
	if !strings.Contains(lower, "<body") {
		return fmt.Errorf("文档缺少 <body>")
	}
	if err := validateHTMLSecurity(document); err != nil {
		return err
	}
	return validateHTMLQuality(document)
}

// validateHTMLSecurity 按黑名单拦掉访问宿主、动态求值与联网的写法。
func validateHTMLSecurity(document string) error {
	// 先摘掉命名空间地址，免得「外部网络地址」那条把内联 SVG 误伤成违规。
	scan := scrubNamespaceURIs(document)
	for _, rule := range htmlForbiddenRules {
		if rule.pattern.MatchString(scan) {
			return fmt.Errorf("页面里出现了%s；交互页面必须自包含，不访问宿主页面也不联网", rule.reason)
		}
	}
	return nil
}

// scrubNamespaceURIs 摘掉允许出现的命名空间地址。
func scrubNamespaceURIs(document string) string {
	for _, uri := range htmlNamespaceURIs {
		document = strings.ReplaceAll(document, uri, "")
	}
	return document
}

// validateHTMLQuality 拦掉「静态卡片冒充交互页面」这类不合格产出。
func validateHTMLQuality(document string) error {
	if !htmlInteractivePattern.MatchString(document) {
		return fmt.Errorf("页面里没有可操作元素（button、input、select、textarea 或 canvas 至少一个）")
	}
	if !htmlScriptPattern.MatchString(document) {
		return fmt.Errorf("页面里没有脚本，静态 HTML 产生不了交互结果")
	}
	if !htmlEventPattern.MatchString(document) {
		return fmt.Errorf("脚本里没有为元素绑定事件（addEventListener 或 on* 属性）")
	}
	return nil
}

// htmlFeedback 把交互页的校验失败整理成给模型的修正提示。
func htmlFeedback(cause error) string {
	return fmt.Sprintf("上一次生成的交互页面校验失败：%s\n请修正并重新输出一份完整、自包含的 HTML 文档。不要解释，不要使用 Markdown 代码围栏。", cause.Error())
}
