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
    <div v-if="!isUser" class="mt-0.5 flex size-9 shrink-0 items-center justify-center overflow-hidden rounded-full border-2 border-[#e2e5ee] bg-[#fdfdff] text-xs font-semibold text-[#a8873f] shadow-[0_2px_8px_rgba(61,48,32,0.08)] dark:border-[#2a3549] dark:bg-[#1b2436] dark:text-[#e6b184]" :style="participant?.color ? { borderBottomColor: participant.color, borderBottomWidth: '3px' } : undefined">
      <img v-if="participant?.avatar && !avatarFailed" :src="participant.avatar" alt="" class="size-full object-cover" @error="avatarFailed = true" />
      <span v-else>{{ name.slice(0, 1) }}</span>
    </div>
    <div :class="cn('min-w-0 flex-1', isUser && 'max-w-[88%] flex-none')">
      <div v-if="!isUser" class="mb-1.5 flex min-h-5 flex-wrap items-center gap-1.5">
        <span class="text-xs font-semibold text-[#68748a] dark:text-[#e8eefb]">{{ name }}</span>
        <span v-if="participant?.role" class="border-l border-[#e2e5ee] pl-1.5 text-[10px] text-[#98a1b3] dark:border-[#2a3549] dark:text-[#93a0b8]">{{ participant.role }}</span>
        <span v-if="streaming" class="size-1.5 animate-pulse rounded-full bg-gold-500 motion-reduce:animate-none" :aria-label="t('chat.speaking', { name })" />
      </div>
      <div :class="cn('rounded-xl px-3 py-2.5 text-[13px] leading-6 shadow-[0_3px_12px_rgba(61,48,32,0.04)]', isUser ? 'rounded-br-sm border border-[#d9b58f] bg-[#f1dfc5] text-[#68748a] dark:border-[#2a3549] dark:bg-[#1b2436] dark:text-[#f8ead9]' : message.from === 'teacher' ? 'rounded-bl-sm border border-[#e7d7c4] border-l-2 bg-[#fdfdff] text-[#68748a] dark:border-[#2a3549] dark:bg-[#1b2436] dark:text-[#e8eefb]' : 'border border-[#e2e5ee] bg-[#f4f5f9] text-[#68748a] dark:border-[#2a3549] dark:bg-[#1b2436] dark:text-[#ddd0bf]')" :style="!isUser && participant?.color ? { borderLeftColor: participant.color } : undefined">
        <MarkdownText :source="message.text" />
        <span v-if="streaming" aria-hidden="true" class="ml-1 inline-block h-3.5 w-0.5 animate-pulse bg-gold-600 align-middle motion-reduce:animate-none" />
      </div>
      <div :class="cn('mt-1 flex h-5 items-center', isUser && 'justify-end')">
        <button v-if="message.text && !streaming" type="button" :aria-label="copied ? t('chat.copied') : t('chat.copy')" :title="copied ? t('chat.copied') : t('chat.copy')" class="flex items-center gap-1 rounded-lg p-1 text-[10px] text-[#98a1b3] transition hover:bg-[#faf5ec] hover:text-[#8a6f3c] focus-visible:outline-2 focus-visible:outline-gold-500 sm:opacity-0 sm:group-hover:opacity-100 dark:hover:bg-[#1b2436] dark:hover:text-[#93a0b8]" @click="copyMessage">
          <Check v-if="copied" class="size-3 text-emerald-500" />
          <Copy v-else class="size-3" />
          <span v-if="copied" role="status">{{ t('chat.copied') }}</span>
        </button>
      </div>
    </div>
  </article>
</template>
