<script setup lang="ts">
/**
 * 设置弹窗 —— 文档 §5.10。
 * 原项目是 Radix Dialog + 多个分区（providers / theme / …）。
 * 这里先用 reka-ui Dialog 落一个基础外壳，分区内容后续补。
 */
import { DialogContent, DialogDescription, DialogOverlay, DialogPortal, DialogRoot, DialogTitle } from 'reka-ui'
import { X } from 'lucide-vue-next'
import { useI18n } from 'vue-i18n'
import { Monitor, Moon, Sun } from 'lucide-vue-next'

import { useTheme, type ThemeMode } from '@/composables/useTheme'
import { cn } from '@/lib/utils'

const open = defineModel<boolean>('open', { default: false })

const { t } = useI18n()
const { mode, setMode } = useTheme()

const themeOptions: { value: ThemeMode; labelKey: string; icon: typeof Sun }[] = [
  { value: 'light', labelKey: 'settings.light', icon: Sun },
  { value: 'dark', labelKey: 'settings.dark', icon: Moon },
  { value: 'system', labelKey: 'settings.system', icon: Monitor },
]
</script>

<template>
  <DialogRoot v-model:open="open">
    <DialogPortal>
      <DialogOverlay class="fixed inset-0 z-[100] bg-black/40 backdrop-blur-[2px]" />
      <DialogContent
        class="fixed top-1/2 left-1/2 z-[101] w-[calc(100vw-2rem)] max-w-[440px] -translate-x-1/2 -translate-y-1/2 rounded-2xl border border-border bg-background p-6 shadow-2xl focus:outline-none"
      >
        <div class="mb-5 flex items-start justify-between">
          <div>
            <DialogTitle class="text-lg font-semibold tracking-tight">
              {{ t('settings.title') }}
            </DialogTitle>
            <DialogDescription class="mt-1 text-[13px] text-muted-foreground">
              {{ t('settings.theme') }}
            </DialogDescription>
          </div>
          <button
            type="button"
            class="rounded-full p-1.5 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
            @click="open = false"
          >
            <X class="size-4" />
          </button>
        </div>

        <!-- 主题 -->
        <div class="space-y-2">
          <div class="text-[13px] font-medium text-muted-foreground">
            {{ t('settings.theme') }}
          </div>
          <div class="flex gap-2">
            <button
              v-for="opt in themeOptions"
              :key="opt.value"
              type="button"
              :class="
                cn(
                  'flex flex-1 items-center justify-center gap-1.5 rounded-lg border px-3 py-2 text-[13px] transition-all',
                  mode === opt.value
                    ? 'border-violet-300 bg-violet-50 text-violet-700 dark:border-violet-600 dark:bg-violet-500/10 dark:text-violet-300'
                    : 'border-border text-muted-foreground hover:bg-muted',
                )
              "
              @click="setMode(opt.value)"
            >
              <component :is="opt.icon" class="size-3.5" />
              {{ t(opt.labelKey) }}
            </button>
          </div>
        </div>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
