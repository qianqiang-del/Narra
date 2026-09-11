<script setup lang="ts">
/**
 * 右上角悬浮胶囊工具栏 —— 文档 §5.1。
 *
 * 结构：语言切换 | 分隔线 | 主题三态 | 分隔线 | 设置
 * 交互：mousedown 点击外部关闭主题下拉；打开语言下拉时先关主题下拉。
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Check, ChevronDown, Monitor, Moon, Settings, Sun } from 'lucide-vue-next'

import { useTheme, type ThemeMode } from '@/composables/useTheme'
import { SUPPORTED_LOCALES, setLocale, type LocaleCode } from '@/i18n'
import { cn } from '@/lib/utils'

const emit = defineEmits<{ (e: 'open-settings'): void }>()

const { locale, t } = useI18n()
const { mode, setMode } = useTheme()

const toolbarRef = ref<HTMLElement | null>(null)
const themeOpen = ref(false)
const langOpen = ref(false)

const currentLocale = computed(() => SUPPORTED_LOCALES.find((l) => l.code === locale.value))
const localeShort = computed(() => currentLocale.value?.short ?? 'EN')

const themeOptions: { value: ThemeMode; labelKey: string; icon: typeof Sun }[] = [
  { value: 'light', labelKey: 'settings.light', icon: Sun },
  { value: 'dark', labelKey: 'settings.dark', icon: Moon },
  { value: 'system', labelKey: 'settings.system', icon: Monitor },
]

const themeIcon = computed(
  () => themeOptions.find((o) => o.value === mode.value)?.icon ?? Monitor,
)

function pickTheme(next: ThemeMode) {
  setMode(next)
  themeOpen.value = false
}

function pickLocale(code: LocaleCode) {
  setLocale(code)
  langOpen.value = false
}

function onDocMouseDown(e: MouseEvent) {
  if (!toolbarRef.value?.contains(e.target as Node)) {
    themeOpen.value = false
    langOpen.value = false
  }
}

onMounted(() => document.addEventListener('mousedown', onDocMouseDown))
onBeforeUnmount(() => document.removeEventListener('mousedown', onDocMouseDown))
</script>

<template>
  <div
    ref="toolbarRef"
    class="fixed top-4 right-4 z-50 flex items-center gap-1 rounded-full border border-gray-100/50 bg-white/60 px-2 py-1.5 shadow-sm backdrop-blur-md dark:border-gray-700/50 dark:bg-gray-800/60"
  >
    <!-- 语言切换 -->
    <div class="relative">
      <button
        type="button"
        class="flex items-center gap-1 rounded-full px-3 py-1.5 text-xs font-bold text-gray-500 transition-all hover:bg-white hover:text-gray-800 hover:shadow-sm dark:text-gray-400 dark:hover:bg-gray-700 dark:hover:text-gray-200"
        @click="((langOpen = !langOpen), (themeOpen = false))"
      >
        <span>{{ localeShort }}</span>
        <ChevronDown class="size-3 opacity-50" />
      </button>

      <div
        v-if="langOpen"
        class="absolute top-full right-0 mt-2 z-50 min-w-[140px] overflow-hidden rounded-lg border border-gray-200 bg-white shadow-lg dark:border-gray-700 dark:bg-gray-800"
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

    <div class="h-4 w-[1px] bg-gray-200 dark:bg-gray-700" />

    <!-- 主题三态 -->
    <div class="relative">
      <button
        type="button"
        class="rounded-full p-2 text-gray-400 transition-all hover:bg-white hover:text-gray-800 hover:shadow-sm dark:text-gray-500 dark:hover:bg-gray-700"
        @click="((themeOpen = !themeOpen), (langOpen = false))"
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

    <div class="h-4 w-[1px] bg-gray-200 dark:bg-gray-700" />

    <!-- 设置 -->
    <button
      type="button"
      class="group rounded-full p-2 text-gray-400 transition-all hover:bg-white hover:text-gray-800 hover:shadow-sm dark:text-gray-500 dark:hover:bg-gray-700"
      @click="emit('open-settings')"
    >
      <Settings class="size-4 transition-transform duration-500 group-hover:rotate-90" />
    </button>
  </div>
</template>
