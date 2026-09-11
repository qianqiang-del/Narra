<script setup lang="ts">
/**
 * AgentVoicePill —— 音色选择 pill（文档 §5.5）。
 * v-model 绑定当前音色 id；popover 内支持搜索、分组、试听。
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Check, ChevronDown, Play, Search, Volume2 } from 'lucide-vue-next'

import { VOICE_GROUPS, voiceName } from '@/data/voices'
import { cn } from '@/lib/utils'

const { t } = useI18n()

const model = defineModel<string>({ required: true })
const pillRef = ref<HTMLElement | null>(null)
const popOpen = ref(false)
const keyword = ref('')

const label = computed(() => voiceName(model.value))
const disabled = computed(() => VOICE_GROUPS.every((g) => g.voices.length === 0))

const groups = computed(() => {
  const kw = keyword.value.trim().toLowerCase()
  if (!kw) return VOICE_GROUPS
  return VOICE_GROUPS.map((g) => ({
    label: g.label,
    voices: g.voices.filter(
      (v) => v.name.toLowerCase().includes(kw) || v.id.toLowerCase().includes(kw),
    ),
  })).filter((g) => g.voices.length > 0)
})

const isEmpty = computed(() => groups.value.length === 0)

function pick(id: string) {
  model.value = id
  popOpen.value = false
  keyword.value = ''
}

function onDocMouseDown(e: MouseEvent) {
  if (pillRef.value && !pillRef.value.contains(e.target as Node)) popOpen.value = false
}

onMounted(() => document.addEventListener('mousedown', onDocMouseDown))
onBeforeUnmount(() => document.removeEventListener('mousedown', onDocMouseDown))
</script>

<template>
  <div ref="pillRef" class="relative shrink-0">
    <button
      type="button"
      :class="
        cn(
          'flex h-6 w-[100px] items-center gap-1.5 rounded-full bg-primary/10 px-2.5 text-[11px] text-primary/80 transition-colors hover:bg-primary/20',
          disabled && 'cursor-not-allowed bg-muted/40 text-muted-foreground/30',
        )
      "
      :disabled="disabled"
      @click="popOpen = !popOpen"
    >
      <Volume2 class="size-3 shrink-0" />
      <span class="min-w-0 flex-1 truncate text-left">
        {{ disabled ? t('agentBar.noVoice') : label }}
      </span>
      <ChevronDown class="size-3 shrink-0 opacity-60" />
    </button>

    <div
      v-if="popOpen"
      class="absolute right-0 bottom-full z-50 mb-1 w-80 overflow-hidden rounded-xl border border-border bg-popover shadow-lg sm:w-96"
    >
      <div class="border-b border-border/60 p-2">
        <div class="relative">
          <Search
            class="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground/50"
          />
          <input
            v-model="keyword"
            type="text"
            :placeholder="t('agentBar.searchVoice')"
            class="h-8 w-full rounded-md border border-input pl-8 pr-3 text-sm outline-none focus:ring-1 focus:ring-violet-400/40"
          />
        </div>
      </div>

      <div class="max-h-64 overflow-y-auto p-1">
        <template v-for="g in groups" :key="g.label">
          <div
            class="sticky top-0 z-10 bg-popover/95 px-2 py-1 text-[10px] font-semibold tracking-wider text-muted-foreground/50 uppercase backdrop-blur"
          >
            {{ g.label }}
          </div>
          <button
            v-for="v in g.voices"
            :key="v.id"
            type="button"
            class="group/voice flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left transition-colors hover:bg-muted/60"
            @click="pick(v.id)"
          >
            <span class="min-w-0 flex-1 truncate text-[13px]">{{ v.name }}</span>
            <span class="shrink-0 text-[10px] text-muted-foreground/40">{{ v.lang }}</span>
            <Play
              class="size-3 shrink-0 text-muted-foreground/30 opacity-0 transition-opacity group-hover/voice:opacity-100"
            />
            <Check v-if="model === v.id" class="size-3.5 shrink-0 text-violet-500" />
            <span v-else class="size-3.5 shrink-0" />
          </button>
        </template>

        <div v-if="isEmpty" class="px-2 py-6 text-center text-[13px] text-muted-foreground/50">
          {{ t('agentBar.noVoice') }}
        </div>
      </div>
    </div>
  </div>
</template>
