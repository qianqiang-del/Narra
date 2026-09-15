/**
 * 后端 HTTP 客户端。
 *
 * 后端的响应信封**不是** REST 风格（见后端 `pkg/response/response.go`）：
 * 成功和失败都返回 HTTP 200，成败只看 body 里的 `code`，0 才是成功。
 * 所以这里不能看 `res.ok`——那永远是 true。而且 `data` 字段带 `omitempty`，
 * 出错时它整个不存在，不是 null。
 */

/** 后端统一响应信封 */
interface Envelope<T> {
  code: number
  message: string
  data?: T
}

/** 成功码，对应后端 errors.CodeSuccess */
const CODE_SUCCESS = 0

/** 网络层失败（后端没起、代理没通）——没有服务端的 code 可用 */
const CODE_NETWORK = -1

export class ApiError extends Error {
  readonly code: number

  constructor(code: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.code = code
  }
}

/** 开发期走 vite 代理（vite.config.ts 里 /api → localhost:8080） */
const BASE = '/api/v1'

export async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response
  try {
    res = await fetch(`${BASE}${path}`, {
      ...init,
      headers: { 'Content-Type': 'application/json', ...init?.headers },
    })
  } catch (cause) {
    // fetch 只在网络层失败时 reject。这里刻意不编造中文文案——报什么由调用方决定
    throw new ApiError(CODE_NETWORK, cause instanceof Error ? cause.message : String(cause))
  }

  let body: Envelope<T>
  try {
    body = (await res.json()) as Envelope<T>
  } catch {
    throw new ApiError(res.status, `响应不是合法 JSON（HTTP ${res.status}）`)
  }

  if (body.code !== CODE_SUCCESS) {
    throw new ApiError(body.code, body.message || `请求失败（code ${body.code}）`)
  }

  return body.data as T
}
