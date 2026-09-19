<script setup lang="ts">
/**
 * 解析正文预览。
 *
 * 正文只在 `/knowledge/documents/:id/preview` 这一个接口出网（列表刻意不带它，
 * 一篇文档可能几十万字），所以内容要打开时才拉，`documentId` 一变就重新拉。
 */
import { DialogContent, DialogPortal, DialogRoot, DialogTitle } from 'reka-ui'
import { FileText, Loader2, X } from 'lucide-vue-next'
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'

import { useKnowledgeStore } from '@/stores/knowledge'

const open = defineModel<boolean>('open', { default: false })

const props = defineProps<{ documentId: number | null }>()

const { t } = useI18n()
const store = useKnowledgeStore()

const title = ref('')
const content = ref('')
const loading = ref(false)

watch([open, () => props.documentId], async ([isOpen, id]) => {
  if (!isOpen || id === null) return
  loading.value = true
  title.value = ''
  content.value = ''
  try {
    const document = await store.preview(id)
    title.value = document.title
    content.value = document.content
  } catch (error) {
    toast.error(error instanceof Error ? error.message : t('knowledge.error.preview'))
    open.value = false
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <DialogRoot v-model:open="open">
    <DialogPortal>
      <DialogContent
        class="fixed top-1/2 left-1/2 z-[105] flex max-h-[85vh] w-[min(760px,calc(100vw-2rem))] -translate-x-1/2 -translate-y-1/2 flex-col overflow-hidden rounded-2xl border border-border bg-background shadow-2xl focus:outline-none"
      >
        <header class="flex items-center gap-3 border-b border-border px-5 py-4">
          <FileText class="size-4 shrink-0 text-violet-500" />
          <DialogTitle class="min-w-0 flex-1 truncate text-[15px] font-medium">
            {{ title || t('knowledge.preview.loading') }}
          </DialogTitle>
          <button
            type="button"
            class="cursor-pointer rounded p-1 text-zinc-500 transition-colors hover:bg-muted"
            :title="t('knowledge.preview.close')"
            @click="open = false"
          >
            <X class="size-4" />
          </button>
        </header>

        <div v-if="loading" class="flex flex-1 items-center justify-center p-10">
          <Loader2 class="size-5 animate-spin text-zinc-500" />
        </div>
        <pre v-else class="flex-1 overflow-auto p-5 text-[13px] leading-7 whitespace-pre-wrap">{{
          content || t('knowledge.preview.empty')
        }}</pre>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
