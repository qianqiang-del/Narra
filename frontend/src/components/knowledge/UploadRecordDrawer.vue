<script setup lang="ts">
/**
 * 上传记录抽屉 —— 只装"还没收录成功"的那些（等待中 / 处理中 / 失败），只提供删除。
 *
 * 它与主页互补：主页是已收录的，抽屉是没收录成功的。所以"删记录"只影响抽屉，
 * 主页不受影响 —— 这正是原型里"两处删除互不影响"在当前数据模型下能成立的原因。
 *
 * 批 ② 之后这里会换成 `GET /knowledge/upload-records`，届时抽屉里还能出现
 * "已收录但文档已被删除"的历史行；现在没有记录表，那种行本来就派生不出来。
 */
import { X, Trash2 } from 'lucide-vue-next'
import { computed, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import KnowledgeRow from './KnowledgeRow.vue'
import type { KnowledgeDocument } from '@/api/knowledge'
import { useKnowledgeStore } from '@/stores/knowledge'

const open = defineModel<boolean>('open', { default: false })

const emit = defineEmits<{ remove: [document: KnowledgeDocument] }>()

const { t } = useI18n()
const store = useKnowledgeStore()

const records = computed(() => store.uploadRecords)

/**
 * 抽屉打开时锁住页面滚动。
 * 不锁的话，页面自身那条滚动条会一直挂在抽屉右边，和抽屉内部这条并排出现。
 * （`html` 上有 `overflow-y: scroll`，所以只需要处理 body。）
 */
watch(open, (isOpen) => {
  document.body.style.overflow = isOpen ? 'hidden' : ''
})

onUnmounted(() => {
  document.body.style.overflow = ''
})
</script>

<template>
  <Teleport to="body">
    <div
      class="fixed inset-0 z-[55] bg-black/30 transition-opacity duration-200"
      :class="open ? 'opacity-100' : 'pointer-events-none opacity-0'"
      @click="open = false"
    />

    <aside
      class="fixed top-0 right-0 bottom-0 z-[60] flex w-[min(100vw,max(50vw,420px))] flex-col border-l border-border bg-background shadow-2xl transition-transform duration-200"
      :class="open ? 'translate-x-0' : 'translate-x-full'"
    >
      <header class="flex shrink-0 items-center gap-3 border-b border-border px-5 py-3.5">
        <h2 class="flex-1 text-[15px] font-medium">{{ t('knowledge.records.title') }}</h2>
        <span class="text-[13px] text-zinc-600 dark:text-zinc-400">
          {{ t('knowledge.records.count', { total: records.length }) }}
        </span>
        <button
          type="button"
          class="cursor-pointer rounded-lg p-1 text-zinc-500 transition-colors hover:bg-muted hover:text-foreground"
          :title="t('knowledge.preview.close')"
          @click="open = false"
        >
          <X class="size-4" />
        </button>
      </header>

      <p
        v-if="records.length"
        class="shrink-0 border-b border-border px-5 py-2 text-xs leading-5 text-zinc-600 dark:text-zinc-400"
      >
        {{ t('knowledge.records.hint') }}
      </p>

      <div class="min-h-0 flex-1 overflow-y-auto py-1">
        <ul v-if="records.length" class="divide-y divide-border/60">
          <KnowledgeRow
            v-for="document in records"
            :key="document.id"
            :document="document"
            show-status
          >
            <template #actions>
              <button
                type="button"
                class="inline-flex cursor-pointer items-center gap-1 rounded-md border border-red-200 px-2.5 py-1 text-xs text-red-600 transition-colors hover:bg-red-50 dark:border-red-900 dark:text-red-400 dark:hover:bg-red-950/40"
                @click="emit('remove', document)"
              >
                <Trash2 class="size-3.5" />
                {{ t('knowledge.action.delete') }}
              </button>
            </template>
          </KnowledgeRow>
        </ul>

        <p v-else class="px-5 py-12 text-center text-[13px] text-zinc-600 dark:text-zinc-400">
          {{ t('knowledge.records.empty') }}
        </p>
      </div>
    </aside>
  </Teleport>
</template>
