package rag

import "testing"

// classify_test.go 盯着切分入口的"文档类型判定"：
// 判定只依赖内容与可选的文件名旁证（见 classify.go），
// 宁可漏判成普通文本，也不能把文档错判成代码。

const classifyGoFixture = `package main

import "fmt"

func main() {
	fmt.Println("hello")
}

func login() {
}

func logout() {
}
`

// 完整的 Go 文件（有无 .txt 后缀都一样）应当判成 code/go。
// 你贴进编辑器的那段 Reranked 代码就是这一档：中文注释、全角标点都不影响解析器。
func TestClassifyGoSource(t *testing.T) {
	hints := map[string]DocumentHint{
		"没有旁证":         {},
		"txt 后缀":       {SourceURI: "notes.txt"},
		"无关的扩展名":       {Path: "upload.bin"},
		"只有 SourceURI": {SourceURI: "snippet.txt"},
	}
	for name, hint := range hints {
		classification := ClassifyDocument(classifyGoFixture, hint)
		if classification.Kind != KindCode || classification.Language != "go" {
			t.Errorf("%s：应判成 code/go，实际 %+v", name, classification)
		}
	}
}

// 只贴一个函数（没有 package 子句）也要能认出来：垫一行 package 交给解析器验收。
func TestClassifyGoFragment(t *testing.T) {
	fragment := `// login 处理登录。中文注释不影响解析。
func login(name string) error {
	return nil
}
`
	classification := ClassifyDocument(fragment, DocumentHint{SourceURI: "snippet.txt"})
	if classification.Kind != KindCode || classification.Language != "go" {
		t.Fatalf("Go 片段应判成 code/go，实际 %+v", classification)
	}
}

// JSON/JSONL 由 json.Valid 精确校验，散文不可能通过。
func TestClassifyJSONAndJSONL(t *testing.T) {
	for name, content := range map[string]string{
		"对象": `{"server": {"host": "0.0.0.0", "port": 8080}}`,
		"数组": `[{"id": 1}, {"id": 2}]`,
		"多行": "{\n  \"a\": 1,\n  \"b\": [1, 2, 3]\n}",
	} {
		classification := ClassifyDocument(content, DocumentHint{SourceURI: "data.txt"})
		if classification.Kind != KindCode || classification.Language != "json" {
			t.Errorf("%s：应判成 code/json，实际 %+v", name, classification)
		}
	}

	jsonl := "{\"id\": 1, \"name\": \"甲\"}\n{\"id\": 2, \"name\": \"乙\"}\n"
	classification := ClassifyDocument(jsonl, DocumentHint{SourceURI: "log.txt"})
	if classification.Kind != KindCode || classification.Language != "jsonl" {
		t.Fatalf("JSONL 应判成 code/jsonl，实际 %+v", classification)
	}
}

// .txt 里的 Python 也要认出来：def/class 加 import 或缩进块两个锚点。
func TestClassifyPythonInPlainText(t *testing.T) {
	python := `import os

# 读取配置
def load(path):
    with open(path) as handle:
        return handle.read()

class UserService:
    def create(self):
        return True
`
	classification := ClassifyDocument(python, DocumentHint{SourceURI: "notes.txt"})
	if classification.Kind != KindCode || classification.Language != "python" {
		t.Fatalf("Python 内容应判成 code/python，实际 %+v", classification)
	}
}

// 带围栏的说明文档不能因为里面有 Go 代码块就被当成代码：整篇仍是文档，
// 围栏里的代码块由 Markdown 切分器的"原子块"逻辑保护。
func TestClassifyMarkdownFenceStaysDocument(t *testing.T) {
	markdown := "# Go 并发\n\n说明文字。\n\n```go\nfunc main() {}\n```\n"
	if classification := ClassifyDocument(markdown, DocumentHint{SourceURI: "guide.md"}); classification.Kind != KindDocument {
		t.Fatalf(".md 应判成文档，实际 %+v", classification)
	}

	// .txt 里的围栏文档同样不能被 Go 解析抢走，理由字段要说清是围栏判的。
	notes := "说明文字。\n\n```go\nfunc main() {}\n```\n"
	classification := ClassifyDocument(notes, DocumentHint{SourceURI: "notes.txt"})
	if classification.Kind != KindDocument || classification.Reason != "markdown-fence" {
		t.Fatalf(".txt 里的围栏文档应判成 document（markdown-fence），实际 %+v", classification)
	}
}

// 提到代码词汇的中文散文必须保持普通文本：误判成代码的代价比漏判大。
func TestClassifyProseWithCodeWordsStaysPlainText(t *testing.T) {
	prose := "在 Go 里，func main 是程序入口。\n\n这段话解释为什么代码不能按句子切。\n"
	classification := ClassifyDocument(prose, DocumentHint{SourceURI: "notes.txt"})
	if classification.Kind != KindPlainText {
		t.Fatalf("散文应判成 plain_text，实际 %+v", classification)
	}
}

// Go 片段嵌在中文说明里（既不是完整文件、也不是干净片段）不能误判。
func TestClassifyProsePlusCodeSnippetStaysText(t *testing.T) {
	mixed := "下面是一个例子：\n\nfunc login() {}\n\n这只是笔记，不是源码文件。\n"
	classification := ClassifyDocument(mixed, DocumentHint{SourceURI: "notes.txt"})
	if classification.Kind == KindCode {
		t.Fatalf("说明文字里夹一行 func 不该判成代码，实际 %+v", classification)
	}
}

// 扩展名明确时按扩展名拍板：文件叫 .go，就按 Go 处理。
func TestClassifyExtensionWinsOverContent(t *testing.T) {
	classification := ClassifyDocument("这是一段散文，不是 Go。\n", DocumentHint{SourceURI: "readme.go"})
	if classification.Kind != KindCode || classification.Language != "go" {
		t.Fatalf(".go 后缀应判成 code/go，实际 %+v", classification)
	}
}

// 认不出来的内容一律普通文本，绝不能抛错或乱猜。
func TestClassifyUnknownDefaultsToPlainText(t *testing.T) {
	classification := ClassifyDocument("一段平平无奇的中文。\n\n没有任何结构。\n", DocumentHint{SourceURI: "笔记.dat"})
	if classification.Kind != KindPlainText {
		t.Fatalf("未知内容应判成 plain_text，实际 %+v", classification)
	}
}
