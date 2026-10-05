import assert from 'node:assert/strict'
import test from 'node:test'

import { parseMarkdown } from '../src/lib/markdown.ts'

test('parses aligned tables with safe inline cells and escaped/code pipes', () => {
  const [table] = parseMarkdown('| Name | Value |\n| :--- | ---: |\n| **A** | `a|b` |\n| x\\|y | <img onerror=alert(1)> |')
  assert.equal(table.type, 'table')
  assert.deepEqual(table.alignments, ['left', 'right'])
  assert.deepEqual(table.rows[0][1], [{ type: 'code', value: 'a|b' }])
  assert.deepEqual(table.rows[1][0], [{ type: 'text', value: 'x|y' }])
  assert.deepEqual(table.rows[1][1], [{ type: 'text', value: '<img onerror=alert(1)>' }])
})

test('tables accept optional outside pipes and pad short rows', () => {
  const [table, paragraph] = parseMarkdown('A | B\n--- | :---:\none |\n\nAfter')
  assert.equal(table.type, 'table')
  assert.equal(table.rows[0].length, 2)
  assert.deepEqual(table.rows[0][1], [])
  assert.equal(paragraph.type, 'paragraph')
})

test('incomplete separators and fenced tables stay literal', () => {
  assert.equal(parseMarkdown('| A | B |\n| -- |')[0].type, 'paragraph')
  assert.equal(parseMarkdown('```\nA | B\n--- | ---\n```')[0].type, 'codeBlock')
})

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
