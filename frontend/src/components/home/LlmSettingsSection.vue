<script setup lang="ts">
import { reactive, ref } from 'vue'
import { storeToRefs } from 'pinia'
import { Bot, Eye, EyeOff, Plus, Trash2, Wifi, X } from 'lucide-vue-next'
import { useLlmStore } from '@/stores/llm'
import type { LlmProvider } from '@/api/llm'
import { cn } from '@/lib/utils'

const store = useLlmStore()
const { providers, loading } = storeToRefs(store)

const editingId = ref<number | null>(null)
const formOpen = ref(false)
const submitting = ref(false)
const testingId = ref<number | null>(null)
const error = ref('')
const message = ref('')
const deleteConfirmId = ref<number | null>(null)
/** API Key 是否明文显示。默认遮住：密码框看不清填了什么，填错了也发现不了 */
const showApiKey = ref(false)
const form = reactive({
  name: '', baseUrl: '', apiKey: '', clearApiKey: false, timeoutSeconds: 60, models: [''],
})

function resetForm(provider?: LlmProvider) {
  editingId.value = provider?.id ?? null
  Object.assign(form, {
    name: provider?.name ?? '', baseUrl: provider?.baseUrl ?? '',
    apiKey: '', clearApiKey: false,
    timeoutSeconds: Number.parseInt(provider?.timeout ?? '60', 10) || 60,
    models: provider ? [...provider.models] : [''],
  })
  error.value = ''
  message.value = ''
  showApiKey.value = false
  formOpen.value = true
}

function closeForm() { formOpen.value = false; editingId.value = null }
function addModel() { form.models.push('') }
function removeModel(index: number) {
  if (form.models.length === 1) form.models[0] = ''
  else form.models.splice(index, 1)
}

async function submit() {
  submitting.value = true
  error.value = ''
  try {
    await store.save({
      name: form.name, baseUrl: form.baseUrl, apiKey: form.apiKey,
      clearApiKey: form.clearApiKey, timeout: `${form.timeoutSeconds}s`,
      models: form.models.map((x) => x.trim()).filter(Boolean),
    }, editingId.value ?? undefined)
    closeForm()
    message.value = '配置已保存，请测试连接后启用。'
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally { submitting.value = false }
}

async function testProvider(provider: LlmProvider) {
  testingId.value = provider.id
  error.value = ''
  message.value = ''
  try {
    const result = await store.test(provider.id)
    message.value = `“${provider.name}”${result.message}，现在可以启用。`
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
    await store.loadProviders()
  } finally { testingId.value = null }
}

async function toggle(provider: LlmProvider) {
  error.value = ''
  try { await store.setEnabled(provider.id, !provider.enabled) }
  catch (e) { error.value = e instanceof Error ? e.message : String(e) }
}

async function remove(provider: LlmProvider) {
  if (deleteConfirmId.value !== provider.id) {
    deleteConfirmId.value = provider.id
    setTimeout(() => { if (deleteConfirmId.value === provider.id) deleteConfirmId.value = null }, 3000)
    return
  }
  try { await store.remove(provider.id); deleteConfirmId.value = null }
  catch (e) { error.value = e instanceof Error ? e.message : String(e) }
}

function statusText(provider: LlmProvider) {
  if (provider.testStatus === 'success') return '测试成功'
  if (provider.testStatus === 'failed') return '测试失败'
  return '未测试'
}
</script>

<template>
  <section class="space-y-5">
    <div class="flex items-start justify-between gap-3">
      <div>
        <h2 class="text-lg font-semibold tracking-tight">大模型</h2>
        <p class="mt-1 text-[13px] text-muted-foreground">配置 OpenAI 兼容服务，测试成功并启用后可用于生成课堂。</p>
      </div>
      <button v-if="!formOpen" type="button" class="inline-flex items-center gap-1.5 rounded-lg bg-violet-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-violet-700" @click="resetForm()">
        <Plus class="size-3.5" />新增配置
      </button>
    </div>

    <form v-if="formOpen" class="space-y-3 rounded-xl border border-border p-4" @submit.prevent="submit">
      <div class="flex items-center justify-between">
        <h3 class="text-sm font-semibold">{{ editingId ? '编辑配置' : '新增配置' }}</h3>
        <button type="button" class="text-muted-foreground hover:text-foreground" @click="closeForm"><X class="size-4" /></button>
      </div>
      <div class="grid gap-3 sm:grid-cols-2">
        <label class="text-sm font-medium">配置名称
          <input v-model="form.name" required maxlength="120" placeholder="DeepSeek" class="mt-1 w-full rounded-lg border border-input bg-background px-3 py-2 text-sm outline-none focus:border-violet-400" />
        </label>
        <label class="text-sm font-medium">请求超时（秒）
          <input v-model.number="form.timeoutSeconds" required type="number" min="1" max="600" class="mt-1 w-full rounded-lg border border-input bg-background px-3 py-2 text-sm outline-none focus:border-violet-400" />
        </label>
      </div>
      <label class="block text-sm font-medium">Base URL
        <input v-model="form.baseUrl" required type="url" placeholder="https://api.openai.com/v1" class="mt-1 w-full rounded-lg border border-input bg-background px-3 py-2 text-sm outline-none focus:border-violet-400" />
      </label>
      <label class="block text-sm font-medium">API Key
        <div class="relative mt-1">
          <input
            v-model="form.apiKey"
            :type="showApiKey ? 'text' : 'password'"
            autocomplete="new-password"
            placeholder="可留空；编辑时留空表示保持不变"
            class="w-full rounded-lg border border-input bg-background px-3 py-2 pr-10 text-sm outline-none focus:border-violet-400"
          />
          <button
            type="button"
            :title="showApiKey ? '隐藏' : '显示'"
            :aria-label="showApiKey ? '隐藏 API Key' : '显示 API Key'"
            class="absolute right-1.5 top-1/2 -translate-y-1/2 rounded p-1.5 text-muted-foreground hover:bg-muted"
            @click="showApiKey = !showApiKey"
          >
            <EyeOff v-if="showApiKey" class="size-4" />
            <Eye v-else class="size-4" />
          </button>
        </div>
      </label>
      <label v-if="editingId" class="flex items-center gap-2 text-xs text-muted-foreground">
        <input v-model="form.clearApiKey" type="checkbox" />清除已保存的 API Key
      </label>
      <div>
        <div class="mb-1 flex items-center justify-between">
          <span class="text-sm font-medium">模型列表</span>
          <button type="button" class="text-xs text-violet-600 hover:text-violet-700" @click="addModel">+ 添加模型</button>
        </div>
        <div class="space-y-2">
          <div v-for="(_, index) in form.models" :key="index" class="flex gap-2">
            <input v-model="form.models[index]" required maxlength="160" placeholder="gpt-4o-mini" class="min-w-0 flex-1 rounded-lg border border-input bg-background px-3 py-2 font-mono text-sm outline-none focus:border-violet-400" />
            <button type="button" class="rounded-lg border border-border px-2 text-muted-foreground hover:text-destructive" @click="removeModel(index)"><Trash2 class="size-4" /></button>
          </div>
        </div>
      </div>
      <p v-if="error" class="text-sm text-destructive">{{ error }}</p>
      <div class="flex justify-end gap-2 border-t border-border pt-3">
        <button type="button" class="rounded-lg border border-border px-3 py-2 text-sm hover:bg-muted" @click="closeForm">取消</button>
        <button type="submit" :disabled="submitting" class="rounded-lg bg-violet-600 px-3 py-2 text-sm font-medium text-white disabled:opacity-50">{{ submitting ? '保存中…' : '保存' }}</button>
      </div>
    </form>

    <p v-if="message" class="text-sm text-emerald-600">{{ message }}</p>
    <p v-if="error && !formOpen" class="text-sm text-destructive">{{ error }}</p>
    <div v-if="loading" class="py-8 text-center text-sm text-muted-foreground">加载中…</div>
    <div v-else-if="providers.length === 0 && !formOpen" class="rounded-xl border border-dashed border-border py-10 text-center">
      <Bot class="mx-auto mb-2 size-7 text-muted-foreground/50" />
      <p class="text-sm text-muted-foreground">尚未配置大模型</p>
    </div>
    <div v-else class="space-y-2">
      <div v-for="provider in providers" :key="provider.id" class="rounded-xl border border-border p-4">
        <div class="flex items-center gap-3">
          <div class="min-w-0 flex-1">
            <div class="flex flex-wrap items-center gap-2">
              <span class="font-medium">{{ provider.name }}</span>
              <span :class="cn('rounded px-1.5 py-0.5 text-[11px]', provider.testStatus === 'success' ? 'bg-emerald-100 text-emerald-700' : provider.testStatus === 'failed' ? 'bg-red-100 text-red-700' : 'bg-muted text-muted-foreground')">{{ statusText(provider) }}</span>
              <span class="text-[11px] text-muted-foreground">{{ provider.apiKeyConfigured ? 'API Key 已配置' : '无 API Key' }}</span>
            </div>
            <p class="mt-1 truncate font-mono text-xs text-muted-foreground">{{ provider.baseUrl }}</p>
            <p class="mt-1 text-xs text-muted-foreground">{{ provider.models.join('、') }}</p>
            <p v-if="provider.lastTestError" class="mt-1 text-xs text-destructive">{{ provider.lastTestError }}</p>
          </div>
          <button type="button" class="rounded px-2 py-1 text-xs text-muted-foreground hover:bg-muted" @click="resetForm(provider)">编辑</button>
          <button type="button" :disabled="testingId === provider.id" class="inline-flex items-center gap-1 rounded px-2 py-1 text-xs text-muted-foreground hover:bg-muted disabled:opacity-50" @click="testProvider(provider)">
            <Wifi class="size-3" />{{ testingId === provider.id ? '测试中…' : '测试' }}
          </button>
          <button type="button" :class="cn('relative inline-flex h-5 w-9 shrink-0 rounded-full transition-colors', provider.enabled ? 'bg-violet-600' : 'bg-muted', provider.testStatus !== 'success' && 'opacity-50')" @click="toggle(provider)">
            <span :class="cn('mt-0.5 inline-block h-4 w-4 rounded-full bg-white shadow transition-transform', provider.enabled ? 'translate-x-4' : 'translate-x-0.5')" />
          </button>
          <button type="button" :class="cn('rounded p-1 text-muted-foreground hover:text-destructive', deleteConfirmId === provider.id && 'bg-destructive text-white')" @click="remove(provider)"><Trash2 class="size-4" /></button>
        </div>
      </div>
    </div>
  </section>
</template>
