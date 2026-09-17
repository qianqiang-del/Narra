<script setup lang="ts">
/**
 * 设置弹窗 —— 文档 §5.10。
 * 使用左侧导航组织设置项，便于后续扩展更多配置页面。
 */
import { DialogContent, DialogDescription, DialogOverlay, DialogPortal, DialogRoot, DialogTitle } from 'reka-ui'
import { Bot, Database, Monitor, Moon, Palette, Plug, Plus, Sun, Trash2, Wifi, X } from 'lucide-vue-next'
import { reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { storeToRefs } from 'pinia'

import { useTheme, type ThemeMode } from '@/composables/useTheme'
import { useMcpStore } from '@/stores/mcp'
import { useLlmStore } from '@/stores/llm'
import LlmSettingsSection from '@/components/home/LlmSettingsSection.vue'
import { cn } from '@/lib/utils'

const open = defineModel<boolean>('open', { default: false })
const activeSection = defineModel<'theme' | 'llm' | 'embedding' | 'mcp'>('section', { default: 'theme' })

const { t } = useI18n()
const { mode, setMode } = useTheme()
const llmStore = useLlmStore()

const mcpStore = useMcpStore()
const { servers: mcpServers, loading: mcpLoading } = storeToRefs(mcpStore)

const deleteConfirmId = ref<number | null>(null)
const testingId = ref<number | null>(null)
const testResult = ref<{ id: number; success: boolean; message: string; tools?: string[] } | null>(null)

const mcpFormOpen = ref(false)
const mcpFormSubmitting = ref(false)
const mcpFormError = ref('')
const mcpForm = reactive({
  serverId: '',
  name: '',
  endpoint: '',
  apiKey: '',
  transport: 'streamable_http',
  enabled: true,
  required: false,
  startupTimeout: '10s',
  discoveryTimeout: '10s',
  callTimeout: '30s',
  sortOrder: 0,
})

function openAddForm() {
  mcpFormOpen.value = true
  mcpFormError.value = ''
  Object.assign(mcpForm, {
    serverId: '', name: '', endpoint: '', apiKey: '',
    transport: 'streamable_http', enabled: true, required: false,
    startupTimeout: '10s', discoveryTimeout: '10s', callTimeout: '30s', sortOrder: 0,
  })
}

function closeAddForm() {
  mcpFormOpen.value = false
  mcpFormError.value = ''
}

async function submitMcpForm() {
  mcpFormSubmitting.value = true
  mcpFormError.value = ''
  try {
    await mcpStore.add({ ...mcpForm })
    closeAddForm()
  } catch (e) {
    mcpFormError.value = e instanceof Error ? e.message : String(e)
  } finally {
    mcpFormSubmitting.value = false
  }
}

function openMcp() {
  activeSection.value = 'mcp'
  mcpStore.load()
}

function openLlm() {
  activeSection.value = 'llm'
  void llmStore.loadProviders()
}

let testTimer: ReturnType<typeof setTimeout> | null = null

async function runTest(id: number) {
  testingId.value = id
  testResult.value = null
  if (testTimer) { clearTimeout(testTimer); testTimer = null }
  try {
    const result = await mcpStore.test(id)
    testResult.value = { id, ...result }
  } catch (e) {
    testResult.value = { id, success: false, message: e instanceof Error ? e.message : String(e) }
  } finally {
    testingId.value = null
    testTimer = setTimeout(() => { testResult.value = null }, 5000)
  }
}

async function toggleEnabled(id: number, current: boolean) {
  try {
    await mcpStore.edit(id, { enabled: !current })
  } catch { /* ignore */ }
}

async function confirmDelete(id: number) {
  if (deleteConfirmId.value === id) {
    try {
      await mcpStore.remove(id)
      deleteConfirmId.value = null
      if (testResult.value?.id === id) testResult.value = null
    } catch { /* ignore */ }
  } else {
    deleteConfirmId.value = id
    setTimeout(() => { if (deleteConfirmId.value === id) deleteConfirmId.value = null }, 3000)
  }
}

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
              :class="cn('flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-sm transition-colors', activeSection === 'llm' ? 'bg-violet-100 text-violet-800 dark:bg-violet-500/15 dark:text-violet-200' : 'text-muted-foreground hover:bg-muted hover:text-foreground')"
              @click="openLlm"
            >
              <Bot class="size-4" />
              {{ t('settings.llm') }}
            </button>
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
            <button
              type="button"
              :class="cn('flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-sm transition-colors', activeSection === 'mcp' ? 'bg-violet-100 text-violet-800 dark:bg-violet-500/15 dark:text-violet-200' : 'text-muted-foreground hover:bg-muted hover:text-foreground')"
              @click="openMcp"
            >
              <Plug class="size-4" />
              {{ t('settings.mcpTools') }}
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

          <LlmSettingsSection v-else-if="activeSection === 'llm'" />

          <section v-else-if="activeSection === 'embedding'" class="space-y-5">
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

          <section v-else-if="activeSection === 'mcp'" class="space-y-5">
            <div class="flex items-center justify-between">
              <div>
                <h2 class="text-lg font-semibold tracking-tight">{{ t('settings.mcpTools') }}</h2>
                <DialogDescription class="mt-1 text-[13px] text-muted-foreground">
                  {{ t('mcp.desc') }}
                </DialogDescription>
              </div>
              <button
                v-if="!mcpFormOpen"
                type="button"
                class="inline-flex items-center gap-1.5 rounded-lg bg-violet-600 px-3 py-1.5 text-xs font-medium text-white transition-colors hover:bg-violet-700"
                @click="openAddForm"
              >
                <Plus class="size-3.5" />
                {{ t('mcp.add') }}
              </button>
            </div>

            <!-- 新增表单 -->
            <form v-if="mcpFormOpen" class="space-y-3 rounded-lg border border-border p-4" @submit.prevent="submitMcpForm">
              <div class="grid gap-3 sm:grid-cols-2">
                <label class="block text-sm font-medium">
                  {{ t('mcp.form.serverId') }}
                  <input v-model="mcpForm.serverId" required placeholder="tavily" class="mt-1 w-full rounded-lg border border-input bg-background px-3 py-1.5 text-sm outline-none focus:border-violet-400 focus:ring-2 focus:ring-violet-200" />
                </label>
                <label class="block text-sm font-medium">
                  {{ t('mcp.form.name') }}
                  <input v-model="mcpForm.name" required placeholder="Tavily Search" class="mt-1 w-full rounded-lg border border-input bg-background px-3 py-1.5 text-sm outline-none focus:border-violet-400 focus:ring-2 focus:ring-violet-200" />
                </label>
              </div>
              <label class="block text-sm font-medium">
                {{ t('mcp.form.endpoint') }}
                <input v-model="mcpForm.endpoint" required type="url" placeholder="https://mcp.tavily.com/mcp" class="mt-1 w-full rounded-lg border border-input bg-background px-3 py-1.5 text-sm outline-none focus:border-violet-400 focus:ring-2 focus:ring-violet-200" />
              </label>
              <label class="block text-sm font-medium">
                {{ t('mcp.form.apiKey') }}
                <input v-model="mcpForm.apiKey" type="password" autocomplete="new-password" placeholder="sk-..." class="mt-1 w-full rounded-lg border border-input bg-background px-3 py-1.5 text-sm outline-none focus:border-violet-400 focus:ring-2 focus:ring-violet-200" />
              </label>
              <p v-if="mcpFormError" class="text-sm text-destructive">{{ mcpFormError }}</p>
              <div class="flex justify-end gap-2 border-t border-border pt-3">
                <button type="button" class="rounded-lg border border-border px-3 py-1.5 text-sm transition-colors hover:bg-muted" @click="closeAddForm">
                  {{ t('common.cancel') }}
                </button>
                <button type="submit" :disabled="mcpFormSubmitting" class="rounded-lg bg-violet-600 px-3 py-1.5 text-sm font-medium text-white transition-colors hover:bg-violet-700 disabled:opacity-50">
                  {{ mcpFormSubmitting ? t('mcp.form.submitting') : t('mcp.form.submit') }}
                </button>
              </div>
            </form>

            <div v-else-if="mcpLoading" class="py-8 text-center text-sm text-muted-foreground">
              {{ t('common.loading') }}
            </div>

            <div v-else-if="mcpServers.length === 0" class="py-8 text-center text-sm text-muted-foreground">
              {{ t('mcp.empty') }}
            </div>

            <div v-else class="space-y-2">
              <div
                v-for="srv in mcpServers"
                :key="srv.id"
                class="flex items-center gap-3 rounded-lg border border-border px-4 py-3 transition-colors hover:bg-muted/50"
              >
                <div class="min-w-0 flex-1">
                  <div class="flex items-center gap-2">
                    <span class="text-sm font-medium">{{ srv.name }}</span>
                    <span class="rounded bg-muted px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">
                      {{ srv.serverId }}
                    </span>
                  </div>
                  <p class="mt-0.5 truncate font-mono text-xs text-muted-foreground">
                    {{ srv.endpoint }}
                  </p>
                </div>

                <!-- 测试连接 -->
                <button
                  type="button"
                  class="inline-flex items-center gap-1 rounded px-2 py-1 text-xs text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:opacity-50"
                  :disabled="testingId === srv.id"
                  @click="runTest(srv.id)"
                >
                  <Wifi class="size-3" />
                  {{ testingId === srv.id ? t('mcp.testing') : t('mcp.test') }}
                </button>

                <!-- 启用开关 -->
                <button
                  type="button"
                  :class="cn(
                    'relative inline-flex h-5 w-9 shrink-0 cursor-pointer rounded-full transition-colors',
                    srv.enabled ? 'bg-violet-600' : 'bg-muted'
                  )"
                  @click="toggleEnabled(srv.id, srv.enabled)"
                >
                  <span
                    :class="cn(
                      'inline-block h-4 w-4 transform rounded-full bg-white shadow-sm transition-transform',
                      srv.enabled ? 'translate-x-4' : 'translate-x-0.5'
                    )"
                    class="mt-0.5"
                  />
                </button>

                <!-- 删除 -->
                <button
                  type="button"
                  :class="cn(
                    'rounded p-1 transition-colors',
                    deleteConfirmId === srv.id
                      ? 'bg-destructive text-destructive-foreground'
                      : 'text-muted-foreground hover:bg-destructive/10 hover:text-destructive'
                  )"
                  @click="confirmDelete(srv.id)"
                >
                  <Trash2 class="size-3.5" />
                </button>
              </div>
            </div>

            <!-- 测试结果 -->
            <div
              v-if="testResult"
              :class="cn(
                'rounded-lg border px-4 py-3 text-sm',
                testResult.success ? 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-800 dark:bg-emerald-950 dark:text-emerald-300' : 'border-destructive/30 bg-destructive/5 text-destructive'
              )"
            >
              <p class="font-medium">{{ testResult.message }}</p>
              <p v-if="testResult.tools?.length" class="mt-1 text-xs opacity-80">
                {{ t('mcp.tools') }}：{{ testResult.tools.join(', ') }}
              </p>
            </div>
          </section>
        </main>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
