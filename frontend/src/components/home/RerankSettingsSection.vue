<script setup lang="ts">
import { reactive, ref } from 'vue'
import { storeToRefs } from 'pinia'
import { ArrowUpDown, Eye, EyeOff, Plus, Trash2, Wifi, X } from 'lucide-vue-next'
import { useRerankStore } from '@/stores/rerank'
import type { RerankModel } from '@/api/rerank'
import { cn } from '@/lib/utils'

/**
 * 设置弹层里的「重排模型」栏目。
 *
 * 布局与「大模型」同款：配置卡片列表 + 按需展开的表单。多存一条、同时只启用一条
 * （后端用约束保证），启用状态由开关表达；删除与测试都作用在单条配置上。
 */
const store = useRerankStore()
const { models, loading } = storeToRefs(store)

const editingId = ref<number | null>(null)
const formOpen = ref(false)
const submitting = ref(false)
const testingId = ref<number | null>(null)
const error = ref('')
const deleteConfirmId = ref<number | null>(null)
/** API Key 是否明文显示。默认遮住：密码框看不清填了什么，填错了也发现不了 */
const showApiKey = ref(false)
const form = reactive({
  name: '', baseUrl: '', apiKey: '', clearApiKey: false, timeoutSeconds: 2, model: '',
})

/** 把后端返回的 Go 时长串（如 "2s"）解析成秒数，parseInt 对 "1m30s" 只会读出 1。 */
function durationToSeconds(duration: string): number {
  const matched = duration.match(/^(?:(\d+)h)?(?:(\d+)m)?(?:(\d+(?:\.\d+)?)s)?$/)
  if (!matched) return 2

  return Number(matched[1] ?? 0) * 3600 + Number(matched[2] ?? 0) * 60 + Number(matched[3] ?? 0)
}

function resetForm(item?: RerankModel) {
  editingId.value = item?.id ?? null
  Object.assign(form, {
    name: item?.name ?? '', baseUrl: item?.baseUrl ?? '',
    apiKey: '', clearApiKey: false,
    timeoutSeconds: durationToSeconds(item?.timeout ?? '2s'),
    model: item?.model ?? '',
  })
  error.value = ''
  showApiKey.value = false
  formOpen.value = true
}

function closeForm() { formOpen.value = false; editingId.value = null }

async function submit() {
  submitting.value = true
  error.value = ''
  try {
    await store.save({
      name: form.name, baseUrl: form.baseUrl, apiKey: form.apiKey,
      clearApiKey: form.clearApiKey, timeout: `${form.timeoutSeconds}s`,
      model: form.model.trim(),
    }, editingId.value ?? undefined)
    closeForm()
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    submitting.value = false
  }
}

async function testModel(item: RerankModel) {
  testingId.value = item.id
  error.value = ''
  try {
    await store.test(item.id)
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
    await store.loadModels()
  } finally {
    testingId.value = null
  }
}

async function toggle(item: RerankModel) {
  error.value = ''
  try { await store.setEnabled(item.id, !item.enabled) }
  catch (e) { error.value = e instanceof Error ? e.message : String(e) }
}

async function remove(item: RerankModel) {
  if (deleteConfirmId.value !== item.id) {
    deleteConfirmId.value = item.id
    setTimeout(() => { if (deleteConfirmId.value === item.id) deleteConfirmId.value = null }, 3000)
    return
  }
  try { await store.remove(item.id); deleteConfirmId.value = null }
  catch (e) { error.value = e instanceof Error ? e.message : String(e) }
}

function statusText(item: RerankModel) {
  if (item.testStatus === 'success') return '测试成功'
  if (item.testStatus === 'failed') return '测试失败'
  return '未测试'
}
</script>

<template>
  <section class="space-y-5">
    <div class="flex items-center justify-between gap-3">
      <h3 class="text-sm font-semibold">已保存的配置</h3>
      <button v-if="!formOpen" type="button" class="inline-flex items-center gap-1.5 rounded-lg bg-teal-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-teal-700" @click="resetForm()">
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
          <input v-model="form.name" required maxlength="120" placeholder="硅基流动 · bge-reranker-v2-m3" class="mt-1 w-full rounded-lg border border-input bg-background px-3 py-2 text-sm outline-none focus:border-teal-400" />
        </label>
        <label class="text-sm font-medium">请求超时（秒）
          <input v-model.number="form.timeoutSeconds" required type="number" min="1" max="600" class="mt-1 w-full rounded-lg border border-input bg-background px-3 py-2 text-sm outline-none focus:border-teal-400" />
        </label>
      </div>
      <label class="block text-sm font-medium">Base URL
        <input v-model="form.baseUrl" required type="url" placeholder="https://api.siliconflow.cn/v1" class="mt-1 w-full rounded-lg border border-input bg-background px-3 py-2 text-sm outline-none focus:border-teal-400" />
      </label>
      <label class="block text-sm font-medium">模型 ID
        <input v-model="form.model" required maxlength="160" placeholder="BAAI/bge-reranker-v2-m3" class="mt-1 w-full rounded-lg border border-input bg-background px-3 py-2 font-mono text-sm outline-none focus:border-teal-400" />
      </label>
      <label class="block text-sm font-medium">API Key
        <div class="relative mt-1">
          <input
            v-model="form.apiKey"
            :type="showApiKey ? 'text' : 'password'"
            autocomplete="new-password"
            placeholder="可留空；编辑时留空表示保持不变"
            class="w-full rounded-lg border border-input bg-background px-3 py-2 pr-10 text-sm outline-none focus:border-teal-400"
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
      <p v-if="error" class="text-sm text-destructive">{{ error }}</p>
      <div class="flex justify-end gap-2 border-t border-border pt-3">
        <button type="button" class="rounded-lg border border-border px-3 py-2 text-sm hover:bg-muted" @click="closeForm">取消</button>
        <button type="submit" :disabled="submitting" class="rounded-lg bg-teal-600 px-3 py-2 text-sm font-medium text-white disabled:opacity-50">{{ submitting ? '保存中…' : '保存' }}</button>
      </div>
    </form>

    <p v-if="error && !formOpen" class="text-sm text-destructive">{{ error }}</p>
    <div v-if="loading" class="py-8 text-center text-sm text-muted-foreground">加载中…</div>
    <div v-else-if="models.length === 0 && !formOpen" class="rounded-xl border border-dashed border-border py-10 text-center">
      <ArrowUpDown class="mx-auto mb-2 size-7 text-muted-foreground/50" />
      <p class="text-sm text-muted-foreground">尚未配置重排模型</p>
    </div>
    <div v-else class="space-y-2">
      <div v-for="item in models" :key="item.id" class="rounded-xl border border-border p-4">
        <div class="flex items-center gap-3">
          <div class="min-w-0 flex-1">
            <div class="flex flex-wrap items-center gap-2">
              <span class="font-medium">{{ item.name }}</span>
              <span :class="cn('rounded px-1.5 py-0.5 text-[11px]', item.testStatus === 'success' ? 'bg-emerald-100 text-emerald-700' : item.testStatus === 'failed' ? 'bg-red-100 text-red-700' : 'bg-muted text-muted-foreground')">{{ statusText(item) }}</span>
              <span class="text-[11px] text-muted-foreground">{{ item.apiKeyConfigured ? 'API Key 已配置' : '无 API Key' }}</span>
            </div>
            <p class="mt-1 truncate font-mono text-xs text-muted-foreground">{{ item.baseUrl }}</p>
            <p class="mt-1 truncate font-mono text-xs text-muted-foreground">{{ item.model }}</p>
            <p v-if="item.lastTestError" class="mt-1 text-xs text-destructive">{{ item.lastTestError }}</p>
          </div>
          <button type="button" class="rounded px-2 py-1 text-xs text-muted-foreground hover:bg-muted" @click="resetForm(item)">编辑</button>
          <button type="button" :disabled="testingId === item.id" class="inline-flex items-center gap-1 rounded px-2 py-1 text-xs text-muted-foreground hover:bg-muted disabled:opacity-50" @click="testModel(item)">
            <Wifi class="size-3" />{{ testingId === item.id ? '测试中…' : '测试' }}
          </button>
          <button type="button" :class="cn('relative inline-flex h-5 w-9 shrink-0 rounded-full transition-colors', item.enabled ? 'bg-teal-600' : 'bg-muted', item.testStatus !== 'success' && 'opacity-50')" @click="toggle(item)">
            <span :class="cn('mt-0.5 inline-block h-4 w-4 rounded-full bg-white shadow transition-transform', item.enabled ? 'translate-x-4' : 'translate-x-0.5')" />
          </button>
          <button type="button" :class="cn('rounded p-1 text-muted-foreground hover:text-destructive', deleteConfirmId === item.id && 'bg-destructive text-white')" @click="remove(item)"><Trash2 class="size-4" /></button>
        </div>
      </div>
    </div>
  </section>
</template>
