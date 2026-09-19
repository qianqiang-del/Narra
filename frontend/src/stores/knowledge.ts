import { defineStore } from 'pinia'
import { computed, ref, watch } from 'vue'

import {
  fetchKnowledgeDocuments,
  fetchKnowledgeDocument,
  fetchKnowledgeDocumentPreview,
  deleteKnowledgeDocument,
  uploadKnowledgeFile,
  type KnowledgeDocument,
  type KnowledgeDocumentStatus,
} from '@/api/knowledge'

/**
 * 知识库列表状态。
 *
 * 页面结构对齐 `docs/prototypes/knowledge-redesign.html`：
 * - **主页**只列已就绪（ready）的文档，滚到底由服务端续接下一页；
 * - **上传记录**装的是"还没收录成功"的那些（等待中 / 处理中 / 失败），只提供删除。
 *   两者互不重叠 —— 所以删掉一条上传记录，不会影响主页上看到的内容。
 * - 收录是异步的：`upload` 拿到的是 pending 文档，靠 `fetchKnowledgeDocument`
 *   轮询到终态，前端据此驱动"处理中"的反馈。
 *
 * 两个列表都走**服务端筛选与分页**（批 ①）：主页问 `status=ready`，上传记录问
 * `status=pending,processing,failed`。在那之前是一次拉满上限再在前端 filter ——
 * 而列表本身是分页的，只看得到已经拉下来的那几页，"第一页全是 failed、
 * ready 排在第二页"会被过滤成空列表。
 *
 * ⚠️ 一处临时实现，等批 ② 落地后替换，**组件不需要跟着改**：
 * "上传记录"是从文档列表里挑出非 ready 的行 —— 批 ② 之前"一条记录 = 一行文档"，
 * 所以删记录等同于删那篇文档；因为抽屉里不出现 ready 文档，这个差异在界面上不可见。
 *    TODO(批 ②)：改读 `GET /knowledge/upload-records`，删除改调 `DELETE /upload-records/:id`。
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

  /** "还没收录成功"的三个状态，就是上传记录的取值范围 */
  const RECORD_STATUSES: KnowledgeDocumentStatus[] = ['pending', 'processing', 'failed']

  /** 主页数据：已收录的文档。按页从服务端取回后依次累加 */
  const readyDocuments = ref<KnowledgeDocument[]>([])

  /** 服务端给出的 ready 总数，用来算还有没有下一页 */
  const readyTotal = ref(0)

  /** 已经铺到第几页。0 表示还没拉过 */
  const readyPage = ref(0)

  /** 上传记录：等待中 / 处理中 / 失败。顺序沿用后端（新的在前） */
  const uploadRecords = ref<KnowledgeDocument[]>([])

  const loading = ref(false)

  /** 正在续接下一页。与 loading 分开：续接不该把首屏的加载态再亮一次 */
  const loadingMore = ref(false)

  /**
   * 有文件正在收录。
   *
   * 它同时就是"一次只能传一份"的前端约束：`upload` 会一直轮询到终态，
   * 所以这个标志从提交一直挂到处理结束，界面据此禁用整个上传区。
   * 服务端的强制拒绝也在（批 ①，见后端 SubmitFile），前端这层仍然保留 ——
   * 少一次注定失败的往返。
   */
  const uploading = ref(false)

  /** 本次上传的文档，供新增弹层的"最近一次上传"卡片显示进度 */
  const activeUpload = ref<KnowledgeDocument | null>(null)

  /** 搜索关键字，匹配标题与原始文件名（由服务端做模糊匹配） */
  const keyword = ref('')

  const hasMore = computed(() => readyDocuments.value.length < readyTotal.value)

  /** 空态：加载完了但一条 ready 都铺不出来（可能是没有 ready，也可能被搜索过滤光了） */
  const isEmpty = computed(() => !loading.value && readyDocuments.value.length === 0)

  /** 最近一次失败的记录，卡片拿它显示失败原因 */
  const lastFailedRecord = computed(
    () => uploadRecords.value.find((d) => d.status === 'failed') ?? null,
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
        fetchKnowledgeDocuments({ page: 1, size: RECORD_PAGE_SIZE, status: RECORD_STATUSES }),
      ])
      readyDocuments.value = ready.list
      readyTotal.value = ready.total
      readyPage.value = ready.page
      uploadRecords.value = records.list
    } finally {
      loading.value = false
    }
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

  /** 把一条文档并进上传记录（有则替换、无则插到最前），用于上传过程中原地刷新进度 */
  function upsertRecord(document: KnowledgeDocument) {
    const index = uploadRecords.value.findIndex((item) => item.id === document.id)
    if (index >= 0) uploadRecords.value[index] = document
    else uploadRecords.value.unshift(document)
  }

  /**
   * 上传并收录一份文件，一路轮询到终态。
   *
   * **不把"处理失败"当异常抛出**：失败同样是有效结果（库里留了一行 failed，
   * 调用方要在上传记录里把原因交给用户），所以只有请求本身出错
   * （网络、后端拒绝、轮询超时）才抛。
   *
   * 后端此刻若正在收另一份，上传接口会拒（409），错误照样从这里抛出去 ——
   * 前端在上传期间本来就锁着入口，撞上它的是并发场景。
   */
  async function upload(file: File, title?: string): Promise<KnowledgeDocument> {
    uploading.value = true
    try {
      let document = await uploadKnowledgeFile(file, title)
      activeUpload.value = document
      upsertRecord(document)

      const deadline = Date.now() + POLL_TIMEOUT_MS
      while (document.status === 'pending' || document.status === 'processing') {
        if (Date.now() >= deadline) throw new Error('文档处理超时，请稍后刷新查看状态')
        await new Promise((resolve) => setTimeout(resolve, 1000))
        document = await fetchKnowledgeDocument(document.id)
        activeUpload.value = document
        upsertRecord(document)
      }

      // 终态之后整表重拉：这一份要从上传记录移到主页，切片数、updated_at 这些
      // 也是后端在收尾时补的，别拿轮询到的最后一帧糊弄过去
      await load()
      return document
    } finally {
      uploading.value = false
      activeUpload.value = null
    }
  }

  async function preview(id: number) {
    return fetchKnowledgeDocumentPreview(id)
  }

  /** 删除一篇文档（切片与向量由数据库级联清理），然后整表重拉 */
  async function remove(id: number) {
    await deleteKnowledgeDocument(id)
    await load()
  }

  return {
    // 数据
    readyDocuments,
    readyTotal,
    uploadRecords,
    lastFailedRecord,
    activeUpload,
    // 界面状态
    loading,
    uploading,
    keyword,
    hasMore,
    isEmpty,
    // 动作
    load,
    loadMore,
    upload,
    preview,
    remove,
  }
})
