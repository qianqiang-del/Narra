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
import { Bot, Check, ChevronDown, FileText, Globe2, Paperclip, Search, X } from 'lucide-vue-next'

import UiTooltip from '@/components/ui/UiTooltip.vue'
import { MODEL_PROVIDERS } from '@/data/providers'
import { cn } from '@/lib/utils'

const { t } = useI18n()

/** 与 HomeView 共享的生成配置 */
const providerId = defineModel<string>('providerId', { default: 'openai' })
const modelId = defineModel<string>('modelId', { default: 'gpt-4o-mini' })
const webSearch = defineModel<boolean>('webSearch', { default: false })
const extractor = defineModel<string>('extractor', { default: 'mineru' })
const materials = defineModel<{ id: string; name: string; size: number }[]>('materials', {
  default: () => [],
})

const { hasProvider } = defineProps<{ hasProvider?: boolean }>()

const rootRef = ref<HTMLElement | null>(null)
const openMenu = ref<'model' | 'material' | null>(null)
const modelKeyword = ref('')
const materialDragging = ref(false)

const providers = MODEL_PROVIDERS

const currentProvider = computed(() => providers.find((p) => p.id === providerId.value))
const currentModel = computed(() => currentProvider.value?.models.find((m) => m.id === modelId.value))

const filteredProviders = computed(() => {
  const kw = modelKeyword.value.trim().toLowerCase()
  if (!kw) return providers
  return providers.filter(
    (p) => p.name.toLowerCase().includes(kw) || p.id.toLowerCase().includes(kw),
  )
})

const activeModels = computed(() => currentProvider.value?.models ?? [])

const EXTRACTORS = [
  { id: 'mineru', name: 'MinerU' },
  { id: 'unpdf', name: 'unpdf' },
]

function toggle(menu: typeof openMenu.value) {
  openMenu.value = openMenu.value === menu ? null : menu
}

function pickProvider(id: string) {
  providerId.value = id
  const p = providers.find((x) => x.id === id)
  if (p) modelId.value = p.models[0]?.id ?? ''
}

function pickModel(id: string) {
  modelId.value = id
  openMenu.value = null
}

function removeMaterial(id: string) {
  materials.value = materials.value.filter((m) => m.id !== id)
}

function onFilePick(e: Event) {
  const input = e.target as HTMLInputElement
  const files = Array.from(input.files ?? [])
  input.value = ''
  for (const f of files) {
    materials.value = [
      ...materials.value,
      { id: `${f.name}-${f.size}-${Date.now()}`, name: f.name, size: f.size },
    ]
  }
}

function onDrop(e: DragEvent) {
  materialDragging.value = false
  const files = Array.from(e.dataTransfer?.files ?? [])
  for (const f of files) {
    materials.value = [
      ...materials.value,
      { id: `${f.name}-${f.size}-${Date.now()}`, name: f.name, size: f.size },
    ]
  }
}

/** 通用 pill 类名（文档 §5.6 的三套基类） */
const pillCls =
  'inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-medium transition-all cursor-pointer select-none whitespace-nowrap'
const pillMuted = cn(pillCls, 'border-border/50 text-muted-foreground/70 hover:bg-muted/60 hover:text-foreground')
const pillActive = cn(
  pillCls,
  'border-violet-200/60 bg-violet-100 text-violet-700 dark:border-violet-700/50 dark:bg-violet-900/30 dark:text-violet-300',
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
        @click="toggle('model')"
      >
        <Bot class="size-3.5" />
        {{ t('home.configureModel') }}
      </button>

      <button
        v-else
        type="button"
        class="inline-flex h-8 min-w-0 items-center gap-1.5 rounded-full border border-violet-200/70 bg-violet-50 px-2 text-xs font-medium text-violet-700 transition-colors hover:bg-violet-100 dark:border-violet-700/50 dark:bg-violet-900/30 dark:text-violet-300"
        @click="toggle('model')"
      >
        <img
          v-if="currentProvider"
          :src="currentProvider.logo"
          alt=""
          class="size-3.5 shrink-0 object-contain"
        />
        <Bot v-else class="size-3.5 shrink-0" />
        <span class="min-w-0 truncate">{{ currentModel?.name ?? '选择模型' }}</span>
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
                class="h-8 w-full rounded-md border border-input pl-8 text-xs outline-none focus:ring-1 focus:ring-violet-400/40"
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
                    providerId === p.id && 'bg-violet-50 text-violet-700 dark:bg-violet-900/30',
                  )
                "
                @click="pickProvider(p.id)"
              >
                <img :src="p.logo" alt="" class="size-3.5 shrink-0 object-contain" />
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
                  modelId === m.id && 'bg-violet-50 text-violet-700 ring-1 ring-violet-200',
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
        <Paperclip class="size-3.5" />
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
        <div class="mb-2 flex items-center justify-between gap-2">
          <span class="text-xs font-medium text-muted-foreground/70">{{
            t('toolbar.documentExtractor')
          }}</span>
          <select
            v-model="extractor"
            class="h-7 rounded-md border border-input bg-transparent px-1.5 text-xs outline-none"
          >
            <option v-for="x in EXTRACTORS" :key="x.id" :value="x.id">{{ x.name }}</option>
          </select>
        </div>

        <label
          :class="
            cn(
              'flex cursor-pointer flex-col items-center justify-center gap-1.5 rounded-lg border-2 border-dashed p-4 transition-colors',
              materialDragging
                ? 'border-violet-400 bg-violet-50 dark:bg-violet-900/20'
                : 'border-muted-foreground/20 hover:border-violet-300',
            )
          "
          @dragover.prevent="materialDragging = true"
          @dragleave.prevent="materialDragging = false"
          @drop.prevent="onDrop"
        >
          <Paperclip class="size-4 text-muted-foreground/50" />
          <span class="text-center text-[11px] text-muted-foreground/60">
            {{ t('toolbar.materialLimit') }}
          </span>
          <input
            type="file"
            multiple
            class="hidden"
            accept=".pdf,.doc,.docx,.ppt,.pptx,.xls,.xlsx,.txt,.md,.markdown,image/*"
            @change="onFilePick"
          />
        </label>

        <div v-if="materials.length" class="mt-2 max-h-40 space-y-1.5 overflow-y-auto">
          <div
            v-for="m in materials"
            :key="m.id"
            class="flex items-center gap-2 rounded-lg border border-border/50 px-2 py-2"
          >
            <div class="flex size-8 shrink-0 items-center justify-center rounded-lg bg-violet-100 dark:bg-violet-900/30">
              <FileText class="size-3.5 text-violet-600 dark:text-violet-300" />
            </div>
            <span class="min-w-0 flex-1 truncate text-xs">{{ m.name }}</span>
            <button
              type="button"
              class="shrink-0 rounded p-1 text-muted-foreground/50 hover:bg-muted hover:text-foreground"
              @click="removeMaterial(m.id)"
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

      <button
        v-else
        type="button"
        :class="webSearch ? pillActive : pillMuted"
        :aria-pressed="webSearch"
        :title="webSearch ? t('toolbar.webSearchOn') : t('toolbar.webSearchOff')"
        @click="webSearch = !webSearch"
      >
        <Globe2 :class="cn('size-3.5', webSearch && 'animate-pulse')" />
      </button>
    </div>
  </div>
</template>
