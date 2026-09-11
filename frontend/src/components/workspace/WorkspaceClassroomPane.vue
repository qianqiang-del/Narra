<script setup lang="ts">
/**
 * WorkspaceClassroomPane —— 右侧课件栏。
 *
 * 头部：课程 Tab 条（切换/关闭，关闭最后一个即收起整栏）+ 开始学习。
 * 主体：左课件缩略图栏（序号 + 标题 + SlideFrame 缩略图）+
 *      右 16:9 主画布（SlideFrame interactive）。
 *
 * 只读预览：不提供任何元素级编辑入口，修改课件一律走左侧对话。
 */
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Play, X } from 'lucide-vue-next'

import SlideFrame from '@/components/workspace/SlideFrame.vue'
import type { WorkspaceCourse } from '@/data/workspace'
import { cn } from '@/lib/utils'

const props = defineProps<{
  /** 已打开的课程 Tab（按打开顺序） */
  openCourses: WorkspaceCourse[]
  activeCourseId: string | null
}>()

const emit = defineEmits<{
  (e: 'activate-course', id: string): void
  (e: 'close-course', id: string): void
  (e: 'start-learning', id: string): void
}>()

const { t } = useI18n()

const activeCourse = computed(
  () => props.openCourses.find((c) => c.id === props.activeCourseId) ?? null,
)

const activePageId = ref<string | null>(null)

watch(
  activeCourse,
  (course) => {
    activePageId.value = course?.pages[0]?.id ?? null
  },
  { immediate: true },
)

const activePage = computed(
  () => activeCourse.value?.pages.find((p) => p.id === activePageId.value) ?? null,
)

const activePageIndex = computed(() => {
  if (!activeCourse.value || !activePageId.value) return -1
  return activeCourse.value.pages.findIndex((p) => p.id === activePageId.value)
})
</script>

<template>
  <section
    class="flex h-full min-w-0 flex-1 flex-col bg-muted/30"
    data-testid="workspace-classroom-pane"
  >
    <!-- 头部：课程 Tab 条 + 开始学习 -->
    <header class="flex h-12 shrink-0 items-center gap-1 border-b border-border bg-background px-2">
      <div class="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto">
        <div
          v-for="course in openCourses"
          :key="course.id"
          :class="
            cn(
              'group flex h-8 shrink-0 items-center gap-1.5 rounded-lg pr-1.5 pl-3 text-[12px] font-medium transition-colors',
              course.id === activeCourseId
                ? 'bg-violet-500/10 text-violet-700 dark:text-violet-300'
                : 'text-muted-foreground hover:bg-accent hover:text-foreground',
            )
          "
        >
          <button
            type="button"
            class="max-w-44 truncate"
            @click="emit('activate-course', course.id)"
          >
            {{ course.title }}
          </button>
          <button
            type="button"
            :title="t('workspace.closeCourse')"
            :class="
              cn(
                'rounded p-0.5 transition-all',
                course.id === activeCourseId
                  ? 'text-violet-500 hover:bg-violet-500/15'
                  : 'text-muted-foreground/50 hover:bg-accent hover:text-foreground',
              )
            "
            @click="emit('close-course', course.id)"
          >
            <X class="size-3" />
          </button>
        </div>
      </div>

      <button
        type="button"
        :disabled="!activeCourse"
        class="inline-flex h-8 shrink-0 items-center gap-1.5 rounded-lg bg-violet-600/10 px-3 text-[11px] font-medium text-violet-700 transition-all hover:bg-violet-600/15 hover:shadow-sm disabled:pointer-events-none disabled:opacity-40 dark:text-violet-300"
        @click="activeCourse && emit('start-learning', activeCourse.id)"
      >
        <Play class="size-3.5" />
        {{ t('workspace.startLearning') }}
      </button>
    </header>

    <!-- 主体 -->
    <div v-if="activeCourse" class="flex min-h-0 flex-1">
      <!-- 缩略图栏 -->
      <aside
        class="flex w-52 shrink-0 flex-col gap-2 overflow-y-auto border-r border-border bg-background/60 p-2.5"
      >
        <button
          v-for="(page, i) in activeCourse.pages"
          :key="page.id"
          type="button"
          :class="
            cn(
              'group flex w-full flex-col gap-1.5 rounded-lg p-1.5 text-left transition-all',
              page.id === activePageId
                ? 'bg-violet-500/10 ring-1 ring-violet-300 dark:ring-violet-700'
                : 'hover:bg-accent',
            )
          "
          @click="activePageId = page.id"
        >
          <div class="flex items-center gap-1.5">
            <span
              :class="
                cn(
                  'flex h-4 w-4 shrink-0 items-center justify-center rounded-full text-[10px] font-bold',
                  page.id === activePageId
                    ? 'bg-violet-600 text-white'
                    : 'bg-muted text-muted-foreground',
                )
              "
            >
              {{ i + 1 }}
            </span>
            <span
              :class="
                cn(
                  'truncate text-xs font-semibold',
                  page.id === activePageId
                    ? 'text-violet-700 dark:text-violet-300'
                    : 'text-foreground/70',
                )
              "
            >
              {{ page.title }}
            </span>
          </div>
          <div
            :class="
              cn(
                'relative aspect-video w-full overflow-hidden rounded-md bg-white ring-1',
                page.id === activePageId
                  ? 'ring-violet-300 dark:ring-violet-700'
                  : 'ring-black/5 dark:ring-white/10',
              )
            "
          >
            <SlideFrame :html="page.html" />
          </div>
        </button>
      </aside>

      <!-- 主画布 -->
      <div class="flex min-w-0 flex-1 flex-col items-center justify-center gap-3 p-6">
        <div
          class="relative aspect-video max-h-full w-full max-w-full overflow-hidden rounded-xl bg-white shadow-lg ring-1 ring-black/5 dark:ring-white/10"
        >
          <SlideFrame v-if="activePage" :key="activePage.id" :html="activePage.html" interactive />
        </div>
        <p class="shrink-0 text-[11px] text-muted-foreground tabular-nums">
          {{ activePageIndex + 1 }} / {{ activeCourse.pages.length }}
        </p>
      </div>
    </div>

    <!-- 无课程打开 -->
    <div
      v-else
      class="flex flex-1 flex-col items-center justify-center gap-2 text-muted-foreground"
    >
      <p class="text-sm">{{ t('workspace.noCourseOpen') }}</p>
      <p class="text-xs">{{ t('workspace.noCourseOpenHint') }}</p>
    </div>
  </section>
</template>
