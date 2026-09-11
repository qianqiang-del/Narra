<script setup lang="ts">
/**
 * ClassroomHeader —— 文档 §6.3（顶部 Header，h-20 = 80px）。
 *
 * 左：返回 + 「当前场景」+ 场景标题
 * 右：HeaderControls = 语言/主题/设置胶囊 + Pro 开关 + 导出下拉
 */
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  ArrowLeft,
  Check,
  ChevronDown,
  Download,
  FileDown,
  Loader2,
  Monitor,
  Moon,
  NotebookText,
  Package,
  Archive,
  Settings,
  Sun,
} from 'lucide-vue-next'

import UiTooltip from '@/components/ui/UiTooltip.vue'
import { SwitchRoot, SwitchThumb } from 'reka-ui'
import { useTheme, type ThemeMode } from '@/composables/useTheme'
import { SUPPORTED_LOCALES, setLocale, type LocaleCode } from '@/i18n'
import { cn } from '@/lib/utils'

defineProps<{
  sceneTitle: string
  proMode: boolean
  exporting: boolean
  exportPercent: number
  canExport: boolean
}>()

const emit = defineEmits<{
  (e: 'back'): void
  (e: 'toggle-pro'): void
  (e: 'export', kind: string): void
  (e: 'open-settings'): void
}>()

const { t, locale } = useI18n()
const { mode, setMode } = useTheme()

const rootRef = ref<HTMLElement | null>(null)
const themeOpen = ref(false)
const langOpen = ref(false)
const exportOpen = ref(false)

const themeOptions: { value: ThemeMode; labelKey: string; icon: typeof Sun }[] = [
  { value: 'light', labelKey: 'settings.light', icon: Sun },
  { value: 'dark', labelKey: 'settings.dark', icon: Moon },
  { value: 'system', labelKey: 'settings.system', icon: Monitor },
]
const themeIcon = ref(themeOptions[2].icon)
function syncThemeIcon() {
  themeIcon.value = themeOptions.find((o) => o.value === mode.value)?.icon ?? Monitor
}
syncThemeIcon()

const localeShort = ref('EN')
function syncLocaleShort() {
  localeShort.value = SUPPORTED_LOCALES.find((l) => l.code === locale.value)?.short ?? 'EN'
}
syncLocaleShort()

const exportItems: { id: string; labelKey: string; icon: typeof Download }[] = [
  { id: 'pptx', labelKey: 'header.exportPptx', icon: FileDown },
  { id: 'pack', labelKey: 'header.exportResourcePack', icon: Package },
  { id: 'zip', labelKey: 'header.exportZip', icon: Archive },
  { id: 'script', labelKey: 'header.exportScript', icon: NotebookText },
]

function pickTheme(next: ThemeMode) {
  setMode(next)
  syncThemeIcon()
  themeOpen.value = false
}

function pickLocale(code: LocaleCode) {
  setLocale(code)
  syncLocaleShort()
  langOpen.value = false
}

function onDocMouseDown(e: MouseEvent) {
  if (!rootRef.value?.contains(e.target as Node)) {
    themeOpen.value = false
    langOpen.value = false
    exportOpen.value = false
  }
}

onMounted(() => document.addEventListener('mousedown', onDocMouseDown))
onBeforeUnmount(() => document.removeEventListener('mousedown', onDocMouseDown))
</script>

<template>
  <header class="z-10 flex h-20 items-center justify-between gap-4 bg-transparent px-8">
    <!-- 左 -->
    <div class="flex min-w-0 items-center gap-3">
      <button
        type="button"
        class="shrink-0 rounded-lg p-2 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-700 dark:text-gray-500 dark:hover:bg-gray-800 dark:hover:text-gray-300"
        @click="emit('back')"
      >
        <ArrowLeft class="size-5" />
      </button>
      <div class="flex min-w-0 flex-col">
        <span class="mb-0.5 text-[10px] font-bold tracking-widest text-gray-400 uppercase">
          {{ t('stage.currentScene') }}
        </span>
        <h1 class="truncate text-xl font-bold tracking-tight text-gray-800 dark:text-gray-200">
          {{ sceneTitle }}
        </h1>
      </div>
    </div>

    <!-- 右：HeaderControls -->
    <div ref="rootRef" class="flex shrink-0 items-center gap-2">
      <!-- 语言 / 主题 / 设置 -->
      <div
        class="flex shrink-0 items-center gap-1 rounded-full border border-gray-100/50 bg-white/60 px-2 py-1.5 shadow-sm backdrop-blur-md dark:border-gray-700/50 dark:bg-gray-800/60"
      >
        <div class="relative">
          <button
            type="button"
            class="flex items-center gap-1 rounded-full px-2.5 py-1 text-xs font-bold text-gray-500 transition-all hover:bg-white hover:text-gray-800 dark:text-gray-400 dark:hover:bg-gray-700"
            @click="((langOpen = !langOpen), (themeOpen = false), (exportOpen = false))"
          >
            {{ localeShort }}
            <ChevronDown class="size-3 opacity-50" />
          </button>
          <div
            v-if="langOpen"
            class="absolute top-full right-0 z-50 mt-2 min-w-[140px] overflow-hidden rounded-lg border border-gray-200 bg-white shadow-lg dark:border-gray-700 dark:bg-gray-800"
          >
            <button
              v-for="l in SUPPORTED_LOCALES"
              :key="l.code"
              type="button"
              :class="
                cn(
                  'flex w-full items-center gap-2 px-4 py-2 text-left text-sm transition-colors hover:bg-gray-100 dark:hover:bg-gray-700',
                  locale === l.code && 'bg-purple-50 text-purple-600 dark:bg-purple-900/20 dark:text-purple-400',
                )
              "
              @click="pickLocale(l.code)"
            >
              <Check v-if="locale === l.code" class="size-3.5" />
              <span v-else class="size-3.5" />
              {{ l.label }}
            </button>
          </div>
        </div>

        <div class="h-4 w-px bg-gray-200 dark:bg-gray-700" />

        <div class="relative">
          <button
            type="button"
            class="rounded-full p-2 text-gray-400 transition-all hover:bg-white hover:shadow-sm dark:text-gray-500 dark:hover:bg-gray-700"
            @click="((themeOpen = !themeOpen), (langOpen = false), (exportOpen = false))"
          >
            <component :is="themeIcon" class="size-4" />
          </button>
          <div
            v-if="themeOpen"
            class="absolute top-full right-0 z-50 mt-2 min-w-[140px] overflow-hidden rounded-lg border border-gray-200 bg-white shadow-lg dark:border-gray-700 dark:bg-gray-800"
          >
            <button
              v-for="opt in themeOptions"
              :key="opt.value"
              type="button"
              :class="
                cn(
                  'flex w-full items-center gap-2 px-4 py-2 text-left text-sm transition-colors hover:bg-gray-100 dark:hover:bg-gray-700',
                  mode === opt.value && 'bg-purple-50 text-purple-600 dark:bg-purple-900/20 dark:text-purple-400',
                )
              "
              @click="pickTheme(opt.value)"
            >
              <component :is="opt.icon" class="size-3.5" />
              {{ t(opt.labelKey) }}
            </button>
          </div>
        </div>

        <div class="h-4 w-px bg-gray-200 dark:bg-gray-700" />

        <button
          type="button"
          class="group rounded-full p-2 text-gray-400 transition-all hover:bg-white hover:shadow-sm dark:text-gray-500 dark:hover:bg-gray-700"
          @click="emit('open-settings')"
        >
          <Settings class="size-4 transition-transform duration-500 group-hover:rotate-90" />
        </button>
      </div>

      <!-- Pro 开关 -->
      <label
        :class="
          cn(
            'inline-flex h-9 shrink-0 items-center gap-2.5 rounded-full border bg-white/60 px-3 shadow-sm backdrop-blur-md transition-colors duration-200 dark:bg-gray-800/60',
            proMode ? 'border-violet-500/60' : 'border-gray-100/50 dark:border-gray-700/50',
          )
        "
      >
        <span
          :class="
            cn(
              'text-[11px] font-bold tracking-[0.14em] uppercase tabular-nums',
              proMode ? 'text-violet-600 dark:text-violet-300' : 'text-gray-400',
            )
          "
        >
          {{ t('header.proMode') }}
        </span>
        <SwitchRoot
          :model-value="proMode"
          class="relative h-5 w-9 shrink-0 rounded-full transition-colors data-[state=checked]:bg-violet-500 data-[state=unchecked]:bg-gray-200 dark:data-[state=unchecked]:bg-gray-700"
          @update:model-value="emit('toggle-pro')"
        >
          <SwitchThumb
            class="block size-4 translate-x-0.5 rounded-full bg-white shadow transition-transform data-[state=checked]:translate-x-4.5"
          />
        </SwitchRoot>
      </label>

      <!-- 导出下拉 -->
      <div class="relative">
        <UiTooltip :content="canExport ? t('header.export') : t('header.exportUnavailable')">
          <button
            type="button"
            :class="
              cn(
                'shrink-0 rounded-full p-2 transition-colors',
                canExport
                  ? 'text-gray-400 hover:bg-white hover:text-gray-700 dark:text-gray-500 dark:hover:bg-gray-700'
                  : 'cursor-not-allowed text-gray-300 opacity-50 dark:text-gray-600',
              )
            "
            :disabled="!canExport"
            @click="((exportOpen = !exportOpen), (themeOpen = false), (langOpen = false))"
          >
            <Loader2 v-if="exporting" class="size-5 animate-spin" />
            <Download v-else class="size-5" />
          </button>
        </UiTooltip>

        <div
          v-if="exporting"
          class="pointer-events-none absolute top-full right-0 mt-1 text-[10px] whitespace-nowrap text-muted-foreground/70 tabular-nums"
        >
          {{ t('header.exporting', { percent: exportPercent }) }}
        </div>

        <div
          v-if="exportOpen"
          class="absolute top-full right-0 z-50 mt-2 min-w-[190px] overflow-hidden rounded-xl border border-gray-200 bg-white p-1 shadow-lg dark:border-gray-700 dark:bg-gray-800"
        >
          <button
            v-for="item in exportItems"
            :key="item.id"
            type="button"
            class="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-[13px] text-gray-600 transition-colors hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-700"
            @click="((exportOpen = false), emit('export', item.id))"
          >
            <component :is="item.icon" class="size-3.5 text-gray-400" />
            {{ t(item.labelKey) }}
          </button>
        </div>
      </div>
    </div>
  </header>
</template>
