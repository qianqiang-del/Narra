import { request } from './client'

export interface DiscussionRun {
  id: number
  traceId: string
  status: string
  stopReason?: string
  modelId?: string
  startedAt?: string
  finishedAt?: string
  durationMs?: number
  turnCount: number
  inputTokens: number
  outputTokens: number
  totalTokens: number
  tokenSource: string
  estimatedCost?: number
  currency?: string
  errorMessage?: string
  createdAt: string
}

export interface TraceSpan {
  id: number
  spanId: string
  parentSpanId?: string
  kind: string
  name: string
  status: string
  startedAt: string
  endedAt?: string
  durationMs?: number
  turnId?: number
  inputTokens?: number
  outputTokens?: number
  attempt?: number
  turnNo?: number
  turns?: number
  agentName?: string
  nextAction?: string
  stopReason?: string
  inputSummary?: string
  outputSummary?: string
  errorMessage?: string
}

export interface DiscussionTrace { run: DiscussionRun; spans: TraceSpan[] }

interface DiscussionRunDTO {
  id: number; trace_id: string; status: string; stop_reason?: string; model_id?: string
  started_at?: string; finished_at?: string; duration_ms?: number; turn_count: number
  input_tokens: number; output_tokens: number; total_tokens: number; token_source: string
  estimated_cost?: number; currency?: string; error_message?: string; created_at: string
}
interface TraceSpanDTO {
  id: number; span_id: string; parent_span_id?: string; kind: string; name: string; status: string
  started_at: string; ended_at?: string; duration_ms?: number; turn_id?: number
  input_tokens?: number; output_tokens?: number; attempt?: number
  turn_no?: number; turns?: number; agent_name?: string; next_action?: string; stop_reason?: string
  input_summary?: string; output_summary?: string; error_message?: string
}
interface DiscussionTraceDTO { run: DiscussionRunDTO; spans: TraceSpanDTO[] }

function toRun(item: DiscussionRunDTO): DiscussionRun {
  return { id: item.id, traceId: item.trace_id, status: item.status, stopReason: item.stop_reason, modelId: item.model_id, startedAt: item.started_at, finishedAt: item.finished_at, durationMs: item.duration_ms, turnCount: item.turn_count, inputTokens: item.input_tokens, outputTokens: item.output_tokens, totalTokens: item.total_tokens, tokenSource: item.token_source, estimatedCost: item.estimated_cost, currency: item.currency, errorMessage: item.error_message, createdAt: item.created_at }
}
function toTrace(item: DiscussionTraceDTO): DiscussionTrace {
  return { run: toRun(item.run), spans: item.spans.map((span) => ({ id: span.id, spanId: span.span_id, parentSpanId: span.parent_span_id, kind: span.kind, name: span.name, status: span.status, startedAt: span.started_at, endedAt: span.ended_at, durationMs: span.duration_ms, turnId: span.turn_id, inputTokens: span.input_tokens, outputTokens: span.output_tokens, attempt: span.attempt, turnNo: span.turn_no, turns: span.turns, agentName: span.agent_name, nextAction: span.next_action, stopReason: span.stop_reason, inputSummary: span.input_summary, outputSummary: span.output_summary, errorMessage: span.error_message })) }
}

export function fetchDiscussionRuns(conversationId: number, limit = 20): Promise<DiscussionRun[]> {
  return request<DiscussionRunDTO[]>(`/conversations/${conversationId}/runs?limit=${limit}`).then((items) => items.map(toRun))
}
export function fetchDiscussionTrace(conversationId: number, runId: number): Promise<DiscussionTrace> {
  return request<DiscussionTraceDTO>(`/conversations/${conversationId}/runs/${runId}/trace`).then(toTrace)
}
