package documentparser

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// OCR 引擎取值，与 python/parse_document.py 的 --ocr-engine 选项一一对应。
const (
	// OCREngineRapidOCR 本地 ONNX 推理，不外发文档内容。默认值。
	OCREngineRapidOCR = "rapidocr"
	// OCREngineAPI 通用 OCR API，会把图片发送到外部服务，必须由用户显式选择。
	OCREngineAPI = "api"
)

const (
	// defaultPythonVersion uv 默认准备的解释器版本。docling 要求 >= 3.10。
	defaultPythonVersion = "3.12"
	// defaultParseTimeout 单次解析超时。CPU 上跑版面模型是分钟级的，所以给得比较宽。
	defaultParseTimeout = 10 * time.Minute
	// defaultPrepareTimeout 单次环境准备（下载解释器 + 建 venv + 装依赖）的墙钟上限。
	//
	// 正常安装就是分钟级的（实测 uv pip install 约 15 分钟），所以给足余量；但必须有，
	// 因为准备跑在解析之前、与解析共用一条调用链：一次卡住的 uv pip install 会把
	// worker 的整轮调度停在 group.Wait 上（见 rag.Worker.process），
	// 该行永远 processing、上传入口跟着永久 409。
	defaultPrepareTimeout = 20 * time.Minute

	// defaultMaxOCRPages 单次解析允许 OCR 的页数上限。
	//
	// 扫描页是一页一次推理（或一次网络往返），页数不设限时一份几百页的扫描件会：
	//  1) 把唯一的 worker 占满整个解析预算 —— 期间所有人上传都是 409；
	//  2) 大概率撞上解析超时被杀，而重试是从第 1 页重来，永远过不去。
	// 超限快速失败（错误码 PARSER_TOO_MANY_OCR_PAGES），把"该拆文件"这件事直接告诉用户。
	// 100 页按本地小模型的速度大致是几分钟，留得出余量给后续的切分与向量化。
	defaultMaxOCRPages = 100

	scriptName       = "parse_document.py"
	requirementsName = "requirements.txt"
)

// Config 是文档解析器的运行配置。
//
// 设计前提：使用者机器上不需要预装 Python —— 解释器与依赖由本包自行解析（见 Resolver），
// 或者由发布包直接携带。
type Config struct {
	// Enabled 是否对外提供文档解析能力。默认关闭，与 embedding / tts 的处理一致。
	Enabled bool
	// PythonPath 显式指定解释器路径。留空则走自动解析；企业内已有环境、或开发机调试时用得上。
	PythonPath string
	// RuntimeDir 随发布包携带的独立运行时目录（离线模式）。留空时取 <程序目录>/python-runtime。
	RuntimeDir string
	// EnvDir uv 现场建的环境目录（在线模式）。留空时取 <用户缓存目录>/narra/documentparser-env。
	EnvDir string
	// ScriptPath parse_document.py 路径。留空时按 发布包目录 → 源码目录 的顺序自动查找。
	ScriptPath string
	// Requirements 依赖清单路径。留空时取与脚本同目录的 requirements.txt。
	Requirements string
	// UVPath uv 可执行文件路径。留空时按 RuntimeDir → 程序目录 → PATH 的顺序查找。
	UVPath string
	// PythonVersion uv 需要准备的解释器版本，如 3.12。
	PythonVersion string
	// IndexURL PyPI 镜像地址（如 https://pypi.tuna.tsinghua.edu.cn/simple）。
	// 留空走官方源；国内首次准备依赖时建议配置。
	IndexURL string
	// ParseTimeout 单次解析超时，必须为正。
	ParseTimeout time.Duration
	// PrepareTimeout 单次环境准备（uv 下载解释器与依赖）的墙钟上限，必须为正。
	// 它只约束"准备"这一段，不含解析；超时会杀掉 uv 进程并按准备失败报错。
	PrepareTimeout time.Duration
	// MaxOCRPages 单次解析允许 OCR 的页数上限，必须为正。
	// 超过时脚本快速失败（PARSER_TOO_MANY_OCR_PAGES），避免一份大扫描件占满解析预算。
	MaxOCRPages int
	// OCREngine 见 OCREngineXxx 常量。
	OCREngine string
	// OCRAPIBaseURL / OCRAPIKey / OCRAPIModel 仅在 OCREngine 为 api 时使用。
	// 注意：密钥通过环境变量传给脚本，不会出现在进程命令行里。
	OCRAPIBaseURL string
	OCRAPIKey     string
	OCRAPIModel   string
	// WorkDir 图片导出根目录。留空时每次解析新建系统临时目录。
	WorkDir string
}

// WithDefaults 补全空值。
//
// 脚本与依赖清单按发布包布局查找；目录一律落在用户缓存区而不是程序目录 —— 程序目录
// 通常在 Program Files 下是只读的，而且卸载会连带删除。
func (c Config) WithDefaults() Config {
	if strings.TrimSpace(c.ScriptPath) == "" {
		c.ScriptPath = firstExisting(scriptCandidates())
	}
	if strings.TrimSpace(c.Requirements) == "" && c.ScriptPath != "" {
		c.Requirements = filepath.Join(filepath.Dir(c.ScriptPath), requirementsName)
	}
	if strings.TrimSpace(c.RuntimeDir) == "" {
		c.RuntimeDir = filepath.Join(executableDir(), "python-runtime")
	}
	if strings.TrimSpace(c.EnvDir) == "" {
		c.EnvDir = filepath.Join(userCacheDir(), "documentparser-env")
	}
	if strings.TrimSpace(c.PythonVersion) == "" {
		c.PythonVersion = defaultPythonVersion
	}
	if c.ParseTimeout <= 0 {
		c.ParseTimeout = defaultParseTimeout
	}
	if c.PrepareTimeout <= 0 {
		c.PrepareTimeout = defaultPrepareTimeout
	}
	if c.MaxOCRPages <= 0 {
		c.MaxOCRPages = defaultMaxOCRPages
	}
	if strings.TrimSpace(c.OCREngine) == "" {
		c.OCREngine = OCREngineRapidOCR
	}
	return c
}

// Validate 校验配置。
//
// 脚本与依赖清单必须能找到 —— 这两个文件缺失时错误信息要直接指向修复动作，
// 而不是等真正解析时才抛一个看不懂的 ImportError。
func (c Config) Validate() error {
	if strings.TrimSpace(c.ScriptPath) == "" {
		return fmt.Errorf("找不到 %s，请检查发布包是否完整，或用 document_parser.script_path 显式指定", scriptName)
	}
	if !isFile(c.ScriptPath) {
		return fmt.Errorf("document_parser.script_path 指向的文件不存在: %s", c.ScriptPath)
	}
	if strings.TrimSpace(c.Requirements) == "" || !isFile(c.Requirements) {
		return fmt.Errorf("找不到依赖清单 %s，请检查发布包是否完整", filepath.Join(filepath.Dir(c.ScriptPath), requirementsName))
	}
	if c.ParseTimeout <= 0 {
		return fmt.Errorf("document_parser.timeout 必须大于 0")
	}
	if c.PrepareTimeout <= 0 {
		return fmt.Errorf("document_parser.prepare_timeout 必须大于 0")
	}
	if c.MaxOCRPages <= 0 {
		return fmt.Errorf("document_parser.max_ocr_pages 必须大于 0")
	}

	switch c.OCREngine {
	case OCREngineRapidOCR:
	case OCREngineAPI:
		if err := validateHTTPURL(c.OCRAPIBaseURL); err != nil {
			return fmt.Errorf("document_parser.ocr_api_base_url 必须是有效的 http 或 https URL")
		}
		if strings.TrimSpace(c.OCRAPIModel) == "" {
			return fmt.Errorf("document_parser.ocr_api_model 不能为空")
		}
	default:
		return fmt.Errorf("document_parser.ocr_engine 必须是 %s 或 %s", OCREngineRapidOCR, OCREngineAPI)
	}

	return nil
}

func validateHTTPURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("invalid url")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("invalid scheme")
	}
	return nil
}

// scriptCandidates 按发布包布局返回脚本的可能位置，从最可能到最不可能。
func scriptCandidates() []string {
	dir := executableDir()
	return []string{
		filepath.Join(dir, "python", scriptName),                          // 发布包：<程序目录>/python/
		filepath.Join(dir, "pkg", "documentparser", "python", scriptName), // 从项目根运行构建产物
		filepath.Join("pkg", "documentparser", "python", scriptName),      // 开发时工作目录即项目根
	}
}

func executableDir() string {
	executable, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(executable)
}

func userCacheDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "narra")
	}
	return filepath.Join(dir, "narra")
}

func firstExisting(paths []string) string {
	for _, path := range paths {
		if isFile(path) {
			return path
		}
	}
	return ""
}

func isFile(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func uvBinaryName() string {
	if runtime.GOOS == "windows" {
		return "uv.exe"
	}
	return "uv"
}
