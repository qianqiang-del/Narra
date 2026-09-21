package documentparser

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// 错误码。前 5 个与 python/parse_document.py 的 error_code 一一对应；后 3 个是 Go 侧新增的
// ——它们描述的是"还没到脚本那一步"就发生的事，脚本永远不会报这三个码。
const (
	CodeFileNotFound       = "PARSER_FILE_NOT_FOUND"
	CodeUnsupportedType    = "PARSER_UNSUPPORTED_TYPE"
	CodeOCRConfigMissing   = "PARSER_OCR_CONFIG_MISSING"
	CodeOCREngineInvalid   = "PARSER_OCR_ENGINE_INVALID"
	CodeFailed             = "PARSER_FAILED"
	CodeRuntimeUnavailable = "PARSER_RUNTIME_UNAVAILABLE"
	CodeTimeout            = "PARSER_TIMEOUT"
	CodeEncodingInvalid    = "PARSER_ENCODING_INVALID"
)

const (
	// stderrTailBytes 错误里回带的 stderr 长度上限。
	stderrTailBytes = 800
	// tempDirPrefix 解析产物（导出图片）的临时目录前缀。
	tempDirPrefix = "narra-parse-"
)

// Error 是解析器的稳定错误。Code 可直接用于前端文案映射，不需要解析 message。
type Error struct {
	Code    string
	Message string
	Stderr  string
}

// Error 拼出的是**给日志与排障看的完整诊断**：错误码在前、stderr 原文在后。
// 它不适合直接显示给用户 —— 界面要的是 UserMessage()。
func (e *Error) Error() string {
	if e.Stderr == "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("%s: %s (stderr: %s)", e.Code, e.Message, e.Stderr)
}

// UserMessage 把错误折成一句给用户看的中文：没有错误码，也没有 stderr 原文。
//
// 这两个方法的分工必须分清，它们很容易被混用，而混用的后果就在界面上：
//   - Error() 是诊断串，形如
//     "PARSER_FAILED: 文档解析失败 (stderr: parser failed: No module named 'scipy')"；
//   - UserMessage() 是一句人话，形如 "文档解析失败：解析环境缺少 Python 模块 scipy"。
//
// 主体直接取 Message —— 脚本的 error_message 本来就是中文，Go 侧新增的几条也是，
// 所以不需要按错误码另立一张文案表。stderr 只在能认出"缺哪个依赖"这类
// 用户可据以行动的原因时才作为补充缀上去（见 stderrHint）。
func (e *Error) UserMessage() string {
	if e == nil {
		return ""
	}
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = defaultUserMessage(e.Code)
	}
	hint := stderrHint(e.Stderr)
	switch {
	case message == "":
		return hint
	case hint == "":
		return message
	default:
		return message + "：" + hint
	}
}

// defaultUserMessage 是 Message 为空时按错误码给的兜底说法。
//
// 正常情况下走不到这里（每条错误都带了 Message）。但界面拿到空串就没法解释
// "这一行为什么是红的"，所以宁可给一句笼统的，也不要留空。
func defaultUserMessage(code string) string {
	switch code {
	case CodeFileNotFound:
		return "文件不存在或已被移动"
	case CodeUnsupportedType:
		return "不支持这种文件类型"
	case CodeOCRConfigMissing:
		return "这份文件需要 OCR，但还没配置 OCR 服务"
	case CodeOCREngineInvalid:
		return "OCR 引擎配置有误"
	case CodeRuntimeUnavailable:
		return "文档解析环境不可用"
	case CodeTimeout:
		return "文档解析超时"
	case CodeEncodingInvalid:
		return "文件编码不是 UTF-8 或 UTF-16"
	default:
		return "文档解析失败"
	}
}

// stderrHint 从解析脚本的 stderr 里摘一句中文原因，认不出来就返回空串。
//
// 只认"含义确定、用户能据此行动"的那几种。宁可返回空串（此时界面只显示 Message），
// 也不把整段 traceback 原样贴上去 —— 那正是这个函数要消掉的东西。
// 需要完整 stderr 的是排障，那条路走 Error() 与 metadata.error_detail。
func stderrHint(stderr string) string {
	if module := missingModule(stderr); module != "" {
		return "解析环境缺少 Python 模块 " + module
	}
	return ""
}

// missingModule 从 stderr 里认出 "No module named 'xxx'" 这类缺失依赖的报错，
// 返回模块名；认不出来返回空串。
//
// 这是本地解析环境最常见的失败：依赖没装全时，此后每一份需要它的文件都会在
// 同一步倒下，用户看到的原因却一直是笼统的"文档解析失败"。
// 模块名保留原样 —— 它是 Python 的标识符，不是可以翻译的文案。
func missingModule(stderr string) string {
	const marker = "No module named "
	index := strings.Index(stderr, marker)
	if index < 0 {
		return ""
	}
	// 模块名可能被引号包着（"No module named 'scipy'"），先剥掉左侧的包裹字符。
	rest := strings.TrimLeft(stderr[index+len(marker):], " \t'\"")
	end := 0
	for end < len(rest) && isModuleNameByte(rest[end]) {
		end++
	}
	// 尾巴上的点号属于句子（"No module named 'scipy'."），不属于模块名。
	return strings.TrimRight(rest[:end], ".")
}

// isModuleNameByte 判断一个字节能否是模块名的一部分。
//
// 只认 ASCII：Python 的依赖名实际都是 ASCII，而按字节判断能天然在多字节字符的
// 第一个字节上停下来，不会切出半个汉字。
func isModuleNameByte(char byte) bool {
	switch {
	case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9':
		return true
	case char == '_', char == '.', char == '-':
		return true
	default:
		return false
	}
}

// Page 是脚本输出的单页信息（仅 PDF 有）。
type Page struct {
	Number int    `json:"number"`
	Text   string `json:"text"`

	// Failed 表示这一页的 OCR 识别失败 —— 与"这一页本来就没有文字"是两回事。
	// 脚本对单页失败不中断整篇，所以失败信息只能从这里读出来；没有它，下游无法
	// 区分"扫描件是空白页"和"整条 OCR 都挂了"。
	Failed bool `json:"failed,omitempty"`
}

// Result 是一次解析的产物，字段与脚本输出一一对应。
type Result struct {
	Markdown     string         `json:"markdown"`
	PicturePaths []string       `json:"picture_paths"`
	Pages        []Page         `json:"pages"`
	Metadata     map[string]any `json:"metadata"`

	// WorkDir 本次解析产物的根目录（导出图片在里面）。
	// 由调用方负责清理：图片要先上传到对象存储，再删掉整个目录。
	WorkDir string `json:"-"`
}

// ParserName 返回脚本自报的解析器身份（metadata.parser）。
//
// 取值是 pymupdf / docling / rapidocr / api_ocr —— 供排障与统计使用，不要拿来硬编码判断。
func (r *Result) ParserName() string {
	if r == nil || r.Metadata == nil {
		return ""
	}
	name, _ := r.Metadata["parser"].(string)
	return name
}

// Fallback 表示本次是否走了 OCR 回退路径（仅 PDF 有意义）。
func (r *Result) Fallback() bool {
	if r == nil || r.Metadata == nil {
		return false
	}
	value, _ := r.Metadata["fallback"].(bool)
	return value
}

// Mixed 表示这份 PDF 既用了文字层也用了 OCR（即含扫描页的混合型文档）。
//
// 混合意味着解析方式不统一：文字层部分保真，OCR 部分取决于识别质量。做质量评估
// 或分页引用时应当知道这一点，否则会误以为整份文档是同一个来源。
func (r *Result) Mixed() bool {
	if r == nil || r.Metadata == nil {
		return false
	}
	mixed, _ := r.Metadata["mixed"].(bool)
	return mixed
}

// OCRFailedPages 返回 OCR 识别失败的页码。
//
// 整份 OCR 全失败时它会包含所有页 —— 那种情况下 Markdown 为空并不代表"文档没有
// 内容"，调用方应据此决定重试还是报错，而不是把空结果当正常产物入库。
func (r *Result) OCRFailedPages() []int {
	if r == nil {
		return nil
	}
	var failed []int
	for _, page := range r.Pages {
		if page.Failed {
			failed = append(failed, page.Number)
		}
	}
	return failed
}

// Cleanup 删除本次解析的产物目录（导出的图片在里面）。
//
// 成功路径的临时目录归调用方所有：先把 picture_paths 上传到对象存储、把 Markdown
// 里的本地路径回填成 URL，再调这里。失败路径由 Parse 自己清理，那时 Result 是 nil，
// 所以调用方用 defer 兜底也不必判空（nil 接收者安全）。
func (r *Result) Cleanup() error {
	if r == nil || strings.TrimSpace(r.WorkDir) == "" {
		return nil
	}
	return os.RemoveAll(r.WorkDir)
}

// Request 是一次解析请求，留空的字段回落到 Config 的同名配置。
type Request struct {
	// Path 待解析文件的路径。
	Path string
	// WorkDir 图片导出根目录。留空时自动新建系统临时目录，实际目录记在 Result.WorkDir。
	WorkDir string
	// OCREngine / OCRAPIBaseURL / OCRAPIKey / OCRAPIModel 覆盖配置里的 OCR 设置。
	OCREngine     string
	OCRAPIBaseURL string
	OCRAPIKey     string
	OCRAPIModel   string
}

// Status 描述解析能力的就绪状态。
type Status struct {
	Ready  bool   `json:"ready"`
	Source string `json:"source,omitempty"`
	Python string `json:"python,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// Parser 是文档解析能力的最小接口。
//
// 先立接口再实现，是因为本地 Python 子进程只是众多形态之一：外部解析服务、测试替身、
// 将来的常驻 worker 都是实现。上层摄入链路只依赖这个接口，换实现不用动调用方。
type Parser interface {
	Parse(ctx context.Context, req Request) (*Result, error)
	Status(ctx context.Context) Status
}

// PythonParser 通过本地 Python 子进程实现 Parser。
//
// 使用者机器上不需要预装 Python：解释器与依赖由 Resolver 三级解析（见 resolve.go）。
type PythonParser struct {
	cfg      Config
	resolver *Resolver
	runner   commandRunner

	mu      sync.Mutex
	runtime *Runtime
}

// 编译期锚定接口，避免以后改签名时悄悄漂移。
var _ Parser = (*PythonParser)(nil)

// NewPythonParser 构造解析器。配置里的空值会按默认布局补全，关键路径缺失会直接报错。
func NewPythonParser(cfg Config) (*PythonParser, error) {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &PythonParser{cfg: cfg, resolver: NewResolver(cfg), runner: osRunner{}}, nil
}

// Config 返回补全后的配置，便于调用方查看实际生效的路径。
func (p *PythonParser) Config() Config {
	return p.cfg
}

// Prepare 确保运行时可用。
//
// 首次调用可能耗时数分钟（要下载解释器与依赖），所以允许在后台调用并通过 onProgress
// 汇报进度 —— 千万不要把它放在 HTTP 请求路径上同步等。
func (p *PythonParser) Prepare(ctx context.Context, onProgress func(string)) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.runtime != nil {
		return nil
	}
	resolved, err := p.resolver.Resolve(ctx, onProgress)
	if err != nil {
		return err
	}
	p.runtime = resolved
	return nil
}

// Status 返回就绪状态，供健康检查探活。
func (p *PythonParser) Status(ctx context.Context) Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.runtime == nil {
		return Status{Ready: false, Source: "python", Reason: "运行环境尚未准备；首次解析时会自动准备"}
	}
	return Status{Ready: true, Source: p.runtime.Source, Python: p.runtime.Python}
}

// Parse 调用脚本解析一个文件。
//
// 临时目录的归属：req.WorkDir 由调用方提供时归调用方；留空时这里自动创建，
// 成功时随 Result.WorkDir 交给调用方（它要先上传图片、回填 URL 再删），
// 失败时由这里清掉 —— 否则每个失败请求都会在系统临时目录留下一个 narra-parse-*。
func (p *PythonParser) Parse(ctx context.Context, req Request) (result *Result, err error) {
	if strings.TrimSpace(req.Path) == "" {
		return nil, &Error{Code: CodeFileNotFound, Message: "待解析文件路径为空"}
	}
	if !isFile(req.Path) {
		return nil, &Error{Code: CodeFileNotFound, Message: "待解析文件不存在: " + req.Path}
	}
	if err := p.Prepare(ctx, nil); err != nil {
		return nil, err
	}

	python := p.pythonPath()
	if python == "" {
		return nil, &Error{Code: CodeRuntimeUnavailable, Message: "Python 运行时尚未就绪"}
	}

	workDir := strings.TrimSpace(req.WorkDir)
	if workDir == "" {
		dir, createErr := os.MkdirTemp("", tempDirPrefix)
		if createErr != nil {
			return nil, &Error{Code: CodeFailed, Message: "创建解析临时目录失败: " + createErr.Error()}
		}
		workDir = dir
		// best-effort：删不掉也只是多留一个临时目录，不该把清理失败放大成解析失败。
		defer func() {
			if err != nil {
				_ = os.RemoveAll(workDir)
			}
		}()
	}

	parseCtx, cancel := context.WithTimeout(ctx, p.cfg.ParseTimeout)
	defer cancel()

	args := []string{
		p.cfg.ScriptPath,
		"--input", req.Path,
		"--ocr-engine", firstNonEmpty(req.OCREngine, p.cfg.OCREngine),
		"--work-dir", workDir,
	}
	if apiURL := firstNonEmpty(req.OCRAPIBaseURL, p.cfg.OCRAPIBaseURL); apiURL != "" {
		args = append(args, "--ocr-api-url", apiURL)
	}
	if model := firstNonEmpty(req.OCRAPIModel, p.cfg.OCRAPIModel); model != "" {
		args = append(args, "--ocr-api-model", model)
	}

	stdout, stderr, err := p.runner.Run(parseCtx, "", p.pythonEnv(req), python, args...)
	if err != nil {
		if parseCtx.Err() != nil {
			return nil, &Error{
				Code:    CodeTimeout,
				Message: fmt.Sprintf("文档解析超时（%s），文件: %s", p.cfg.ParseTimeout, filepath.Base(req.Path)),
				Stderr:  tail(string(stderr), stderrTailBytes),
			}
		}
		if wireErr := errorFromOutput(stdout, stderr); wireErr != nil {
			return nil, wireErr
		}
		// Message 只放一句人话，不把 err.Error()（"exit status 2" 这类 Go 的 exec 术语）
		// 拼进去 —— 它对用户没有任何可操作性，真因在 stderr 里，由 UserMessage 摘出来。
		// 例外是 stderr 也空的时候：那时 exec 的错误是唯一线索，兜进 Message，
		// 免得界面只剩一句无从下手的"文档解析失败"。
		message := "文档解析失败"
		stderrTail := tail(string(stderr), stderrTailBytes)
		if stderrTail == "" {
			message = "文档解析失败: " + err.Error()
		}
		return nil, &Error{
			Code:    CodeFailed,
			Message: message,
			Stderr:  stderrTail,
		}
	}

	parsed, ok := parseStdout(stdout)
	if !ok {
		return nil, &Error{
			Code:    CodeFailed,
			Message: "解析器没有输出可识别的 JSON 结果",
			Stderr:  tail(string(stderr), stderrTailBytes),
		}
	}
	parsed.WorkDir = workDir
	return parsed, nil
}

func (p *PythonParser) pythonPath() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.runtime == nil {
		return ""
	}
	return p.runtime.Python
}

// pythonEnv 传给脚本的环境变量。
//
// OCR 密钥走环境变量而不是命令行参数：命令行会出现在进程列表里，同一台机器上的
// 其他用户可以直接看到，而设置页保存的密钥是加密存储的。
func (p *PythonParser) pythonEnv(req Request) []string {
	env := []string{
		"PYTHONIOENCODING=utf-8", // 脚本自己也 reconfigure 了标准输出，这里是双保险
		"PYTHONUTF8=1",
		"PYTHONDONTWRITEBYTECODE=1", // 不在（可能只读的）安装目录里留 __pycache__
	}
	if key := firstNonEmpty(req.OCRAPIKey, p.cfg.OCRAPIKey); key != "" {
		env = append(env, "NARRA_OCR_API_KEY="+key)
	}
	return env
}

// wireError 对应脚本的错误协议。
type wireError struct {
	Code    string `json:"error_code"`
	Message string `json:"error_message"`
}

// parseStdout 从标准输出里取结果。
//
// 脚本正常时只往 stdout 打一行 JSON，但第三方库（docling 等）往 stdout 打日志的概率不为零，
// 所以从后往前找第一个"看起来是解析结果"的 JSON 对象，而不是直接解析整段输出。
func parseStdout(stdout []byte) (*Result, bool) {
	lines := bytes.Split(stdout, []byte{'\n'})
	for index := len(lines) - 1; index >= 0; index-- {
		text := strings.TrimSpace(string(lines[index]))
		if !isJSONObject(text) {
			continue
		}
		var probe map[string]json.RawMessage
		if err := json.Unmarshal([]byte(text), &probe); err != nil {
			continue
		}
		if !looksLikeResult(probe) {
			continue
		}
		var result Result
		if err := json.Unmarshal([]byte(text), &result); err != nil {
			continue
		}
		return &result, true
	}
	return nil, false
}

// errorFromOutput 提取脚本的错误协议。
//
// 脚本失败时先往 stdout 打一行错误 JSON，再以 exit 2 退出；但 Python 的 traceback 走 stderr，
// 所以两个流都要看。
func errorFromOutput(stdout, stderr []byte) *Error {
	for _, stream := range [][]byte{stdout, stderr} {
		lines := bytes.Split(stream, []byte{'\n'})
		for index := len(lines) - 1; index >= 0; index-- {
			text := strings.TrimSpace(string(lines[index]))
			if !isJSONObject(text) {
				continue
			}
			var wire wireError
			if err := json.Unmarshal([]byte(text), &wire); err != nil || wire.Code == "" {
				continue
			}
			return &Error{Code: wire.Code, Message: wire.Message, Stderr: tail(string(stderr), stderrTailBytes)}
		}
	}
	return nil
}

// looksLikeResult 判断一行 JSON 是否是脚本的解析结果。
//
// 只认 markdown 键：脚本的 ParseResult 是 dataclass，markdown 字段恒出现在输出里，
// 所以它既是必要条件也是稳定的身份标志。放宽到"任意一个键命中就算"会让第三方库
// 往 stdout 打的 JSON 日志（那些库很爱用 metadata、pages 这类通用字段）被误认成
// 结果 —— 那会让解析"成功"返回空 markdown，属于典型的静默数据损坏。
func looksLikeResult(probe map[string]json.RawMessage) bool {
	_, ok := probe["markdown"]
	return ok
}

func isJSONObject(text string) bool {
	return len(text) >= 2 && text[0] == '{' && text[len(text)-1] == '}'
}

// tail 取字符串末尾 limit 个字节，用于回带错误现场。
func tail(text string, limit int) string {
	if len(text) <= limit {
		return strings.TrimSpace(text)
	}
	return "..." + strings.TrimSpace(text[len(text)-limit:])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
