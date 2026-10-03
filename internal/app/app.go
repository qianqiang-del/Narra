package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"narra/internal/agent/classroom"
	"narra/internal/agent/discussion"
	"narra/internal/api"
	"narra/internal/bootstrap"
	"narra/internal/material"
	internalmcp "narra/internal/mcp"
	"narra/internal/model/entity"
	"narra/internal/rag"
	"narra/internal/rag/documentimage"
	"narra/internal/rag/einoretriever"
	"narra/internal/repository"
	"narra/internal/retention"
	"narra/internal/service"
	"narra/pkg/config"
	"narra/pkg/crypto"
	"narra/pkg/database"
	"narra/pkg/documentparser"
	"narra/pkg/embedding"
	"narra/pkg/logger"
	"narra/pkg/rerank"
	"narra/pkg/tts"

	"github.com/cloudwego/eino-ext/callbacks/langfuse"
	"github.com/cloudwego/eino/callbacks"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"
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
	imageStore      *documentimage.Store
	retention       *retention.Cleaner
	langfuseFlush   func()
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
		&entity.RerankSetting{},
	); err != nil {
		return fmt.Errorf("数据库迁移失败: %w", err)
	}
	logger.Info("数据库表结构迁移完成")

	// 词法检索的表达式索引要等表建好之后再补（pg_bigm 优先、pg_trgm 兜底，幂等）。
	// 与向量索引同一种立场：失败只让检索退化为顺序扫描、不影响正确性，所以不上抛；
	// 但两类别混为一谈 —— 扩展都没装是**环境属性**（提示即可），其余失败（DDL 权限、
	// 数据库故障）才留告警。
	if kind, err := repository.EnsureLexicalIndex(context.Background(), a.postgresDB); err != nil {
		if errors.Is(err, repository.ErrLexicalIndexUnavailable) {
			logger.Info("数据库没有 pg_bigm / pg_trgm 扩展，词法检索走顺序扫描（结果不受影响，数据量大时会慢）")
		} else {
			logger.Warn("建立词法检索索引失败，词法检索将退化为顺序扫描", zap.Error(err))
		}
	} else {
		logger.Info("词法检索索引就绪", zap.String("extension", string(kind)))
	}

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
	// 观测：必须在任何 graph 跑起来之前注册（AppendGlobalHandlers 不是线程安全的）。
	if a.cfg.Langfuse.Enabled {
		handler, flusher := langfuse.NewLangfuseHandler(&langfuse.Config{
			Host:      a.cfg.Langfuse.Host,
			PublicKey: a.cfg.Langfuse.PublicKey,
			SecretKey: a.cfg.Langfuse.SecretKey,
			Name:      "narra",
		})
		callbacks.AppendGlobalHandlers(handler)
		a.langfuseFlush = flusher
		logger.Info("Langfuse 观测已启用", zap.String("host", a.cfg.Langfuse.Host))
	}

	// ========== 创建 Repository ==========
	roleRepo := repository.NewRoleRepository(a.postgresDB)
	embeddingSettingRepo := repository.NewEmbeddingSettingRepository(a.postgresDB)
	embeddingModelRepo := repository.NewEmbeddingModelRepository(a.postgresDB)
	mcpServerRepo := repository.NewMCPServerRepository(a.postgresDB)
	llmProviderRepo := repository.NewLLMProviderRepository(a.postgresDB)
	rerankSettingRepo := repository.NewRerankSettingRepository(a.postgresDB)
	// 重排运行时：检索侧每次请求向它要"当前生效的精排客户端"，设置页保存/启停后
	// 由服务层热更新（见 rerankSettingSvc 的 LoadActive 与 reload）；nil = 精排关闭。
	rerankManager := rerank.NewManager()
	classroomRepo := repository.NewClassroomRepository(a.postgresDB)
	folderRepo := repository.NewFolderRepository(a.postgresDB)
	classroomAgentRepo := repository.NewClassroomAgentRepository(a.postgresDB)
	txManager := repository.NewTransactionManager(a.postgresDB)
	sceneRepo := repository.NewSceneRepository(a.postgresDB)
	sceneSegmentRepo := repository.NewSceneSegmentRepository(a.postgresDB)
	knowledgeDocumentRepo := repository.NewKnowledgeDocumentRepository(a.postgresDB)
	knowledgeUploadRecordRepo := repository.NewKnowledgeUploadRecordRepository(a.postgresDB)
	knowledgeSearchRepo := repository.NewKnowledgeSearchRepository(a.postgresDB)
	// 对话与事件是两个仓储：前者回答"这条对话在不在"，后者是 SSE 的事件来源。
	// 事件由编排 / 工作台在各自事务里写（见 ConversationEventRepository.AppendNext），
	// SSE 侧只读。
	conversationRepo := repository.NewConversationRepository(a.postgresDB)
	conversationEventRepo := repository.NewConversationEventRepository(a.postgresDB)
	traceSpanRepo := repository.NewAgentTraceSpanRepository(a.postgresDB)
	// 多 Agent 讨论（成员 C）用到的仓储：消息、运行、回合、上下文摘要、共享记忆。
	// 这几个表此前没有任何生产代码使用过 —— 讨论链路是它们的第一个使用者。
	messageRepo := repository.NewMessageRepository(a.postgresDB)
	runRepo := repository.NewRunRepository(a.postgresDB)
	turnRepo := repository.NewTurnRepository(a.postgresDB)
	compactionRepo := repository.NewContextCompactionRepository(a.postgresDB)
	sharedMemoryRepo := repository.NewSharedMemoryRepository(a.postgresDB)

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
	knowledgeIngest := a.cfg.KnowledgeIngest.WithDefaults()
	// 知识库图片是持久资产，落在本地目录（第一版单机部署），由静态路由对外提供；
	// 收录时发布图片、删除文档时清理，两处必须用同一个目录（见 documentimage）。
	knowledgeDir := a.cfg.Storage.KnowledgeDir
	if knowledgeDir == "" {
		knowledgeDir = "data/knowledge"
	}
	// 与 uploadDir 同理统一成绝对路径：相对路径会按"当时的 cwd"解析，
	// 换个工作目录启动，静态路由指向的目录和收录写入的目录就不是同一个了。
	knowledgeDir, err := filepath.Abs(knowledgeDir)
	if err != nil {
		return fmt.Errorf("解析知识图片目录的绝对路径失败: %w", err)
	}
	imageStore, err := documentimage.NewStore(knowledgeDir)
	if err != nil {
		return fmt.Errorf("初始化知识图片存储失败: %w", err)
	}
	a.imageStore = imageStore
	knowledgeIngester := rag.NewIngester(
		knowledgeDocumentRepo, knowledgeUploadRecordRepo, embeddingModelRepo, embeddingManager, parser,
		rag.IngestOptions{
			Tx:                   txManager,
			QueueCapacity:        knowledgeIngest.QueueCapacity,
			EmbeddingConcurrency: knowledgeIngest.EmbeddingConcurrency,
			Images:               imageStore,
			MaterialTTL:          knowledgeIngest.MaterialTTL,
		})
	uploadDir := a.cfg.Storage.UploadDir
	if uploadDir == "" {
		uploadDir = "data/uploads"
	}
	// 统一成绝对路径再往三处注入（controller / service / worker）。
	// 否则默认的 "data/uploads" 会原样记进 metadata.upload_path，而读取、重试与清理
	// 都按"当时的 cwd"解析 —— 换个工作目录启动，失败原件就找不到了：
	// 重试报"原件不在"，删除时的清理也会静默失效（文件永远留在磁盘上）。
	uploadDir, err = filepath.Abs(uploadDir)
	if err != nil {
		return fmt.Errorf("解析上传目录的绝对路径失败: %w", err)
	}
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		return fmt.Errorf("创建上传目录失败: %w", err)
	}
	// 文件收录跑在后台 worker 里：上传接口只建 pending 行，解析与向量化由它轮询推进。
	// 并发数来自 knowledge_ingest.parse_concurrency（默认 2）：解析同时吃 CPU 与 OCR，
	// 上限是"别把机器挤满"；向量化另有独立的 embedding_concurrency，见 rag.IngestOptions。
	// uploadDir 必须和上面给 controller、service 的是同一个值，否则删除时的暂存清理会静默失效。
	a.knowledgeWorker = rag.NewWorker(knowledgeDocumentRepo, knowledgeIngester, uploadDir, knowledgeIngest.ParseConcurrency)
	a.knowledgeWorker.Start()
	// 检索是收录的另一半门面：两路召回（余弦相似度 + 词项命中）经 RRF 融合，
	// 编排在 rag.Retriever，服务层只做 DTO 映射。向量模型的登记与索引维护走
	// embeddingModelRepo —— 检索只在同一模型下比向量，那个"默认模型"由它说了算。
	knowledgeRetriever := rag.NewRetriever(knowledgeSearchRepo, embeddingModelRepo, embeddingManager)
	// 词法路的中文分词器要加载秒级大小的词典：在启动时做掉，不让第一个检索请求承担这笔开销。
	rag.WarmupLexicalTokenizer()
	// 多查询门面：单查询直通 rag.Retriever；输入里带 queries 变体时，经 Eino 的
	// multiquery 流程并发召回、RRF 融合（见 internal/rag/einoretriever）。
	// 服务层认的是这一个接口，HTTP 与 MCP 工具两条入口同时受益。
	multiRetrieval, err := einoretriever.NewMultiQuery(knowledgeRetriever)
	if err != nil {
		return fmt.Errorf("创建多查询检索失败: %w", err)
	}
	// 精排装饰器包在最外层：两层 RRF 融合后只精排一次、在截断到 top_k 之前完成。
	// 是否生效由 rerankManager 决定（设置页保存/启停即时生效）；关闭时原样透传，
	// 行为与"没包这一层"完全一致。
	var knowledgeRetrieval rag.Searcher = multiRetrieval
	reranked, err := rag.NewReranked(knowledgeRetrieval, func() rag.Reranker {
		if client := rerankManager.Current(); client != nil {
			return client
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("创建精排装饰器失败: %w", err)
	}
	knowledgeRetrieval = reranked
	// 上下文装配层包在最外面：折叠同组命中、把整节/整符号拼好放进 context，再交给服务层。
	// 精排的输入与打分不受影响；装配失败自动降级为"只返回命中切片"（见 rag/assemble.go）。
	assembled, err := rag.NewAssembled(knowledgeRetrieval, knowledgeSearchRepo)
	if err != nil {
		return fmt.Errorf("创建上下文装配层失败: %w", err)
	}
	knowledgeRetrieval = assembled
	knowledgeSvc := service.NewKnowledgeService(knowledgeDocumentRepo, knowledgeUploadRecordRepo, knowledgeIngester, knowledgeRetrieval, embeddingModelRepo, uploadDir, knowledgeDir)

	// 内置工具 rag_retrieve：把知识库检索直接挂给 Eino agent（见 internal/mcp/knowledge_tool.go）。
	// 注册点在这里而不是 NewManager 那边，是因为工具的实现依赖知识库服务 ——
	// 装配顺序天然把它排在后面，而 mcpManager 是指针，课堂 worker 后面才建，拿到的就是含它的工具集。
	// 这两步失败都是装配错误（依赖为空、工具重名），直接让启动失败，不留给运行时才发现。
	ragRetrieveTool, err := internalmcp.NewKnowledgeRetrieveTool(knowledgeSvc)
	if err != nil {
		return fmt.Errorf("创建知识库检索工具失败: %w", err)
	}
	if err := a.mcpManager.RegisterLocalTool(ragRetrieveTool); err != nil {
		return fmt.Errorf("注册知识库检索工具失败: %w", err)
	}

	// 课程材料消费：规划/调研阶段按本课材料的 document_id 定向取文本与检索，
	// 不让材料只躺在知识库里等模型"碰巧"查到（见 internal/material）。
	materialSource, err := material.NewSource(knowledgeDocumentRepo, knowledgeDocumentRepo, knowledgeSvc)
	if err != nil {
		return fmt.Errorf("创建课程材料消费组件失败: %w", err)
	}

	// 材料目录+摘要：首次建课时用本课模型惰性生成，写回 knowledge_documents.material_outline，
	// 后续课堂按内容校验和命中缓存；生成失败退回代码目录（见 internal/material/outline.go）。
	materialOutlines, err := material.NewOutlineBuilder(knowledgeDocumentRepo, knowledgeDocumentRepo, knowledgeDocumentRepo)
	if err != nil {
		return fmt.Errorf("创建课程材料摘要组件失败: %w", err)
	}

	llmProviderSvc := service.NewLLMProviderService(llmProviderRepo, encryptionKey, a.mcpManager)
	// 重排配置是「多存一条、同时只启用一条」：设置页增删改测，检索侧只读启用中的那条。
	// 先做启动对齐（没有启用记录时关闭精排），之后的变动由服务层的 reload 热更新。
	rerankSettingSvc := service.NewRerankSettingService(rerankSettingRepo, encryptionKey, rerankManager)
	if err := rerankSettingSvc.LoadActive(context.Background()); err != nil {
		return fmt.Errorf("加载重排配置失败: %w", err)
	}
	audioDir := a.cfg.Storage.AudioDir
	if audioDir == "" {
		audioDir = "data/audio"
	}
	audioDir, err = filepath.Abs(audioDir)
	if err != nil {
		return fmt.Errorf("解析音频目录失败: %w", err)
	}
	if err := os.MkdirAll(audioDir, 0o755); err != nil {
		return fmt.Errorf("创建音频目录失败: %w", err)
	}

	// ========== 课堂受理 + 生成任务 ==========
	// TTS 未启用时 ttsClient 是 nil 指针；直接塞进接口会让接口非 nil，
	// 下游 deps.TTS == nil 的判断失效。所以只在真的有客户端时才赋值。
	classroomDeps := classroom.Deps{
		Providers:       llmProviderRepo,
		Classrooms:      classroomRepo,
		Scenes:          sceneRepo,
		Segments:        sceneSegmentRepo,
		Agents:          classroomAgentRepo,
		Roles:           roleRepo,
		Tx:              txManager,
		AudioDir:        audioDir,
		Tools:           a.mcpManager,
		Materials:       materialSource,
		Outlines:        materialOutlines,
		EncryptionKey:   encryptionKey,
		PageConcurrency: a.cfg.Classroom.PageConcurrency,
		TTSPoolSize:     a.cfg.Classroom.TTSPoolSize,
		MaxDuration:     a.cfg.Classroom.MaxDuration,
	}
	if ttsClient != nil {
		classroomDeps.TTS = ttsClient
	}
	queue, workerRuntime, err := bootstrap.BuildWorker(classroomDeps, a.cfg)
	if err != nil {
		return err
	}
	a.worker = workerRuntime
	classroomSvc := service.NewClassroomService(classroomRepo, classroomAgentRepo, roleRepo, sceneRepo, llmProviderSvc, knowledgeDocumentRepo, queue, txManager, audioDir)
	folderSvc := service.NewFolderService(folderRepo, txManager)

	// 对话事件流（SSE）：执行过程与最终结果从 conversation_events 里增量读、推给前端。
	// 事件的写入不经过服务层 —— 它属于产生内容的那条链路（编排 / 工作台）的事务。
	conversationSvc := service.NewConversationService(conversationRepo, classroomRepo, messageRepo, conversationEventRepo)

	// 多 Agent 讨论（成员 C）：用户发一句话 → 跑一趟讨论，过程写进事件表，
	// 由上面那条 SSE 流带给前端。
	//
	// 编排器上挂的 Model / Summarizer / Extractor 是**兜底**：真正每次讨论用的那套
	// 由 ModelFactory 按课程快照现建（哪门课选了哪个服务商、哪个模型），
	// 建成之后经 WithModels 换到一个新编排器上，不会动这个共享实例。
	discussionOrchestrator, err := discussion.New(discussion.Deps{
		Tx:            txManager,
		Conversations: conversationRepo,
		Messages:      messageRepo,
		Runs:          runRepo,
		Turns:         turnRepo,
		Compactions:   compactionRepo,
		Memories:      sharedMemoryRepo,
		Events:        conversationEventRepo,
		Spans:         traceSpanRepo,
		Model:         discussion.FakeModel{},
		Summarizer:    discussion.FakeModel{},
		Extractor:     discussion.FakeModel{},
		// 按上一轮给出的动作换人：会"停下来问用户"，也会在认不出动作时兜底换人。
		Director: discussion.TurnTakingDirector{},
		Logger:   logger.GetLogger(),
	})
	if err != nil {
		return fmt.Errorf("装配讨论编排器失败: %w", err)
	}
	discussionSvc := service.NewDiscussionService(service.DiscussionDeps{
		Conversations: conversationRepo,
		Classrooms:    classroomRepo,
		Scenes:        sceneRepo,
		Segments:      sceneSegmentRepo,
		Agents:        classroomAgentRepo,
		Roles:         roleRepo,
		Messages:      messageRepo,
		Tx:            txManager,
		Orchestrator:  discussionOrchestrator,
		// 读课程快照里的服务商与模型 → 解密 API Key → 建 llm 客户端 →
		// 包成讨论要的三件能力。密钥与课堂生成那条链路同源，都是 JWT Secret 派生的。
		ModelFactory: discussion.NewRuntimeFactory(discussion.NewEntityProviderFinder(llmProviderRepo), encryptionKey),
		Logger:       logger.GetLogger(),
	})

	// 过程数据的过期清理：expires_at 在写入时就按各自保留期算好了（事件 7 天），
	// 清理侧只认这一列。没有它事件表会一直涨，而它记录的事实另有更长的生命周期。
	// 课程材料同挂在这条循环上：到期未关联的材料逐篇走 KnowledgeService.Delete，
	// 连带清理归档原件与文档图片（见 service.ExpiredMaterialCleaner）。
	materialCleaner, err := service.NewExpiredMaterialCleaner(knowledgeDocumentRepo, knowledgeSvc)
	if err != nil {
		return fmt.Errorf("创建课程材料清理器失败: %w", err)
	}
	a.retention = retention.New(
		retention.Table{Name: "conversation_events", Store: conversationEventRepo},
		retention.Table{Name: "agent_trace_spans", Store: traceSpanRepo},
		retention.Table{Name: "course_materials", Store: materialCleaner},
	)
	if count, err := bootstrap.ReconcileDiscussions(context.Background(), txManager, runRepo, turnRepo, messageRepo, conversationEventRepo); err != nil {
		return fmt.Errorf("讨论运行启动对账失败: %w", err)
	} else if count > 0 {
		logger.Warn("已收尾上次服务中断的讨论", zap.Int("count", count))
	}

	// 对账：队列里已不会继续处理的 generating 课程，归档的判失败、丢了的重投。
	if err := bootstrap.ReconcileGenerating(context.Background(), classroomDeps, workerRuntime, queue); err != nil {
		return err
	}

	sceneSvc := service.NewSceneService(sceneSegmentRepo, sceneRepo)
	traceSvc := service.NewTraceService(runRepo, turnRepo, traceSpanRepo)
	a.router = api.NewRouter(roleSvc, embeddingSettingSvc, voiceSvc, mcpServerSvc, llmProviderSvc, rerankSettingSvc, classroomSvc, folderSvc, sceneSvc, knowledgeSvc, conversationSvc, discussionSvc, traceSvc, uploadDir, parser, knowledgeIngest)
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
	audioDir := a.cfg.Storage.AudioDir
	if audioDir == "" {
		audioDir = "data/audio"
	}
	if absoluteDir, err := filepath.Abs(audioDir); err == nil {
		audioDir = absoluteDir
	}
	engine.Static("/audio", audioDir)

	// 知识库图片与音频同类：静态路由直接把它暴露出去（第一版单机部署，见 documentimage）。
	// 目录在 initDependencies 里已统一成绝对路径并建好；imageStore 为空只可能是
	// 装配顺序被绕过，此时不挂路由也不影响启动。
	if a.imageStore != nil {
		engine.Static(documentimage.URLPrefix, a.imageStore.ImagesDir())
	}

	// 注册路由
	a.router.Setup(engine)

	// 创建 HTTP 服务器
	// 读超时要覆盖"最大批量上传在较慢链路上传完"的时间，否则 100MB 的限制会被网络
	// 提前掐断（见 config.AppConfig.ReadTimeout）。没配置时退回 60s 的历史值。
	readTimeout := a.cfg.App.ReadTimeout
	if readTimeout <= 0 {
		readTimeout = 60 * time.Second
	}
	a.server = &http.Server{
		Addr:        fmt.Sprintf(":%d", a.cfg.App.Port),
		Handler:     engine,
		ReadTimeout: readTimeout,
		// 普通接口的写死线。SSE（pkg/sse.Start）与同步的正文收录
		// （knowledge.IngestText）会各自解除它 —— 它们单次处理可能超过 60s。
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

	// 启动过期数据清理（启动时先清一轮，之后按周期跑）
	if a.retention != nil {
		a.retention.Start()
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

	// 过期清理要先于关库停下：它下一轮可能正好在写 DELETE。
	if a.retention != nil {
		retentionCtx, retentionCancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := a.retention.Stop(retentionCtx); err != nil {
			logger.Error("过期清理任务未在预算内停稳", zap.Error(err))
		}
		retentionCancel()
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

	// 把缓冲里的 trace 刷出去；进程退出后没发出去的事件就丢了。
	if a.langfuseFlush != nil {
		a.langfuseFlush()
	}

	// 关闭数据库连接
	_ = database.ClosePostgres()
	_ = database.CloseRedis()

	// 同步日志
	_ = logger.Sync()

	logger.Info("服务器已关闭")
	logger.Info("=========================================")
}
