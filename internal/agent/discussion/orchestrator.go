package discussion

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"

	"go.uber.org/zap"

	"narra/internal/model/entity"
	"narra/internal/repository"
)

const (
	// errorMessageLimit 是落库的错误描述最大字节数。
	// 库里是 text 列，本身就存得下；限制是为了别把整段堆栈或原始 API 响应写进去 ——
	// 那一列是给运维看"为什么失败"的，不是日志。
	errorMessageLimit = 500

	// finalizeTimeout 是收尾写库的超时。
	finalizeTimeout = 5 * time.Second
)

// Deps 是编排器需要的全部外部依赖。
//
// 用结构体而不是一串参数：这里有七个依赖，参数列表既难读又容易传错顺序，
// 而且以后加依赖（比如第 5 步的共享记忆）会改掉所有调用处的签名。
// 用结构体则只在调用处多写一行字段。
type Deps struct {
	Tx            repository.TransactionManager // 把"写消息 + 记回合"绑成一笔账
	Conversations repository.ConversationRepository
	Messages      repository.MessageRepository
	Runs          repository.RunRepository
	Turns         repository.TurnRepository
	Model         Model    // 真实模型或假模型，见 model.go
	Director      Director // 选人策略；留空则用"轮流发言"
	Logger        *zap.Logger

	// Compactions 读写历史摘要，供上下文压缩使用。
	Compactions repository.ContextCompactionRepository

	// Summarizer 把过长的历史压成摘要；与 Model 分开，见 model.go 的说明。
	Summarizer Summarizer

	// Memories 读写共享上下文记忆（第 5 步）—— 讨论收尾时记下结论，下次发言时带上。
	Memories repository.SharedMemoryRepository

	// Extractor 把一场讨论提炼成几条值得长期记住的事。
	Extractor MemoryExtractor

	// Events 追加讨论过程中的事件（第 6 步）—— 前端靠它边聊边看。
	//
	// 注意这里**只写库、不推送**：事件先落库，再由 SSE 那一层按序号读出去发给前端。
	// 走表而不是直接推，是因为断线续传只能靠落在库里的序号（见交接文档的"通路铁律"）。
	Events repository.ConversationEventRepository

	// ContextBudget 是组装上下文时的 token 上限，超过就触发摘要压缩；0 表示用默认值。
	// 做成可配是为了让测试能用很小的值把压缩逼出来 —— 真要造出几千 token 的对话，
	// 用例又慢又难读。
	ContextBudget int

	// ContextKeepRecent 是压缩时保留多少条最近消息不进摘要；0 表示用默认值。
	ContextKeepRecent int

	// ContextMaxMemories 是每次发言最多带几条共享记忆进上下文；0 表示用默认值。
	ContextMaxMemories int
}

// Orchestrator 跑完一次完整的课堂讨论。
type Orchestrator struct {
	deps Deps
}

// New 创建编排器。
//
// 在装配阶段就把缺依赖的问题暴露出来：这类错误如果在第一次请求时才炸，
// 表现是"讨论莫名其妙失败"，而根因只是启动时少传了一个仓储。
func New(deps Deps) (*Orchestrator, error) {
	switch {
	case deps.Tx == nil:
		return nil, fmt.Errorf("编排器缺少事务管理器")
	case deps.Conversations == nil:
		return nil, fmt.Errorf("编排器缺少对话仓储")
	case deps.Messages == nil:
		return nil, fmt.Errorf("编排器缺少消息仓储")
	case deps.Runs == nil:
		return nil, fmt.Errorf("编排器缺少运行仓储")
	case deps.Turns == nil:
		return nil, fmt.Errorf("编排器缺少回合仓储")
	case deps.Compactions == nil:
		return nil, fmt.Errorf("编排器缺少摘要仓储")
	case deps.Memories == nil:
		return nil, fmt.Errorf("编排器缺少共享记忆仓储")
	case deps.Events == nil:
		return nil, fmt.Errorf("编排器缺少事件仓储")
	case deps.Model == nil:
		return nil, fmt.Errorf("编排器缺少模型")
	case deps.Summarizer == nil:
		return nil, fmt.Errorf("编排器缺少摘要器")
	case deps.Extractor == nil:
		return nil, fmt.Errorf("编排器缺少记忆提炼器")
	}
	if deps.Director == nil {
		deps.Director = RoundRobinDirector{}
	}
	if deps.Logger == nil {
		// 用空日志器兜底而不是依赖全局单例：没初始化过的全局 logger 是空指针，
		// 一路调用下去会在真正干活的地方 panic —— 那种崩溃现场离根因太远。
		deps.Logger = zap.NewNop()
	}
	return &Orchestrator{deps: deps}, nil
}

// Run 跑一次讨论：开一趟运行，逐个角色发言，直到该停为止。
//
// 它只在"确实跑起来了"的情况下返回 nil 错误；一旦中途失败，会尽力把运行和当前回合
// 标成失败再返回错误 —— 半途而废的记录里，"为什么没跑完"比"跑完的部分"更值钱。
//
// ⚠️ 契约：**返回错误时 Result 依然是有意义的**（RunID / TraceID / Turns 都已填好），
// 调用方可以拿它去查库、写日志。这与"err 非空就别用其它返回值"的常见惯例不同，
// 是有意的 —— 失败时最需要的恰恰是"哪一趟活失败了"这个 ID。
func (o *Orchestrator) Run(ctx context.Context, request Request) (Result, error) {
	maxTurns, err := normalizeMaxTurns(request.MaxTurns)
	if err != nil {
		return Result{}, err
	}
	if len(request.Participants) == 0 {
		return Result{}, fmt.Errorf("讨论至少需要一个参与者")
	}
	if request.ConversationID == 0 {
		return Result{}, fmt.Errorf("缺少对话 ID")
	}
	if request.TriggerMessageID == 0 {
		return Result{}, fmt.Errorf("缺少触发消息 ID")
	}

	// 触发消息的正文就是这场讨论的主题。在这里读一次，而不是每一轮都读：
	// 讨论过程中主题不会变，重复读只是多几次查询。
	trigger, err := o.deps.Messages.FindByID(ctx, request.TriggerMessageID)
	if err != nil {
		return Result{}, fmt.Errorf("读取触发消息失败: %w", err)
	}

	// 读对话是为了拿"这属于哪门课"：共享记忆要按课堂查（课堂级记忆同一门课通用）。
	// 也顺带把"对话不存在"挡在建运行记录之前 —— 否则会先留下一条挂在空气上的运行记录。
	conversation, err := o.deps.Conversations.FindByID(ctx, request.ConversationID)
	if err != nil {
		return Result{}, fmt.Errorf("读取对话失败: %w", err)
	}

	run, err := o.openRun(ctx, request, maxTurns)
	if err != nil {
		return Result{}, err
	}

	log := o.deps.Logger.With(
		zap.Uint64("run_id", run.ID),
		zap.String("trace_id", run.TraceID),
		zap.Uint64("conversation_id", request.ConversationID),
	)

	// 让前端先知道"这一趟开工了、桌上有谁"。事务外发：它不同生共死于任何一条记录，
	// 写不进去最多是这次的流少个头，不该让讨论跑不起来。
	o.emitEvent(ctx, request.ConversationID, &run.ID, nil,
		entity.ConversationEventRunStarted, runStartedPayload{
			TriggerMessageID: request.TriggerMessageID,
			MaxTurns:         run.MaxTurns,
			Participants:     participantBriefs(request.Participants),
		})

	log.Info("讨论开始", zap.Int16("max_turns", run.MaxTurns), zap.Int("participants", len(request.Participants)))

	spoken := make([]int, len(request.Participants))
	outcomes := make([]TurnOutcome, 0, maxTurns)
	lastSpeaker := -1
	lastAction := ""
	stopReason := ""

	for turnNo := int16(1); turnNo <= run.MaxTurns; turnNo++ {
		decision := o.deps.Director.Decide(DiscussionState{
			Participants: request.Participants,
			Spoken:       spoken,
			LastSpeaker:  lastSpeaker,
			LastAction:   lastAction,
			TurnNo:       turnNo,
		})
		if decision.Stop {
			// 停下来有好几种原因（有人要问用户 / 有人宣布结束 / 全员说完），
			// 逐一分清是有意的：它们都算"正常收尾"，但对用户和运维的含义完全不同 ——
			// "等用户回答"和"已经结束"在前端是两种界面。
			stopReason = decision.StopReason
			log.Info("讨论停下", zap.String("stop_reason", stopReason), zap.Int16("turns", turnNo-1))
			break
		}
		if decision.SpeakerIndex < 0 || decision.SpeakerIndex >= len(request.Participants) {
			// 选人策略返回了越界下标，这是编排自身的 bug。既不能继续往下跑
			// （会把消息记到不存在的人头上），也不能让它把进程带崩 ——
			// 当场收尾，并留下能直接定位的原因。
			return o.abandon(run, fmt.Errorf(
				"选人策略返回了非法的发言人下标 %d（参与者共 %d 人）",
				decision.SpeakerIndex, len(request.Participants),
			), outcomes, log)
		}

		participant := request.Participants[decision.SpeakerIndex]
		// 选完人就把结论告诉前端：用户看得见"为什么现在轮到他"。
		// 放在下标检查之后 —— 越界时那不是一个真的决定，不该发出去。
		o.emitEvent(ctx, request.ConversationID, &run.ID, nil,
			entity.ConversationEventDirectorDecision, directorDecisionPayload{
				TurnNo:    turnNo,
				AgentID:   participant.ClassroomAgentID,
				AgentName: participant.Name,
				Reason:    decision.Reason,
			})

		outcome, err := o.speak(ctx, request, run, conversation.ClassroomID, participant, turnNo, trigger.Content)
		if err != nil {
			return o.abandon(run, err, outcomes, log)
		}

		spoken[decision.SpeakerIndex]++
		lastSpeaker = decision.SpeakerIndex
		lastAction = outcome.NextAction
		outcomes = append(outcomes, outcome)
		log.Info("回合完成",
			zap.Int16("turn_no", outcome.TurnNo),
			zap.String("agent", outcome.AgentName),
			zap.Uint64("message_id", outcome.MessageID),
			zap.Int32("output_tokens", outcome.OutputTokens),
			zap.String("next_action", outcome.NextAction),
		)
	}

	if stopReason == "" {
		stopReason = entity.RunStopMaxTurns
	}

	// 终态由停止原因决定：只有"要问用户"是挂起，其余都是正常收尾。
	// 把"等用户"也写成 completed，前端就会把一场没聊完的讨论显示成已结束。
	status := entity.RunStatusCompleted
	if stopReason == entity.RunStopWaiting {
		status = entity.RunStatusWaitingUser
	}

	if err := o.closeRun(ctx, run, status, stopReason, nil, log); err != nil {
		// 连终态都没写进去，前端最需要一个"结束信号"来停止转圈 —— 尽力补一条。
		o.emitEvent(ctx, request.ConversationID, &run.ID, nil,
			entity.ConversationEventRunFailed, runFailedPayload{
				Error: truncate(err.Error(), errorMessageLimit),
			})
		return Result{
			RunID: run.ID, TraceID: run.TraceID,
			Status: entity.RunStatusFailed, StopReason: entity.RunStopError,
			Turns: outcomes,
		}, err
	}

	// 整趟讨论的终态。挂起等用户与正常结束是两件事，前端对应两种界面 ——
	// 把挂起写成"已结束"，用户就会以为这堂课聊完了。
	if status == entity.RunStatusWaitingUser {
		o.emitEvent(ctx, request.ConversationID, &run.ID, nil,
			entity.ConversationEventRunWaitingUser, runWaitingUserPayload{Reason: stopReason})
	} else {
		o.emitEvent(ctx, request.ConversationID, &run.ID, nil,
			entity.ConversationEventRunCompleted, runCompletedPayload{
				StopReason: stopReason,
				Turns:      len(outcomes),
			})
	}

	// 记忆在收尾之后提炼：它不参与"这次讨论算不算成功"的判定（决策 S5-7 / S5-8），
	// 所以放在运行终态写完之后，失败也只记日志、就地吞掉。
	// 放这个位置还有个好处：即便提炼慢，讨论的成功结果也已经落库了。
	o.extractMemories(ctx, conversation.ClassroomID, request, trigger.Content, outcomes, log)

	log.Info("讨论结束",
		zap.String("status", status),
		zap.String("stop_reason", stopReason),
		zap.Int("turns", len(outcomes)),
	)

	return Result{
		RunID:      run.ID,
		TraceID:    run.TraceID,
		Status:     status,
		StopReason: stopReason,
		Turns:      outcomes,
	}, nil
}

// openRun 建一趟运行并标记为进行中。
//
// attempt_no 由仓储在事务里分配：同一条用户消息可以被重新执行（用户点重试），
// 每次都新增一行而不是覆盖旧行，好让"第一次为什么失败"留在库里。
func (o *Orchestrator) openRun(ctx context.Context, request Request, maxTurns int) (*entity.OrchestrationRun, error) {
	run := &entity.OrchestrationRun{
		ConversationID:      request.ConversationID,
		TriggerMessageID:    request.TriggerMessageID,
		TraceID:             newTraceID(),
		Status:              entity.RunStatusQueued,
		MaxTurns:            int16(maxTurns),
		OrchestratorVersion: orchestratorVersion,
		ConfigSnapshot: json.RawMessage(fmt.Sprintf(
			`{"orchestrator_version":%q,"max_turns":%d,"participants":%d}`,
			orchestratorVersion, maxTurns, len(request.Participants),
		)),
	}

	if err := o.deps.Tx.Run(ctx, func(ctx context.Context) error {
		return o.deps.Runs.CreateNextAttempt(ctx, run)
	}); err != nil {
		return nil, fmt.Errorf("建立编排运行失败: %w", err)
	}

	if err := o.deps.Runs.MarkRunning(ctx, run.ID, time.Now().UTC()); err != nil {
		return nil, fmt.Errorf("标记运行开始失败: %w", err)
	}
	return run, nil
}

// speak 跑完一个回合：建回合记录 → 叫模型 → 写消息 → 收尾回合。
//
// 分成两笔事务，中间夹着模型调用，这是有意的：
//
//	事务一：建回合（状态 running）—— 让"这一轮已经开始"立刻可见
//	事务外：调模型 —— 真实模型下这里要跑几秒到几十秒，绝不能包在事务里持着锁等
//	事务二：写消息 + 挂到回合 + 收尾回合 + 刷新对话的最近消息时间 —— 一起成功或一起失败
//
// 事务二里那四步必须同生共死：只写消息不挂到回合，前端点开消息回溯不到是哪一轮产生的；
// 只挂不写消息，回合会指向一条不存在的记录。
//
// classroomID 一路传到上下文组装：共享记忆要按"课堂 + 对话"两个维度查。
func (o *Orchestrator) speak(
	ctx context.Context,
	request Request,
	run *entity.OrchestrationRun,
	classroomID uint64,
	participant Participant,
	turnNo int16,
	topic string,
) (TurnOutcome, error) {
	turn := &entity.AgentTurn{
		RunID:            run.ID,
		ClassroomAgentID: &participant.ClassroomAgentID,
		AgentSnapshot:    participant.snapshot(),
		Status:           entity.AgentTurnStatusRunning,
	}
	if err := o.deps.Tx.Run(ctx, func(ctx context.Context) error {
		if err := o.deps.Turns.CreateNext(ctx, turn); err != nil {
			return err
		}
		// 回合记录与"他开始说话了"这条事件同事务：回合没建成，
		// 前端就不该以为有人已经开口（否则圆桌上会挂着一个不存在的人）。
		return o.appendEvent(ctx, request.ConversationID, &run.ID, &turn.ID,
			entity.ConversationEventAgentStarted, agentStartedPayload{
				TurnID:    turn.ID,
				TurnNo:    turn.TurnNo,
				AgentID:   participant.ClassroomAgentID,
				AgentName: participant.Name,
			})
	}); err != nil {
		return TurnOutcome{}, fmt.Errorf("建立第 %d 个回合失败: %w", turnNo, err)
	}

	history, err := o.history(ctx, classroomID, request.ConversationID)
	if err != nil {
		o.abandonTurn(turn, err)
		return TurnOutcome{}, err
	}

	response, err := o.callModel(ctx, GenerationRequest{
		Participant: participant,
		Topic:       topic,
		TurnNo:      turnNo,
		History:     history,
	})
	if err != nil {
		o.abandonTurn(turn, err)
		return TurnOutcome{}, fmt.Errorf("第 %d 轮生成失败: %w", turnNo, err)
	}

	finishedAt := time.Now().UTC()
	message := &entity.ConversationMessage{
		ConversationID:   request.ConversationID,
		SenderType:       entity.MessageSenderAgent,
		ClassroomAgentID: &participant.ClassroomAgentID,
		SenderSnapshot:   participant.snapshot(),
		Content:          response.Content,
		Status:           entity.MessageStatusCompleted,
		TokenCount:       response.OutputTokens,
	}
	// 下一步动作必须落库：它是"这场讨论为什么停、下一轮该谁上"的唯一依据，
	// 也是事后查"讨论怎么跑偏了"的第一手线索。
	// 落库前先归一：库上有 CHECK 只认四个值，而模型可能回一句谁也想不到的话。
	nextAction := normalizeNextAction(response.NextAction)
	if err := o.deps.Tx.Run(ctx, func(ctx context.Context) error {
		if err := o.deps.Messages.AppendNext(ctx, message); err != nil {
			return err
		}
		// 正文与"说完了"两条事件必须跟着消息一起提交：它们的载荷里有 message_id，
		// 而且前端正是靠它们把这句话画上屏幕 —— 消息没落库，它们就该一起消失。
		//
		// 当前模型一次返回整段，所以 delta 只发一批；接上流式模型后同一个字段分多次发，
		// 这段代码要改成"边收边发"，但事件契约不变。
		if err := o.appendEvent(ctx, request.ConversationID, &run.ID, &turn.ID,
			entity.ConversationEventMessageDelta, messageDeltaPayload{
				TurnID:    turn.ID,
				MessageID: message.ID,
				Delta:     response.Content,
			}); err != nil {
			return err
		}
		if err := o.appendEvent(ctx, request.ConversationID, &run.ID, &turn.ID,
			entity.ConversationEventMessageCompleted, messageCompletedPayload{
				TurnID:     turn.ID,
				MessageID:  message.ID,
				Content:    response.Content,
				TokenCount: response.OutputTokens,
			}); err != nil {
			return err
		}
		if err := o.deps.Turns.AttachOutputMessage(ctx, turn.ID, message.ID); err != nil {
			return err
		}
		if err := o.deps.Turns.Finish(ctx, turn.ID, repository.TurnResult{
			Status:       entity.AgentTurnStatusCompleted,
			NextAction:   &nextAction,
			OutputTokens: response.OutputTokens,
			FinishedAt:   finishedAt,
		}); err != nil {
			return err
		}
		// 回合收尾的事件也在这笔事务里：它的载荷里有 message_id 与下一步动作，
		// 与前面几条是同一个"这一轮说完了"的完整交代。
		if err := o.appendEvent(ctx, request.ConversationID, &run.ID, &turn.ID,
			entity.ConversationEventAgentCompleted, agentCompletedPayload{
				TurnID:     turn.ID,
				TurnNo:     turn.TurnNo,
				MessageID:  message.ID,
				NextAction: nextAction,
			}); err != nil {
			return err
		}
		// 顺手把对话的"最近一条消息时间"往前推：这是课堂列表排序的依据，
		// 漏掉它会导致正在活跃的讨论被排到列表下面去。
		return o.deps.Conversations.TouchLastMessage(ctx, request.ConversationID, finishedAt)
	}); err != nil {
		o.abandonTurn(turn, err)
		return TurnOutcome{}, fmt.Errorf("落库第 %d 轮结果失败: %w", turnNo, err)
	}

	return TurnOutcome{
		TurnID:       turn.ID,
		TurnNo:       turn.TurnNo,
		AgentName:    participant.Name,
		MessageID:    message.ID,
		Content:      response.Content,
		OutputTokens: response.OutputTokens,
		NextAction:   nextAction,
	}, nil
}

// callModel 调一次模型；失败自动重试一次，两次都失败才把错误抛出去。
//
// 为什么要重试：真实环境里模型调用会因网络抖动、上游限流偶发失败，这类失败重试一次
// 通常就好了。不重试的话，一次抖动就意味着用户那句话永远得不到回应。
//
// 为什么只重试一次：重试解决的是"偶发"，不是"持续故障"。上游真挂了，快速失败比
// 长时间卡住更好 —— 前端在等，用户也在等。
//
// 重试的是"这一次模型调用"，不是"这一轮"：回合不重建、消息也还没写（消息是模型
// 成功返回之后才写的），所以重试不会留下重复记录。
func (o *Orchestrator) callModel(ctx context.Context, request GenerationRequest) (GenerationResponse, error) {
	const attempts = 2

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		response, err := o.deps.Model.Generate(ctx, request)
		if err == nil {
			return response, nil
		}
		lastErr = err

		if attempt < attempts {
			o.deps.Logger.Warn("模型调用失败，准备重试",
				zap.String("agent", request.Participant.Name),
				zap.Int16("turn_no", request.TurnNo),
				zap.Int("attempt", attempt),
				zap.Error(err),
			)
		}
	}
	// 错误信息里写明"已重试 N 次"：线上看到这句话就能立刻判断是偶发抖动还是持续故障，
	// 不用去翻日志确认到底重试没有。
	return GenerationResponse{}, fmt.Errorf("模型调用失败（已重试 %d 次）: %w", attempts-1, lastErr)
}

// history 读这次发言要带的上下文，按发生顺序排列。
//
// 保留这个名字和签名（只多了一个 classroomID），内部换成按预算组装的版本（context.go）：
// 调用点只有 speak 一处，换实现不必让它知道"现在有摘要、有压缩、有共享记忆"这些细节。
func (o *Orchestrator) history(ctx context.Context, classroomID uint64, conversationID uint64) ([]HistoryMessage, error) {
	return o.buildContext(ctx, classroomID, conversationID, o.deps.Logger)
}

// closeRun 写运行的终态。
//
// status 由调用方给，而不是从 stopReason 反推：收尾有"完成"和"等用户"两种，
// 反推的映射一旦漏一种，就会把没聊完的讨论写成已结束。
func (o *Orchestrator) closeRun(ctx context.Context, run *entity.OrchestrationRun, status string, stopReason string, cause error, log *zap.Logger) error {
	result := repository.RunResult{
		Status:     status,
		StopReason: stringPtr(stopReason),
		FinishedAt: time.Now().UTC(),
	}
	if cause != nil {
		result.Status = entity.RunStatusFailed
		result.ErrorMessage = stringPtr(truncate(cause.Error(), errorMessageLimit))
	}
	if err := o.deps.Runs.Finish(ctx, run.ID, result); err != nil {
		log.Error("写入运行终态失败", zap.Error(err))
		return fmt.Errorf("写入运行终态失败: %w", err)
	}
	return nil
}

// abandon 在失败时收拾现场：把运行标成失败，然后原样返回原因。
//
// 它**刻意不接收调用方的 context**：失败的原因很可能就是"上游超时/用户取消了"，
// 那个 ctx 已经作废，拿它去写库会立刻再失败一次，于是连"这次讨论失败了"都留不下来，
// 前端只能一直转圈。所以这里从 context.Background() 另起一个带超时的。
// 参数干脆不收，比"收下却不用"更不容易让人误会成"它会用调用方的 ctx 收尾"。
func (o *Orchestrator) abandon(run *entity.OrchestrationRun, cause error, outcomes []TurnOutcome, log *zap.Logger) (Result, error) {
	finalizeCtx, cancel := context.WithTimeout(context.Background(), finalizeTimeout)
	defer cancel()

	if err := o.closeRun(finalizeCtx, run, entity.RunStatusFailed, entity.RunStopError, cause, log); err != nil {
		log.Error("标记运行失败状态时又出错了", zap.Error(err))
	}

	// 失败也要让前端知道，否则它会一直转圈等一个不会来的结果。
	// 用 finalizeCtx：调用方的 ctx 很可能正是失败的原因（超时/取消），
	// 拿它去写事件会立刻再失败一次，连"这次失败了"都送不出去。
	o.emitEvent(finalizeCtx, run.ConversationID, &run.ID, nil,
		entity.ConversationEventRunFailed, runFailedPayload{
			Error: truncate(cause.Error(), errorMessageLimit),
		})

	return Result{
		RunID:      run.ID,
		TraceID:    run.TraceID,
		Status:     entity.RunStatusFailed,
		StopReason: entity.RunStopError,
		Turns:      outcomes,
	}, cause
}

// abandonTurn 尽力把一个没跑完的回合标成失败。
//
// 同样不接收调用方的 context，理由见 abandon。
// 这里**不覆盖**原始错误：回合状态写不进去只说明现场收拾得不干净，
// 真正让这次讨论崩掉的原因还在调用方的错误里，那个才是有用的信息。
func (o *Orchestrator) abandonTurn(turn *entity.AgentTurn, cause error) {
	finalizeCtx, cancel := context.WithTimeout(context.Background(), finalizeTimeout)
	defer cancel()

	message := truncate(cause.Error(), errorMessageLimit)
	err := o.deps.Turns.Finish(finalizeCtx, turn.ID, repository.TurnResult{
		Status:       entity.AgentTurnStatusFailed,
		ErrorMessage: &message,
		FinishedAt:   time.Now().UTC(),
	})
	if err != nil {
		o.deps.Logger.Error("标记回合失败状态时又出错了",
			zap.Uint64("turn_id", turn.ID), zap.Error(err))
	}
}

// normalizeMaxTurns 兜住最大回合数。
//
// 上界必须和数据库的 CHECK 一致，否则会在 INSERT 时才失败 ——
// 那时运行记录还没建、错误信息也只是一句约束冲突，看不出是参数传错了。
func normalizeMaxTurns(value int) (int, error) {
	if value == 0 {
		return defaultMaxTurns, nil
	}
	if value < minMaxTurns || value > maxMaxTurns {
		return 0, fmt.Errorf("最大回合数 %d 超出允许范围 %d~%d", value, minMaxTurns, maxMaxTurns)
	}
	return value, nil
}

// speakerName 从消息快照里取发言人的显示名。
//
// 用快照而不是去 join 角色表：角色后来改了名字，历史消息里显示的仍应是当时那个名字。
// 快照是空的（比如早期数据或系统消息）时才退到按发送者类型给个通用称呼。
func speakerName(message entity.ConversationMessage) string {
	if len(message.SenderSnapshot) > 0 {
		var snapshot struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(message.SenderSnapshot, &snapshot); err == nil && snapshot.Name != "" {
			return snapshot.Name
		}
	}
	switch message.SenderType {
	case entity.MessageSenderUser:
		return userSpeaker
	case entity.MessageSenderSystem:
		return "系统"
	default:
		return "某位角色"
	}
}

// newTraceID 生成 32 位十六进制追踪号，格式与库里的 CHECK 约束一致。
func newTraceID() string {
	var buffer [16]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		// 系统熵源出问题的概率极低，但不代表不会。退回时间戳：位数与格式仍然合法，
		// 单机本地也足够区分（同一纳秒内不会开两趟讨论）。
		return fmt.Sprintf("%032x", time.Now().UnixNano())
	}
	return hex.EncodeToString(buffer[:])
}

// truncate 按字节上限截断文本，并保证结果仍是合法 UTF-8。
//
// 直接切字节可能把一个汉字的三个字节切成两半，落库时 PostgreSQL 会拒绝这段无效编码。
// 所以截断后退到最后一个完整字符边界。
func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := text[:limit]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut + "…"
}

func stringPtr(value string) *string { return &value }
