import assert from 'node:assert/strict'
import test from 'node:test'

import { applyTraceEvent, createDiscussionTrace } from '../src/lib/discussionTrace.ts'

function event(sequenceNo, eventType, payload) {
  return { sequenceNo, eventType, payload, runId: 1, turnId: payload.turn_id ?? null, createdAt: '2026-09-29T00:00:00Z' }
}

test('trace condenses a discussion into readable public stages', () => {
  const trace = createDiscussionTrace()
  applyTraceEvent(trace, event(1, 'run.started', { participants: [{ name: '陈老师' }, { name: '小助手' }] }))
  applyTraceEvent(trace, event(2, 'director.decision', { turn_no: 1, agent_name: '陈老师', reason: '先建立概念' }))
  applyTraceEvent(trace, event(3, 'agent.started', { turn_id: 9, turn_no: 1, agent_name: '陈老师' }))
  applyTraceEvent(trace, event(4, 'message.completed', { turn_id: 9, message_id: 20, content: '正文', token_count: 3 }))
  applyTraceEvent(trace, event(5, 'agent.completed', { turn_id: 9, turn_no: 1, message_id: 20, next_action: 'end' }))
  applyTraceEvent(trace, event(6, 'run.completed', { stop_reason: '完成', turns: 1 }))

  assert.equal(trace.status, 'completed')
  assert.equal(trace.completedTurns, 1)
  assert.equal(trace.steps.at(-1).title, '讨论已完成')
  assert.equal(trace.steps.find((step) => step.id === 'turn-9').status, 'done')
  assert.equal(trace.steps.some((step) => step.detail === '先建立概念'), true)
})

test('trace hides raw failure details from the user', () => {
  const trace = createDiscussionTrace()
  applyTraceEvent(trace, event(1, 'run.failed', { error: 'proxyconnect tcp: dial tcp 127.0.0.1:9' }))

  assert.equal(trace.status, 'failed')
  assert.equal(trace.steps[0].detail, '模型服务暂时不可用，请稍后重试')
  assert.equal(trace.steps[0].detail.includes('127.0.0.1'), false)
})
