package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"narra/internal/api"
	"narra/internal/model/entity"
	"narra/internal/repository"
	"narra/internal/service"
	"narra/pkg/config"
	"narra/pkg/database"
	"narra/pkg/embedding"
	"narra/pkg/logger"
	"narra/pkg/tts"
)

// App 应用结构体
type App struct {
	cfg        *config.Config
	postgresDB *gorm.DB
	redis      *redis.Client
	router     *api.Router
	server     *http.Server
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

	// 建表；唯一约束 / CHECK / 外键 / 触发器由 migrations/0001_constraints.sql 补
	//
	// PresetAgent 必须排在自己的关联表 ClassroomAgent 之前：0001 里有
	// classroom_agents.agent_id -> preset_agents.id 的外键，表得先存在。
	if err := autoMigrateDatabase(a.postgresDB); err != nil {
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

func autoMigrateDatabase(db *gorm.DB) error {
	if err := db.AutoMigrate(baseDatabaseEntities()...); err != nil {
		return err
	}

	for _, entity := range memberCDatabaseEntities() {
		if db.Migrator().HasTable(entity) {
			continue
		}
		if err := db.AutoMigrate(entity); err != nil {
			return err
		}
	}

	return nil
}

// databaseEntities 按外键依赖顺序返回需要建表的实体。
func databaseEntities() []any {
	entities := append([]any{}, baseDatabaseEntities()...)
	return append(entities, memberCDatabaseEntities()...)
}

func baseDatabaseEntities() []any {
	return []any{
		&entity.Folder{},
		&entity.Classroom{},
		&entity.PresetAgent{},
		&entity.ClassroomAgent{},
		&entity.Scene{},
		&entity.SceneSegment{},
	}
}

func memberCDatabaseEntities() []any {
	return []any{
		&entity.ClassroomConversation{},
		&entity.ConversationMessage{},
		&entity.ContextCompaction{},
		&entity.OrchestrationRun{},
		&entity.AgentTurn{},
		&entity.SharedContextMemory{},
		&entity.ConversationEvent{},
		&entity.AgentTraceSpan{},
	}
}

// initDependencies 初始化依赖注入
//
// 顺序是 db → repository → service → router，每一层只拿到它下面那一层。
// 数据库连接在 initDatabase 里已经建好，这里只往下传。
func (a *App) initDependencies() error {
	// ========== 创建 Repository ==========
	roleRepo := repository.NewRoleRepository(a.postgresDB)
	embeddingSettingRepo := repository.NewEmbeddingSettingRepository(a.postgresDB)

	// ========== 创建 Service ==========
	roleSvc := service.NewRoleService(roleRepo)
	embeddingManager := embedding.NewManager(a.cfg.Embedding)
	embeddingSettingSvc := service.NewEmbeddingSettingService(embeddingSettingRepo, embeddingManager, a.cfg.JWT.Secret)
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

	// ========== 创建 Router ==========
	a.router = api.NewRouter(roleSvc, embeddingSettingSvc, voiceSvc)
	return nil
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

	// 关闭路由连接
	if a.router != nil {
		if err := a.router.Close(); err != nil {
			logger.Error("关闭路由连接失败", zap.Error(err))
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
