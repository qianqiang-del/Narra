package documentparser

import (
	"strings"
	"testing"
)

// 这一组用例守的是"给用户看的那句话"：Error.Error() 拼的是诊断串（错误码 + stderr），
// 只能进日志；界面上显示的必须是 UserMessage()。两者混用时的症状很具体 ——
// 界面上出现 "PARSER_FAILED: 文档解析失败 (stderr: parser failed: No module named 'scipy')"。

func TestUserMessageDropsCodeAndStderr(t *testing.T) {
	err := &Error{
		Code:    CodeFailed,
		Message: "文档解析失败",
		Stderr:  "parser failed: No module named 'scipy'",
	}

	message := err.UserMessage()
	if message != "文档解析失败：解析环境缺少 Python 模块 scipy" {
		t.Fatalf("用户文案 = %q", message)
	}
	for _, banned := range []string{"PARSER_", "stderr"} {
		if strings.Contains(message, banned) {
			t.Errorf("用户文案里不该出现 %q，实际 %q", banned, message)
		}
	}
}

// TestUserMessageKeepsMessageWhenStderrIsNoise stderr 里认不出可行动的原因时，
// 只回 Message —— 宁可少说，也不把 traceback 原样贴到界面上。
func TestUserMessageKeepsMessageWhenStderrIsNoise(t *testing.T) {
	err := &Error{
		Code:    CodeFailed,
		Message: "解析器没有输出可识别的 JSON 结果",
		Stderr:  "Traceback (most recent call last):\n  File \"parse.py\", line 1\nValueError: boom",
	}

	message := err.UserMessage()
	if message != "解析器没有输出可识别的 JSON 结果" {
		t.Fatalf("用户文案 = %q", message)
	}
	if strings.Contains(message, "Traceback") {
		t.Errorf("用户文案里不该带上 stderr 原文，实际 %q", message)
	}
}

// TestUserMessageFallsBackToCode 每条错误本来都带 Message，这条兜底是防"界面拿到空串
// 就没法解释这一行为什么是红的"。
func TestUserMessageFallsBackToCode(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{CodeTimeout, "文档解析超时"},
		{CodeRuntimeUnavailable, "文档解析环境不可用"},
		{CodeUnsupportedType, "不支持这种文件类型"},
		{"SOMETHING_NEW", "文档解析失败"},
	}
	for _, item := range cases {
		got := (&Error{Code: item.code}).UserMessage()
		if got != item.want {
			t.Errorf("错误码 %s 的兜底文案 = %q，期望 %q", item.code, got, item.want)
		}
	}
}

func TestUserMessageOnNilError(t *testing.T) {
	var err *Error
	if message := err.UserMessage(); message != "" {
		t.Errorf("nil 错误应当给出空串，实际 %q", message)
	}
}

func TestMissingModuleReadsModuleName(t *testing.T) {
	cases := []struct {
		name   string
		stderr string
		want   string
	}{
		{"脚本自己的包装", "parser failed: No module named 'scipy'", "scipy"},
		{"Python 的原始报错", "ModuleNotFoundError: No module named 'docling'", "docling"},
		{"带包路径", "No module named 'docling.datamodel'", "docling.datamodel"},
		{"裸模块名加句号", "ImportError: No module named scipy.", "scipy"},
		{"前后有换行与缩进", "\n    No module named 'fitz'   \n", "fitz"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if got := missingModule(item.stderr); got != item.want {
				t.Errorf("模块名 = %q，期望 %q", got, item.want)
			}
		})
	}
}

// TestMissingModuleIgnoresUnrelatedStderr 认不出来必须返回空串 —— 返回半截垃圾
// 会直接变成界面上那句原因的后半段。
func TestMissingModuleIgnoresUnrelatedStderr(t *testing.T) {
	cases := []string{
		"",
		"parser failed: exit status 2",
		// marker 在但后面没有模块名（截断的日志）
		"No module named ",
		"No module named '",
	}
	for _, stderr := range cases {
		if got := missingModule(stderr); got != "" {
			t.Errorf("stderr %q 不该摘出模块名，实际 %q", stderr, got)
		}
	}
}
