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
    player.onended = () => emit('audio-state', false)
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
    class="relative z-20 flex shrink-0 flex-col overflow-hidden border-l border-gray-100 bg-white/80 shadow-[-2px_0_24px_rgba(0,0,0,0.02)] backdrop-blur-xl dark:border-gray-800 dark:bg-gray-900/80"
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
                ? 'border-teal-600 text-teal-800 dark:text-teal-300'
                : 'border-transparent text-gray-400 hover:text-gray-600 dark:hover:text-gray-300',
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
              ? 'border-teal-600 text-teal-800 dark:text-teal-300'
              : 'border-transparent text-gray-400 hover:text-gray-600 dark:hover:text-gray-300',
          )"
          @click="emit('update:tab', 'trace')"
        >
          <Activity class="size-3.5" />
          {{ t('chat.trace') }}
          <span v-if="trace.status === 'running'" class="absolute top-2 right-3 size-1.5 animate-ping rounded-full bg-teal-500" />
        </button>
        <button
          type="button"
          :class="
            cn(
              'relative flex h-full flex-1 items-center justify-center gap-1.5 border-b-2 text-[13px] font-medium transition-colors',
              tab === 'chat'
                ? 'border-teal-500 text-teal-700 dark:text-teal-300'
                : 'border-transparent text-gray-400 hover:text-gray-600 dark:hover:text-gray-300',
            )
          "
          @click="emit('update:tab', 'chat')"
        >
          <MessageSquare class="size-3.5" />
          {{ t('chat.chat') }}
          <span
            v-if="hasActiveSession"
            class="absolute top-2 right-3 size-1.5 animate-ping rounded-full bg-amber-400"
          />
        </button>
      </div>

      <button
        type="button"
        :title="t('workspace.newSession')"
        :aria-label="t('workspace.newSession')"
        :disabled="busy"
        class="shrink-0 rounded-md p-1 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-700 disabled:opacity-40 dark:hover:bg-gray-800"
        @click="emit('new-session')"
      >
        <Plus class="size-4" />
      </button>

      <button
        type="button"
        :title="t('workspace.collapseChat')"
        :aria-label="t('workspace.collapseChat')"
        class="shrink-0 rounded-md p-1 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-700 dark:hover:bg-gray-800"
        @click="emit('toggle-collapse')"
      >
        <PanelRightClose class="size-4" />
      </button>
    </div>

    <DiscussionModelSelector v-if="classroomId" v-show="tab === 'chat'" :classroom-id="classroomId" :disabled="sending || closing" :running="running" @busy="onModelBusy" />

    <!-- 笔记 -->
    <div v-if="tab === 'lecture'" class="scrollbar-hide flex-1 space-y-2 overflow-y-auto p-3">
      <button
        v-for="n in notes"
        :key="n.id"
        type="button"
        :disabled="running || sending"
        :class="n.id === activeNoteId ? 'w-full rounded-xl border border-teal-300 bg-teal-50 p-3 text-left ring-2 ring-teal-200 dark:border-teal-700 dark:bg-teal-950/30' : 'w-full rounded-xl border border-gray-100 bg-white p-3 text-left dark:border-gray-800 dark:bg-gray-900'"
        @click="playAudio(n.audioPath, n.body, n.id)"
      >
        <div class="text-[13px] font-semibold text-gray-800 dark:text-gray-100">{{ n.title }}</div>
        <p class="mt-1 text-[12px] leading-relaxed text-gray-500 dark:text-gray-400">{{ n.body }}</p>
      </button>
    </div>

    <!-- 轨迹 -->
    <TraceTimeline v-else-if="tab === 'trace'" :trace="trace" :conversation-id="activeConversationId" />

    <!-- 对话 -->
    <div v-else class="flex min-h-0 flex-1 flex-col bg-[#f8faf9] dark:bg-zinc-950">
      <div v-if="error" role="alert" class="mx-3 mt-3 rounded-md border border-red-200 bg-red-50 p-2.5 text-xs leading-5 text-red-700 dark:border-red-900 dark:bg-red-950/30 dark:text-red-300">
        {{ error }}
        <button type="button" class="ml-2 underline" :disabled="busy" @click="emit('retry')">{{ t('chat.retry') }}</button>
      </div>
      <div v-if="view === 'list'" class="scrollbar-hide min-h-0 flex-1 overflow-y-auto">
        <div class="flex items-center justify-between border-b border-zinc-200 px-4 py-3 dark:border-zinc-800">
          <span class="text-xs font-semibold text-zinc-700 dark:text-zinc-200">{{ t('chat.conversationMessages') }}</span>
          <span class="text-[11px] tabular-nums text-zinc-400">{{ sessions.length }}</span>
        </div>
        <p v-if="loadingConversations" class="py-4 text-center text-xs text-gray-400">{{ t('chat.loadingMessages') }}</p>
        <div v-else-if="sessions.length === 0" class="flex min-h-60 flex-col items-center justify-center p-6 text-center">
          <div class="flex size-10 items-center justify-center rounded-md border border-teal-100 bg-teal-50 dark:border-teal-900 dark:bg-teal-950/30">
            <MessageSquare class="size-4 text-teal-700 dark:text-teal-300" />
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
            'flex w-full min-w-0 items-start gap-2.5 border-b border-zinc-200 px-4 py-3.5 text-left transition-colors dark:border-zinc-800',
            s.active ? 'border-l-2 border-l-teal-700 bg-teal-50/70 pl-3.5 dark:border-l-teal-400 dark:bg-teal-950/20' : 'border-l-2 border-l-transparent hover:bg-white dark:hover:bg-zinc-900',
          )"
          @click="emit('open-session', s.id)"
        >
          <span class="mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-md bg-white text-teal-700 ring-1 ring-zinc-200 dark:bg-zinc-900 dark:text-teal-300 dark:ring-zinc-700">
            <MessageCircle class="size-3.5" />
          </span>
          <span class="min-w-0 flex-1">
            <span class="flex min-w-0 items-start gap-2">
              <span class="min-w-0 flex-1 truncate text-[13px] font-semibold text-zinc-800 dark:text-zinc-100">{{ s.title }}</span>
              <span v-if="s.active" class="mt-1 size-1.5 shrink-0 rounded-full bg-emerald-500" :title="t('chat.currentSession')" />
            </span>
            <span class="mt-1 block truncate text-[11px] text-zinc-500 dark:text-zinc-400">{{ s.preview || t(`chat.sessionType.${s.type}`) }}</span>
          </span>
        </button>
      </div>

      <template v-else>
        <div class="flex shrink-0 items-center gap-2 border-b border-zinc-200 bg-white px-3 py-3 dark:border-zinc-800 dark:bg-zinc-900">
          <button type="button" :aria-label="t('chat.backToList')" :title="t('chat.backToList')" class="shrink-0 rounded-md p-1.5 text-zinc-500 hover:bg-zinc-100 dark:hover:bg-zinc-800" @click="emit('back')"><ArrowLeft class="size-4" /></button>
          <span class="min-w-0 flex-1 truncate text-[13px] font-semibold text-zinc-800 dark:text-zinc-100">{{ activeTitle }}</span>
          <button v-if="activeConversationId" type="button" :disabled="sending || loadingMessages || closing" class="flex shrink-0 items-center gap-1 rounded-md border border-zinc-200 px-2 py-1.5 text-[11px] font-medium text-zinc-600 transition hover:border-rose-200 hover:bg-rose-50 hover:text-rose-700 disabled:opacity-40 dark:border-zinc-700 dark:text-zinc-300 dark:hover:bg-rose-950/30" @click="emit('end-session')"><Square class="size-2.5" />{{ closing ? t('chat.ending') : t('roundtable.stopDiscussion') }}</button>
        </div>
        <div class="flex shrink-0 flex-wrap items-center gap-x-2 gap-y-1 border-b border-zinc-200 px-4 py-2 dark:border-zinc-800" aria-live="polite">
          <span :class="cn('size-1.5 shrink-0 rounded-full', status === 'ready' ? 'bg-zinc-400' : status === 'waiting' ? 'bg-amber-500' : 'bg-emerald-500')" />
          <span class="min-w-0 flex-1 text-[11px] font-medium text-zinc-600 dark:text-zinc-300">{{ statusText }}</span>
          <span v-if="participants?.length" class="flex shrink-0 items-center gap-1 text-[10px] tabular-nums text-zinc-500 dark:text-zinc-400"><Users class="size-3" />{{ t('chat.participantCount', { count: participants.length }) }}</span>
        </div>
        <div ref="messageList" class="scrollbar-hide min-h-0 flex-1 space-y-4 overflow-y-auto px-3 py-4" @scroll="onScroll">
          <div v-if="loadingMessages" class="py-3 text-center text-xs text-gray-400">{{ t('chat.loadingMessages') }}</div>
          <p v-else-if="!messages.length" class="py-8 text-center text-xs text-gray-400">{{ t('chat.startConversation') }}</p>
          <ChatMessage v-for="message in messages" :key="message.id" :message="message" :participant="participantFor(message)" :streaming="streamingIds?.has(message.id) && running" />
          <p v-if="running || sending || thinking" role="status" class="flex items-center gap-2 pl-10 text-xs text-teal-700 dark:text-teal-300"><span class="size-1.5 animate-pulse rounded-full bg-teal-500 motion-reduce:animate-none" />{{ speakingName ? t('chat.speaking', { name: speakingName }) : t('chat.thinking') }}</p>
          <p v-else-if="yourTurn" role="status" class="pl-10 text-xs text-amber-700 dark:text-amber-300">{{ t('roundtable.yourTurnHint') }}</p>
        </div>
        <button v-if="!followMessages" type="button" class="mx-auto mb-2 flex items-center gap-1 rounded-md border border-zinc-200 bg-white px-3 py-1.5 text-xs text-teal-700 shadow-sm dark:border-zinc-700 dark:bg-zinc-900 dark:text-teal-300" @click="scrollToLatest"><ArrowDown class="size-3" />{{ t('chat.latest') }}</button>
        <form class="mx-3 mt-2 flex shrink-0 items-end gap-2 rounded-md border border-zinc-200 bg-white p-2 transition-colors focus-within:border-teal-600 focus-within:ring-2 focus-within:ring-teal-600/10 dark:border-zinc-700 dark:bg-zinc-900 dark:focus-within:border-teal-400" @submit.prevent="send">
          <textarea :value="draft" rows="2" class="max-h-28 min-w-0 flex-1 resize-none bg-transparent px-1 py-1 text-[13px] leading-5 text-zinc-800 outline-none placeholder:text-zinc-400 dark:text-zinc-100" :placeholder="t('chat.inputPlaceholder')" :aria-label="t('chat.inputPlaceholder')" @input="emit('update:draft', ($event.target as HTMLTextAreaElement).value)" @focus="emit('input-activate')" @keydown="onKeydown" />
          <button type="submit" :disabled="busy || modelBusy || !!error || !draft.trim()" :aria-label="t('workspace.send')" :title="t('workspace.send')" class="flex size-8 shrink-0 items-center justify-center rounded-md bg-teal-700 text-white transition-colors hover:bg-teal-800 disabled:opacity-40 dark:bg-teal-600 dark:hover:bg-teal-500"><Send class="size-3.5" /></button>
        </form>
        <p class="px-4 py-2 text-[10px] text-zinc-400">{{ t('chat.keyboardHint') }}</p>
      </template>
    </div>

    <!-- 拖拽手柄 -->
    <div
      class="group absolute top-0 bottom-0 left-0 z-50 w-1.5 cursor-col-resize"
      @mousedown="emit('resize-start', $event)"
    >
      <div
        class="absolute top-1/2 left-0.5 h-8 w-0.5 -translate-y-1/2 rounded-full bg-gray-300 transition-colors group-hover:bg-teal-400"
      />
    </div>
  </div>

  <!-- 折叠后的展开把手 -->
  <button
    v-if="collapsed"
    type="button"
    class="absolute top-4 right-2 z-30 rounded-md bg-white/80 p-1.5 text-gray-400 shadow-sm ring-1 ring-gray-100 backdrop-blur transition-colors hover:text-gray-700 dark:bg-slate-800/80 dark:ring-gray-700"
    @click="emit('toggle-collapse')"
  >
    <PanelRightOpen class="size-4" />
  </button>
</template>
