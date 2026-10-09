import assert from 'node:assert/strict'
import test from 'node:test'
import { h, nextTick } from 'vue'
import { createI18n } from 'vue-i18n'
import { browserEvents, findAll, node, renderer } from './helpers/vueHarness.mjs'

const { default: KnowledgeRow } = await import('../src/components/knowledge/KnowledgeRow.vue')

function materialDocument(overrides = {}) {
  return {
    id: 1, kind: 'material', title: 'GC垃圾回收.docx', sourceUri: 'GC垃圾回收.docx',
    enabled: true, status: 'ready', stage: '', failedStage: '', error: '',
    parser: 'docling', chunks: 17, characters: 10234,
    expiresAt: null, updatedAt: '2026-10-03T12:11:58Z', sourceType: 'import',
    ...overrides,
  }
}

function mountRow(document) {
  const root = node('#root')
  const app = renderer.createApp({ render: () => h(KnowledgeRow, { document }) })
  app.use(
    createI18n({
      legacy: false, locale: 'zh-CN', missingWarn: false, fallbackWarn: false,
      messages: {
        'zh-CN': { knowledge: { material: { pending: '待使用', associated: '已关联' } } },
      },
    }),
  )
  app.mount(root)
  return { root, app }
}

function tagElement(root, label) {
  const tag = findAll(root, (item) => item.children.some((child) => child.text === label))[0]
  assert.ok(tag, `找不到标签：${label}`)
  return tag
}

test('material lifecycle tags distinguish pending from associated', async () => {
  const restore = browserEvents()

  const pending = mountRow(materialDocument({ id: 1, expiresAt: '2026-10-10T20:10:20Z' }))
  try {
    await nextTick()
    assert.match(tagElement(pending.root, '待使用').props.class, /border-gold-200/)
  } finally {
    pending.app.unmount()
  }

  const associated = mountRow(materialDocument({ id: 2, expiresAt: null }))
  try {
    await nextTick()
    const tag = tagElement(associated.root, '已关联')
    assert.match(tag.props.class, /border-brand-200/)
    assert.doesNotMatch(tag.props.class, /border-gold-200/)
  } finally {
    associated.app.unmount()
    restore()
  }
})
