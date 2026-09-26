import { ApiError, CODE_STREAM, request, streamEvents } from './client'

/**
 * 知识库文档接口。
 *
 * 后端是异步收录：上传请求只做落盘与建行，返回时文档是 pending，解析与向量化由
 * 后台 worker 推进。批量上传返回**逐项结果**：某个文件被拒（格式、大小、队列满）
 * 不影响同批其它文件。
 *
 * 进度有两条路：单篇文档可以订阅 SSE（`watchKnowledgeDocument`，重试时用）；
 * 批量上传不再为每个文件各开一条 SSE —— 浏览器对同源 HTTP/1.1 只有 6 条连接，
 * 一批 10 个文件会把连接池占满。批量改由 store 轮询上传记录列表收敛状态。
 */

/** 后端 `response.KnowledgeDocument` 的原样形状 */
interface KnowledgeDocumentDTO {
  id: number
  title: string
  source_type: string
  source_uri?: string
  enabled: boolean
  status: string
  stage?: string
  failed_stage?: string
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
 * 收录阶段，对应后端 knowledge_documents.ingest_stage。
 *
 * 它是"当前正在做、失败后下次从哪一步恢复"的步骤：parse 读原文件解析、
 * chunk 从正文切分、embed 从切片生成向量。ready 的文档没有下一步，后端返回空串。
 * 界面上只在失败行显示它，用来说明"卡在哪一步"——具体原因在 error 里。
 */
export type KnowledgeDocumentStage = 'parse' | 'chunk' | 'embed'

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
export type KnowledgeSourceType = 'manual' | 'import'

export interface KnowledgeDocument {
  id: number
  title: string
  sourceType: KnowledgeSourceType
  /** 上传时的原始文件名；手动录入的文档为空 */
  sourceUri: string
  /**
   * 是否参与检索。停用不删切片与向量，只是召回时被过滤掉（两条检索 SQL 都带这个条件），
   * 由 `setKnowledgeDocumentEnabled` 切换；列表里停用的行会带「已停用」徽章。
   */
  enabled: boolean
  status: KnowledgeDocumentStatus
  /** 收录阶段；ready 或后端没给时为空串 */
  stage: KnowledgeDocumentStage | ''
  /**
   * 失败卡在哪一步的**细粒度**位置（select_parser / vector / store / worker……）；
   * 成功或后端没给时为空串。界面用它挑说明文案，避免把"写库失败"说成"向量化失败"。
   */
  failedStage: string
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
  stage?: string
  failed_stage?: string
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
  /** 关联文档的收录阶段；文档已删除或 ready 时为空串 */
  stage: KnowledgeDocumentStage | ''
  /** 关联文档失败卡在哪一步（细粒度）；成功、未结束或文档已删除时为空串 */
  failedStage: string
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

/**
 * 上传限制的兜底默认值。
 *
 * 真实值由 `fetchKnowledgeUploadLimits()` 从后端下发（见 knowledge_ingest 配置），
 * 这里的常量只在接口还没返回时用于界面预检；服务端始终是唯一裁判。
 */
export const DEFAULT_UPLOAD_LIMITS: KnowledgeUploadLimits = {
  maxFiles: 10,
  maxFileBytes: 16 << 20,
  maxBatchBytes: 100 << 20,
}

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

/**
 * 由内置纯文本解析器直读的扩展名。
 *
 * 其余受支持的格式（PDF / Office / 图片）要走后端的 Python 解析器 —— 只有它们会在
 * **首次使用时触发环境准备**（现场下载解释器与依赖，分钟级）。所以"首次上传会慢一点"
 * 的提示必须按这条边界判断，别对着 .md 也说。
 */
export const PLAIN_TEXT_EXTENSIONS = ['.md', '.markdown', '.txt', '.text'] as const

/** 这份文件是否需要 Python 解析器（即首次上传时可能要等环境准备） */
export function needsDocumentParser(fileName: string): boolean {
  const name = fileName.toLowerCase()
  if (PLAIN_TEXT_EXTENSIONS.some((ext) => name.endsWith(ext))) return false
  return SUPPORTED_EXTENSIONS.some((ext) => name.endsWith(ext))
}

/**
 * 批量上传的限制值，由后端下发（GET /knowledge/documents/upload-limits）。
 * 服务端始终是唯一裁判：这些值用于选择文件时的预检，不是校验规则的复刻。
 */
export interface KnowledgeUploadLimits {
  maxFiles: number
  maxFileBytes: number
  maxBatchBytes: number
}

interface KnowledgeUploadLimitsDTO {
  max_files: number
  max_file_bytes: number
  max_batch_bytes: number
}

/**
 * 取批量上传的限制值。失败时调用方用 DEFAULT_UPLOAD_LIMITS 兜底 ——
 * 它只影响预检的宽严，真正的拒绝始终来自服务端。
 */
export async function fetchKnowledgeUploadLimits(): Promise<KnowledgeUploadLimits> {
  const d = await request<KnowledgeUploadLimitsDTO>('/knowledge/documents/upload-limits')
  return {
    maxFiles: d.max_files,
    maxFileBytes: d.max_file_bytes,
    maxBatchBytes: d.max_batch_bytes,
  }
}

/** 后端 `documentparser.Status` 的原样形状 */
interface KnowledgeParserStatusDTO {
  enabled: boolean
  ready: boolean
  source?: string
  python?: string
  reason?: string
  preparing?: boolean
  progress?: string
}

/**
 * 解析环境状态。
 *
 * 两个布尔要分开读：
 * - `enabled=false`：配置里没开解析能力，PDF / Office 传上去必然失败；
 * - `enabled=true && ready=false`：能力开着但环境还没备好 —— 首次上传这类文件时，
 *   后端会现场用 uv 下载解释器与依赖，`preparing` / `progress` 描述的就是这段时间。
 *
 * `ready` 在同一个进程内一旦为真就不会再变回假，所以"首次上传"的提示天然只出现一次；
 * 依赖清单升级导致的重建是唯一例外。
 */
export interface KnowledgeParserStatus {
  enabled: boolean
  ready: boolean
  /** 是否正在准备环境（首次上传会触发） */
  preparing: boolean
  /** 当前准备步骤的说明；未在准备时为空串 */
  progress: string
  /** 后端给的原因，未就绪时才有意义 */
  reason: string
}

/**
 * 取解析环境状态。它永远秒回：准备过程在后台跑，不会被这个接口等住
 * （见后端 PythonParser 的 stateMu 说明）。
 */
export async function fetchKnowledgeParserStatus(): Promise<KnowledgeParserStatus> {
  return toParserStatus(await request<KnowledgeParserStatusDTO>('/knowledge/documents/parser/status'))
}

function toParserStatus(d: KnowledgeParserStatusDTO): KnowledgeParserStatus {
  return {
    enabled: d.enabled === true,
    ready: d.ready === true,
    preparing: d.preparing === true,
    progress: d.progress ?? '',
    reason: d.reason ?? '',
  }
}

/** 后端可能不返回 stage、或将来返回新取值；只有三个已知阶段会被界面使用，其余归空串。 */
function toStage(value: string | undefined): KnowledgeDocumentStage | '' {
  return value === 'parse' || value === 'chunk' || value === 'embed' ? value : ''
}

/**
 * 细粒度失败位置（后端 metadata.stage）→ 展示分组键（i18n 的 `knowledge.stage.*`）。
 *
 * 分组的依据是"用户看到的这一步是什么"：model / vector / embed 都属于向量化，
 * 而 store（写库失败）单独成组 —— 向量已经算好了、只是没写进去，说成"向量化失败"
 * 会把人引到错误的方向。位置未知（历史数据）时回落到粗粒度的收录阶段。
 */
const FAILURE_STAGE_KEYS: Record<string, string> = {
  select_parser: 'parse',
  parse: 'parse',
  chunk: 'chunk',
  model: 'embed',
  embed: 'embed',
  vector: 'embed',
  store: 'store',
  status: 'status',
  worker: 'worker',
}

/** 取失败位置对应的展示分组键；非失败或位置未知时返回空串，界面不显示前缀。 */
export function failureStageKey(failedStage: string, stage: KnowledgeDocumentStage | ''): string {
  const key = FAILURE_STAGE_KEYS[failedStage]
  if (key) return key
  return stage
}

function toDocument(d: KnowledgeDocumentDTO): KnowledgeDocument {
  return {
    id: d.id,
    title: d.title,
    sourceType: d.source_type as KnowledgeSourceType,
    sourceUri: d.source_uri ?? '',
    enabled: d.enabled,
    status: d.status as KnowledgeDocumentStatus,
    stage: toStage(d.stage),
    failedStage: d.failed_stage ?? '',
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

/** 进度流上的事件名，与后端 controller 的 eventDocument / eventParser / eventError 对齐 */
const STREAM_DOCUMENT = 'document'
const STREAM_PARSER = 'parser'
const STREAM_ERROR = 'error'

export interface KnowledgeDocumentStreamHandlers {
  /** 文档快照，形状与 fetchKnowledgeDocument 相同；有变化才推 */
  onDocument?: (document: KnowledgeDocument) => void
  /** 解析环境状态，形状与 fetchKnowledgeParserStatus 相同；有变化才推 */
  onParser?: (status: KnowledgeParserStatus) => void
}

/**
 * 订阅一篇文档的收录进度（SSE）。
 *
 * 服务端在文档走到终态（ready / failed）时推完最后一帧就关流，本函数随之正常返回；
 * 中途失败（文档被删等）以 error 事件给出原因，这里翻成 ApiError 抛出。
 * `signal` 供调用方超时或主动放弃时断开流用 —— 断开后服务端下一次写就能发现。
 */
export async function watchKnowledgeDocument(
  id: number,
  handlers: KnowledgeDocumentStreamHandlers = {},
  signal?: AbortSignal,
): Promise<void> {
  for await (const message of streamEvents(`/knowledge/documents/${id}/events`, signal)) {
    switch (message.event) {
      case STREAM_DOCUMENT:
        handlers.onDocument?.(toDocument(JSON.parse(message.data) as KnowledgeDocumentDTO))
        break
      case STREAM_PARSER:
        handlers.onParser?.(toParserStatus(JSON.parse(message.data) as KnowledgeParserStatusDTO))
        break
      case STREAM_ERROR: {
        const payload = JSON.parse(message.data) as { message?: string }
        throw new ApiError(CODE_STREAM, payload.message || '进度推送已中断')
      }
      default:
        // 未知事件名忽略：服务端以后可以加新帧，老前端不该因此崩掉。
        break
    }
  }
}

export async function fetchKnowledgeDocumentPreview(id: number): Promise<KnowledgeDocumentPreview> {
  const data = await request<KnowledgeDocumentDTO & { content: string }>(`/knowledge/documents/${id}/preview`)
  return { ...toDocument(data), content: data.content }
}

export async function deleteKnowledgeDocument(id: number): Promise<void> {
  await request<null>(`/knowledge/documents/${id}`, { method: 'DELETE' })
}

/**
 * 切换一篇文档是否参与检索。
 *
 * 停用不删任何东西：切片与向量原样保留，只是检索时不再召回它；改回 true 立即恢复。
 * 返回更新后的文档（形状与详情接口一致）。
 */
export async function setKnowledgeDocumentEnabled(
  id: number,
  enabled: boolean,
): Promise<KnowledgeDocument> {
  const d = await request<KnowledgeDocumentDTO>(`/knowledge/documents/${id}/enabled`, {
    method: 'PATCH',
    body: JSON.stringify({ enabled }),
  })
  return toDocument(d)
}

function toUploadRecord(r: KnowledgeUploadRecordDTO): KnowledgeUploadRecord {
  return {
    id: r.id,
    documentId: r.document_id ?? null,
    title: r.title,
    originalName: r.original_name,
    sizeBytes: r.size_bytes,
    status: r.status as KnowledgeDocumentStatus,
    stage: toStage(r.stage),
    failedStage: r.failed_stage ?? '',
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
 * 它占着收录队列的名额，容量迟迟不释放）；已经 ready 的文档绝不触碰，只是记录
 * 失去关联、状态变成「已收录后删除」。
 */
export async function deleteUploadRecord(id: number): Promise<void> {
  await request<null>(`/knowledge/upload-records/${id}`, { method: 'DELETE' })
}

/** 后端 `response.KnowledgeIngestItem` 的原样形状 */
interface KnowledgeIngestItemDTO {
  original_name: string
  document_id: number | null
  status: string
  error?: string
}

/** 后端 `response.KnowledgeIngestBatch` 的原样形状 */
interface KnowledgeIngestBatchDTO {
  items: KnowledgeIngestItemDTO[]
  accepted: number
  rejected: number
}

/**
 * 批量上传里的单项结果。
 *
 * `pending` 表示已入队（documentId 有值），`rejected` 表示未入队（error 有原因）。
 * 两者都是"这次请求的正常结果"，不是异常 —— 某个文件被拒不影响同批其它文件。
 */
export interface KnowledgeIngestItem {
  originalName: string
  documentId: number | null
  status: 'pending' | 'rejected'
  error: string
}

export interface KnowledgeIngestBatch {
  items: KnowledgeIngestItem[]
  accepted: number
  rejected: number
}

/**
 * 批量上传一批文件并收录，返回与入参同序的逐项结果。
 *
 * 字段名用 `files`（可重复）。后端同时接受旧的单 `file`，但新前端统一走批量形状。
 * 只有请求体层面的问题（超总量、文件数超限、multipart 损坏）才整批失败并抛 ApiError；
 * 单文件的问题以 `rejected` 出现在逐项结果里。
 */
export async function uploadKnowledgeFiles(
  files: File[],
  title?: string,
): Promise<KnowledgeIngestBatch> {
  const form = new FormData()
  for (const file of files) form.append('files', file)
  const trimmed = title?.trim()
  if (trimmed) form.append('title', trimmed)

  const d = await request<KnowledgeIngestBatchDTO>('/knowledge/documents', {
    method: 'POST',
    body: form,
  })
  return {
    items: d.items.map((item) => ({
      originalName: item.original_name,
      documentId: item.document_id ?? null,
      status: item.status === 'pending' ? 'pending' : 'rejected',
      error: item.error ?? '',
    })),
    accepted: d.accepted,
    rejected: d.rejected,
  }
}

/**
 * 让一条收录失败的文档重新排队：后端复用服务器上留下的原件再跑一遍。
 *
 * 不收新文件 —— 失败时原件已经被归档在服务器上，所以这里没有 body。
 * 返回的文档是 pending，调用方接着订阅 `watchKnowledgeDocument` 看进度，
 * 流程与上传之后完全一样（重试与上传在前端共用同一段等待）。
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
