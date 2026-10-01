package documentparser

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// 源码/数据文件必须能直读：不能因为文档解析能力没启用（python 为 nil）而被拒，
// 也不能被交给只会 docx/pdf 的 Python 解析器。切分入口再按内容类型选切法
// （见 internal/rag/classify.go），那是读取之后的另一件事。
func TestParserForReadsCodeFilesAsPlainText(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "upload.go")
	content := "package main\n\nfunc main() {}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入测试文件失败: %v", err)
	}

	parser, err := ParserFor(path, nil)
	if err != nil {
		t.Fatalf("代码文件应当有可用的解析器: %v", err)
	}
	if _, ok := parser.(*PlainTextParser); !ok {
		t.Fatalf("代码文件应当走直读解析器，实际 %T", parser)
	}

	result, err := parser.Parse(context.Background(), Request{Path: path})
	if err != nil {
		t.Fatalf("读取代码文件失败: %v", err)
	}
	if result.Markdown != content {
		t.Fatalf("直读内容被改动了:\n%q", result.Markdown)
	}
}

// 需要真正解析能力的格式在 python 缺失时仍要明确失败，不能被这次改动顺带放行。
func TestParserForStillRejectsUnsupportedWithoutPython(t *testing.T) {
	if _, err := ParserFor("upload.pdf", nil); err == nil {
		t.Fatal("没有 Python 解析器时 PDF 应当明确报错")
	}
}
