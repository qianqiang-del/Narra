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
	v.SetDefault("document_parser.python_version", "3.12")
	v.SetDefault("document_parser.ocr_engine", "rapidocr")
	v.SetDefault("storage.upload_dir", "data/uploads")

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

	if err := config.Embedding.Validate(); err != nil {
		return nil, fmt.Errorf("向量服务配置无效: %w", err)
	}
	if err := config.TTS.Validate(); err != nil {
		return nil, fmt.Errorf("tts 配置无效: %w", err)
	}
	if err := config.DocumentParser.Validate(); err != nil {
		return nil, fmt.Errorf("文档解析器配置无效: %w", err)
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
