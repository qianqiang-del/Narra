<script setup lang="ts">
import { computed } from 'vue'
import { X } from 'lucide-vue-next'

import MarkdownText from '@/components/shared/MarkdownText.vue'
import type { WhiteboardEntry } from '@/lib/classroomDiscussion'

const props = defineProps<{
  artifacts: WhiteboardEntry[]
  selectedId: string | null
}>()

const emit = defineEmits<{
  (e: 'select', id: string): void
  (e: 'close'): void
}>()

const selected = computed(() => {
  if (!props.artifacts.length) return null
  return props.artifacts.find((item) => item.id === props.selectedId) ?? props.artifacts.at(-1) ?? null
})
</script>

<template>
  <section
    data-testid="discussion-whiteboard"
    role="dialog"
    aria-label="互动白板"
    class="absolute inset-3 z-[110] flex min-h-0 overflow-hidden rounded-xl border border-brand-200 bg-white/95 shadow-2xl ring-1 ring-black/5 backdrop-blur dark:border-brand-900 dark:bg-slate-900/95"
  >
    <aside class="flex w-40 shrink-0 flex-col border-r border-brand-100 bg-brand-50/60 dark:border-brand-950 dark:bg-brand-950/20">
      <div class="flex items-center justify-between border-b border-brand-100 px-3 py-2.5 dark:border-brand-950">
        <h2 class="text-xs font-semibold text-brand-900 dark:text-brand-100">互动白板</h2>
        <button type="button" aria-label="关闭白板" class="rounded p-1 text-brand-800 hover:bg-brand-100 dark:text-brand-200 dark:hover:bg-brand-900/50" @click="emit('close')">
          <X class="size-3.5" />
        </button>
      </div>
      <div v-if="!artifacts.length" class="flex flex-1 items-center justify-center px-3 text-center text-[11px] leading-5 text-brand-800/70 dark:text-brand-200/70">
        讨论中还没有白板内容
      </div>
      <div v-else class="min-h-0 flex-1 space-y-1 overflow-y-auto p-2">
        <button
          v-for="item in artifacts"
          :key="item.id"
          type="button"
          :aria-pressed="item.id === selected?.id"
          :class="item.id === selected?.id ? 'border-brand-300 bg-white text-brand-900 shadow-sm dark:border-brand-800 dark:bg-slate-800 dark:text-brand-100' : 'border-transparent text-brand-800/75 hover:border-brand-200 hover:bg-card/70 dark:text-brand-200/75 dark:hover:border-brand-900 dark:hover:bg-slate-800/70'"
          class="w-full rounded-md border px-2.5 py-2 text-left text-[11px] leading-4 transition-colors"
          @click="emit('select', item.id)"
        >
          <span class="block truncate font-medium">{{ item.title }}</span>
          <span class="mt-0.5 block text-[10px] opacity-70">{{ item.kind }}</span>
        </button>
      </div>
    </aside>

    <article v-if="selected" class="min-w-0 flex-1 overflow-y-auto px-5 py-4 text-slate-700 dark:text-slate-200">
      <header class="mb-3 border-b border-slate-200 pb-2.5 dark:border-slate-700">
        <h3 class="text-base font-semibold tracking-tight text-slate-900 dark:text-slate-100">{{ selected.title }}</h3>
        <p class="mt-0.5 text-[10px] uppercase tracking-wider text-brand-700 dark:text-brand-300">{{ selected.kind }}</p>
      </header>
      <MarkdownText :source="selected.content" />
    </article>
    <div v-else class="flex min-w-0 flex-1 items-center justify-center text-xs text-slate-400 dark:text-slate-500">
      选择一条白板内容
    </div>
  </section>
</template>
