import { computed, ref, watch } from 'vue'

export type ThemeMode = 'light' | 'dark' | 'system'

const STORAGE_KEY = 'narra-theme'

/**
 * 主题三态：light / dark / system。
 * 与原项目一致，用 `.dark` class 加在 <html> 上（非 media query），
 * 对应 globals.css 里的 `@custom-variant dark (&:is(.dark *))`。
 */

function readStored(): ThemeMode {
  const saved = localStorage.getItem(STORAGE_KEY) as ThemeMode | null
  if (saved === 'light' || saved === 'dark' || saved === 'system') return saved
  return 'system'
}

const mode = ref<ThemeMode>(readStored())
const systemDark = ref(false)

const mediaQuery = window.matchMedia('(prefers-color-scheme: dark)')
systemDark.value = mediaQuery.matches
mediaQuery.addEventListener('change', (e) => {
  systemDark.value = e.matches
})

const isDark = computed(() => (mode.value === 'system' ? systemDark.value : mode.value === 'dark'))

function apply() {
  document.documentElement.classList.toggle('dark', isDark.value)
  document.documentElement.style.colorScheme = isDark.value ? 'dark' : 'light'
}

// 首帧即应用，避免闪烁
apply()

watch([mode, isDark], () => {
  apply()
  localStorage.setItem(STORAGE_KEY, mode.value)
})

export function useTheme() {
  function setMode(next: ThemeMode) {
    mode.value = next
  }

  return {
    mode,
    isDark,
    setMode,
    setLight: () => setMode('light'),
    setDark: () => setMode('dark'),
    setSystem: () => setMode('system'),
  }
}
