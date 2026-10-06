import assert from 'node:assert/strict'
import test from 'node:test'
import { createPinia } from 'pinia'
import { nextTick } from 'vue'
import { browserEvents, findAll, node, renderer } from './helpers/vueHarness.mjs'

const { default: RerankSettingsSection } = await import('../src/components/home/RerankSettingsSection.vue')
const { useRerankStore } = await import('../src/stores/rerank.ts')

const savedModel = {
  id: 1, name: '硅基流动 · bge-reranker-v2-m3', baseUrl: 'https://api.siliconflow.cn/v1',
  timeout: '120s', model: 'BAAI/bge-reranker-v2-m3', apiKeyConfigured: true,
  testStatus: 'success', lastTestError: null, enabled: true,
}

function findButton(root, label) {
  const button = findAll(root, (item) => item.type === 'button' && item.children.some((child) => child.text?.includes(label)))[0]
  assert.ok(button, `找不到按钮：${label}`)
  return button
}

function heading(root, text) {
  return findAll(root, (item) => item.type === 'h3' && item.children[0]?.text === text).length
}

function mentionsModel(root) {
  return findAll(root, (item) => item.text?.includes(savedModel.name)).length
}

test('saved rerank models are hidden while the add or edit form is open', async () => {
  const restore = browserEvents()
  const root = node('#root')
  const pinia = createPinia()
  const app = renderer.createApp(RerankSettingsSection)
  app.use(pinia)
  useRerankStore(pinia).models = [savedModel]
  try {
    app.mount(root)
    await nextTick()
    // 列表态：标题与已保存的配置卡片都在。
    assert.equal(heading(root, '已保存的配置'), 1)
    assert.ok(mentionsModel(root) > 0)

    // 新增态：表单打开后不再显示已保存的配置与模型卡片。
    findButton(root, '新增配置').props.onClick()
    await nextTick()
    assert.equal(heading(root, '新增配置'), 1)
    assert.equal(heading(root, '已保存的配置'), 0)
    assert.equal(mentionsModel(root), 0)

    // 取消后回到列表。
    findButton(root, '取消').props.onClick()
    await nextTick()
    assert.equal(heading(root, '已保存的配置'), 1)
    assert.ok(mentionsModel(root) > 0)

    // 编辑态：同样只显示表单，不显示已保存的模型。
    findButton(root, '编辑').props.onClick()
    await nextTick()
    assert.equal(heading(root, '编辑配置'), 1)
    assert.equal(heading(root, '已保存的配置'), 0)
    assert.equal(mentionsModel(root), 0)
  } finally {
    app.unmount()
    restore()
  }
})
