package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"narra/internal/agent/classroom"
	"narra/internal/api"
	"narra/internal/bootstrap"
	internalmcp "narra/internal/mcp"
	"narra/internal/model/entity"
	"narra/internal/rag"
	"narra/internal/repository"
	"narra/internal/service"
	"narra/pkg/config"
	"narra/pkg/crypto"
	"narra/pkg/database"
	"narra/pkg/documentparser"
	"narra/pkg/embedding"
	"narra/pkg/logger"
	"narra/pkg/tts"
)

// App 应用结构体
type App struct {
	cfg             *config.Config
	postgresDB      *gorm.DB
	redis           *redis.Client
	router          *api.Router
	server          *http.Server
	mcpManager      *internalmcp.Manager
	worker          *bootstrap.WorkerRuntime
	knowledgeWorker *rag.Worker
}

// NewApp 创建应用实例
func NewApp() *App {
	return &App{}
}

// Initialize 初始化应用
func (a *App) Initialize(configPath string) error {
	// 1. 加载配置
	if err := a.initConfig(configPath); err != nil {
		return err
	}

	// 2. 初始化日志
	if err := a.initLogger(); err != nil {
		return err
	}

	// 3. 初始化数据库
	if err := a.initDatabase(); err != nil {
		return err
	}

	// 4. 初始化依赖
	if err := a.initDependencies(); err != nil {
		return err
	}

	// 5. 初始化路由
	a.initRouter()

	// 6. 初始化服务器
	a.initServer()

	return nil
}

// initConfig 加载配置
func (a *App) initConfig(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}
	a.cfg = cfg
	return nil
}

// initLogger 初始化日志
func (a *App) initLogger() error {
	if err := logger.Init(&a.cfg.Log); err != nil {
		return fmt.Errorf("日志初始化失败: %w", err)
	}

	// 打印启动横幅
	logger.Info("=========================================")
	logger.Info(fmt.Sprintf("欢迎使用 %s", a.cfg.App.Name))
	logger.Info(fmt.Sprintf("版本: %s", a.cfg.App.Version))
	logger.Info(fmt.Sprintf("模式: %s", a.cfg.App.Mode))
	logger.Info("配置加载成功")
	logger.Info("=========================================")

	return nil
}

// initDatabase 初始化数据库
func (a *App) initDatabase() error {
	// 初始化 PostgreSQL
	postgresDB, err := database.InitPostgres(&a.cfg.Database.Postgres)
	if err != nil {
		return fmt.Errorf("PostgreSQL 初始化失败: %w", err)
	}
	a.postgresDB = postgresDB

	// pgvector 扩展必须先于 AutoMigrate 建好：knowledge_embeddings.embedding 的列类型是
	// vector，扩展不存在时建表直接失败（SQLSTATE 42704）、服务起不来。
	// vector 是数据库级扩展，幂等，重复执行无代价。
	if err := a.postgresDB.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error; err != nil {
		return fmt.Errorf("创建 vector 扩展失败: %w", err)
	}

	// 建表与全部约束（CHECK / 外键 / 唯一 / 索引，含带 WHERE 谓词的部分索引）都由
	// AutoMigrate 完成，声明在实体字段的 tag 上；migrations/ 只剩种子数据一个文件，
	// 见 migrations/README.md。
	//
	// 下面的顺序保持被引用表在前（GORM 的 ReorderModels 也会再排一次，双保险）：
	// 例如 PresetAgent 必须排在 ClassroomAgent 之前，前者的表先存在，后者的外键才建得出。
	logger.Info("开始数据库迁移...")
	if err := a.postgresDB.AutoMigrate(
		&entity.Folder{},
		&entity.Classroom{},
		&entity.PresetAgent{},
		&entity.ClassroomAgent{},
		&entity.Scene{},
		&entity.SceneSegment{},
		&entity.EmbeddingModel{},
		&entity.EmbeddingSetting{},
		&entity.KnowledgeDocument{},
		&entity.KnowledgeUploadRecord{},
		&entity.KnowledgeChunk{},
		&entity.KnowledgeEmbedding{},
		&entity.ClassroomConversation{},
		&entity.ConversationMessage{},
		&entity.ContextCompaction{},
		&entity.OrchestrationRun{},
		&entity.AgentTurn{},
		&entity.SharedContextMemory{},
		&entity.ConversationEvent{},
		&entity.AgentTraceSpan{},
		&entity.MCPServer{},
		&entity.LLMProvider{},
	); err != nil {
		return fmt.Errorf("数据库迁移失败: %w", err)
	}
	logger.Info("数据库表结构迁移完成")

	// 初始化 Redis（可选，失败不影响核心功能）
	rs, err := database.InitRedis(&a.cfg.Database.Redis)
	if err != nil {
		logger.Warn("Redis 初始化失败，将不影响核心功能", zap.Error(err))
	}
	a.redis = rs

	return nil
}

// initDependencies 初始化依赖注入
//
// 顺序是 db → repository → service → router，每一层只拿到它下面那一层。
// 数据库连接在 initDatabase 里已经建好，这里只往下传。
func (a *App) initDependencies() error {
	// ========== 创建 Repository ==========
	roleRepo := repository.NewRoleRepository(a.postgresDB)
	embeddingSettingRepo := repository.NewEmbeddingSettingRepository(a.postgresDB)
	embeddingModelRepo := repository.NewEmbeddingModelRepository(a.postgresDB)
	mcpServerRepo := repository.NewMCPServerRepository(a.postgresDB)
	llmProviderRepo := repository.NewLLMProviderRepository(a.postgresDB)
	classroomRepo := repository.NewClassroomRepository(a.postgresDB)
	sceneRepo := repository.NewSceneRepository(a.postgresDB)
	sceneSegmentRepo := repository.NewSceneSegmentRepository(a.postgresDB)
	knowledgeDocumentRepo := repository.NewKnowledgeDocumentRepository(a.postgresDB)
	knowledgeUploadRecordRepo := repository.NewKnowledgeUploadRecordRepository(a.postgresDB)

	// ========== 创建 Service ==========
	roleSvc := service.NewRoleService(roleRepo)
	embeddingManager := embedding.NewManager(a.cfg.Embedding)
	embeddingSettingSvc := service.NewEmbeddingSettingService(embeddingSettingRepo, embeddingModelRepo, embeddingManager, a.cfg.JWT.Secret)
	if err := embeddingSettingSvc.LoadActive(context.Background()); err != nil {
		return err
	}

	// TTS 没启用或配置不全时客户端留 nil：音色列表照常可用，只有试听会返回一句明确的
	// 错误。这里不 fail-fast，是因为试听是附加能力，不该拦住整个服务启动。
	var ttsClient *tts.Client
	if a.cfg.TTS.Enabled {
		client, err := tts.NewClient(a.cfg.TTS)
		if err != nil {
			logger.Warn("语音合成客户端初始化失败，试听功能不可用", zap.Error(err))
		} else {
			ttsClient = client
		}
	}
	voiceSvc := service.NewVoiceService(ttsClient)
	encryptionKey := crypto.DeriveKey(a.cfg.JWT.Secret)
	a.mcpManager = internalmcp.NewManager(a.cfg.App)
	mcpServerSvc := service.NewMCPServerService(mcpServerRepo, a.cfg.App, encryptionKey, a.mcpManager)
	if err := mcpServerSvc.LoadRuntime(context.Background()); err != nil {
		return fmt.Errorf("MCP 初始化失败: %w", err)
	}

	// ========== 创建 Router ==========
	// 收录链路归 internal/rag，服务层只做 DTO 映射与文档查询。与 MCP 同一种装法：
	// 运行时模块（rag.Ingester / mcp.Manager）在这里建好，再作为依赖注入服务层。
	// 文档与上传记录是两个仓储：前者是资产，后者是投递历史，表也不同。
	parser := newDocumentParser(a.cfg)
	knowledgeIngester := rag.NewIngester(
		knowledgeDocumentRepo, knowledgeUploadRecordRepo, embeddingModelRepo, embeddingManager, parser)
	uploadDir := a.cfg.Storage.UploadDir
	if uploadDir == "" {
		uploadDir = "data/uploads"
	}
	// 统一成绝对路径再往三处注入（controller / service / worker）。
	// 否则默认的 "data/uploads" 会原样记进 metadata.upload_path，而读取、重试与清理
	// 都按"当时的 cwd"解析 —— 换个工作目录启动，失败原件就找不到了：
	// 重试报"原件不在"，删除时的清理也会静默失效（文件永远留在磁盘上）。
	uploadDir, err := filepath.Abs(uploadDir)
	if err != nil {
		return fmt.Errorf("解析上传目录的绝对路径失败: %w", err)
	}
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		return fmt.Errorf("创建上传目录失败: %w", err)
	}
	// 文件收录跑在后台 worker 里：上传接口只建 pending 行，解析与向量化由它按秒轮询推进。
	// 并发固定为 1 —— 收录同时吃 CPU 和上游额度，MVP 阶段串行跑更容易定位问题；
	// uploadDir 必须和上面给 controller、service 的是同一个值，否则删除时的暂存清理会静默失效。
	a.knowledgeWorker = rag.NewWorker(knowledgeDocumentRepo, knowledgeIngester, uploadDir, 1)
	a.knowledgeWorker.Start()
	knowledgeSvc := service.NewKnowledgeService(knowledgeDocumentRepo, knowledgeUploadRecordRepo, knowledgeIngester, uploadDir)
	llmProviderSvc := service.NewLLMProviderService(llmProviderRepo, encryptionKey)

	// ========== 课堂受理 + 生成任务 ==========
	classroomDeps := classroom.Deps{
		Providers:     llmProviderRepo,
		Classrooms:    classroomRepo,
		Scenes:        sceneRepo,
		Segments:      sceneSegmentRepo,
		Tools:         a.mcpManager,
		EncryptionKey: encryptionKey,
	}
	queue, workerRuntime, err := bootstrap.BuildWorker(classroomDeps, a.cfg)
	if err != nil {
		return err
	}
	a.worker = workerRuntime
	classroomSvc := service.NewClassroomService(classroomRepo, llmProviderSvc, queue)

	// 对账：队列里已不会继续处理的 generating 课程，归档的判失败、丢了的重投。
	if err := bootstrap.ReconcileGenerating(context.Background(), classroomDeps, workerRuntime, queue); err != nil {
		return err
	}

	a.router = api.NewRouter(roleSvc, embeddingSettingSvc, voiceSvc, mcpServerSvc, llmProviderSvc, classroomSvc, knowledgeSvc, uploadDir, parser)
	return nil
}

// newDocumentParser 按配置创建文档解析器；没启用时返回 nil。
//
// 这里刻意不 fail-fast。解析器负责的是 PDF / Office 这类"需要真解析"的格式，
// 它起不来（脚本缺失、依赖没装好）时，md 与 txt 仍然可以正常导入，
// 整个服务更不该因此启动失败 —— 启动被拦住的后果是用户连设置页都进不去，反而修不了。
// 真需要它却拿不到时，用户会在上传那份 PDF 时收到一句明确说明。
func newDocumentParser(cfg *config.Config) documentparser.Parser {
	if !cfg.DocumentParser.Enabled {
		return nil
	}

	parser, err := documentparser.NewPythonParser(documentparser.Config{
		Enabled:        true,
		PythonPath:     cfg.DocumentParser.PythonPath,
		RuntimeDir:     cfg.DocumentParser.RuntimeDir,
		EnvDir:         cfg.DocumentParser.EnvDir,
		ScriptPath:     cfg.DocumentParser.ScriptPath,
		Requirements:   cfg.DocumentParser.Requirements,
		UVPath:         cfg.DocumentParser.UVPath,
		PythonVersion:  cfg.DocumentParser.PythonVersion,
		IndexURL:       cfg.DocumentParser.IndexURL,
		ParseTimeout:   cfg.DocumentParser.Timeout,
		PrepareTimeout: cfg.DocumentParser.PrepareTimeout,
		MaxOCRPages:    cfg.DocumentParser.MaxOCRPages,
		OCREngine:      cfg.DocumentParser.OCREngine,
		OCRAPIBaseURL:  cfg.DocumentParser.OCRAPIBaseURL,
		OCRAPIKey:      cfg.DocumentParser.OCRAPIKey,
		OCRAPIModel:    cfg.DocumentParser.OCRAPIModel,
		WorkDir:        cfg.DocumentParser.WorkDir,
	})
	if err != nil {
		logger.Warn("文档解析器初始化失败，PDF / Office 格式暂时无法收录；md 与 txt 不受影响",
			zap.Error(err))
		return nil
	}
	return parser
}

// initRouter 初始化路由
func (a *App) initRouter() {
	// 设置 Gin 模式
	gin.SetMode(a.cfg.App.Mode)
}

// initServer 初始化 HTTP 服务器
func (a *App) initServer() {
	engine := gin.New()

	// 注册路由
	a.router.Setup(engine)

	// 创建 HTTP 服务器
	a.server = &http.Server{
		Addr:           fmt.Sprintf(":%d", a.cfg.App.Port),
		Handler:        engine,
		ReadTimeout:    60 * time.Second,
		WriteTimeout:   60 * time.Second,
		MaxHeaderBytes: 1 << 20, // 1 MB
	}
}

// Run 运行应用
func (a *App) Run() {
	// 启动生成任务消费端
	if a.worker != nil {
		if err := a.worker.Start(); err != nil {
			logger.Fatal("生成任务消费端启动失败", zap.Error(err))
		}
	}

	// 启动 HTTP 服务器
	go func() {
		logger.Info("HTTP 服务器启动",
			zap.String("addr", a.server.Addr),
			zap.String("mode", a.cfg.App.Mode),
		)
		if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("HTTP 服务器启动失败", zap.Error(err))
		}
	}()

	// 优雅关闭
	a.gracefulShutdown()
}

// gracefulShutdown 优雅关闭
func (a *App) gracefulShutdown() {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("正在关闭服务器...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 关闭 HTTP 服务器
	if err := a.server.Shutdown(ctx); err != nil {
		logger.Error("服务器关闭失败", zap.Error(err))
	}

	// worker 用独立预算：取消之后它还要把在飞的任务收尾（解析子进程被杀、心跳停掉），
	// 这一步需要自己的时长，不能蹭上面那个已经被 server.Shutdown 用掉一截的 5s。
	// 而且必须在关数据库之前完成 —— 否则在飞任务的收尾会打在已经关闭的连接上。
	if a.knowledgeWorker != nil {
		workerCtx, workerCancel := context.WithTimeout(context.Background(), 60*time.Second)
		if err := a.knowledgeWorker.Stop(workerCtx); err != nil {
			logger.Error("知识库 worker 未在预算内停稳，可能留下 processing 行（下次启动会回收）", zap.Error(err))
		}
		workerCancel()
	}

	if a.router != nil {
		if err := a.router.Close(); err != nil {
			logger.Error("关闭路由连接失败", zap.Error(err))
		}
	}

	// 停止生成任务消费端：等在途任务跑完（超时的会被推回队列），避免半截被关掉数据库连接。
	if a.worker != nil {
		a.worker.Shutdown()
	}

	if a.mcpManager != nil {
		if err := a.mcpManager.Close(ctx); err != nil {
			logger.Error("关闭 MCP 连接失败", zap.Error(err))
		}
	}

	// 关闭数据库连接
	_ = database.ClosePostgres()
	_ = database.CloseRedis()

	// 同步日志
	_ = logger.Sync()

	logger.Info("服务器已关闭")
	logger.Info("=========================================")
}
