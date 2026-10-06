<script setup lang="ts">
/**
 * ChatArea —— 文档 §6.8（右侧聊天面板，默认 340 / 240~560，可折叠）。
 * Tabs：笔记（lecture）/ 对话（chat）。对话支持列表、历史详情和实时消息。
 */
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Activity, ArrowDown, ArrowLeft, BookOpen, MessageCircle, MessageSquare, PanelRightClose, PanelRightOpen, Plus, Send, Square, Users } from 'lucide-vue-next'
import ChatMessage from './ChatMessage.vue'
import TraceTimeline from './TraceTimeline.vue'
import DiscussionModelSelector from './DiscussionModelSelector.vue'

import { cn } from '@/lib/utils'
import { registerAudio } from '@/lib/audioPlayback'
import { discussionStatus } from '@/lib/discussionAppearance'
import type { DiscussionTrace } from '@/lib/discussionTrace'
import type { Bubble, ChatNote, ChatSession, Participant } from '@/types/classroom'

const props = defineProps<{
  classroomId?: number
  collapsed: boolean
  width: number
  tab: 'lecture' | 'trace' | 'chat'
  sessions: ChatSession[]
  hasActiveSession: boolean
  notes: ChatNote[]
  activeNoteId?: string | null
  messages: Bubble[]
  loadingMessages?: boolean
  activeConversationId?: number | null
  view: 'list' | 'conversation'
  busy: boolean
  running: boolean
  sending: boolean
  loadingConversations: boolean
  error: string
  draft: string
  thinking: boolean
  yourTurn: boolean
  speakingName?: string
  participants?: Participant[]
  streamingIds?: Set<string>
  closing?: boolean
  trace: DiscussionTrace
}>()

const emit = defineEmits<{
  (e: 'model-busy', value: boolean): void
  (e: 'update:tab', v: 'lecture' | 'trace' | 'chat'): void
  (e: 'toggle-collapse'): void
  (e: 'resize-start', ev: MouseEvent): void
  (e: 'open-session', id: string): void
  (e: 'new-session'): void
  (e: 'audio-state', playing: boolean): void
  (e: 'audio-ended'): void
  (e: 'audio-caption', payload: { id: string; text: string }): void
  (e: 'send', text: string): void
  (e: 'update:draft', text: string): void
  (e: 'back'): void
  (e: 'retry'): void
  (e: 'input-activate'): void
  (e: 'end-session'): void
}>()

const { t } = useI18n()
const messageList = ref<HTMLElement | null>(null)
const followMessages = ref(true)
const modelBusy = ref(!!props.classroomId)
function onModelBusy(value: boolean) {
  modelBusy.value = value
  emit('model-busy', value)
}
const activeTitle = computed(() => props.sessions.find((item) => item.active)?.title || t('workspace.newSession'))
const status = computed(() => discussionStatus(props))
const statusText = computed(() => status.value === 'speaking'
  ? t('chat.speaking', { name: props.speakingName })
  : t(`chat.status.${status.value}`))
watch(() => props.messages, async () => {
  if (!followMessages.value) return
  await nextTick()
  messageList.value?.scrollTo({ top: messageList.value.scrollHeight })
}, { deep: true })
watch(() => [props.activeConversationId, props.view], async () => {
  followMessages.value = true
  await nextTick()
  messageList.value?.scrollTo({ top: messageList.value.scrollHeight })
})
function onScroll() {
  const element = messageList.value
  if (element) followMessages.value = element.scrollHeight - element.scrollTop - element.clientHeight < 48
}
function scrollToLatest() {
  followMessages.value = true
  messageList.value?.scrollTo({ top: messageList.value.scrollHeight })
}
function participantFor(message: Bubble) {
  return props.participants?.find((participant) => message.agentKey ? participant.id === message.agentKey : participant.name === message.name)
}
function send() {
  const text = props.draft.trim()
  if (!text || props.busy || modelBusy.value || props.error) return
  followMessages.value = true
  emit('send', text)
}
function onKeydown(event: KeyboardEvent) {
  if (event.key !== 'Enter' || event.shiftKey || event.isComposing) return
  event.preventDefault()
  send()
}
function playAudio(path?: string | null, text?: string, id?: string) {
  if (props.running || props.sending) return
  const normalized = path?.trim().replaceAll('\\', '/')
  if (normalized) {
    const player = new Audio(`/audio/${normalized}`)
    registerAudio(player)
    if (text && id) emit('audio-caption', { id, text })
    player.onended = () => {
      emit('audio-state', false)
      emit('audio-ended')
    }
    player.onerror = () => emit('audio-state', false)
    void player.play().then(() => {
      if (props.running || props.sending) { player.pause(); return }
      emit('audio-state', true)
    }).catch(() => emit('audio-state', false))
  }
}

</script>

<template>
  <div
    data-testid="classroom-chat"
    class="discussion-panel relative z-20 flex shrink-0 flex-col overflow-hidden border-l border-[#e2e5ee] bg-[#f4f5f9]/95 shadow-[-10px_0_34px_rgba(61,48,32,0.08)] backdrop-blur-xl dark:border-[#2a3549] dark:bg-[#151d2e]/95"
    :style="{
      width: collapsed ? '0px' : `${width}px`,
      transition: 'width 0.3s ease',
    }"
  >
    <!-- Tabs 头部 -->
    <div class="mt-3 mb-1 flex h-10 shrink-0 items-center gap-1 px-3">
      <div class="flex h-full w-0 flex-1 items-center gap-1">
        <button
          type="button"
          :class="
            cn(
              'relative flex h-full flex-1 items-center justify-center gap-1.5 border-b-2 text-[13px] font-medium transition-colors',
              tab === 'lecture'
                ? 'border-gold-700 text-[#8a6f3c] dark:border-gold-500 dark:text-gold-300'
                : 'border-transparent text-[#98a1b3] hover:text-[#8a6f3c] dark:hover:text-gold-200',
            )
          "
          @click="emit('update:tab', 'lecture')"
        >
          <BookOpen class="size-3.5" />
          {{ t('chat.notes') }}
        </button>
        <button
          type="button"
          :class="cn(
            'relative flex h-full flex-1 items-center justify-center gap-1.5 border-b-2 text-[13px] font-medium transition-colors',
            tab === 'trace'
              ? 'border-gold-700 text-[#8a6f3c] dark:border-gold-500 dark:text-gold-300'
              : 'border-transparent text-[#98a1b3] hover:text-[#8a6f3c] dark:hover:text-gold-200',
          )"
          @click="emit('update:tab', 'trace')"
        >
          <Activity class="size-3.5" />
          {{ t('chat.trace') }}
          <span v-if="trace.status === 'running'" class="absolute top-2 right-3 size-1.5 animate-ping rounded-full bg-gold-500" />
        </button>
        <button
          type="button"
          :class="
            cn(
              'relative flex h-full flex-1 items-center justify-center gap-1.5 border-b-2 text-[13px] font-medium transition-colors',
              tab === 'chat'
                ? 'border-gold-700 text-[#8a6f3c] dark:border-gold-500 dark:text-gold-300'
                : 'border-transparent text-[#98a1b3] hover:text-[#8a6f3c] dark:hover:text-gold-200',
            )
          "
          @click="emit('update:tab', 'chat')"
        >
          <MessageSquare class="size-3.5" />
          {{ t('chat.chat') }}
          <span
            v-if="hasActiveSession"
            class="absolute top-2 right-3 size-1.5 animate-ping rounded-full bg-gold-400"
          />
        </button>
      </div>

      <button
        type="button"
        :title="t('workspace.newSession')"
        :aria-label="t('workspace.newSession')"
        :disabled="busy"
        class="shrink-0 rounded-md p-1 text-[#98a1b3] transition-colors hover:bg-[#faf5ec] hover:text-[#8a6f3c] disabled:opacity-40 dark:hover:bg-[#151d2e]"
        @click="emit('new-session')"
      >
        <Plus class="size-4" />
      </button>

      <button
        type="button"
        :title="t('workspace.collapseChat')"
        :aria-label="t('workspace.collapseChat')"
        class="shrink-0 rounded-md p-1 text-[#98a1b3] transition-colors hover:bg-[#faf5ec] hover:text-[#8a6f3c] dark:hover:bg-[#151d2e]"
        @click="emit('toggle-collapse')"
      >
        <PanelRightClose class="size-4" />
      </button>
    </div>

    <DiscussionModelSelector v-if="classroomId" v-show="tab === 'chat'" :classroom-id="classroomId" :disabled="sending || closing" :running="running" @busy="onModelBusy" />

    <!-- 笔记 -->
    <div v-if="tab === 'lecture'" class="discussion-notes scrollbar-hide flex-1 space-y-2 overflow-y-auto p-3">
      <button
        v-for="n in notes"
        :key="n.id"
        type="button"
        :disabled="running || sending"
        :class="n.id === activeNoteId ? 'w-full rounded-xl border border-gold-300 bg-[#faf5ec] p-3 text-left ring-2 ring-gold-200/70 dark:border-gold-700 dark:bg-gold-900/30' : 'w-full rounded-xl border border-[#e2e5ee] bg-[#fdfdff] p-3 text-left dark:border-[#2a3549] dark:bg-[#1b2436]'"
        @click="playAudio(n.audioPath, n.body, n.id)"
      >
        <div class="text-[13px] font-semibold text-[#68748a] dark:text-[#e8eefb]">{{ n.title }}</div>
        <p class="mt-1 text-[12px] leading-relaxed text-[#98a1b3] dark:text-[#93a0b8]">{{ n.body }}</p>
      </button>
    </div>

    <!-- 轨迹 -->
    <TraceTimeline v-else-if="tab === 'trace'" :trace="trace" :conversation-id="activeConversationId" />

    <!-- 对话 -->
    <div v-else class="discussion-chat flex min-h-0 flex-1 flex-col bg-[#eef0f6] dark:bg-[#0f1626]">
      <div v-if="error" role="alert" class="mx-3 mt-3 rounded-lg border border-rose-200 bg-rose-50/90 p-2.5 text-xs leading-5 text-rose-800 dark:border-rose-900 dark:bg-rose-950/30 dark:text-rose-300">
        {{ error }}
        <button type="button" class="ml-2 underline" :disabled="busy" @click="emit('retry')">{{ t('chat.retry') }}</button>
      </div>
      <div v-if="view === 'list'" class="scrollbar-hide min-h-0 flex-1 overflow-y-auto">
        <div class="flex items-center justify-between border-b border-[#e2e5ee] px-4 py-3 dark:border-[#2a3549]">
          <span class="text-xs font-semibold tracking-wide text-[#2b3340] dark:text-[#e8eefb]">{{ t('chat.conversationMessages') }}</span>
          <span class="rounded-full bg-[#e2e5ee] px-2 py-0.5 text-[10px] tabular-nums text-[#68748a] dark:bg-[#151d2e] dark:text-[#c8b9a5]">{{ sessions.length }}</span>
        </div>
        <p v-if="loadingConversations" class="py-4 text-center text-xs text-gray-400">{{ t('chat.loadingMessages') }}</p>
        <div v-else-if="sessions.length === 0" class="flex min-h-60 flex-col items-center justify-center p-6 text-center">
          <div class="flex size-10 items-center justify-center rounded-xl border border-gold-200 bg-gold-50 dark:border-gold-900 dark:bg-gold-900/30">
            <MessageSquare class="size-4 text-gold-700 dark:text-gold-300" />
          </div>
          <p class="mt-3 text-[13px] text-gray-500 dark:text-gray-400">{{ t('chat.noConversations') }}</p>
          <p class="mt-1 text-[12px] text-gray-400">{{ t('chat.startConversation') }}</p>
        </div>

        <button
          v-for="s in sessions"
          :key="s.id"
          type="button"
          :disabled="busy && !s.active"
          :class="cn(
            'flex w-full min-w-0 items-start gap-2.5 border-b border-[#e2e5ee] px-4 py-3.5 text-left transition-colors dark:border-[#2a3549]',
            s.active ? 'border-l-2 border-l-gold-700 bg-[#faf5ec] pl-3.5 dark:border-l-gold-400 dark:bg-gold-900/20' : 'border-l-2 border-l-transparent hover:bg-[#fdfdff] dark:hover:bg-[#1b2436]',
          )"
          @click="emit('open-session', s.id)"
        >
          <span class="mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-lg bg-[#fdfdff] text-gold-700 ring-1 ring-[#cdd3e0] dark:bg-[#1b2436] dark:text-gold-300 dark:ring-[#1b2436]">
            <MessageCircle class="size-3.5" />
          </span>
          <span class="min-w-0 flex-1">
            <span class="flex min-w-0 items-start gap-2">
              <span class="min-w-0 flex-1 truncate text-[13px] font-semibold text-[#68748a] dark:text-[#e8eefb]">{{ s.title }}</span>
              <span v-if="s.active" class="mt-1 size-1.5 shrink-0 rounded-full bg-emerald-500" :title="t('chat.currentSession')" />
            </span>
            <span class="mt-1 block truncate text-[11px] text-[#98a1b3] dark:text-[#93a0b8]">{{ s.preview || t(`chat.sessionType.${s.type}`) }}</span>
          </span>
        </button>
      </div>

      <template v-else>
        <div class="flex shrink-0 items-center gap-2 border-b border-[#e2e5ee] bg-[#fdfdff] px-3 py-3 dark:border-[#2a3549] dark:bg-[#1b2436]">
          <button type="button" :aria-label="t('chat.backToList')" :title="t('chat.backToList')" class="shrink-0 rounded-lg p-1.5 text-[#98a1b3] hover:bg-[#faf5ec] dark:hover:bg-[#1b2436]" @click="emit('back')"><ArrowLeft class="size-4" /></button>
          <span class="min-w-0 flex-1 truncate text-[13px] font-semibold text-[#68748a] dark:text-[#e8eefb]">{{ activeTitle }}</span>
          <button v-if="activeConversationId" type="button" :disabled="sending || loadingMessages || closing" class="flex shrink-0 items-center gap-1 rounded-lg border border-[#dfc9bd] px-2 py-1.5 text-[11px] font-medium text-[#8f4d40] transition hover:border-rose-300 hover:bg-rose-50 hover:text-rose-700 disabled:opacity-40 dark:border-[#2a3549] dark:text-[#e6a99e] dark:hover:bg-rose-950/30" @click="emit('end-session')"><Square class="size-2.5" />{{ closing ? t('chat.ending') : t('roundtable.stopDiscussion') }}</button>
        </div>
        <div class="flex shrink-0 flex-wrap items-center gap-x-2 gap-y-1 border-b border-[#e2e5ee] bg-[#f4f5f9] px-4 py-2 dark:border-[#2a3549] dark:bg-[#151d2e]" aria-live="polite">
          <span :class="cn('size-1.5 shrink-0 rounded-full', status === 'ready' ? 'bg-zinc-400' : status === 'waiting' ? 'bg-gold-500' : 'bg-emerald-500')" />
          <span class="min-w-0 flex-1 text-[11px] font-medium text-[#68748a] dark:text-[#d2c2af]">{{ statusText }}</span>
          <span v-if="participants?.length" class="flex shrink-0 items-center gap-1 text-[10px] tabular-nums text-[#98a1b3] dark:text-[#93a0b8]"><Users class="size-3" />{{ t('chat.participantCount', { count: participants.length }) }}</span>
        </div>
        <div ref="messageList" class="scrollbar-hide min-h-0 flex-1 space-y-4 overflow-y-auto px-3 py-4" @scroll="onScroll">
          <div v-if="loadingMessages" class="py-3 text-center text-xs text-gray-400">{{ t('chat.loadingMessages') }}</div>
          <p v-else-if="!messages.length" class="py-8 text-center text-xs text-gray-400">{{ t('chat.startConversation') }}</p>
          <ChatMessage v-for="message in messages" :key="message.id" :message="message" :participant="participantFor(message)" :streaming="streamingIds?.has(message.id) && running" />
          <p v-if="running || sending || thinking" role="status" class="flex items-center gap-2 pl-10 text-xs text-gold-800 dark:text-gold-300"><span class="size-1.5 animate-pulse rounded-full bg-gold-500 motion-reduce:animate-none" />{{ speakingName ? t('chat.speaking', { name: speakingName }) : t('chat.thinking') }}</p>
          <p v-else-if="yourTurn" role="status" class="pl-10 text-xs text-gold-700 dark:text-gold-300">{{ t('roundtable.yourTurnHint') }}</p>
        </div>
        <button v-if="!followMessages" type="button" class="mx-auto mb-2 flex items-center gap-1 rounded-lg border border-[#e2e5ee] bg-[#fdfdff] px-3 py-1.5 text-xs text-gold-800 shadow-sm dark:border-[#2a3549] dark:bg-[#1b2436] dark:text-gold-300" @click="scrollToLatest"><ArrowDown class="size-3" />{{ t('chat.latest') }}</button>
        <form class="mx-3 mt-2 flex shrink-0 items-end gap-2 rounded-xl border border-[#e2e5ee] bg-[#fdfdff] p-2 shadow-[0_6px_18px_rgba(61,48,32,0.05)] transition-colors focus-within:border-gold-600 focus-within:ring-2 focus-within:ring-gold-600/10 dark:border-[#2a3549] dark:bg-[#1b2436] dark:focus-within:border-gold-400" @submit.prevent="send">
          <textarea :value="draft" rows="2" class="max-h-28 min-w-0 flex-1 resize-none bg-transparent px-1 py-1 text-[13px] leading-5 text-[#68748a] outline-none placeholder:text-[#a99b8a] dark:text-[#e8eefb]" :placeholder="t('chat.inputPlaceholder')" :aria-label="t('chat.inputPlaceholder')" @input="emit('update:draft', ($event.target as HTMLTextAreaElement).value)" @focus="emit('input-activate')" @keydown="onKeydown" />
          <button type="submit" :disabled="busy || modelBusy || !!error || !draft.trim()" :aria-label="t('workspace.send')" :title="t('workspace.send')" class="flex size-8 shrink-0 items-center justify-center rounded-lg bg-[#8f5b38] text-white transition-colors hover:bg-[#2a3549] disabled:opacity-40 dark:bg-[#b8794b] dark:hover:bg-[#d39461]"><Send class="size-3.5" /></button>
        </form>
        <p class="px-4 py-2 text-[10px] text-[#a99b8a]">{{ t('chat.keyboardHint') }}</p>
      </template>
    </div>

    <!-- 拖拽手柄 -->
    <div
      class="group absolute top-0 bottom-0 left-0 z-50 w-1.5 cursor-col-resize"
      @mousedown="emit('resize-start', $event)"
    >
      <div
        class="absolute top-1/2 left-0.5 h-8 w-0.5 -translate-y-1/2 rounded-full bg-[#b9c8e3] transition-colors group-hover:bg-gold-500"
      />
    </div>
  </div>

  <!-- 折叠后的展开把手 -->
  <button
    v-if="collapsed"
    type="button"
    class="absolute top-4 right-2 z-30 rounded-lg bg-[#fdfdff]/90 p-1.5 text-[#98a1b3] shadow-sm ring-1 ring-[#cdd3e0] backdrop-blur transition-colors hover:text-[#8a6f3c] dark:bg-[#1b2436]/90 dark:ring-[#1b2436]"
    @click="emit('toggle-collapse')"
  >
    <PanelRightOpen class="size-4" />
  </button>
</template>
