<script setup lang="ts">
/**
 * 知识库列表的一行 —— **主页与上传记录抽屉共用同一个组件**。
 *
 * 两个列表的信息层级本来就一样（状态 → 标题 → 原始文件名 → 失败原因 → 元信息），
 * 各写一套只会让留白、字号和 hover 效果慢慢漂移。
 *
 * 但批 ② 之后两条数据的**来源不同了**：主页是 `KnowledgeDocument`（有解析器、
 * 切片数、字符数），抽屉是 `KnowledgeUploadRecord`（有原始文件名、字节数、投递时间）。
 * 差异就收敛在这里 —— 组件把两种形状各自摊平成一行的字段，调用方只管把手上的
 * 那一条丢进来，不用挑 props、也不用自己拼元信息。
 */
import { AlertCircle, Archive, CheckCircle2, Loader2 } from 'lucide-vue-next'
import type { Component } from 'vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type {
  KnowledgeBadgeStatus,
  KnowledgeDocument,
  KnowledgeDocumentStage,
  KnowledgeUploadRecord,
} from '@/api/knowledge'
import { failureStageKey } from '@/api/knowledge'
import { cn } from '@/lib/utils'

const props = defineProps<{
  /** 主页的一行：一篇已收录的文档 */
  document?: KnowledgeDocument
  /** 抽屉里的一行：一次文件投递的流水 */
  record?: KnowledgeUploadRecord
}>()

const { t } = useI18n()

const marks: Record<KnowledgeBadgeStatus, { icon: Component; class: string }> = {
  ready: { icon: CheckCircle2, class: 'text-emerald-500' },
  // 收录曾经成功、文档后来被删：用"归档"而不是打叉 —— 那次投递本身没错
  removed: { icon: Archive, class: 'text-zinc-400' },
  failed: { icon: AlertCircle, class: 'text-red-500' },
  processing: { icon: Loader2, class: 'animate-spin text-amber-500' },
}

const badgeStyles: Record<KnowledgeBadgeStatus, string> = {
  ready:
    'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-300',
  removed: 'border-border bg-muted text-zinc-600 dark:text-zinc-400',
  failed:
    'border-red-200 bg-red-50 text-red-700 dark:border-red-800 dark:bg-red-950/40 dark:text-red-300',
  processing:
    'border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-800 dark:bg-amber-950/40 dark:text-amber-300',
}

/**
 * 记录的展示状态：把后端四个原始状态收成四个徽章。
 *
 * 等待中与处理中对用户是同一件事（"还没好"），都读作处理中；ready 则要再看文档
 * 还在不在 —— 记录还在、文档没了，读作「已收录后删除」。
 */
function recordBadge(record: KnowledgeUploadRecord): KnowledgeBadgeStatus {
  if (record.status === 'ready') return record.documentId == null ? 'removed' : 'ready'
  if (record.status === 'failed') return 'failed'
  return 'processing'
}

function formatNumber(value: number) {
  return value.toLocaleString()
}

function formatDate(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

function formatSize(bytes: number) {
  if (!bytes) return ''
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}

interface RowModel {
  title: string
  subtitle: string
  error: string
  /** 失败行的粗粒度阶段（"解析阶段失败"等）；非失败或阶段未知时为空串 */
  stageLabel: string
  icon: Component
  iconClass: string
  badge: { label: string; class: string } | null
  tag: string | null
  meta: string[]
}

/**
 * 把失败位置翻成一句粗粒度说明，拼在具体错误前面。
 *
 * 位置映射（细粒度优先、粗粒度兜底）统一在 `failureStageKey` 里，两个组件共用一份。
 */
function failureStageLabel(status: string, stage: KnowledgeDocumentStage | '', failedStage: string): string {
  if (status !== 'failed') return ''
  const key = failureStageKey(failedStage, stage)
  return key ? t(`knowledge.stage.${key}`) : ''
}

const model = computed<RowModel>(() => {
  const record = props.record
  if (record) {
    const status = recordBadge(record)
    return {
      title: record.title,
      // 文档被删之后标题会回落到原始文件名，此时两行字一模一样 —— 别重复显示
      subtitle: record.title === record.originalName ? '' : record.originalName,
      error: record.error,
      stageLabel: failureStageLabel(record.status, record.stage, record.failedStage),
      icon: marks[status].icon,
      iconClass: marks[status].class,
      badge: { label: t(`knowledge.records.status.${status}`), class: badgeStyles[status] },
      tag: null,
      meta: [
        formatSize(record.sizeBytes),
        `${t('knowledge.records.col.created')} ${formatDate(record.createdAt)}`,
      ].filter(Boolean),
    }
  }

  const document = props.document as KnowledgeDocument
  return {
    title: document.title,
    subtitle: document.sourceUri,
    error: document.error,
    stageLabel: failureStageLabel(document.status, document.stage, document.failedStage),
    icon: marks.ready.icon,
    iconClass: marks.ready.class,
    badge: null,
    tag: t(`knowledge.source.${document.sourceType === 'api' ? 'api' : document.sourceType === 'manual' ? 'manual' : 'import'}`),
    meta: [
      document.parser,
      `${t('knowledge.col.chunks')} ${formatNumber(document.chunks)}`,
      `${t('knowledge.col.characters')} ${formatNumber(document.characters)}`,
      `${t('knowledge.col.updatedAt')} ${formatDate(document.updatedAt)}`,
    ].filter(Boolean),
  }
})
</script>

<template>
  <li
    class="group flex items-start gap-3 rounded-xl px-3 py-3.5 transition-colors hover:bg-muted/50"
  >
    <component :is="model.icon" :class="cn('mt-0.5 size-4 shrink-0', model.iconClass)" />

    <div class="min-w-0 flex-1">
      <div class="flex flex-wrap items-center gap-2">
        <span class="min-w-0 truncate text-sm font-medium text-foreground">{{ model.title }}</span>

        <span
          v-if="model.badge"
          :class="cn(
            'shrink-0 rounded-full border px-2 py-0.5 text-xs leading-none',
            model.badge.class,
          )"
        >
          {{ model.badge.label }}
        </span>
        <span
          v-else-if="model.tag"
          class="shrink-0 rounded-full border border-border px-2 py-0.5 text-xs leading-none text-zinc-600 dark:text-zinc-400"
        >
          {{ model.tag }}
        </span>
      </div>

      <!--
        副信息一律用**实心**中性灰（`text-zinc-600` ≈ #52525b）。
        原来这里写的是 `text-muted-foreground/70`：token 本身只有 oklch(0.556) ≈ #737373，
        再乘 70% 透明度后白底上只剩 ≈#a3a3a3，对比度 ~2.7:1 —— 12px 的字根本立不住。
        列表里除标题外每一段都踩了"又小又淡"，整页看起来就是糊的。
      -->
      <p v-if="model.subtitle" class="mt-1 truncate text-[13px] text-zinc-600 dark:text-zinc-400">
        {{ model.subtitle }}
      </p>

      <!-- 失败原因原样展示：后端那句里写着卡在哪一步；前面再补一个粗粒度阶段，两者分开读 -->
      <p
        v-if="model.error"
        class="mt-1.5 rounded-md bg-red-50 px-2 py-1.5 text-xs break-words text-red-700 dark:bg-red-950/30 dark:text-red-300"
      >
        <span v-if="model.stageLabel" class="font-medium">{{ model.stageLabel }}：</span>
        {{ model.error }}
      </p>

      <div
        v-if="model.meta.length"
        class="mt-2 flex flex-wrap items-center gap-x-2 gap-y-1 text-[13px] text-zinc-600 dark:text-zinc-400"
      >
        <template v-for="(part, index) in model.meta" :key="index">
          <span v-if="index > 0" class="text-zinc-300 dark:text-zinc-700">·</span>
          <span>{{ part }}</span>
        </template>
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
