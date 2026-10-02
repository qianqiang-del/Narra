<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Check, Copy } from 'lucide-vue-next'
import { toast } from 'vue-sonner'

import { cn } from '@/lib/utils'
import type { Bubble, Participant } from '@/types/classroom'
import MarkdownText from '@/components/shared/MarkdownText.vue'

const props = defineProps<{
  message: Bubble
  participant?: Participant
  streaming?: boolean
}>()

const { t } = useI18n()
const copied = ref(false)
const avatarFailed = ref(false)
const isUser = computed(() => props.message.from === 'user')
const name = computed(() => isUser.value ? t('chat.you') : props.message.name || t('chat.assistant'))

watch(() => props.participant?.avatar, () => { avatarFailed.value = false })
watch(() => props.message.text, () => { copied.value = false })

async function copyMessage() {
  try {
    await navigator.clipboard.writeText(props.message.text)
    copied.value = true
  } catch {
    toast.error(t('chat.copyFailed'))
  }
}
</script>

<template>
  <article :class="cn('group flex min-w-0 gap-2.5', isUser && 'justify-end')" :aria-label="name">
    <div v-if="!isUser" class="mt-0.5 flex size-8 shrink-0 items-center justify-center overflow-hidden rounded-md border border-zinc-200 bg-white text-xs font-semibold text-teal-800 dark:border-zinc-700 dark:bg-zinc-800 dark:text-teal-200" :style="participant?.color ? { borderBottomColor: participant.color, borderBottomWidth: '2px' } : undefined">
      <img v-if="participant?.avatar && !avatarFailed" :src="participant.avatar" alt="" class="size-full object-cover" @error="avatarFailed = true" />
      <span v-else>{{ name.slice(0, 1) }}</span>
    </div>
    <div :class="cn('min-w-0 flex-1', isUser && 'max-w-[88%] flex-none')">
      <div v-if="!isUser" class="mb-1.5 flex min-h-5 flex-wrap items-center gap-1.5">
        <span class="text-xs font-semibold text-zinc-800 dark:text-zinc-100">{{ name }}</span>
        <span v-if="participant?.role" class="border-l border-zinc-300 pl-1.5 text-[10px] text-zinc-500 dark:border-zinc-600 dark:text-zinc-400">{{ participant.role }}</span>
        <span v-if="streaming" class="size-1.5 animate-pulse rounded-full bg-teal-500 motion-reduce:animate-none" :aria-label="t('chat.speaking', { name })" />
      </div>
      <div :class="cn('rounded-md px-3 py-2.5 text-[13px] leading-6', isUser ? 'border border-teal-200 bg-teal-100 text-zinc-900 dark:border-teal-800 dark:bg-teal-950/60 dark:text-zinc-100' : message.from === 'teacher' ? 'border border-teal-100 border-l-2 bg-white text-zinc-800 dark:border-teal-900 dark:bg-zinc-900 dark:text-zinc-100' : 'border border-zinc-200 bg-white text-zinc-700 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-200')" :style="!isUser && participant?.color ? { borderLeftColor: participant.color } : undefined">
        <MarkdownText :source="message.text" />
        <span v-if="streaming" aria-hidden="true" class="ml-1 inline-block h-3.5 w-0.5 animate-pulse bg-teal-500 align-middle motion-reduce:animate-none" />
      </div>
      <div :class="cn('mt-1 flex h-5 items-center', isUser && 'justify-end')">
        <button v-if="message.text && !streaming" type="button" :aria-label="copied ? t('chat.copied') : t('chat.copy')" :title="copied ? t('chat.copied') : t('chat.copy')" class="flex items-center gap-1 rounded p-1 text-[10px] text-zinc-400 transition hover:bg-zinc-100 hover:text-zinc-700 focus-visible:outline-2 focus-visible:outline-teal-500 sm:opacity-0 sm:group-hover:opacity-100 dark:hover:bg-zinc-800 dark:hover:text-zinc-200" @click="copyMessage">
          <Check v-if="copied" class="size-3 text-emerald-500" />
          <Copy v-else class="size-3" />
          <span v-if="copied" role="status">{{ t('chat.copied') }}</span>
        </button>
      </div>
    </div>
  </article>
</template>
