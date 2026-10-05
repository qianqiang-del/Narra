import assert from 'node:assert/strict'
import test from 'node:test'
import { existsSync, readFileSync } from 'node:fs'
import { registerHooks } from 'node:module'
import { resolve as resolvePath } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'

import { compileScript, parse } from '@vue/compiler-sfc'
import { createPinia } from 'pinia'
import ts from 'typescript'
import { createRenderer, nextTick } from 'vue'

const sourceRoot = pathToFileURL(resolvePath('src') + '/')

registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier.startsWith('@/') || (specifier.startsWith('.') && context.parentURL?.startsWith(sourceRoot.href))) {
      const base = specifier.startsWith('@/')
        ? new URL(specifier.slice(2), sourceRoot)
        : new URL(specifier, context.parentURL)
      const exact = fileURLToPath(base)
      return { url: pathToFileURL(existsSync(exact) ? exact : `${exact}.ts`).href, shortCircuit: true }
    }
    return nextResolve(specifier, context)
  },
  load(url, context, nextLoad) {
    if (!url.startsWith(sourceRoot.href) || (!url.endsWith('.vue') && !url.endsWith('.ts'))) return nextLoad(url, context)
    const source = readFileSync(fileURLToPath(url), 'utf8')
    const script = url.endsWith('.vue')
      ? compileScript(parse(source, { filename: url }).descriptor, { id: 'llm-settings-test', inlineTemplate: true }).content
      : source
    return {
      format: 'module',
      source: ts.transpileModule(script, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } }).outputText,
      shortCircuit: true,
    }
  },
})

const { default: LlmSettingsSection } = await import('../src/components/home/LlmSettingsSection.vue')
const { useLlmStore } = await import('../src/stores/llm.ts')

function node(type, text = '') {
  return { type, text, props: {}, children: [], parent: null, addEventListener() {}, removeEventListener() {}, scrollIntoView() {} }
}

const renderer = createRenderer({
  createElement: (type) => node(type),
  createText: (text) => node('#text', text),
  createComment: (text) => node('#comment', text),
  setText: (target, text) => { target.text = text },
  setElementText: (target, text) => { target.children = [node('#text', text)] },
  insert(target, parent, anchor = null) {
    if (target.parent) {
      const oldIndex = target.parent.children.indexOf(target)
      if (oldIndex >= 0) target.parent.children.splice(oldIndex, 1)
    }
    target.parent = parent
    const index = anchor ? parent.children.indexOf(anchor) : -1
    parent.children.splice(index < 0 ? parent.children.length : index, 0, target)
  },
  remove(target) {
    if (!target.parent) return
    const index = target.parent.children.indexOf(target)
    if (index >= 0) target.parent.children.splice(index, 1)
    target.parent = null
  },
  parentNode: (target) => target.parent,
  nextSibling: (target) => {
    const children = target.parent?.children
    return children ? children[children.indexOf(target) + 1] ?? null : null
  },
  patchProp: (target, key, _previous, next) => { target.props[key] = next },
})

function find(target, predicate) {
  return predicate(target) ? target : target.children.map((child) => find(child, predicate)).find(Boolean)
}

test('price search keeps a candidate pending until the user adopts it', async () => {
  const previousFetch = globalThis.fetch
  let searchRequests = 0
  let completeRefresh
  globalThis.fetch = async (url, init) => {
    assert.match(String(url), /\/settings\/llm\/providers\/1\/pricing\/suggestions$/)
    assert.equal(JSON.parse(init.body).model_id, 'deepseek-flash')
    searchRequests++
    if (searchRequests === 2) return new Promise((resolve) => { completeRefresh = resolve })
    return { json: async () => ({ code: 0, data: {
      model_id: 'deepseek-flash', found: true,
      candidates: [{ model_id: 'deepseek-flash', pricing_mode: 'token_price', input_per_million: 0.4, output_per_million: 1.6, currency: 'USD', source: 'search', source_url: 'https://example.com/pricing', billing_note: '标准 API 价格' }],
    } }) }
  }

  const root = node('#root')
  const pinia = createPinia()
  const app = renderer.createApp(LlmSettingsSection)
  app.use(pinia)
  useLlmStore(pinia).providers = [{
    id: 1, name: 'DeepSeek', baseUrl: 'https://api.deepseek.com', models: ['deepseek-flash'],
    pricing: {}, timeout: '2m0s', apiKeyConfigured: true, testStatus: 'success', enabled: true,
  }]
  try {
    app.mount(root)
    find(root, (item) => item.props['data-testid'] === 'edit-llm-provider').props.onClick()
    await nextTick()
    await new Promise(setImmediate)
    await nextTick()
    const form = find(root, (item) => item.type === 'form')
    assert.ok(form)
    assert.equal(find(form, (item) => item.type === 'h3')?.children[0]?.text, '编辑配置')
    assert.equal(find(form, (item) => item.type === 'input' && item.props.placeholder === '未填写')?.props.value, '')
    assert.ok(find(form, (item) => item.type === 'button' && item.children.some((child) => child.text?.includes('采用此候选'))))
    assert.equal(searchRequests, 1)

    find(form, (item) => item.type === 'button' && item.children.some((child) => child.text?.includes('采用此候选'))).props.onClick()
    await nextTick()
    assert.equal(find(form, (item) => item.type === 'input' && item.props.placeholder === '未填写')?.props.value, 0.4)
    const prices = find(form, (item) => item.type === 'div' && item.props.class?.includes('grid-cols-3'))
    assert.equal(prices.children[1].children.find((item) => item.type === 'input')?.props.value, 1.6)

    find(form, (item) => item.type === 'button' && item.children.some((child) => child.props.role === 'status'))?.props.onClick()
    const inputPrice = find(form, (item) => item.type === 'input' && item.props.placeholder === '未填写')
    inputPrice.props.onInput({ target: { value: '9' } })
    await nextTick()
    completeRefresh({ json: async () => ({ code: 0, data: {
      model_id: 'deepseek-flash', found: true,
      candidates: [{ model_id: 'deepseek-flash', pricing_mode: 'token_price', input_per_million: 0.5, output_per_million: 2, currency: 'USD', source: 'search', source_url: 'https://example.com/pricing' }],
    } }) })
    await new Promise(setImmediate)
    await nextTick()
    assert.equal(find(form, (item) => item.type === 'input' && item.props.placeholder === '未填写')?.props.value, 9)
    find(form, (item) => item.type === 'input' && item.props.placeholder === 'USD').props.onInput({ target: { value: 'cny' } })
    await nextTick()
    assert.equal(find(form, (item) => item.type === 'input' && item.props.placeholder === 'USD')?.props.value, 'CNY')
    assert.ok(find(form, (item) => item.type === 'a' && item.props.href === 'https://example.com/pricing'), '原始候选来源应保留供核对')
  } finally {
    app.unmount()
    globalThis.fetch = previousFetch
  }
})

test('multiplier candidate is visible but cannot be adopted as a token price', async () => {
  const previousFetch = globalThis.fetch
  globalThis.fetch = async () => ({ json: async () => ({ code: 0, data: {
    model_id: 'gpt-6-astra', found: true,
    candidates: [{ model_id: 'gpt-6-astra', pricing_mode: 'multiplier', group: 'Codex Mix', group_ratio: 0.25, billing_note: '缺少基础币价，无法直接换算实际费用', source_url: 'https://nowcoding.ai/api/pricing' }],
  } }) })
  const root = node('#root')
  const pinia = createPinia()
  const app = renderer.createApp(LlmSettingsSection)
  app.use(pinia)
  useLlmStore(pinia).providers = [{ id: 1, name: 'Proxy', baseUrl: 'https://proxy.example/v1', models: ['gpt-6-astra'], pricing: {}, timeout: '60s', apiKeyConfigured: true, testStatus: 'success', enabled: true }]
  try {
    app.mount(root)
    find(root, (item) => item.props['data-testid'] === 'edit-llm-provider').props.onClick()
    await nextTick(); await new Promise(setImmediate); await nextTick()
    const form = find(root, (item) => item.type === 'form')
    assert.ok(find(form, (item) => item.type === 'p' && item.children.some((child) => child.text?.includes('缺少基础币价'))))
    assert.equal(find(form, (item) => item.type === 'button' && item.children.some((child) => child.text?.includes('采用此候选'))), undefined)
    assert.equal(find(form, (item) => item.type === 'input' && item.props.placeholder === '未填写')?.props.value, '')
  } finally { app.unmount(); globalThis.fetch = previousFetch }
})

test('missing search MCP leaves prices editable and permits a later retry', async () => {
  const previousFetch = globalThis.fetch
  let searchRequests = 0
  globalThis.fetch = async (url) => {
    assert.match(String(url), /\/settings\/llm\/providers\/1\/pricing\/suggestions$/)
    searchRequests++
    if (searchRequests === 1) {
      return { json: async () => ({ code: 503, message: '请先配置并启用提供网页搜索工具的联网搜索 MCP 服务' }) }
    }
    return { json: async () => ({ code: 0, data: {
      model_id: 'deepseek-flash', found: true,
      candidates: [{ model_id: 'deepseek-flash', pricing_mode: 'token_price', input_per_million: 0.4, output_per_million: 1.6, currency: 'USD', source: 'search', source_url: 'https://example.com/pricing' }],
    } }) }
  }

  const root = node('#root')
  const pinia = createPinia()
  const app = renderer.createApp(LlmSettingsSection)
  app.use(pinia)
  useLlmStore(pinia).providers = [{
    id: 1, name: 'DeepSeek', baseUrl: 'https://api.deepseek.com',
    models: ['deepseek-flash', 'deepseek-chat'], pricing: {}, timeout: '60s',
    apiKeyConfigured: true, testStatus: 'success', enabled: true,
  }]
  try {
    app.mount(root)
    find(root, (item) => item.props['data-testid'] === 'edit-llm-provider').props.onClick()
    await nextTick()
    await new Promise(setImmediate)
    await nextTick()
    const form = find(root, (item) => item.type === 'form')
    assert.equal(searchRequests, 1, 'MCP 不可用时不应为每个模型重复发起自动查价')
    assert.ok(find(form, (item) => item.type === 'p' && item.children.some((child) => child.text?.includes('价格可手动填写'))))
    assert.equal(find(form, (item) => item.type === 'p' && item.props.class?.includes('text-destructive')), undefined)

    const manualPrice = find(form, (item) => item.type === 'input' && item.props.placeholder === '未填写')
    manualPrice.props.onInput({ target: { value: '9' } })
    await nextTick()
    assert.equal(find(form, (item) => item.type === 'input' && item.props.placeholder === '未填写')?.props.value, 9)
    assert.equal(find(form, (item) => item.type === 'button' && item.props.type === 'submit')?.props.disabled, false)

    find(form, (item) => item.type === 'button' && item.children.some((child) => child.props.role === 'status'))?.props.onClick()
    await new Promise(setImmediate)
    await nextTick()
    assert.equal(searchRequests, 2, '手动点击应重新检查已恢复的搜索服务')
  } finally {
    app.unmount()
    globalThis.fetch = previousFetch
  }
})
