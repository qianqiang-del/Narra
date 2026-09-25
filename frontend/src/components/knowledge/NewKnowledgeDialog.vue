<script setup lang="ts">
/**
 * 「新增知识库」弹层：文件导入 / 直接录入两个形态。
 *
 * 文件导入：选文件 → 填标题 → 提交，下面挂一张"最近一次上传"卡片。
 * 一次只能传一份：`store.upload` 会一直盯到终态，`store.uploading` 也就一直为真，
 * 期间整个上传区禁用。服务端的强制拒绝属于批 ①，前端这层到时候仍然保留
 * （少一次注定失败的往返）。
 *
 * 卡片是"被拒绝时用户能看懂发生了什么"的配套 UI：它显示当前在处理哪一份、
 * 上一份是因为什么失败的；失败的那一份可以直接重试 —— 后端拿归档在服务器上的
 * 原件重跑，用户不用重新选文件。
 *
 * 直接录入：粘贴或输入 Markdown 正文，走同步链路（见 store.ingestText），
 * 没有解析与进度流，也没有上传记录 —— 所以这个形态下不显示上面那张卡片。
 */
import {
  DialogContent,
  DialogDescription,
  DialogOverlay,
  DialogPortal,
  DialogRoot,
  DialogTitle,
} from 'reka-ui'
import { AlertCircle, FileText, Info, Loader2, PenLine, RotateCw, Upload, X } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'

import { MAX_UPLOAD_BYTES, SUPPORTED_EXTENSIONS, failureStageKey, needsDocumentParser } from '@/api/knowledge'
import { useKnowledgeStore } from '@/stores/knowledge'

const open = defineModel<boolean>('open', { default: false })

const { t } = useI18n()
const store = useKnowledgeStore()

/** 当前形态：文件导入 / 直接录入。两个页签共用下面的标题输入框 */
const mode = ref<'file' | 'text'>('file')

const selectedFile = ref<File | null>(null)
const titleInput = ref('')
const dragging = ref(false)
const textContent = ref('')

const acceptAttr = SUPPORTED_EXTENSIONS.join(',')
const supportedHint = SUPPORTED_EXTENSIONS.join(' / ')

const busy = computed(() => store.uploading || store.submitting)

/** 处理中的那一份：只有 uploading 期间才有值（store 在 finally 里清掉） */
const processing = computed(() => (store.uploading ? store.activeUpload : null))

const failed = computed(() => (store.uploading ? null : store.lastFailedRecord))

/**
 * 上一份失败的可不可以重试。
 *
 * 重试要落到一篇文档上，而记录未必还关联着文档 —— 那次投递的成果可能已经被删了
 * （记录仍在，状态显示为「已收录后删除」，但失败的那些没有这个说法：文档一删，
 * 记录就只剩 documentId 为空）。那种情况没有可重跑的对象，按钮置灰。
 */
const canRetry = computed(() => Boolean(failed.value?.documentId))

/**
 * 失败行的粗粒度阶段（"解析阶段失败"等）。
 *
 * 与具体错误分开显示：前缀回答"卡在哪一大步"，error 回答"具体为什么"。
 * 细粒度位置（如 store：向量算好了但没写进去）优先，位置未知时回落到收录阶段。
 */
const failedStageLabel = computed(() => {
  const record = failed.value
  if (!record) return ''
  const key = failureStageKey(record.failedStage, record.stage)
  return key ? t(`knowledge.stage.${key}`) : ''
})

const selectedSize = computed(() =>
  selectedFile.value ? `${(selectedFile.value.size / 1024 / 1024).toFixed(1)} MB` : '',
)

/**
 * "首次上传需要先准备解析环境"的提醒是否出现。
 *
 * 三个条件缺一不可：选中的文件确实要用 Python 解析器；后端说解析能力开着；环境还没备好。
 * 环境备好之后 ready 为真，提示自然消失 —— 不需要前端自己记"这是不是第一次"。
 *
 * enabled=false（配置里就没开）时不提示：那不是"要等一会儿"，是这份文件根本解析不了，
 * 该给的是一句不同的说明，别混进"首次较慢"里。
 */
const parserHint = computed(() => {
  const status = store.parserStatus
  if (!selectedFile.value || !needsDocumentParser(selectedFile.value.name)) return false
  return status !== null && status.enabled && !status.ready
})

// 每次打开弹层都刷新一次环境状态：上次关掉之后环境可能已经装好了。
watch(open, (visible) => {
  if (visible) void store.loadParserStatus()
})

/**
 * 先在前端挡两道：扩展名与大小。
 *
 * 不是为了替代后端校验（后端两道都有），而是省掉一次注定失败的往返 ——
 * 不然用户要等整个解析失败才看到"格式不支持"。
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

async function submit() {
  const file = selectedFile.value
  if (!file || busy.value) return

  try {
    const document = await store.upload(file, titleInput.value)
    if (document.status === 'failed') {
      // 失败不抛异常：它同样是有效结果，库里留了一行，抽屉里能看到原因
      toast.error(t('knowledge.upload.failed'))
    } else {
      toast.success(
        t('knowledge.upload.success', { title: document.title, chunks: document.chunks }),
      )
    }
    clearSelection()
  } catch (error) {
    toast.error(error instanceof Error ? error.message : t('knowledge.error.upload'))
  }
}

/** 当前页签能不能提交：文件要有选中文件；正文要有非空内容与标题（正文录入标题必填） */
const canSubmit = computed(() => {
  if (mode.value === 'file') return Boolean(selectedFile.value)
  return textContent.value.trim().length > 0 && titleInput.value.trim().length > 0
})

/** 提交按钮的唯一入口：按当前页签分派 */
function submitCurrent() {
  return mode.value === 'file' ? submit() : submitText()
}

/**
 * 直接收录一段正文。
 *
 * 同步链路：成功返回时文档已经是 ready（所以 toast 里能直接报切片数），失败由后端
 * 返 400，这里把原因原样提示出来 —— 失败的那篇文档留在库里，但主页只查 ready、
 * 它又没有上传记录，界面上不会出现它；用户要重来就再提交一次（会新建一篇）。
 *
 * 标题必填（与文件导入不同）：文件有原始文件名可以回落，正文没有，标题就是它
 * 在列表里的唯一标识。
 */
async function submitText() {
  const content = textContent.value.trim()
  const title = titleInput.value.trim()
  if (!content || !title || busy.value) return

  try {
    const document = await store.ingestText(content, title)
    toast.success(
      t('knowledge.upload.success', { title: document.title, chunks: document.chunks }),
    )
    textContent.value = ''
    titleInput.value = ''
  } catch (error) {
    toast.error(error instanceof Error ? error.message : t('knowledge.error.ingestText'))
  }
}

/**
 * 重试上一份失败的文件。
 *
 * 重试的是那份**文档**，不是这条记录：后端把同一行改回 pending 重新跑一遍，
 * 成功后仍是同一条记录、同一篇文档，不会多出一条投递历史。
 *
 * 失败同样不当异常：它还是有效结果（库里那行又变回 failed），照上传的口径给提示。
 */
async function retryFailed() {
  const record = failed.value
  if (!record?.documentId || busy.value) return

  try {
    const document = await store.retry(record.documentId)
    if (document.status === 'failed') {
      toast.error(t('knowledge.upload.failed'))
    } else {
      toast.success(
        t('knowledge.upload.success', { title: document.title, chunks: document.chunks }),
      )
    }
  } catch (error) {
    toast.error(error instanceof Error ? error.message : t('knowledge.error.retry'))
  }
}
</script>

<template>
  <DialogRoot v-model:open="open">
    <DialogPortal>
      <DialogOverlay class="fixed inset-0 z-[100] bg-black/40 backdrop-blur-[2px]" />
      <DialogContent
        class="fixed top-1/2 left-1/2 z-[101] w-[min(560px,calc(100vw-2rem))] -translate-x-1/2 -translate-y-1/2 rounded-2xl border border-border bg-background p-5 shadow-2xl focus:outline-none"
      >
        <!-- 标题区带一个显式关闭按钮：只靠 Esc 和点遮罩关闭对用户不够友好 -->
        <div class="flex items-start gap-3">
          <div class="min-w-0 flex-1">
            <DialogTitle class="text-[15px] font-medium">{{ t('knowledge.new.title') }}</DialogTitle>
            <DialogDescription class="mt-1 text-[13px] text-zinc-600 dark:text-zinc-400">
              {{ t(mode === 'file' ? 'knowledge.new.desc' : 'knowledge.new.descText') }}
            </DialogDescription>
          </div>
          <button
            type="button"
            class="shrink-0 cursor-pointer rounded-lg p-1 text-zinc-500 transition-colors hover:bg-muted hover:text-foreground"
            :title="t('knowledge.preview.close')"
            @click="open = false"
          >
            <X class="size-4" />
          </button>
        </div>

        <!-- 形态切换。切换不打断进行中的任务：busy 时两个页签的输入都禁用 -->
        <div class="mt-4 grid grid-cols-2 gap-1 rounded-xl border border-border bg-muted/40 p-1">
          <button
            type="button"
            class="cursor-pointer rounded-lg px-3 py-1.5 text-[13px] font-medium transition-colors"
            :class="
              mode === 'file'
                ? 'bg-background text-foreground shadow-sm'
                : 'text-zinc-600 hover:text-foreground dark:text-zinc-400'
            "
            @click="mode = 'file'"
          >
            {{ t('knowledge.new.tabFile') }}
          </button>
          <button
            type="button"
            class="cursor-pointer rounded-lg px-3 py-1.5 text-[13px] font-medium transition-colors"
            :class="
              mode === 'text'
                ? 'bg-background text-foreground shadow-sm'
                : 'text-zinc-600 hover:text-foreground dark:text-zinc-400'
            "
            @click="mode = 'text'"
          >
            {{ t('knowledge.new.tabText') }}
          </button>
        </div>

        <label
          v-if="mode === 'file'"
          class="mt-4 flex flex-col items-center justify-center gap-2 rounded-xl border border-dashed px-4 py-7 text-center transition-colors"
          :class="
            busy
              ? 'pointer-events-none border-border opacity-60'
              : dragging
                ? 'cursor-pointer border-violet-400 bg-violet-50/60 dark:bg-violet-950/20'
                : 'cursor-pointer border-border hover:border-violet-300 hover:bg-muted/40'
          "
          @dragover.prevent="dragging = true"
          @dragleave.prevent="dragging = false"
          @drop.prevent="onDrop"
        >
          <input type="file" class="hidden" :accept="acceptAttr" :disabled="busy" @change="onFileChange" />
          <Upload class="size-5 text-zinc-400" />
          <span class="text-[13px] text-zinc-600 dark:text-zinc-400">
            {{
              busy && processing
                ? t('knowledge.new.dropBusy', { title: processing.title })
                : t('knowledge.upload.drop', { formats: supportedHint })
            }}
          </span>
        </label>

        <!-- 已选文件 -->
        <div
          v-if="mode === 'file' && selectedFile"
          class="mt-3 flex items-center gap-2 rounded-lg border border-border bg-muted/40 px-3 py-2"
        >
          <FileText class="size-4 shrink-0 text-zinc-500" />
          <span class="min-w-0 flex-1 truncate text-[13px]">{{ selectedFile.name }}</span>
          <span class="shrink-0 text-[13px] text-zinc-600 dark:text-zinc-400">{{ selectedSize }}</span>
          <button
            type="button"
            class="shrink-0 cursor-pointer rounded p-0.5 text-zinc-500 transition-colors hover:text-foreground disabled:cursor-not-allowed disabled:opacity-40"
            :disabled="busy"
            :title="t('knowledge.upload.clear')"
            @click="clearSelection"
          >
            <X class="size-3.5" />
          </button>
        </div>

        <!--
          首次上传这类文件要先把解析环境装出来（后端用 uv 现场下载解释器与依赖，
          分钟级）。不提醒的话，用户只会看到"处理中"长时间不动，以为卡死了。
        -->
        <div
          v-if="mode === 'file' && parserHint"
          class="mt-3 flex items-start gap-2 rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-xs leading-5 text-amber-800 dark:border-amber-800 dark:bg-amber-950/30 dark:text-amber-200"
        >
          <Info class="mt-0.5 size-3.5 shrink-0" />
          <span>
            <template v-if="store.parserStatus?.preparing">
              {{ t('knowledge.upload.parserPreparing', { progress: store.parserStatus.progress }) }}
            </template>
            <template v-else>{{ t('knowledge.upload.parserSetup') }}</template>
          </span>
        </div>

        <!-- 直接录入：正文按 Markdown 处理，没有解析这一步，提交后同步返回结果 -->
        <textarea
          v-if="mode === 'text'"
          v-model="textContent"
          rows="8"
          :disabled="busy"
          :placeholder="t('knowledge.text.placeholder')"
          class="mt-4 w-full resize-y rounded-xl border border-input bg-background px-3 py-2 text-[13px] leading-6 outline-none transition-colors placeholder:text-zinc-500 focus:border-violet-400 disabled:opacity-60"
        />

        <!-- 标题与提交：正文录入必填，文件导入可选（留空回落到正文首个标题） -->
        <div class="mt-3 flex items-center gap-2">
          <input
            v-model="titleInput"
            type="text"
            :disabled="busy"
            :placeholder="
              t(mode === 'text' ? 'knowledge.text.titlePlaceholder' : 'knowledge.upload.titlePlaceholder')
            "
            class="min-w-0 flex-1 rounded-lg border border-input bg-background px-3 py-2 text-[13px] outline-none transition-colors placeholder:text-zinc-500 focus:border-violet-400 disabled:opacity-60"
          />
          <button
            type="button"
            :disabled="!canSubmit || busy"
            class="flex shrink-0 cursor-pointer items-center gap-1.5 rounded-lg bg-primary px-4 py-2 text-[13px] font-medium text-primary-foreground transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-40"
            @click="submitCurrent"
          >
            <Loader2 v-if="busy" class="size-3.5 animate-spin" />
            <PenLine v-else-if="mode === 'text'" class="size-3.5" />
            <Upload v-else class="size-3.5" />
            {{ busy ? t('knowledge.upload.submitting') : t('knowledge.upload.submit') }}
          </button>
        </div>

        <!-- 正文录入了内容却还没填标题时的就地提醒：光靠按钮置灰说不出为什么 -->
        <p
          v-if="mode === 'text' && textContent.trim() && !titleInput.trim()"
          class="mt-1.5 text-xs text-red-600 dark:text-red-400"
        >
          {{ t('knowledge.text.titleRequired') }}
        </p>

        <!-- 最近一次上传（只对文件投递有意义，正文收录没有记录） -->
        <div v-if="mode === 'file'" class="mt-4 overflow-hidden rounded-xl border border-border">
          <div class="border-b border-border bg-muted/60 px-3 py-2 text-[13px] font-medium">
            {{ t('knowledge.last.title') }}
          </div>
          <div class="p-3 text-[13px]">
            <!-- 处理中 -->
            <div v-if="processing" class="flex items-start gap-2.5">
              <Loader2 class="mt-0.5 size-4 shrink-0 animate-spin text-amber-500" />
              <div class="min-w-0 flex-1">
                <p class="truncate font-medium">{{ processing.title }}</p>
                <p class="mt-1 flex flex-wrap items-center gap-1.5 text-zinc-600 dark:text-zinc-400">
                  <span
                    class="rounded-full border border-amber-200 bg-amber-50 px-2 py-0.5 text-xs leading-none text-amber-700 dark:border-amber-800 dark:bg-amber-950/40 dark:text-amber-300"
                  >
                    {{ t('knowledge.status.processing') }}
                  </span>
                  <!-- 首次上传：后端此时在准备解析环境，进度条要能对应上，否则这条一直停着像卡死 -->
                  <span v-if="store.parserStatus?.preparing">
                    {{ t('knowledge.last.preparing', { progress: store.parserStatus.progress }) }}
                  </span>
                  <span v-else>{{ t('knowledge.last.processing') }}</span>
                </p>
              </div>
            </div>

            <!-- 上次失败 -->
            <div v-else-if="failed" class="flex items-start gap-2.5">
              <AlertCircle class="mt-0.5 size-4 shrink-0 text-red-500" />
              <div class="min-w-0 flex-1">
                <p class="truncate font-medium">{{ failed.title }}</p>
                <p class="mt-1 flex flex-wrap items-center gap-1.5 text-zinc-600 dark:text-zinc-400">
                  <span
                    class="rounded-full border border-red-200 bg-red-50 px-2 py-0.5 text-xs leading-none text-red-700 dark:border-red-800 dark:bg-red-950/40 dark:text-red-300"
                  >
                    {{ t('knowledge.status.failed') }}
                  </span>
                  <span>{{ t('knowledge.last.failedHint') }}</span>
                </p>
                <p
                  v-if="failed.error"
                  class="mt-1.5 rounded-md bg-red-50 px-2 py-1.5 text-xs break-words text-red-700 dark:bg-red-950/30 dark:text-red-300"
                >
                  <span v-if="failedStageLabel" class="font-medium">{{ failedStageLabel }}：</span>
                  {{ failed.error }}
                </p>
                <!-- 重试同一份原件：不用重新选文件（后端留着归档的输入） -->
                <button
                  type="button"
                  class="mt-2 inline-flex cursor-pointer items-center gap-1 rounded-md border border-border px-2.5 py-1 text-xs transition-colors hover:bg-muted disabled:cursor-not-allowed disabled:opacity-40"
                  :disabled="!canRetry"
                  :title="
                    canRetry ? t('knowledge.action.retry') : t('knowledge.last.retryUnavailable')
                  "
                  @click="retryFailed"
                >
                  <RotateCw class="size-3.5" />
                  {{ t('knowledge.action.retry') }}
                </button>
              </div>
            </div>

            <!-- 空闲 -->
            <p v-else class="text-zinc-600 dark:text-zinc-400">{{ t('knowledge.last.idle') }}</p>
          </div>
        </div>

        <p class="mt-3 text-xs leading-5 text-muted-foreground">
          {{ t(mode === 'file' ? 'knowledge.new.note' : 'knowledge.text.note') }}
        </p>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
