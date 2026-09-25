<script setup lang="ts">
/**
 * Roundtable —— 文档 §6.7（圆桌区，h-[192px]）。
 * 左：教师头像 + 在线点（+ 悬停信息卡）；中：气泡流 / 输入框 / 录音 UI / 思考三点；
 * 右：学员头像轮播（+ 悬停信息卡：头像/姓名/角色徽章/人设）+ 分隔线 + 麦克风/聊天按钮 + 用户头像。
 *
 * 麦克风只此一处（右列），避免"两个麦克风"重复；点击切换录音态，录音态下接浏览器
 * Web Speech API（若可用）做实时语音识别，识别结果回填到输入框。
 */
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  ChevronLeft,
  ChevronRight,
  MessageSquare,
  Mic,
  MicOff,
  Send,
} from 'lucide-vue-next'
import {
  HoverCardContent,
  HoverCardPortal,
  HoverCardRoot,
  HoverCardTrigger,
} from 'reka-ui'

import { cn } from '@/lib/utils'
import { useProfileStore } from '@/stores/profile'
import type { Bubble, Participant } from '@/types/classroom'

const props = defineProps<{
  bubbles: Bubble[]
  speaking: 'teacher' | 'agent' | null
  thinking: boolean
  yourTurn: boolean
  recording: boolean
  /** 学员参与者（教师另行在左列展示） */
  participants?: Participant[]
  /** 正在发言的学员 id（高亮描边） */
  speakingAgentId?: string | null
  /** 用户头像；缺省取 profile store 里用户自己挑的那张 */
  userAvatar?: string
  /** 语音识别是否可用；关闭时麦克风按钮置灰 */
  asrEnabled?: boolean
}>()

const emit = defineEmits<{
  (e: 'send', text: string): void
  (e: 'toggle-recording'): void
}>()

const { t } = useI18n()
const profileStore = useProfileStore()

const draft = ref('')
const inputOpen = ref(false)
const inputRef = ref<HTMLTextAreaElement | null>(null)
const agentScrollRef = ref<HTMLElement | null>(null)

/** 只展示最近 3 条，避免 192px 高度溢出 */
const visible = computed(() => {
  const lecture = props.bubbles.filter((bubble) => bubble.id.startsWith('lecture-'))
  const conversations = props.bubbles.filter((bubble) => !bubble.id.startsWith('lecture-')).slice(-3)
  const currentLecture = lecture.at(-1)
  return currentLecture ? [...conversations, currentLecture] : conversations
})

const teacherActive = computed(() => props.speaking === 'teacher')
const studentActive = (id: string) => props.speakingAgentId === id

const bubbleClass: Record<Bubble['from'], string> = {
  user: 'bg-purple-600/95 backdrop-blur-sm border-purple-400/40 text-white rounded-br-sm shadow-md shadow-purple-300/30 self-end',
  agent: 'bg-blue-50/95 border-blue-200/60 text-gray-700 rounded-br-sm shadow-sm dark:bg-blue-950/40 dark:border-blue-900 dark:text-blue-100',
  teacher:
    'relative bg-purple-50/95 border-purple-200/70 text-purple-900 rounded-bl-sm shadow-sm cursor-pointer dark:bg-purple-950/40 dark:border-purple-800 dark:text-purple-100 before:absolute before:left-[-7px] before:bottom-4 before:size-3 before:rotate-45 before:border-l before:border-b before:border-purple-200/70 before:bg-purple-50/95 dark:before:border-purple-800 dark:before:bg-purple-950/40',
}

const resolvedUserAvatar = computed(() => props.userAvatar || profileStore.profile.avatar)

function submit() {
  const text = draft.value.trim()
  if (!text) return
  emit('send', text)
  draft.value = ''
  nextTick(() => inputRef.value?.focus())
}

function toggleInput() {
  inputOpen.value = !inputOpen.value
  if (inputOpen.value) nextTick(() => inputRef.value?.focus())
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    submit()
  }
}

/** 聊天按钮 / 用户头像点击 → 聚焦输入框 */
function focusInput() {
  inputOpen.value = true
  nextTick(() => inputRef.value?.focus())
}

/* ── 学员头像横向滚动 ── */
function scrollAgents(dx: number) {
  agentScrollRef.value?.scrollBy({ left: dx, behavior: 'smooth' })
}
function onAgentWheel(e: WheelEvent) {
  if (Math.abs(e.deltaY) > Math.abs(e.deltaX)) {
    if (agentScrollRef.value) agentScrollRef.value.scrollLeft += e.deltaY
    e.preventDefault()
  }
}

/* ── 语音识别（可选，浏览器不支持时退化为纯录音 UI） ── */
const interim = ref('')
let recognition: any = null

function startRecognition() {
  const SR = (window as any).SpeechRecognition || (window as any).webkitSpeechRecognition
  if (!SR) return
  try {
    recognition = new SR()
    recognition.lang = 'zh-CN'
    recognition.interimResults = true
    recognition.continuous = false
    recognition.onresult = (e: any) => {
      let txt = ''
      for (let i = e.resultIndex; i < e.results.length; i++) txt += e.results[i][0].transcript
      interim.value = txt
    }
    recognition.start()
  } catch {
    recognition = null
  }
}

function stopRecognition() {
  if (recognition) {
    try {
      recognition.stop()
    } catch {
      /* ignore */
    }
    recognition = null
  }
  if (interim.value.trim()) {
    draft.value = interim.value.trim()
    interim.value = ''
    nextTick(() => inputRef.value?.focus())
  }
}

watch(
  () => props.recording,
  (on) => (on ? startRecognition() : stopRecognition()),
)
onBeforeUnmount(stopRecognition)
</script>

<template>
  <div
    class="relative z-10 flex h-[192px] w-full shrink-0 flex-col border-t border-gray-100 bg-white/60 backdrop-blur-md dark:border-gray-800 dark:bg-gray-900/60"
  >
    <div class="flex min-h-0 flex-1 items-stretch">
      <!-- 左：教师 -->
      <div
        class="relative flex w-[90px] shrink-0 flex-col items-center justify-center gap-2 border-r border-gray-100 dark:border-gray-800"
      >
        <div
          class="pointer-events-none absolute top-0 h-16 w-full bg-gradient-to-b from-purple-50/50 to-transparent dark:from-purple-900/20"
        />

        <HoverCardRoot :open-delay="300" :close-delay="100">
          <HoverCardTrigger as-child>
            <div
              :class="
                cn(
                  'relative flex size-12 cursor-pointer items-center justify-center rounded-full transition-all duration-300',
                  teacherActive
                    ? 'scale-105 border border-purple-500 shadow-[0_0_12px_rgba(168,85,247,0.4)]'
                    : 'border border-transparent',
                )
              "
            >
              <div class="size-10 overflow-hidden rounded-full">
                <img src="/avatars/teacher-2.png" alt="" class="size-full object-cover" />
              </div>
              <span
                class="absolute right-0.5 bottom-0.5 size-4 rounded-full border-2 border-white bg-green-500 dark:border-gray-900"
              />
            </div>
          </HoverCardTrigger>
          <HoverCardPortal>
            <HoverCardContent side="top" align="center" class="z-[200] w-64 rounded-xl border border-gray-100 bg-white p-3 shadow-xl dark:border-gray-700 dark:bg-gray-800">
              <div class="flex items-center gap-2">
                <div class="size-8 shrink-0 overflow-hidden rounded-full bg-gray-100 dark:bg-gray-800">
                  <img src="/avatars/teacher-2.png" alt="" class="size-full" />
                </div>
                <div class="min-w-0">
                  <p class="truncate text-sm font-medium text-gray-800 dark:text-gray-100">
                    {{ t('roundtable.teacher') }}
                  </p>
                  <span
                    class="mt-0.5 inline-block rounded-full px-1.5 py-0.5 text-[10px] leading-tight text-white"
                    :style="{ backgroundColor: '#722ed1' }"
                  >
                    {{ t('roundtable.roles.teacher') }}
                  </span>
                </div>
              </div>
              <p class="mt-2 whitespace-pre-line text-xs leading-relaxed text-gray-500 dark:text-gray-400">
                {{ t('roundtable.teacherPersona') }}
              </p>
            </HoverCardContent>
          </HoverCardPortal>
        </HoverCardRoot>

        <span
          class="rounded-full border border-gray-100 bg-white/90 px-2 py-0.5 text-[10px] font-bold tracking-wider text-gray-500 uppercase dark:border-gray-700 dark:bg-gray-800/90 dark:text-gray-400"
        >
          {{ t('roundtable.teacher') }}
        </span>
      </div>

      <!-- 中：气泡区 -->
      <div class="relative mx-3 mb-2 min-w-0 flex-1">
        <div
          class="relative flex size-full flex-col overflow-hidden rounded-[2.5rem] border border-white/50 bg-gradient-to-b from-white/40 to-white/80 px-5 py-3 shadow-[0_20px_60px_-15px_rgba(0,0,0,0.05),inset_0_1px_0_0_rgba(255,255,255,0.9)] backdrop-blur-xl dark:border-gray-700/50 dark:from-gray-800/40 dark:to-gray-800/70"
        >
          <!-- 气泡滚动区（mt-auto 而非 justify-end，否则滚动时顶部会被裁） -->
          <div class="scrollbar-hide flex min-h-0 flex-1 flex-col overflow-y-auto">
            <div class="mt-auto flex flex-col gap-1.5">
              <div
                v-for="b in visible"
                :key="b.id"
                :class="
                  cn(
                    'max-h-[100px] w-[min(520px,calc(100%-1rem))] shrink-0 overflow-y-auto rounded-2xl border px-4 py-2.5 text-[12px] leading-relaxed',
                    bubbleClass[b.from],
                  )
                "
              >
                <div v-if="b.from === 'teacher'" class="mb-1 flex items-center gap-1.5 pl-0.5">
                  <div class="size-5 shrink-0 overflow-hidden rounded-full border border-purple-200">
                    <img src="/avatars/teacher-2.png" alt="" class="size-full object-cover" />
                  </div>
                  <span class="text-[10px] font-bold tracking-wide text-purple-600 uppercase dark:text-purple-300">
                    {{ b.name || t('roundtable.teacher') }}
                  </span>
                </div>
                <span
                  v-if="b.name && b.from !== 'teacher'"
                  class="mb-0.5 block text-[10px] font-bold tracking-wide text-gray-400 uppercase"
                >
                  {{ b.name }}
                </span>
                {{ b.text }}
              </div>
            </div>
          </div>

          <!-- 思考三点 -->
          <div v-if="thinking" class="flex shrink-0 items-center gap-1.5 self-start pt-1.5 pl-1">
            <span
              v-for="i in 3"
              :key="i"
              class="size-1.5 animate-pulse rounded-full bg-purple-500"
              :style="{ animationDelay: `${(i - 1) * 0.2}s`, animationDuration: '1.2s' }"
            />
          </div>

          <!-- 录音中（识别结果实时回填） -->
          <div v-if="recording" class="flex shrink-0 items-center gap-3 self-end px-2 pt-1">
            <div class="flex h-8 items-center gap-0.5">
              <span
                v-for="i in 12"
                :key="i"
                class="w-0.5 rounded-full bg-purple-500"
                :style="{
                  animation: `wave 0.6s ease-in-out ${i * 0.05}s infinite alternate`,
                  height: `${4 + (i % 4) * 3}px`,
                }"
              />
            </div>
            <div class="relative">
              <span class="absolute inset-0 animate-ping rounded-full bg-purple-500/30" />
              <span
                class="relative flex size-10 items-center justify-center rounded-full bg-gradient-to-br from-purple-600 to-indigo-700 text-white shadow-lg"
              >
                <Mic class="size-5" />
              </span>
            </div>
            <span class="max-w-[180px] truncate text-[12px] text-purple-600 dark:text-purple-300">
              {{ interim || t('roundtable.listening') }}
            </span>
          </div>

          <!-- 输入框 -->
          <div
            v-if="!recording && inputOpen"
            class="mt-1.5 w-fit max-w-[85%] min-w-[200px] shrink-0 self-end rounded-2xl rounded-br-none border border-purple-200 bg-white/90 p-2 shadow-2xl ring-1 ring-purple-100/50 backdrop-blur-md sm:max-w-[65%] sm:min-w-[300px] dark:border-purple-800 dark:bg-gray-800/90 dark:ring-purple-900/40"
          >
            <div class="flex items-end gap-1.5">
              <textarea
                ref="inputRef"
                v-model="draft"
                rows="1"
                :placeholder="yourTurn ? t('roundtable.yourTurnHint') : t('roundtable.inputPlaceholder')"
                class="max-h-16 min-h-[34px] flex-1 resize-none bg-transparent px-2 py-1.5 text-[13px] leading-relaxed outline-none placeholder:text-gray-400"
                @keydown="onKeydown"
              />
              <button
                type="button"
                class="flex size-9 shrink-0 items-center justify-center rounded-xl bg-purple-600 text-white transition-colors hover:bg-purple-700 disabled:opacity-40"
                :disabled="!draft.trim()"
                @click="submit"
              >
                <Send class="size-4" />
              </button>
              <button
                type="button"
                class="flex size-9 shrink-0 items-center justify-center rounded-xl text-gray-400 transition-colors hover:bg-purple-50 hover:text-purple-600"
                aria-label="关闭消息输入框"
                @click="toggleInput"
              >
                <MessageSquare class="size-4" />
              </button>
            </div>
          </div>
        </div>
      </div>

      <!-- 右：参与者（学员头像 + 用户头像 + 麦克风/聊天） -->
      <div
        class="flex w-[140px] shrink-0 flex-col border-l border-gray-100/50 bg-gray-50/30 py-3 dark:border-gray-700/50 dark:bg-gray-900/30"
      >
        <!-- 学员头像横向滚动 + 悬停信息卡 -->
        <div class="group/scroll relative flex-none">
          <button
            type="button"
            class="absolute left-0 top-0 bottom-0 z-10 flex w-5 items-center justify-center bg-gradient-to-r from-gray-50/90 to-transparent opacity-0 transition-opacity group-hover/scroll:opacity-100 dark:from-gray-900/90"
            @click="scrollAgents(-80)"
          >
            <ChevronLeft class="size-3.5 text-gray-400" />
          </button>

          <div
            ref="agentScrollRef"
            class="scrollbar-hide overflow-x-auto overflow-y-hidden px-2 py-1"
            @wheel="onAgentWheel"
          >
            <div class="flex w-max gap-1">
              <div v-for="p in participants" :key="p.id" class="relative shrink-0 group/student">
              <HoverCardRoot :open-delay="300" :close-delay="100">
                <HoverCardTrigger as-child>
                  <div
                    :class="
                      cn(
                        'relative size-9 cursor-pointer rounded-full transition-all duration-300',
                        studentActive(p.id)
                          ? 'scale-110 opacity-100'
                          : 'scale-95 opacity-50 grayscale-[0.2] hover:scale-100 hover:opacity-100 hover:grayscale-0',
                      )
                    "
                  >
                    <div
                      :class="
                        cn(
                          'absolute inset-0 rounded-full border-2 transition-all duration-300',
                          studentActive(p.id)
                            ? 'border-purple-500 shadow-[0_0_8px_rgba(168,85,247,0.4)] dark:border-purple-400'
                            : 'border-white dark:border-gray-700',
                        )
                      "
                    />
                    <div class="absolute inset-0.5 overflow-hidden rounded-full bg-gray-100 dark:bg-gray-800">
                      <img :src="p.avatar" :alt="p.name" class="size-full" />
                    </div>
                    <div
                      v-if="studentActive(p.id)"
                      class="absolute -right-0.5 -top-0.5 z-20 flex size-3 items-center justify-center rounded-full border border-white bg-green-500 dark:border-gray-800"
                    >
                      <div class="size-1 animate-pulse rounded-full bg-white" />
                    </div>
                  </div>
                </HoverCardTrigger>
                <HoverCardPortal>
                  <HoverCardContent
                    side="top"
                    align="end"
                    :side-offset="6"
                    class="z-[200] w-64 rounded-xl border border-gray-100 bg-white p-3 shadow-xl dark:border-gray-700 dark:bg-gray-800"
                  >
                    <div class="flex items-center gap-2">
                      <div class="size-8 shrink-0 overflow-hidden rounded-full bg-gray-100 dark:bg-gray-800">
                        <img :src="p.avatar" :alt="p.name" class="size-full" />
                      </div>
                      <div class="min-w-0">
                        <p class="truncate text-sm font-medium text-gray-800 dark:text-gray-100">
                          {{ p.name }}
                        </p>
                        <span
                          class="mt-0.5 inline-block rounded-full px-1.5 py-0.5 text-[10px] leading-tight text-white"
                          :style="{ backgroundColor: p.color || '#6b7280' }"
                        >
                          {{ t(`roundtable.roles.${p.roleType}`) }}
                        </span>
                      </div>
                    </div>
                    <p
                      v-if="p.persona"
                      class="mt-2 whitespace-pre-line text-xs leading-relaxed text-gray-500 dark:text-gray-400"
                    >
                      {{ p.persona }}
                    </p>
                  </HoverCardContent>
                </HoverCardPortal>
                </HoverCardRoot>
              </div>
            </div>
          </div>

          <button
            type="button"
            class="absolute right-0 top-0 bottom-0 z-10 flex w-5 items-center justify-center bg-gradient-to-l from-gray-50/90 to-transparent opacity-0 transition-opacity group-hover/scroll:opacity-100 dark:from-gray-900/90"
            @click="scrollAgents(80)"
          >
            <ChevronRight class="size-3.5 text-gray-400" />
          </button>
        </div>

        <div class="mx-auto my-1.5 h-px w-8 shrink-0 bg-gray-200 opacity-50 dark:bg-gray-700" />

        <!-- 用户头像 + 麦克风 / 聊天按钮 -->
        <div class="flex min-h-0 flex-1 items-center justify-center gap-3 px-2">
          <div class="flex shrink-0 flex-col gap-1.5">
            <button
              type="button"
              :disabled="!asrEnabled"
              :class="
                cn(
                  'flex size-8 items-center justify-center rounded-full border shadow-sm transition-all active:scale-95',
                  !asrEnabled
                    ? 'cursor-not-allowed border-gray-200 bg-gray-100 text-gray-300 dark:border-gray-700 dark:bg-gray-800/50 dark:text-gray-600'
                    : recording
                      ? 'border-purple-600 bg-purple-600 text-white dark:border-purple-500'
                      : 'border-gray-200 bg-white text-gray-400 hover:border-purple-200 hover:text-purple-600 dark:border-gray-700 dark:bg-gray-800 dark:hover:text-purple-400',
                )
              "
              :title="asrEnabled ? t('roundtable.voiceInput') : t('roundtable.asrDisabled')"
              @click="emit('toggle-recording')"
            >
              <MicOff v-if="!asrEnabled" class="size-3.5" />
              <Mic v-else class="size-3.5" />
            </button>
            <button
              type="button"
              class="flex size-8 items-center justify-center rounded-full border border-gray-200 bg-white text-gray-400 shadow-sm transition-all hover:border-purple-200 hover:text-purple-600 active:scale-95 dark:border-gray-700 dark:bg-gray-800 dark:hover:text-purple-400"
              @click="toggleInput"
            >
              <MessageSquare class="size-3.5" />
            </button>
          </div>

          <!-- 用户头像（轮到你时高亮） -->
          <div class="relative shrink-0 cursor-pointer group" @click="focusInput">
            <div
              :class="
                cn(
                  'relative flex size-16 items-center justify-center rounded-full transition-all duration-300',
                  yourTurn
                    ? 'scale-105'
                    : 'scale-95 opacity-50 grayscale-[0.2] group-hover:scale-100 group-hover:opacity-100 group-hover:grayscale-0',
                )
              "
            >
              <div
                :class="
                  cn(
                    'absolute inset-0 rounded-full border-2 transition-all duration-300',
                    yourTurn
                      ? 'border-amber-500 shadow-[0_0_12px_rgba(245,158,11,0.4)] animate-pulse'
                      : 'border-white group-hover:border-purple-200 dark:border-gray-700 dark:group-hover:border-purple-600',
                  )
                "
              />
              <div class="relative z-10 size-14 overflow-hidden rounded-full bg-gray-50 text-2xl dark:bg-gray-800">
                <img :src="resolvedUserAvatar" :alt="t('roundtable.you')" class="size-full" />
              </div>
              <div
                class="absolute right-0 top-0 z-20 flex size-5 items-center justify-center rounded-full border border-gray-100 bg-white shadow-md dark:border-gray-700 dark:bg-gray-800"
              >
                <div
                  :class="
                    cn(
                      'size-1.5 rounded-full',
                      yourTurn ? 'animate-pulse bg-purple-500' : 'bg-gray-300 dark:bg-gray-600',
                    )
                  "
                />
              </div>
            </div>
            <span
              v-if="yourTurn"
              class="absolute -bottom-2 left-1/2 -translate-x-1/2 whitespace-nowrap rounded-full bg-amber-500 px-2 py-0.5 text-[9px] font-bold text-white shadow-sm dark:bg-amber-400"
            >
              {{ t('roundtable.yourTurn') }}
            </span>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
