package conversation

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"narra/internal/service"
	"narra/pkg/response"
	"narra/pkg/sse"
)

const (
	// streamEventError 是传输层错误帧的事件名。
	//
	// 它不是业务事件（那 9 种见 entity 的常量），表示"这条流自身出了问题，推完即关"：
	// 服务端查库失败、或对话被删。前端据此把等待结束掉，而不是一直挂着。
	streamEventError = "error"

	// defaultEventsBatchSize 单次查库的条数上限。
	//
	// 重连补历史时可能一次积压几百条，按批取、推完一批立刻取下一批，
	// 既不把整条对话的事件读进内存，也不会在补历史时被 500ms 的轮询节奏拖慢。
	defaultEventsBatchSize = 500

	// defaultEventsPollInterval 空闲时查库的节奏：新事件从落库到推给前端最多晚这么久。
	// 与上传进度流同一个取舍 —— 事件表是唯一事实来源，轮询它比让生产者跨包回调简单；
	// 将来若要换成 LISTEN/NOTIFY 或进程内总线，这个端点的对外契约不用变。
	defaultEventsPollInterval = 500 * time.Millisecond

	// defaultEventsHeartbeatInterval 心跳间隔。
	//
	// 对话流可能长时间没有事件（用户在打字、Agent 在思考），空闲连接会被反代与浏览器
	// 掐掉；写心跳失败也正好是"对端已断开"的检测点。
	defaultEventsHeartbeatInterval = 20 * time.Second
)

// Controller 是对话事件流的 HTTP 处理器。
//
// 它只做三件事：校验对话、把事件按 SSE 帧写出去、断线续传的起点解析。
// 事件的产生（谁在什么时候写什么事件）不属于这里。
type Controller struct {
	svc service.ConversationService

	// 节奏参数。零值由 NewController 补成 default* 默认值；测试调小它们，
	// 否则一条事件要等半秒、一次心跳要等二十秒才能观察完。
	batchSize         int
	pollInterval      time.Duration
	heartbeatInterval time.Duration
}

// NewController 创建对话事件流处理器。
func NewController(svc service.ConversationService) *Controller {
	return &Controller{
		svc:               svc,
		batchSize:         defaultEventsBatchSize,
		pollInterval:      defaultEventsPollInterval,
		heartbeatInterval: defaultEventsHeartbeatInterval,
	}
}

// Events 用 SSE 推送一条对话的事件流 —— 也就是 Agent 的执行过程与最终结果。
//
// 契约（前端 api/conversation.ts 的 watchConversationEvents 按它实现）：
//   - 一条事件一个帧：id = sequence_no，event = event_type，
//     data = response.ConversationEvent（自描述，含 run / turn 与 payload）；
//   - 断线续传：重连时带 Last-Event-ID 头（EventSource 重连会自动带）或 ?after=N，
//     服务端从 N 之后接着推；两个都不带就是从 0 重放（事件只保留 7 天）；
//   - 空闲时每 20 秒一行注释心跳；服务端自身出错推一帧 error 后关流；
//   - 流不会在 run.completed / run.failed 时自动关闭：对话是长生命周期的资源，
//     收不收由客户端决定（跑完一轮就关，或留着等下一轮，两种用法都成立）。
func (c *Controller) Events(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 {
		response.BadRequest(ctx, "对话 ID 无效")
		return
	}

	requestCtx := ctx.Request.Context()
	// 存在性检查必须在 SSE 头之前：响应头一旦发出去，错误就只剩"事件流里的 error 帧"
	// 这一种表达方式，而"对话不存在"本该是一条普通的 JSON 错误。
	exists, err := c.svc.Exists(requestCtx, id)
	if err != nil {
		response.InternalError(ctx, err.Error())
		return
	}
	if !exists {
		response.NotFound(ctx, "对话不存在")
		return
	}

	after := resumeSequence(ctx)

	sse.Start(ctx)

	poll := time.NewTicker(c.pollInterval)
	defer poll.Stop()
	heartbeat := time.NewTicker(c.heartbeatInterval)
	defer heartbeat.Stop()

	for {
		// 补历史可能要推很多批，中途客户端断开时不必等写完最后一批才发现。
		if requestCtx.Err() != nil {
			return
		}

		events, err := c.svc.ListEventsAfter(requestCtx, id, after, c.batchSize)
		if err != nil {
			_ = sse.Event(ctx, streamEventError, gin.H{"message": err.Error()})
			return
		}

		for _, event := range events {
			payload, err := json.Marshal(event)
			if err != nil {
				// 纯数据结构序列化不会失败；真失败了也不该把坏帧写上线。
				_ = sse.Event(ctx, streamEventError, gin.H{"message": "事件序列化失败"})
				return
			}
			// id = sequence_no：断线续传的起点，也是前端去重与排序的依据。
			if sse.EventWithID(ctx, event.SequenceNo, event.EventType, payload) != nil {
				return
			}
			after = event.SequenceNo
		}

		// 取满一批说明后面还有积压，立刻继续取；否则回到"等新事件"的节奏。
		if len(events) == c.batchSize {
			continue
		}

		select {
		case <-requestCtx.Done():
			// 客户端断开了（切页面、主动 abort）：结束循环，不用再写。
			return
		case <-heartbeat.C:
			if sse.Heartbeat(ctx) != nil {
				return
			}
		case <-poll.C:
		}
	}
}

// resumeSequence 解析断线续传的起点。
//
// 优先 Last-Event-ID 头：浏览器原生 EventSource 重连时会自动带上，前端不用做任何事；
// 其次 ?after= 查询参数：fetch 版客户端（我们的 watchConversationEvents）用它。
// 都缺失或非法时返回 0 —— 从头重放，而不是报错：一次重连把历史补全，比让用户
// 手动刷新更能接受，而且客户端本来就会去重（按 sequence_no）。
func resumeSequence(ctx *gin.Context) int64 {
	raw := strings.TrimSpace(ctx.GetHeader("Last-Event-ID"))
	if raw == "" {
		raw = strings.TrimSpace(ctx.Query("after"))
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0
	}
	return value
}
