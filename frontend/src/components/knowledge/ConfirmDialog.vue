<script setup lang="ts">
/**
 * 删除确认框（主页删知识库、抽屉删上传记录共用）。
 *
 * 用 reka-ui 的 Dialog 而不是 `window.confirm`：原生确认框里的文案没法排版，
 * 也没法区分"这条记录会连带清掉什么"这种需要两行说明的场景。
 * z-index 取在所有知识库弹层之上 —— 它可能从抽屉里被唤起。
 */
import { DialogContent, DialogDescription, DialogOverlay, DialogPortal, DialogRoot, DialogTitle } from 'reka-ui'
import { Loader2 } from 'lucide-vue-next'
import { useI18n } from 'vue-i18n'

const open = defineModel<boolean>('open', { default: false })

const props = defineProps<{
  title: string
  message: string
  /** 第二行小字：说明这次删除会连带影响什么 */
  note?: string
  confirmText?: string
  /** 确认中：按钮转圈并禁用，避免连点两次 */
  pending?: boolean
}>()

const emit = defineEmits<{ confirm: [] }>()

const { t } = useI18n()

function onConfirm() {
  if (props.pending) return
  emit('confirm')
}
</script>

<template>
  <DialogRoot v-model:open="open">
    <DialogPortal>
      <DialogOverlay class="fixed inset-0 z-[110] bg-black/40 backdrop-blur-[2px]" />
      <DialogContent
        class="fixed top-1/2 left-1/2 z-[111] w-[min(420px,calc(100vw-2rem))] -translate-x-1/2 -translate-y-1/2 rounded-2xl border border-border bg-background p-5 shadow-2xl focus:outline-none"
      >
        <DialogTitle class="text-[15px] font-medium">{{ title }}</DialogTitle>
        <DialogDescription class="mt-2 text-[13px] leading-6 break-words text-zinc-600 dark:text-zinc-400">
          {{ message }}
        </DialogDescription>
        <p v-if="note" class="mt-2 text-xs leading-5 text-zinc-600 dark:text-zinc-400">
          {{ note }}
        </p>

        <div class="mt-5 flex justify-end gap-2">
          <button
            type="button"
            class="cursor-pointer rounded-lg border border-border px-3.5 py-1.5 text-[13px] font-medium transition-colors hover:bg-muted"
            @click="open = false"
          >
            {{ t('common.cancel') }}
          </button>
          <button
            type="button"
            :disabled="pending"
            class="flex cursor-pointer items-center gap-1.5 rounded-lg bg-destructive px-3.5 py-1.5 text-[13px] font-medium text-destructive-foreground transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-60"
            @click="onConfirm"
          >
            <Loader2 v-if="pending" class="size-3.5 animate-spin" />
            {{ confirmText ?? t('knowledge.action.delete') }}
          </button>
        </div>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
