import assert from 'node:assert/strict'
import test from 'node:test'

import { applyDiscussionEvent, createDiscussionDisplay } from '../src/lib/classroomDiscussion.ts'

function event(sequenceNo, eventType, payload) {
  return { sequenceNo, eventType, payload, runId: 1, turnId: payload.turn_id ?? null, createdAt: '2026-09-29T00:00:00Z' }
}

test('history restores speaker snapshots and user messages', () => {
  const display = createDiscussionDisplay([
    { id: 10, senderType: 'user', senderSnapshot: {}, content: '为什么？' },
    { id: 11, senderType: 'agent', senderSnapshot: { name: '张老师', role: 'teacher' }, content: '因为如此。' },
    { id: 12, senderType: 'agent', senderSnapshot: { name: '小明', role: 'student' }, content: '我有问题。' },
  ])
  assert.deepEqual(display.bubbles, [
    { id: 'message-10', from: 'user', text: '为什么？' },
    { id: 'message-11', from: 'teacher', name: '张老师', text: '因为如此。' },
    { id: 'message-12', from: 'agent', name: '小明', text: '我有问题。' },
  ])
})

test('SSE deltas append once and completed content replaces the stream', () => {
  const display = createDiscussionDisplay([])
  applyDiscussionEvent(display, event(1, 'agent.started', { turn_id: 2, agent_name: '小明', agent_id: 3 }))
  applyDiscussionEvent(display, event(2, 'message.delta', { turn_id: 2, message_id: 20, delta: '第一' }))
  applyDiscussionEvent(display, event(3, 'message.delta', { turn_id: 2, message_id: 20, delta: '第二' }))
  applyDiscussionEvent(display, event(4, 'message.completed', { turn_id: 2, message_id: 20, content: '完整正文' }))
  applyDiscussionEvent(display, event(3, 'message.delta', { turn_id: 2, message_id: 20, delta: '第二' }))
  assert.deepEqual(display.bubbles, [{ id: 'message-20', from: 'agent', name: '小明', text: '完整正文' }])
  assert.equal(display.lastSequence, 4)
})

test('replaying old deltas never duplicates a history message', () => {
  const display = createDiscussionDisplay([{ id: 20, senderType: 'agent', senderSnapshot: { name: '张老师', role: 'teacher' }, content: '已经保存' }])
  applyDiscussionEvent(display, event(1, 'message.delta', { turn_id: 2, message_id: 20, delta: '旧增量' }))
  assert.equal(display.bubbles[0].text, '已经保存')
})

test('agent completion clears the current speaker while the run continues', () => {
  const display = createDiscussionDisplay([])
  applyDiscussionEvent(display, event(1, 'agent.started', { turn_id: 2, turn_no: 1, agent_id: 3, agent_name: '小明' }))
  assert.equal(display.speaking, 'agent')

  applyDiscussionEvent(display, event(2, 'agent.completed', { turn_id: 2, turn_no: 1, message_id: 20, next_action: 'switch_agent' }))
  assert.equal(display.speaking, null)
  assert.equal(display.thinking, true)
})

test('run.failed becomes a visible error and clears waiting state', () => {
  const display = createDiscussionDisplay([])
  applyDiscussionEvent(display, event(1, 'run.started', { trigger_message_id: 5, max_turns: 6, participants: [] }))
  applyDiscussionEvent(display, event(2, 'run.failed', { error: '模型连接失败' }))
  assert.equal(display.thinking, false)
  assert.deepEqual(display.bubbles, [{ id: 'run-error-2', from: 'agent', name: '系统', text: '模型连接失败' }])
})
