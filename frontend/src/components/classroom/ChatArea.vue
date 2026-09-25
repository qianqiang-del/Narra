<script setup lang="ts">
/**
 * ChatArea —— 文档 §6.8（右侧聊天面板，默认 340 / 240~560，可折叠）。
 * Tabs：笔记（lecture）/ 对话（chat）。空态 + 会话卡。
 */
import { useI18n } from 'vue-i18n'
import { BookOpen, MessageSquare, PanelRightClose, PanelRightOpen } from 'lucide-vue-next'

import { cn } from '@/lib/utils'
import { registerAudio } from '@/lib/audioPlayback'
import type { ChatNote, ChatSession } from '@/types/classroom'

defineProps<{
  collapsed: boolean
  width: number
  tab: 'lecture' | 'chat'
  sessions: ChatSession[]
  hasActiveSession: boolean
  notes: ChatNote[]
  activeNoteId?: string | null
}>()

const emit = defineEmits<{
  (e: 'update:tab', v: 'lecture' | 'chat'): void
  (e: 'toggle-collapse'): void
  (e: 'resize-start', ev: MouseEvent): void
  (e: 'open-session', id: string): void
  (e: 'audio-state', playing: boolean): void
  (e: 'audio-caption', payload: { id: string; text: string }): void
}>()

const { t } = useI18n()
function playAudio(path?: string | null, text?: string, id?: string) {
  const normalized = path?.trim().replaceAll('\\', '/')
  if (normalized) {
    const player = new Audio(`/audio/${normalized}`)
    registerAudio(player)
    if (text && id) emit('audio-caption', { id, text })
    player.onended = () => emit('audio-state', false)
    player.onerror = () => emit('audio-state', false)
    void player.play().then(() => emit('audio-state', true)).catch(() => emit('audio-state', false))
  }
}

const TYPE_BADGE: Record<ChatSession['type'], string> = {
  qa: 'bg-blue-100 text-blue-600 dark:bg-blue-900/30 dark:text-blue-300',
  discussion: 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300',
  lecture: 'bg-purple-100 text-purple-600 dark:bg-purple-900/30 dark:text-purple-300',
}
</script>

<template>
  <div
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
                ? 'border-violet-500 text-violet-700 dark:text-violet-300'
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
          :class="
            cn(
              'relative flex h-full flex-1 items-center justify-center gap-1.5 border-b-2 text-[13px] font-medium transition-colors',
              tab === 'chat'
                ? 'border-violet-500 text-violet-700 dark:text-violet-300'
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
        class="shrink-0 rounded-md p-1 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-700 dark:hover:bg-gray-800"
        @click="emit('toggle-collapse')"
      >
        <PanelRightClose class="size-4" />
      </button>
    </div>

    <!-- 笔记 -->
    <div v-if="tab === 'lecture'" class="scrollbar-hide flex-1 space-y-2 overflow-y-auto p-3">
      <button
        v-for="n in notes"
        :key="n.id"
        type="button"
        :class="n.id === activeNoteId ? 'w-full rounded-xl border border-purple-300 bg-purple-50 p-3 text-left ring-2 ring-purple-200 dark:border-purple-700 dark:bg-purple-950/30' : 'w-full rounded-xl border border-gray-100 bg-white p-3 text-left dark:border-gray-800 dark:bg-gray-900'"
        @click="playAudio(n.audioPath, n.body, n.id)"
      >
        <div class="text-[13px] font-semibold text-gray-800 dark:text-gray-100">{{ n.title }}</div>
        <p class="mt-1 text-[12px] leading-relaxed text-gray-500 dark:text-gray-400">{{ n.body }}</p>
      </button>
    </div>

    <!-- 对话 -->
    <div v-else class="scrollbar-hide flex-1 space-y-2 overflow-y-auto p-3">
      <div
        v-if="sessions.length === 0"
        class="flex h-full flex-col items-center justify-center p-6 text-center opacity-50"
      >
        <div class="flex size-12 items-center justify-center rounded-full bg-gray-100 dark:bg-gray-800">
          <MessageSquare class="size-5 text-gray-400" />
        </div>
        <p class="mt-3 text-[13px] text-gray-500 dark:text-gray-400">{{ t('chat.noConversations') }}</p>
        <p class="mt-1 text-[12px] text-gray-400">{{ t('chat.startConversation') }}</p>
      </div>

      <button
        v-for="s in sessions"
        :key="s.id"
        type="button"
        :class="
          cn(
            'w-full overflow-hidden rounded-xl border text-left transition-all duration-500',
            s.active
              ? 'border-purple-200 bg-purple-50/30 shadow-sm dark:border-purple-800 dark:bg-purple-900/20'
              : 'border-gray-100 hover:border-gray-200 dark:border-gray-800 dark:hover:border-gray-700',
          )
        "
        @click="emit('open-session', s.id)"
      >
        <div class="flex items-center gap-2 px-3 pt-2.5">
          <span
            :class="
              cn(
                'rounded-full px-1.5 py-0.5 text-[10px] font-bold tracking-wide uppercase',
                TYPE_BADGE[s.type],
              )
            "
          >
            {{ s.type }}
          </span>
          <span class="min-w-0 flex-1 truncate text-[13px] font-medium text-gray-700 dark:text-gray-200">
            {{ s.title }}
          </span>
        </div>
        <p class="truncate px-3 pt-1 pb-2.5 text-[12px] text-gray-400">
          {{ s.preview }}
        </p>
      </button>
    </div>

    <!-- 拖拽手柄 -->
    <div
      class="group absolute top-0 bottom-0 left-0 z-50 w-1.5 cursor-col-resize"
      @mousedown="emit('resize-start', $event)"
    >
      <div
        class="absolute top-1/2 left-0.5 h-8 w-0.5 -translate-y-1/2 rounded-full bg-gray-300 transition-colors group-hover:bg-purple-400"
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
