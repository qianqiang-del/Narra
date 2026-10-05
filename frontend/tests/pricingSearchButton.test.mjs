import assert from 'node:assert/strict'
import test from 'node:test'
import { h, nextTick, reactive } from 'vue'
import { renderer, node, findAll } from './helpers/vueHarness.mjs'

test('price search exposes busy state, disables repeats, and restores the command', async () => {
  const { default: Component } = await import('../src/components/home/PricingSearchButton.vue')
  const props = reactive({ loading: false })
  const root = node('root')
  const app = renderer.createApp({ setup: () => () => h(Component, props) })
  app.mount(root)
  const button = () => findAll(root, (n) => n.type === 'button')[0]
  assert.equal(button().props.disabled, false)
  props.loading = true
  await nextTick()
  assert.equal(button().props.disabled, true)
  assert.equal(button().props['aria-busy'], true)
  assert.equal(findAll(root, (n) => n.props.role === 'status').length, 1)
  assert.ok(findAll(root, (n) => String(n.props.class).includes('price-search-motion')).length)
  props.loading = false
  await nextTick()
  assert.equal(button().props.disabled, false)
  app.unmount()
})
