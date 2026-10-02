import assert from 'node:assert/strict'
import test from 'node:test'

import { nextVisibleText } from '../src/lib/typewriter.ts'

test('typewriter reveals one character for a normal backlog', () => {
  assert.equal(nextVisibleText('你', '你好世界', 20), '你好')
})

test('typewriter accelerates when a long answer is queued', () => {
  const target = 'a'.repeat(400)
  const next = nextVisibleText('', target, 20)
  assert.ok(next.length > 1)
  assert.ok(next.length <= 8)
})

test('typewriter never loses the final target text', () => {
  const once = nextVisibleText('已有内容', '已有内容补完', 20)
  assert.equal(nextVisibleText(once, '已有内容补完', 20), '已有内容补完')
})
