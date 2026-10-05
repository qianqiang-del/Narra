import { existsSync, readFileSync } from 'node:fs'
import { registerHooks } from 'node:module'
import { resolve } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer } from 'vue'

const sourceRoot = pathToFileURL(resolve('src') + '/')
registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier.startsWith('@/') || (specifier.startsWith('.') && context.parentURL?.startsWith(sourceRoot.href))) {
      const base = specifier.startsWith('@/') ? new URL(specifier.slice(2), sourceRoot) : new URL(specifier, context.parentURL)
      const exact = fileURLToPath(base)
      return { url: pathToFileURL(existsSync(exact) ? exact : `${exact}.ts`).href, shortCircuit: true }
    }
    return nextResolve(specifier, context)
  },
  load(url, context, nextLoad) {
    if (!url.startsWith(sourceRoot.href) || (!url.endsWith('.vue') && !url.endsWith('.ts'))) return nextLoad(url, context)
    const source = readFileSync(fileURLToPath(url), 'utf8')
    const script = url.endsWith('.vue')
      ? compileScript(parse(source, { filename: url }).descriptor, { id: 'model-selection-test', inlineTemplate: true }).content
      : source
    return {
      format: 'module',
      source: ts.transpileModule(script, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } }).outputText,
      shortCircuit: true,
    }
  },
})

export function node(type, text = '') {
  return { type, text, props: {}, style: {}, children: [], parent: null, scrollTo() {}, addEventListener() {}, removeEventListener() {}, contains(target) { return target === this || this.children.some((child) => child.contains(target)) } }
}

export const renderer = createRenderer({
  createElement: (type) => node(type), createText: (text) => node('#text', text), createComment: (text) => node('#comment', text),
  setText: (target, text) => { target.text = text },
  setElementText: (target, text) => { target.children = [node('#text', text)] },
  insert(target, parent, anchor = null) {
    if (target.parent) target.parent.children.splice(target.parent.children.indexOf(target), 1)
    target.parent = parent
    const index = anchor ? parent.children.indexOf(anchor) : -1
    parent.children.splice(index < 0 ? parent.children.length : index, 0, target)
  },
  remove(target) {
    if (target.parent) target.parent.children.splice(target.parent.children.indexOf(target), 1)
    target.parent = null
  },
  parentNode: (target) => target.parent,
  nextSibling: (target) => target.parent?.children[target.parent.children.indexOf(target) + 1] ?? null,
  patchProp: (target, key, _previous, next) => { target.props[key] = next },
})

export function findAll(target, predicate) {
  return [...(predicate(target) ? [target] : []), ...target.children.flatMap((child) => findAll(child, predicate))]
}

export function browserEvents() {
  const previous = globalThis.document
  globalThis.document = { addEventListener() {}, removeEventListener() {} }
  return () => { globalThis.document = previous }
}
