<script setup lang="ts">
/**
 * 知识库页面 —— 信息结构对齐 `docs/prototypes/knowledge-redesign.html`。
 *
 * 三块：
 * - **主页列表**只列已收录（ready）的文档，滚到底自动续接下一批（每批 10 条）；
 * - **新增知识库**收进弹层：一次只能传一份，上一份处理完才放开；
 * - **上传记录**收进抽屉：只装没收录成功的（等待中 / 处理中 / 失败），只提供删除。
 *
 * 收录取自后端异步链路：上传请求只落盘建行，解析与向量化在后台推进。所以这里的
 * 重点不是进度条，而是把"还在处理""处理失败了、为什么"讲清楚 —— 失败原因
 * 由后端原样带回，直接展示，不加工。
 *
 * ⚠️ 一处依赖后端批次，见 `stores/knowledge.ts` 文件头的 TODO：
 * 上传记录目前从文档列表里派生（批 ② 独立成表）。搜索与分页已经在服务端做（批 ①）。
 */
import { ArrowLeft, Bell, Database, Loader2, Plus, RefreshCw, Search, X } from 'lucide-vue-next'
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { storeToRefs } from 'pinia'
import { toast } from 'vue-sonner'

import type { KnowledgeDocument } from '@/api/knowledge'
import ConfirmDialog from '@/components/knowledge/ConfirmDialog.vue'
import DocumentPreviewDialog from '@/components/knowledge/DocumentPreviewDialog.vue'
import KnowledgeRow from '@/components/knowledge/KnowledgeRow.vue'
import NewKnowledgeDialog from '@/components/knowledge/NewKnowledgeDialog.vue'
import UploadRecordDrawer from '@/components/knowledge/UploadRecordDrawer.vue'
import { useKnowledgeStore } from '@/stores/knowledge'

const { t } = useI18n()
const router = useRouter()
const store = useKnowledgeStore()

const { loading, keyword, readyDocuments, readyTotal, uploadRecords, hasMore, isEmpty } =
  storeToRefs(store)

const newOpen = ref(false)
const recordsOpen = ref(false)
const previewOpen = ref(false)
const previewId = ref<number | null>(null)
const confirmOpen = ref(false)
const deleting = ref(false)
/** 待确认删除的目标：主页删的是知识库，抽屉删的是上传记录，文案与后果都不一样 */
const pendingDelete = ref<{ kind: 'doc' | 'record'; document: KnowledgeDocument } | null>(null)

const sentinel = ref<HTMLElement | null>(null)
let observer: IntersectionObserver | null = null

const searching = computed(() => keyword.value.trim().length > 0)

const confirmTitle = computed(() =>
  pendingDelete.value?.kind === 'record'
    ? t('knowledge.records.remove.title')
    : t('knowledge.remove.title'),
)
const confirmMessage = computed(() =>
  pendingDelete.value
    ? t(
        pendingDelete.value.kind === 'record'
          ? 'knowledge.records.remove.message'
          : 'knowledge.remove.confirm',
        { title: pendingDelete.value.document.title },
      )
    : '',
)
const confirmNote = computed(() =>
  pendingDelete.value?.kind === 'record'
    ? t('knowledge.records.remove.note')
    : t('knowledge.remove.note'),
)

onMounted(async () => {
  await refresh()
  startObserving()
})

onUnmounted(() => {
  observer?.disconnect()
  observer = null
})

async function refresh() {
  try {
    await store.load()
  } catch (error) {
    toast.error(error instanceof Error ? error.message : t('knowledge.error.load'))
  }
}

/**
 * 滚到底续接：**用哨兵 + IntersectionObserver，不用 window 的 scroll 事件**。
 *
 * scroll 事件在"首批内容不足一屏"时会死锁 —— 没有滚动条就没有 scroll 事件，
 * 于是永远触发不了续接，列表永远停在第一批。观察哨兵则不受此影响：
 * 它在视口内就说明已经到底。
 *
 * 列表每次变化都要重新观察：IntersectionObserver 只在**可见性发生变化**时回调，
 * 而 `observe()` 会立即投递一次当前状态，于是"一屏没填满"时能连续续接，
 * 内容一旦超出视口就自然停下（`loadMore` 在没有更多时直接返回）。
 */
function startObserving() {
  if (typeof IntersectionObserver === 'undefined') return
  observer = new IntersectionObserver(
    (entries) => {
      if (entries.some((entry) => entry.isIntersecting)) store.loadMore()
    },
    { rootMargin: '120px' },
  )
  void watchSentinel()
}

async function watchSentinel() {
  await nextTick()
  const element = sentinel.value
  if (!element || !observer) return
  observer.unobserve(element)
  observer.observe(element)
}

/**
 * 监听整个列表而不只是它的长度。
 *
 * 只看长度的漏洞：列表内容换了但条数没变时（例如换了个关键字、命中数恰好相同、
 * 或者上传完成把一份 pending 换成 ready），哨兵不会重新评估，
 * 而它此刻可能已经在视口里了。
 */
watch(readyDocuments, () => {
  void watchSentinel()
})

function clearSearch() {
  keyword.value = ''
}

function openPreview(document: KnowledgeDocument) {
  previewId.value = document.id
  previewOpen.value = true
}

function askDeleteDocument(document: KnowledgeDocument) {
  pendingDelete.value = { kind: 'doc', document }
  confirmOpen.value = true
}

function askDeleteRecord(document: KnowledgeDocument) {
  pendingDelete.value = { kind: 'record', document }
  confirmOpen.value = true
}

async function confirmDelete() {
  const target = pendingDelete.value
  if (!target) return
  deleting.value = true
  try {
    await store.remove(target.document.id)
    toast.success(
      target.kind === 'record' ? t('knowledge.records.removed') : t('knowledge.remove.success'),
    )
    confirmOpen.value = false
    pendingDelete.value = null
  } catch (error) {
    toast.error(error instanceof Error ? error.message : t('knowledge.error.remove'))
  } finally {
    deleting.value = false
  }
}

function goBack() {
  router.push({ name: 'home' })
}
</script>

<template>
  <div
    class="relative flex min-h-[100dvh] w-full flex-col bg-gradient-to-b from-slate-50 to-slate-100 dark:from-slate-950 dark:to-slate-900"
  >
    <header
      class="sticky top-0 z-30 flex flex-wrap items-center gap-3 border-b border-border/60 bg-white/80 px-4 py-3 backdrop-blur-xl md:px-8 dark:bg-slate-900/80"
    >
      <button
        type="button"
        class="flex size-8 shrink-0 cursor-pointer items-center justify-center rounded-lg text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
        :title="t('knowledge.back')"
        @click="goBack"
      >
        <ArrowLeft class="size-4" />
      </button>

      <div class="flex shrink-0 items-center gap-2">
        <Database class="size-4 text-violet-500" />
        <h1 class="text-[15px] font-medium">{{ t('knowledge.title') }}</h1>
      </div>

      <!-- 搜索：与原型一致，只搜标题与原始文件名 -->
      <div class="relative ml-auto w-full min-w-0 sm:w-72">
        <Search
          class="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-zinc-500"
        />
        <input
          v-model="keyword"
          type="search"
          :placeholder="t('knowledge.search.placeholder')"
          class="w-full rounded-lg border border-input bg-background py-1.5 pr-8 pl-8.5 text-[13px] outline-none transition-colors placeholder:text-zinc-500 focus:border-violet-400"
        />
        <button
          v-if="searching"
          type="button"
          class="absolute top-1/2 right-2 -translate-y-1/2 cursor-pointer rounded p-0.5 text-zinc-500 transition-colors hover:text-foreground"
          :title="t('knowledge.search.clear')"
          @click="clearSearch"
        >
          <X class="size-4" />
        </button>
      </div>

      <button
        type="button"
        class="flex shrink-0 cursor-pointer items-center gap-1.5 rounded-lg border border-border px-3 py-1.5 text-[13px] font-medium transition-colors hover:bg-muted"
        @click="recordsOpen = true"
      >
        <Bell class="size-4" />
        {{ t('knowledge.toolbar.records') }}
        <span
          v-if="uploadRecords.length"
          class="rounded-full bg-amber-100 px-1.5 py-0.5 text-[11px] leading-none font-medium text-amber-700 dark:bg-amber-950/50 dark:text-amber-300"
        >
          {{ uploadRecords.length }}
        </span>
      </button>

      <button
        type="button"
        class="flex shrink-0 cursor-pointer items-center gap-1.5 rounded-lg bg-primary px-3 py-1.5 text-[13px] font-medium text-primary-foreground transition-opacity hover:opacity-90"
        @click="newOpen = true"
      >
        <Plus class="size-4" />
        {{ t('knowledge.toolbar.new') }}
      </button>

      <button
        type="button"
        class="flex size-8 shrink-0 cursor-pointer items-center justify-center rounded-lg border border-border text-muted-foreground transition-colors hover:bg-muted disabled:cursor-not-allowed disabled:opacity-50"
        :disabled="loading"
        :title="t('knowledge.refresh')"
        @click="refresh"
      >
        <RefreshCw class="size-3.5" :class="loading && 'animate-spin'" />
      </button>
    </header>

    <main class="mx-auto flex w-full max-w-[900px] flex-1 flex-col px-4 py-5 md:px-8">
      <!-- 首次加载 -->
      <div
        v-if="loading && readyDocuments.length === 0"
        class="flex items-center justify-center gap-2 py-16 text-[13px] text-zinc-600 dark:text-zinc-400"
      >
        <Loader2 class="size-4 animate-spin" />
        {{ t('common.loading') }}
      </div>

      <!--
        空态：分成"一份都没有"和"被搜索过滤光了"两种，后者要让用户知道是搜索的问题。
        筛选在服务端，所以这里只能按"有没有在搜"分 —— 结果为空时无从判断
        是库里本来就没有 ready，还是关键字把它们都滤掉了。
      -->
      <div v-else-if="isEmpty" class="flex flex-col items-center gap-2 py-20 text-center">
        <Database class="size-6 text-zinc-400" />
        <p class="text-[13px] text-zinc-600 dark:text-zinc-400">
          {{
            searching
              ? t('knowledge.list.filteredEmpty', { keyword: keyword.trim() })
              : t('knowledge.list.empty')
          }}
        </p>
      </div>

      <template v-else>
        <ul class="divide-y divide-border/60">
          <KnowledgeRow
            v-for="document in readyDocuments"
            :key="document.id"
            :document="document"
          >
            <template #actions>
              <button
                type="button"
                class="inline-flex cursor-pointer items-center gap-1 rounded-md border border-border px-2.5 py-1 text-xs text-zinc-700 transition-colors hover:bg-muted dark:text-zinc-300"
                @click="openPreview(document)"
              >
                {{ t('knowledge.action.view') }}
              </button>
              <button
                type="button"
                class="inline-flex cursor-pointer items-center gap-1 rounded-md border border-red-200 px-2.5 py-1 text-xs text-red-600 transition-colors hover:bg-red-50 dark:border-red-900 dark:text-red-400 dark:hover:bg-red-950/40"
                @click="askDeleteDocument(document)"
              >
                {{ t('knowledge.action.delete') }}
              </button>
            </template>
          </KnowledgeRow>
        </ul>

        <!-- 已显示 X / Y · 下拉刷新（滚到底自动续接，这里只是把进度说清楚） -->
        <p class="mt-4 text-center text-xs text-zinc-600 dark:text-zinc-400">
          {{ t('knowledge.list.shown', { shown: readyDocuments.length, total: readyTotal }) }}
          <template v-if="hasMore">
            <span class="mx-1.5 text-zinc-300 dark:text-zinc-700">·</span>{{ t('knowledge.list.pullRefresh') }}
          </template>
        </p>
      </template>

      <!-- 续接触发点：它进入视口就说明已经到底 -->
      <div ref="sentinel" aria-hidden="true" class="h-px w-full" />
    </main>

    <NewKnowledgeDialog v-model:open="newOpen" />
    <UploadRecordDrawer v-model:open="recordsOpen" @remove="askDeleteRecord" />
    <DocumentPreviewDialog v-model:open="previewOpen" :document-id="previewId" />

    <ConfirmDialog
      v-model:open="confirmOpen"
      :title="confirmTitle"
      :message="confirmMessage"
      :note="confirmNote"
      :pending="deleting"
      @confirm="confirmDelete"
    />
  </div>
</template>
