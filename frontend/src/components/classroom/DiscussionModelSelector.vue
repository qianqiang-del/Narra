<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { Loader2, RefreshCw } from 'lucide-vue-next'
import ModelPicker from '@/components/shared/ModelPicker.vue'
import { fetchAvailableLlmModels, type AvailableLlmModel } from '@/api/llm'
import { fetchDiscussionSettings, updateDiscussionSettings, type DiscussionSettings } from '@/api/discussionSettings'
import type { ModelSelection } from '@/lib/modelSelection'

const props = defineProps<{ classroomId: number; disabled?: boolean; running?: boolean }>()
const emit = defineEmits<{ busy: [value: boolean] }>()
const models = ref<AvailableLlmModel[]>([])
const settings = ref<DiscussionSettings | null>(null)
const loading = ref(true)
const saving = ref(false)
const error = ref('')
let version = 0
const selection = computed(() => settings.value ?? { providerId: null, modelId: '' })
watch(() => loading.value || saving.value || !settings.value?.available, (value) => emit('busy', value), { immediate: true, flush: 'sync' })

async function load() {
  const current = ++version
  const classroomId = props.classroomId
  loading.value = true
  saving.value = false
  error.value = ''
  settings.value = null
  models.value = []
  try {
    const [config, available] = await Promise.allSettled([fetchDiscussionSettings(classroomId), fetchAvailableLlmModels()])
    if (current !== version) return
    if (config.status === 'fulfilled') settings.value = config.value
    if (available.status === 'fulfilled') models.value = available.value
    const failure = config.status === 'rejected' ? config.reason : available.status === 'rejected' ? available.reason : null
    if (failure) error.value = failure instanceof Error ? failure.message : '读取讨论模型失败'
  } catch (cause) {
    if (current === version) error.value = cause instanceof Error ? cause.message : '读取讨论模型失败'
  } finally { if (current === version) loading.value = false }
}
async function select(value: ModelSelection) {
  if (saving.value || loading.value || props.disabled) return
  const current = version
  const classroomId = props.classroomId
  saving.value = true
  error.value = ''
  try {
    const config = await updateDiscussionSettings(classroomId, value)
    if (current === version) settings.value = config
  } catch (cause) {
    if (current === version) error.value = cause instanceof Error ? cause.message : '保存讨论模型失败'
  } finally { if (current === version) saving.value = false }
}
watch(() => props.classroomId, load, { immediate: true })
onBeforeUnmount(() => { version++ })
</script>

<template>
  <div class="shrink-0 border-b border-border px-3 py-2">
    <div class="mb-1 flex items-center justify-between gap-2 text-[11px] text-muted-foreground">
      <span>讨论模型</span>
      <span v-if="loading || saving" role="status" class="flex items-center gap-1"><Loader2 class="size-3 animate-spin" />{{ saving ? '保存中' : '加载中' }}</span>
      <span v-else-if="running">下次发言生效</span>
      <span v-else-if="settings?.source === 'generation'">沿用生成模型</span>
    </div>
    <div class="flex min-w-0 items-center gap-1">
      <ModelPicker class="min-w-0 flex-1" compact :models="models" :selection="selection" :disabled="loading || saving || disabled" @select="select" />
      <button type="button" :disabled="loading || saving || disabled" title="刷新讨论模型" aria-label="刷新讨论模型" class="flex size-8 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-muted disabled:opacity-40" @click="load">
        <RefreshCw class="size-3.5" />
      </button>
    </div>
    <p v-if="error" role="alert" class="mt-1 break-words text-xs text-red-600 dark:text-red-400">{{ error }}</p>
    <p v-else-if="!loading && settings && !settings.available" role="alert" class="mt-1 text-xs text-gold-700 dark:text-gold-300">所选模型不可用，请重新选择。</p>
  </div>
</template>
