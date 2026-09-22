package documentparser

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// stubRunner 记录被执行的命令；onRun 可以伪造副作用（例如"uv 把解释器建好了"）。
type stubRunner struct {
	calls [][]string
	onRun func(name string, args []string) error
}

func (s *stubRunner) Run(_ context.Context, _ string, _ []string, name string, args ...string) ([]byte, []byte, error) {
	s.calls = append(s.calls, append([]string{name}, args...))
	if s.onRun != nil {
		if err := s.onRun(name, args); err != nil {
			return nil, nil, err
		}
	}
	return nil, nil, nil
}

// allowImportProbe 把运行时自检换成任何 Python 都能通过的语句。
// 测试机不该被要求真的装好 docling。
func allowImportProbe(t *testing.T) {
	t.Helper()
	original := importProbe
	importProbe = "import sys"
	t.Cleanup(func() { importProbe = original })
}

// testPython 找一个真实可用的解释器；找不到就跳过，避免没有 Python 的环境整包报红。
func testPython(t *testing.T) string {
	t.Helper()
	if path := os.Getenv("NARRA_TEST_PYTHON"); path != "" {
		return path
	}
	for _, candidate := range []string{"python3", "python", "py"} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	t.Skip("未找到 Python 解释器，跳过子进程用例（可用 NARRA_TEST_PYTHON 指定）")
	return ""
}

// fakeScript 造出"脚本 + 依赖清单"，让 NewPythonParser 能通过配置校验。
func fakeScript(t *testing.T, body string) (scriptPath string, requirementsPath string) {
	t.Helper()
	dir := t.TempDir()
	scriptPath = filepath.Join(dir, scriptName)
	requirementsPath = filepath.Join(dir, requirementsName)
	if err := os.WriteFile(scriptPath, []byte(body), 0o644); err != nil {
		t.Fatalf("写入假脚本失败: %v", err)
	}
	if err := os.WriteFile(requirementsPath, []byte("docling-slim==2.127.0\n"), 0o644); err != nil {
		t.Fatalf("写入假依赖清单失败: %v", err)
	}
	return scriptPath, requirementsPath
}

// venvPythonPath 返回当前平台上 venv 解释器的路径，与 findPythonIn 的判断保持一致。
func venvPythonPath(dir string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(dir, "python.exe")
	}
	return filepath.Join(dir, "bin", "python3")
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入 %s 失败: %v", path, err)
	}
}

func TestParseStdoutSkipsNoiseAndTakesLastJSON(t *testing.T) {
	stdout := []byte("INFO docling: loading layout model\n" +
		"{\"markdown\":\"# 标题\",\"picture_paths\":[],\"pages\":[],\"metadata\":{\"parser\":\"docling\"}}\n" +
		"WARNING: trailing log line\n")

	result, ok := parseStdout(stdout)
	if !ok {
		t.Fatal("应当能从日志噪声里解析出结果")
	}
	if result.Markdown != "# 标题" {
		t.Fatalf("markdown 不对: %q", result.Markdown)
	}
	if result.ParserName() != "docling" {
		t.Fatalf("解析器身份不对: %q", result.ParserName())
	}
}

func TestParseStdoutRejectsPureNoise(t *testing.T) {
	result, ok := parseStdout([]byte("INFO: ready\nnot a json line\n"))
	if ok || result != nil {
		t.Fatalf("噪声不应被当成结果: %+v", result)
	}
}

func TestErrorFromOutputReadsErrorProtocol(t *testing.T) {
	stdout := []byte("{\"error_code\": \"PARSER_UNSUPPORTED_TYPE\", \"error_message\": \"不支持的文档类型: .md\"}\n")
	found := errorFromOutput(stdout, []byte("Traceback (most recent call last): ...\n"))
	if found == nil {
		t.Fatal("应当识别出错误协议")
	}
	if found.Code != CodeUnsupportedType {
		t.Fatalf("错误码不对: %q", found.Code)
	}
	if !strings.Contains(found.Message, ".md") {
		t.Fatalf("错误信息不对: %q", found.Message)
	}
}

func TestErrorFromOutputFallsBackToStderr(t *testing.T) {
	stderr := []byte("{\"error_code\": \"PARSER_OCR_CONFIG_MISSING\", \"error_message\": \"未配置 OCR API 服务地址\"}\n")
	if found := errorFromOutput(nil, stderr); found == nil || found.Code != CodeOCRConfigMissing {
		t.Fatalf("应当能从 stderr 里读出错误协议: %+v", found)
	}
}

func TestConfigWithDefaultsDerivesRequirementsFromScript(t *testing.T) {
	script, requirements := fakeScript(t, "print('noop')\n")

	cfg := Config{ScriptPath: script}.WithDefaults()
	if cfg.Requirements != requirements {
		t.Fatalf("依赖清单应默认取脚本同目录: %q", cfg.Requirements)
	}
	if cfg.OCREngine != OCREngineRapidOCR {
		t.Fatalf("OCR 引擎默认应为本地: %q", cfg.OCREngine)
	}
	if cfg.ParseTimeout != defaultParseTimeout {
		t.Fatalf("超时默认值不对: %s", cfg.ParseTimeout)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("补全后的配置应当合法: %v", err)
	}
}

func TestConfigValidateRejectsMissingScript(t *testing.T) {
	cfg := Config{ScriptPath: filepath.Join(t.TempDir(), "nope.py")}
	if err := cfg.Validate(); err == nil {
		t.Fatal("脚本不存在时应当报错")
	}
	if err := (Config{}).Validate(); err == nil {
		t.Fatal("脚本路径为空时应当报错")
	}
}

func TestConfigValidateRejectsIncompleteOCREngine(t *testing.T) {
	script, requirements := fakeScript(t, "print('noop')\n")
	cfg := Config{ScriptPath: script, Requirements: requirements, OCREngine: OCREngineAPI}
	if err := cfg.Validate(); err == nil {
		t.Fatal("选 API OCR 却没填地址与模型时应当报错")
	}
}

func TestPythonParserParsesScriptOutput(t *testing.T) {
	python := testPython(t)
	allowImportProbe(t)

	const body = `import json
print(json.dumps({
    "markdown": "# hello",
    "picture_paths": ["/tmp/a.png"],
    "pages": [{"number": 1, "text": "p1"}],
    "metadata": {"parser": "pymupdf", "fallback": False},
}, ensure_ascii=False))
`
	script, requirements := fakeScript(t, body)
	input := filepath.Join(t.TempDir(), "doc.pdf")
	writeFile(t, input, "x")

	parser, err := NewPythonParser(Config{PythonPath: python, ScriptPath: script, Requirements: requirements})
	if err != nil {
		t.Fatalf("构造解析器失败: %v", err)
	}

	result, err := parser.Parse(context.Background(), Request{Path: input})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if result.Markdown != "# hello" {
		t.Fatalf("markdown 不对: %q", result.Markdown)
	}
	if result.ParserName() != "pymupdf" {
		t.Fatalf("解析器身份不对: %q", result.ParserName())
	}
	if len(result.PicturePaths) != 1 || len(result.Pages) != 1 {
		t.Fatalf("图片或页数不对: %+v", result)
	}
	if result.WorkDir == "" {
		t.Fatal("未指定 WorkDir 时应自动创建临时目录并回填")
	}
	if _, err := os.Stat(result.WorkDir); err != nil {
		t.Fatalf("工作目录不存在: %v", err)
	}
	os.RemoveAll(result.WorkDir)
}

func TestPythonParserMapsErrorProtocolToCode(t *testing.T) {
	python := testPython(t)
	allowImportProbe(t)

	const body = `import json
print(json.dumps({"error_code": "PARSER_UNSUPPORTED_TYPE", "error_message": "不支持的文档类型: .md"}))
raise SystemExit(2)
`
	script, requirements := fakeScript(t, body)
	input := filepath.Join(t.TempDir(), "doc.md")
	writeFile(t, input, "# hi")

	parser, err := NewPythonParser(Config{PythonPath: python, ScriptPath: script, Requirements: requirements})
	if err != nil {
		t.Fatalf("构造解析器失败: %v", err)
	}

	_, err = parser.Parse(context.Background(), Request{Path: input})
	var parseErr *Error
	if !errors.As(err, &parseErr) {
		t.Fatalf("应当返回 *Error，实际: %v", err)
	}
	if parseErr.Code != CodeUnsupportedType {
		t.Fatalf("错误码不对: %q", parseErr.Code)
	}
}

func TestPythonParserReportsMissingFileWithoutSpawning(t *testing.T) {
	python := testPython(t)
	allowImportProbe(t)

	script, requirements := fakeScript(t, "print('never runs')\n")
	parser, err := NewPythonParser(Config{PythonPath: python, ScriptPath: script, Requirements: requirements})
	if err != nil {
		t.Fatalf("构造解析器失败: %v", err)
	}

	_, err = parser.Parse(context.Background(), Request{Path: filepath.Join(t.TempDir(), "nope.pdf")})
	var parseErr *Error
	if !errors.As(err, &parseErr) || parseErr.Code != CodeFileNotFound {
		t.Fatalf("应当返回文件不存在错误，实际: %v", err)
	}
}

func TestPythonParserTimesOutAndKillsProcess(t *testing.T) {
	python := testPython(t)
	allowImportProbe(t)

	script, requirements := fakeScript(t, "import time\ntime.sleep(30)\n")
	input := filepath.Join(t.TempDir(), "doc.pdf")
	writeFile(t, input, "x")

	parser, err := NewPythonParser(Config{
		PythonPath:   python,
		ScriptPath:   script,
		Requirements: requirements,
		ParseTimeout: 300 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("构造解析器失败: %v", err)
	}

	start := time.Now()
	_, err = parser.Parse(context.Background(), Request{Path: input})
	var parseErr *Error
	if !errors.As(err, &parseErr) || parseErr.Code != CodeTimeout {
		t.Fatalf("应当返回超时错误，实际: %v", err)
	}
	// 关键：超时必须真的把进程杀掉，不能等脚本自己跑完 30 秒
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("超时后没有及时结束进程，耗时 %s", elapsed)
	}
}

func TestResolverPrefersConfiguredPython(t *testing.T) {
	allowImportProbe(t)

	dir := t.TempDir()
	python := filepath.Join(dir, "python-custom")
	writeFile(t, python, "")

	runner := &stubRunner{}
	resolver := &Resolver{
		cfg:      Config{PythonPath: python}.WithDefaults(),
		runner:   runner,
		lookPath: func(string) (string, error) { return "", os.ErrNotExist },
	}

	resolved, err := resolver.Resolve(context.Background(), nil)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if resolved.Source != "configured" {
		t.Fatalf("应当优先使用配置的解释器，实际来源: %q", resolved.Source)
	}
	if len(runner.calls) != 1 || !strings.Contains(strings.Join(runner.calls[0], " "), "-c") {
		t.Fatalf("应当只跑一次自检，实际: %v", runner.calls)
	}
}

func TestResolverRejectsMissingConfiguredPython(t *testing.T) {
	allowImportProbe(t)

	resolver := &Resolver{
		cfg:      Config{PythonPath: filepath.Join(t.TempDir(), "nope")}.WithDefaults(),
		runner:   &stubRunner{},
		lookPath: func(string) (string, error) { return "", os.ErrNotExist },
	}

	_, err := resolver.Resolve(context.Background(), nil)
	var resolveErr *Error
	if !errors.As(err, &resolveErr) || resolveErr.Code != CodeRuntimeUnavailable {
		t.Fatalf("应当返回运行时不可用，实际: %v", err)
	}
}

func TestResolverProvisionsEnvironmentWithUV(t *testing.T) {
	allowImportProbe(t)

	dir := t.TempDir()
	requirements := filepath.Join(dir, requirementsName)
	content := []byte("docling-slim==2.127.0\n")
	if err := os.WriteFile(requirements, content, 0o644); err != nil {
		t.Fatalf("写入依赖清单失败: %v", err)
	}

	envDir := filepath.Join(dir, "env")
	uvPath := filepath.Join(dir, uvBinaryName())
	writeFile(t, uvPath, "")

	// 模拟 uv venv 把解释器建出来
	runner := &stubRunner{onRun: func(_ string, args []string) error {
		if len(args) == 0 || args[0] != "venv" {
			return nil
		}
		target := args[len(args)-1]
		python := venvPythonPath(target)
		if err := os.MkdirAll(filepath.Dir(python), 0o755); err != nil {
			return err
		}
		file, err := os.Create(python)
		if err != nil {
			return err
		}
		return file.Close()
	}}

	resolver := &Resolver{
		cfg: Config{
			Requirements: requirements,
			EnvDir:       envDir,
			UVPath:       uvPath,
		}.WithDefaults(),
		runner:   runner,
		lookPath: func(string) (string, error) { return "", os.ErrNotExist },
	}

	var progress []string
	resolved, err := resolver.Resolve(context.Background(), func(message string) {
		progress = append(progress, message)
	})
	if err != nil {
		t.Fatalf("准备环境失败: %v", err)
	}
	if resolved.Source != "provisioned" {
		t.Fatalf("来源应为 provisioned，实际: %q", resolved.Source)
	}

	joined := make([]string, 0, len(runner.calls))
	for _, call := range runner.calls {
		joined = append(joined, strings.Join(call, " "))
	}
	all := strings.Join(joined, "\n")
	// --clear 不能少：已存在的环境目录若被复用，就绪标记关于"按这份清单建的"承诺就成空话；
	// 而不给这个 flag 时 uv 会对已存在的目录直接报错，重建路径根本走不通（见下面那条用例）。
	for _, want := range []string{"python install", "venv --python 3.12 --clear", "pip install --python"} {
		if !strings.Contains(all, want) {
			t.Fatalf("缺少准备步骤 %q，实际命令:\n%s", want, all)
		}
	}
	if len(progress) == 0 {
		t.Fatal("应当上报准备进度")
	}

	marker, err := os.ReadFile(filepath.Join(envDir, markerFileName))
	if err != nil {
		t.Fatalf("应当写入就绪标记: %v", err)
	}
	if strings.TrimSpace(string(marker)) != requirementsDigest(content) {
		t.Fatal("就绪标记的内容应当是依赖清单摘要")
	}
}

// TestResolverRebuildsUnmarkedExistingEnv 复现"依赖清单改了、环境还在"的场景。
//
// 这是最容易出事的一条路径：目录里有旧环境、没有就绪标记（或标记与当前清单不符），
// 于是 Resolve 落到 provision。而 uv venv 面对已存在的目录会直接报错退出，除非给它
// --clear —— 少了这个 flag，自动重建永远失败，只剩"手工删目录"这一条路，而且报错信息
// 与"依赖装少了"毫无关系，很难追。这里让 stub 忠实模拟 uv 的行为来钉住它。
func TestResolverRebuildsUnmarkedExistingEnv(t *testing.T) {
	allowImportProbe(t)

	dir := t.TempDir()
	requirements := filepath.Join(dir, requirementsName)
	writeFile(t, requirements, "docling-slim==2.127.0\n")

	// 旧环境：解释器在，但没有就绪标记，等价于"这份环境是按上一版清单建的"。
	envDir := filepath.Join(dir, "env")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatalf("造旧环境目录失败: %v", err)
	}
	writeFile(t, venvPythonPath(envDir), "")

	uvPath := filepath.Join(dir, uvBinaryName())
	writeFile(t, uvPath, "")

	runner := &stubRunner{onRun: func(_ string, args []string) error {
		if len(args) == 0 || args[0] != "venv" {
			return nil
		}
		target := args[len(args)-1]
		cleared := false
		for _, arg := range args {
			if arg == "--clear" {
				cleared = true
			}
		}
		if _, err := os.Stat(target); err == nil && !cleared {
			// uv 的真实行为：目标已存在且没有 --clear 时报错。
			return errors.New("a virtual environment already exists")
		}
		python := venvPythonPath(target)
		if err := os.MkdirAll(filepath.Dir(python), 0o755); err != nil {
			return err
		}
		return os.WriteFile(python, nil, 0o644)
	}}

	resolver := &Resolver{
		cfg: Config{
			Requirements: requirements,
			EnvDir:       envDir,
			UVPath:       uvPath,
		}.WithDefaults(),
		runner:   runner,
		lookPath: func(string) (string, error) { return "", os.ErrNotExist },
	}

	resolved, err := resolver.Resolve(context.Background(), nil)
	if err != nil {
		t.Fatalf("清单变更后应当能重建环境，实际失败: %v", err)
	}
	if resolved.Source != "provisioned" {
		t.Fatalf("来源应为 provisioned，实际: %q", resolved.Source)
	}
}

// TestImportProbeCoversConversionEntry 给自检探针的深度上护栏。
//
// docling 里存在"顶层 import 能过、子模块才炸"的依赖缺失：少了 scipy / rtree 时
// `import docling` 一路正常，直到 document_converter 那条链走到 base_ocr_model 才失败。
// 探针若退回逐个 import 顶层包，准备阶段就拦不住这类缺失，只能等用户上传文档时才报错。
func TestImportProbeCoversConversionEntry(t *testing.T) {
	if !strings.Contains(importProbe, "docling.document_converter") {
		t.Fatalf("自检探针必须探到真正要用的转换入口，当前: %q", importProbe)
	}
	if !strings.Contains(importProbe, "fitz") {
		t.Fatalf("自检探针必须覆盖 PDF 文字层用到的 fitz，当前: %q", importProbe)
	}
}

func TestResolverWithoutUVExplainsHowToFix(t *testing.T) {
	allowImportProbe(t)

	dir := t.TempDir()
	requirements := filepath.Join(dir, requirementsName)
	writeFile(t, requirements, "docling-slim==2.127.0\n")

	resolver := &Resolver{
		cfg:      Config{Requirements: requirements, EnvDir: filepath.Join(dir, "env")}.WithDefaults(),
		runner:   &stubRunner{},
		lookPath: func(string) (string, error) { return "", os.ErrNotExist },
	}

	_, err := resolver.Resolve(context.Background(), nil)
	var resolveErr *Error
	if !errors.As(err, &resolveErr) || resolveErr.Code != CodeRuntimeUnavailable {
		t.Fatalf("应当返回运行时不可用，实际: %v", err)
	}
	if !strings.Contains(resolveErr.Message, "python_path") {
		t.Fatalf("错误信息里应当给出修复方式，实际: %q", resolveErr.Message)
	}
}

// realScript 返回仓库里真实的 Python 脚本与依赖清单（绝对路径）。
// go test 的工作目录是包目录，所以相对路径可以直接定位。
func realScript(t *testing.T) (string, string) {
	t.Helper()
	script, err := filepath.Abs(filepath.Join("python", scriptName))
	if err != nil {
		t.Fatalf("解析脚本绝对路径失败: %v", err)
	}
	requirements, err := filepath.Abs(filepath.Join("python", requirementsName))
	if err != nil {
		t.Fatalf("解析依赖清单绝对路径失败: %v", err)
	}
	if !isFile(script) {
		t.Skipf("找不到真实脚本 %s（测试工作目录应为包目录）", script)
	}
	return script, requirements
}

// requirePyMuPDF 确认解释器能 import fitz；没有就跳过，不把环境缺失算成代码问题。
func requirePyMuPDF(t *testing.T, python string) {
	t.Helper()
	if output, err := exec.Command(python, "-c", "import fitz").CombinedOutput(); err != nil {
		t.Skipf("解释器缺少 PyMuPDF，跳过 PDF 用例: %v\n%s", err, output)
	}
}

// generatePDF 用 PyMuPDF 现场造一份测试 PDF。
// body 是接收已打开的 doc 对象的 Python 片段，doc 的保存与关闭由这里负责。
func generatePDF(t *testing.T, python string, out string, body string) {
	t.Helper()
	source := "import sys, fitz\n" +
		"doc = fitz.open()\n" +
		body + "\n" +
		"doc.save(sys.argv[1])\n" +
		"doc.close()\n"
	path := filepath.Join(t.TempDir(), "gen_pdf.py")
	writeFile(t, path, source)
	if output, err := exec.Command(python, path, out).CombinedOutput(); err != nil {
		t.Fatalf("生成测试 PDF 失败: %v\n%s", err, output)
	}
}

// countTempParseDirs 数系统临时目录里残留的 narra-parse-* 目录。
func countTempParseDirs(t *testing.T) int {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(os.TempDir(), tempDirPrefix+"*"))
	if err != nil {
		t.Fatalf("枚举临时目录失败: %v", err)
	}
	return len(matches)
}

func TestParseStdoutRejectsJSONWithoutMarkdown(t *testing.T) {
	// 第三方库往 stdout 打的 JSON 日志常带 metadata / pages / picture_paths 这类通用字段。
	// 只要求"任意一个键命中"就会被误认成结果，而那会让解析"成功"返回空 markdown ——
	// 属于静默数据损坏，比报错危险得多。真结果必然带 markdown 键，只认它就够了。
	stdout := []byte(`{"metadata":{"module":"layout"},"pages":[],"picture_paths":[]}` + "\n")
	result, ok := parseStdout(stdout)
	if ok || result != nil {
		t.Fatalf("缺少 markdown 键的 JSON 不应被当成解析结果: %+v", result)
	}
}

func TestResultReportsOCRFailedPages(t *testing.T) {
	stdout := []byte(`{"markdown":"","picture_paths":[],"pages":[` +
		`{"number":1,"text":"ok"},` +
		`{"number":2,"text":"","failed":true},` +
		`{"number":3,"text":"","failed":true}],` +
		`"metadata":{"parser":"rapidocr","fallback":true}}` + "\n")

	result, ok := parseStdout(stdout)
	if !ok {
		t.Fatal("应当解析出结果")
	}
	failed := result.OCRFailedPages()
	if len(failed) != 2 || failed[0] != 2 || failed[1] != 3 {
		t.Fatalf("失败页报告不对: %v", failed)
	}
	// 这组字段存在的理由：整份 OCR 挂掉时 markdown 同样是空的，
	// 只有 failed 标记能把"识别失败"和"文档本来就没内容"分开。
	if result.Markdown != "" || !result.Fallback() {
		t.Fatalf("前提形态不对: markdown=%q fallback=%v", result.Markdown, result.Fallback())
	}
}

func TestParseCleansUpTempDirOnFailure(t *testing.T) {
	python := testPython(t)
	allowImportProbe(t)

	const body = `import json
print(json.dumps({"error_code": "PARSER_FAILED", "error_message": "boom"}))
raise SystemExit(2)
`
	script, requirements := fakeScript(t, body)
	input := filepath.Join(t.TempDir(), "doc.pdf")
	writeFile(t, input, "x")

	parser, err := NewPythonParser(Config{PythonPath: python, ScriptPath: script, Requirements: requirements})
	if err != nil {
		t.Fatalf("构造解析器失败: %v", err)
	}

	before := countTempParseDirs(t)
	if _, err := parser.Parse(context.Background(), Request{Path: input}); err == nil {
		t.Fatal("应当返回错误")
	}
	if after := countTempParseDirs(t); after > before {
		t.Fatalf("失败路径泄漏了临时目录: before=%d after=%d", before, after)
	}
}

func TestPythonScriptKeepsTextLayerWhenOnlyBlankPagesFollow(t *testing.T) {
	python := testPython(t)
	requirePyMuPDF(t, python)
	allowImportProbe(t)

	script, requirements := realScript(t)
	pdfPath := filepath.Join(t.TempDir(), "blank_tail.pdf")
	generatePDF(t, python, pdfPath,
		"p = doc.new_page()\n"+
			"p.insert_text((72, 72), 'text layer page')\n"+
			"doc.new_page()")

	parser, err := NewPythonParser(Config{PythonPath: python, ScriptPath: script, Requirements: requirements})
	if err != nil {
		t.Fatalf("构造解析器失败: %v", err)
	}

	result, err := parser.Parse(context.Background(), Request{Path: pdfPath})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	defer func() { _ = result.Cleanup() }()

	// 空白页（无文字层也无图片）不该被当成扫描页：否则一份带章节分隔页的正常文档
	// 会被拖去加载 OCR 引擎，白白付出一次初始化代价。
	if got := result.ParserName(); got != "pymupdf" {
		t.Fatalf("空白页不该触发 OCR，解析器应为 pymupdf，实际: %q", got)
	}
	if result.Mixed() {
		t.Fatal("只有空白页时不该标记为混合型")
	}
	if !strings.Contains(result.Markdown, "text layer page") {
		t.Fatalf("文字层内容丢了: %q", result.Markdown)
	}
	if len(result.Pages) != 2 {
		t.Fatalf("两页信息都应保留，实际: %d", len(result.Pages))
	}
}

func TestPythonScriptOCRsScannedPagesInMixedPDF(t *testing.T) {
	python := testPython(t)
	requirePyMuPDF(t, python)
	allowImportProbe(t)

	// 用假的 OpenAI 兼容服务顶替 OCR：既不需要 rapidocr，也能让"扫描页内容进入
	// markdown"有确定断言。api_ocr.py 只用标准库，所以不引入额外依赖。
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"scanned page two"}}]}`))
	}))
	defer server.Close()

	script, requirements := realScript(t)
	pdfPath := filepath.Join(t.TempDir(), "mixed.pdf")
	generatePDF(t, python, pdfPath,
		"p1 = doc.new_page()\n"+
			"p1.insert_text((72, 72), 'page one text layer')\n"+
			"p2 = doc.new_page()\n"+
			"pix = fitz.Pixmap(fitz.csRGB, fitz.IRect(0, 0, 120, 60))\n"+
			"pix.set_rect(pix.irect, (0, 0, 0))\n"+
			"p2.insert_image(fitz.Rect(72, 72, 272, 172), pixmap=pix)\n"+
			"p3 = doc.new_page()\n"+
			"p3.insert_text((72, 72), 'page three text layer')")

	parser, err := NewPythonParser(Config{PythonPath: python, ScriptPath: script, Requirements: requirements})
	if err != nil {
		t.Fatalf("构造解析器失败: %v", err)
	}

	result, err := parser.Parse(context.Background(), Request{
		Path:          pdfPath,
		OCREngine:     OCREngineAPI,
		OCRAPIBaseURL: server.URL,
		OCRAPIModel:   "fake-ocr",
	})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	defer func() { _ = result.Cleanup() }()

	if !result.Mixed() {
		t.Fatal("文字层与扫描页混排的 PDF 应当标记为混合型")
	}
	if len(result.Pages) != 3 {
		t.Fatalf("三页都应保留，实际: %d", len(result.Pages))
	}
	if failed := result.OCRFailedPages(); len(failed) != 0 {
		t.Fatalf("假 OCR 服务不会失败，不该报告失败页: %v", failed)
	}

	// 核心断言：扫描页的内容必须进入 markdown，且页序保持 1 → 2 → 3。
	// 按整份判定的旧逻辑会直接返回只有第 1、3 页的结果，第 2 页静默消失。
	first := strings.Index(result.Markdown, "page one text layer")
	second := strings.Index(result.Markdown, "scanned page two")
	third := strings.Index(result.Markdown, "page three text layer")
	if first < 0 || second < 0 || third < 0 {
		t.Fatalf("markdown 缺少某页内容: first=%d second=%d third=%d\n%s",
			first, second, third, result.Markdown)
	}
	if !(first < second && second < third) {
		t.Fatalf("页序不对: first=%d second=%d third=%d", first, second, third)
	}
	if result.Pages[1].Number != 2 || !strings.Contains(result.Pages[1].Text, "scanned page two") {
		t.Fatalf("第 2 页应当带上 OCR 文本: %+v", result.Pages[1])
	}
}

// blockingRunner 在第一次被调用时通知，然后一直等到 ctx 结束 ——
// 模拟"uv 装依赖时卡住"。
type blockingRunner struct{ started chan struct{} }

func (b *blockingRunner) Run(ctx context.Context, _ string, _ []string, _ string, _ ...string) ([]byte, []byte, error) {
	close(b.started)
	<-ctx.Done()
	return nil, nil, ctx.Err()
}

// newBlockedPrepareParser 造一个"准备阶段会一直卡住"的解析器：
// 脚本与依赖清单是真文件（过配置校验），uv 路径指向一个存在的占位文件，
// 真正的命令执行被阻塞 runner 接管（注：准备走 resolver 的 runner，
// 解析脚本才走 parser.runner）。
func newBlockedPrepareParser(t *testing.T, prepareTimeout time.Duration) (*PythonParser, *blockingRunner) {
	t.Helper()

	script, requirements := fakeScript(t, "print('{}')")
	uvPath := filepath.Join(t.TempDir(), "uv")
	writeFile(t, uvPath, "stub")

	parser, err := NewPythonParser(Config{
		ScriptPath:     script,
		Requirements:   requirements,
		RuntimeDir:     t.TempDir(), // 空目录：确保不会走"随包运行时"而绕过 provision
		EnvDir:         t.TempDir(),
		UVPath:         uvPath,
		PrepareTimeout: prepareTimeout,
	})
	if err != nil {
		t.Fatalf("构造解析器失败: %v", err)
	}

	blocker := &blockingRunner{started: make(chan struct{})}
	parser.resolver.runner = blocker
	return parser, blocker
}

// 准备环境期间 Status 必须立刻给出"正在准备"，而不是跟在 Prepare 后面一起挂住。
//
// 这是前端"首次上传需先配置环境"提示的数据来源：那段时间可能十几分钟，若状态接口
// 被同一个锁顶住，前端连这句话都问不出来。
func TestStatusReportsPreparingWithoutBlocking(t *testing.T) {
	parser, blocker := newBlockedPrepareParser(t, 0) // 0 走默认 20 分钟

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- parser.Prepare(ctx, nil) }()

	select {
	case <-blocker.started:
	case <-time.After(10 * time.Second):
		t.Fatal("准备流程没有开始")
	}

	statusCh := make(chan Status, 1)
	go func() { statusCh <- parser.Status(context.Background()) }()
	select {
	case status := <-statusCh:
		if !status.Enabled {
			t.Error("解析能力是开着的，Enabled 应当为真")
		}
		if status.Ready {
			t.Error("环境还没准备好，不该报 ready")
		}
		if !status.Preparing {
			t.Error("应当报告正在准备环境")
		}
		if status.Progress == "" {
			t.Error("应当带上当前准备进度")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Status 被准备过程阻塞了")
	}

	cancel()
	<-done

	if status := parser.Status(context.Background()); status.Preparing {
		t.Error("准备结束后不该还挂着 Preparing")
	}
}

// 准备阶段必须有墙钟上限：卡住的 uv pip install 会把 worker 的整轮调度停住
// （见 rag.Worker.process 的 group.Wait），该行永远 processing、上传入口跟着 409。
// 到点必须返回一个可辨认的错误码，而不是无限等。
func TestPrepareTimesOut(t *testing.T) {
	parser, _ := newBlockedPrepareParser(t, 50*time.Millisecond)

	err := parser.Prepare(context.Background(), nil)

	var prepareErr *Error
	if !errors.As(err, &prepareErr) {
		t.Fatalf("应当返回解析器错误，实际: %v", err)
	}
	if prepareErr.Code != CodePrepareTimeout {
		t.Fatalf("错误码 = %q，期望 %q", prepareErr.Code, CodePrepareTimeout)
	}
	if parser.currentRuntime() != nil {
		t.Error("超时后不该缓存半成品运行时，下次解析必须重新准备")
	}
}

// 关停取消与超时是两回事：取消必须原样上抛 context.Canceled，
// 让 worker 走"不落终态、等周期回收"的分支，而不是记成一次环境准备失败。
func TestPrepareCanceledIsNotReportedAsTimeout(t *testing.T) {
	parser, blocker := newBlockedPrepareParser(t, time.Minute)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- parser.Prepare(ctx, nil) }()

	select {
	case <-blocker.started:
	case <-time.After(10 * time.Second):
		t.Fatal("准备流程没有开始")
	}
	cancel()

	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消应当原样上抛 context.Canceled，实际: %v", err)
	}
	var prepareErr *Error
	if errors.As(err, &prepareErr) {
		t.Fatalf("取消不该被包装成解析器错误: %v", prepareErr)
	}
}

// 结果行没有结尾换行时也要能取到（脚本用 print 保证有，但日志噪声可能改变行尾）。
func TestParseStdoutAcceptsLineWithoutTrailingNewline(t *testing.T) {
	stdout := []byte("INFO docling: loading layout model\n{\"markdown\":\"# 标题\"}")
	result, ok := parseStdout(stdout)
	if !ok {
		t.Fatal("末行没有换行时也应当能解析出结果")
	}
	if result.Markdown != "# 标题" {
		t.Fatalf("markdown 不对: %q", result.Markdown)
	}
}

// CRLF 行尾不能把 \r 带进 JSON 解析（Windows 上的日志库有时这么写）。
func TestParseStdoutHandlesCRLF(t *testing.T) {
	stdout := []byte("INFO: ready\r\n{\"markdown\":\"# 标题\"}\r\n")
	result, ok := parseStdout(stdout)
	if !ok {
		t.Fatal("CRLF 行尾应当被当作空白处理")
	}
	if result.Markdown != "# 标题" {
		t.Fatalf("markdown 不对: %q", result.Markdown)
	}

	found := errorFromOutput([]byte("{\"error_code\":\"PARSER_FAILED\",\"error_message\":\"boom\"}\r\n"), nil)
	if found == nil || found.Code != CodeFailed {
		t.Fatalf("错误协议在 CRLF 下也应当可识别，实际: %+v", found)
	}
}

// 逐行反扫要覆盖首行、末行与空输入这几个边界。
func TestForEachLineReverseVisitsAllLines(t *testing.T) {
	var lines []string
	forEachLineReverse([]byte("a\nbb\nccc"), func(line []byte) bool {
		lines = append(lines, string(line))
		return true
	})
	if got := strings.Join(lines, ","); got != "ccc,bb,a" {
		t.Errorf("反扫顺序 = %q，期望 ccc,bb,a", got)
	}

	lines = nil
	forEachLineReverse([]byte("a\nbb\nccc\n"), func(line []byte) bool {
		// 结尾换行会多出一个空段（与 bytes.Split 的行为一致），跳过它。
		if text := strings.TrimSpace(string(line)); text != "" {
			lines = append(lines, text)
		}
		return true
	})
	if got := strings.Join(lines, ","); got != "ccc,bb,a" {
		t.Errorf("带结尾换行时 = %q，期望 ccc,bb,a", got)
	}

	lines = nil
	forEachLineReverse(nil, func(line []byte) bool {
		lines = append(lines, string(line))
		return true
	})
	if len(lines) != 0 {
		t.Errorf("空输入不该产生任何行，实际 %v", lines)
	}

	// visit 返回 false 时提前停止
	count := 0
	forEachLineReverse([]byte("1\n2\n3\n4"), func([]byte) bool {
		count++
		return count < 2
	})
	if count != 2 {
		t.Errorf("提前停止应当只访问 2 行，实际 %d", count)
	}
}
