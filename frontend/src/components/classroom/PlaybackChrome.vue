<script setup lang="ts">
/**
 * PlaybackChrome —— 文档 §6.2（课堂主界面三栏布局）。
 *
 * 尺寸常量（源码注释明确，不可臆改）：
 *   Header      80px  (h-20)
 *   Roundtable 192px  (h-[192px])
 *   SceneSidebar 默认 220 / min 170 / max 400
 *   ChatArea     默认 340 / min 240 / max 560
 *   舞台高度 = calc(100% - 80px - 192px)
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { AlertTriangle } from 'lucide-vue-next'
import { toast } from 'vue-sonner'

import CanvasArea from '@/components/classroom/CanvasArea.vue'
import ChatArea from '@/components/classroom/ChatArea.vue'
import ClassroomHeader from '@/components/classroom/ClassroomHeader.vue'
import Roundtable from '@/components/classroom/Roundtable.vue'
import SceneSidebar from '@/components/classroom/SceneSidebar.vue'
import SettingsDialog from '@/components/home/SettingsDialog.vue'
import UiTooltip from '@/components/ui/UiTooltip.vue'
import { useResizable } from '@/composables/useResizable'
import type { Classroom, Scene } from '@/data/scenes'
import { PRESET_ROLES } from '@/data/agents'
import type { Bubble, ChatNote, ChatSession, Participant } from '@/types/classroom'
import { cn } from '@/lib/utils'

const props = defineProps<{ classroom: Classroom }>()

const { t } = useI18n()
const router = useRouter()

/* ---------- 面板尺寸（解构为顶层 ref，模板才会自动解包） ---------- */
const {
  width: sidebarWidth,
  collapsed: sidebarCollapsed,
  start: startSidebarResize,
  toggle: toggleSidebar,
} = useResizable({ initial: 220, min: 170, max: 400, side: 'left' })

const {
  width: chatWidth,
  collapsed: chatCollapsed,
  start: startChatResize,
  toggle: toggleChat,
} = useResizable({ initial: 340, min: 240, max: 560, side: 'right' })

/* ---------- 播放状态 ---------- */
const FALLBACK_SCENE: Scene = { id: '__empty', title: '', type: 'slide', status: 'pending' }

const scenes = computed(() => props.classroom.scenes)
const activeIndex = ref(0)
const activeScene = computed<Scene>(() => scenes.value[activeIndex.value] ?? FALLBACK_SCENE)

const playing = ref(false)
const volume = ref(1)
const speed = ref(1)
const autoPlay = ref(false)
const whiteboardOpen = ref(false)
const fullscreen = ref(false)
const isPresenting = ref(false)
const controlsVisible = ref(true)

const settingsOpen = ref(false)
const proMode = ref(false)

const exporting = ref(false)
const exportPercent = ref(0)

const pendingSelectId = ref<string | null>(null)
const confirmSwitchOpen = ref(false)
const topicInProgress = ref(false)

const courseComplete = computed(() => activeScene.value.type === 'complete')

const stats = computed(() => ({
  scenes: scenes.value.length,
  minutes: 18,
  agents: 3,
  messages: 24,
}))

/* ---------- 圆桌 / 聊天 ---------- */
const bubbles = ref<Bubble[]>([
  {
    id: 'b1',
    from: 'teacher',
    name: '陈老师',
    text: '欢迎！我们从最基础的问题开始——为什么选 Python？',
  },
  {
    id: 'b2',
    from: 'agent',
    name: '好奇宝宝',
    text: '因为它写起来像英语句子，读代码就能猜到意思。',
  },
])
const speaking = ref<'teacher' | 'agent' | null>('teacher')
const thinking = ref(false)
const yourTurn = ref(false)
const recording = ref(false)

const chatTab = ref<'lecture' | 'chat'>('chat')

const sessions = ref<ChatSession[]>([
  {
    id: 'c1',
    title: '为什么 Python 适合入门？',
    type: 'qa',
    preview: '语法接近自然语言，学习曲线平缓…',
    active: true,
  },
  {
    id: 'c2',
    title: '关于变量作用域的讨论',
    type: 'discussion',
    preview: '局部变量与全局变量的边界在哪里…',
  },
  {
    id: 'c3',
    title: '第 2 页讲解笔记',
    type: 'lecture',
    preview: '变量是内存中的一块空间…',
  },
])
const notes = ref<ChatNote[]>([
  {
    id: 'n1',
    title: '变量与类型',
    body: 'Python 是动态类型语言，赋值时才确定类型。常用 type() 查看。',
  },
  {
    id: 'n2',
    title: '命名规范',
    body: '变量名只能由字母、数字、下划线组成，且不能以数字开头。',
  },
])

const hasActiveSession = computed(() => sessions.value.some((s) => s.active))
const showPlayHint = computed(() => !playing.value && !courseComplete.value)

/* ---------- 圆桌参与者（原遗漏：学员头像 + 信息卡 + 麦克风/聊天） ---------- */
const participants = computed<Participant[]>(() =>
  PRESET_ROLES.map((r) => ({
    id: r.id,
    name: r.name,
    roleType: r.roleType,
    role: r.role,
    avatar: r.avatar,
    color: r.color,
    persona: r.persona,
  })),
)
const asrEnabled = ref(true)
function toggleRecording() {
  recording.value = !recording.value
}

/* ---------- 交互 ---------- */
function selectScene(id: string) {
  if (id === activeScene.value.id) return
  if (topicInProgress.value) {
    pendingSelectId.value = id
    confirmSwitchOpen.value = true
    return
  }
  applySelect(id)
}

function applySelect(id: string) {
  const i = scenes.value.findIndex((s) => s.id === id)
  if (i >= 0) activeIndex.value = i
  topicInProgress.value = false
}

function confirmSwitch() {
  if (pendingSelectId.value) applySelect(pendingSelectId.value)
  pendingSelectId.value = null
  confirmSwitchOpen.value = false
}

function cancelSwitch() {
  pendingSelectId.value = null
  confirmSwitchOpen.value = false
}

function retryScene(id: string) {
  const s = scenes.value.find((x) => x.id === id)
  if (!s) return
  s.status = 'generating'
  window.setTimeout(() => {
    s.status = 'ready'
  }, 1500)
}

function prev() {
  if (activeIndex.value > 0) activeIndex.value -= 1
}
function next() {
  if (activeIndex.value < scenes.value.length - 1) activeIndex.value += 1
}

function togglePlay() {
  playing.value = !playing.value
}

function togglePro() {
  proMode.value = !proMode.value
  toast(proMode.value ? '已进入 Pro 模式' : '已返回普通模式')
}

function openSession(id: string) {
  sessions.value = sessions.value.map((s) => ({ ...s, active: s.id === id }))
}

function sendMessage(text: string) {
  bubbles.value = [...bubbles.value, { id: `b-${Date.now()}`, from: 'user', text }]
  thinking.value = true
  window.setTimeout(() => {
    thinking.value = false
    bubbles.value = [
      ...bubbles.value,
      {
        id: `b-${Date.now()}-r`,
        from: 'teacher',
        name: '陈老师',
        text: '好问题，我们下一段就来展开讲。',
      },
    ]
  }, 1200)
}

function toggleFullscreen() {
  if (!document.fullscreenElement) {
    void document.documentElement.requestFullscreen?.()
  } else {
    void document.exitFullscreen?.()
  }
}

function onExport(kind: string) {
  exporting.value = true
  exportPercent.value = 0
  const timer = window.setInterval(() => {
    exportPercent.value += 10
    if (exportPercent.value >= 100) {
      window.clearInterval(timer)
      exporting.value = false
      toast(`已导出 ${kind}`)
    }
  }, 120)
}

/** 键盘快捷键（文档 §6.12） */
function onKeydown(e: KeyboardEvent) {
  const tag = (e.target as HTMLElement | null)?.tagName
  if (tag === 'INPUT' || tag === 'TEXTAREA') return
  switch (e.key) {
    case 'ArrowLeft':
      prev()
      break
    case 'ArrowRight':
      next()
      break
    case ' ':
      e.preventDefault()
      togglePlay()
      break
    case 'ArrowUp':
      volume.value = Math.min(1, volume.value + 0.1)
      break
    case 'ArrowDown':
      volume.value = Math.max(0, volume.value - 0.1)
      break
    case 'm':
    case 'M':
      volume.value = volume.value === 0 ? 1 : 0
      break
    case 's':
    case 'S':
      toggleSidebar()
      break
    case 'c':
    case 'C':
      toggleChat()
      break
    case 'Escape':
      if (document.fullscreenElement) void document.exitFullscreen?.()
      fullscreen.value = false
      break
  }
}

function onFullscreenChange() {
  fullscreen.value = Boolean(document.fullscreenElement)
}

onMounted(() => {
  window.addEventListener('keydown', onKeydown)
  document.addEventListener('fullscreenchange', onFullscreenChange)
})
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeydown)
  document.removeEventListener('fullscreenchange', onFullscreenChange)
})
</script>

<template>
  <div
    :class="
      cn(
        'relative flex flex-1 overflow-hidden bg-gray-50 dark:bg-gray-900',
        isPresenting && !controlsVisible && 'cursor-none',
      )
    "
  >
    <!-- 左：场景栏 -->
    <SceneSidebar
      :scenes="scenes"
      :active-id="activeScene.id"
      :collapsed="sidebarCollapsed"
      :width="sidebarWidth"
      @select="selectScene"
      @retry="retryScene"
      @toggle-collapse="toggleSidebar"
      @resize-start="startSidebarResize"
    />

    <!-- 中：主区 -->
    <div class="relative flex min-w-0 flex-1 flex-col overflow-hidden">
      <!-- 顶部 Header（80px） -->
      <ClassroomHeader
        v-if="!isPresenting"
        :scene-title="activeScene.title || classroom.title"
        :pro-mode="proMode"
        :exporting="exporting"
        :export-percent="exportPercent"
        :can-export="true"
        @back="router.push({ name: 'home' })"
        @toggle-pro="togglePro"
        @export="onExport"
        @open-settings="settingsOpen = true"
      />

      <!-- 舞台：高度 = 100% - (Header + Roundtable) -->
      <div
        class="relative isolate min-h-0 flex-1 overflow-hidden"
        :style="{ height: `calc(100% - ${isPresenting ? 0 : 80}px - 192px)` }"
      >
        <CanvasArea
          :scene="activeScene"
          :index="activeIndex"
          :total="scenes.length"
          :playing="playing"
          :volume="volume"
          :speed="speed"
          :auto-play="autoPlay"
          :whiteboard-open="whiteboardOpen"
          :fullscreen="fullscreen"
          :chat-collapsed="chatCollapsed"
          :show-play-hint="showPlayHint"
          :course-complete="courseComplete"
          :stats="stats"
          @toggle-sidebar="toggleSidebar"
          @prev="prev"
          @next="next"
          @toggle-play="togglePlay"
          @update:volume="volume = $event"
          @update:speed="speed = $event"
          @toggle-auto-play="autoPlay = !autoPlay"
          @toggle-whiteboard="whiteboardOpen = !whiteboardOpen"
          @toggle-fullscreen="toggleFullscreen"
          @toggle-chat="toggleChat"
        />
      </div>

      <!-- 圆桌区（192px） -->
      <Roundtable
        :bubbles="bubbles"
        :speaking="speaking"
        :thinking="thinking"
        :your-turn="yourTurn"
        :recording="recording"
        :participants="participants"
        :asr-enabled="asrEnabled"
        @send="sendMessage"
        @toggle-recording="toggleRecording"
      />
    </div>

    <!-- 右：聊天面板 -->
    <ChatArea
      :collapsed="chatCollapsed"
      :width="chatWidth"
      :tab="chatTab"
      :sessions="sessions"
      :has-active-session="hasActiveSession"
      :notes="notes"
      @update:tab="chatTab = $event"
      @toggle-collapse="toggleChat"
      @resize-start="startChatResize"
      @open-session="openSession"
    />

    <!-- 白板占位入口（完整白板后续补） -->
    <UiTooltip v-if="whiteboardOpen" :content="t('whiteboard.minimize')" side="top">
      <button
        type="button"
        class="absolute bottom-[200px] left-1/2 z-[120] -translate-x-1/2 rounded-full bg-white/90 px-3 py-1.5 text-[11px] font-medium text-purple-600 shadow-lg ring-1 ring-purple-200 backdrop-blur dark:bg-gray-800/90 dark:ring-purple-800"
        @click="whiteboardOpen = false"
      >
        {{ t('whiteboard.title') }}
      </button>
    </UiTooltip>

    <!-- 切换场景确认弹窗 -->
    <Transition
      enter-active-class="transition duration-200 ease-out"
      enter-from-class="opacity-0"
      leave-active-class="transition duration-150 ease-in"
      leave-to-class="opacity-0"
    >
      <div
        v-if="confirmSwitchOpen"
        class="fixed inset-0 z-[200] flex items-center justify-center bg-black/40 p-4 backdrop-blur-[2px]"
        @click.self="cancelSwitch"
      >
        <div
          class="w-full max-w-sm overflow-hidden rounded-2xl border-0 bg-background shadow-[0_25px_60px_-12px_rgba(0,0,0,0.15)]"
        >
          <div class="h-1 bg-gradient-to-r from-amber-400 via-orange-400 to-red-400" />
          <div class="px-6 pt-6">
            <div
              class="flex size-12 items-center justify-center rounded-full bg-amber-50 ring-1 ring-amber-200/50 dark:bg-amber-950/30"
            >
              <AlertTriangle class="size-5 text-amber-500" />
            </div>
            <h3 class="mt-4 text-base font-bold">{{ t('stage.confirmSwitchTitle') }}</h3>
            <p class="mt-1.5 text-sm leading-relaxed text-gray-500 dark:text-gray-400">
              {{ t('stage.confirmSwitchMessage') }}
            </p>
          </div>
          <div class="flex flex-row gap-3 px-6 pt-3 pb-5">
            <button
              type="button"
              class="flex-1 cursor-pointer rounded-xl border border-border px-4 py-2 text-sm font-medium transition-colors hover:bg-muted"
              @click="cancelSwitch"
            >
              {{ t('common.cancel') }}
            </button>
            <button
              type="button"
              class="flex-1 cursor-pointer rounded-xl border-0 bg-gradient-to-r from-amber-500 to-orange-500 px-4 py-2 text-sm font-medium text-white shadow-md shadow-amber-200/50 transition-opacity hover:opacity-90"
              @click="confirmSwitch"
            >
              {{ t('stage.confirmSwitchTitle') }}
            </button>
          </div>
        </div>
      </div>
    </Transition>

    <SettingsDialog v-model:open="settingsOpen" />
  </div>
</template>
