<script setup lang="ts">
/**
 * 「新增知识库」弹层：选文件 → 填标题 → 提交，下面挂一张"最近一次上传"卡片。
 *
 * 一次只能传一份：`store.upload` 会一直轮询到终态，`store.uploading` 也就一直为真，
 * 期间整个上传区禁用。服务端的强制拒绝属于批 ①，前端这层到时候仍然保留
 * （少一次注定失败的往返）。
 *
 * 卡片是"被拒绝时用户能看懂发生了什么"的配套 UI：它显示当前在处理哪一份、
 * 上一份是因为什么失败的；失败的那一份可以直接重试 —— 后端拿归档在服务器上的
 * 原件重跑，用户不用重新选文件。
 */
import {
  DialogContent,
  DialogDescription,
  DialogOverlay,
  DialogPortal,
  DialogRoot,
  DialogTitle,
} from 'reka-ui'
import { AlertCircle, FileText, Loader2, RotateCw, Upload, X } from 'lucide-vue-next'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'

import { MAX_UPLOAD_BYTES, SUPPORTED_EXTENSIONS } from '@/api/knowledge'
import { useKnowledgeStore } from '@/stores/knowledge'

const open = defineModel<boolean>('open', { default: false })

const { t } = useI18n()
const store = useKnowledgeStore()

const selectedFile = ref<File | null>(null)
const titleInput = ref('')
const dragging = ref(false)

const acceptAttr = SUPPORTED_EXTENSIONS.join(',')
const supportedHint = SUPPORTED_EXTENSIONS.join(' / ')

const busy = computed(() => store.uploading)

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

const selectedSize = computed(() =>
  selectedFile.value ? `${(selectedFile.value.size / 1024 / 1024).toFixed(1)} MB` : '',
)

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
              {{ t('knowledge.new.desc') }}
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

        <label
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
          v-if="selectedFile"
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

        <!-- 标题（可选）与提交 -->
        <div class="mt-3 flex items-center gap-2">
          <input
            v-model="titleInput"
            type="text"
            :disabled="busy"
            :placeholder="t('knowledge.upload.titlePlaceholder')"
            class="min-w-0 flex-1 rounded-lg border border-input bg-background px-3 py-2 text-[13px] outline-none transition-colors placeholder:text-zinc-500 focus:border-violet-400 disabled:opacity-60"
          />
          <button
            type="button"
            :disabled="!selectedFile || busy"
            class="flex shrink-0 cursor-pointer items-center gap-1.5 rounded-lg bg-primary px-4 py-2 text-[13px] font-medium text-primary-foreground transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-40"
            @click="submit"
          >
            <Loader2 v-if="busy" class="size-3.5 animate-spin" />
            <Upload v-else class="size-3.5" />
            {{ busy ? t('knowledge.upload.submitting') : t('knowledge.upload.submit') }}
          </button>
        </div>

        <!-- 最近一次上传 -->
        <div class="mt-4 overflow-hidden rounded-xl border border-border">
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
                  <span>{{ t('knowledge.last.processing') }}</span>
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
          {{ t('knowledge.new.note') }}
        </p>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
