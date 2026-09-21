package documentparser

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// markerFileName 记录"当前环境是按哪份依赖清单建出来的"。
//
// 依赖清单变了就必须重建，否则会留下"代码升级了、依赖还是旧的"这类最难排查的问题。
const markerFileName = ".narra-requirements.sha256"

// importProbe 是运行时自检要执行的语句。
//
// 声明成变量而不是常量，是为了让测试能把它换成一定能过的语句（测试环境不该依赖真实
// 装好的 docling）。生产路径上不会改它。
//
// 探的是**真正要用的那个入口**，而不是逐个 import 顶层包：docling 存在"顶层能过、
// 子模块才炸"的缺失 —— `import docling` 一路正常，直到 document_converter 这条链走到
// docling.models.base_ocr_model（它需要 scipy / rtree）才失败，而那时用户文档已经在上传
// 路径上了。探针指到这里，这类缺失就只会在准备阶段以明确错误暴露出来。
var importProbe = "import fitz; from docling.document_converter import DocumentConverter"

// Runtime 描述一次解析实际使用的解释器。
type Runtime struct {
	Python string // 解释器可执行文件绝对路径
	Source string // 来源：configured / bundled / provisioned
	Dir    string // 所属目录
}

// Resolver 在"使用者机器上不需要预装 Python"的前提下，找到或准备出一个可用解释器。
//
// 三级回退，顺序固定：
//  1. 配置里的 python_path —— 企业内已有环境、或开发机调试；
//  2. 随发布包携带的独立运行时（<程序目录>/python-runtime）—— 离线模式；
//  3. 用 uv 现场建的环境（<用户缓存>/narra/documentparser-env）—— 在线模式，首次运行下载依赖。
//
// 三条都不通时返回 CodeRuntimeUnavailable，错误信息里带修复动作。
type Resolver struct {
	cfg         Config
	runner      commandRunner
	lookPath    func(string) (string, error)
	allowUserUV bool
}

// NewResolver 构造解析器；配置里的空值会先按默认布局补全。
func NewResolver(cfg Config) *Resolver {
	return &Resolver{
		cfg:         cfg.WithDefaults(),
		runner:      osRunner{},
		lookPath:    exec.LookPath,
		allowUserUV: true,
	}
}

// Resolve 返回可用的运行时，必要时在此处完成环境准备（首次可能耗时数分钟）。
//
// onProgress 可以为 nil；准备过程的关键步骤都会通过它上报，便于前端展示进度。
func (r *Resolver) Resolve(ctx context.Context, onProgress func(string)) (*Runtime, error) {
	report := func(message string) {
		if onProgress != nil {
			onProgress(message)
		}
	}

	if explicit := strings.TrimSpace(r.cfg.PythonPath); explicit != "" {
		python, err := filepath.Abs(explicit)
		if err != nil {
			python = explicit
		}
		if !isFile(python) {
			return nil, &Error{
				Code:    CodeRuntimeUnavailable,
				Message: fmt.Sprintf("配置的 python_path 不存在: %s", python),
			}
		}
		if err := r.verify(ctx, python); err != nil {
			return nil, err
		}
		return &Runtime{Python: python, Source: "configured", Dir: filepath.Dir(python)}, nil
	}

	if python := findPythonIn(r.cfg.RuntimeDir); python != "" {
		report("使用随发布包携带的 Python 运行时")
		if err := r.verify(ctx, python); err != nil {
			return nil, err
		}
		return &Runtime{Python: python, Source: "bundled", Dir: r.cfg.RuntimeDir}, nil
	}

	if python := findPythonIn(r.cfg.EnvDir); python != "" && r.markerMatches() {
		if err := r.verify(ctx, python); err != nil {
			return nil, err
		}
		return &Runtime{Python: python, Source: "provisioned", Dir: r.cfg.EnvDir}, nil
	}

	// 走到这里有两种情况：环境还不存在，或者环境在但与当前依赖清单不符 —— 后者一样要重建，
	// 所以文案不能说成"未找到"。
	report("开始准备文档解析环境（首次运行需要下载依赖，可能耗时数分钟）")
	if err := r.provision(ctx, report); err != nil {
		return nil, err
	}

	python := findPythonIn(r.cfg.EnvDir)
	if python == "" {
		return nil, &Error{
			Code:    CodeRuntimeUnavailable,
			Message: fmt.Sprintf("环境准备结束但没找到解释器，请检查目录: %s", r.cfg.EnvDir),
		}
	}
	if err := r.verify(ctx, python); err != nil {
		return nil, err
	}
	return &Runtime{Python: python, Source: "provisioned", Dir: r.cfg.EnvDir}, nil
}

// verify 自检关键依赖能否导入。
//
// 放在准备阶段做，是为了让"依赖缺失"在启动或首次调用时就以明确错误暴露出来，
// 而不是等用户传了一份文档才炸出一个看不懂的 ImportError。
func (r *Resolver) verify(ctx context.Context, python string) error {
	_, stderr, err := r.runner.Run(ctx, "", nil, python, "-c", importProbe)
	if err == nil {
		return nil
	}
	return &Error{
		Code:    CodeRuntimeUnavailable,
		Message: fmt.Sprintf("Python 环境不可用（%s 失败），解释器: %s", importProbe, python),
		Stderr:  tail(string(stderr), stderrTailBytes),
	}
}

// provision 用 uv 现场准备环境：下载解释器 → 建 venv → 装依赖 → 落就绪标记。
//
// 解释器装在自家目录（UV_PYTHON_INSTALL_DIR），不碰系统 PATH，也不影响使用者机器上
// 已有的任何 Python —— 卸载时删掉这一个目录即可。
func (r *Resolver) provision(ctx context.Context, report func(string)) error {
	requirements, err := os.ReadFile(r.cfg.Requirements)
	if err != nil {
		return &Error{
			Code:    CodeRuntimeUnavailable,
			Message: fmt.Sprintf("读不到依赖清单 %s: %v", r.cfg.Requirements, err),
		}
	}

	uv, err := r.findUV()
	if err != nil {
		return err
	}

	base := []string{"UV_PYTHON_INSTALL_DIR=" + filepath.Join(filepath.Dir(r.cfg.EnvDir), "python")}
	if mirror := strings.TrimSpace(r.cfg.IndexURL); mirror != "" {
		base = append(base, "UV_INDEX_URL="+mirror)
	}

	report(fmt.Sprintf("准备 Python %s 解释器", r.cfg.PythonVersion))
	if err := r.run(ctx, uv, base, "python", "install", r.cfg.PythonVersion); err != nil {
		return err
	}

	report(fmt.Sprintf("创建虚拟环境 %s", r.cfg.EnvDir))
	// --clear：能走到这一步，说明现成环境要么不存在、要么与当前依赖清单不符，那它就该被
	// 重建而不是复用 —— 就绪标记的语义正是"这个环境是按这份清单建的"，只有清空重建才能
	// 让这句话为真。不加这个 flag 时 uv 对已存在的目录直接报错退出，于是"改了依赖清单 →
	// 自动重建环境"这条自愈路径根本走不通（只剩手工删目录一条路）。
	if err := r.run(ctx, uv, base, "venv", "--python", r.cfg.PythonVersion, "--clear", r.cfg.EnvDir); err != nil {
		return err
	}

	python := findPythonIn(r.cfg.EnvDir)
	if python == "" {
		return &Error{
			Code:    CodeRuntimeUnavailable,
			Message: "uv 创建虚拟环境后未找到解释器: " + r.cfg.EnvDir,
		}
	}

	report("安装文档解析依赖（首次较慢）")
	if err := r.run(ctx, uv, base, "pip", "install", "--python", python, "-r", r.cfg.Requirements); err != nil {
		return err
	}

	if err := writeMarker(r.cfg.EnvDir, requirements); err != nil {
		return &Error{Code: CodeRuntimeUnavailable, Message: "写入就绪标记失败: " + err.Error()}
	}
	report("文档解析环境准备完成")
	return nil
}

// run 执行一条准备命令，失败时把 stderr 尾巴带上，方便定位是网络还是依赖冲突。
func (r *Resolver) run(ctx context.Context, name string, env []string, args ...string) error {
	_, stderr, err := r.runner.Run(ctx, "", env, name, args...)
	if err == nil {
		return nil
	}
	return &Error{
		Code:    CodeRuntimeUnavailable,
		Message: fmt.Sprintf("执行 %s %s 失败: %v", filepath.Base(name), strings.Join(args, " "), err),
		Stderr:  tail(string(stderr), stderrTailBytes),
	}
}

// findUV 依次在 配置 → 随包目录 → 程序目录 → PATH 里找 uv。
func (r *Resolver) findUV() (string, error) {
	candidates := make([]string, 0, 5)
	if path := strings.TrimSpace(r.cfg.UVPath); path != "" {
		candidates = append(candidates, path)
	}
	if r.cfg.RuntimeDir != "" {
		candidates = append(candidates, filepath.Join(r.cfg.RuntimeDir, uvBinaryName()))
	}
	candidates = append(candidates, filepath.Join(executableDir(), uvBinaryName()))
	if r.allowUserUV {
		if home, err := os.UserHomeDir(); err == nil {
			candidates = append(candidates,
				filepath.Join(home, ".local", "bin", uvBinaryName()),
				filepath.Join(home, "AppData", "Local", "uv", "bin", uvBinaryName()),
			)
		}
	}

	for _, candidate := range candidates {
		if isFile(candidate) {
			return candidate, nil
		}
	}
	if path, err := r.lookPath(uvBinaryName()); err == nil {
		return path, nil
	}

	return "", &Error{
		Code: CodeRuntimeUnavailable,
		Message: fmt.Sprintf(
			"自动准备 Python 环境需要 uv，但没找到它。三种解法：把 %s 放到 %s；配置 document_parser.uv_path 指向它；或配置 document_parser.python_path 指向已有的 Python 环境",
			uvBinaryName(), executableDir()),
	}
}

// markerMatches 判断现成环境是否就是按当前依赖清单建的。
func (r *Resolver) markerMatches() bool {
	requirements, err := os.ReadFile(r.cfg.Requirements)
	if err != nil {
		return false
	}
	recorded, err := os.ReadFile(filepath.Join(r.cfg.EnvDir, markerFileName))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(recorded)) == requirementsDigest(requirements)
}

func requirementsDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func writeMarker(envDir string, requirements []byte) error {
	return os.WriteFile(filepath.Join(envDir, markerFileName), []byte(requirementsDigest(requirements)), 0o644)
}

// findPythonIn 在目录里找解释器，同时兼容 venv 与 python-build-standalone 两种布局。
func findPythonIn(dir string) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}

	var candidates []string
	if runtime.GOOS == "windows" {
		candidates = []string{
			filepath.Join(dir, "python.exe"),            // uv 建的 venv（根目录也有解释器）
			filepath.Join(dir, "Scripts", "python.exe"), // 标准 venv 布局
			filepath.Join(dir, "python", "python.exe"),  // python-build-standalone 解包后
		}
	} else {
		candidates = []string{
			filepath.Join(dir, "bin", "python3"),
			filepath.Join(dir, "python", "bin", "python3"),
			filepath.Join(dir, "bin", "python"),
		}
	}

	for _, candidate := range candidates {
		if isFile(candidate) {
			return candidate
		}
	}
	return ""
}
