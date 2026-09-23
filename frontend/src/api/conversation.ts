import { ApiError, CODE_STREAM, streamEvents } from './client'

/**
 * 对话事件流接口（SSE）：Agent 的执行过程与最终结果。
 *
 * 契约见后端 `internal/api/v1/conversation/controller.go` 的 Events；
 * payload 的形状与 `internal/agent/discussion/events.go` 一一对应 ——
 * 改字段要两边一起改（事件一旦写进库就按当时的样子重放，老事件不会跟着变形）。
 *
 * 一条事件一个帧：`id` 是 sequence_no（断线续传的起点），`event` 是事件类型，
 * `data` 是自描述信封（序号、run / turn、payload、时间）。
 *
 * 断线续传：把最后收到的 sequenceNo 当 `after` 重新订阅，服务端从它之后接着推；
 * 服务端只保留最近 7 天的事件，更早的历史以 conversation_messages 为准。
 */

// ---------- 各事件类型的 payload ----------

/** run.started：这一趟讨论开始了 */
export interface RunStartedPayload {
  trigger_message_id: number
  max_turns: number
  participants: ParticipantPayload[]
}

/** 圆桌成员的展示快照（发言当时的样子） */
export interface ParticipantPayload {
  agent_id: number
  name: string
  role: string
}

/** director.decision：下一个该谁、为什么 */
export interface DirectorDecisionPayload {
  turn_no: number
  agent_id: number
  agent_name: string
  reason: string
}

/** agent.started：某个回合开始 */
export interface AgentStartedPayload {
  turn_id: number
  turn_no: number
  agent_id: number
  agent_name: string
}

/** message.delta：正文增量（当前模型非流式，一条消息就是一批） */
export interface MessageDeltaPayload {
  turn_id: number
  message_id: number
  delta: string
}

/** message.completed：一条消息完成，带完整正文供对齐 */
export interface MessageCompletedPayload {
  turn_id: number
  message_id: number
  content: string
  token_count: number
}

/** agent.completed：某个回合结束 */
export interface AgentCompletedPayload {
  turn_id: number
  turn_no: number
  message_id: number
  next_action: string
}

/** run.waiting_user：整趟讨论挂起等用户 */
export interface RunWaitingUserPayload {
  reason: string
}

/** run.completed：整趟讨论正常收尾 */
export interface RunCompletedPayload {
  stop_reason: string
  turns: number
}

/** run.failed：整趟讨论失败 */
export interface RunFailedPayload {
  error: string
}

/** 事件类型 → payload 形状 */
export interface ConversationEventPayloads {
  'run.started': RunStartedPayload
  'director.decision': DirectorDecisionPayload
  'agent.started': AgentStartedPayload
  'message.delta': MessageDeltaPayload
  'message.completed': MessageCompletedPayload
  'agent.completed': AgentCompletedPayload
  'run.waiting_user': RunWaitingUserPayload
  'run.completed': RunCompletedPayload
  'run.failed': RunFailedPayload
}

export type ConversationEventType = keyof ConversationEventPayloads

/**
 * 一条事件。
 *
 * 它是按 eventType 分发的联合类型：`switch (event.eventType)` 之后 payload 会自动
 * 收窄成对应形状，不需要手写断言。
 */
export type ConversationEvent = {
  [K in ConversationEventType]: {
    /** 对话内序号，从 1 开始且单调递增（允许空洞）；断线续传把它当 after */
    sequenceNo: number
    eventType: K
    /** 所属编排运行；与运行无关的事件为 null */
    runId: number | null
    /** 所属 Agent 回合；与回合无关的事件为 null */
    turnId: number | null
    payload: ConversationEventPayloads[K]
    createdAt: string
  }
}[ConversationEventType]

/** 后端 `response.ConversationEvent` 的原样形状（data 行） */
interface ConversationEventDTO {
  sequence_no: number
  event_type: string
  run_id?: number
  turn_id?: number
  payload?: unknown
  created_at: string
}

function toConversationEvent(d: ConversationEventDTO): ConversationEvent {
  // 这里的断言无法避免：event_type 与 payload 的对应关系在类型系统里是运行时数据，
  // 服务端保证两者一致（同一个生产者写的事件）。
  return {
    sequenceNo: d.sequence_no,
    eventType: d.event_type as ConversationEventType,
    runId: d.run_id ?? null,
    turnId: d.turn_id ?? null,
    payload: (d.payload ?? {}) as ConversationEventPayloads[ConversationEventType],
    createdAt: d.created_at,
  } as ConversationEvent
}

export interface WatchConversationOptions {
  /**
   * 从哪个序号之后开始收；省略或 0 表示从头重放。
   * 断线重连时传"最后收到的 sequenceNo"，不会重复收到旧事件。
   */
  after?: number
  /** 主动断开流用（组件卸载、切页）。断开后服务端下一次写就能发现 */
  signal?: AbortSignal
}

/**
 * 订阅一条对话的事件流，逐条产出。
 *
 * 流**不会**在 `run.completed` / `run.failed` 时自动结束：对话是长生命周期的资源，
 * 收不收由调用方决定 —— 跑完一轮就 `break`（下次带 `after` 重连续传），
 * 或留着继续等下一轮。服务端推 `error` 帧表示流自身出错，这里抛 ApiError。
 */
export async function* watchConversationEvents(
  conversationID: number,
  options: WatchConversationOptions = {},
): AsyncGenerator<ConversationEvent> {
  const query = options.after && options.after > 0 ? `?after=${options.after}` : ''
  const path = `/conversations/${conversationID}/events${query}`

  for await (const message of streamEvents(path, options.signal)) {
    if (message.event === 'error') {
      const payload = JSON.parse(message.data) as { message?: string }
      throw new ApiError(CODE_STREAM, payload.message || '事件流已中断')
    }
    yield toConversationEvent(JSON.parse(message.data) as ConversationEventDTO)
  }
}
