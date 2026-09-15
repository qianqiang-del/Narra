<script setup lang="ts">
/**
 * 设置弹窗 —— 文档 §5.10。
 * 使用左侧导航组织设置项，便于后续扩展更多配置页面。
 */
import { DialogContent, DialogDescription, DialogOverlay, DialogPortal, DialogRoot, DialogTitle } from 'reka-ui'
import { Database, Monitor, Moon, Palette, Sun, X } from 'lucide-vue-next'
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { useTheme, type ThemeMode } from '@/composables/useTheme'
import { cn } from '@/lib/utils'

const open = defineModel<boolean>('open', { default: false })

const { t } = useI18n()
const { mode, setMode } = useTheme()
const activeSection = ref<'theme' | 'embedding'>('theme')

const themeOptions: { value: ThemeMode; labelKey: string; icon: typeof Sun }[] = [
  { value: 'light', labelKey: 'settings.light', icon: Sun },
  { value: 'dark', labelKey: 'settings.dark', icon: Moon },
  { value: 'system', labelKey: 'settings.system', icon: Monitor },
]

type EmbeddingForm = {
  base_url: string
  model: string
  timeout: number
  dimensions: number
  api_key: string
}

const apiBaseURL = import.meta.env.VITE_API_BASE_URL ?? '/api/v1'
const embeddingForm = ref<EmbeddingForm>({
  base_url: '',
  model: '',
  timeout: 30,
  dimensions: 0,
  api_key: '',
})
const embeddingLoaded = ref(false)
const apiKeyConfigured = ref(false)
const embeddingMessage = ref('')
const embeddingError = ref(false)
const loadingEmbedding = ref(false)
const testingEmbedding = ref(false)
const savingEmbedding = ref(false)

async function embeddingRequest(path: string, options: RequestInit = {}) {
  const response = await fetch(`${apiBaseURL}/settings/embedding${path}`, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...options.headers,
    },
  })
  const payload = await response.json()
  if (!response.ok || payload.code !== 0) {
    throw new Error(payload.message || t('settings.embeddingRequestFailed'))
  }
  return payload.data
}

function showEmbeddingMessage(message: string, isError = false) {
  embeddingMessage.value = message
  embeddingError.value = isError
}

function durationToSeconds(duration: string): number {
  const matched = duration.match(/^(?:(\d+)h)?(?:(\d+)m)?(?:(\d+(?:\.\d+)?)s)?$/)
  if (!matched) return 30

  return Number(matched[1] ?? 0) * 3600 + Number(matched[2] ?? 0) * 60 + Number(matched[3] ?? 0)
}

function embeddingPayload() {
  return {
    ...embeddingForm.value,
    enabled: true,
    timeout: `${embeddingForm.value.timeout}s`,
  }
}

async function loadEmbeddingConfig() {
  loadingEmbedding.value = true
  try {
    const data = await embeddingRequest('')
    embeddingForm.value = {
      base_url: data.base_url,
      model: data.model,
      timeout: durationToSeconds(data.timeout),
      dimensions: data.dimensions,
      api_key: '',
    }
    apiKeyConfigured.value = data.api_key_configured
    embeddingLoaded.value = true
    showEmbeddingMessage(t('settings.embeddingLoaded'))
  } catch (error) {
    showEmbeddingMessage(error instanceof Error ? error.message : t('settings.embeddingRequestFailed'), true)
  } finally {
    loadingEmbedding.value = false
  }
}

async function testEmbeddingConfig() {
  testingEmbedding.value = true
  try {
    const data = await embeddingRequest('/test', {
      method: 'POST',
      body: JSON.stringify(embeddingPayload()),
    })
    showEmbeddingMessage(t('settings.embeddingTestSuccess', { dimensions: data.dimensions }))
  } catch (error) {
    showEmbeddingMessage(error instanceof Error ? error.message : t('settings.embeddingRequestFailed'), true)
  } finally {
    testingEmbedding.value = false
  }
}

async function saveEmbeddingConfig() {
  savingEmbedding.value = true
  try {
    const data = await embeddingRequest('', {
      method: 'PUT',
      body: JSON.stringify(embeddingPayload()),
    })
    apiKeyConfigured.value = data.api_key_configured
    embeddingForm.value.api_key = ''
    showEmbeddingMessage(t('settings.embeddingSaved'))
  } catch (error) {
    showEmbeddingMessage(error instanceof Error ? error.message : t('settings.embeddingRequestFailed'), true)
  } finally {
    savingEmbedding.value = false
  }
}

function openEmbedding() {
  activeSection.value = 'embedding'
  if (!embeddingLoaded.value) {
    void loadEmbeddingConfig()
  }
}
</script>

<template>
  <DialogRoot v-model:open="open">
    <DialogPortal>
      <DialogOverlay class="fixed inset-0 z-[100] bg-black/40 backdrop-blur-[2px]" />
      <DialogContent
        class="fixed top-1/2 left-1/2 z-[101] flex h-[min(620px,calc(100dvh-2rem))] w-[calc(100vw-2rem)] max-w-[760px] -translate-x-1/2 -translate-y-1/2 overflow-hidden rounded-2xl border border-border bg-background shadow-2xl focus:outline-none"
      >
        <aside class="flex w-44 shrink-0 flex-col border-r border-border bg-muted/35 p-3 sm:w-52">
          <DialogTitle class="px-2 py-2 text-base font-semibold tracking-tight">
            {{ t('settings.title') }}
          </DialogTitle>
          <nav class="mt-3 space-y-1" :aria-label="t('settings.title')">
            <button
              type="button"
              :class="cn('flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-sm transition-colors', activeSection === 'theme' ? 'bg-violet-100 text-violet-800 dark:bg-violet-500/15 dark:text-violet-200' : 'text-muted-foreground hover:bg-muted hover:text-foreground')"
              @click="activeSection = 'theme'"
            >
              <Palette class="size-4" />
              {{ t('settings.theme') }}
            </button>
            <button
              type="button"
              :class="cn('flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-sm transition-colors', activeSection === 'embedding' ? 'bg-violet-100 text-violet-800 dark:bg-violet-500/15 dark:text-violet-200' : 'text-muted-foreground hover:bg-muted hover:text-foreground')"
              @click="openEmbedding"
            >
              <Database class="size-4" />
              {{ t('settings.embedding') }}
            </button>
          </nav>
        </aside>

        <main class="min-w-0 flex-1 overflow-y-auto p-6">
          <button
            type="button"
            class="absolute top-4 right-4 rounded-full p-1.5 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
            @click="open = false"
          >
            <X class="size-4" />
          </button>

          <section v-if="activeSection === 'theme'" class="space-y-5">
            <div>
              <h2 class="text-lg font-semibold tracking-tight">{{ t('settings.theme') }}</h2>
              <DialogDescription class="mt-1 text-[13px] text-muted-foreground">
                {{ t('settings.themeDesc') }}
              </DialogDescription>
            </div>
            <div class="grid gap-2 sm:grid-cols-3">
            <button
              v-for="opt in themeOptions"
              :key="opt.value"
              type="button"
              :class="
                cn(
                  'flex items-center justify-center gap-2 rounded-xl border px-3 py-3 text-[13px] transition-all',
                  mode === opt.value
                    ? 'border-violet-300 bg-violet-50 text-violet-700 shadow-sm dark:border-violet-600 dark:bg-violet-500/10 dark:text-violet-300'
                    : 'border-border text-muted-foreground hover:bg-muted',
                )
              "
              @click="setMode(opt.value)"
            >
              <component :is="opt.icon" class="size-4" />
              {{ t(opt.labelKey) }}
            </button>
            </div>
          </section>

          <section v-else class="space-y-5">
            <div>
              <h2 class="text-lg font-semibold tracking-tight">{{ t('settings.embedding') }}</h2>
              <DialogDescription class="mt-1 text-[13px] text-muted-foreground">
                {{ t('settings.embeddingDesc') }}
              </DialogDescription>
            </div>

            <form class="space-y-4" @submit.prevent="saveEmbeddingConfig">
              <label class="block text-sm font-medium">
                {{ t('settings.embeddingBaseURL') }}
                <input
                  v-model="embeddingForm.base_url"
                  type="url"
                  placeholder="https://api.openai.com/v1"
                  class="mt-1.5 w-full rounded-lg border border-input bg-background px-3 py-2 text-sm outline-none transition-colors focus:border-violet-400 focus:ring-2 focus:ring-violet-200 dark:focus:ring-violet-900"
                />
              </label>

              <div class="grid gap-4 sm:grid-cols-2">
                <label class="block text-sm font-medium">
                  {{ t('settings.embeddingModel') }}
                  <input
                    v-model="embeddingForm.model"
                    type="text"
                    placeholder="text-embedding-3-small"
                    class="mt-1.5 w-full rounded-lg border border-input bg-background px-3 py-2 text-sm outline-none transition-colors focus:border-violet-400 focus:ring-2 focus:ring-violet-200 dark:focus:ring-violet-900"
                  />
                </label>
                <label class="block text-sm font-medium">
                  {{ t('settings.embeddingDimensions') }}
                  <input
                    v-model.number="embeddingForm.dimensions"
                    type="number"
                    min="1"
                    placeholder="1536"
                    class="mt-1.5 w-full rounded-lg border border-input bg-background px-3 py-2 text-sm outline-none transition-colors focus:border-violet-400 focus:ring-2 focus:ring-violet-200 dark:focus:ring-violet-900"
                  />
                </label>
              </div>

              <div class="grid gap-4 sm:grid-cols-2">
                <label class="block text-sm font-medium">
                  <span class="flex items-center gap-1">
                    {{ t('settings.embeddingTimeout') }}
                    <span class="text-xs font-normal text-muted-foreground">s</span>
                  </span>
                  <input
                    v-model.number="embeddingForm.timeout"
                    type="number"
                    min="1"
                    step="1"
                    placeholder="30"
                    class="mt-1.5 w-full rounded-lg border border-input bg-background px-3 py-2 text-sm outline-none transition-colors focus:border-violet-400 focus:ring-2 focus:ring-violet-200 dark:focus:ring-violet-900"
                  />
                </label>
                <label class="block text-sm font-medium">
                  {{ t('settings.embeddingAPIKey') }}
                  <input
                    v-model="embeddingForm.api_key"
                    type="password"
                    autocomplete="new-password"
                    :placeholder="apiKeyConfigured ? t('settings.embeddingAPIKeyConfigured') : t('settings.embeddingAPIKeyPlaceholder')"
                    class="mt-1.5 w-full rounded-lg border border-input bg-background px-3 py-2 text-sm outline-none transition-colors focus:border-violet-400 focus:ring-2 focus:ring-violet-200 dark:focus:ring-violet-900"
                  />
                </label>
              </div>

              <p v-if="embeddingMessage" :class="embeddingError ? 'text-sm text-destructive' : 'text-sm text-emerald-600 dark:text-emerald-400'">
                {{ embeddingMessage }}
              </p>

              <div class="flex justify-end gap-2 border-t border-border pt-4">
                <button
                  type="button"
                  class="rounded-lg border border-border px-4 py-2 text-sm font-medium transition-colors hover:bg-muted disabled:cursor-not-allowed disabled:opacity-50"
                  :disabled="testingEmbedding || savingEmbedding"
                  @click="testEmbeddingConfig"
                >
                  {{ testingEmbedding ? t('settings.testing') : t('settings.testConnection') }}
                </button>
                <button
                  type="submit"
                  class="rounded-lg bg-violet-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-violet-700 disabled:cursor-not-allowed disabled:opacity-50"
                  :disabled="testingEmbedding || savingEmbedding"
                >
                  {{ savingEmbedding ? t('settings.saving') : t('settings.save') }}
                </button>
              </div>
            </form>
          </section>
        </main>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
