<script setup lang="ts">
/**
 * 上传记录抽屉 —— 一次文件投递的流水，**包含已经收录成功的那些**。
 *
 * 记录与知识是两种东西，这是批 ② 之后最要紧的一条：主页列的是"现在库里有哪些
 * 知识"，抽屉列的是"投递过什么"。所以一条记录在文档被删之后仍然留着，那一行的
 * 状态读作「已收录后删除」—— 它说明这份知识曾经成功入过库、后来被删了，
 * 而不是"那次上传没成功"。
 *
 * 删除只清这一条流水：若它对应的文档还没收录成功，后端会连带把那份文档与暂存文件
 * 一起清掉（不收掉的话上传闸门会一直卡着）；已经收录成功的文档绝不触碰。
 *
 * 失败的那一行多一个「重试」：让同一篇文档再跑一遍（后端留着归档的原件），
 * 不新建记录、也不用重新选文件。文档已经被删掉的（documentId 为空）没有可重跑的对象，
 * 那个按钮不出现。
 */
import { X, Trash2, RotateCw } from 'lucide-vue-next'
import { computed, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import KnowledgeRow from './KnowledgeRow.vue'
import type { KnowledgeUploadRecord } from '@/api/knowledge'
import { useKnowledgeStore } from '@/stores/knowledge'

const open = defineModel<boolean>('open', { default: false })

const emit = defineEmits<{
  remove: [record: KnowledgeUploadRecord]
  /** 请求重试这一条投递（真正的调用与提示在页面层，与删除同一个路数） */
  retry: [record: KnowledgeUploadRecord]
}>()

const { t } = useI18n()
const store = useKnowledgeStore()

const records = computed(() => store.uploadRecords)

/**
 * 抽屉打开时锁住页面滚动、顺便对一次账。
 *
 * 锁滚动：给 body 设 `overflow: hidden` 就够 —— html 的 overflow 保持默认的
 * visible，此时 body 的 overflow 会**传播到视口**（CSS Overflow §3），视口因此真的
 * 滚不动，页面那条滚动条也不再和抽屉内部这条并排。⚠️ 前提是 globals.css 里 html
 * 别写 `overflow-y: scroll`（那会让传播失效，这句锁就成了空操作）；槽位常驻交给
 * `scrollbar-gutter: stable`，所以锁与解锁都不会横向位移。
 *
 * 对账：记录是后台在改的（上传还在处理、别处可能刚删过东西），而抽屉多半是关着
 * 的时候数据变旧。拉失败就用手上这份，不要因为一次刷新失败把抽屉清空。
 */
watch(open, async (isOpen) => {
  document.body.style.overflow = isOpen ? 'hidden' : ''
  if (!isOpen) return
  try {
    await store.loadRecords()
  } catch {
    /* 保留已有列表 */
  }
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
          {{ t('knowledge.records.count', { total: store.recordTotal }) }}
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
            v-for="record in records"
            :key="record.id"
            :record="record"
          >
            <template #actions>
              <!--
                只有失败且还关联着文档的那些能重试：重试的对象是文档，
                文档已经被删掉之后这条记录只是一段历史（没有可重跑的输入）。
              -->
              <button
                v-if="record.status === 'failed' && record.documentId"
                type="button"
                class="inline-flex cursor-pointer items-center gap-1 rounded-md border border-border px-2.5 py-1 text-xs transition-colors hover:bg-muted"
                @click="emit('retry', record)"
              >
                <RotateCw class="size-3.5" />
                {{ t('knowledge.action.retry') }}
              </button>
              <button
                type="button"
                class="inline-flex cursor-pointer items-center gap-1 rounded-md border border-red-200 px-2.5 py-1 text-xs text-red-600 transition-colors hover:bg-red-50 dark:border-red-900 dark:text-red-400 dark:hover:bg-red-950/40"
                @click="emit('remove', record)"
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
