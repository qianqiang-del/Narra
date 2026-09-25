<script setup lang="ts">
/**
 * SceneSidebar —— 文档 §6.4（左侧场景栏，默认 220 / 170~400，可折叠）。
 * 缩略图按场景类型（slide/quiz/interactive/pbl）用不同配色与示意图。
 */
import { useI18n } from 'vue-i18n'
import {
  BookOpen,
  Check,
  Cpu,
  MousePointer2,
  PanelLeftClose,
  PanelLeftOpen,
  PieChart,
  RefreshCw,
  Trophy,
} from 'lucide-vue-next'

import { SCENE_TYPE_STYLES, type Scene, type SceneType } from '@/types/scene'
import { cn } from '@/lib/utils'
import SceneRenderer from '@/components/classroom/SceneRenderer.vue'

const props = defineProps<{
  scenes: Scene[]
  activeId: string
  collapsed: boolean
  width: number
}>()

const emit = defineEmits<{
  (e: 'select', id: string): void
  (e: 'retry', id: string): void
  (e: 'toggle-collapse'): void
  (e: 'resize-start', ev: MouseEvent): void
}>()

const { t } = useI18n()

const TYPE_ICON: Record<SceneType, typeof BookOpen> = {
  slide: BookOpen,
  quiz: PieChart,
  interactive: MousePointer2,
  pbl: Cpu,
  complete: Trophy,
}

const TYPE_LABEL_KEY: Record<SceneType, string> = {
  slide: 'scene.typeSlide',
  quiz: 'scene.typeQuiz',
  interactive: 'scene.typeInteractive',
  pbl: 'scene.typePbl',
  complete: 'scene.typeComplete',
}
</script>

<template>
  <aside
    class="relative z-20 flex shrink-0 flex-col border-r border-gray-100 bg-white/80 shadow-[2px_0_24px_rgba(0,0,0,0.02)] backdrop-blur-xl dark:border-gray-800 dark:bg-slate-900/80"
    :style="{
      width: collapsed ? '0px' : `${width}px`,
      transition: 'width 0.3s ease',
      overflow: 'hidden',
    }"
  >
    <!-- Logo 头 -->
    <div class="mt-3 mb-1 flex h-10 shrink-0 items-center justify-between px-3">
      <img src="/logo-horizontal.png" alt="Narra" class="h-6" />
      <button
        type="button"
        class="rounded-md p-1 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-700 dark:hover:bg-gray-800"
        @click="emit('toggle-collapse')"
      >
        <PanelLeftClose class="size-4" />
      </button>
    </div>

    <!-- 场景列表 -->
    <div class="scrollbar-hide flex-1 space-y-2 overflow-y-auto p-2 pt-1">
      <button
        v-for="(scene, i) in scenes"
        :key="scene.id"
        type="button"
        :class="
          cn(
            'group relative flex w-full cursor-pointer flex-col gap-1 rounded-lg p-1.5 text-left transition-all duration-200',
            scene.status === 'failed'
              ? 'bg-red-50/30 ring-1 ring-red-100 dark:bg-red-950/20'
              : scene.status === 'complete'
                ? 'bg-amber-50 ring-1 ring-amber-200 dark:bg-amber-950/20'
                : scene.id === activeId
                  ? 'bg-purple-50 ring-1 ring-purple-200 dark:bg-purple-900/20'
                  : 'hover:bg-gray-50/80 dark:hover:bg-gray-800/50',
          )
        "
        @click="emit('select', scene.id)"
      >
        <!-- 序号 + 标题 -->
        <div class="flex items-center gap-1.5">
          <span
            :class="
              cn(
                'flex h-4 w-4 shrink-0 items-center justify-center rounded-full text-[10px] font-black',
                scene.status === 'complete'
                  ? 'bg-amber-500 text-white'
                  : scene.id === activeId
                    ? 'bg-purple-600 text-white shadow-sm shadow-purple-500/30'
                    : 'bg-gray-200 text-gray-500 dark:bg-gray-700 dark:text-gray-400',
              )
            "
          >
            {{ i + 1 }}
          </span>
          <span
            :class="
              cn(
                'truncate text-xs font-bold',
                scene.status === 'failed'
                  ? 'text-red-600'
                  : scene.id === activeId
                    ? 'text-purple-700 dark:text-purple-300'
                    : 'text-gray-600 dark:text-gray-300',
              )
            "
          >
            {{ scene.title }}
          </span>
        </div>

        <!-- 缩略图 -->
        <div
          :class="
            cn(
              'relative aspect-video w-full overflow-hidden rounded ring-1',
              SCENE_TYPE_STYLES[scene.type].ring,
              'bg-gradient-to-br',
              SCENE_TYPE_STYLES[scene.type].gradient,
            )
          "
        >
          <div class="absolute inset-0 z-10 overflow-hidden bg-white dark:bg-gray-800">
            <div class="pointer-events-none origin-top-left scale-[0.19]" style="width: 526%; height: 526%">
              <SceneRenderer :scene="scene" />
            </div>
          </div>
          <!-- 生成中：骨架 + shimmer -->
          <template v-if="scene.status === 'generating' || scene.status === 'pending'">
            <div class="flex size-full flex-col justify-center gap-1.5 p-2">
              <div class="h-1.5 w-3/4 animate-pulse rounded-full bg-white/70" />
              <div class="h-1.5 w-1/2 animate-pulse rounded-full bg-white/60" />
              <div class="h-1.5 w-2/3 animate-pulse rounded-full bg-white/50" />
            </div>
            <div
              class="absolute inset-0 animate-[shimmer_2s_infinite] bg-gradient-to-r from-transparent via-white/40 to-transparent"
            />
          </template>

          <!-- 失败 -->
          <template v-else-if="scene.status === 'failed'">
            <div class="flex size-full flex-col items-center justify-center gap-1">
              <RefreshCw
                :class="cn('size-4 text-red-400', scene.status === 'failed' && '')"
              />
              <span class="text-[9px] font-medium text-red-500">{{ t('scene.statusFailed') }}</span>
            </div>
          </template>

          <!-- 完成 -->
          <template v-else-if="scene.status === 'complete' || scene.type === 'complete'">
            <div class="relative flex size-full items-center justify-center">
              <span class="absolute size-8 animate-pulse rounded-full bg-amber-300/40" />
              <Trophy class="relative size-8 text-amber-500" />
            </div>
          </template>

          <!-- 正常：按类型画示意图 -->
          <template v-else>
            <!-- slide -->
            <div v-if="scene.type === 'slide'" class="flex size-full flex-col gap-1 overflow-hidden bg-gradient-to-br from-violet-50 to-blue-50 p-2">
              <div class="truncate text-[8px] font-bold text-gray-800/90">{{ scene.slide?.heading || scene.title }}</div>
              <div
                v-for="(bullet, bulletIndex) in (scene.slide?.bullets || []).slice(0, 3)"
                :key="bulletIndex"
                class="truncate rounded bg-white/70 px-1 py-0.5 text-[7px] leading-tight text-gray-700/80"
              >
                {{ bullet }}
              </div>
              <div v-if="scene.slide?.accent" class="mt-auto truncate rounded bg-violet-100/80 px-1 py-0.5 text-[6px] font-medium text-violet-700/80">
                {{ scene.slide.accent }}
              </div>
            </div>

            <!-- quiz：2×2 选项格 -->
            <div v-else-if="scene.type === 'quiz'" class="flex size-full flex-col justify-center gap-1 p-2">
              <div class="mb-0.5 h-1.5 w-3/4 rounded-full bg-white/80" />
              <div class="grid grid-cols-2 gap-1">
                <div v-for="n in 4" :key="n" class="h-2.5 rounded-sm bg-white/60" />
              </div>
            </div>

            <!-- interactive：伪浏览器窗口 -->
            <div v-else-if="scene.type === 'interactive'" class="flex size-full flex-col p-1.5">
              <div class="flex items-center gap-0.5 pb-1">
                <span class="size-1 rounded-full bg-red-400" />
                <span class="size-1 rounded-full bg-amber-400" />
                <span class="size-1 rounded-full bg-emerald-400" />
                <div class="ml-1 h-1 flex-1 rounded-full bg-white/60" />
              </div>
              <div class="flex-1 rounded-sm bg-white/70" />
            </div>

            <!-- pbl：3 列看板 -->
            <div v-else class="grid size-full grid-cols-3 gap-1 p-1.5">
              <div v-for="n in 3" :key="n" class="flex flex-col gap-0.5 rounded-sm bg-white/50 p-1">
                <div class="h-1 w-full rounded-full bg-white/80" />
                <div class="h-1.5 w-full rounded-sm bg-white/60" />
                <div class="h-1.5 w-2/3 rounded-sm bg-white/60" />
              </div>
            </div>
          </template>
        </div>

        <!-- 类型角标 -->
        <div class="flex items-center gap-1 text-[9px] text-gray-400">
          <component :is="TYPE_ICON[scene.type]" class="size-2.5" />
          {{ t(TYPE_LABEL_KEY[scene.type]) }}
          <Check v-if="scene.status === 'complete'" class="ml-auto size-2.5 text-amber-500" />
        </div>
      </button>
    </div>

    <!-- 拖拽调宽手柄 -->
    <div
      class="group absolute top-0 right-0 bottom-0 z-50 w-1.5 cursor-col-resize"
      @mousedown="emit('resize-start', $event)"
    >
      <div
        class="absolute top-1/2 right-0.5 h-8 w-0.5 -translate-y-1/2 rounded-full bg-gray-300 transition-colors group-hover:bg-purple-400"
      />
    </div>
  </aside>

  <!-- 折叠后的展开把手 -->
  <button
    v-if="collapsed"
    type="button"
    class="absolute top-4 left-2 z-30 rounded-md bg-white/80 p-1.5 text-gray-400 shadow-sm ring-1 ring-gray-100 backdrop-blur transition-colors hover:text-gray-700 dark:bg-slate-800/80 dark:ring-gray-700"
    @click="emit('toggle-collapse')"
  >
    <PanelLeftOpen class="size-4" />
  </button>
</template>
