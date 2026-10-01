package rag

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// code_test.go 盯着代码切分的两条底线：
//  1. 结构单元（函数、类型、JSON 键）尽量完整，绝不从中间截断；
//  2. 超预算时按语句/块/行逐级降级，而不是按字符一剁了事。

const codeGoFixture = `package main

import "fmt"

func main() {
	fmt.Println("hello")
}

func login(name string) error {
	fmt.Println("login", name)
	return nil
}

func logout() {
	fmt.Println("logout")
}
`

// chunkWith 找出第一个包含指定文本的切片，断言用。
func chunkWith(chunks []Chunk, needle string) (Chunk, bool) {
	for _, chunk := range chunks {
		if strings.Contains(chunk.Content, needle) {
			return chunk, true
		}
	}
	return Chunk{}, false
}

// 小文件整篇足够小，合不合成一片无所谓；关键是每个函数体必须跟着函数头。
func TestSplitCodeKeepsGoFunctionsWhole(t *testing.T) {
	chunks := SplitCode(codeGoFixture, "go", ChunkOptions{})
	if len(chunks) == 0 {
		t.Fatal("Go 源码应当切出切片")
	}

	var joined strings.Builder
	for _, chunk := range chunks {
		joined.WriteString(chunk.Content)
		joined.WriteString("\n")
		if chunk.ContentType != string(KindCode) || chunk.Language != "go" {
			t.Errorf("第 %d 片缺少代码元数据: %+v", chunk.Index, chunk)
		}
	}
	text := joined.String()
	for _, name := range []string{"func main(", "func login(", "func logout("} {
		if count := strings.Count(text, name); count != 1 {
			t.Errorf("%q 出现 %d 次，说明函数被切成残片", name, count)
		}
	}

	login, ok := chunkWith(chunks, "func login(")
	if !ok {
		t.Fatal("没有切片包含 login 函数")
	}
	if !strings.Contains(login.Content, `fmt.Println("login", name)`) || !strings.Contains(login.Content, "return nil") {
		t.Errorf("login 的函数体没有跟着函数头: %q", login.Content)
	}
	logout, ok := chunkWith(chunks, "func logout(")
	if !ok || !strings.Contains(logout.Content, `fmt.Println("logout")`) {
		t.Errorf("logout 的函数体不完整: %+v", logout)
	}
}

// 超大函数按函数体语句降级：每一行要么是完整语句，要么是花括号，不允许半个语句。
func TestSplitCodeSplitsOversizedGoFunctionAtStatements(t *testing.T) {
	var builder strings.Builder
	builder.WriteString("package main\n\nfunc big() {\n")
	for index := 0; index < 300; index++ {
		fmt.Fprintf(&builder, "\tx%03d := %d\n", index, index)
	}
	builder.WriteString("}\n")

	chunks := SplitCode(builder.String(), "go", ChunkOptions{MaxChars: 300, MinChars: 50, Overlap: 1})
	if len(chunks) < 2 {
		t.Fatalf("300 行语句应当切成多片，实际 %d 片", len(chunks))
	}

	statement := regexp.MustCompile(`^x\d{3} := \d+$`)
	for _, chunk := range chunks {
		for _, line := range strings.Split(chunk.Content, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || trimmed == "package main" || trimmed == "func big() {" || trimmed == "}" {
				continue
			}
			if !statement.MatchString(trimmed) {
				t.Errorf("第 %d 片出现被截断的行: %q", chunk.Index, line)
			}
		}
	}
	if last := chunks[len(chunks)-1]; !strings.Contains(last.Content, "}") {
		t.Errorf("末片没有收尾花括号: %q", last.Content)
	}
}

// 方法/函数的 symbol 要跟着每一片走；合并了多个符号的片留空。
func TestSplitCodeAssignsGoSymbols(t *testing.T) {
	body := strings.Repeat("\tvalue = value + 1\n", 60)
	content := "package main\n\ntype UserService struct {\n\trepo string\n}\n\n" +
		"func (s *UserService) CreateUser(name string) error {\n" + body + "\treturn nil\n}\n"

	chunks := SplitCode(content, "go", ChunkOptions{MaxChars: 200, MinChars: 20, Overlap: 1})
	methodFound := false
	for _, chunk := range chunks {
		if chunk.Symbol == "UserService.CreateUser" {
			methodFound = true
			if chunk.SymbolType != "method" {
				t.Errorf("方法符号类型应当是 method，实际 %q", chunk.SymbolType)
			}
		}
	}
	if !methodFound {
		t.Fatalf("没有切片带上方法符号 UserService.CreateUser: %+v", chunks)
	}

	structFound := false
	for _, chunk := range chunks {
		if chunk.Symbol == "UserService" {
			structFound = true
			if chunk.SymbolType != "struct" {
				t.Errorf("类型符号类型应当是 struct，实际 %q", chunk.SymbolType)
			}
		}
	}
	if !structFound {
		t.Fatalf("没有切片带上类型符号 UserService: %+v", chunks)
	}
}

// 小的 JSON 对象整片保留，符号是顶层键路径。
func TestSplitCodeKeepsSmallJSONObject(t *testing.T) {
	content := `{"server": {"host": "0.0.0.0", "port": 8080}}`
	chunks := SplitCode(content, "json", ChunkOptions{})
	if len(chunks) != 1 {
		t.Fatalf("小对象应当只出一片，实际 %d 片", len(chunks))
	}
	chunk := chunks[0]
	if chunk.Symbol != "server" || chunk.SymbolType != "json" {
		t.Errorf("JSON 符号应当为 server/json，实际 %q/%q", chunk.Symbol, chunk.SymbolType)
	}
	if !strings.Contains(chunk.Content, `"host"`) || !strings.Contains(chunk.Content, `"port"`) {
		t.Errorf("JSON 内容不完整: %q", chunk.Content)
	}
}

// JSON 数组：每个元素必须是完整的对象，括号不允许在切片边界上失衡。
func TestSplitCodeKeepsJSONArrayElementsWhole(t *testing.T) {
	var builder strings.Builder
	builder.WriteString("[")
	for index := 0; index < 40; index++ {
		if index > 0 {
			builder.WriteString(",")
		}
		fmt.Fprintf(&builder, `{"id": %d, "name": "条目-%d", "note": "%s"}`,
			index, index, strings.Repeat("内容", 20))
	}
	builder.WriteString("]")

	chunks := SplitCode(builder.String(), "json", ChunkOptions{MaxChars: 400, MinChars: 20, Overlap: 1})
	if len(chunks) < 2 {
		t.Fatalf("40 个大元素应当切成多片，实际 %d 片", len(chunks))
	}
	for _, chunk := range chunks {
		if strings.Count(chunk.Content, "{") != strings.Count(chunk.Content, "}") {
			t.Errorf("第 %d 片的花括号不配对: %q", chunk.Index, chunk.Content)
		}
	}
	if _, ok := chunkWith(chunks, `"id": 7, "name": "条目-7"`); !ok {
		t.Error("元素 7 被切碎了：id 与 name 不在同一片")
	}
}

// 超预算的嵌套值要带着路径继续往里钻，而不是整体硬切。
func TestCollectJSONPiecesRecursesIntoOversizedValues(t *testing.T) {
	content := `{"alpha": {"one": "` + strings.Repeat("甲", 60) + `", "two": "` + strings.Repeat("乙", 60) + `"}}`
	pieces, ok := collectJSONPieces(content, "", 80, 0)
	if !ok || len(pieces) == 0 {
		t.Fatal("递归切分不应失败")
	}
	found := false
	for _, item := range pieces {
		if item.symbol == "alpha.one" || item.symbol == "alpha.two" {
			found = true
		}
	}
	if !found {
		t.Fatalf("递归后应当出现 alpha.one / alpha.two 路径符号: %+v", pieces)
	}
}

// 文本里贴的 Python：按缩进分块，def 与它的函数体不被拆散。
func TestSplitCodePythonByIndentation(t *testing.T) {
	content := "import os\n\n# 读取配置\ndef load(path):\n    with open(path) as handle:\n        return handle.read()\n\n" +
		"def save(path, data):\n    with open(path, \"w\") as handle:\n        handle.write(data)\n"

	chunks := SplitCode(content, "python", ChunkOptions{MaxChars: 150, MinChars: 10, Overlap: 1})
	load, ok := chunkWith(chunks, "def load(")
	if !ok || !strings.Contains(load.Content, "return handle.read()") {
		t.Fatalf("load 的函数体不完整: %+v", load)
	}
	save, ok := chunkWith(chunks, "def save(")
	if !ok || !strings.Contains(save.Content, "handle.write(data)") {
		t.Fatalf("save 的函数体不完整: %+v", save)
	}
}

// 切分路由：代码、文档、普通文本分别落到对应的切分器，并带上 content_type。
func TestSplitDocumentRoutesCodeAndText(t *testing.T) {
	ctx := context.Background()
	options := ChunkOptions{}

	codeChunks, err := splitDocument(ctx, codeGoFixture, DocumentHint{}, options)
	if err != nil {
		t.Fatalf("代码切分失败: %v", err)
	}
	for _, chunk := range codeChunks {
		if chunk.ContentType != string(KindCode) || chunk.Language != "go" {
			t.Errorf("代码切片的路由元数据不对: %+v", chunk)
		}
	}

	documentChunks, err := splitDocument(ctx, "# 标题\n\n正文段落。", DocumentHint{SourceURI: "a.md"}, options)
	if err != nil {
		t.Fatalf("文档切分失败: %v", err)
	}
	if len(documentChunks) == 0 || documentChunks[0].ContentType != string(KindDocument) || documentChunks[0].Heading != "标题" {
		t.Errorf("Markdown 应当仍按标题切分并标 document: %+v", documentChunks)
	}

	textChunks, err := splitDocument(ctx, "一段普通文本。\n\n另一段。", DocumentHint{SourceURI: "a.txt"}, options)
	if err != nil {
		t.Fatalf("文本切分失败: %v", err)
	}
	if len(textChunks) == 0 || textChunks[0].ContentType != string(KindPlainText) {
		t.Errorf("普通文本应当标 plain_text: %+v", textChunks)
	}
}

// .txt 里只贴了一个函数（没有 package 子句）：也要走 AST，而不是退回通用兜底。
func TestSplitDocumentSplitsGoFragmentFromText(t *testing.T) {
	fragment := "func login() {\n\tfmt.Println(\"login\")\n}\n\nfunc logout() {\n}\n"
	chunks, err := splitDocument(context.Background(), fragment, DocumentHint{SourceURI: "snippet.txt"}, ChunkOptions{})
	if err != nil {
		t.Fatalf("片段切分失败: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("片段应当切出切片")
	}
	for _, chunk := range chunks {
		if chunk.ContentType != string(KindCode) || chunk.Language != "go" {
			t.Errorf("片段应当按 code/go 处理: %+v", chunk)
		}
	}
	login, ok := chunkWith(chunks, "func login(")
	if !ok || !strings.Contains(login.Content, `fmt.Println("login")`) {
		t.Fatalf("login 的函数体不完整: %+v", login)
	}
}

// 拿本项目自己的 rerank.go 当真实样本：用户往编辑器里贴的就是这种文件
// （中文注释、方法接收者、泛型前的老写法都有），必须判成 code/go 并带出符号。
func TestSplitCodeRealProjectGoFile(t *testing.T) {
	source, err := os.ReadFile("rerank.go")
	if err != nil {
		t.Skipf("读取样例文件失败（跳过）: %v", err)
	}
	content := string(source)

	classification := ClassifyDocument(content, DocumentHint{SourceURI: "粘贴的代码.txt"})
	if classification.Kind != KindCode || classification.Language != "go" {
		t.Fatalf("整份 Go 文件应当判成 code/go，实际 %+v", classification)
	}

	// 预算调小一点，让小声明各自成片 —— 默认预算下相邻小单元会合并，
	// 合并片按设计不标符号（横跨多个符号时标哪个都是误导）。
	chunks := SplitCode(content, "go", ChunkOptions{MaxChars: 300, MinChars: 50, Overlap: 1})
	if len(chunks) < 5 {
		t.Fatalf("这个体量的文件应当切出多片，实际 %d 片", len(chunks))
	}

	// 代码片不带重叠前缀：任何一片都不该以孤立的收尾花括号开头。
	for _, chunk := range chunks {
		firstLine := strings.SplitN(chunk.Content, "\n", 2)[0]
		if strings.TrimSpace(firstLine) == "}" {
			t.Errorf("第 %d 片以孤立的 } 开头，说明重叠前缀把上一片的收尾复制过来了: %q",
				chunk.Index, firstLine)
		}
	}
	symbols := map[string]string{}
	for _, chunk := range chunks {
		if chunk.ContentType != string(KindCode) || chunk.Language != "go" {
			t.Errorf("第 %d 片缺少 code/go 元数据", chunk.Index)
		}
		if chunk.Symbol != "" {
			symbols[chunk.Symbol] = chunk.SymbolType
		}
	}
	// scoredHit 这类小声明常与相邻单元合成一片，按设计合并片不标符号；
	// 这里只断言拆分后仍能独立成片的大符号。
	for symbol, symbolType := range map[string]string{
		"Reranked.Retrieve": "method",
		"NewReranked":       "function",
		"truncateHits":      "function",
	} {
		if symbols[symbol] != symbolType {
			t.Errorf("符号 %q 的类型应当是 %q，实际 %q（已知符号：%v）",
				symbol, symbolType, symbols[symbol], symbols)
		}
	}
}

// 切片的类型元数据要能原样落库、原样读回，否则前端展示/检索过滤拿不到 symbol。
func TestChunkMetadataRoundTripsThroughEntities(t *testing.T) {
	chunks := []Chunk{
		{Index: 0, Content: "package main", ContentType: "code", Language: "go", Symbol: "main", SymbolType: "function"},
		{Index: 1, Content: "正文段落。", ContentType: "document"},
	}
	stored := buildStoredChunks(7, chunks)
	if stored[0].ContentType == nil || *stored[0].ContentType != "code" ||
		stored[0].Language == nil || *stored[0].Language != "go" ||
		stored[0].Symbol == nil || *stored[0].Symbol != "main" ||
		stored[0].SymbolType == nil || *stored[0].SymbolType != "function" {
		t.Fatalf("代码元数据没有写进实体: %+v", stored[0])
	}
	if stored[1].Language != nil || stored[1].Symbol != nil || stored[1].SymbolType != nil {
		t.Fatalf("文档切片不该有代码元数据: %+v", stored[1])
	}

	back := chunksFromEntities(stored)
	for index := range chunks {
		if back[index].ContentType != chunks[index].ContentType ||
			back[index].Language != chunks[index].Language ||
			back[index].Symbol != chunks[index].Symbol ||
			back[index].SymbolType != chunks[index].SymbolType {
			t.Errorf("第 %d 片元数据回读不一致: %+v -> %+v", index, chunks[index], back[index])
		}
	}
}
