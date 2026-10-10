package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

var globalConfig *Config

// Load 加载配置文件
func Load(configPath string) (*Config, error) {
	// 本地开发便利：存在 .env 时自动加载（当前目录找不到就向上找，最多 8 层——
	// go test / IDE 的工作目录常是子目录，也要能找到仓库根的那份）。
	// godotenv 不覆盖已存在的环境变量，CI / Docker Compose 显式注入的变量优先；
	// 找不到文件时静默跳过，线上不受影响。
	loadDotEnv()

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
	v.SetDefault("embedding.timeout", "120s")
	v.SetDefault("sms.region_id", "cn-hangzhou")
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
	v.SetDefault("document_parser.max_vlm_calls", 100)
	v.SetDefault("document_parser.python_version", "3.12")
	v.SetDefault("document_parser.ocr_engine", "rapidocr")
	v.SetDefault("storage.upload_dir", "data/uploads")
	v.SetDefault("storage.audio_dir", "data/audio")
	// 知识库图片是持久资产：解析产出的图片发布在这里，收录成功也不能删（见 documentimage）。
	v.SetDefault("storage.knowledge_dir", "data/knowledge")
	// 对象存储默认启用：storage.oss 四项填齐走 OSS，全部留空退回本地目录兜底
	// （装配层按 Configured 选择实现，启动日志会打印当前模式）；凭证建议用环境变量注入。
	v.SetDefault("storage.oss.timeout", "30s")
	// 知识库批量导入：一批最多 10 份、单份 16MB、整批 100MB；后台最多同时解析 2 篇，
	// 向量化全局串行（默认 1）。队列上限 100 同时是暂存盘的占用上限。
	v.SetDefault("knowledge_ingest.max_files", DefaultKnowledgeMaxFiles)
	v.SetDefault("knowledge_ingest.max_file_bytes", DefaultKnowledgeMaxFileBytes)
	v.SetDefault("knowledge_ingest.max_batch_bytes", DefaultKnowledgeMaxBatchBytes)
	v.SetDefault("knowledge_ingest.queue_capacity", DefaultKnowledgeQueueCapacity)
	v.SetDefault("knowledge_ingest.parse_concurrency", DefaultKnowledgeParseConcurrency)
	v.SetDefault("knowledge_ingest.embedding_concurrency", DefaultKnowledgeEmbeddingConcurrency)
	// 课程材料（首页随建课上传）未关联课堂时的保留时长；到期由 retention 清理。
	v.SetDefault("knowledge_ingest.material_ttl", DefaultKnowledgeMaterialTTL)
	// 上传是长请求：读超时必须留够把整批文件传完的时间，见 AppConfig.ReadTimeout。
	v.SetDefault("app.read_timeout", "5m")
	// 后台生成任务：默认串行、最多重试两次、单次不超过一刻钟、每十分钟对一次账。
	v.SetDefault("worker.concurrency", 1)
	v.SetDefault("worker.max_retry", 2)
	v.SetDefault("worker.timeout", "30m")
	v.SetDefault("worker.reconcile_interval", "10m")
	// 课堂生成：页面并发默认 3。调大之前先确认模型与 TTS 厂商的并发额度；
	// 两个值分开计数，语音合成不占模型的并发名额。
	v.SetDefault("classroom.page_concurrency", 3)
	v.SetDefault("classroom.tts_pool_size", 3)

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
	if val := os.Getenv("SMS_ENABLED"); val == "true" {
		config.SMS.Enabled = true
	}
	if val := os.Getenv("SMS_REGION_ID"); val != "" {
		config.SMS.RegionID = val
	}
	if val := os.Getenv("SMS_SIGN_NAME"); val != "" {
		config.SMS.SignName = val
	}
	if val := os.Getenv("SMS_TEMPLATE_CODE"); val != "" {
		config.SMS.TemplateCode = val
	}
	if val := os.Getenv("SMS_ACCESS_KEY_ID"); val != "" {
		config.SMS.AccessKeyID = val
	}
	if val := os.Getenv("SMS_ACCESS_KEY_SECRET"); val != "" {
		config.SMS.AccessKeySecret = val
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
	// OSS 凭证走环境变量优先：AK/SK 不该随着 config.yaml 进版本库，生产环境
	// 由部署侧注入即可。
	if val := os.Getenv("OSS_ACCESS_KEY_ID"); val != "" {
		config.Storage.OSS.AccessKeyID = val
	}
	if val := os.Getenv("OSS_ACCESS_KEY_SECRET"); val != "" {
		config.Storage.OSS.AccessKeySecret = val
	}

	// 连接与运行参数同样支持环境变量（容器里用服务名连库、按环境调级别，不必改 yaml）。
	if err := applyEnvOverrides(config); err != nil {
		return nil, err
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
	if err := config.Classroom.Validate(); err != nil {
		return nil, fmt.Errorf("课堂生成配置无效: %w", err)
	}
	if err := config.KnowledgeIngest.Validate(); err != nil {
		return nil, fmt.Errorf("知识库收录配置无效: %w", err)
	}
	if err := config.Storage.Validate(); err != nil {
		return nil, fmt.Errorf("存储配置无效: %w", err)
	}
	if err := config.Langfuse.Validate(); err != nil {
		return nil, fmt.Errorf("langfuse 配置无效: %w", err)
	}

	globalConfig = config
	return config, nil
}

// loadDotEnv 在当前目录及最多 8 层父目录中查找 .env 并加载，供本地开发使用。
// 从仓库根运行时第一层就命中；go test / IDE 的工作目录是子目录时会向上找到仓库根那份。
func loadDotEnv() {
	dir, err := os.Getwd()
	if err != nil {
		return
	}
	for depth := 0; depth < 8; depth++ {
		candidate := filepath.Join(dir, ".env")
		if _, statErr := os.Stat(candidate); statErr == nil {
			_ = godotenv.Load(candidate)
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		dir = parent
	}
}

// applyEnvOverrides 用环境变量覆盖部署相关的连接与运行参数。
// 只认显式设置且非空的变量；数值解析失败直接报错，避免"填错了却静默退回默认值"。
func applyEnvOverrides(cfg *Config) error {
	stringsToOverride := []struct {
		envKey string
		target *string
	}{
		{"POSTGRES_HOST", &cfg.Database.Postgres.Host},
		{"POSTGRES_USERNAME", &cfg.Database.Postgres.Username},
		{"POSTGRES_DATABASE", &cfg.Database.Postgres.Database},
		{"POSTGRES_SSLMODE", &cfg.Database.Postgres.SSLMode},
		{"REDIS_HOST", &cfg.Database.Redis.Host},
		{"APP_MODE", &cfg.App.Mode},
		{"LOG_LEVEL", &cfg.Log.Level},
	}
	for _, item := range stringsToOverride {
		if val := os.Getenv(item.envKey); val != "" {
			*item.target = val
		}
	}

	intsToOverride := []struct {
		envKey string
		target *int
	}{
		{"POSTGRES_PORT", &cfg.Database.Postgres.Port},
		{"REDIS_PORT", &cfg.Database.Redis.Port},
		{"REDIS_DB", &cfg.Database.Redis.DB},
		{"APP_PORT", &cfg.App.Port},
	}
	for _, item := range intsToOverride {
		val := strings.TrimSpace(os.Getenv(item.envKey))
		if val == "" {
			continue
		}
		n, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("环境变量 %s 需要是数字，当前值 %q", item.envKey, val)
		}
		*item.target = n
	}

	// app.mode 直接决定 gin 的模式（非法值会让 gin 在启动时 panic），提前拦住并给出明确报错。
	switch cfg.App.Mode {
	case "debug", "release", "test":
	default:
		return fmt.Errorf("app.mode 只能是 debug / release / test，当前值 %q", cfg.App.Mode)
	}
	return nil
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
