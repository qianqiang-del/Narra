import { computed, ref } from 'vue'

import {
  DEFAULT_UPLOAD_LIMITS,
  fetchKnowledgeUploadLimits,
  type KnowledgeUploadLimits,
} from '@/api/knowledge'

/**
 * 上传限制的共享加载：首次调用发一次请求，之后所有组件复用同一份结果。
 *
 * 真实值由后端下发（GET /knowledge/documents/upload-limits，来源是 knowledge_ingest
 * 配置）；请求失败时保留 DEFAULT_UPLOAD_LIMITS 兜底，界面照常可用。首页工具栏与
 * 工作台对话栏都用它渲染"单个不超过 X"的提示，避免把上限写死在文案里。
 */
const limits = ref<KnowledgeUploadLimits>({ ...DEFAULT_UPLOAD_LIMITS })
let loading: Promise<void> | null = null

/** 单个文件上限的展示文本，如 "16MB"（整数不带小数，非整数保留一位）。 */
const sizeLabel = computed(() => formatLimitSize(limits.value.maxFileBytes))

export function useUploadLimits() {
  if (!loading) {
    loading = fetchKnowledgeUploadLimits()
      .then((value) => {
        limits.value = value
      })
      .catch(() => {
        /* 保留兜底默认值：限制以服务端校验为准，这里只影响提示文案 */
      })
  }
  return { limits, sizeLabel }
}

function formatLimitSize(bytes: number): string {
  const mb = bytes / (1024 * 1024)
  return `${Number.isInteger(mb) ? mb : mb.toFixed(1)}MB`
}
