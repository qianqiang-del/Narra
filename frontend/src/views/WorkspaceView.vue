<script setup lang="ts">
/**
 * Pro 工作区 —— 对应旧 app/workspace/page.tsx（WorkspaceShell）。
 *
 * 三栏布局（对齐截图与原版 WorkspaceShell.tsx）：
 *   WorkspaceRail（左导航，简化版）
 *   WorkspaceChatPane（中对话，修改课件的唯一入口）
 *   WorkspaceClassroomPane（右课件，静态 HTML + iframe 渲染）
 *
 * 与原版一致的约定：
 * - 对话栏保持固定宽度，课件栏吃掉剩余空间；
 * - 课程 Tab 关闭最后一个即收起整个课件栏；
 * - 对话与课程是多对多：切对话不动课件，切课件不重建对话。
 *
 * 没有 classroomId 时保留 mock 预览；带 classroomId 时使用 Go 对话 API 和 SSE。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { PanelLeftOpen } from 'lucide-vue-next'
import { useRoute } from 'vue-router'

import WorkspaceRail from '@/components/workspace/WorkspaceRail.vue'
import WorkspaceChatPane from '@/components/workspace/WorkspaceChatPane.vue'
import WorkspaceClassroomPane from '@/components/workspace/WorkspaceClassroomPane.vue'
import { useResizable } from '@/composables/useResizable'
import {
  createConversation,
  fetchConversationMessages,
  fetchConversations,
  startDiscussion,
  watchConversationEvents,
  type Conversation,
} from '@/api/conversation'
import {
  WORKSPACE_COURSES,
  WORKSPACE_SESSIONS,
  getCourse,
  getSessionMessages,
  type ChatMessage,
} from '@/data/workspace'
import { nextVisibleText } from '@/lib/typewriter'

const { t } = useI18n()
const router = useRouter()
const route = useRoute()

/* ── 布局：左栏 260 固定调宽，对话栏默认 340，课件栏吃剩余 ── */

const rail = useResizable({ initial: 260, min: 200, max: 360, side: 'left' })
const chat = useResizable({ initial: 340, min: 280, max: 560, side: 'left' })

/* ── 会话与课程状态 ── */

const sessions = ref(WORKSPACE_SESSIONS)
const courses = WORKSPACE_COURSES
const classroomId = computed(() => {
  const value = Number(route.params.classroomId)
  return Number.isSafeInteger(value) && value > 0 ? value : null
})

const activeSessionId = ref<string | null>('session-agent-tool')
/** 已打开的课程 Tab（按打开顺序）；默认把两门课都打开，还原截图 */
const openCourseIds = ref<string[]>(['course-agent-tool', 'course-rag'])
const activeCourseId = ref<string | null>('course-agent-tool')

/** 消息按会话本地可变副本；后端接入后改 SSE 流 */
const messagesBySession = ref<Record<string, ChatMessage[]>>(
  Object.fromEntries(sessions.value.map((s) => [s.id, getSessionMessages(s.id)])),
)

const activeSession = computed(
  () => sessions.value.find((s) => s.id === activeSessionId.value) ?? null,
)
const activeMessages = computed(() =>
  activeSessionId.value ? (messagesBySession.value[activeSessionId.value] ?? []) : [],
)

const loading = ref(false)
const loadError = ref<string | null>(null)
const sending = ref(false)
let streamController: AbortController | null = null
let typewriterTimer: number | null = null
let typewriterList: ChatMessage[] | null = null
const typewriterTargets = new Map<string, string>()
let streamedConversationId: number | null = null
let loadGeneration = 0
let sendInFlight = false
const lastSequenceByConversation = new Map<number, number>()

function sessionFromConversation(item: Conversation) {
  return {
    id: String(item.id),
    title: item.title,
    updatedAt: item.lastMessageAt
      ? new Date(item.lastMessageAt).toLocaleString()
      : new Date(item.updatedAt).toLocaleString(),
  }
}

function messageFromHistory(item: Awaited<ReturnType<typeof fetchConversationMessages>>[number]): ChatMessage {
  return {
    id: String(item.id),
    role: item.senderType === 'user' ? 'user' : 'assistant',
    text: item.content,
  }
}

function abortStream() {
  streamController?.abort()
  streamController = null
  streamedConversationId = null
  stopTypewriter()
}

function startTypewriter(list: ChatMessage[]) {
  typewriterList = list
  if (typewriterTimer !== null) return
  typewriterTimer = window.setInterval(() => {
    const currentList = typewriterList
    if (!currentList) return
    let waiting = false
    for (const message of currentList) {
      const target = typewriterTargets.get(message.id)
      if (target == null) continue
      message.text = nextVisibleText(message.text, target)
      waiting ||= message.text.length < target.length
    }
    if (!waiting) stopTypewriter()
  }, 24)
}

function stopTypewriter() {
  if (typewriterTimer !== null) {
    window.clearInterval(typewriterTimer)
    typewriterTimer = null
  }
  typewriterList = null
  typewriterTargets.clear()
}

async function loadConversation(item: Conversation, generation: number) {
  const id = String(item.id)
  sessions.value = sessions.value.filter((session) => session.id !== id).concat(sessionFromConversation(item))
  activeSessionId.value = id
  const messages = await fetchConversationMessages(item.id)
  if (generation !== loadGeneration || activeSessionId.value !== id) return
  messagesBySession.value[id] = messages.map(messageFromHistory)
  subscribeToConversation(item.id, messagesBySession.value[id])
}

async function loadRemoteSessions() {
  const generation = ++loadGeneration
  abortStream()
  if (!classroomId.value) {
    sessions.value = WORKSPACE_SESSIONS
    messagesBySession.value = Object.fromEntries(sessions.value.map((s) => [s.id, getSessionMessages(s.id)]))
    activeSessionId.value = 'session-agent-tool'
    loadError.value = null
    loading.value = false
    return
  }
  loading.value = true
  loadError.value = null
  try {
    const conversations = await fetchConversations(classroomId.value)
    if (generation !== loadGeneration) return
    sessions.value = conversations.map(sessionFromConversation)
    messagesBySession.value = {}
    const first = conversations[0]
    if (first) await loadConversation(first, generation)
    else activeSessionId.value = null
  } catch (error) {
    if (generation === loadGeneration) {
      loadError.value = error instanceof Error ? error.message : String(error)
      sessions.value = []
      messagesBySession.value = {}
      activeSessionId.value = null
    }
  } finally {
    if (generation === loadGeneration) loading.value = false
  }
}

async function consumeEvents(
  conversationId: number,
  list: ChatMessage[],
  signal: AbortSignal,
) {
  const streamingMessageIds = new Set<string>()
  while (!signal.aborted) {
    try {
      const after = lastSequenceByConversation.get(conversationId) ?? 0
      for await (const event of watchConversationEvents(conversationId, { after, signal })) {
        if (signal.aborted) return
        if (event.sequenceNo <= (lastSequenceByConversation.get(conversationId) ?? 0)) continue
        lastSequenceByConversation.set(conversationId, event.sequenceNo)
        loadError.value = null
        if (event.eventType === 'message.completed') {
          const message = event.payload
          const existing = list.find((item) => item.id === String(message.message_id))
          if (existing) existing.text = message.content
          else list.push({ id: String(message.message_id), role: 'assistant', text: message.content })
          typewriterTargets.delete(String(message.message_id))
          streamingMessageIds.delete(String(message.message_id))
        }
        if (event.eventType === 'message.delta') {
          const message = event.payload
          const messageId = String(message.message_id)
          const existing = list.find((item) => item.id === messageId)
          if (existing && !streamingMessageIds.has(messageId)) continue
          const target = (typewriterTargets.get(messageId) ?? existing?.text ?? '') + message.delta
          typewriterTargets.set(messageId, target)
          if (existing && streamingMessageIds.has(messageId)) existing.text = nextVisibleText(existing.text, target)
          else if (!existing) {
            list.push({ id: messageId, role: 'assistant', text: nextVisibleText('', target) })
            streamingMessageIds.add(messageId)
          }
          startTypewriter(list)
        }
        if (event.eventType === 'run.failed') {
          list.push({
            id: `error-${conversationId}-${event.sequenceNo}`,
            role: 'assistant',
            text: event.payload.error || '讨论执行失败',
          })
        }
      }
      if (!signal.aborted) throw new Error('事件流已中断')
    } catch (error) {
      if (signal.aborted) return
      loadError.value = error instanceof Error ? error.message : String(error)
      await new Promise<void>((resolve) => {
        const timeout = window.setTimeout(resolve, 1500)
        signal.addEventListener('abort', () => { window.clearTimeout(timeout); resolve() }, { once: true })
      })
    }
  }
}

function subscribeToConversation(conversationId: number, list: ChatMessage[]) {
  if (streamedConversationId === conversationId && streamController && !streamController.signal.aborted) return
  abortStream()
  const controller = new AbortController()
  streamController = controller
  streamedConversationId = conversationId
  void consumeEvents(conversationId, list, controller.signal)
}

const openCourses = computed(() =>
  openCourseIds.value.map((id) => getCourse(id)).filter((c) => c != null),
)

/* ── 交互 ── */

async function selectSession(id: string) {
  const generation = ++loadGeneration
  abortStream()
  activeSessionId.value = id
  if (classroomId.value) {
    const numericId = Number(id)
    if (Number.isSafeInteger(numericId)) {
      loadError.value = null
      try {
        const items = await fetchConversationMessages(numericId)
        if (generation !== loadGeneration || activeSessionId.value !== id) return
        messagesBySession.value[id] = items.map(messageFromHistory)
        subscribeToConversation(numericId, messagesBySession.value[id])
      } catch (error) {
        if (generation === loadGeneration) loadError.value = error instanceof Error ? error.message : String(error)
      }
    }
  }
}

function newSession() {
  ++loadGeneration
  abortStream()
  activeSessionId.value = null
}

function openCourse(id: string) {
  if (!openCourseIds.value.includes(id)) openCourseIds.value.push(id)
  activeCourseId.value = id
}

function closeCourse(id: string) {
  const i = openCourseIds.value.indexOf(id)
  if (i === -1) return
  openCourseIds.value.splice(i, 1)
  if (activeCourseId.value === id) {
    activeCourseId.value = openCourseIds.value[Math.max(0, i - 1)] ?? null
  }
}

let msgSeq = 0
async function send(text: string) {
  if (!classroomId.value) {
    // 没有课堂 ID 时保留工作区的离线预览行为。
    if (!activeSessionId.value) {
      const id = `session-local-${++msgSeq}`
      sessions.value.unshift({ id, title: text.slice(0, 24), updatedAt: t('workspace.timeJustNow') })
      messagesBySession.value[id] = []
      activeSessionId.value = id
    }
    const sid = activeSessionId.value
    const list = messagesBySession.value[sid] ?? (messagesBySession.value[sid] = [])
    list.push({ id: `u-${Date.now()}`, role: 'user', text })
    window.setTimeout(() => list.push({ id: `a-${Date.now()}`, role: 'assistant', text: t('workspace.mockReply') }), 600)
    return
  }

  if (sendInFlight) return
  sendInFlight = true
  sending.value = true

  let list: ChatMessage[] | undefined
  let pendingMessage: ChatMessage | undefined
  loadError.value = null
  const generation = loadGeneration
  try {
    let conversationId = activeSessionId.value ? Number(activeSessionId.value) : NaN
    if (!Number.isSafeInteger(conversationId)) {
      const created = await createConversation(classroomId.value, { title: text.slice(0, 40), type: 'discussion' })
      sessions.value.unshift(sessionFromConversation(created))
      activeSessionId.value = String(created.id)
      messagesBySession.value[String(created.id)] = []
      conversationId = created.id
    }
    list = messagesBySession.value[String(conversationId)] ?? (messagesBySession.value[String(conversationId)] = [])
    pendingMessage = { id: `pending-${Date.now()}`, role: 'user', text }
    list.push(pendingMessage)
    const started = await startDiscussion(conversationId, text)
    pendingMessage.id = String(started.messageId)
    if (generation === loadGeneration && activeSessionId.value === String(conversationId)) {
      subscribeToConversation(conversationId, list)
    }
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : String(error)
    if (list && pendingMessage) {
      const index = list.indexOf(pendingMessage)
      if (index >= 0) list.splice(index, 1)
      list.push({ id: `error-${Date.now()}`, role: 'assistant', text: error instanceof Error ? error.message : String(error) })
    }
  } finally {
    sendInFlight = false
    sending.value = false
  }
}

onMounted(() => void loadRemoteSessions())
watch(classroomId, () => void loadRemoteSessions())
onBeforeUnmount(() => abortStream())

function startLearning(courseId: string) {
  router.push({ name: 'classroom', params: { id: courseId } })
}
</script>

<template>
  <div class="narra-page narra-workspace flex h-[100dvh] w-full overflow-hidden bg-background" data-testid="pro-workspace">
    <!-- 左导航 -->
    <div
      class="relative h-full shrink-0"
      :style="{ width: rail.displayWidth.value, transition: rail.dragging.value ? 'none' : 'width .2s ease' }"
    >
      <WorkspaceRail
        v-show="!rail.collapsed.value"
        :sessions="sessions"
        :courses="courses"
        :active-session-id="activeSessionId"
        :active-course-id="activeCourseId"
        @select-session="selectSession"
        @select-course="openCourse"
        @new-session="newSession"
      />
      <!-- 调宽手柄 -->
      <div
        v-show="!rail.collapsed.value"
        class="group absolute top-0 right-0 bottom-0 z-40 w-1.5 cursor-col-resize"
        @mousedown="rail.start"
      >
        <div
          class="absolute top-1/2 right-0.5 h-8 w-0.5 -translate-y-1/2 rounded-full bg-border transition-colors group-hover:bg-brand-400"
        />
      </div>
    </div>

    <!-- 中：对话栏 -->
    <div
      class="relative h-full shrink-0 border-r border-border"
      :style="{
        width: chat.collapsed.value ? '0px' : chat.displayWidth.value,
        transition: chat.dragging.value ? 'none' : 'width .2s ease',
      }"
    >
      <WorkspaceChatPane
        v-show="!chat.collapsed.value"
        :title="activeSession?.title ?? null"
        :messages="activeMessages"
        :error="loadError"
        :sending="sending"
        collapsible
        @collapse="chat.toggle"
        @open-course="openCourse"
        @send="send"
        @retry="loadRemoteSessions"
      />
      <!-- 对话栏调宽手柄 -->
      <div
        v-show="!chat.collapsed.value"
        class="group absolute top-0 right-0 bottom-0 z-40 w-1.5 cursor-col-resize"
        @mousedown="chat.start"
      >
        <div
          class="absolute top-1/2 right-0.5 h-8 w-0.5 -translate-y-1/2 rounded-full bg-border transition-colors group-hover:bg-brand-400"
        />
      </div>
      <!-- 折叠后的展开把手 -->
      <button
        v-if="chat.collapsed.value"
        type="button"
        :title="t('workspace.chats')"
        class="absolute top-3 left-2 z-40 rounded-md border border-border bg-background p-1.5 text-muted-foreground shadow-sm transition-colors hover:text-foreground"
        @click="chat.toggle"
      >
        <PanelLeftOpen class="size-4" />
      </button>
    </div>

    <!-- 右：课件栏 -->
    <WorkspaceClassroomPane
      :open-courses="openCourses"
      :active-course-id="activeCourseId"
      @activate-course="activeCourseId = $event"
      @close-course="closeCourse"
      @start-learning="startLearning"
    />
  </div>
</template>
