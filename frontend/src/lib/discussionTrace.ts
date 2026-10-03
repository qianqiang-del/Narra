import type { ConversationEvent } from '@/api/conversation'

export type TraceStepStatus = 'done' | 'active' | 'waiting' | 'error'

export interface TraceStep {
  id: string
  title: string
  detail?: string
  status: TraceStepStatus
  agentName?: string
  turnNo?: number
  timestamp: string
}

export interface DiscussionTrace {
  status: 'idle' | 'running' | 'waiting' | 'completed' | 'failed'
  steps: TraceStep[]
  currentAgent?: string
  completedTurns: number
}

export function createDiscussionTrace(): DiscussionTrace {
  return { status: 'idle', steps: [], completedTurns: 0 }
}

function addStep(trace: DiscussionTrace, step: Omit<TraceStep, 'timestamp'>, timestamp: string) {
  trace.steps.push({ ...step, timestamp })
}

function finishActive(trace: DiscussionTrace) {
  for (const step of trace.steps) if (step.status === 'active') step.status = 'done'
}

export function applyTraceEvent(trace: DiscussionTrace, event: ConversationEvent): void {
  const timestamp = event.createdAt
  switch (event.eventType) {
    case 'run.started':
      trace.status = 'running'
      trace.currentAgent = undefined
      trace.completedTurns = 0
      trace.steps = []
      addStep(trace, { id: `run-${event.sequenceNo}`, title: '讨论已开始', status: 'done', detail: `${event.payload.participants.length} 位参与者已就位` }, timestamp)
      break
    case 'director.decision':
      finishActive(trace)
      addStep(trace, { id: `decision-${event.sequenceNo}`, title: `选择 ${event.payload.agent_name} 发言`, status: 'done', detail: event.payload.reason, agentName: event.payload.agent_name, turnNo: event.payload.turn_no }, timestamp)
      break
    case 'agent.started':
      finishActive(trace)
      trace.status = 'running'
      trace.currentAgent = event.payload.agent_name
      addStep(trace, { id: `turn-${event.payload.turn_id}`, title: `${event.payload.agent_name} 正在思考`, status: 'active', agentName: event.payload.agent_name, turnNo: event.payload.turn_no }, timestamp)
      break
    case 'message.completed': {
      const step = trace.steps.find((item) => item.id === `turn-${event.payload.turn_id}`)
      if (step) {
        step.status = 'done'
        step.title = `${step.agentName ?? '角色'} 完成发言`
      }
      break
    }
    case 'agent.completed':
      trace.completedTurns = Math.max(trace.completedTurns, event.payload.turn_no)
      trace.currentAgent = undefined
      break
    case 'run.waiting_user':
      finishActive(trace)
      trace.status = 'waiting'
      addStep(trace, { id: `waiting-${event.sequenceNo}`, title: '等待你的回复', status: 'waiting', detail: '你可以继续追问或结束本次讨论' }, timestamp)
      break
    case 'run.completed':
      finishActive(trace)
      trace.status = 'completed'
      trace.currentAgent = undefined
      addStep(trace, { id: `completed-${event.sequenceNo}`, title: '讨论已完成', status: 'done', detail: `共完成 ${event.payload.turns} 轮` }, timestamp)
      break
    case 'run.failed':
      finishActive(trace)
      trace.status = 'failed'
      trace.currentAgent = undefined
      addStep(trace, { id: `failed-${event.sequenceNo}`, title: '讨论暂时中断', status: 'error', detail: '模型服务暂时不可用，请稍后重试' }, timestamp)
      break
    case 'message.delta':
      break
  }
}
