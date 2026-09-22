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

/** 流式响应中途的错误帧（SSE 的 error 事件）——同样没有服务端的业务 code */
export const CODE_STREAM = -2

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
    // FormData 的 Content-Type 必须由浏览器自己生成（它要往里塞 boundary），
    // 这里写死 application/json 会让后端把 multipart 体整个解析不出来。
    const isFormData = init?.body instanceof FormData
    res = await fetch(`${BASE}${path}`, {
      ...init,
      headers: {
        ...(isFormData ? {} : { 'Content-Type': 'application/json' }),
        ...init?.headers,
      },
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

/** 一条 SSE 事件 */
export interface SseEvent {
  /** 事件名；服务端没写 event 行时按协议默认 `message` */
  event: string
  /** data 行拼接结果（多行用 \n 连接） */
  data: string
}

/**
 * 读取一条 SSE 流（GET），逐条产出事件。
 *
 * 为什么不用浏览器原生 EventSource：
 * - 它不能带 Authorization 头，后端上鉴权之后就用不了；
 * - 它没有 AbortSignal 这样的外部取消手段，只有 close()；
 * - 连接出错时只给一个笼统的 error 事件，读不到后端写的原因。
 * 代价是要自己解析 SSE 帧 —— 这里只实现标准里最小子集：注释、event、data。
 *
 * 业务错误仍然是统一信封（HTTP 200 + JSON）：路径不存在、文档不存在等情况下
 * 拿到的是 JSON 而不是流，这里按信封翻成 ApiError，与 request() 的读法一致。
 */
export async function* streamEvents(path: string, signal?: AbortSignal): AsyncGenerator<SseEvent> {
  const res = await fetch(`${BASE}${path}`, {
    headers: { Accept: 'text/event-stream' },
    signal,
  })

  if (!(res.headers.get('Content-Type') ?? '').includes('text/event-stream')) {
    let body: Envelope<unknown>
    try {
      body = (await res.json()) as Envelope<unknown>
    } catch {
      throw new ApiError(res.status, `响应不是事件流（HTTP ${res.status}）`)
    }
    if (body.code !== CODE_SUCCESS) {
      throw new ApiError(body.code, body.message || `请求失败（code ${body.code}）`)
    }
    throw new ApiError(res.status, '响应不是事件流')
  }
  if (!res.body) {
    throw new ApiError(res.status, '响应没有可读的流')
  }

  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''

  try {
    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })

      // 空行是事件之间的分隔；一次读到的不一定恰好是整数条，边收边切。
      let separator = /\r?\n\r?\n/.exec(buffer)
      while (separator) {
        const block = buffer.slice(0, separator.index)
        buffer = buffer.slice(separator.index + separator[0].length)
        const event = parseSseBlock(block)
        if (event) yield event
        separator = /\r?\n\r?\n/.exec(buffer)
      }
    }
  } finally {
    // 消费方提前退出（例如已经拿到终态）时把连接关掉，别让服务端一直挂着。
    void reader.cancel().catch(() => {})
  }
}

/** 解析一个事件块（不含结尾空行）；没有 data 的块按协议忽略 */
function parseSseBlock(block: string): SseEvent | null {
  let event = 'message'
  const data: string[] = []
  for (const line of block.split(/\r?\n/)) {
    if (line === '' || line.startsWith(':')) continue // 注释（心跳就是它）
    const colon = line.indexOf(':')
    const field = colon < 0 ? line : line.slice(0, colon)
    let value = colon < 0 ? '' : line.slice(colon + 1)
    if (value.startsWith(' ')) value = value.slice(1)
    if (field === 'event') event = value
    else if (field === 'data') data.push(value)
  }
  if (data.length === 0) return null
  return { event, data: data.join('\n') }
}
