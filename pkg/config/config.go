package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Config 应用配置结构体
type Config struct {
	App       AppConfig       `mapstructure:"app"`
	Database  DatabaseConfig  `mapstructure:"database"`
	LLM       LLMConfig       `mapstructure:"llm"`
	Embedding EmbeddingConfig `mapstructure:"embedding"`
	JWT       JWTConfig       `mapstructure:"jwt"`
	Log       LogConfig       `mapstructure:"log"`
	CORS      CORSConfig      `mapstructure:"cors"`
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

// LLMConfig 大模型配置，喂给 Eino 的 ChatModel。
//
// 平铺而不是 provider 列表，是因为 V1 只用一家：OpenAI、DeepSeek、通义走的都是
// OpenAI 兼容协议，换一家只需改 BaseURL 和 Model。将来真要多厂商并存、按课程选模型时，
// 再改成分组结构，并把选中的 provider 记进 classrooms.generation_config（§4.2）。
//
// APIKey 不要写进配置文件：走环境变量 LLM_API_KEY 覆盖（见 loader.go）。
type LLMConfig struct {
	APIKey  string        `mapstructure:"api_key"`  // 密钥
	BaseURL string        `mapstructure:"base_url"` // 接口地址，如 https://api.openai.com/v1
	Model   string        `mapstructure:"model"`    // 模型 ID，如 gpt-4o-mini
	Timeout time.Duration `mapstructure:"timeout"`  // 单次请求超时
}

// BGEM3Dimensions 是 BGE-M3 dense embedding 的固定向量维度。
const BGEM3Dimensions = 1024

// EmbeddingConfig 是 BGE-M3 向量化模型的配置。
//
// 它与 LLMConfig 分开，避免聊天模型与 embedding 模型混用端点、密钥和超时。
// APIKey 只允许通过环境变量 EMBEDDING_API_KEY 注入，禁止写入 YAML 配置文件。
type EmbeddingConfig struct {
	Enabled    bool          `mapstructure:"enabled"`    // 是否启用 BGE-M3 向量化能力
	APIKey     string        `mapstructure:"-"`          // 服务密钥，仅由环境变量注入；本地服务可为空
	BaseURL    string        `mapstructure:"base_url"`   // BGE-M3 服务根地址，启用时必填
	Model      string        `mapstructure:"model"`      // 模型 ID 或服务端部署别名
	Timeout    time.Duration `mapstructure:"timeout"`    // 单次向量化请求超时，启用时必须为正数
	Dimensions int           `mapstructure:"dimensions"` // BGE-M3 dense 向量维度，固定为 1024
}

// Validate 校验 BGE-M3 embedding 配置。
func (c EmbeddingConfig) Validate() error {
	if strings.TrimSpace(c.Model) == "" {
		return fmt.Errorf("embedding.model 不能为空")
	}
	if c.Dimensions != BGEM3Dimensions {
		return fmt.Errorf("embedding.dimensions 必须为 %d", BGEM3Dimensions)
	}
	if !c.Enabled {
		return nil
	}

	baseURL, err := url.Parse(strings.TrimSpace(c.BaseURL))
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" ||
		(baseURL.Scheme != "http" && baseURL.Scheme != "https") {
		return fmt.Errorf("embedding.base_url 必须是有效的 http 或 https URL")
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("embedding.timeout 必须大于 0")
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
