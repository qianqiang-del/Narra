<script setup lang="ts">
/**
 * Tooltip —— 基于 reka-ui 的轻量封装，对应原 shadcn/ui 的 Tooltip。
 * 用法：
 *   <UiTooltip content="提示文字" side="bottom">
 *     <button>...</button>
 *   </UiTooltip>
 */
import {
  TooltipContent,
  TooltipPortal,
  TooltipProvider,
  TooltipRoot,
  TooltipTrigger,
} from 'reka-ui'

withDefaults(
  defineProps<{
    content?: string
    side?: 'top' | 'right' | 'bottom' | 'left'
    sideOffset?: number
    delay?: number
    disabled?: boolean
  }>(),
  {
    content: '',
    side: 'top',
    sideOffset: 6,
    delay: 300,
    disabled: false,
  },
)
</script>

<template>
  <TooltipProvider v-if="!disabled && content" :delay-duration="delay">
    <TooltipRoot>
      <TooltipTrigger as-child>
        <slot />
      </TooltipTrigger>
      <TooltipPortal>
        <TooltipContent
          :side="side"
          :side-offset="sideOffset"
          class="z-[60] max-w-[240px] rounded-md bg-primary px-2.5 py-1.5 text-xs text-primary-foreground shadow-md select-none"
        >
          {{ content }}
        </TooltipContent>
      </TooltipPortal>
    </TooltipRoot>
  </TooltipProvider>
  <slot v-else />
</template>
