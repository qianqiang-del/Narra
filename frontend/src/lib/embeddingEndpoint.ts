/**
 * 识别「原生协议」的向量化服务地址。
 *
 * Narra 只对接 OpenAI 兼容的 POST {base_url}/embeddings，而各家的原生向量 API 形状与它
 * 不同：把官方文档里的原生地址直接粘进设置页，请求注定失败，报错却只有一句 HTTP 状态码。
 * 这里在保存/测试前按已知形状拦下，并给出兼容地址。
 *
 * 只认形状确定的官方 host 与官方路径后缀；未知 host、自建网关一律放行——宁可漏报，
 * 不误伤，真实的连通性仍由「测试连接」负责。
 */

/** 已知的原生向量接口形状；文案按此 id 从 `settings.embeddingNativeHint` 取。 */
export type NativeEmbeddingRule =
  | 'gemini'
  | 'vertex'
  | 'cohere'
  | 'ollama'
  | 'dashscope'
  | 'anthropic'

export interface NativeEmbeddingHint {
  rule: NativeEmbeddingRule
  /** 建议改用的 OpenAI 兼容地址；服务商没有对应兼容服务时为空串。 */
  suggestion: string
}

/** Gemini 的 OpenAI 兼容地址；Vertex 的原生地址也建议改到它（或 Vertex 自己的 endpoints/openapi）。 */
const GEMINI_COMPATIBLE_URL = 'https://generativelanguage.googleapis.com/v1beta/openai/'

/**
 * 命中已知原生协议时返回提示，否则返回 null（放行）。
 * 解析失败同样放行：非法地址交给表单自带的 type=url 与后端校验。
 */
export function detectNativeEmbeddingEndpoint(rawBaseUrl: string): NativeEmbeddingHint | null {
  const url = parseHttpUrl(rawBaseUrl)
  if (!url) return null

  // 末尾斜杠会让所有后缀匹配失手，先去掉；URL 解析已把 host 规范成小写。
  const path = url.pathname.replace(/\/+$/, '')

  if (isGeminiNative(url.hostname, path)) {
    return { rule: 'gemini', suggestion: GEMINI_COMPATIBLE_URL }
  }
  if (isVertexNative(url.hostname, path)) {
    return { rule: 'vertex', suggestion: GEMINI_COMPATIBLE_URL }
  }
  if (isCohereNative(url.hostname, path)) {
    return { rule: 'cohere', suggestion: 'https://api.cohere.ai/compatibility/v1' }
  }
  if (isOllamaNative(path)) {
    return { rule: 'ollama', suggestion: `${url.origin}${stripOllamaSuffix(path)}/v1` }
  }
  if (isDashScopeNative(url.hostname, path)) {
    return { rule: 'dashscope', suggestion: `${url.origin}/compatible-mode/v1` }
  }
  if (url.hostname === 'api.anthropic.com') {
    return { rule: 'anthropic', suggestion: '' }
  }

  return null
}

function parseHttpUrl(raw: string): URL | null {
  try {
    const url = new URL(raw.trim())
    if (url.protocol !== 'http:' && url.protocol !== 'https:') return null
    return url
  } catch {
    return null
  }
}

/** AI Studio 原生：官方 host（兼容地址在 /openai 子路径下），或任意 host 上的 :embedContent 后缀。 */
function isGeminiNative(hostname: string, path: string): boolean {
  if (hostname === 'generativelanguage.googleapis.com') {
    return !/\/openai(\/|$)/.test(path)
  }
  return /:(embedContent|batchEmbedContents)$/.test(path)
}

/** Vertex 原生：aiplatform 主机；兼容端点是 .../endpoints/openapi。 */
function isVertexNative(hostname: string, path: string): boolean {
  if (hostname !== 'aiplatform.googleapis.com' && !hostname.endsWith('-aiplatform.googleapis.com')) {
    return false
  }
  return !path.endsWith('/openapi')
}

/** Cohere 原生：官方 host 上除 compatibility 之外都是原生形状（v2/embed 等）。 */
function isCohereNative(hostname: string, path: string): boolean {
  if (hostname !== 'api.cohere.ai' && hostname !== 'api.cohere.com') return false
  return !path.includes('/compatibility')
}

/** Ollama 原生：/api/embed 或旧的 /api/embeddings。 */
function isOllamaNative(path: string): boolean {
  return /\/api\/(embed|embeddings)$/.test(path)
}

/** 百炼原生：dashscope 官方 host；兼容地址在 /compatible-mode 下。 */
function isDashScopeNative(hostname: string, path: string): boolean {
  if (hostname !== 'dashscope.aliyuncs.com' && hostname !== 'dashscope-intl.aliyuncs.com') {
    return false
  }
  return !path.includes('/compatible-mode')
}

function stripOllamaSuffix(path: string): string {
  return path.replace(/\/api\/(embed|embeddings)$/, '')
}
