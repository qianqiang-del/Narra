import assert from 'node:assert/strict'
import test from 'node:test'

import { discussionStatus } from '../src/lib/discussionAppearance.ts'

test('speaker takes priority while a discussion is running', () => {
  assert.equal(discussionStatus({ running: true, sending: false, thinking: true, yourTurn: false, speakingName: '陈老师' }), 'speaking')
})

test('processing and user turn have distinct states', () => {
  assert.equal(discussionStatus({ running: false, sending: true, thinking: false, yourTurn: false }), 'working')
  assert.equal(discussionStatus({ running: false, sending: false, thinking: false, yourTurn: true }), 'waiting')
})

test('an idle conversation does not claim to be running', () => {
  assert.equal(discussionStatus({ running: false, sending: false, thinking: false, yourTurn: false }), 'ready')
})
