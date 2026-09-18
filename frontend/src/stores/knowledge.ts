import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

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
 * 后端是异步收录：`upload` 拿到的是 pending 文档，之后靠 fetchKnowledgeDocument(id)
 * 轮询，直到 status 落到 ready / failed。
 *
 * 已知短板：轮询绑在 `upload` 里面，所以只有"这一次上传"会被跟踪 —— 刷新页面或重新
 * 进入页面时，库里仍在 pending / processing 的行不会自动更新，要等手动刷新。把轮询
 * 提到 store 层（发现有非终态就定时拉详情）是已知的下一步。
 */
export const useKnowledgeStore = defineStore('knowledge', () => {
  const documents = ref<KnowledgeDocument[]>([])
  const total = ref(0)
  const page = ref(1)
  const size = ref(20)
  const totalPage = ref(0)

  /** 列表加载中 */
  const loading = ref(false)
  /** 有文件正在收录 —— 这个状态要一直挂到后端返回，可能好几秒 */
  const uploading = ref(false)

  const isEmpty = computed(() => !loading.value && documents.value.length === 0)

  /**
   * 拉取列表。
   *
   * 不传页码时拉当前页：收录完一篇之后原地刷新最自然，跳回第一页会让用户
   * 正在看的那一段跑掉。
   */
  async function load(targetPage = page.value, targetSize = size.value) {
    loading.value = true
    try {
      const result = await fetchKnowledgeDocuments(targetPage, targetSize)
      documents.value = result.list
      total.value = result.total
      page.value = result.page
      size.value = result.size
      totalPage.value = result.totalPage
    } finally {
      loading.value = false
    }
  }

  /**
   * 上传并收录一份文件：先落库（pending），再轮询到终态，然后回到第一页。
   *
   * 返回后端给出的文档：失败时它也会返回（库里已经有一行 status = failed 的记录），
   * 调用方要用它取失败原因给用户看，所以这里不吞异常、也不把失败当成"没返回值"。
   */
  async function upload(file: File, title?: string): Promise<KnowledgeDocument> {
    uploading.value = true
    try {
      let document = await uploadKnowledgeFile(file, title)
      await load(1)
      const deadline = Date.now() + 15 * 60 * 1000
      while (document.status === 'pending' || document.status === 'processing') {
        if (Date.now() >= deadline) throw new Error('文档处理超时，请稍后刷新查看状态')
        await new Promise((resolve) => setTimeout(resolve, 1000))
        document = await fetchKnowledgeDocument(document.id)
        const index = documents.value.findIndex((item) => item.id === document.id)
        if (index >= 0) documents.value[index] = document
      }
      await load(1)
      if (document.status === 'failed') throw new Error(document.error || '文档处理失败')
      return document
    } finally {
      uploading.value = false
    }
  }

  function goToPage(target: number) {
    const clamped = Math.min(Math.max(target, 1), Math.max(totalPage.value, 1))
    if (clamped === page.value) return Promise.resolve()
    return load(clamped)
  }

  async function preview(id: number) {
    return fetchKnowledgeDocumentPreview(id)
  }

  async function remove(id: number) {
    await deleteKnowledgeDocument(id)
    await load(page.value)
  }

  return {
    documents,
    total,
    page,
    size,
    totalPage,
    loading,
    uploading,
    isEmpty,
    load,
    upload,
    goToPage,
    preview,
    remove,
  }
})
