package rag

// classify.go 是切分入口的"文档类型判定"：先看这堆内容是什么，再决定用哪把刀。
//
// 它回答的问题不是"用哪个解析器把文件读进来"（那是 documentparser.ParserFor 的事），
// 而是"内容已经是文本了，该按文档切、按普通文本切，还是按代码结构切"。
// 判定只依赖内容本身和可选的旁证（来源路径/文件名），不读文件、不查库、不发网络请求 ——
// 这样它才能在收录链路的三条恢复路径上给出同一个答案：上传、编辑器录入、从库里重切。
//
// 置信度是分层的，宁可漏判不可错判：
//  1. 扩展名能明确说清的，直接采信（.md 是文档，.go/.py/.json 是代码）；
//  2. Go 源码用 go/parser 真解析验证，JSON 用 json.Valid 真验证 —— 这两条几乎不会认错；
//  3. 其他语言看内容特征，要求多个锚点同时成立才认；
//  4. 都拿不准时按普通文本处理，由现有的段落/句子切分兜底。
//
// 为什么要保守：误判的代价不对称。代码被当文章切，最多是不够好；文章被当代码切，
// 长中文段落没有换行可依，行级硬切比句子切分更糟。所以只有"整篇明确就是代码"
// 才走代码切分，不在同一篇内容里逐段挑代码。

import (
	"encoding/json"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"
)

// DocumentKind 是判定结论里的文档类型，也是落进 knowledge_chunks.content_type 的取值。
type DocumentKind string

const (
	// KindDocument 是有明确结构的文档：Markdown、以及 PDF/Office 转换出来的产物。
	KindDocument DocumentKind = "document"
	// KindPlainText 是没有标题结构的普通文本，走与文档相同的段落/句子装填。
	KindPlainText DocumentKind = "plain_text"
	// KindCode 是源代码或代码样数据（JSON/JSONL 等），走代码切分。
	KindCode DocumentKind = "code"
)

// DocumentHint 是内容之外的旁证，用来先按扩展名拍板。
//
// Path 是磁盘上的暂存路径（worker 侧是 upload.go 这类保留后缀的名字），
// SourceURI 是用户看到的原始文件名。两者都可能为空（编辑器直接录入的内容没有文件），
// 为空时完全依赖内容判定。
type DocumentHint struct {
	Path      string
	SourceURI string
}

// Classification 是一次判定的结论。
//
// Reason 只用于测试与排障，不落库：它说明这次是按扩展名、Go 解析、JSON 校验还是
// 特征打分判出来的，排查"为什么这段内容没被当代码"时先看它。
type Classification struct {
	Kind     DocumentKind
	Language string
	Reason   string
}

// documentExtensions 是明确按文档处理的后缀。只有这两种格式"内容即产物"，
// 不需要也不应该被代码判定截胡。
var documentExtensions = map[string]bool{
	".md":       true,
	".markdown": true,
}

// codeLanguageByExtension 是扩展名到语言的映射。
//
// 只放"本质上是文本"的代码与数据格式：它们可以被 PlainTextParser 直接读成文本，
// 不需要 Python 解析器。扩展名命中时直接判代码，不再看内容 —— 用户把文件叫 .go，
// 我们就按 Go 处理；即使内容不完整，代码兜底切分也比按句子切安全。
var codeLanguageByExtension = map[string]string{
	".go":     "go",
	".py":     "python",
	".pyw":    "python",
	".js":     "javascript",
	".mjs":    "javascript",
	".cjs":    "javascript",
	".jsx":    "javascript",
	".ts":     "typescript",
	".tsx":    "typescript",
	".java":   "java",
	".kt":     "kotlin",
	".kts":    "kotlin",
	".cs":     "csharp",
	".c":      "c",
	".h":      "c",
	".cc":     "cpp",
	".cpp":    "cpp",
	".cxx":    "cpp",
	".hpp":    "cpp",
	".rs":     "rust",
	".rb":     "ruby",
	".php":    "php",
	".swift":  "swift",
	".scala":  "scala",
	".lua":    "lua",
	".pl":     "perl",
	".pm":     "perl",
	".r":      "r",
	".sh":     "shell",
	".bash":   "shell",
	".zsh":    "shell",
	".ps1":    "powershell",
	".bat":    "batch",
	".cmd":    "batch",
	".sql":    "sql",
	".json":   "json",
	".jsonl":  "jsonl",
	".ndjson": "jsonl",
	".yaml":   "yaml",
	".yml":    "yaml",
	".toml":   "toml",
	".xml":    "xml",
	".html":   "html",
	".htm":    "html",
	".css":    "css",
	".scss":   "scss",
	".less":   "less",
	".vue":    "vue",
	".svelte": "svelte",
	".dart":   "dart",
}

// ClassifyDocument 判定一份内容该走哪种切分。
func ClassifyDocument(content string, hint DocumentHint) Classification {
	content = normalize(content)

	if extension := hintExtension(hint); extension != "" {
		if documentExtensions[extension] {
			return Classification{Kind: KindDocument, Reason: "extension:" + extension}
		}
		if language, ok := codeLanguageByExtension[extension]; ok {
			return Classification{Kind: KindCode, Language: language, Reason: "extension:" + extension}
		}
	}

	if detectGo(content) {
		return Classification{Kind: KindCode, Language: "go", Reason: "go-parser"}
	}
	if language, ok := detectJSON(content); ok {
		return Classification{Kind: KindCode, Language: language, Reason: "json-valid"}
	}
	// 围栏要排在语言签名之前：Markdown 里的代码块是"文档的一部分"，
	// 整篇仍然是文档；而 Go 原始字符串也用反引号，先跑 Go 解析器能盖住那种边角。
	if hasMarkdownFence(content) {
		return Classification{Kind: KindDocument, Reason: "markdown-fence"}
	}
	if language, ok := detectLanguageSignatures(content); ok {
		return Classification{Kind: KindCode, Language: language, Reason: "signature"}
	}
	if genericCodeScore(content) >= genericCodeScoreThreshold {
		return Classification{Kind: KindCode, Reason: "score"}
	}
	if hasATXHeading(content) {
		return Classification{Kind: KindDocument, Reason: "heading"}
	}
	return Classification{Kind: KindPlainText, Reason: "default"}
}

// hintExtension 从旁证里取一个小写扩展名，Path 优先于 SourceURI（前者更贴近实际内容）。
func hintExtension(hint DocumentHint) string {
	for _, name := range []string{hint.Path, hint.SourceURI} {
		if extension := strings.ToLower(filepath.Ext(strings.TrimSpace(name))); extension != "" {
			return extension
		}
	}
	return ""
}

// hasGoDeclLine 是 go/parser 之前的廉价闸门：没有一行像 Go 声明就根本不必解析。
//
// 要求关键字出现在行首（允许缩进）：散文里偶尔提到 func 不会触发，
// 而真正的 Go 片段至少有一行以 func/type/var/const/package 开头。
func hasGoDeclLine(content string) bool {
	prefixes := [...]string{
		"package ", "import ", "import(",
		"func ", "func(", "type ", "type(",
		"var ", "var(", "const ", "const(",
	}
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		for _, prefix := range prefixes {
			if strings.HasPrefix(trimmed, prefix) {
				return true
			}
		}
	}
	return false
}

// detectGo 用标准库解析器验证整篇内容是不是 Go。
//
// 两条路：先按完整文件解析（必须有 package 子句）；失败后再试"片段"——
// 很多用户只复制一个函数，没有 package 行，垫一行 package p 就能让解析器验收。
// 解析成功且至少有一个声明才算数，中文注释、全角标点都不会干扰语法解析。
func detectGo(content string) bool {
	if !hasGoDeclLine(content) {
		return false
	}

	if parsed, err := parser.ParseFile(token.NewFileSet(), "detect.go", content, parser.SkipObjectResolution); err == nil &&
		parsed != nil && parsed.Name != nil && len(parsed.Decls) > 0 {
		return true
	}

	wrapped := "package p\n" + content
	parsed, err := parser.ParseFile(token.NewFileSet(), "detect.go", wrapped, parser.SkipObjectResolution)
	return err == nil && parsed != nil && len(parsed.Decls) > 0
}

// detectJSON 验证整篇内容是不是合法 JSON（或逐行合法的 JSONL）。
//
// json.Valid 是精确校验：整个内容必须恰好是一个 JSON 值，散文不可能通过。
func detectJSON(content string) (string, bool) {
	trimmed := strings.TrimSpace(content)
	if len(trimmed) < 2 {
		return "", false
	}
	if trimmed[0] == '{' || trimmed[0] == '[' {
		if json.Valid([]byte(trimmed)) {
			return "json", true
		}
	}
	if looksLikeJSONLines(trimmed) {
		return "jsonl", true
	}
	return "", false
}

// looksLikeJSONLines 判断内容是不是"一行一个 JSON 对象/数组"的 JSONL。
//
// 要求每行都以 { 或 [ 开头并整体合法：纯数字/字符串的逐行列表虽然也是合法 JSON，
// 但那更像普通文本（编号列表、词表），不抢。
func looksLikeJSONLines(content string) bool {
	lines := 0
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if (trimmed[0] != '{' && trimmed[0] != '[') || !json.Valid([]byte(trimmed)) {
			return false
		}
		lines++
	}
	return lines >= 2
}

// hasMarkdownFence 判断内容里有没有围栏代码块。
func hasMarkdownFence(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			return true
		}
	}
	return false
}

// hasATXHeading 判断内容里有没有 Markdown 风格的一级到六级标题。
//
// 它排在所有代码判定之后：Python/Shell 的 # 注释同样以 # 开头，只有确认不是代码了，
// 这个信号才用来把 .txt 里的 Markdown 笔记判成文档。
func hasATXHeading(content string) bool {
	return atxHeadingPattern.MatchString(content)
}

var atxHeadingPattern = regexp.MustCompile(`(?m)^#{1,6}(?:[ \t]|$)`)

// detectLanguageSignatures 对其他常见语言做多锚点判定，凑不齐就返回 false。
//
// 每个语言要求至少两个互相独立的锚点：单个特征（比如一行 def）在散文或配置里
// 也可能出现，两个同时出现就基本没跑了。顺序从特征最强、最不容易混淆的语言开始。
func detectLanguageSignatures(content string) (string, bool) {
	if detectPython(content) {
		return "python", true
	}
	if detectJavaScript(content) {
		return "javascript", true
	}
	if language, ok := detectJavaFamily(content); ok {
		return language, true
	}
	if detectRust(content) {
		return "rust", true
	}
	if detectShell(content) {
		return "shell", true
	}
	if detectSQL(content) {
		return "sql", true
	}
	return "", false
}

var (
	pythonDefLine      = regexp.MustCompile(`(?m)^[ \t]*(?:def|class)[ \t]+\w+.*:`)
	pythonImportLine   = regexp.MustCompile(`(?m)^[ \t]*(?:import|from)[ \t]+\w`)
	pythonIndentedLine = regexp.MustCompile(`(?m)^(?: {2,}|\t)\S`)
	pythonColonLine    = regexp.MustCompile(`(?m)^[ \t]*\S.*:[ \t]*$`)
	pythonMainGuard    = regexp.MustCompile(`(?m)^[ \t]*(?:if[ \t]+__name__|@\w+|print\()`)
)

// detectPython：def/class 是强锚点，另外再凑一个 import、缩进块或主入口守卫。
func detectPython(content string) bool {
	if !pythonDefLine.MatchString(content) {
		return false
	}
	score := 1
	if pythonImportLine.MatchString(content) {
		score++
	}
	if pythonIndentedLine.MatchString(content) && pythonColonLine.MatchString(content) {
		score++
	}
	if pythonMainGuard.MatchString(content) {
		score++
	}
	return score >= 2
}

var (
	jsFunctionLine = regexp.MustCompile(`(?m)^[ \t]*(?:export[ \t]+)?(?:default[ \t]+)?(?:async[ \t]+)?function[ \t]+\w+[ \t]*\(`)
	jsArrowAssign  = regexp.MustCompile(`(?m)^[ \t]*(?:const|let|var)[ \t]+\w+[ \t]*=[ \t]*(?:async[ \t]*)?\(?[^;\n]*\)?[ \t]*=>`)
	jsModuleLine   = regexp.MustCompile(`(?m)^[ \t]*(?:import\b|export\b)`)
	jsVariableLine = regexp.MustCompile(`(?m)^[ \t]*(?:const|let|var)[ \t]+\w+[ \t]*=`)
	jsConsoleCall  = regexp.MustCompile(`console\.log\(`)
)

// detectJavaScript：函数声明或箭头赋值是强锚点，再凑一个模块/变量/分号行。
// 只看箭头赋值要求与 const/let 同行，避免把 Rust 的 match 分支误当成 JS 箭头函数。
func detectJavaScript(content string) bool {
	strong := jsFunctionLine.MatchString(content) || jsArrowAssign.MatchString(content)
	if !strong {
		return false
	}
	score := 1
	switch {
	case jsModuleLine.MatchString(content):
		score++
	case jsVariableLine.MatchString(content):
		score++
	case jsConsoleCall.MatchString(content):
		score++
	case semicolonLineRatio(content) >= 0.2:
		score++
	}
	return score >= 2
}

var (
	javaTypeLine     = regexp.MustCompile(`(?m)^[ \t]*(?:(?:public|private|protected|internal|static|final|abstract|sealed|partial)[ \t]+)*(?:class|interface|enum)[ \t]+\w+`)
	javaAccessLine   = regexp.MustCompile(`\b(?:public|private|protected|internal)[ \t]`)
	javaPackageLine  = regexp.MustCompile(`(?m)^[ \t]*(?:package[ \t]+[\w.]+[ \t]*;|using[ \t]+[\w.]+[ \t]*;)`)
	javaPrintCall    = regexp.MustCompile(`(?:System\.out\.println|Console\.WriteLine)`)
	javaAnnotation   = regexp.MustCompile(`(?m)^[ \t]*@\w+`)
	javaCSharpMarker = regexp.MustCompile(`(?m)(?:^[ \t]*using[ \t]+[\w.]+[ \t]*;|Console\.WriteLine)`)
)

// detectJavaFamily：class/interface/enum 声明是强锚点，再凑一个访问修饰符、
// package/using 行、控制台输出或注解。能认出 C# 时返回 csharp，否则按 java。
func detectJavaFamily(content string) (string, bool) {
	if !javaTypeLine.MatchString(content) {
		return "", false
	}
	score := 1
	switch {
	case javaAccessLine.MatchString(content):
		score++
	case javaPackageLine.MatchString(content):
		score++
	case javaPrintCall.MatchString(content):
		score++
	case javaAnnotation.MatchString(content):
		score++
	}
	if score < 2 {
		return "", false
	}
	if javaCSharpMarker.MatchString(content) {
		return "csharp", true
	}
	return "java", true
}

var (
	rustFunctionLine = regexp.MustCompile(`(?m)^[ \t]*(?:pub[ \t]+)?(?:async[ \t]+)?fn[ \t]+\w+`)
	rustLetLine      = regexp.MustCompile(`\blet[ \t]+(?:mut[ \t]+)?\w+`)
	rustDeclLine     = regexp.MustCompile(`(?m)^[ \t]*(?:pub[ \t]+)?(?:impl|trait|struct|enum|mod|use)\b`)
)

// detectRust：fn 是强锚点，再凑一个 let 或 impl/struct/use 声明。
func detectRust(content string) bool {
	if !rustFunctionLine.MatchString(content) {
		return false
	}
	return rustLetLine.MatchString(content) || rustDeclLine.MatchString(content)
}

var (
	shellShebang  = regexp.MustCompile(`^#![^\n]*\b(?:sh|bash|zsh|fish)\b`)
	shellBlockEnd = regexp.MustCompile(`(?m)^[ \t]*(?:fi|done|esac)\b`)
	shellSubst    = regexp.MustCompile(`\$\(|\$\{`)
	shellAssign   = regexp.MustCompile(`(?m)^[A-Za-z_][A-Za-z0-9_]*=`)
	shellPipe     = regexp.MustCompile(`\|[ \t]*(?:grep|awk|sed|cut|sort|uniq|xargs)\b`)
)

// detectShell：shebang、块结束词、命令替换、环境变量赋值、管道命令，凑两个才算。
func detectShell(content string) bool {
	score := 0
	if shellShebang.MatchString(content) {
		score++
	}
	if shellBlockEnd.MatchString(content) {
		score++
	}
	if shellSubst.MatchString(content) {
		score++
	}
	if shellAssign.MatchString(content) {
		score++
	}
	if shellPipe.MatchString(content) {
		score++
	}
	return score >= 2
}

var (
	sqlSelect = regexp.MustCompile(`(?is)\bselect\b[^;]{0,200}?\bfrom\b`)
	sqlClause = regexp.MustCompile(`(?i)\b(?:where|group[ \t]+by|order[ \t]+by|having|join)\b`)
	sqlDML    = regexp.MustCompile(`(?i)\b(?:insert[ \t]+into|update[ \t]+\w+[ \t]+set|delete[ \t]+from|create[ \t]+table|alter[ \t]+table|drop[ \t]+table)\b`)
)

// detectSQL：select...from 是强锚点，再凑一个子句或 DML。
func detectSQL(content string) bool {
	if !sqlSelect.MatchString(content) {
		return false
	}
	return sqlClause.MatchString(content) || sqlDML.MatchString(content)
}

// genericCodeScoreThreshold 是通用打分的通过线。
//
// 打分只服务"整篇明显是代码但没匹配上任何签名"的兜底（比如 C/C++）。
// 阈值取高：宁可漏判成普通文本，也不要把一篇排得工整的散文错认成代码。
const genericCodeScoreThreshold = 4

// genericCodeKeywords 是通用打分认的行首关键字（不区分大小写）。
var genericCodeKeywords = []string{
	"func", "def", "class", "interface", "struct", "enum", "typedef",
	"import", "from", "using", "namespace", "package", "module",
	"public", "private", "protected", "static", "final", "abstract",
	"void", "int", "char", "float", "double", "bool", "string", "long", "short", "unsigned",
	"var", "let", "const", "fn", "impl", "trait", "type", "map", "range",
	"return", "if", "else", "for", "while", "switch", "case", "break", "continue",
	"try", "catch", "throw", "new", "delete", "sizeof", "printf", "cout", "endl",
	"include", "export", "async", "await", "lambda", "go", "defer", "select",
}

// genericCodeScore 用若干结构信号给"代码样"打分，见 genericCodeScoreThreshold。
//
// 中文句读是负向信号：正常的中文散文每段都会出现句号/问号，而代码里的中文基本
// 只在注释里，整篇不会以句读结尾成段。英文散文没有这个信号，靠关键字比例与
// 括号/分号比例区分。
func genericCodeScore(content string) int {
	total, keyword, brace, indent, comment, semicolon := 0, 0, 0, 0, 0, 0
	cjkPunctuation := 0
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		total++
		firstWord := trimmed
		if index := strings.IndexAny(firstWord, " \t("); index >= 0 {
			firstWord = firstWord[:index]
		}
		lowered := strings.ToLower(strings.TrimLeft(firstWord, "#"))
		for _, candidate := range genericCodeKeywords {
			if lowered == candidate {
				keyword++
				break
			}
		}
		if strings.HasSuffix(trimmed, "{") || trimmed == "}" || strings.HasPrefix(trimmed, "}") {
			brace++
		}
		if line != strings.TrimLeft(line, " \t") {
			indent++
		}
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") ||
			strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
			comment++
		}
		if strings.HasSuffix(trimmed, ";") {
			semicolon++
		}
		cjkPunctuation += strings.Count(line, "。") + strings.Count(line, "！") +
			strings.Count(line, "？") + strings.Count(line, "；")
	}
	if total == 0 {
		return 0
	}

	score := 0
	if ratio(keyword, total) >= 0.2 {
		score += 2
	}
	if ratio(brace, total) >= 0.2 {
		score++
	}
	if ratio(indent, total) >= 0.3 {
		score++
	}
	if ratio(comment, total) >= 0.1 {
		score++
	}
	if ratio(semicolon, total) >= 0.2 {
		score++
	}
	if cjkPunctuation >= 3 {
		score -= 2
	}
	return score
}

// ratio 返回 count/total 的浮点比例，total 为 0 时按 0 处理。
func ratio(count, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(count) / float64(total)
}

// semicolonLineRatio 统计"以分号结尾的行"占比，供 JS 判定用。
func semicolonLineRatio(content string) float64 {
	total, semicolon := 0, 0
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		total++
		if strings.HasSuffix(trimmed, ";") {
			semicolon++
		}
	}
	return ratio(semicolon, total)
}
