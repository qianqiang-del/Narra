import { defineStore } from 'pinia'
import { computed, ref, watch } from 'vue'

import {
  fetchKnowledgeDocuments,
  fetchKnowledgeDocument,
  fetchKnowledgeDocumentPreview,
  deleteKnowledgeDocument,
  uploadKnowledgeFile,
  type KnowledgeDocument,
} from '@/api/knowledge'

/**
 * 知识库列表状态。
 *
 * 页面结构对齐 `docs/prototypes/knowledge-redesign.html`：
 * - **主页**只列已就绪（ready）的文档，下拉一次续接 KNOWLEDGE_BATCH 条；
 * - **上传记录**装的是"还没收录成功"的那些（等待中 / 处理中 / 失败），只提供删除。
 *   两者互不重叠 —— 所以删掉一条上传记录，不会影响主页上看到的内容。
 * - 收录是异步的：`upload` 拿到的是 pending 文档，靠 `fetchKnowledgeDocument`
 *   轮询到终态，前端据此驱动"处理中"的反馈。
 *
 * ⚠️ 两处临时实现，都是等后端批次落地后替换，**组件不需要跟着改**：
 * 1. 搜索是前端过滤 —— 后端 `GET /knowledge/documents` 目前只认 page/size。
 *    因此一次把后端上限拉满（见 LIST_PAGE_SIZE），否则会漏掉"第一页全是 failed、
 *    ready 排在第二页"这种情况。
 *    TODO(批 ①)：改成 `status=ready` + `keyword` 的服务端筛选与分页。
 * 2. "上传记录"是从文档列表里挑出非 ready 的行 —— 批 ② 之前"一条记录 = 一行文档"，
 *    所以删记录等同于删那篇文档；因为抽屉里不出现 ready 文档，这个差异在界面上不可见。
 *    TODO(批 ②)：改读 `GET /knowledge/upload-records`，删除改调 `DELETE /upload-records/:id`。
 */
export const useKnowledgeStore = defineStore('knowledge', () => {
  /** 主页每次"下拉"续接的条数 */
  const KNOWLEDGE_BATCH = 10

  /** 列表一次拉多少条。与后端 service.maxPageSize 对齐（100） */
  const LIST_PAGE_SIZE = 100

  /** 轮询上限。一份大文档解析几分钟很正常，但不能无限等 */
  const POLL_TIMEOUT_MS = 15 * 60 * 1000

  /** 后端列表的最近一次快照，含全部状态 */
  const documents = ref<KnowledgeDocument[]>([])
  const loading = ref(false)

  /**
   * 有文件正在收录。
   *
   * 它同时就是"一次只能传一份"的前端约束：`upload` 会一直轮询到终态，
   * 所以这个标志从提交一直挂到处理结束，界面据此禁用整个上传区。
   * 服务端的强制拒绝属于批 ①，到那时前端这层仍然保留（少一次注定失败的往返）。
   */
  const uploading = ref(false)

  /** 本次上传的文档，供新增弹层的"最近一次上传"卡片显示进度 */
  const activeUpload = ref<KnowledgeDocument | null>(null)

  /** 搜索关键字，匹配标题与原始文件名 */
  const keyword = ref('')

  /** 主页已经铺到第几条 */
  const visible = ref(KNOWLEDGE_BATCH)

  /** 主页数据：只有已就绪的文档 */
  const readyDocuments = computed(() =>
    documents.value.filter((document) => document.status === 'ready'),
  )

  /** 主页数据再叠一层搜索 */
  const matchedDocuments = computed(() => {
    const needle = keyword.value.trim().toLowerCase()
    if (!needle) return readyDocuments.value
    return readyDocuments.value.filter(
      (document) =>
        document.title.toLowerCase().includes(needle) ||
        document.sourceUri.toLowerCase().includes(needle),
    )
  })

  /** 当前真正铺在页面上的那些 */
  const shownDocuments = computed(() => matchedDocuments.value.slice(0, visible.value))

  const hasMore = computed(() => visible.value < matchedDocuments.value.length)

  /** 空态：加载完了但一条都铺不出来（可能是没有 ready，也可能被搜索过滤光了） */
  const isEmpty = computed(() => !loading.value && shownDocuments.value.length === 0)

  /** 上传记录 = 还没收录成功的文档；顺序沿用后端（新的在前） */
  const uploadRecords = computed(() => documents.value.filter((d) => d.status !== 'ready'))

  /** 最近一次失败的记录，卡片拿它显示失败原因 */
  const lastFailedRecord = computed(
    () => uploadRecords.value.find((d) => d.status === 'failed') ?? null,
  )

  // 关键字一变就回到第一批：否则从宽到窄时，visible 会停在一个早已越界的下标上
  watch(keyword, () => {
    visible.value = KNOWLEDGE_BATCH
  })

  function loadMore() {
    if (!hasMore.value) return
    visible.value = Math.min(visible.value + KNOWLEDGE_BATCH, matchedDocuments.value.length)
  }

  /** 把一条文档并进列表（有则替换、无则插到最前），用于上传过程中原地刷新进度 */
  function upsert(document: KnowledgeDocument) {
    const index = documents.value.findIndex((item) => item.id === document.id)
    if (index >= 0) documents.value[index] = document
    else documents.value.unshift(document)
  }

  /**
   * 拉取列表。
   *
   * 一次拉满上限：主页只显示 ready、而筛选还在前端做（见文件头 TODO），
   * 分页拉取会漏掉排在后面的 ready 文档。
   */
  async function load() {
    loading.value = true
    try {
      const result = await fetchKnowledgeDocuments(1, LIST_PAGE_SIZE)
      documents.value = result.list
    } finally {
      loading.value = false
    }
  }

  /**
   * 上传并收录一份文件，一路轮询到终态。
   *
   * **不把"处理失败"当异常抛出**：失败同样是有效结果（库里留了一行 failed，
   * 调用方要在上传记录里把原因交给用户），所以只有请求本身出错
   * （网络、后端拒绝、轮询超时）才抛。
   */
  async function upload(file: File, title?: string): Promise<KnowledgeDocument> {
    uploading.value = true
    try {
      let document = await uploadKnowledgeFile(file, title)
      activeUpload.value = document
      upsert(document)

      const deadline = Date.now() + POLL_TIMEOUT_MS
      while (document.status === 'pending' || document.status === 'processing') {
        if (Date.now() >= deadline) throw new Error('文档处理超时，请稍后刷新查看状态')
        await new Promise((resolve) => setTimeout(resolve, 1000))
        document = await fetchKnowledgeDocument(document.id)
        activeUpload.value = document
        upsert(document)
      }

      // 终态之后整表重拉：切片数、updated_at 这些由后端在收尾时补，
      // 别拿轮询到的最后一帧糊弄过去
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
    documents,
    readyDocuments,
    matchedDocuments,
    shownDocuments,
    uploadRecords,
    lastFailedRecord,
    activeUpload,
    // 界面状态
    loading,
    uploading,
    keyword,
    visible,
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
