<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, useId } from 'vue'
import { Bot, ChevronDown, Search } from 'lucide-vue-next'
import type { AvailableLlmModel } from '@/api/llm'
import { findProviderLogo } from '@/data/providers'
import { isSelectedModel, uniqueModels, type ModelSelection } from '@/lib/modelSelection'

const props = withDefaults(defineProps<{
  models: AvailableLlmModel[]
  selection: ModelSelection
  disabled?: boolean
  compact?: boolean
  side?: 'top' | 'bottom'
}>(), { disabled: false, compact: false, side: 'bottom' })
const emit = defineEmits<{ select: [selection: ModelSelection] }>()
const radioName = useId()
const root = ref<HTMLElement | null>(null)
const open = ref(false)
const browsing = ref<number | null>(null)
const keyword = ref('')
const models = computed(() => uniqueModels(props.models))
const providers = computed(() => [...new Map(models.value.map((row) => [row.providerId, row.providerName])).entries()])
const filteredProviders = computed(() => providers.value.filter(([, name]) => name.toLowerCase().includes(keyword.value.toLowerCase().trim())))
/** 供模板直接调用：把服务商名换成原厂 logo 路径，未命中返回空串。 */
const logoOf = (name: string) => findProviderLogo(name)
/** 触发按钮上显示当前选中项的原厂 logo。 */
const currentLogo = computed(() => (selected.value ? findProviderLogo(selected.value.providerName) : ''))
const visibleModels = computed(() => models.value.filter((row) => row.providerId === browsing.value))
const selected = computed(() => models.value.find((row) => isSelectedModel(row, props.selection)))
const label = computed(() => selected.value ? `${selected.value.providerName} / ${selected.value.modelId}` : props.selection.modelId ? `${props.selection.modelId}（不可用）` : '选择模型')

function toggle() {
  if (props.disabled) return
  open.value = !open.value
  if (open.value) {
    keyword.value = ''
    browsing.value = selected.value?.providerId ?? providers.value[0]?.[0] ?? null
  }
}
function choose(row: AvailableLlmModel) {
  if (props.disabled) return
  emit('select', { providerId: row.providerId, modelId: row.modelId })
  open.value = false
}
function outside(event: MouseEvent) {
  if (root.value && !root.value.contains(event.target as Node)) open.value = false
}
function escape(event: KeyboardEvent) {
  if (event.key === 'Escape') open.value = false
}
onMounted(() => {
  document.addEventListener('mousedown', outside)
  document.addEventListener('keydown', escape)
})
onBeforeUnmount(() => {
  document.removeEventListener('mousedown', outside)
  document.removeEventListener('keydown', escape)
})
</script>

<template>
  <div ref="root" class="relative min-w-0">
    <button type="button" data-testid="model-picker-trigger" :disabled="disabled" :aria-expanded="open" :title="label"
      class="flex h-8 w-full min-w-0 items-center gap-1.5 rounded-md border border-brand-200/70 bg-brand-50 px-2 text-xs text-brand-800 hover:bg-brand-100 disabled:cursor-wait disabled:opacity-50 dark:border-brand-800 dark:bg-brand-950 dark:text-brand-200"
      @click="toggle">
      <img v-if="currentLogo" :src="currentLogo" :alt="selected?.providerName" class="size-3.5 shrink-0 rounded-[3px] object-contain" /><Bot v-else class="size-3.5 shrink-0" /><span class="min-w-0 flex-1 truncate text-left">{{ label }}</span><ChevronDown class="size-3 shrink-0" />
    </button>
    <div v-if="open && !disabled" class="z-50 max-w-[calc(100vw-2rem)] overflow-hidden rounded-md border border-border bg-popover p-2 shadow-lg"
      :class="compact ? 'relative mt-2 w-full' : ['absolute left-0 w-[480px]', side === 'top' ? 'bottom-full mb-2' : 'top-full mt-2']">
      <div class="relative mb-2">
        <Search class="pointer-events-none absolute left-2 top-2 size-3.5 text-muted-foreground" />
        <input v-model="keyword" type="search" aria-label="搜索服务商" placeholder="搜索服务商" class="h-8 w-full rounded border border-input bg-transparent pl-7 pr-2 text-xs" />
      </div>
      <div class="grid grid-cols-[minmax(64px,1fr)_minmax(0,2fr)] gap-2" :class="compact ? 'h-[min(256px,25dvh)]' : 'h-64'">
        <div class="overflow-y-auto border-r border-border pr-2">
          <button v-for="[id, name] in filteredProviders" :key="id" :data-provider-id="id" type="button" :title="name"
            class="mb-1 flex w-full items-center gap-2 rounded px-2 py-2 text-left text-xs hover:bg-muted" :class="browsing === id ? 'bg-brand-50 text-brand-800 dark:bg-brand-950 dark:text-brand-200' : ''"
            @click="browsing = id">
            <img v-if="logoOf(name)" :src="logoOf(name)" :alt="name" class="size-4 shrink-0 rounded-[3px] object-contain" />
            <Bot v-else class="size-4 shrink-0 text-muted-foreground" />
            <span class="min-w-0 flex-1 truncate">{{ name }}</span>
          </button>
        </div>
        <div class="overflow-y-auto">
          <label v-for="row in visibleModels" :key="row.modelId" class="flex cursor-pointer items-center gap-2 rounded px-2 py-2 text-xs hover:bg-muted" :title="row.modelId">
            <input type="radio" :name="radioName" :checked="isSelectedModel(row, selection)" :disabled="disabled" class="shrink-0 accent-brand-800" @change="choose(row)" />
            <span class="min-w-0 break-all">{{ row.modelId }}</span>
          </label>
          <p v-if="!visibleModels.length" class="p-2 text-xs text-muted-foreground">暂无可用模型</p>
        </div>
      </div>
    </div>
  </div>
</template>
