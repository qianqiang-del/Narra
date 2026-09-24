package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"
)

var globalConfig *Config

// Load 加载配置文件
func Load(configPath string) (*Config, error) {
	v := viper.New()

	// 设置配置文件路径
	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath("./configs")
		v.AddConfigPath(".")
	}

	// 环境变量前缀
	v.SetEnvPrefix("NARRA")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	// Embedding 默认不启用。模型与向量维度因服务商而异，启用时必须显式配置。
	v.SetDefault("embedding.timeout", "30s")
	// TTS 同理。provider 故意不给默认值：它决定客户端走哪种协议，写错了要到合成那一步才炸。
	v.SetDefault("tts.model", "qwen3-tts-flash")
	v.SetDefault("tts.timeout", "60s")
	// 文档解析：解释器与依赖由 Go 侧自动准备（见 pkg/documentparser）。脚本自身没有超时控制，
	// 而 CPU 上跑版面模型是分钟级的，所以这里必须给足；OCR 默认走本地，不外发文档内容。
	v.SetDefault("document_parser.timeout", "10m")
	// 准备环境（uv 下载解释器与依赖）单独计时，不蹭解析那 10 分钟：正常安装就要十几分钟。
	// 它必须有值 —— 没上限的 uv pip install 卡住会把 worker 整轮调度停摆，且上传入口永久 409。
	v.SetDefault("document_parser.prepare_timeout", "20m")
	// OCR 是逐页推理：页数不设限时，一份几百页的扫描件会把唯一的 worker 占满整个解析预算
	// （期间上传入口 409），而且大概率撞超时、重试又从第 1 页重来。超限快速失败。
	v.SetDefault("document_parser.max_ocr_pages", 100)
	v.SetDefault("document_parser.python_version", "3.12")
	v.SetDefault("document_parser.ocr_engine", "rapidocr")
	v.SetDefault("storage.upload_dir", "data/uploads")
	v.SetDefault("storage.audio_dir", "data/audio")
	// 后台生成任务：默认串行、最多重试两次、单次不超过一刻钟、每十分钟对一次账。
	v.SetDefault("worker.concurrency", 1)
	v.SetDefault("worker.max_retry", 2)
	v.SetDefault("worker.timeout", "30m")
	v.SetDefault("worker.reconcile_interval", "10m")

	// 读取配置文件
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	// 解析配置
	config := &Config{}
	if err := v.Unmarshal(config); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}
	config.ConfigPath = v.ConfigFileUsed()

	// 从环境变量覆盖敏感配置
	if val := os.Getenv("POSTGRES_PASSWORD"); val != "" {
		config.Database.Postgres.Password = val
	}
	if val := os.Getenv("REDIS_PASSWORD"); val != "" {
		config.Database.Redis.Password = val
	}
	if val := os.Getenv("JWT_SECRET"); val != "" {
		config.JWT.Secret = val
	}
	if val := os.Getenv("EMBEDDING_API_KEY"); val != "" {
		config.Embedding.APIKey = val
	}
	if val := os.Getenv("TTS_API_KEY"); val != "" {
		config.TTS.APIKey = val
	}
	if val := os.Getenv("DOCUMENT_PARSER_OCR_API_KEY"); val != "" {
		config.DocumentParser.OCRAPIKey = val
	}
	if val := os.Getenv("LANGFUSE_PUBLIC_KEY"); val != "" {
		config.Langfuse.PublicKey = val
	}
	if val := os.Getenv("LANGFUSE_SECRET_KEY"); val != "" {
		config.Langfuse.SecretKey = val
	}

	if err := config.Embedding.Validate(); err != nil {
		return nil, fmt.Errorf("向量服务配置无效: %w", err)
	}
	if err := config.TTS.Validate(); err != nil {
		return nil, fmt.Errorf("tts 配置无效: %w", err)
	}
	if err := config.DocumentParser.Validate(); err != nil {
		return nil, fmt.Errorf("文档解析器配置无效: %w", err)
	}
	if err := config.Worker.Validate(); err != nil {
		return nil, fmt.Errorf("后台任务配置无效: %w", err)
	}
	if err := config.Langfuse.Validate(); err != nil {
		return nil, fmt.Errorf("langfuse 配置无效: %w", err)
	}

	globalConfig = config
	return config, nil
}

// Get 获取全局配置
func Get() *Config {
	if globalConfig == nil {
		panic("配置未初始化，请先调用 Load() 加载配置")
	}
	return globalConfig
}

// MustLoad 加载配置，失败时 panic
func MustLoad(configPath string) *Config {
	config, err := Load(configPath)
	if err != nil {
		panic(err)
	}
	return config
}
