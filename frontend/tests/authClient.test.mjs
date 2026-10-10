import assert from 'node:assert/strict'
import test from 'node:test'
import { existsSync, readFileSync } from 'node:fs'
import { registerHooks } from 'node:module'
import { resolve as resolvePath } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import ts from 'typescript'

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
    if (!url.startsWith(sourceRoot.href) || !url.endsWith('.ts')) return nextLoad(url, context)
    const source = readFileSync(fileURLToPath(url), 'utf8')
    return {
      format: 'module',
      source: ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } }).outputText,
      shortCircuit: true,
    }
  },
})

const { createLlmProvider } = await import('../src/api/llm.ts')
const { saveSession, getToken } = await import('../src/lib/authSession.ts')

test('creating an LLM provider sends the logged-in token', async () => {
  const previousStorage = globalThis.localStorage
  const previousFetch = globalThis.fetch
  const values = new Map()
  globalThis.localStorage = {
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
    removeItem: (key) => values.delete(key),
  }
  saveSession({ token: 'test-token', expires_in: 3600, user: { id: 1, phone: '13500000000' } })
  globalThis.fetch = async (url, init) => {
    assert.equal(url, '/api/v1/settings/llm/providers')
    assert.equal(init.headers.Authorization, 'Bearer test-token')
    return { json: async () => ({ code: 0, data: { id: 1, name: 'test', models: [], pricing: {} } }) }
  }
  try {
    await createLlmProvider({ name: 'test', baseUrl: 'https://example.com', apiKey: '', clearApiKey: false, timeout: '30s', models: [], pricing: {} })
  } finally {
    globalThis.fetch = previousFetch
    globalThis.localStorage = previousStorage
  }
})

test('a protected 401 clears the session and returns to login', async () => {
  const previousStorage = globalThis.localStorage
  const previousLocation = globalThis.window
  const previousFetch = globalThis.fetch
  const values = new Map()
  globalThis.localStorage = {
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
    removeItem: (key) => values.delete(key),
  }
  const destinations = []
  globalThis.window = { location: { pathname: '/workspace/1', search: '?tab=models', hash: '', assign: (url) => destinations.push(url) } }
  saveSession({ token: 'expired-token', expires_in: 3600, user: { id: 1, phone: '13500000000' } })
  globalThis.fetch = async () => ({ json: async () => ({ code: 401, message: '令牌无效' }) })
  try {
    await assert.rejects(createLlmProvider({ name: 'test', baseUrl: 'https://example.com', apiKey: '', clearApiKey: false, timeout: '30s', models: [], pricing: {} }), /令牌无效/)
    assert.equal(getToken(), null)
    assert.deepEqual(destinations, ['/login?redirect=%2Fworkspace%2F1%3Ftab%3Dmodels'])
  } finally {
    globalThis.fetch = previousFetch
    globalThis.window = previousLocation
    globalThis.localStorage = previousStorage
  }
})
