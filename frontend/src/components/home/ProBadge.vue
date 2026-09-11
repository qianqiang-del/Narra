<script setup lang="ts">
/**
 * ProBadge —— 文档 §5.3 的 "Pro" 药丸开关。
 */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { cn } from '@/lib/utils'

const props = withDefaults(
  defineProps<{
    active?: boolean
    interactive?: boolean
  }>(),
  { active: false, interactive: true },
)

const emit = defineEmits<{ (e: 'toggle', next: boolean): void }>()

const { t } = useI18n()

const label = computed(() => t('proMode.badge'))

function onClick() {
  if (!props.interactive) return
  emit('toggle', !props.active)
}
</script>

<template>
  <button
    type="button"
    :role="interactive ? 'switch' : undefined"
    :aria-checked="interactive ? active : undefined"
    :aria-label="label"
    :class="
      cn(
        'relative inline-flex items-center rounded-full border px-[7px] py-[2px] text-[9.5px] font-semibold tracking-[0.2em] uppercase leading-[1.35] transition-[color,background-color,border-color,box-shadow] duration-300 select-none',
        interactive && 'cursor-pointer active:scale-[0.94]',
        active
          ? 'border-violet-400/70 bg-violet-500/10 text-violet-600 dark:text-violet-300'
          : 'border-border bg-background/70 text-muted-foreground hover:border-violet-400/70 hover:bg-violet-50/60 hover:text-violet-600 dark:hover:bg-violet-500/10',
      )
    "
    @click="onClick"
  >
    Pro
  </button>
</template>
