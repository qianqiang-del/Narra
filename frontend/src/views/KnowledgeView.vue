<script setup lang="ts">
/**
 * 知识库页面 —— 上传文档、查看正文、删除文档。
 *
 * 后端收录是**异步**的：上传请求只落盘建行就返回（文档是 pending），解析与向量化
 * 在后台推进，状态经 pending → processing → ready / failed 变化。所以这里的核心
 * 不是"进度条"，而是把"还在处理"这个状态说清楚，并把失败原因原样交给用户 ——
 * 后端返回的 error 里已经写明卡在哪一步（例如解析环境没准备好）。
 *
 * 删除会级联清掉切片与向量（数据库层 ON DELETE CASCADE），执行前有确认；仍在
 * pending / processing 的文档，其上传暂存目录也会一并清理。
 * 启停（enabled）仍然没有接口 —— 后端只读不可写，所以界面上不做开关。
 */
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import {
  AlertCircle,
  ArrowLeft,
  CheckCircle2,
  Database,
  Eye,
  FileText,
  Inbox,
  Loader2,
  RefreshCw,
  Trash2,
  Upload,
  X,
} from 'lucide-vue-next'
import { storeToRefs } from 'pinia'
import { toast } from 'vue-sonner'

import {
  MAX_UPLOAD_BYTES,
  SUPPORTED_EXTENSIONS,
  type KnowledgeDocumentStatus,
} from '@/api/knowledge'
import { cn } from '@/lib/utils'
import { useKnowledgeStore } from '@/stores/knowledge'

const { t } = useI18n()
const router = useRouter()
const store = useKnowledgeStore()
const { documents, total, page, totalPage, loading, uploading, isEmpty } = storeToRefs(store)

const selectedFile = ref<File | null>(null)
const titleInput = ref('')
const dragging = ref(false)
const preview = ref<Awaited<ReturnType<typeof store.preview>> | null>(null)
const previewLoading = ref(false)
const deletingId = ref<number | null>(null)

const acceptAttr = SUPPORTED_EXTENSIONS.join(',')
const supportedHint = SUPPORTED_EXTENSIONS.join(' / ')

const canUpload = computed(() => selectedFile.value !== null && !uploading.value)

/** 选中的文件大小，MB，保留一位小数 */
const selectedSize = computed(() =>
  selectedFile.value ? `${(selectedFile.value.size / 1024 / 1024).toFixed(1)} MB` : '',
)

onMounted(() => {
  void refresh()
})

async function refresh() {
  try {
    await store.load()
  } catch (error) {
    toast.error(error instanceof Error ? error.message : t('knowledge.error.load'))
  }
}

/**
 * 在前端先挡两道：扩展名与大小。
 *
 * 不是为了替代后端校验（后端两道都有），而是为了省掉一次注定失败的往返 ——
 * 上传 PDF 时如果不挡，用户会等完整个解析失败才看到错误。
 */
function pickFile(file: File | undefined) {
  if (!file) return
  const name = file.name.toLowerCase()
  if (!SUPPORTED_EXTENSIONS.some((ext) => name.endsWith(ext))) {
    toast.error(t('knowledge.upload.unsupported', { formats: supportedHint }))
    return
  }
  if (file.size > MAX_UPLOAD_BYTES) {
    toast.error(t('knowledge.upload.tooLarge', { limit: MAX_UPLOAD_BYTES >> 20 }))
    return
  }
  selectedFile.value = file
}

function onFileChange(event: Event) {
  const input = event.target as HTMLInputElement
  pickFile(input.files?.[0])
  // 清空 input：否则连续选同一个文件时 change 不触发，第二次点没反应
  input.value = ''
}

function onDrop(event: DragEvent) {
  dragging.value = false
  pickFile(event.dataTransfer?.files?.[0])
}

function clearSelection() {
  selectedFile.value = null
  titleInput.value = ''
}

async function submitUpload() {
  const file = selectedFile.value
  if (!file || uploading.value) return

  try {
    const document = await store.upload(file, titleInput.value)
    toast.success(
      t('knowledge.upload.success', { title: document.title, chunks: document.chunks }),
    )
    clearSelection()
  } catch (error) {
    toast.error(error instanceof Error ? error.message : t('knowledge.error.upload'))
    // 收录失败也会在库里留一行 status = failed，刷新一下让用户看见它和原因
    void refresh()
  }
}

async function openPreview(id: number) {
  previewLoading.value = true
  try { preview.value = await store.preview(id) } catch (error) {
    toast.error(error instanceof Error ? error.message : t('knowledge.error.preview'))
  } finally { previewLoading.value = false }
}

async function removeDocument(id: number, title: string) {
  if (!window.confirm(t('knowledge.remove.confirm', { title }))) return
  deletingId.value = id
  try {
    await store.remove(id)
    toast.success(t('knowledge.remove.success'))
  } catch (error) {
    toast.error(error instanceof Error ? error.message : t('knowledge.error.remove'))
  } finally { deletingId.value = null }
}

const statusStyles: Record<KnowledgeDocumentStatus, string> = {
  ready: 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-300',
  failed: 'border-red-200 bg-red-50 text-red-700 dark:border-red-800 dark:bg-red-950/40 dark:text-red-300',
  processing: 'border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-800 dark:bg-amber-950/40 dark:text-amber-300',
  pending: 'border-border bg-muted text-muted-foreground',
}

function statusClass(status: KnowledgeDocumentStatus) {
  return statusStyles[status] ?? statusStyles.pending
}

function statusLabel(status: KnowledgeDocumentStatus) {
  return t(`knowledge.status.${status}`)
}

function sourceLabel(sourceType: string) {
  return t(`knowledge.source.${sourceType === 'manual' || sourceType === 'api' ? sourceType : 'import'}`)
}

function formatDate(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

function formatNumber(value: number) {
  return value.toLocaleString()
}

function goBack() {
  router.push({ name: 'home' })
}

function changePage(target: number) {
  void store.goToPage(target).catch((error: unknown) => {
    toast.error(error instanceof Error ? error.message : t('knowledge.error.load'))
  })
}
</script>

<template>
  <div
    class="relative flex min-h-[100dvh] w-full flex-col bg-gradient-to-b from-slate-50 to-slate-100 dark:from-slate-950 dark:to-slate-900"
  >
    <!-- 顶部：返回 + 标题 + 刷新 -->
    <header
      class="sticky top-0 z-30 flex items-center gap-3 border-b border-border/60 bg-white/80 px-4 py-3 backdrop-blur-xl md:px-8 dark:bg-slate-900/80"
    >
      <button
        type="button"
        class="flex size-8 shrink-0 cursor-pointer items-center justify-center rounded-lg text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
        :title="t('knowledge.back')"
        @click="goBack"
      >
        <ArrowLeft class="size-4" />
      </button>

      <div class="flex min-w-0 items-center gap-2">
        <Database class="size-4 shrink-0 text-violet-500" />
        <h1 class="truncate text-[15px] font-medium">{{ t('knowledge.title') }}</h1>
      </div>

      <button
        type="button"
        class="ml-auto flex shrink-0 cursor-pointer items-center gap-1.5 rounded-lg border border-border px-3 py-1.5 text-xs font-medium transition-colors hover:bg-muted disabled:cursor-not-allowed disabled:opacity-50"
        :disabled="loading"
        @click="refresh"
      >
        <RefreshCw :class="cn('size-3.5', loading && 'animate-spin')" />
        {{ t('knowledge.refresh') }}
      </button>
    </header>

    <main class="mx-auto flex w-full max-w-[900px] flex-1 flex-col gap-6 p-4 md:p-8">
      <!-- 上传区 -->
      <section class="rounded-2xl border border-border/60 bg-white/80 p-5 shadow-sm backdrop-blur-xl dark:bg-slate-900/80">
        <h2 class="mb-1 text-sm font-medium">{{ t('knowledge.upload.title') }}</h2>
        <p class="mb-4 text-xs text-muted-foreground">{{ t('knowledge.upload.hint') }}</p>

        <!-- 拖拽区。用 label 包住隐藏 input，点哪里都能唤起选择框 -->
        <label
          :class="
            cn(
              'flex cursor-pointer flex-col items-center justify-center gap-2 rounded-xl border border-dashed px-4 py-8 text-center transition-colors',
              dragging
                ? 'border-violet-400 bg-violet-50/60 dark:bg-violet-950/20'
                : 'border-border hover:border-violet-300 hover:bg-muted/40',
              uploading && 'pointer-events-none opacity-60',
            )
          "
          @dragover.prevent="dragging = true"
          @dragleave.prevent="dragging = false"
          @drop.prevent="onDrop"
        >
          <input
            type="file"
            class="hidden"
            :accept="acceptAttr"
            :disabled="uploading"
            @change="onFileChange"
          />
          <Upload class="size-5 text-muted-foreground/60" />
          <span class="text-xs text-muted-foreground">
            {{ t('knowledge.upload.drop', { formats: supportedHint }) }}
          </span>
        </label>

        <!-- 已选文件 -->
        <div
          v-if="selectedFile"
          class="mt-3 flex items-center gap-2 rounded-lg border border-border bg-muted/40 px-3 py-2"
        >
          <FileText class="size-4 shrink-0 text-muted-foreground" />
          <span class="min-w-0 flex-1 truncate text-xs">{{ selectedFile.name }}</span>
          <span class="shrink-0 text-xs text-muted-foreground">{{ selectedSize }}</span>
          <button
            type="button"
            class="shrink-0 cursor-pointer rounded p-0.5 text-muted-foreground transition-colors hover:text-foreground disabled:cursor-not-allowed disabled:opacity-40"
            :disabled="uploading"
            :title="t('knowledge.upload.clear')"
            @click="clearSelection"
          >
            <X class="size-3.5" />
          </button>
        </div>

        <!-- 标题（可选）与提交 -->
        <div class="mt-3 flex flex-col gap-2 sm:flex-row">
          <input
            v-model="titleInput"
            type="text"
            :disabled="uploading"
            :placeholder="t('knowledge.upload.titlePlaceholder')"
            class="min-w-0 flex-1 rounded-lg border border-border bg-transparent px-3 py-2 text-xs placeholder:text-muted-foreground/50 focus:border-violet-400 focus:outline-none disabled:opacity-60"
          />
          <button
            type="button"
            :class="
              cn(
                'flex shrink-0 items-center justify-center gap-1.5 rounded-lg px-4 py-2 text-xs font-medium transition-all',
                canUpload
                  ? 'cursor-pointer bg-primary text-primary-foreground hover:opacity-90'
                  : 'cursor-not-allowed bg-muted text-muted-foreground/40',
              )
            "
            :disabled="!canUpload"
            @click="submitUpload"
          >
            <Loader2 v-if="uploading" class="size-3.5 animate-spin" />
            <Upload v-else class="size-3.5" />
            {{ uploading ? t('knowledge.upload.submitting') : t('knowledge.upload.submit') }}
          </button>
        </div>

        <p v-if="uploading" class="mt-2 text-xs text-muted-foreground">
          {{ t('knowledge.upload.submittingHint') }}
        </p>
      </section>

      <!-- 列表 -->
      <section class="rounded-2xl border border-border/60 bg-white/80 shadow-sm backdrop-blur-xl dark:bg-slate-900/80">
        <div class="flex items-center justify-between border-b border-border/60 px-5 py-3">
          <h2 class="text-sm font-medium">{{ t('knowledge.list.title') }}</h2>
          <span class="text-xs text-muted-foreground">
            {{ t('knowledge.list.total', { total: formatNumber(total) }) }}
          </span>
        </div>

        <!-- 加载中（首次进入，还没有数据可显示） -->
        <div v-if="loading && documents.length === 0" class="flex items-center justify-center gap-2 py-12 text-xs text-muted-foreground">
          <Loader2 class="size-4 animate-spin" />
          {{ t('common.loading') }}
        </div>

        <!-- 空状态 -->
        <div v-else-if="isEmpty" class="flex flex-col items-center gap-2 py-12 text-center">
          <Inbox class="size-6 text-muted-foreground/40" />
          <p class="text-xs text-muted-foreground">{{ t('knowledge.list.empty') }}</p>
        </div>

        <ul v-else class="divide-y divide-border/60">
          <li v-for="doc in documents" :key="doc.id" class="px-5 py-3.5">
            <div class="flex items-start gap-3">
              <AlertCircle v-if="doc.status === 'failed'" class="mt-0.5 size-4 shrink-0 text-red-500" />
              <CheckCircle2 v-else-if="doc.status === 'ready'" class="mt-0.5 size-4 shrink-0 text-emerald-500" />
              <Loader2 v-else class="mt-0.5 size-4 shrink-0 animate-spin text-amber-500" />

              <div class="min-w-0 flex-1">
                <div class="flex flex-wrap items-center gap-2">
                  <span class="min-w-0 truncate text-[13px] font-medium">{{ doc.title }}</span>
                  <span
                    :class="
                      cn(
                        'shrink-0 rounded-full border px-2 py-0.5 text-[11px] leading-none',
                        statusClass(doc.status),
                      )
                    "
                  >
                    {{ statusLabel(doc.status) }}
                  </span>
                  <span class="shrink-0 rounded-full border border-border px-2 py-0.5 text-[11px] leading-none text-muted-foreground">
                    {{ sourceLabel(doc.sourceType) }}
                  </span>
                  <span v-if="doc.parser" class="shrink-0 text-[11px] text-muted-foreground/70">
                    {{ doc.parser }}
                  </span>
                </div>

                <p v-if="doc.sourceUri" class="mt-1 truncate text-xs text-muted-foreground/70">
                  {{ doc.sourceUri }}
                </p>

                <!-- 失败原因原样展示：后端那句里写着卡在哪一步和怎么解决 -->
                <p v-if="doc.error" class="mt-1.5 rounded-md bg-red-50 px-2 py-1.5 text-xs break-words text-red-700 dark:bg-red-950/30 dark:text-red-300">
                  {{ doc.error }}
                </p>

                <div class="mt-1.5 flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
                  <span>{{ t('knowledge.col.chunks') }} {{ formatNumber(doc.chunks) }}</span>
                  <span>{{ t('knowledge.col.characters') }} {{ formatNumber(doc.characters) }}</span>
                  <span>{{ t('knowledge.col.updatedAt') }} {{ formatDate(doc.updatedAt) }}</span>
                </div>

                <div class="mt-2 flex gap-2">
                  <button type="button" class="inline-flex items-center gap-1 rounded-md border border-border px-2 py-1 text-[11px] hover:bg-muted" @click="openPreview(doc.id)">
                    <Eye class="size-3" /> {{ t('knowledge.action.view') }}
                  </button>
                  <button type="button" class="inline-flex items-center gap-1 rounded-md border border-red-200 px-2 py-1 text-[11px] text-red-600 hover:bg-red-50 disabled:opacity-50" :disabled="deletingId === doc.id" @click="removeDocument(doc.id, doc.title)">
                    <Trash2 class="size-3" /> {{ t('knowledge.action.delete') }}
                  </button>
                </div>
              </div>
            </div>
          </li>
        </ul>

        <!-- 分页 -->
        <div
          v-if="totalPage > 1"
          class="flex items-center justify-between border-t border-border/60 px-5 py-3"
        >
          <button
            type="button"
            class="cursor-pointer rounded-lg border border-border px-3 py-1.5 text-xs transition-colors hover:bg-muted disabled:cursor-not-allowed disabled:opacity-40"
            :disabled="page <= 1 || loading"
            @click="changePage(page - 1)"
          >
            {{ t('knowledge.page.prev') }}
          </button>
          <span class="text-xs text-muted-foreground">
            {{ t('knowledge.page.indicator', { page, totalPage }) }}
          </span>
          <button
            type="button"
            class="cursor-pointer rounded-lg border border-border px-3 py-1.5 text-xs transition-colors hover:bg-muted disabled:cursor-not-allowed disabled:opacity-40"
            :disabled="page >= totalPage || loading"
            @click="changePage(page + 1)"
          >
            {{ t('knowledge.page.next') }}
          </button>
        </div>
      </section>
    </main>

    <div v-if="preview || previewLoading" class="fixed inset-0 z-50 flex items-center justify-center bg-black/30 p-4" @click.self="preview = null">
      <section class="flex max-h-[85vh] w-full max-w-3xl flex-col rounded-2xl bg-background shadow-xl">
        <header class="flex items-center gap-3 border-b border-border px-5 py-4">
          <FileText class="size-4 text-violet-500" />
          <h2 class="min-w-0 flex-1 truncate text-sm font-medium">{{ preview?.title ?? t('knowledge.preview.loading') }}</h2>
          <button type="button" class="rounded p-1 text-muted-foreground hover:bg-muted" :title="t('knowledge.preview.close')" @click="preview = null"><X class="size-4" /></button>
        </header>
        <div v-if="previewLoading" class="flex flex-1 items-center justify-center p-10"><Loader2 class="size-5 animate-spin" /></div>
        <pre v-else class="overflow-auto whitespace-pre-wrap p-5 text-xs leading-6">{{ preview?.content || t('knowledge.preview.empty') }}</pre>
      </section>
    </div>
  </div>
</template>
