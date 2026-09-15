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
	// Embedding defaults keep older configuration files valid while allowing
	// the service to remain opt-in via embedding.enabled.
	v.SetDefault("embedding.model", "BAAI/bge-m3")
	v.SetDefault("embedding.dimensions", BGEM3Dimensions)
	v.SetDefault("embedding.timeout", "30s")

	// 读取配置文件
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	// 解析配置
	config := &Config{}
	if err := v.Unmarshal(config); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

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
	if val := os.Getenv("LLM_API_KEY"); val != "" {
		config.LLM.APIKey = val
	}
	if val := os.Getenv("EMBEDDING_API_KEY"); val != "" {
		config.Embedding.APIKey = val
	}

	if err := config.Embedding.Validate(); err != nil {
		return nil, fmt.Errorf("embedding 配置无效: %w", err)
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
