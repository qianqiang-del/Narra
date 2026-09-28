package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"narra/internal/agent/discussion"
	responsedto "narra/internal/model/dto/response"
	"narra/internal/model/entity"
	"narra/internal/repository"
	apperrors "narra/pkg/errors"
)

const (
	// maxUserMessageRunes 是用户单条消息的字数上限（按字符数，不按字节数）。
	//
	// 正文列是 text，本身存得下更长的东西；这个上限挡的是"把一整篇文章贴进来"这类
	// 用法 —— 它会整段进入上下文，挤掉讨论本身的位置。按字符数算是因为中文一个字
	// 三个字节，按字节限制会让中文用户觉得"才写了 1300 字就不让发了"。
	maxUserMessageRunes = 4000

	// defaultDiscussionTimeout 是单趟讨论的总超时。
	//
	// 一趟讨论最多跑 6 个回合，每个回合要调一次大模型（可能还要重试一次）。
	// 给足十分钟看不出问题，但这道闸必须有：没有它，一个卡住的上游会让 goroutine
	// 永远挂着，而"这条对话正在讨论中"的锁也就永远不放开。设成可配是为了让用例
	// 能用很短的时间验证超时路径。
	defaultDiscussionTimeout = 15 * time.Minute
)

// DiscussionDeps 是讨论入口所需的外部依赖。
//
// 用结构体而不是一长串参数：这里有八个依赖，参数列表既难读又容易传错顺序
// （与 internal/agent/discussion 的 Deps 同一个理由）。
type DiscussionDeps struct {
	Conversations repository.ConversationRepository
	Classrooms    repository.ClassroomRepository
	Agents        repository.ClassroomAgentRepository
	Roles         repository.RoleRepository
	Messages      repository.MessageRepository
	Tx            repository.TransactionManager

	// Orchestrator 是真正跑讨论的那台。由装配方构造好传进来 ——
	// 编排器的依赖（模型、事件仓储、记忆仓储……）比这里更多，
	// 让入口去凑齐它们只会把两层的装配缠在一起。
	Orchestrator *discussion.Orchestrator

	Logger *zap.Logger

	// Timeout 是单趟讨论的总超时；0 表示用默认值。
	Timeout time.Duration
}

// discussionService 是 DiscussionService 的实现。
type discussionService struct {
	conversations repository.ConversationRepository
	classrooms    repository.ClassroomRepository
	agents        repository.ClassroomAgentRepository
	roles         repository.RoleRepository
	messages      repository.MessageRepository
	tx            repository.TransactionManager
	orchestrator  *discussion.Orchestrator
	logger        *zap.Logger
	timeout       time.Duration

	// running 记着"哪些对话正在跑讨论"。
	//
	// 用进程内的表而不是查库，是因为讨论就跑在本进程的 goroutine 里：
	// 查库（找有没有 running 的运行记录）会引入一个更糟的失效模式 ——
	// 进程重启后那些记录永远停在 running，那条对话就再也开不了讨论了。
	// 内存表的生命周期与"谁在跑"完全一致，重启即清空，不会卡住任何东西。
	//
	// 代价：多实例部署时这条锁只在单实例内生效。当前是单机部署，
	// 真要多实例时应该换成数据库里的一个唯一约束，而不是把这张表搬到 Redis。
	mu      sync.Mutex
	running map[uint64]struct{}
}

var _ DiscussionService = (*discussionService)(nil)

// NewDiscussionService 创建讨论入口服务。
func NewDiscussionService(deps DiscussionDeps) DiscussionService {
	switch {
	case deps.Conversations == nil:
		panic("讨论入口缺少对话仓储")
	case deps.Classrooms == nil:
		panic("讨论入口缺少课程仓储")
	case deps.Agents == nil:
		panic("讨论入口缺少课堂角色仓储")
	case deps.Roles == nil:
		panic("讨论入口缺少角色池仓储")
	case deps.Messages == nil:
		panic("讨论入口缺少消息仓储")
	case deps.Tx == nil:
		panic("讨论入口缺少事务管理器")
	case deps.Orchestrator == nil:
		panic("讨论入口缺少编排器")
	}

	log := deps.Logger
	if log == nil {
		// 兜底成空日志器，而不是依赖全局单例：没初始化过的全局 logger 是空指针，
		// 会在真正干活的地方 panic（与编排器同一种做法）。
		log = zap.NewNop()
	}
	timeout := deps.Timeout
	if timeout <= 0 {
		timeout = defaultDiscussionTimeout
	}

	return &discussionService{
		conversations: deps.Conversations,
		classrooms:    deps.Classrooms,
		agents:        deps.Agents,
		roles:         deps.Roles,
		messages:      deps.Messages,
		tx:            deps.Tx,
		orchestrator:  deps.Orchestrator,
		logger:        log,
		timeout:       timeout,
		running:       make(map[uint64]struct{}),
	}
}

// Start 受理一次讨论。
//
// 校验顺序是有讲究的，从"最便宜、最可能被拒"排到"最贵"：
//
//	内容 → 对话在不在、还开不开 → 有没有别的讨论在跑 → 桌上有谁 → 模型配了没 → 落库 → 开跑
//
// 把"桌上没人""没配模型"放在落库之前，是为了让失败干净：用户得到的是一句明确的话，
// 库里也不会留下一条"发了消息却没人理"的记录。
func (s *discussionService) Start(ctx context.Context, conversationID uint64, content string) (*responsedto.DiscussionStart, error) {
	text := strings.TrimSpace(content)
	if text == "" {
		return nil, apperrors.New(apperrors.CodeMissingParam, "消息内容不能为空")
	}
	if utf8.RuneCountInString(text) > maxUserMessageRunes {
		return nil, apperrors.New(apperrors.CodeInvalidParam,
			fmt.Sprintf("消息内容超过 %d 字上限", maxUserMessageRunes))
	}

	conversation, err := s.conversations.FindByID(ctx, conversationID)
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return nil, apperrors.New(apperrors.CodeNotFound, "对话不存在")
	case err != nil:
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "查询对话失败", err)
	}
	if conversation.Status != entity.ConversationStatusActive {
		// 已结束的对话再发起讨论，内容会挂在一个用户以为早已收场的会话里。
		return nil, apperrors.New(apperrors.CodeConflict, "这条对话已结束，无法再发起讨论")
	}

	// 同一对话同时只跑一趟：两趟并行写同一条流，前端看到的是两场讨论交错在一起。
	if !s.acquire(conversationID) {
		return nil, apperrors.New(apperrors.CodeConflict, "这条对话正在讨论中，请稍后再试")
	}
	// 这里用标志位而不是直接 defer release：只有真正把讨论交出去之后，
	// 锁才归 goroutine 管。中途任何一步失败都必须当场放开，否则这条对话就永远开不了讨论了。
	handedOver := false
	defer func() {
		if !handedOver {
			s.release(conversationID)
		}
	}()

	participants, err := s.participants(ctx, conversation.ClassroomID)
	if err != nil {
		return nil, err
	}
	if len(participants) == 0 {
		return nil, apperrors.New(apperrors.CodeBadRequest, "这堂课还没有角色，无法发起讨论")
	}

	model, err := s.modelSnapshot(ctx, conversation.ClassroomID)
	if err != nil {
		return nil, err
	}

	message, err := s.appendUserMessage(ctx, conversationID, text)
	if err != nil {
		return nil, err
	}

	// 交出去：讨论在后台跑，这个请求立刻返回。
	// 用独立的 context（不接请求的 ctx）——请求一返回，它的 ctx 就被取消了，
	// 而讨论才刚开始。
	go s.run(conversationID, message.ID, participants)

	handedOver = true
	s.logger.Info("讨论已受理",
		zap.Uint64("conversation_id", conversationID),
		zap.Uint64("trigger_message_id", message.ID),
		zap.Int("participants", len(participants)),
		zap.Uint64("llm_provider_id", model.ProviderID),
		zap.String("llm_model_id", model.ModelID),
	)

	return &responsedto.DiscussionStart{
		ConversationID: conversationID,
		MessageID:      message.ID,
	}, nil
}

// run 在后台跑完一趟讨论。
//
// 这是唯一一处"结果没人接收"的调用：它返回时 HTTP 请求早已结束，所以成败只能靠
// 日志和事件表说话 —— 讨论失败时编排器会自己往事件表写一条 run.failed，
// 前端据此把等待结束掉，不会一直转圈。
func (s *discussionService) run(conversationID uint64, triggerMessageID uint64, participants []discussion.Participant) {
	// 无论怎么结束都要放锁，否则这条对话只能讨论一次。
	defer s.release(conversationID)

	// 兜住 panic：这个 goroutine 里没有 HTTP 中间件那层 recover，
	// 一次 panic 会带走整个进程 —— 为一场讨论崩掉整个服务，代价完全不成比例。
	defer func() {
		if recovered := recover(); recovered != nil {
			s.logger.Error("讨论执行中发生 panic，已隔离",
				zap.Uint64("conversation_id", conversationID),
				zap.Any("panic", recovered),
			)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()

	result, err := s.orchestrator.Run(ctx, discussion.Request{
		ConversationID:   conversationID,
		TriggerMessageID: triggerMessageID,
		Participants:     participants,
		// MaxTurns 留 0：轮数由后端定（默认值在编排器里），前端不参与。
	})
	if err != nil {
		// 编排器已经尽力把失败写进了运行记录与事件表；这里只补一条日志，
		// 带上它返回的 RunID / TraceID，排查时能直接从日志跳到库里那一行。
		s.logger.Error("讨论执行失败",
			zap.Uint64("conversation_id", conversationID),
			zap.Uint64("run_id", result.RunID),
			zap.String("trace_id", result.TraceID),
			zap.Error(err),
		)
		return
	}

	s.logger.Info("讨论执行完成",
		zap.Uint64("conversation_id", conversationID),
		zap.Uint64("run_id", result.RunID),
		zap.String("trace_id", result.TraceID),
		zap.String("status", result.Status),
		zap.String("stop_reason", result.StopReason),
		zap.Int("turns", len(result.Turns)),
	)
}

// participants 拼出圆桌上有谁。
//
// 角色从两张表合起来：classroom_agents 给出"这堂课用了哪些角色、各自是哪一行"，
// preset_agents 给出名字、身份与人设。顺序取角色池的展示顺序 ——
// 前端圆桌按同一个顺序排，两边一致用户才不会觉得"人换位了"。
func (s *discussionService) participants(ctx context.Context, classroomID uint64) ([]discussion.Participant, error) {
	links, err := s.agents.ListByClassroom(ctx, classroomID)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "查询课堂角色失败", err)
	}
	if len(links) == 0 {
		return nil, nil
	}

	agentIDs := make([]uint64, 0, len(links))
	for _, link := range links {
		agentIDs = append(agentIDs, link.AgentID)
	}
	roles, err := s.roles.ListByIDs(ctx, agentIDs)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "查询角色池失败", err)
	}

	return buildParticipants(links, roles), nil
}

// buildParticipants 把两张表的结果合成参与者列表。
//
// 抽成纯函数是为了能单独验证"顺序对不对、ID 有没有取错"—— 这一条最容易错，
// 而且错法很隐蔽：角色都上桌了、名字都对，只有归属字段用的是另一张表的 ID，
// 表现是消息写不进库（外键对不上），看起来像"数据库出问题了"。
func buildParticipants(links []entity.ClassroomAgent, roles []entity.PresetAgent) []discussion.Participant {
	linkByAgent := make(map[uint64]uint64, len(links))
	for _, link := range links {
		linkByAgent[link.AgentID] = link.ID
	}

	// roles 已由仓储按 sort_order 升序返回，直接沿用它的顺序。
	participants := make([]discussion.Participant, 0, len(roles))
	for _, role := range roles {
		linkID, ok := linkByAgent[role.ID]
		if !ok {
			// 理论上到不了这里：classroom_agents.agent_id 有外键约束。
			// 真出现了也只是少一个人上桌，不该让整场讨论开不起来。
			continue
		}
		participants = append(participants, discussion.Participant{
			// 注意这里是 classroom_agents.id（本课程的角色实例），不是 preset_agents.id：
			// 消息与回合的归属字段指向的就是它。
			ClassroomAgentID: linkID,
			Name:             role.Name,
			Role:             role.Role,
			Persona:          role.Persona,
		})
	}
	return participants
}

// modelConfig 是课程快照里与本链路相关的那几个字段。
type modelConfig struct {
	ProviderID uint64 `json:"llm_provider_id"`
	ModelID    string `json:"llm_model_id"`
}

// modelSnapshot 读这门课生成时记下的模型配置。
//
// 模型不由讨论自己选：用户在前端配好服务商、生成课堂时把选择记进课程快照，
// 这里只是把它读回来。读而不校验的话，一门没配模型的课会一路跑到第一次调模型
// 才失败 —— 那时运行记录已经建了，用户看到的是一场莫名其妙的失败，
// 而不是一句"这堂课没有配置大模型"。
//
// ⚠️ 现在读出来只用于校验与日志（讨论还用替身模型）。接上真实大模型时，
// 就是在这个位置按 providerID 查出连接信息、解密密钥、建客户端。
func (s *discussionService) modelSnapshot(ctx context.Context, classroomID uint64) (modelConfig, error) {
	classroom, err := s.classrooms.FindByID(ctx, classroomID)
	if err != nil {
		return modelConfig{}, apperrors.NewWithErr(apperrors.CodeInternalError, "查询课程失败", err)
	}

	var config modelConfig
	if err := json.Unmarshal(classroom.GenerationConfig, &config); err != nil {
		return modelConfig{}, apperrors.NewWithErr(apperrors.CodeInternalError, "解析课程模型配置失败", err)
	}
	if config.ProviderID == 0 {
		return modelConfig{}, apperrors.New(apperrors.CodeBadRequest, "这堂课没有配置大模型，无法发起讨论")
	}
	return config, nil
}

// appendUserMessage 把用户那句话写进对话。
//
// 它和"推一下对话的最近消息时间"在同一笔事务里：课堂列表按那个时间排序，
// 只写消息不推时间，正在活跃的对话会被排到列表下面去。
func (s *discussionService) appendUserMessage(ctx context.Context, conversationID uint64, content string) (*entity.ConversationMessage, error) {
	message := &entity.ConversationMessage{
		ConversationID: conversationID,
		SenderType:     entity.MessageSenderUser,
		// 用户消息没有角色，归属列为空；快照给个空对象，与列的默认值一致。
		SenderSnapshot: json.RawMessage(`{}`),
		Content:        content,
		// 用户的话是说完才发过来的，不存在"正在流式接收"的中间态。
		Status: entity.MessageStatusCompleted,
	}

	now := time.Now().UTC()
	if err := s.tx.Run(ctx, func(ctx context.Context) error {
		if err := s.messages.AppendNext(ctx, message); err != nil {
			return err
		}
		return s.conversations.TouchLastMessage(ctx, conversationID, now)
	}); err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "写入消息失败", err)
	}
	return message, nil
}

// acquire 尝试占住这条对话，已经被占则返回 false。
func (s *discussionService) acquire(conversationID uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, busy := s.running[conversationID]; busy {
		return false
	}
	s.running[conversationID] = struct{}{}
	return true
}

// release 放开这条对话。
func (s *discussionService) release(conversationID uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.running, conversationID)
}
