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

test('history restores whiteboards in message order and ignores malformed metadata', () => {
  const display = createDiscussionDisplay([
    { id: 10, senderType: 'agent', senderSnapshot: { name: '陈老师', role: '主讲' }, content: '第一张', whiteboard: { title: '先看这里', kind: 'steps', content: '步骤一' } },
    { id: 11, senderType: 'agent', senderSnapshot: { name: '小助手', role: '辅助' }, content: '无白板', whiteboard: null },
    { id: 12, senderType: 'agent', senderSnapshot: { name: '笔记君', role: '记录' }, content: '空白板', whiteboard: { title: ' ', kind: 'table', content: '内容' } },
    { id: 13, senderType: 'agent', senderSnapshot: { name: '杠精同学', role: '质疑' }, content: '错误白板', whiteboard: { title: '缺内容', kind: 'table' } },
    { id: 14, senderType: 'agent', senderSnapshot: { name: '好奇宝宝', role: '提问' }, content: '第二张', whiteboard: { title: '再看这里', kind: 'concepts', content: '概念' } },
  ])

  assert.deepEqual(display.whiteboards, [
    { id: 'whiteboard-10', messageId: 10, title: '先看这里', kind: 'steps', content: '步骤一' },
    { id: 'whiteboard-14', messageId: 14, title: '再看这里', kind: 'concepts', content: '概念' },
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

test('message.completed adds a whiteboard and ignores replayed sequence numbers', () => {
  const display = createDiscussionDisplay([])
  applyDiscussionEvent(display, event(1, 'agent.started', { turn_id: 2, agent_name: '小明', agent_id: 3 }))
  applyDiscussionEvent(display, event(2, 'message.completed', {
    turn_id: 2,
    message_id: 20,
    content: '完整正文',
    whiteboard: { title: '关键结论', kind: 'concepts', content: '结论内容' },
  }))
  applyDiscussionEvent(display, event(2, 'message.completed', {
    turn_id: 2,
    message_id: 20,
    content: '不应覆盖',
    whiteboard: { title: '重复事件', kind: 'steps', content: '不应新增' },
  }))

  assert.deepEqual(display.whiteboards, [
    { id: 'whiteboard-20', messageId: 20, title: '关键结论', kind: 'concepts', content: '结论内容' },
  ])
  assert.equal(display.bubbles[0].text, '完整正文')
  assert.equal(display.lastSequence, 2)
})

test('message.completed ignores an empty whiteboard without failing the event', () => {
  const display = createDiscussionDisplay([])
  applyDiscussionEvent(display, event(1, 'agent.started', { turn_id: 2, agent_name: '小明', agent_id: 3 }))
  applyDiscussionEvent(display, event(2, 'message.completed', {
    turn_id: 2,
    message_id: 20,
    content: '完整正文',
    whiteboard: { title: '', kind: 'steps', content: '   ' },
  }))

  assert.deepEqual(display.whiteboards, [])
  assert.equal(display.bubbles[0].text, '完整正文')
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

test('classroom role keys identify the speaker even when the role label is Chinese', () => {
  const display = createDiscussionDisplay([])
  applyDiscussionEvent(display, event(1, 'run.started', { participants: [
    { agent_id: 31, agent_key: 'teacher', name: '陈老师', role: '主讲', role_type: 'teacher' },
    { agent_id: 32, agent_key: 'curious', name: '好奇宝宝', role: '提问', role_type: 'student' },
  ] }))
  applyDiscussionEvent(display, event(2, 'agent.started', { agent_id: 31, turn_id: 10 }))
  assert.equal(display.speaking, 'teacher')
  assert.equal(display.speakingAgentKey, 'teacher')
  applyDiscussionEvent(display, event(3, 'agent.started', { agent_id: 32, turn_id: 11 }))
  assert.equal(display.speakingAgentKey, 'curious')
  applyDiscussionEvent(display, event(4, 'message.delta', { turn_id: 11, message_id: 12, delta: '我想问' }))
  assert.equal(display.bubbles[0].name, '好奇宝宝')
  assert.equal(display.bubbles[0].agentKey, 'curious')
  applyDiscussionEvent(display, event(5, 'run.completed', {}))
  assert.equal(display.speakingAgentKey, null)
})
