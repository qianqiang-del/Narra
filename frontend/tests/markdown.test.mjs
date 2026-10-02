import assert from 'node:assert/strict'
import test from 'node:test'

import { parseMarkdown } from '../src/lib/markdown.ts'

test('normalizes escaped emphasis and unordered lists', () => {
  const nodes = parseMarkdown('\\*\\*重点\\*\\*\n\\- 第一项\n\\- 第二项')

  assert.deepEqual(nodes, [
    { type: 'paragraph', children: [{ type: 'strong', children: [{ type: 'text', value: '重点' }] }] },
    { type: 'list', ordered: false, items: [
      [{ type: 'text', value: '第一项' }],
      [{ type: 'text', value: '第二项' }],
    ] },
  ])
})

test('keeps incomplete streaming emphasis as plain text', () => {
  assert.deepEqual(parseMarkdown('正在输出 **未完成'), [
    { type: 'paragraph', children: [{ type: 'text', value: '正在输出 **未完成' }] },
  ])
})

test('parses headings, ordered lists, inline code, and fenced code', () => {
  const nodes = parseMarkdown('# 标题\n1. 步骤\n\n说明 `value`\n\n```go\nfmt.Println("ok")\n```')

  assert.equal(nodes[0].type, 'heading')
  assert.equal(nodes[1].type, 'list')
  assert.deepEqual(nodes[2], { type: 'paragraph', children: [
    { type: 'text', value: '说明 ' },
    { type: 'code', value: 'value' },
  ] })
  assert.deepEqual(nodes[3], { type: 'codeBlock', language: 'go', value: 'fmt.Println("ok")' })
})

test('keeps HTML as text instead of executable markup', () => {
  const nodes = parseMarkdown('<script>alert(1)</script>')

  assert.deepEqual(nodes, [
    { type: 'paragraph', children: [{ type: 'text', value: '<script>alert(1)</script>' }] },
  ])
})
