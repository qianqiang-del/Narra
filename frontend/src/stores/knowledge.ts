import { defineStore } from 'pinia'
import { computed, ref, watch } from 'vue'

import {
  fetchKnowledgeDocuments,
  fetchKnowledgeDocument,
  fetchKnowledgeDocumentPreview,
  fetchKnowledgeParserStatus,
  deleteKnowledgeDocument,
  deleteUploadRecord,
  fetchUploadRecords,
  retryKnowledgeDocument,
  uploadKnowledgeFile,
  ingestKnowledgeText,
  watchKnowledgeDocument,
  type KnowledgeDocument,
  type KnowledgeParserStatus,
  type KnowledgeUploadRecord,
} from '@/api/knowledge'

/**
 * 知识库列表状态。
 *
 * 页面结构对齐 `docs/prototypes/knowledge-redesign.html`：
 * - **主页**只列已就绪（ready）的文档，滚到底由服务端续接下一页；
 * - **上传记录**装的是每一次文件投递的流水（批 ② 起独立成表），含已收录成功的。
 *
 * 两者是**两种东西**，这是批 ② 之后最要紧的一条：
 * 主页回答"现在库里有哪些知识"，抽屉回答"投递过什么"。所以一条记录在文档被删之后
 * 仍然留着（那时它的展示状态是「已收录后删除」），而主页看不到任何痕迹 ——
 * 记录条数与主页条数**不该相等**，也不该被拿来互推。
 *
 * 收录是异步的：`upload` 拿到的是 pending 文档，靠 SSE 进度流盯到终态
 * （`watchUntilSettled`，流不可用时回退 `fetchKnowledgeDocument` 轮询），
 * 前端据此驱动"处理中"的反馈。
 *
 * 两个列表都走**服务端筛选与分页**（批 ①）：主页问 `status=ready`，
 * 上传记录走独立的 `GET /knowledge/upload-records`。在那之前记录是从文档列表里
 * 派生出来的（问 `status=pending,processing,failed`）—— 那样既漏掉已收录的历史，
 * 也没法表达"文档删了但那次投递确实成功过"。
 */
export const useKnowledgeStore = defineStore('knowledge', () => {
  /** 主页每次"下拉"续接的条数，也是首屏拉取的条数 */
  const KNOWLEDGE_BATCH = 10

  /** 上传记录一次拉多少条。后端每页上限是 100，条数更多时抽屉只显示前 100 条 */
  const RECORD_PAGE_SIZE = 100

  /** 搜索防抖：每敲一个字打一次请求太吵，300ms 内的连续输入只发最后一次 */
  const SEARCH_DEBOUNCE_MS = 300

  /** 轮询上限。一份大文档解析几分钟很正常，但不能无限等 */
  const POLL_TIMEOUT_MS = 15 * 60 * 1000

  /**
   * 环境准备连续多久没有进展就算卡住。
   *
   * 首次上传时后端可能在准备解析环境，这一步本身就可能超过 POLL_TIMEOUT_MS ——
   * 它不该被算成"这份文档处理超时"（否则提示刚出现，界面就先报超时，而后台一切正常）。
   * 判据不是"正在准备"就无限等，而是"进度还在变"：连续这么久没有推进才视为卡住。
   */
  const PREPARE_STALL_MS = 10 * 60 * 1000

  /** 主页数据：已收录的文档。按页从服务端取回后依次累加 */
  const readyDocuments = ref<KnowledgeDocument[]>([])

  /** 服务端给出的 ready 总数，用来算还有没有下一页 */
  const readyTotal = ref(0)

  /** 已经铺到第几页。0 表示还没拉过 */
  const readyPage = ref(0)

  /**
   * 上传记录：**完整流水**，含已收录成功的。顺序沿用后端（新的在前）。
   *
   * 只拉第一页（100 条）—— 抽屉是"看看最近传过什么"的地方，不做无限滚动。
   */
  const uploadRecords = ref<KnowledgeUploadRecord[]>([])

  /** 服务端给出的记录总数，抽屉标题上的"N 条"用它 */
  const recordTotal = ref(0)

  const loading = ref(false)

  /** 正在续接下一页。与 loading 分开：续接不该把首屏的加载态再亮一次 */
  const loadingMore = ref(false)

  /**
   * 有文件正在收录。
   *
   * 它同时就是"一次只能传一份"的前端约束：`upload` 会一直盯到终态，
   * 所以这个标志从提交一直挂到处理结束，界面据此禁用整个上传区。
   * 服务端的强制拒绝也在（批 ①，见后端 SubmitFile），前端这层仍然保留 ——
   * 少一次注定失败的往返。
   */
  const uploading = ref(false)

  /**
   * 有正文正在收录。
   *
   * 与 uploading 分开：正文收录是同步链路，后端**不过文件收录的闸门**，
   * 所以两者本可以并行；这里只是给弹层一个"提交中"的禁用依据，
   * 不参与"一次只能传一份"的约束。
   */
  const submitting = ref(false)

  /** 本次上传的文档，供新增弹层的"最近一次上传"卡片显示进度 */
  const activeUpload = ref<KnowledgeDocument | null>(null)

  /**
   * 解析环境状态，供"首次上传需要先准备环境"的提示使用。
   *
   * 失败静默：它只是提示，探测不到就当不知道 —— 不该因为一次状态探测失败把上传拦住。
   * 环境备好之后 ready 为真，提示自然消失，前端不需要自己记"是不是第一次"。
   */
  const parserStatus = ref<KnowledgeParserStatus | null>(null)

  async function loadParserStatus() {
    try {
      parserStatus.value = await fetchKnowledgeParserStatus()
    } catch {
      /* 提示用，探测失败不影响上传 */
    }
  }

  /** 搜索关键字，匹配标题与原始文件名（由服务端做模糊匹配） */
  const keyword = ref('')

  const hasMore = computed(() => readyDocuments.value.length < readyTotal.value)

  /** 空态：加载完了但一条 ready 都铺不出来（可能是没有 ready，也可能被搜索过滤光了） */
  const isEmpty = computed(() => !loading.value && readyDocuments.value.length === 0)

  /**
   * 最近一次失败的投递，新增弹层的"上次失败"卡片拿它显示原因。
   *
   * 列表是倒序的，所以取到的第一条 failed 就是最近那次 —— 不用再比时间。
   */
  const lastFailedRecord = computed(
    () => uploadRecords.value.find((record) => record.status === 'failed') ?? null,
  )

  /**
   * 还需要盯着的记录数：等待中 / 处理中 / 失败。工具条上的小角标用它。
   *
   * 不能拿 `uploadRecords.length` 当角标 —— 记录里混着已收录成功的历史，
   * 那会让角标随着"用得多"单调增长，看不出有没有事要管。
   */
  const recordAlerts = computed(
    () => uploadRecords.value.filter((record) => record.status !== 'ready').length,
  )

  // 关键字一变就重新从第一页拉：筛选在服务端，本地不需要再维护"铺到第几条"。
  // 防抖是必须的 —— 否则一个词打三个字就是三次请求，且后到的响应可能先渲染。
  let searchTimer: ReturnType<typeof setTimeout> | null = null
  watch(keyword, () => {
    if (searchTimer) clearTimeout(searchTimer)
    searchTimer = setTimeout(() => {
      void load()
    }, SEARCH_DEBOUNCE_MS)
  })

  /**
   * 拉第一页（连同上传记录）。
   *
   * 两个列表一次并发取回：主页与抽屉是同一屏的两处展示，分两次取会让"上传完成后
   * 抽屉里还挂着旧记录"这种不一致窗口变长。任一失败都算整体失败。
   */
  async function load() {
    loading.value = true
    try {
      const [ready, records] = await Promise.all([
        fetchKnowledgeDocuments({
          page: 1,
          size: KNOWLEDGE_BATCH,
          status: ['ready'],
          keyword: keyword.value,
        }),
        fetchUploadRecords({ page: 1, size: RECORD_PAGE_SIZE }),
      ])
      readyDocuments.value = ready.list
      readyTotal.value = ready.total
      readyPage.value = ready.page
      uploadRecords.value = records.list
      recordTotal.value = records.total
    } finally {
      loading.value = false
    }
  }

  /**
   * 只重拉上传记录。
   *
   * 抽屉每次打开、以及提交后要让新记录立刻出现时用。之所以要单独一条路径：
   * 提交后到终态之间状态翻得很快，把主页也一起重拉会让列表反复闪。
   */
  async function loadRecords() {
    const records = await fetchUploadRecords({ page: 1, size: RECORD_PAGE_SIZE })
    uploadRecords.value = records.list
    recordTotal.value = records.total
  }

  /** 滚到底续接下一页（服务端分页） */
  async function loadMore() {
    if (!hasMore.value || loadingMore.value || loading.value) return
    loadingMore.value = true
    try {
      const next = await fetchKnowledgeDocuments({
        page: readyPage.value + 1,
        size: KNOWLEDGE_BATCH,
        status: ['ready'],
        keyword: keyword.value,
      })
      // 按 id 去重：翻页期间上面若有文档被删或新收录，服务端的偏移会整体前移，
      // 同一篇可能在相邻两页里都出现，直接拼接会在列表里看到重复行。
      const known = new Set(readyDocuments.value.map((document) => document.id))
      readyDocuments.value = [
        ...readyDocuments.value,
        ...next.list.filter((document) => !known.has(document.id)),
      ]
      readyTotal.value = next.total
      readyPage.value = next.page
    } finally {
      loadingMore.value = false
    }
  }

  /**
   * 文档是否已经到终态（ready / failed）。终态之后不会再有变化：
   * 服务端推完最后一帧就关流，轮询也该停。
   */
  function isSettled(document: KnowledgeDocument): boolean {
    return document.status === 'ready' || document.status === 'failed'
  }

  /**
   * "处理超时"与"环境准备卡住"的判据，SSE 与轮询两条路共用一份。
   *
   * 首次上传时后端可能在准备解析环境（分钟级），这段等待不该吃掉"文档处理超时"
   * 的窗口：准备期间只看进度有没有在动，准备结束后窗口从头算（见 PREPARE_STALL_MS）。
   * 上限本身是必须的：一份卡在 pending 的文档（例如 worker 没起来）会把界面永远
   * 锁在"处理中"，而 uploading 一直为真，用户连下一份都传不了。
   */
  function createSettleClock() {
    let deadline = Date.now() + POLL_TIMEOUT_MS
    let lastProgress = ''
    let progressChangedAt = Date.now()
    let wasPreparing = false

    return {
      /** 到点或卡住时抛出。SSE 路每秒调一次（没有事件也要查），轮询路每轮调一次 */
      check() {
        if (parserStatus.value?.preparing) {
          const progress = parserStatus.value.progress
          if (progress !== lastProgress) {
            lastProgress = progress
            progressChangedAt = Date.now()
          }
          if (Date.now() - progressChangedAt >= PREPARE_STALL_MS) {
            throw new Error('解析环境准备似乎卡住了，请稍后刷新查看状态')
          }
          wasPreparing = true
          return
        }
        if (wasPreparing) {
          // 准备刚结束：之前那段时间是环境准备，不是这一份文档的处理时长
          wasPreparing = false
          deadline = Date.now() + POLL_TIMEOUT_MS
        }
        if (Date.now() >= deadline) {
          throw new Error('文档处理超时，请稍后刷新查看状态')
        }
      },
    }
  }

  /**
   * 逐次轮询直到终态，返回最后那一帧。
   *
   * 它现在是 SSE 的兜底路径（流建不起来或中途断开），逻辑与升级前完全一样：
   * 每秒问一次详情，顺便按需问解析环境状态。
   */
  async function pollUntilSettled(
    document: KnowledgeDocument,
    clock = createSettleClock(),
  ): Promise<KnowledgeDocument> {
    let current = document
    while (!isSettled(current)) {
      // 只在"还没问过"或"能力开着但环境没好"时问：
      // 环境备好之后（或压根没开解析能力）这个接口就没必要再打了。
      const unknown = parserStatus.value === null
      const pendingSetup = parserStatus.value?.enabled === true && !parserStatus.value.ready
      if (unknown || pendingSetup) {
        await loadParserStatus()
      }

      clock.check()

      await new Promise((resolve) => setTimeout(resolve, 1000))
      current = await fetchKnowledgeDocument(current.id)
      activeUpload.value = current
    }
    return current
  }

  /**
   * 盯着一份文档直到终态，返回最后那一帧。
   *
   * 上传与重试共用这一段：两者都是"后台在跑、前端盯着状态"，差别只在第一步
   * 怎么把任务交出去。边收帧边把当前帧写进 activeUpload，弹层那张卡片就跟着它动。
   *
   * 主路是 SSE：服务端在有变化时推 document / parser 两帧，正常情况下一次连接
   * 跑到终态，前端不再每秒发一次请求。流不可用（代理不支持长连接、服务重启、
   * 接口没部署）时回退 pollUntilSettled —— 两条路共用同一个 clock，超时口径一致。
   */
  async function watchUntilSettled(document: KnowledgeDocument): Promise<KnowledgeDocument> {
    const clock = createSettleClock()
    const controller = new AbortController()
    let current = document
    let clockError: Error | null = null

    // SSE 只在有变化时才有事件；安静期必须靠本地时钟兜底（worker 挂了就没有事件了），
    // 所以这里按秒查超时/卡住，而不是等下一个事件。
    const timer = window.setInterval(() => {
      try {
        clock.check()
      } catch (error) {
        clockError = error as Error
        controller.abort()
      }
    }, 1000)

    try {
      await watchKnowledgeDocument(
        current.id,
        {
          onDocument: (frame) => {
            current = frame
            activeUpload.value = frame
          },
          onParser: (status) => {
            parserStatus.value = status
          },
        },
        controller.signal,
      )
    } catch (error) {
      // 时钟判定的超时/卡住不是"流不可用"，直接抛；其余情况退回轮询。
      if (clockError) throw clockError
      // 降级不影响界面，但要能在控制台看见原因（后端没重启、接口没部署、
      // 代理不支持长连接都会走到这里）—— 否则表现成"怎么还在每秒发请求"，
      // 排查时只能靠猜。
      console.warn('知识库进度流不可用，已回退到每秒轮询', error)
      return pollUntilSettled(current, clock)
    } finally {
      window.clearInterval(timer)
    }

    // 流正常结束却没到终态（服务端提前关流）：同样回退轮询，而不是把非终态当结果。
    if (!isSettled(current)) {
      console.warn('知识库进度流提前结束，已回退到每秒轮询', current.status)
      return pollUntilSettled(current, clock)
    }
    return current
  }

  /**
   * 拉一次上传记录，失败就当没发生过。
   *
   * 它服务于"提交之后让抽屉立刻有这条"：文件已经在收了，为一次刷新失败把整次
   * 上传报成失败，反而会骗用户重传。
   */
  async function refreshRecordsQuietly() {
    try {
      await loadRecords()
    } catch {
      /* 抽屉的这条晚点到，不影响收录本身 */
    }
  }

  /**
   * 上传并收录一份文件，一路盯到终态。
   *
   * **不把"处理失败"当异常抛出**：失败同样是有效结果（库里留了一行 failed，
   * 调用方要在上传记录里把原因交给用户），所以只有请求本身出错
   * （网络、后端拒绝、等待超时）才抛。
   *
   * 后端此刻若正在收另一份，上传接口会拒（409），错误照样从这里抛出去 ——
   * 前端在上传期间本来就锁着入口，撞上它的是并发场景。
   */
  async function upload(file: File, title?: string): Promise<KnowledgeDocument> {
    uploading.value = true
    try {
      const document = await uploadKnowledgeFile(file, title)
      activeUpload.value = document

      // 提交的同时后端已经写了一条 pending 记录（同一个请求里建的），拉回来让
      // 抽屉立刻有这条。
      await refreshRecordsQuietly()

      const settled = await watchUntilSettled(document)

      // 终态之后整表重拉：这一份的切片数、字符数、updated_at 都是后端在收尾时补的，
      // 记录那一行的状态也是在同一个事务里跟着翻的 —— 别拿流里收到的最后一帧糊弄过去
      await load()
      return settled
    } finally {
      uploading.value = false
      activeUpload.value = null
    }
  }

  /**
   * 重试一次收录失败的投递。
   *
   * **不用重新选文件**：后端把那条 failed 文档改回 pending，输入是失败时归档在
   * 服务器上的原件。所以它与 upload 的差别只有第一步 —— 一个交出新文件，
   * 一个让旧任务重新排队；之后的等待与整表重拉完全一样。
   *
   * 同样置 uploading：重试会占住后台的收录位，期间再传一份会被服务端的闸门拒掉
   * （409），前端先一步把入口锁上，少一次注定失败的往返。
   */
  async function retry(documentId: number): Promise<KnowledgeDocument> {
    uploading.value = true
    try {
      const document = await retryKnowledgeDocument(documentId)
      activeUpload.value = document
      await refreshRecordsQuietly()

      const settled = await watchUntilSettled(document)
      await load()
      return settled
    } finally {
      uploading.value = false
      activeUpload.value = null
    }
  }

  /**
   * 收录一段正文（Markdown），不经过文件与解析。
   *
   * 这条链路是**同步**的：返回时文档已经是 ready 或 failed，没有进度流要盯。
   * 与 upload 的另一处差别在失败路径：后端把文档标成 failed 之后返 400，
   * 这里原样抛出错误给调用方提示 —— 不做"失败也算结果"的处理，因为失败的文档
   * 既不在主页（只查 ready）、也没有上传记录，重拉列表看不到任何东西。
   */
  async function ingestText(content: string, title?: string): Promise<KnowledgeDocument> {
    submitting.value = true
    try {
      const document = await ingestKnowledgeText(content, title)
      // 刷新失败不该把已经成功的收录报成失败：列表晚一点到而已。
      try {
        await load()
      } catch {
        /* 收录本身已经成功 */
      }
      return document
    } finally {
      submitting.value = false
    }
  }

  async function preview(id: number) {
    return fetchKnowledgeDocumentPreview(id)
  }

  /** 删除一篇已收录的知识（切片与向量由数据库级联清理），然后整表重拉 */
  async function removeDocument(id: number) {
    await deleteKnowledgeDocument(id)
    await load()
  }

  /**
   * 删除一条上传记录。
   *
   * 连带后果全在后端那一个事务里：记录对应的文档**若还没收录成功**，会一起被删掉
   * （不收掉的话上传闸门会一直卡着 —— 它数的是 pending + processing 的文档行）；
   * 已经 ready 的文档绝不触碰，只是记录失去关联，那行的状态变成「已收录后删除」。
   *
   * 所以这里要整表重拉而不是只改本地那一行：一次删除可能同时动了两个列表。
   */
  async function removeRecord(id: number) {
    await deleteUploadRecord(id)
    await load()
  }

  return {
    // 数据
    readyDocuments,
    readyTotal,
    uploadRecords,
    recordTotal,
    recordAlerts,
    lastFailedRecord,
    activeUpload,
    parserStatus,
    // 界面状态
    loading,
    uploading,
    submitting,
    keyword,
    hasMore,
    isEmpty,
    // 动作
    load,
    loadRecords,
    loadMore,
    loadParserStatus,
    upload,
    retry,
    ingestText,
    preview,
    removeDocument,
    removeRecord,
  }
})
