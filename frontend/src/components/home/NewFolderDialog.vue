<script setup lang="ts">
/**
 * NewFolderDialog —— 文档 §5.10。
 * 标题「新建文件夹」，描述、输入框（maxLength 80）、取消 / 创建。
 */
import { nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { DialogContent, DialogDescription, DialogOverlay, DialogPortal, DialogRoot, DialogTitle } from 'reka-ui'

const open = defineModel<boolean>('open', { default: false })
const emit = defineEmits<{ (e: 'create', name: string): void }>()

const { t } = useI18n()
const name = ref('')
const inputRef = ref<HTMLInputElement | null>(null)

watch(open, (v) => {
  if (v) {
    name.value = ''
    nextTick(() => inputRef.value?.focus())
  }
})

function submit() {
  const next = name.value.trim()
  if (!next) return
  emit('create', next.slice(0, 80))
  open.value = false
}
</script>

<template>
  <DialogRoot v-model:open="open">
    <DialogPortal>
      <DialogOverlay class="fixed inset-0 z-50 bg-black/50 backdrop-blur-sm data-[state=open]:animate-in data-[state=open]:fade-in-0" />
      <DialogContent
        class="fixed top-1/2 left-1/2 z-50 w-full max-w-[calc(100vw-2rem)] -translate-x-1/2 -translate-y-1/2 rounded-2xl border border-border bg-background p-5 shadow-xl data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=open]:zoom-in-95 sm:max-w-[400px]"
      >
        <DialogTitle class="text-base font-semibold">{{ t('home.newFolder') }}</DialogTitle>
        <DialogDescription class="mt-1 text-[13px] text-muted-foreground">
          {{ t('home.newFolderDesc') }}
        </DialogDescription>

        <input
          ref="inputRef"
          v-model="name"
          type="text"
          maxlength="80"
          :placeholder="t('home.folderNamePlaceholder')"
          class="mt-1.5 w-full rounded-lg border border-border bg-background px-3 py-2 text-[14px] outline-none focus:border-violet-400 focus:ring-2 focus:ring-violet-400/40"
          @keydown.enter.prevent="submit"
        />

        <div class="mt-4 flex justify-end gap-2">
          <button
            type="button"
            class="rounded-lg px-3.5 py-1.5 text-[13px] font-medium text-muted-foreground transition-colors hover:bg-muted"
            @click="open = false"
          >
            {{ t('common.cancel') }}
          </button>
          <button
            type="button"
            class="rounded-lg bg-primary px-3.5 py-1.5 text-[13px] font-medium text-primary-foreground transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-50"
            :disabled="!name.trim()"
            @click="submit"
          >
            {{ t('home.create') }}
          </button>
        </div>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
