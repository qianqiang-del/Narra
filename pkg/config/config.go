package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Config 应用配置结构体
type Config struct {
	App            AppConfig            `mapstructure:"app"`
	Database       DatabaseConfig       `mapstructure:"database"`
	Embedding      EmbeddingConfig      `mapstructure:"embedding"`
	TTS            TTSConfig            `mapstructure:"tts"`
	DocumentParser DocumentParserConfig `mapstructure:"document_parser"`
	Storage        StorageConfig        `mapstructure:"storage"`
	JWT            JWTConfig            `mapstructure:"jwt"`
	Log            LogConfig            `mapstructure:"log"`
	CORS           CORSConfig           `mapstructure:"cors"`
	Worker         WorkerConfig         `mapstructure:"worker"`
	ConfigPath     string               `mapstructure:"-"`
}

type StorageConfig struct {
	UploadDir string `mapstructure:"upload_dir"`
}

// WorkerConfig 是后台生成任务的执行配置。
type WorkerConfig struct {
	Concurrency       int           `mapstructure:"concurrency"`        // 同时处理的生成任务数
	MaxRetry          int           `mapstructure:"max_retry"`          // 单个任务最多重试几次
	Timeout           time.Duration `mapstructure:"timeout"`            // 单次执行超时
	ReconcileInterval time.Duration `mapstructure:"reconcile_interval"` // 周期对账间隔；0 表示只在对齐启动时对账一次
}

// Validate 校验后台任务配置。
func (c WorkerConfig) Validate() error {
	if c.Concurrency < 1 {
		return fmt.Errorf("worker.concurrency 必须大于 0")
	}
	if c.MaxRetry < 0 {
		return fmt.Errorf("worker.max_retry 不能为负")
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("worker.timeout 必须大于 0")
	}
	if c.ReconcileInterval < 0 {
		return fmt.Errorf("worker.reconcile_interval 不能为负")
	}
	return nil
}

// TTSProviderQwen 是 Qwen，走阿里云百炼；音色目录誊的就是它。
const TTSProviderQwen = "qwen"

// ttsSupportedProviders 是允许写进 tts.provider 的值。只有一个也照样做白名单。
var ttsSupportedProviders = []string{TTSProviderQwen}

// MCPServerConfig 是 MCP 模块内部使用的单个 server 连接配置。
type MCPServerConfig struct {
	ID               string        `mapstructure:"id"`
	Enabled          bool          `mapstructure:"enabled"`
	Required         bool          `mapstructure:"required"`
	Transport        string        `mapstructure:"transport"`
	Endpoint         string        `mapstructure:"endpoint"`
	APIKey           string        `mapstructure:"api_key"`
	AuthEnv          string        `mapstructure:"auth_env"`
	StartupTimeout   time.Duration `mapstructure:"startup_timeout"`
	DiscoveryTimeout time.Duration `mapstructure:"discovery_timeout"`
	CallTimeout      time.Duration `mapstructure:"call_timeout"`
}

// AppConfig 应用配置
type AppConfig struct {
	Name    string `mapstructure:"name"`
	Version string `mapstructure:"version"`
	Mode    string `mapstructure:"mode"` // debug, release, test
	Port    int    `mapstructure:"port"`
}

// DatabaseConfig 数据库配置
type DatabaseConfig struct {
	Postgres PostgresConfig `mapstructure:"postgres"`
	Redis    RedisConfig    `mapstructure:"redis"`
}

// PostgresConfig PostgreSQL 配置
type PostgresConfig struct {
	Host         string `mapstructure:"host"`
	Port         int    `mapstructure:"port"`
	Username     string `mapstructure:"username"`
	Password     string `mapstructure:"password"`
	Database     string `mapstructure:"database"`
	MaxIdleConns int    `mapstructure:"max_idle_conns"`
	MaxOpenConns int    `mapstructure:"max_open_conns"`
}

// RedisConfig Redis 配置
type RedisConfig struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
	PoolSize int    `mapstructure:"pool_size"`
}

// EmbeddingConfig 是兼容 OpenAI 协议的向量化服务配置。
type EmbeddingConfig struct {
	Enabled    bool          `mapstructure:"enabled"`    // 是否启用向量化能力
	APIKey     string        `mapstructure:"api_key"`    // 服务密钥；本地服务可为空，环境变量优先级更高
	BaseURL    string        `mapstructure:"base_url"`   // OpenAI 兼容服务根地址，启用时必填
	Model      string        `mapstructure:"model"`      // 模型 ID 或服务端部署别名
	Timeout    time.Duration `mapstructure:"timeout"`    // 单次向量化请求超时，启用时必须为正数
	Dimensions int           `mapstructure:"dimensions"` // 模型返回的向量维度，启用时必须为正数
}

// Validate 校验兼容 OpenAI 协议的 embedding 配置。
func (c EmbeddingConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if strings.TrimSpace(c.Model) == "" {
		return fmt.Errorf("向量模型名称不能为空")
	}
	if c.Dimensions <= 0 {
		return fmt.Errorf("向量维度必须大于 0")
	}

	baseURL, err := url.Parse(strings.TrimSpace(c.BaseURL))
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" ||
		(baseURL.Scheme != "http" && baseURL.Scheme != "https") {
		return fmt.Errorf("向量服务地址必须是有效的 HTTP 或 HTTPS 地址")
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("向量服务请求超时必须大于 0")
	}

	return nil
}

// TTSConfig 是语音合成服务的配置。

type TTSConfig struct {
	Enabled  bool   `mapstructure:"enabled"`  // 是否启用语音合成
	Provider string `mapstructure:"provider"` // 哪家 TTS，取值见 ttsSupportedProviders；启用时必填，故意不给默认值

	APIKey  string        `mapstructure:"api_key"`  // 服务密钥；配置文件与环境变量均可，环境变量优先
	BaseURL string        `mapstructure:"base_url"` // 服务根地址（不含各家自己的路径），启用时必填
	Model   string        `mapstructure:"model"`    // 合成模型 ID，如 qwen3-tts-flash；有默认值，见 loader.go
	Timeout time.Duration `mapstructure:"timeout"`  // 单次合成请求超时，启用时必须为正数
}

// Validate 校验语音合成配置，只在 enabled 为真时校验服务参数。
func (c TTSConfig) Validate() error {
	if !c.Enabled {
		return nil
	}

	provider := strings.TrimSpace(c.Provider)
	supported := false
	for _, p := range ttsSupportedProviders {
		if provider == p {
			supported = true
			break
		}
	}
	if !supported {
		return fmt.Errorf("tts.provider 必须是 %s 之一", strings.Join(ttsSupportedProviders, "、"))
	}

	if strings.TrimSpace(c.Model) == "" {
		return fmt.Errorf("tts.model 不能为空")
	}

	baseURL, err := url.Parse(strings.TrimSpace(c.BaseURL))
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" ||
		(baseURL.Scheme != "http" && baseURL.Scheme != "https") {
		return fmt.Errorf("tts.base_url 必须是有效的 http 或 https URL")
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("tts.timeout 必须大于 0")
	}

	return nil
}

// DocumentParserConfig 是文档解析器（本地 Python 子进程）的配置。
//
// 使用者机器上不需要预装 Python：解释器与依赖要么随发布包携带（runtime_dir），
// 要么首次运行时用 uv 自动准备（env_dir），要么由使用者指定已有环境（python_path）。
type DocumentParserConfig struct {
	Enabled        bool          `mapstructure:"enabled"`         // 是否启用文档解析能力
	PythonPath     string        `mapstructure:"python_path"`     // 已有解释器路径；留空则走三级自动解析
	RuntimeDir     string        `mapstructure:"runtime_dir"`     // 随包携带的运行时目录；留空取 <程序目录>/python-runtime
	EnvDir         string        `mapstructure:"env_dir"`         // uv 现建的环境目录；留空取 <用户缓存>/narra/documentparser-env
	ScriptPath     string        `mapstructure:"script_path"`     // parse_document.py 路径；留空自动查找
	Requirements   string        `mapstructure:"requirements"`    // 依赖清单；留空取脚本同目录的 requirements.txt
	UVPath         string        `mapstructure:"uv_path"`         // uv 路径；留空按 随包目录 → 程序目录 → PATH 查找
	PythonVersion  string        `mapstructure:"python_version"`  // uv 要准备的解释器版本，如 3.12
	IndexURL       string        `mapstructure:"index_url"`       // PyPI 镜像；国内首次准备依赖时建议配置
	Timeout        time.Duration `mapstructure:"timeout"`         // 单次解析超时
	PrepareTimeout time.Duration `mapstructure:"prepare_timeout"` // 首次环境准备（下载解释器与依赖）的墙钟上限
	MaxOCRPages    int           `mapstructure:"max_ocr_pages"`   // 单次解析允许 OCR 的页数上限
	OCREngine      string        `mapstructure:"ocr_engine"`      // rapidocr（本地，默认）或 api（会把图片外发）
	OCRAPIBaseURL  string        `mapstructure:"ocr_api_base_url"`
	OCRAPIKey      string        `mapstructure:"ocr_api_key"`
	OCRAPIModel    string        `mapstructure:"ocr_api_model"`
	WorkDir        string        `mapstructure:"work_dir"` // 图片导出根目录；留空用系统临时目录
}

// documentParserOCREngines 是允许写进 document_parser.ocr_engine 的值。只有一个也照样做白名单。
var documentParserOCREngines = []string{"rapidocr", "api"}

// Validate 校验文档解析器配置，只在 enabled 为真时校验具体参数。
func (c DocumentParserConfig) Validate() error {
	if !c.Enabled {
		return nil
	}

	engine := strings.TrimSpace(c.OCREngine)
	supported := false
	for _, candidate := range documentParserOCREngines {
		if engine == candidate {
			supported = true
			break
		}
	}
	if !supported {
		return fmt.Errorf("document_parser.ocr_engine 必须是 %s 之一", strings.Join(documentParserOCREngines, "、"))
	}

	// 选 api 就意味着文档内容会离开本机，所以地址和模型必须显式填全，不给默认值兜底。
	if engine == "api" {
		apiURL, err := url.Parse(strings.TrimSpace(c.OCRAPIBaseURL))
		if err != nil || apiURL.Scheme == "" || apiURL.Host == "" ||
			(apiURL.Scheme != "http" && apiURL.Scheme != "https") {
			return fmt.Errorf("document_parser.ocr_api_base_url 必须是有效的 http 或 https URL")
		}
		if strings.TrimSpace(c.OCRAPIModel) == "" {
			return fmt.Errorf("document_parser.ocr_api_model 不能为空")
		}
	}

	if c.Timeout <= 0 {
		return fmt.Errorf("document_parser.timeout 必须大于 0")
	}
	if c.PrepareTimeout <= 0 {
		return fmt.Errorf("document_parser.prepare_timeout 必须大于 0")
	}
	if c.MaxOCRPages <= 0 {
		return fmt.Errorf("document_parser.max_ocr_pages 必须大于 0")
	}

	return nil
}

// JWTConfig JWT 配置
type JWTConfig struct {
	Secret      string        `mapstructure:"secret"`
	ExpireHours time.Duration `mapstructure:"expire_hours"`
}

// LogConfig 日志配置
type LogConfig struct {
	Level      string `mapstructure:"level"`       // debug, info, warn, error
	Filename   string `mapstructure:"filename"`    // 日志文件路径
	MaxSize    int    `mapstructure:"max_size"`    // 单个日志文件最大大小(MB)
	MaxBackups int    `mapstructure:"max_backups"` // 保留的旧日志文件数量
	MaxAge     int    `mapstructure:"max_age"`     // 保留旧日志文件的最大天数
	Compress   bool   `mapstructure:"compress"`    // 是否压缩
}

// CORSConfig CORS 配置
type CORSConfig struct {
	Enabled          bool     `mapstructure:"enabled"`
	AllowOrigins     []string `mapstructure:"allow_origins"`
	AllowMethods     []string `mapstructure:"allow_methods"`
	AllowHeaders     []string `mapstructure:"allow_headers"`
	ExposeHeaders    []string `mapstructure:"expose_headers"`
	AllowCredentials bool     `mapstructure:"allow_credentials"`
	MaxAge           int      `mapstructure:"max_age"`
}
