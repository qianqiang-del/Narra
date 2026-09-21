import { request } from './client'

/**
 * 知识库文档接口。
 *
 * 后端是异步收录：上传请求只做落盘与建行，返回时文档是 pending，解析与向量化由
 * 后台 worker 推进。进度靠 `fetchKnowledgeDocument(id)` 轮询 status 拿
 * （见 stores/knowledge.ts）。上传接口本身很快 —— 实测几百毫秒返回。
 */

/** 后端 `response.KnowledgeDocument` 的原样形状 */
interface KnowledgeDocumentDTO {
  id: number
  title: string
  source_type: string
  source_uri?: string
  enabled: boolean
  status: string
  parser?: string
  error?: string
  chunks: number
  characters: number
  created_at: string
  updated_at: string
}

/** 后端 `response.PageResponse` 的原样形状 */
interface PageDTO<T> {
  list: T[]
  page: number
  size: number
  total: number
  total_page: number
}

/** 文档处理状态，对应后端 knowledge_documents.status */
export type KnowledgeDocumentStatus = 'pending' | 'processing' | 'ready' | 'failed'

/**
 * 上传记录一行上出现的状态徽章，**只有四个**。
 *
 * 与数据库里的四个状态不是一一对应：
 * - `pending` / `processing` 对用户是同一件事（"还没好"），合并成 `processing`；
 * - `ready` 要再看文档还在不在 —— 记录还在、文档没了就是 `removed`。
 *
 * `removed` 不是一个数据库状态，而是"记录还在、当时收录成功的文档已经被删了"
 * 这个**展示态**：它由「documentId 为空 + status 仍是 ready」推出来。放进这个类型，
 * 是因为它只活在界面上（文案与配色），不该去污染文档状态的取值。
 */
export type KnowledgeBadgeStatus = 'ready' | 'removed' | 'processing' | 'failed'

/** 文档来源，对应数据库上 source_type 的 CHECK 约束 */
export type KnowledgeSourceType = 'manual' | 'import' | 'api'

export interface KnowledgeDocument {
  id: number
  title: string
  sourceType: KnowledgeSourceType
  /** 上传时的原始文件名；手动录入的文档为空 */
  sourceUri: string
  /**
   * 是否参与检索。**后端目前只读不可写** —— 没有改它的接口，
   * 所以界面上不要给它做开关，点了不会有任何东西接住。
   */
  enabled: boolean
  status: KnowledgeDocumentStatus
  parser: string
  /** 处理失败的原因；成功时为空串 */
  error: string
  chunks: number
  /** 正文字符数。正文本身不出网（一篇可能几十万字） */
  characters: number
  createdAt: string
  updatedAt: string
}

export interface KnowledgePage {
  list: KnowledgeDocument[]
  page: number
  size: number
  total: number
  totalPage: number
}

export interface KnowledgeDocumentPreview extends KnowledgeDocument {
  content: string
}

/** 后端 `response.KnowledgeUploadRecord` 的原样形状 */
interface KnowledgeUploadRecordDTO {
  id: number
  document_id: number | null
  title: string
  original_name: string
  size_bytes: number
  status: string
  error?: string
  created_at: string
  updated_at: string
}

/**
 * 上传记录 —— 一次文件投递的流水，**不是一份知识**。
 *
 * 与 KnowledgeDocument 的两处关键差别：
 * - 它包含已经收录成功的那些（记录是"投递"这个动作的历史）；
 * - 文档被删掉之后记录仍留着，此时 `documentId` 为空、`status` 还是 ready，
 *   界面上读作「已收录后删除」。
 */
export interface KnowledgeUploadRecord {
  id: number
  /** 关联文档 ID；为空表示那次上传的成果已经被删了 */
  documentId: number | null
  /** 展示标题：优先文档标题，文档没了则回落 originalName（后端算好给前端） */
  title: string
  /** 用户看到的原始文件名，始终有值 */
  originalName: string
  /** 文件字节数；0 表示未知（回填出来的历史记录拿不到这个信息） */
  sizeBytes: number
  status: KnowledgeDocumentStatus
  /** 失败原因；成功或未结束时为空串 */
  error: string
  createdAt: string
  updatedAt: string
}

export interface UploadRecordPage {
  list: KnowledgeUploadRecord[]
  page: number
  size: number
  total: number
  totalPage: number
}

/** 单次上传的文件大小上限，与后端 controller 的 maxUploadBytes 对齐 */
export const MAX_UPLOAD_BYTES = 16 << 20

/**
 * 前端允许选择的扩展名。
 *
 * 与后端的解析能力对齐：md / txt 由内置的纯文本解析器直接读；PDF / Office / 图片
 * 交给 Python 解析器（需要后端 document_parser.enabled 为 true 且环境已备好；环境
 * 没备好时会在后端落成 failed，并把原因原样带回来）。
 *
 * 这里只做"格式是否被支持"这一层粗筛，不再按 enabled 提前拦下 PDF —— 真正的能力
 * 边界由后端给结论，前端拦错反而会让用户以为格式不被支持。
 */
export const SUPPORTED_EXTENSIONS = [
  '.md', '.markdown', '.txt', '.text', '.pdf',
  '.docx', '.pptx', '.xlsx', '.png', '.jpg', '.jpeg', '.bmp', '.tiff', '.tif',
] as const

function toDocument(d: KnowledgeDocumentDTO): KnowledgeDocument {
  return {
    id: d.id,
    title: d.title,
    sourceType: d.source_type as KnowledgeSourceType,
    sourceUri: d.source_uri ?? '',
    enabled: d.enabled,
    status: d.status as KnowledgeDocumentStatus,
    parser: d.parser ?? '',
    error: d.error ?? '',
    chunks: d.chunks,
    characters: d.characters,
    createdAt: d.created_at,
    updatedAt: d.updated_at,
  }
}

/** 列表查询条件；四项都可省略，省略就是不限。 */
export interface KnowledgeListParams {
  /** 页码，从 1 起（与后端 page 对齐） */
  page?: number
  /** 每页条数；超过后端上限 100 会被钳到 100 */
  size?: number
  /**
   * 只看这些状态，会拼成逗号分隔的 `status=pending,processing`。
   * 空数组或不传表示不限状态。
   */
  status?: KnowledgeDocumentStatus[]
  /** 在标题与来源文件名上做模糊匹配（后端不区分大小写）；空串或不传表示不限 */
  keyword?: string
}

/**
 * 分页拉取文档列表。列表响应不含正文，只有字符数。
 *
 * 筛选与分页都在服务端做：列表本身就是分页的，拉回来再在前端过滤只能看到
 * 已经下载的那几页 —— "第一页全是 failed、ready 排在第二页"会被过滤成空列表。
 */
export async function fetchKnowledgeDocuments(
  params: KnowledgeListParams = {},
): Promise<KnowledgePage> {
  const search = new URLSearchParams()
  search.set('page', String(params.page ?? 1))
  search.set('size', String(params.size ?? 20))
  if (params.status?.length) search.set('status', params.status.join(','))
  const keyword = params.keyword?.trim()
  if (keyword) search.set('keyword', keyword)

  const data = await request<PageDTO<KnowledgeDocumentDTO>>(`/knowledge/documents?${search}`)
  return {
    list: data.list.map(toDocument),
    page: data.page,
    size: data.size,
    total: data.total,
    totalPage: data.total_page,
  }
}

/** 取单篇文档。查不到时后端返回业务错误，由 request 抛 ApiError。 */
export async function fetchKnowledgeDocument(id: number): Promise<KnowledgeDocument> {
  return toDocument(await request<KnowledgeDocumentDTO>(`/knowledge/documents/${id}`))
}

export async function fetchKnowledgeDocumentPreview(id: number): Promise<KnowledgeDocumentPreview> {
  const data = await request<KnowledgeDocumentDTO & { content: string }>(`/knowledge/documents/${id}/preview`)
  return { ...toDocument(data), content: data.content }
}

export async function deleteKnowledgeDocument(id: number): Promise<void> {
  await request<null>(`/knowledge/documents/${id}`, { method: 'DELETE' })
}

function toUploadRecord(r: KnowledgeUploadRecordDTO): KnowledgeUploadRecord {
  return {
    id: r.id,
    documentId: r.document_id ?? null,
    title: r.title,
    originalName: r.original_name,
    sizeBytes: r.size_bytes,
    status: r.status as KnowledgeDocumentStatus,
    error: r.error ?? '',
    createdAt: r.created_at,
    updatedAt: r.updated_at,
  }
}

/**
 * 分页拉取上传记录（完整流水，含已收录成功的）。
 *
 * 这个接口**没有筛选参数** —— 与文档列表不同，记录不做 status / keyword 筛选。
 * 抽屉是"最近投递过什么"的一览，前端按 `recordAlerts` 自己挑出要盯的那些。
 */
export async function fetchUploadRecords(
  params: { page?: number; size?: number } = {},
): Promise<UploadRecordPage> {
  const search = new URLSearchParams()
  search.set('page', String(params.page ?? 1))
  search.set('size', String(params.size ?? 20))

  const data = await request<PageDTO<KnowledgeUploadRecordDTO>>(
    `/knowledge/upload-records?${search}`,
  )
  return {
    list: data.list.map(toUploadRecord),
    page: data.page,
    size: data.size,
    total: data.total,
    totalPage: data.total_page,
  }
}

/**
 * 删除一条上传记录。
 *
 * 连带后果在后端那一个事务里：对应的文档若还**没收录成功**会被一起删掉（不收掉的话
 * 上传闸门会一直卡着，因为它数的是 pending + processing 的文档行）；已经 ready 的
 * 文档绝不触碰，只是记录失去关联、状态变成「已收录后删除」。
 */
export async function deleteUploadRecord(id: number): Promise<void> {
  await request<null>(`/knowledge/upload-records/${id}`, { method: 'DELETE' })
}

/**
 * 上传一个文件并收录。
 *
 * 走 multipart，字段名必须是 `file`（后端按这个字段取名）。标题留空时后端会依次
 * 回落到正文的一级标题、文件名 —— 所以不传 title 比传空串好，这里只在非空时才带上。
 */
export async function uploadKnowledgeFile(file: File, title?: string): Promise<KnowledgeDocument> {
  const form = new FormData()
  form.append('file', file)
  const trimmed = title?.trim()
  if (trimmed) form.append('title', trimmed)

  const d = await request<KnowledgeDocumentDTO>('/knowledge/documents', {
    method: 'POST',
    body: form,
  })
  return toDocument(d)
}

/**
 * 让一条收录失败的文档重新排队：后端复用服务器上留下的原件再跑一遍。
 *
 * 不收新文件 —— 失败时原件已经被归档在服务器上，所以这里没有 body。
 * 返回的文档是 pending，调用方接着轮询 `fetchKnowledgeDocument` 看进度，
 * 流程与上传之后完全一样（重试与上传在前端共用同一段轮询）。
 *
 * 后端在"现在不能重试"时回 409：已经不是失败态、后台正忙着收别的、
 * 或者原件已经不在服务器上（那种只能重新上传）。都不是请求写错了。
 */
export async function retryKnowledgeDocument(id: number): Promise<KnowledgeDocument> {
  const d = await request<KnowledgeDocumentDTO>(`/knowledge/documents/${id}/retry`, {
    method: 'POST',
  })
  return toDocument(d)
}

/** 直接收录一段正文（Markdown），跳过文件与解析。 */
export async function ingestKnowledgeText(content: string, title?: string): Promise<KnowledgeDocument> {
  const d = await request<KnowledgeDocumentDTO>('/knowledge/documents/text', {
    method: 'POST',
    body: JSON.stringify({ title: title?.trim() ?? '', content }),
  })
  return toDocument(d)
}
