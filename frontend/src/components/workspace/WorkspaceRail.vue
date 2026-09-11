<script setup lang="ts">
/**
 * WorkspaceRail —— 工作区左侧导航栏（简化版）。
 *
 * 还原截图：Logo + PRO 徽标 / 新建对话按钮 / 搜索框 /
 * 「对话 | 课程」Tab / 列表（课程带页数徽标）/ 底部筛选占位。
 * 原版 WorkspaceRail.tsx 的文件夹分组、拖拽、右键菜单不做。
 */
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Bookmark, Plus, Search, SlidersHorizontal, SquarePen } from 'lucide-vue-next'

import type { CourseItem, SessionItem } from '@/data/workspace'
import { cn } from '@/lib/utils'

const props = defineProps<{
  sessions: SessionItem[]
  courses: CourseItem[]
  activeSessionId: string | null
  activeCourseId: string | null
}>()

const emit = defineEmits<{
  (e: 'select-session', id: string): void
  (e: 'select-course', id: string): void
  (e: 'new-session'): void
}>()

const { t } = useI18n()

type TabKey = 'sessions' | 'courses'
const tab = ref<TabKey>('courses')
const keyword = ref('')

const filteredSessions = computed(() =>
  props.sessions.filter((s) => !keyword.value || s.title.includes(keyword.value)),
)
const filteredCourses = computed(() =>
  props.courses.filter((c) => !keyword.value || c.title.includes(keyword.value)),
)
</script>

<template>
  <aside
    class="flex h-full w-full flex-col border-r border-border bg-muted/40"
    data-testid="workspace-rail"
  >
    <!-- Logo 头 -->
    <div class="flex h-14 shrink-0 items-center gap-2 px-4">
      <img src="/logo-horizontal.png" alt="Narra" class="h-5" />
      <span
        class="rounded bg-gradient-to-r from-violet-600 to-fuchsia-500 px-1.5 py-0.5 text-[10px] font-bold tracking-wide text-white"
      >
        PRO
      </span>
    </div>

    <!-- 新建对话 -->
    <div class="shrink-0 px-3 pb-2">
      <button
        type="button"
        class="flex h-9 w-full items-center gap-2 rounded-lg border border-border bg-background px-3 text-[13px] text-muted-foreground shadow-sm transition-colors hover:bg-accent hover:text-foreground"
        @click="emit('new-session')"
      >
        <Plus class="size-4" />
        {{ t('workspace.newSession') }}
      </button>
    </div>

    <!-- Tab：对话 / 课程 -->
    <div class="shrink-0 px-3 pt-1">
      <div class="flex h-8 items-center rounded-lg bg-muted p-0.5">
        <button
          v-for="key in (['sessions', 'courses'] as TabKey[])"
          :key="key"
          type="button"
          :class="
            cn(
              'flex h-7 flex-1 items-center justify-center rounded-md text-xs font-medium transition-colors',
              tab === key
                ? 'bg-background text-foreground shadow-sm'
                : 'text-muted-foreground hover:text-foreground',
            )
          "
          @click="tab = key"
        >
          {{ key === 'sessions' ? t('workspace.chats') : t('workspace.courses') }}
        </button>
      </div>
    </div>

    <!-- 搜索 -->
    <div class="shrink-0 px-3 pt-2">
      <div
        class="flex h-8 items-center gap-2 rounded-lg border border-border bg-background px-2.5"
      >
        <Search class="size-3.5 shrink-0 text-muted-foreground" />
        <input
          v-model="keyword"
          type="text"
          :placeholder="t('workspace.searchPlaceholder')"
          class="h-full min-w-0 flex-1 bg-transparent text-xs outline-none placeholder:text-muted-foreground/70"
        />
      </div>
    </div>

    <!-- 列表 -->
    <div class="min-h-0 flex-1 overflow-y-auto px-3 py-2">
      <!-- 对话列表 -->
      <template v-if="tab === 'sessions'">
        <button
          v-for="s in filteredSessions"
          :key="s.id"
          type="button"
          :class="
            cn(
              'flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left transition-colors',
              s.id === activeSessionId
                ? 'bg-violet-500/10 text-violet-700 dark:text-violet-300'
                : 'text-foreground/80 hover:bg-accent',
            )
          "
          @click="emit('select-session', s.id)"
        >
          <SquarePen class="size-3.5 shrink-0 opacity-50" />
          <span class="min-w-0 flex-1 truncate text-[13px] font-medium">{{ s.title }}</span>
          <span class="shrink-0 text-[10px] text-muted-foreground">{{ s.updatedAt }}</span>
        </button>
        <p
          v-if="filteredSessions.length === 0"
          class="px-2.5 py-6 text-center text-xs text-muted-foreground"
        >
          {{ keyword ? t('workspace.searchEmpty') : t('workspace.sessionsEmpty') }}
        </p>
      </template>

      <!-- 课程列表 -->
      <template v-else>
        <button
          v-for="c in filteredCourses"
          :key="c.id"
          type="button"
          :class="
            cn(
              'flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left transition-colors',
              c.id === activeCourseId
                ? 'bg-violet-500/10 text-violet-700 dark:text-violet-300'
                : 'text-foreground/80 hover:bg-accent',
            )
          "
          @click="emit('select-course', c.id)"
        >
          <Bookmark class="size-3.5 shrink-0 opacity-50" />
          <span class="min-w-0 flex-1 truncate text-[13px] font-medium">{{ c.title }}</span>
          <span
            class="shrink-0 rounded-full bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground"
          >
            {{ t('workspace.sceneCount', { count: c.pageCount }) }}
          </span>
        </button>
        <p
          v-if="filteredCourses.length === 0"
          class="px-2.5 py-6 text-center text-xs text-muted-foreground"
        >
          {{ keyword ? t('workspace.searchEmpty') : t('workspace.coursesEmpty') }}
        </p>
      </template>
    </div>

    <!-- 底部筛选占位 -->
    <div
      class="flex h-10 shrink-0 items-center gap-3 border-t border-border px-4 text-[11px] text-muted-foreground"
    >
      <span class="flex items-center gap-1">
        <SlidersHorizontal class="size-3" />
        {{ t('workspace.filterBar') }}
      </span>
    </div>
  </aside>
</template>
