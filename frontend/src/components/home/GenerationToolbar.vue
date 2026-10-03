<script setup lang="ts">
/**
 * GenerationToolbar —— 文档 §5.6（输入框底部工具栏）。
 *
 * 子项：模型选择器 | 分隔线 | 课程材料 | 联网搜索
 *
 * 模型/解析服务商数据当前为静态表（原项目从服务端配置接口拉取）。
 * 接入 Go 后端后替换为 `GET /api/providers`。
 *
 * 媒体生成：原版是 4 个 Tab 的弹层（Image / Video / TTS / ASR），本项目按需求全部删掉：
 * - 去掉 Image（文生图）与 Video（文生视频）
 * - TTS 恒为开启，不在前端暴露，用户不可选
 * - 「高级设置」入口去掉
 * - ASR 不在这里出现 —— 语音输入统一走 Composer 里那个 SpeechButton（见 §5.2），
 *   避免同一个功能出现两个入口
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'
import {
  AlertCircle,
  Bot,
  Check,
  ChevronDown,
  Clock,
  FileText,
  Globe2,
  Loader2,
  Paperclip,
  RotateCcw,
  Search,
  X,
} from 'lucide-vue-next'

import UiTooltip from '@/components/ui/UiTooltip.vue'
import { findProviderLogo } from '@/data/providers'
import type { AvailableLlmModel } from '@/api/llm'
import { SUPPORTED_EXTENSIONS } from '@/api/knowledge'
import { materialFingerprint, type SelectedMaterial, type SelectedMaterialStatus } from '@/lib/materials'
import { useUploadLimits } from '@/lib/upload-limits'
import { cn } from '@/lib/utils'

const { t } = useI18n()

/** 上传限制由后端下发（共享缓存），提示文案不再写死上限 */
const { limits: uploadLimits, sizeLabel: uploadSizeLabel } = useUploadLimits()

/** 与 HomeView 共享的生成配置 */
const providerId = defineModel<number | null>('providerId', { default: null })
const modelId = defineModel<string>('modelId', { default: '' })
const webSearch = defineModel<boolean>('webSearch', { default: false })
const materials = defineModel<SelectedMaterial[]>('materials', { default: () => [] })

const props = defineProps<{ availableModels: AvailableLlmModel[] }>()
const emit = defineEmits<{ configure: []; retryMaterial: [key: string] }>()

const rootRef = ref<HTMLElement | null>(null)
const openMenu = ref<'model' | 'material' | null>(null)
const modelKeyword = ref('')
const materialDragging = ref(false)

const providers = computed(() => {
  const groups = new Map<number, { id: number; name: string; logo: string; models: { id: string }[] }>()
  for (const row of props.availableModels) {
    let group = groups.get(row.providerId)
    if (!group) {
      group = { id: row.providerId, name: row.providerName, logo: findProviderLogo(row.providerName), models: [] }
      groups.set(row.providerId, group)
    }
    group.models.push({ id: row.modelId })
  }
  return [...groups.values()]
})

const hasProvider = computed(() => props.availableModels.length > 0)

const currentProvider = computed(() => providers.value.find((p) => p.id === providerId.value))
const currentModel = computed(() => currentProvider.value?.models.find((m) => m.id === modelId.value))

const filteredProviders = computed(() => {
  const kw = modelKeyword.value.trim().toLowerCase()
  if (!kw) return providers.value
  return providers.value.filter(
    (p) => p.name.toLowerCase().includes(kw) || String(p.id).includes(kw),
  )
})

const activeModels = computed(() => currentProvider.value?.models ?? [])

function toggle(menu: typeof openMenu.value) {
  openMenu.value = openMenu.value === menu ? null : menu
}

function pickProvider(id: number) {
  providerId.value = id
  const p = providers.value.find((x) => x.id === id)
  if (p) modelId.value = p.models[0]?.id ?? ''
}

function pickModel(id: string) {
  modelId.value = id
  openMenu.value = null
}

function removeMaterial(key: string) {
  materials.value = materials.value.filter((m) => m.key !== key)
}

/**
 * 请求重试一份失败/被拒的材料。
 *
 * 这里只把 key 交给 HomeView —— 重试方式取决于服务端状态（有没有文档、文档是不是
 * 真失败），由它统一处理：能原地重排就原地重排，没有文档的才退回上传。
 */
function retryMaterial(material: SelectedMaterial) {
  emit('retryMaterial', material.key)
}

/** 与后端解析能力对齐的可选扩展名；选择框用它，拖拽/粘贴由下面的预检兜底 */
const acceptAttr = SUPPORTED_EXTENSIONS.join(',')

/**
 * 把用户选中的一批文件并入待上传列表。
 *
 * 先在前端挡几道（扩展名、单份大小、份数、合计大小）—— 与知识库弹层的 pickFiles
 * 同一套判据，不能只依赖后端：服务端是先把整个 multipart 包收完（最多 100MB）
 * 才检查文件数，超份数时整包白传白写盘；不支持的格式与超单份上限则是逐项拒绝，
 * 用户点重试还会把注定失败的文件重传一遍。
 *
 * 跳过与整次不生效的分工也和弹层一致：格式不支持、单份超限只跳过该文件并提示；
 * 份数 / 合计超限则整次选择不生效（部分收下会让用户以为剩下的还有机会，
 * 实际是这次请求发不出去）。真正的拒绝始终来自服务端。
 */
function addFiles(files: File[]) {
  if (files.length === 0) return

  const accepted: File[] = []
  for (const file of files) {
    const name = file.name.toLowerCase()
    if (!SUPPORTED_EXTENSIONS.some((ext) => name.endsWith(ext))) {
      toast.error(t('toolbar.materialUnsupported', { formats: SUPPORTED_EXTENSIONS.join(' / ') }))
      continue
    }
    if (file.size > uploadLimits.value.maxFileBytes) {
      toast.error(t('toolbar.materialTooLarge', { limit: uploadLimits.value.maxFileBytes >> 20 }))
      continue
    }
    accepted.push(file)
  }
  if (accepted.length === 0) return

  // 去重：同名 + 同大小 + 同修改时间视为同一份材料，已在列表里的跳过。
  const seen = new Set(materials.value.map((m) => materialFingerprint(m.file)))
  const merged = [...materials.value]
  for (const file of accepted) {
    const fingerprint = materialFingerprint(file)
    if (seen.has(fingerprint)) continue
    seen.add(fingerprint)
    merged.push({
      key: `${fingerprint}:${Date.now()}`,
      file,
      name: file.name,
      size: file.size,
      status: 'queued',
      documentId: null,
      error: '',
    })
  }

  if (merged.length > uploadLimits.value.maxFiles) {
    toast.error(t('toolbar.materialTooMany', { limit: uploadLimits.value.maxFiles }))
    return
  }
  const total = merged.reduce((sum, material) => sum + material.size, 0)
  if (total > uploadLimits.value.maxBatchBytes) {
    toast.error(
      t('toolbar.materialBatchTooLarge', { limit: uploadLimits.value.maxBatchBytes >> 20 }),
    )
    return
  }
  materials.value = merged
}

function onFilePick(e: Event) {
  const input = e.target as HTMLInputElement
  addFiles(Array.from(input.files ?? []))
  input.value = ''
}

function onDrop(e: DragEvent) {
  materialDragging.value = false
  addFiles(Array.from(e.dataTransfer?.files ?? []))
}

const materialBusy = computed(() =>
  materials.value.some((m) => m.status === 'uploading' || m.status === 'pending'),
)

function materialStatusLabel(status: SelectedMaterialStatus): string {
  return t(`toolbar.materialStatus.${status}`)
}

/** 通用 pill 类名（文档 §5.6 的三套基类） */
const pillCls =
  'inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-medium transition-all cursor-pointer select-none whitespace-nowrap'
const pillMuted = cn(pillCls, 'border-border/50 text-muted-foreground/70 hover:bg-muted/60 hover:text-foreground')
const pillActive = cn(
  pillCls,
  'border-teal-200/60 bg-teal-100 text-teal-700 dark:border-teal-700/50 dark:bg-teal-900/30 dark:text-teal-300',
)

function onDocMouseDown(e: MouseEvent) {
  if (rootRef.value && !rootRef.value.contains(e.target as Node)) openMenu.value = null
}

onMounted(() => document.addEventListener('mousedown', onDocMouseDown))
onBeforeUnmount(() => document.removeEventListener('mousedown', onDocMouseDown))
</script>

<template>
  <div ref="rootRef" class="flex flex-wrap items-center gap-1">
    <!-- 1. 模型选择器 -->
    <div class="relative">
      <button
        v-if="!hasProvider"
        type="button"
        :class="cn(pillCls, 'animate-pulse bg-amber-50 text-amber-600 hover:bg-amber-100')"
        @click="emit('configure')"
      >
        <Bot class="size-3.5" />
        {{ t('home.configureModel') }}
      </button>

      <button
        v-else
        type="button"
        class="inline-flex h-8 min-w-0 items-center gap-1.5 rounded-full border border-teal-200/70 bg-teal-50 px-2 text-xs font-medium text-teal-700 transition-colors hover:bg-teal-100 dark:border-teal-700/50 dark:bg-teal-900/30 dark:text-teal-300"
        @click="toggle('model')"
      >
        <img
          v-if="currentProvider?.logo"
          :src="currentProvider.logo"
          alt=""
          class="size-3.5 shrink-0 object-contain"
        />
        <Bot v-else class="size-3.5 shrink-0" />
        <span class="min-w-0 truncate">{{ currentModel?.id ?? '选择模型' }}</span>
        <ChevronDown class="size-3 shrink-0 opacity-60" />
      </button>

      <!-- 模型 Popover：左服务商 / 右模型 -->
      <div
        v-if="openMenu === 'model'"
        class="absolute bottom-full left-0 z-50 mb-2 w-[640px] max-w-[calc(100vw-2rem)] overflow-hidden rounded-xl border border-border bg-popover p-1.5 shadow-lg"
      >
        <div class="grid h-[430px] grid-cols-[128px_minmax(0,1fr)] sm:grid-cols-[160px_minmax(0,1fr)]">
          <div class="flex min-h-0 flex-col border-r border-border/60 pr-1.5">
            <div class="relative mb-1 shrink-0">
              <Search
                class="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground/50"
              />
              <input
                v-model="modelKeyword"
                type="text"
                placeholder="搜索服务商"
                class="h-8 w-full rounded-md border border-input pl-8 text-xs outline-none focus:ring-1 focus:ring-teal-400/40"
              />
            </div>
            <div class="min-h-0 flex-1 overflow-y-auto">
              <button
                v-for="p in filteredProviders"
                :key="p.id"
                type="button"
                :class="
                  cn(
                    'flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs transition-colors hover:bg-muted/60',
                    providerId === p.id && 'bg-teal-50 text-teal-700 dark:bg-teal-900/30',
                  )
                "
                @click="pickProvider(p.id)"
              >
                <img v-if="p.logo" :src="p.logo" alt="" class="size-3.5 shrink-0 object-contain" />
                <Bot v-else class="size-3.5 shrink-0" />
                <span class="min-w-0 truncate">{{ p.name }}</span>
              </button>
            </div>
          </div>

          <div class="min-h-0 overflow-y-auto pl-1.5">
            <button
              v-for="m in activeModels"
              :key="m.id"
              type="button"
              :class="
                cn(
                  'flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left font-mono text-xs transition-colors hover:bg-muted/60',
                  modelId === m.id && 'bg-teal-50 text-teal-700 ring-1 ring-teal-200',
                )
              "
              @click="pickModel(m.id)"
            >
              <span class="min-w-0 flex-1 truncate">{{ m.id }}</span>
              <Check v-if="modelId === m.id" class="size-3.5 shrink-0" />
            </button>
          </div>
        </div>
      </div>
    </div>

    <div class="mx-1 h-4 w-px bg-border/60" />

    <!-- 2. 课程材料 -->
    <div class="relative">
      <button
        type="button"
        :class="materials.length ? pillActive : pillMuted"
        @click="toggle('material')"
      >
        <Loader2 v-if="materialBusy" class="size-3.5 animate-spin" />
        <Paperclip v-else class="size-3.5" />
        <span v-if="materials.length">
          {{
            materials.length === 1
              ? materials[0].name
              : t('toolbar.materialSelected', { count: materials.length })
          }}
        </span>
      </button>

      <div
        v-if="openMenu === 'material'"
        class="absolute bottom-full left-0 z-50 mb-2 w-72 rounded-xl border border-border bg-popover p-3 shadow-lg"
      >
        <label
          :class="
            cn(
              'flex cursor-pointer flex-col items-center justify-center gap-1.5 rounded-lg border-2 border-dashed p-4 transition-colors',
              materialDragging
                ? 'border-teal-400 bg-teal-50 dark:bg-teal-900/20'
                : 'border-muted-foreground/20 hover:border-teal-300',
            )
          "
          @dragover.prevent="materialDragging = true"
          @dragleave.prevent="materialDragging = false"
          @drop.prevent="onDrop"
        >
          <Paperclip class="size-4 text-muted-foreground/50" />
          <span class="text-center text-[11px] text-muted-foreground/60">
            {{ t('toolbar.materialLimit', { size: uploadSizeLabel, max: uploadLimits.maxFiles }) }}
          </span>
          <input
            type="file"
            multiple
            class="hidden"
            :accept="acceptAttr"
            @change="onFilePick"
          />
        </label>

        <div v-if="materials.length" class="mt-2 max-h-40 space-y-1.5 overflow-y-auto">
          <div
            v-for="m in materials"
            :key="m.key"
            class="flex items-center gap-2 rounded-lg border border-border/50 px-2 py-2"
          >
            <div class="flex size-8 shrink-0 items-center justify-center rounded-lg bg-teal-100 dark:bg-teal-900/30">
              <Loader2
                v-if="m.status === 'uploading' || m.status === 'pending'"
                class="size-3.5 animate-spin text-teal-600 dark:text-teal-300"
              />
              <Check v-else-if="m.status === 'ready'" class="size-3.5 text-emerald-600 dark:text-emerald-400" />
              <AlertCircle
                v-else-if="m.status === 'failed' || m.status === 'rejected'"
                class="size-3.5 text-red-500"
              />
              <Clock v-else-if="m.status === 'queued'" class="size-3.5 text-muted-foreground/60" />
              <FileText v-else class="size-3.5 text-teal-600 dark:text-teal-300" />
            </div>
            <div class="min-w-0 flex-1">
              <p class="truncate text-xs">{{ m.name }}</p>
              <p
                class="truncate text-[10px]"
                :class="
                  m.status === 'failed' || m.status === 'rejected'
                    ? 'text-red-500'
                    : 'text-muted-foreground/60'
                "
                :title="m.error"
              >
                {{ m.error || materialStatusLabel(m.status) }}
              </p>
            </div>
            <button
              v-if="m.status === 'failed' || m.status === 'rejected'"
              type="button"
              class="shrink-0 rounded p-1 text-muted-foreground/50 hover:bg-muted hover:text-foreground"
              :title="t('toolbar.materialRetry')"
              @click="retryMaterial(m)"
            >
              <RotateCcw class="size-3.5" />
            </button>
            <button
              type="button"
              class="shrink-0 rounded p-1 text-muted-foreground/50 hover:bg-muted hover:text-foreground"
              @click="removeMaterial(m.key)"
            >
              <X class="size-3.5" />
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- 3. 联网搜索 -->
    <div class="relative">
      <UiTooltip v-if="!hasProvider" content="请在设置中配置搜索引擎 API Key">
        <button type="button" :class="cn(pillMuted, 'cursor-not-allowed opacity-50')" disabled>
          <Globe2 class="size-3.5" />
        </button>
      </UiTooltip>

      <UiTooltip v-else :content="t('toolbar.webSearchHint')">
        <button
          type="button"
          :class="webSearch ? pillActive : pillMuted"
          :aria-pressed="webSearch"
          @click="webSearch = !webSearch"
        >
          <Globe2 :class="cn('size-3.5', webSearch && 'animate-pulse')" />
        </button>
      </UiTooltip>
    </div>
  </div>
</template>
