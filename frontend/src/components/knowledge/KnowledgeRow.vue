<script setup lang="ts">
/**
 * 知识库列表的一行 —— **主页与上传记录抽屉共用同一个组件**。
 *
 * 这是原型的核心决策：两个列表的信息层级本来就一样（状态 → 标题 → 原始文件名 →
 * 失败原因 → 元信息），各写一套只会让留白、字号和 hover 效果慢慢漂移。
 * 差异收敛到两个 props（`showStatus`）与一个插槽（操作按钮）。
 */
import { AlertCircle, CheckCircle2, Loader2 } from 'lucide-vue-next'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { KnowledgeDocument, KnowledgeDocumentStatus } from '@/api/knowledge'
import { cn } from '@/lib/utils'

const props = defineProps<{
  document: KnowledgeDocument
  /**
   * 是否显示状态徽章。
   * 主页只列 ready，标出来是噪音；抽屉里混着等待中/处理中/失败，必须标。
   */
  showStatus?: boolean
}>()

const { t } = useI18n()

const marks: Record<
  KnowledgeDocumentStatus,
  { icon: typeof CheckCircle2; class: string }
> = {
  ready: { icon: CheckCircle2, class: 'text-emerald-500' },
  failed: { icon: AlertCircle, class: 'text-red-500' },
  processing: { icon: Loader2, class: 'animate-spin text-amber-500' },
  pending: { icon: Loader2, class: 'animate-spin text-amber-500' },
}

const statusStyles: Record<KnowledgeDocumentStatus, string> = {
  ready:
    'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-300',
  failed:
    'border-red-200 bg-red-50 text-red-700 dark:border-red-800 dark:bg-red-950/40 dark:text-red-300',
  processing:
    'border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-800 dark:bg-amber-950/40 dark:text-amber-300',
  pending: 'border-border bg-muted text-muted-foreground',
}

const mark = computed(() => marks[props.document.status] ?? marks.pending)

const statusLabel = computed(() => t(`knowledge.status.${props.document.status}`))

const sourceLabel = computed(() =>
  t(
    `knowledge.source.${
      props.document.sourceType === 'manual' || props.document.sourceType === 'api'
        ? props.document.sourceType
        : 'import'
    }`,
  ),
)

function formatNumber(value: number) {
  return value.toLocaleString()
}

function formatDate(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}
</script>

<template>
  <li
    class="group flex items-start gap-3 rounded-xl px-3 py-3.5 transition-colors hover:bg-muted/50"
  >
    <component :is="mark.icon" :class="cn('mt-0.5 size-4 shrink-0', mark.class)" />

    <div class="min-w-0 flex-1">
      <div class="flex flex-wrap items-center gap-2">
        <span class="min-w-0 truncate text-sm font-medium text-foreground">{{ document.title }}</span>

        <span
          v-if="showStatus"
          :class="cn(
            'shrink-0 rounded-full border px-2 py-0.5 text-xs leading-none',
            statusStyles[document.status],
          )"
        >
          {{ statusLabel }}
        </span>
        <span
          v-else
          class="shrink-0 rounded-full border border-border px-2 py-0.5 text-xs leading-none text-zinc-600 dark:text-zinc-400"
        >
          {{ sourceLabel }}
        </span>
      </div>

      <!--
        副信息一律用**实心**中性灰（`text-zinc-600` ≈ #52525b）。
        原来这里写的是 `text-muted-foreground/70`：token 本身只有 oklch(0.556) ≈ #737373，
        再乘 70% 透明度后白底上只剩 ≈#a3a3a3，对比度 ~2.7:1 —— 12px 的字根本立不住。
        列表里除标题外每一段都踩了"又小又淡"，整页看起来就是糊的。
      -->
      <p v-if="document.sourceUri" class="mt-1 truncate text-[13px] text-zinc-600 dark:text-zinc-400">
        {{ document.sourceUri }}
      </p>

      <!-- 失败原因原样展示：后端那句里写着卡在哪一步 -->
      <p
        v-if="document.error"
        class="mt-1.5 rounded-md bg-red-50 px-2 py-1.5 text-xs break-words text-red-700 dark:bg-red-950/30 dark:text-red-300"
      >
        {{ document.error }}
      </p>

      <div class="mt-2 flex flex-wrap items-center gap-x-2 gap-y-1 text-[13px] text-zinc-600 dark:text-zinc-400">
        <template v-if="document.parser">
          <span>{{ document.parser }}</span>
          <span class="text-zinc-300 dark:text-zinc-700">·</span>
        </template>
        <span>{{ t('knowledge.col.chunks') }} {{ formatNumber(document.chunks) }}</span>
        <span class="text-zinc-300 dark:text-zinc-700">·</span>
        <span>{{ t('knowledge.col.characters') }} {{ formatNumber(document.characters) }}</span>
        <span class="text-zinc-300 dark:text-zinc-700">·</span>
        <span>{{ t('knowledge.col.updatedAt') }} {{ formatDate(document.updatedAt) }}</span>
      </div>
    </div>

    <!--
      操作按钮不再整块乘 55% 透明度：那会让"查看 / 删除"平时几乎看不见（触屏更是没有 hover）。
      默认就是可读的实心色，hover 时再加深。
    -->
    <div class="flex shrink-0 items-center gap-1.5 self-center">
      <slot name="actions" />
    </div>
  </li>
</template>
