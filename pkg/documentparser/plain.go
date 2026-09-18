package documentparser

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// plainParserName 是本实现自报的解析器身份，与 PythonParser 的 pymupdf / docling 并列。
const plainParserName = "plain"

// plainTextMaxBytes 单个纯文本文件的大小上限。
//
// 纯文本是逐字读进内存的，没有流式处理的价值（几百兆的 txt 本来也不该当知识库文档）；
// 设上限是为了让"传错了文件"变成一句明确的报错，而不是把内存吃满。
const plainTextMaxBytes = 32 << 20

// PlainTextParser 直接读取已经是纯文本的文档（.md / .markdown / .txt / .text）。
//
// 它存在的理由是 PythonParser 的一条硬边界：python/parse_document.py 只处理
// docx / pptx / xlsx / pdf 和图片，其它后缀一律抛 PARSER_UNSUPPORTED_TYPE。
// 而知识库导入最常见的格式恰恰是 Markdown 与纯文本 —— 为了这两种格式去起一个
// Python 子进程（首次运行还要下载解释器和 docling，分钟级）既慢又没必要：
// 它们的正文本来就是解析的目标产物，读出来即可，没有"解析"这一步可做。
type PlainTextParser struct{}

var _ Parser = (*PlainTextParser)(nil)

// plainTextExtensions 是本实现负责的后缀（全小写，含点）。
var plainTextExtensions = []string{".md", ".markdown", ".txt", ".text"}

// NewPlainTextParser 创建纯文本解析器。它无状态，可以长期复用。
func NewPlainTextParser() Parser { return &PlainTextParser{} }

// PlainTextExtensions 返回本实现负责的后缀。
func PlainTextExtensions() []string {
	out := make([]string, len(plainTextExtensions))
	copy(out, plainTextExtensions)
	return out
}

// IsPlainTextPath 判断路径是否可以直接按纯文本读取。
func IsPlainTextPath(path string) bool {
	extension := strings.ToLower(filepath.Ext(path))
	for _, candidate := range plainTextExtensions {
		if extension == candidate {
			return true
		}
	}
	return false
}

// ParserFor 按文件后缀挑一个能读它的解析器：纯文本格式走 PlainTextParser，
// 其余交给 python（通常是 PythonParser，也可能为 nil —— 表示文档解析能力没启用）。
//
// 把"哪些格式不用真解析"这个判断放在解析器包而不是调用方，是因为它属于解析器的知识：
// 调用方只该说"给我一个能读这个文件的解析器"，不该自己维护一份格式清单，
// 否则将来支持新格式时，漏改的一定是调用方那一份。
func ParserFor(path string, python Parser) (Parser, error) {
	if IsPlainTextPath(path) {
		return &PlainTextParser{}, nil
	}
	if python == nil {
		return nil, &Error{
			Code: CodeRuntimeUnavailable,
			Message: fmt.Sprintf("没有能解析 %s 的解析器：这类文件需要文档解析能力，"+
				"请在 config.yaml 里打开 document_parser.enabled", filepath.Base(path)),
		}
	}
	return python, nil
}

func (p *PlainTextParser) Status(context.Context) Status {
	return Status{Ready: true, Source: plainParserName, Reason: "纯文本格式直接读取，不依赖 Python 运行时"}
}

// Parse 读出文件内容，原样作为 Markdown 返回。
//
// 不做任何结构改写：正文、标题、代码块都由上游（编辑器或导出工具）决定，
// 这里多动一步都可能把用户精心排好的 Markdown 改坏。只统一换行符 ——
// CRLF 留在正文里，落到数据库就是看不见但处处硌人的脏字符。
func (p *PlainTextParser) Parse(ctx context.Context, req Request) (*Result, error) {
	path := strings.TrimSpace(req.Path)
	if path == "" {
		return nil, &Error{Code: CodeFileNotFound, Message: "待解析文件路径为空"}
	}
	if !isFile(path) {
		return nil, &Error{Code: CodeFileNotFound, Message: "待解析文件不存在: " + path}
	}
	if err := ctx.Err(); err != nil {
		return nil, &Error{Code: CodeTimeout, Message: "解析已取消或超时: " + err.Error()}
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, &Error{Code: CodeFileNotFound, Message: "读取文件信息失败: " + err.Error()}
	}
	if info.Size() > plainTextMaxBytes {
		return nil, &Error{
			Code:    CodeFailed,
			Message: fmt.Sprintf("纯文本文件超过 %d MB 上限: %s", plainTextMaxBytes>>20, filepath.Base(path)),
		}
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, &Error{Code: CodeFailed, Message: "读取文件失败: " + err.Error()}
	}

	markdown, err := decodeText(raw, path)
	if err != nil {
		return nil, err
	}
	markdown = strings.ReplaceAll(strings.ReplaceAll(markdown, "\r\n", "\n"), "\r", "\n")

	if strings.TrimSpace(markdown) == "" {
		// 空文件不算"解析成功"：knowledge_documents 的 CHECK 要求 ready 的文档正文非空，
		// 让它在入库前就失败，比写进一个永远检索不到的空文档清楚得多。
		return nil, &Error{
			Code:    CodeFailed,
			Message: "文件没有可入库的正文: " + filepath.Base(path),
		}
	}

	return &Result{
		Markdown: markdown,
		Metadata: map[string]any{
			"parser":    plainParserName,
			"file_type": strings.ToLower(filepath.Ext(path)),
		},
	}, nil
}

// decodeText 把文件字节解成字符串。
//
// 编码处理不是可有可无的：Windows 记事本默认把 .txt 存成 UTF-16LE（带 BOM），
// 直接按 UTF-8 读会得到一串夹着 NUL 字节的乱码 —— 它能存进库里、能切分、
// 也能向量化，只是检索永远召回不到，属于最难排查的一类问题。
func decodeText(raw []byte, path string) (string, error) {
	switch {
	case bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}):
		return string(raw[3:]), nil // UTF-8 BOM
	case bytes.HasPrefix(raw, []byte{0xFF, 0xFE}):
		return decodeUTF16(raw[2:], binary.LittleEndian), nil
	case bytes.HasPrefix(raw, []byte{0xFE, 0xFF}):
		return decodeUTF16(raw[2:], binary.BigEndian), nil
	}

	if !utf8.Valid(raw) {
		return "", &Error{
			Code: CodeEncodingInvalid,
			Message: fmt.Sprintf("文件不是 UTF-8 或 UTF-16 编码的文本: %s"+
				"（GBK 等本地编码请先另存为 UTF-8）", filepath.Base(path)),
		}
	}
	return string(raw), nil
}

// decodeUTF16 解码 UTF-16 字节序列，代理对由 utf16.Decode 处理。
func decodeUTF16(raw []byte, order binary.ByteOrder) string {
	if len(raw)%2 != 0 {
		// 奇数长度说明文件被截断了，丢掉最后一个不完整的字节即可，
		// 不值得为此让整份文档失败。
		raw = raw[:len(raw)-1]
	}
	units := make([]uint16, len(raw)/2)
	for index := range units {
		units[index] = order.Uint16(raw[index*2:])
	}
	return string(utf16.Decode(units))
}
