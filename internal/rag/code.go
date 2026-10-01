package rag

// code.go 是代码切分：按代码自身的结构出片，而不是按句子/字符硬切。
//
// 与 chunker.go 的分工：chunker.go 处理"文档"（Markdown 标题 + 段落/句子装填），
// 本文件处理"代码"（Go AST、JSON 结构、其他语言的结构兜底）。两者共用同一套
// 预算装填（chunker.go 的 pack / mergeTail / hardSplit），差别有两点：
// 片段从哪儿切出来（文档按句子，代码按声明/语句/JSON 键），以及代码不补重叠
// （重叠为散文设计，对结构切分只会添乱，见 packCodePieces）。
//
// 优先级与 classify.go 的判定链一致：
//   - Go：go/parser 真解析，按 package/import 头、type、func/method、var/const 单元出片；
//     单元超预算才往函数体的语句/嵌套块、结构体的字段里钻，最后才按行硬切；
//   - JSON：按 key/数组元素递归，整片超预算继续往里钻，标量值才交给行长兜底；
//   - 其他语言：没有解析器，按空行 + 括号深度（或 Python 式缩进）取块。
//     这不是 AST，但足以避开"按 . 或 ; 切句"这种最伤代码的切法。
//
// 切片的 symbol / symbol_type 只在能明确判断时写：合并了多个单元的片留空，
// 乱标一个符号比不标更容易误导检索。

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strings"
	"unicode/utf8"
)

// maxCodeSplitDepth 是代码降级切分的递归深度上限。
//
// 函数体切到语句、语句再切到嵌套块，两级通常就够了；上限只防病态嵌套把栈拉深，
// 超过就退回按行硬切 —— 结果仍然可用，只是粒度粗一点。
const maxCodeSplitDepth = 3

// maxJSONDepth 是 JSON 递归的深度上限，理由同上。
const maxJSONDepth = 8

// SplitCode 把一份代码/数据内容切成切片，供 classify.go 判定为 KindCode 时调用。
//
// language 为空表示"是代码但认不出语言"，走通用兜底。返回的切片序号从 0 连续，
// 已带上 content_type/language/symbol/symbol_type；空白内容一律丢弃。
func SplitCode(content string, language string, options ChunkOptions) []Chunk {
	options = options.withDefaults()
	content = normalize(content)
	if strings.TrimSpace(content) == "" {
		return nil
	}

	var pieces []piece
	switch language {
	case "go":
		pieces = goPieces(content, options)
	case "json":
		pieces = jsonPieces(content, options)
	case "jsonl":
		pieces = jsonLinePieces(content, options)
	default:
		pieces = genericCodePieces(content, language, options)
	}

	packed := packCodePieces(pieces, options)
	return finalizeCodeChunks(packed, language)
}

// packCodePieces 复用文档链路的预算装填，但**不加重叠前缀**。
//
// pack 会给相邻片补"句子对齐"的重叠，那是为散文设计的：散文的切点是句子边界，
// 复制上一句能帮下一片接住跨边界的语义。代码的切点本来就是结构边界（函数、语句），
// 重叠只会在下一片开头重复上一片结尾 —— 实测常常只是一个孤零零的 "}"，
// 既没有检索价值，还破坏了"一片一个结构单元"的边界。所以代码统一清掉种子。
func packCodePieces(pieces []piece, options ChunkOptions) []pendingChunk {
	if len(pieces) == 0 {
		return nil
	}
	packed := pack(pieces, options)
	for index := range packed {
		packed[index].seed = ""
	}
	return mergeTail(packed, options)
}

// finalizeCodeChunks 清理空片、统一编号并补上代码类型的公共元数据。
func finalizeCodeChunks(packed []pendingChunk, language string) []Chunk {
	chunks := make([]Chunk, 0, len(packed))
	for _, item := range packed {
		content := item.content()
		if strings.TrimSpace(content) == "" {
			continue
		}
		symbol, symbolType := chunkSymbol(item)
		chunks = append(chunks, Chunk{
			Index:       len(chunks),
			Content:     content,
			ContentType: string(KindCode),
			Language:    language,
			Symbol:      truncateHeading(symbol),
			SymbolType:  symbolType,
		})
	}
	return chunks
}

// chunkSymbol 从一个已装填的切片里取符号：所有非空片段必须指向同一个符号，
// 合并了多个单元的片一律留空 —— 一个片横跨两个函数时，标哪一个都是误导。
func chunkSymbol(chunk pendingChunk) (string, string) {
	symbol, symbolType := "", ""
	for _, part := range chunk.parts {
		if part.symbol == "" {
			continue
		}
		if symbol == "" {
			symbol, symbolType = part.symbol, part.symbolType
			continue
		}
		if symbol != part.symbol {
			return "", ""
		}
	}
	return symbol, symbolType
}

// ---------------------------------------------------------------------------
// Go：go/parser + go/ast
// ---------------------------------------------------------------------------

// goUnit 是一个顶层结构单元：package/import 头、一个声明，或声明之间的一段内容。
// text 覆盖原文的 [start:end)，end 取下一个单元的起点，保证所有字节恰好被覆盖一次，
// 声明之间的普通注释也会跟着前一个单元走。
type goUnit struct {
	text       string
	decl       ast.Decl
	start, end int
	symbol     string
	symbolType string
}

// goSplitter 持有一次 Go 切分需要的上下文。
//
// content 是要切分的原文；source 是实际喂给解析器的文本，片段场景下会多一行
// "package p" 前缀，base 记着这个前缀的长度，所有 offset 都要减去它才能映射回 content。
type goSplitter struct {
	content string
	source  string
	base    int
	fset    *token.FileSet
	limit   int
}

// goPieces 把 Go 源码切成不低于预算的片段。解析失败时退回通用兜底。
func goPieces(content string, options ChunkOptions) []piece {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "source.go", content, parser.ParseComments)

	base := 0
	if (err != nil || file == nil || len(file.Decls) == 0) && !strings.HasPrefix(strings.TrimSpace(content), "package ") {
		// 片段（例如用户只复制了一个函数）：垫一行 package 再解析，
		// 这样"文本里贴的单个函数"也能走 AST，而不是退回通用兜底。
		base = len("package p\n")
		fset = token.NewFileSet()
		file, err = parser.ParseFile(fset, "source.go", "package p\n"+content, parser.ParseComments)
	}
	if err != nil || file == nil || len(file.Decls) == 0 {
		return genericCodePieces(content, "go", options)
	}

	splitter := &goSplitter{
		content: content,
		fset:    fset,
		base:    base,
		limit:   options.contentLimit(),
	}
	units := splitter.units(file)

	pieces := make([]piece, 0, len(units))
	for _, unit := range units {
		text := strings.TrimSpace(unit.text)
		if text == "" {
			continue
		}
		if utf8.RuneCountInString(text) <= splitter.limit {
			pieces = append(pieces, piece{text: text, sep: "\n\n", symbol: unit.symbol, symbolType: unit.symbolType})
			continue
		}
		for _, part := range splitter.splitUnit(unit) {
			if strings.TrimSpace(part) == "" {
				continue
			}
			pieces = append(pieces, piece{text: part, sep: "\n\n", symbol: unit.symbol, symbolType: unit.symbolType})
		}
	}
	if len(pieces) > 0 {
		pieces[0].sep = ""
	}
	return pieces
}

// units 按源码顺序列出所有顶层单元，详见 goUnit 的说明。
func (s *goSplitter) units(file *ast.File) []goUnit {
	units := make([]goUnit, 0, len(file.Decls)+1)

	first := s.declStart(file.Decls[0])
	if first > 0 {
		if text := strings.TrimSpace(s.content[:first]); text != "" {
			units = append(units, goUnit{text: text})
		}
	}

	for index, decl := range file.Decls {
		start := s.declStart(decl)
		end := len(s.content)
		if index+1 < len(file.Decls) {
			end = s.declStart(file.Decls[index+1])
		}
		if start >= end {
			continue
		}
		symbol, symbolType := goSymbol(decl)
		units = append(units, goUnit{
			text:       s.content[start:end],
			decl:       decl,
			start:      start,
			end:        end,
			symbol:     symbol,
			symbolType: symbolType,
		})
	}
	return units
}

// declStart 取声明的起点：有文档注释就从注释算起，注释是函数的一部分。
func (s *goSplitter) declStart(decl ast.Decl) int {
	switch item := decl.(type) {
	case *ast.FuncDecl:
		if item.Doc != nil {
			return s.offset(item.Doc.Pos())
		}
	case *ast.GenDecl:
		if item.Doc != nil {
			return s.offset(item.Doc.Pos())
		}
	}
	return s.offset(decl.Pos())
}

// offset 把 token 位置换算成 content 里的字节偏移（片段场景要减去垫进去的前缀）。
func (s *goSplitter) offset(pos token.Pos) int {
	offset := s.fset.Position(pos).Offset - s.base
	if offset < 0 {
		return 0
	}
	if offset > len(s.content) {
		return len(s.content)
	}
	return offset
}

// lineStart 返回包含该偏移的那一行的起点，用来保住语句/字段的原始缩进。
func (s *goSplitter) lineStart(offset int) int {
	if offset <= 0 {
		return 0
	}
	if offset > len(s.content) {
		offset = len(s.content)
	}
	if index := strings.LastIndexByte(s.content[:offset], '\n'); index >= 0 {
		return index + 1
	}
	return 0
}

// sliceFromLine 从 start 所在行的行首切到 end，去掉首尾空白。
func (s *goSplitter) sliceFromLine(start, end token.Pos) string {
	startOffset := s.lineStart(s.offset(start))
	endOffset := s.offset(end)
	if startOffset >= endOffset {
		return ""
	}
	return strings.TrimSpace(s.content[startOffset:endOffset])
}

// raw 按字节区间取原文，越界一律夹到边界（片段场景里 offset 经过前缀修正，
// Lbrace+1 这类取法在文件末尾可能正好越界一字节）。
func (s *goSplitter) raw(start, end int) string {
	if start < 0 {
		start = 0
	}
	if end > len(s.content) {
		end = len(s.content)
	}
	if start >= end {
		return ""
	}
	return strings.TrimSpace(s.content[start:end])
}

// statementText 取一条语句的原文，从行首开始以保留缩进。
func (s *goSplitter) statementText(stmt ast.Stmt) string {
	return s.sliceFromLine(stmt.Pos(), stmt.End())
}

// splitUnit 把一个超预算的顶层单元继续拆小，见文件头部的优先级说明。
func (s *goSplitter) splitUnit(unit goUnit) []string {
	switch decl := unit.decl.(type) {
	case *ast.FuncDecl:
		if decl.Body != nil && len(decl.Body.List) > 0 {
			header := s.raw(unit.start, s.offset(decl.Body.Lbrace)+1)
			return packCodeParts(header, s.blockParts(decl.Body, 1), "}", s.limit)
		}
	case *ast.GenDecl:
		switch decl.Tok {
		case token.TYPE:
			if len(decl.Specs) == 1 {
				if spec, ok := decl.Specs[0].(*ast.TypeSpec); ok {
					if parts, header, tail, ok := s.typeParts(unit, spec); ok {
						return packCodeParts(header, parts, tail, s.limit)
					}
				}
			}
		case token.VAR, token.CONST, token.IMPORT:
			if parts, header, tail, ok := s.groupParts(unit, decl); ok {
				return packCodeParts(header, parts, tail, s.limit)
			}
		}
	}
	return hardSplit(strings.TrimSpace(unit.text), s.limit)
}

// blockParts 把块里的语句展开成片段；单条语句超预算时先尝试往它的嵌套块里钻，
// 钻不动了才按行硬切。
func (s *goSplitter) blockParts(body *ast.BlockStmt, depth int) []string {
	parts := make([]string, 0, len(body.List))
	for _, stmt := range body.List {
		text := s.statementText(stmt)
		if text == "" {
			continue
		}
		if utf8.RuneCountInString(text) <= s.limit {
			parts = append(parts, text)
			continue
		}
		if nested := s.splitStatement(stmt, depth+1); len(nested) > 0 {
			parts = append(parts, nested...)
			continue
		}
		parts = append(parts, hardSplit(text, s.limit)...)
	}
	return parts
}

// splitStatement 尝试把一条超预算的语句按它自己的块结构拆开。
//
// 只处理"块就是语句全部内容"的形态（if/for/switch 等且没有 else 分支）：
// 带 else 的语句前半和后半语义不同，拆开容易把 else 孤零零地甩在下一片，
// 宁可退回按行硬切。返回 nil 表示拆不了。
func (s *goSplitter) splitStatement(stmt ast.Stmt, depth int) []string {
	if depth > maxCodeSplitDepth {
		return nil
	}

	var body *ast.BlockStmt
	switch value := stmt.(type) {
	case *ast.BlockStmt:
		body = value
	case *ast.IfStmt:
		if value.Else != nil {
			return nil
		}
		body = value.Body
	case *ast.ForStmt:
		body = value.Body
	case *ast.RangeStmt:
		body = value.Body
	case *ast.SwitchStmt:
		body = value.Body
	case *ast.TypeSwitchStmt:
		body = value.Body
	case *ast.SelectStmt:
		body = value.Body
	default:
		return nil
	}
	if body == nil || len(body.List) == 0 {
		return nil
	}

	header := s.raw(s.lineStart(s.offset(stmt.Pos())), s.offset(body.Lbrace)+1)
	return packCodeParts(header, s.blockParts(body, depth), "}", s.limit)
}

// typeParts 展开 struct/interface 类型：按字段/方法出片，首片带类型头，末片带收尾括号。
func (s *goSplitter) typeParts(unit goUnit, spec *ast.TypeSpec) (parts []string, header, tail string, ok bool) {
	var fields *ast.FieldList
	switch value := spec.Type.(type) {
	case *ast.StructType:
		tail = "}"
		fields = value.Fields
	case *ast.InterfaceType:
		tail = "}"
		fields = value.Methods
	default:
		return nil, "", "", false
	}
	if fields == nil || fields.Opening == token.NoPos {
		return nil, "", "", false
	}

	header = s.raw(unit.start, s.offset(fields.Opening)+1)
	parts = make([]string, 0, len(fields.List))
	for _, field := range fields.List {
		text := s.sliceFromLine(field.Pos(), field.End())
		if text == "" {
			continue
		}
		if utf8.RuneCountInString(text) > s.limit {
			parts = append(parts, hardSplit(text, s.limit)...)
			continue
		}
		parts = append(parts, text)
	}
	if len(parts) == 0 {
		return nil, "", "", false
	}
	return parts, header, tail, true
}

// groupParts 展开 var/const/import 的分组声明：首片带 "const (" 这类头，末片带 ")"。
func (s *goSplitter) groupParts(unit goUnit, decl *ast.GenDecl) (parts []string, header, tail string, ok bool) {
	if len(decl.Specs) < 2 || decl.Lparen == token.NoPos {
		return nil, "", "", false
	}

	headerEnd := s.lineStart(s.offset(decl.Specs[0].Pos()))
	header = s.raw(unit.start, headerEnd)
	parts = make([]string, 0, len(decl.Specs))
	for _, spec := range decl.Specs {
		text := s.sliceFromLine(spec.Pos(), spec.End())
		if text == "" {
			continue
		}
		if utf8.RuneCountInString(text) > s.limit {
			parts = append(parts, hardSplit(text, s.limit)...)
			continue
		}
		parts = append(parts, text)
	}
	if len(parts) == 0 {
		return nil, "", "", false
	}
	return parts, header, ")", true
}

// packCodeParts 把 [first + parts + tail] 按预算装成若干片段。
//
// first 挂在第一片、tail 挂在最后一片；单个 part 已经保证不超过预算（调用方负责
// 拆到行级），这里只做就近合并。header 单独超预算（病态的巨型签名）时不做特殊处理：
// 让它超一点，比硬切签名安全。
func packCodeParts(first string, parts []string, tail string, limit int) []string {
	out := make([]string, 0, len(parts)+1)
	current := strings.TrimSpace(first)
	flush := func() {
		if strings.TrimSpace(current) != "" {
			out = append(out, current)
		}
		current = ""
	}

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if current != "" && utf8.RuneCountInString(current)+1+utf8.RuneCountInString(part) > limit {
			flush()
		}
		if current == "" {
			current = part
			continue
		}
		current += "\n" + part
	}

	if tail = strings.TrimSpace(tail); tail != "" {
		if current == "" {
			current = tail
		} else {
			current += "\n" + tail
		}
	}
	flush()
	return out
}

// goSymbol 取声明对应的符号名与类型，取不到（分组声明、import）时返回空串。
func goSymbol(decl ast.Decl) (string, string) {
	switch item := decl.(type) {
	case *ast.FuncDecl:
		name := item.Name.Name
		if item.Recv != nil && len(item.Recv.List) > 0 {
			if receiver := receiverName(item.Recv.List[0].Type); receiver != "" {
				name = receiver + "." + name
			}
			return name, "method"
		}
		return name, "function"
	case *ast.GenDecl:
		if item.Tok == token.IMPORT {
			return "", "import"
		}
		if len(item.Specs) != 1 {
			return "", ""
		}
		switch spec := item.Specs[0].(type) {
		case *ast.TypeSpec:
			kind := "type"
			switch spec.Type.(type) {
			case *ast.StructType:
				kind = "struct"
			case *ast.InterfaceType:
				kind = "interface"
			}
			return spec.Name.Name, kind
		case *ast.ValueSpec:
			if len(spec.Names) == 0 {
				return "", ""
			}
			if item.Tok == token.CONST {
				return spec.Names[0].Name, "constant"
			}
			return spec.Names[0].Name, "variable"
		}
	}
	return "", ""
}

// receiverName 取方法接收者的类型名，剥掉指针与泛型参数（*UserService[T] -> UserService）。
func receiverName(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.StarExpr:
		return receiverName(value.X)
	case *ast.IndexExpr:
		return receiverName(value.X)
	case *ast.IndexListExpr:
		return receiverName(value.X)
	default:
		return ""
	}
}

// ---------------------------------------------------------------------------
// JSON / JSONL
// ---------------------------------------------------------------------------

// jsonPieces 按 key/数组元素递归切 JSON，解析不动时退回通用兜底。
func jsonPieces(content string, options ChunkOptions) []piece {
	pieces, ok := collectJSONPieces(strings.TrimSpace(content), "", options.contentLimit(), 0)
	if !ok || len(pieces) == 0 {
		return genericCodePieces(content, "json", options)
	}
	return pieces
}

// collectJSONPieces 用 token 流遍历一层 JSON 值，把每个成员/元素收成一个片段。
//
// 对象取 "key": <原样值>（json.RawMessage 保留原始格式与 key 顺序），数组按元素下标
// 组织；片段超预算就带着路径继续往值里递归，标量或递归过深时交给硬切兜底。
// symbol 记的是 JSON 路径（server.port、documents[3].title），供检索侧定位。
func collectJSONPieces(value, path string, limit, depth int) ([]piece, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, false
	}
	if depth > maxJSONDepth {
		return scalarJSONPieces(value, path, limit), true
	}

	decoder := json.NewDecoder(strings.NewReader(value))
	token, err := decoder.Token()
	if err != nil {
		return nil, false
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil, false
	}

	var pieces []piece
	switch delimiter {
	case '{':
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, false
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, false
			}
			var raw json.RawMessage
			if err := decoder.Decode(&raw); err != nil {
				return nil, false
			}
			piecePath := joinJSONPath(path, key)
			memberText := `"` + key + `": ` + strings.TrimSpace(string(raw))
			if utf8.RuneCountInString(memberText) <= limit {
				pieces = append(pieces, piece{text: memberText, sep: "\n", symbol: piecePath, symbolType: "json"})
				continue
			}
			if nested, ok := collectJSONPieces(string(raw), piecePath, limit, depth+1); ok && len(nested) > 0 {
				pieces = append(pieces, nested...)
				continue
			}
			pieces = append(pieces, scalarJSONPieces(string(raw), piecePath, limit)...)
		}
	case '[':
		index := 0
		for decoder.More() {
			var raw json.RawMessage
			if err := decoder.Decode(&raw); err != nil {
				return nil, false
			}
			piecePath := fmt.Sprintf("%s[%d]", path, index)
			elementText := strings.TrimSpace(string(raw))
			if utf8.RuneCountInString(elementText) <= limit {
				pieces = append(pieces, piece{text: elementText, sep: "\n", symbol: piecePath, symbolType: "json"})
			} else if nested, ok := collectJSONPieces(elementText, piecePath, limit, depth+1); ok && len(nested) > 0 {
				pieces = append(pieces, nested...)
			} else {
				pieces = append(pieces, scalarJSONPieces(elementText, piecePath, limit)...)
			}
			index++
		}
	default:
		return nil, false
	}

	if len(pieces) == 0 {
		return nil, false
	}
	return pieces, true
}

// scalarJSONPieces 把无法再结构化的 JSON 值按行/字符硬切，路径原样挂在每一片上。
func scalarJSONPieces(value, path string, limit int) []piece {
	text := strings.TrimSpace(value)
	if text == "" {
		return nil
	}
	parts := hardSplit(text, limit)
	pieces := make([]piece, 0, len(parts))
	for _, part := range parts {
		pieces = append(pieces, piece{text: part, sep: "\n", symbol: path, symbolType: "json"})
	}
	return pieces
}

// jsonLinePieces 处理 JSONL：一行一条，天然是切片边界。
func jsonLinePieces(content string, options ChunkOptions) []piece {
	limit := options.contentLimit()
	var pieces []piece
	for _, line := range strings.Split(content, "\n") {
		text := strings.TrimSpace(line)
		if text == "" {
			continue
		}
		if utf8.RuneCountInString(text) <= limit {
			pieces = append(pieces, piece{text: text, sep: "\n"})
			continue
		}
		for _, part := range hardSplit(text, limit) {
			pieces = append(pieces, piece{text: part, sep: "\n"})
		}
	}
	return pieces
}

// joinJSONPath 拼接 JSON 路径。
func joinJSONPath(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

// ---------------------------------------------------------------------------
// 其他语言：没有解析器时的结构兜底
// ---------------------------------------------------------------------------

// genericCodeUnit 是通用兜底切分的一个结构块。
type genericCodeUnit struct {
	text       string
	symbol     string
	symbolType string
}

// genericCodePieces 把非 Go/JSON 的代码切成片段。
//
// 按语言选分块方式：Python/YAML 这类靠缩进的语言按顶格行分块；
// 其余按空行分块，但只在大括号深度归零处断 —— 函数体内的空行不算边界。
// 分块只求"别切进块内部"，符号名能认出来就带上，认不出留空。
func genericCodePieces(content, language string, options ChunkOptions) []piece {
	limit := options.contentLimit()
	units := genericCodeUnits(content, language)
	pieces := make([]piece, 0, len(units))
	for _, unit := range units {
		text := strings.TrimSpace(unit.text)
		if text == "" {
			continue
		}
		if utf8.RuneCountInString(text) <= limit {
			pieces = append(pieces, piece{text: text, sep: "\n\n", symbol: unit.symbol, symbolType: unit.symbolType})
			continue
		}
		for _, part := range splitGenericUnit(text, limit) {
			pieces = append(pieces, piece{text: part, sep: "\n\n", symbol: unit.symbol, symbolType: unit.symbolType})
		}
	}
	return pieces
}

// genericCodeUnits 按语言的结构习惯把内容切成块，见 genericCodePieces 的说明。
func genericCodeUnits(content, language string) []genericCodeUnit {
	lines := strings.Split(content, "\n")
	var units []genericCodeUnit

	if usesIndentation(language) {
		var current []string
		flush := func() {
			if len(current) > 0 {
				units = append(units, newGenericCodeUnit(current))
				current = nil
			}
		}
		for _, line := range lines {
			if strings.TrimSpace(line) == "" {
				// 空行收进当前块：等下一行决定它是块内间隔还是块间间隔。
				current = append(current, line)
				continue
			}
			indented := line != strings.TrimLeft(line, " \t")
			if !indented && len(current) > 0 && !onlyCommentLines(current) {
				flush()
			}
			current = append(current, line)
		}
		flush()
		return units
	}

	scanner := &braceScanner{}
	var current []string
	depth := 0
	flush := func() {
		if len(current) > 0 {
			units = append(units, newGenericCodeUnit(current))
			current = nil
		}
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" && depth == 0 && len(current) > 0 {
			flush()
			continue
		}
		current = append(current, line)
		depth += scanner.delta(line, language)
		if depth < 0 {
			depth = 0
		}
	}
	flush()
	return units
}

// newGenericCodeUnit 收尾一个块：拼回文本并尽力提取符号。
func newGenericCodeUnit(lines []string) genericCodeUnit {
	text := strings.TrimSpace(strings.Join(lines, "\n"))
	symbol, symbolType := genericSymbol(text)
	return genericCodeUnit{text: text, symbol: symbol, symbolType: symbolType}
}

// onlyCommentLines 判断一个块是不是"只有注释和空行"。
//
// 用于缩进语言：紧贴在 def/class 前面的注释块应该跟着声明走，而不是被顶格
// 判定提前切成独立单元。
func onlyCommentLines(lines []string) bool {
	found := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		found = true
		if !(strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") ||
			strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*")) {
			return false
		}
	}
	return found
}

// usesIndentation 判断语言是否按缩进组织结构。
func usesIndentation(language string) bool {
	switch language {
	case "python", "yaml":
		return true
	default:
		return false
	}
}

// braceScanner 是通用的括号深度扫描器：数 {} 时跳过字符串与注释。
//
// 它不是词法器，只服务"空行该不该当作块边界"这个判断。跨行块注释用一个字段
// 记状态；跨行字符串（Go 反引号、JS 模板串）不追踪，行末复位 —— 那种情况下
// 深度可能偏一档，代价是少切一次或多切一次，不会丢内容。
type braceScanner struct {
	inBlock  bool
	inString byte
	escaped  bool
}

// delta 返回一行给括号深度带来的净变化。
func (s *braceScanner) delta(line, language string) int {
	delta := 0
	for index := 0; index < len(line); index++ {
		char := line[index]

		if s.inBlock {
			if char == '*' && index+1 < len(line) && line[index+1] == '/' {
				s.inBlock = false
				index++
			}
			continue
		}
		if s.inString != 0 {
			if s.escaped {
				s.escaped = false
				continue
			}
			switch char {
			case '\\':
				s.escaped = true
			case s.inString:
				s.inString = 0
			}
			continue
		}

		switch char {
		case '"', '\'', '`':
			s.inString = char
		case '/':
			if index+1 < len(line) {
				switch line[index+1] {
				case '/':
					return delta
				case '*':
					s.inBlock = true
					index++
				}
			}
		case '#':
			if hashStartsComment(language) {
				return delta
			}
		case '{':
			delta++
		case '}':
			delta--
		}
	}
	s.inString = 0
	return delta
}

// hashStartsComment 判断 # 在这些语言里是不是行注释。
//
// C/C# 的 # 是预处理指令、不是注释，不能截断整行；其余脚本语言按注释处理。
func hashStartsComment(language string) bool {
	switch language {
	case "shell", "powershell", "batch", "ruby", "perl", "r", "yaml", "python":
		return true
	default:
		return false
	}
}

// splitGenericUnit 把超预算的通用代码块再拆小：优先按块内最小缩进的行开新片
// （相当于往里走一层），拆不动就按行硬切。
func splitGenericUnit(text string, limit int) []string {
	lines := strings.Split(text, "\n")
	if len(lines) < 2 {
		return hardSplit(text, limit)
	}

	base := minLineIndent(lines[1:])
	if base < 0 {
		return hardSplit(text, limit)
	}

	var parts []string
	current := lines[0]
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			current += "\n" + line
			continue
		}
		if indentWidth(line) == base {
			if strings.TrimSpace(current) != "" {
				parts = append(parts, current)
			}
			current = line
			continue
		}
		current += "\n" + line
	}
	if strings.TrimSpace(current) != "" {
		parts = append(parts, current)
	}
	if len(parts) < 2 {
		return hardSplit(text, limit)
	}

	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if utf8.RuneCountInString(part) <= limit {
			out = append(out, part)
			continue
		}
		out = append(out, hardSplit(part, limit)...)
	}
	if len(out) == 0 {
		return hardSplit(text, limit)
	}
	return out
}

// minLineIndent 返回非空行的最小缩进；没有可用的行时返回 -1。
func minLineIndent(lines []string) int {
	minimum := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		width := indentWidth(line)
		if minimum < 0 || width < minimum {
			minimum = width
		}
	}
	return minimum
}

// indentWidth 数一行的缩进宽度，制表符按 4 列算。
func indentWidth(line string) int {
	width := 0
	for _, symbol := range line {
		switch symbol {
		case ' ':
			width++
		case '\t':
			width += 4
		default:
			return width
		}
	}
	return width
}

// genericSymbolPatterns 是通用符号提取的候选，按"越具体越靠前"排列。
var genericSymbolPatterns = []struct {
	pattern    *regexp.Regexp
	symbolType string
}{
	{regexp.MustCompile(`(?m)^[ \t]*(?:public|private|protected|internal|static|final|abstract|sealed|partial|export|default|async)[ \t]+(?:class|interface|enum)[ \t]+(\w+)`), "class"},
	{regexp.MustCompile(`(?m)^[ \t]*(?:class|interface|enum)[ \t]+(\w+)`), "class"},
	{regexp.MustCompile(`(?m)^[ \t]*(?:export[ \t]+)?(?:default[ \t]+)?(?:async[ \t]+)?function[ \t]+(\w+)`), "function"},
	{regexp.MustCompile(`(?m)^[ \t]*(?:async[ \t]+)?def[ \t]+(\w+)`), "function"},
	{regexp.MustCompile(`(?m)^[ \t]*(?:pub[ \t]+)?(?:async[ \t]+)?fn[ \t]+(\w+)`), "function"},
	{regexp.MustCompile(`(?m)^[ \t]*func[ \t]+(?:\([^)]*\)[ \t]*)?(\w+)`), "function"},
	{regexp.MustCompile(`(?m)^[ \t]*(?:public[ \t]+)?interface[ \t]+(\w+)`), "interface"},
	{regexp.MustCompile(`(?m)^[ \t]*(?:pub[ \t]+)?struct[ \t]+(\w+)`), "struct"},
	{regexp.MustCompile(`(?m)^[ \t]*(?:const|let|var)[ \t]+(\w+)[ \t]*=`), "variable"},
}

// genericSymbol 尽力从块头提取符号名，提不到返回空串。
func genericSymbol(text string) (string, string) {
	for _, candidate := range genericSymbolPatterns {
		if match := candidate.pattern.FindStringSubmatch(text); match != nil {
			return match[1], candidate.symbolType
		}
	}
	return "", ""
}
