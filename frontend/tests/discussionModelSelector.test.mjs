import assert from 'node:assert/strict'
import test from 'node:test'
import { h, nextTick, reactive } from 'vue'
import { browserEvents, findAll, node, renderer } from './helpers/vueHarness.mjs'

const rows = [{ provider_id: 1, provider_name: 'A', model_id: 'chat' }, { provider_id: 2, provider_name: 'B', model_id: 'chat' }]
const settings = (id) => ({ llm_provider_id: id, llm_model_id: 'chat', provider_name: id === 1 ? 'A' : 'B', source: 'classroom', available: true })
const response = (data, code = 0) => ({ json: async () => ({ code, data, message: code ? 'save failed' : '' }) })
const flush = async () => { for (let i = 0; i < 12; i++) { await Promise.resolve(); await nextTick() } }
function trigger(root) { return findAll(root, (n) => n.props['data-testid'] === 'model-picker-trigger')[0] }
async function chooseB(root) {
  trigger(root).props.onClick()
  await nextTick()
  findAll(root, (n) => n.props['data-provider-id'] === 2)[0].props.onClick()
  await nextTick()
  findAll(root, (n) => n.type === 'input' && n.props.type === 'radio')[0].props.onChange()
  await flush()
}

test('classroom selector loads persisted choice, saves only after confirmation, and retains choice on save failure', async () => {
  const { default: Selector } = await import('../src/components/classroom/DiscussionModelSelector.vue')
  const restore = browserEvents()
  const previousFetch = globalThis.fetch
  let fail = true
  const writes = []
  globalThis.fetch = async (url, init) => {
    if (init?.method === 'PATCH') {
      writes.push({ url, body: JSON.parse(init.body) })
      return response(settings(2), fail ? 500 : 0)
    }
    return response(String(url).includes('discussion-settings') ? settings(1) : rows)
  }
  const states = []
  const app = renderer.createApp(Selector, { classroomId: 7, onBusy: (value) => states.push(value) })
  const root = node('#root')
  try {
    app.mount(root)
    await flush()
    assert.match(trigger(root).props.title, /A/)
    await chooseB(root)
    assert.match(trigger(root).props.title, /A/)
    assert.equal(findAll(root, (n) => n.props.role === 'alert').length, 1)
    fail = false
    await chooseB(root)
    assert.match(trigger(root).props.title, /B/)
    assert.deepEqual(writes[0], { url: '/api/v1/classrooms/7/discussion-settings', body: { llm_provider_id: 2, llm_model_id: 'chat' } })
    assert.equal(states.at(-1), false)
  } finally { app.unmount(); globalThis.fetch = previousFetch; restore() }
})

test('late settings responses from another classroom never overwrite the current classroom', async () => {
  const { default: Selector } = await import('../src/components/classroom/DiscussionModelSelector.vue')
  const restore = browserEvents()
  const previousFetch = globalThis.fetch
  let resolveFirst
  globalThis.fetch = async (url) => {
    if (String(url).includes('/7/discussion-settings')) return new Promise((resolve) => { resolveFirst = resolve })
    return response(String(url).includes('discussion-settings') ? settings(2) : rows)
  }
  const props = reactive({ classroomId: 7 })
  const app = renderer.createApp({ render: () => h(Selector, { ...props }) })
  const root = node('#root')
  try {
    app.mount(root)
    await flush()
    props.classroomId = 8
    await flush()
    assert.match(trigger(root).props.title, /B/)
    resolveFirst(response(settings(1)))
    await flush()
    assert.match(trigger(root).props.title, /B/)
  } finally { app.unmount(); globalThis.fetch = previousFetch; restore() }
})

test('a failed settings read still allows selecting and saving an available model', async () => {
  const { default: Selector } = await import('../src/components/classroom/DiscussionModelSelector.vue')
  const restore = browserEvents()
  const previousFetch = globalThis.fetch
  globalThis.fetch = async (url, init) => {
    if (init?.method === 'PATCH') return response(settings(2))
    return response(String(url).includes('discussion-settings') ? null : rows, String(url).includes('discussion-settings') ? 500 : 0)
  }
  const states = []
  const app = renderer.createApp(Selector, { classroomId: 7, onBusy: (value) => states.push(value) })
  const root = node('#root')
  try {
    app.mount(root)
    await flush()
    assert.equal(states.at(-1), true)
    trigger(root).props.onClick()
    await nextTick()
    assert.equal(findAll(root, (n) => n.props['data-provider-id'] === 2).length, 1)
    trigger(root).props.onClick()
    await nextTick()
    await chooseB(root)
    assert.match(trigger(root).props.title, /B/)
    assert.equal(states.at(-1), false)
    assert.equal(findAll(root, (n) => n.props.role === 'alert').length, 0)
  } finally { app.unmount(); globalThis.fetch = previousFetch; restore() }
})

test('unavailable discussion models block sending but not reading previous conversations', async () => {
  const { default: ChatArea } = await import('../src/components/classroom/ChatArea.vue')
  const { createI18n } = await import('vue-i18n')
  const { createDiscussionTrace } = await import('../src/lib/discussionTrace.ts')
  const restore = browserEvents()
  const previousFetch = globalThis.fetch
  globalThis.fetch = async (url) => response(String(url).includes('discussion-settings') ? { ...settings(1), available: false } : rows)
  const props = reactive({
    classroomId: 7, collapsed: false, width: 340, tab: 'chat',
    sessions: [{ id: '9', title: 'Earlier discussion', type: 'discussion', preview: '', active: false }],
    hasActiveSession: false, notes: [], messages: [], view: 'list', busy: false,
    running: false, sending: false, loadingConversations: false, error: '', draft: 'hello',
    thinking: false, yourTurn: false, trace: createDiscussionTrace(),
  })
  const opened = []
  const sent = []
  const app = renderer.createApp({ render: () => h(ChatArea, { ...props, onOpenSession: (id) => opened.push(id), onSend: (text) => sent.push(text) }) })
  app.use(createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: {} } }))
  const root = node('#root')
  try {
    app.mount(root)
    await flush()
    const history = findAll(root, (n) => n.type === 'button' && findAll(n, (child) => child.text === 'Earlier discussion').length)[0]
    assert.equal(history.props.disabled, false)
    history.props.onClick()
    assert.deepEqual(opened, ['9'])
    props.view = 'conversation'
    await flush()
    const send = findAll(root, (n) => n.type === 'button' && n.props.type === 'submit')[0]
    assert.equal(send.props.disabled, true)
    findAll(root, (n) => n.type === 'textarea')[0].props.onKeydown({ key: 'Enter', preventDefault() {} })
    assert.deepEqual(sent, [])
  } finally { app.unmount(); globalThis.fetch = previousFetch; restore() }
})
