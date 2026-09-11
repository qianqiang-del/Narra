import { createI18n } from 'vue-i18n'

import enUS from './locales/en-US'
import zhCN from './locales/zh-CN'

/**
 * 原项目支持 12 种语言（zh-CN / zh-TW / en-US / ja-JP / ko-KR / ru-RU /
 * vi-VN / ar-SA / de-DE / es-MX / fr-FR / pt-BR）。
 * 重构时先落 zh-CN + en-US 两套骨架，其余按需补（见 docs/UI-还原文档.md §9 决策项）。
 */
export const SUPPORTED_LOCALES = [
  { code: 'zh-CN', label: '简体中文', short: '中' },
  { code: 'en-US', label: 'English', short: 'EN' },
] as const

export type LocaleCode = (typeof SUPPORTED_LOCALES)[number]['code']

const STORAGE_KEY = 'narra-locale'

function detectLocale(): LocaleCode {
  const saved = localStorage.getItem(STORAGE_KEY) as LocaleCode | null
  if (saved && SUPPORTED_LOCALES.some((l) => l.code === saved)) return saved
  return navigator.language.startsWith('zh') ? 'zh-CN' : 'en-US'
}

const i18n = createI18n({
  legacy: false,
  globalInjection: true,
  locale: detectLocale(),
  fallbackLocale: 'en-US',
  messages: {
    'zh-CN': zhCN,
    'en-US': enUS,
  },
})

export function setLocale(code: LocaleCode) {
  i18n.global.locale.value = code
  localStorage.setItem(STORAGE_KEY, code)
  document.documentElement.lang = code
}

export default i18n
