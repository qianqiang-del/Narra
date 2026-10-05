import assert from 'node:assert/strict'
import test from 'node:test'
import { h, nextTick, reactive } from 'vue'
import { browserEvents, findAll, node, renderer } from './helpers/vueHarness.mjs'

test('model identity includes provider and duplicate model rows are removed', async () => {
  const { uniqueModels, isSelectedModel } = await import('../src/lib/modelSelection.ts')
  const rows = [
    { providerId: 1, providerName: 'A', modelId: 'chat' },
    { providerId: 1, providerName: 'A', modelId: 'chat' },
    { providerId: 2, providerName: 'B', modelId: 'chat' },
  ]
  assert.deepEqual(uniqueModels(rows), [rows[0], rows[2]])
  assert.equal(isSelectedModel(rows[0], { providerId: 2, modelId: 'chat' }), false)
  assert.equal(isSelectedModel(rows[2], { providerId: 2, modelId: 'chat' }), true)
})

test('browsing providers never switches models and a new choice replaces the previous one', async () => {
  const { default: ModelPicker } = await import('../src/components/shared/ModelPicker.vue')
  const restore = browserEvents()
  const props = reactive({
    selection: { providerId: 1, modelId: 'chat' },
    models: [
      { providerId: 1, providerName: 'A', modelId: 'chat' },
      { providerId: 2, providerName: 'B', modelId: 'chat' },
      { providerId: 2, providerName: 'B', modelId: 'chat' },
    ],
  })
  const choices = []
  const app = renderer.createApp({ render: () => h(ModelPicker, { ...props, onSelect(choice) { choices.push(choice); props.selection = choice } }) })
  const root = node('#root')
  try {
    app.mount(root)
    findAll(root, (n) => n.props['data-testid'] === 'model-picker-trigger')[0].props.onClick()
    await nextTick()
    findAll(root, (n) => n.props['data-provider-id'] === 2)[0].props.onClick()
    await nextTick()
    assert.equal(choices.length, 0)
    const radios = findAll(root, (n) => n.type === 'input' && n.props.type === 'radio')
    assert.equal(radios.length, 1)
    assert.equal(radios[0].props.checked, false)
    radios[0].props.onChange()
    await nextTick()
    assert.deepEqual(choices, [{ providerId: 2, modelId: 'chat' }])
    findAll(root, (n) => n.props['data-testid'] === 'model-picker-trigger')[0].props.onClick()
    await nextTick()
    assert.equal(findAll(root, (n) => n.type === 'input' && n.props.type === 'radio' && n.props.checked).length, 1)
    findAll(root, (n) => n.props['data-provider-id'] === 1)[0].props.onClick()
    await nextTick()
    assert.equal(findAll(root, (n) => n.type === 'input' && n.props.type === 'radio' && n.props.checked).length, 0)
  } finally { app.unmount(); restore() }
})
