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
 * 数据为 mock（src/data/workspace.ts），接 Go 后端后替换为接口。
 */
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { PanelLeftOpen } from 'lucide-vue-next'

import WorkspaceRail from '@/components/workspace/WorkspaceRail.vue'
import WorkspaceChatPane from '@/components/workspace/WorkspaceChatPane.vue'
import WorkspaceClassroomPane from '@/components/workspace/WorkspaceClassroomPane.vue'
import { useResizable } from '@/composables/useResizable'
import {
  WORKSPACE_COURSES,
  WORKSPACE_SESSIONS,
  getCourse,
  getSessionMessages,
  type ChatMessage,
} from '@/data/workspace'

const { t } = useI18n()
const router = useRouter()

/* ── 布局：左栏 260 固定调宽，对话栏默认 340，课件栏吃剩余 ── */

const rail = useResizable({ initial: 260, min: 200, max: 360, side: 'left' })
const chat = useResizable({ initial: 340, min: 280, max: 560, side: 'left' })

/* ── 会话与课程状态 ── */

const sessions = ref(WORKSPACE_SESSIONS)
const courses = WORKSPACE_COURSES

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

const openCourses = computed(() =>
  openCourseIds.value.map((id) => getCourse(id)).filter((c) => c != null),
)

/* ── 交互 ── */

function selectSession(id: string) {
  activeSessionId.value = id
}

function newSession() {
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
function send(text: string) {
  // 空态首条消息：创建新会话（对齐原版「draft conversation」行为）
  if (!activeSessionId.value) {
    const id = `session-local-${++msgSeq}`
    sessions.value.unshift({ id, title: text.slice(0, 24), updatedAt: t('workspace.timeJustNow') })
    messagesBySession.value[id] = []
    activeSessionId.value = id
  }
  const sid = activeSessionId.value
  const list = messagesBySession.value[sid] ?? (messagesBySession.value[sid] = [])
  list.push({ id: `u-${Date.now()}`, role: 'user', text })
  // mock：模拟 Agent 应答
  window.setTimeout(() => {
    list.push({ id: `a-${Date.now()}`, role: 'assistant', text: t('workspace.mockReply') })
  }, 600)
}

function startLearning(courseId: string) {
  router.push({ name: 'classroom', params: { id: courseId } })
}
</script>

<template>
  <div class="flex h-[100dvh] w-full overflow-hidden bg-background" data-testid="pro-workspace">
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
          class="absolute top-1/2 right-0.5 h-8 w-0.5 -translate-y-1/2 rounded-full bg-border transition-colors group-hover:bg-violet-400"
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
        collapsible
        @collapse="chat.toggle"
        @open-course="openCourse"
        @send="send"
      />
      <!-- 对话栏调宽手柄 -->
      <div
        v-show="!chat.collapsed.value"
        class="group absolute top-0 right-0 bottom-0 z-40 w-1.5 cursor-col-resize"
        @mousedown="chat.start"
      >
        <div
          class="absolute top-1/2 right-0.5 h-8 w-0.5 -translate-y-1/2 rounded-full bg-border transition-colors group-hover:bg-violet-400"
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
