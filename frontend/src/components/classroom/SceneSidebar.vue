<script setup lang="ts">
/**
 * SceneSidebar —— 文档 §6.4（左侧场景栏，默认 220 / 170~400，可折叠）。
 *
 * 列表来自大纲：每一页都在，包括还没生成完的。生成完的页画真实缩略图且可点；没生成完的
 * 页盖一层「正在生成中」、不可点，等它那一页就绪后由父组件重建列表，这里自然变成可点。
 */
import { useI18n } from 'vue-i18n'
import {
  BookOpen,
  Check,
  MousePointer2,
  PanelLeftClose,
  PanelLeftOpen,
  PieChart,
  RefreshCw,
  Trophy,
} from 'lucide-vue-next'

import { interactiveHTML, SCENE_TYPE_STYLES, type Scene, type SceneType } from '@/types/scene'
import { cn } from '@/lib/utils'
import InteractiveIframeRenderer from '@/components/classroom/InteractiveIframeRenderer.vue'
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
  complete: Trophy,
}

const TYPE_LABEL_KEY: Record<SceneType, string> = {
  slide: 'scene.typeSlide',
  quiz: 'scene.typeQuiz',
  interactive: 'scene.typeInteractive',
  complete: 'scene.typeComplete',
}

/** 只有生成完的页进得去；没生成完的页点不动。 */
function viewable(scene: Scene): boolean {
  return scene.status === 'ready' || scene.type === 'complete'
}

function onSelect(scene: Scene) {
  if (!viewable(scene)) return
  emit('select', scene.id)
}

/** 还没生成完：pending / generating 都算。 */
function generating(scene: Scene): boolean {
  return scene.status === 'generating' || scene.status === 'pending'
}
</script>

<template>
  <aside
    class="relative z-20 flex shrink-0 flex-col border-r border-gray-100 bg-card/80 shadow-[2px_0_24px_rgba(0,0,0,0.02)] backdrop-blur-xl dark:border-gray-800 dark:bg-slate-900/80"
    :style="{
      width: collapsed ? '0px' : `${width}px`,
      transition: 'width 0.3s ease',
      overflow: 'hidden',
    }"
  >
    <!-- Logo 头（点击回首页） -->
    <div class="mt-3 mb-1 flex h-10 shrink-0 items-center justify-between px-3">
      <RouterLink
        to="/"
        class="min-w-0 transition-opacity hover:opacity-80"
        :title="t('common.backToHome')"
        aria-label="Narra"
      >
        <img src="/logo-horizontal.svg" alt="Narra" class="h-10 w-[148px] max-w-full object-contain" />
      </RouterLink>
      <button
        type="button"
        class="rounded-md p-1 text-gray-400 transition-colors hover:bg-muted hover:text-gray-700 dark:hover:bg-gray-800"
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
        :disabled="!viewable(scene) && scene.status !== 'failed'"
        :title="viewable(scene) || scene.status === 'failed' ? undefined : t('scene.statusGeneratingNow')"
        :class="
          cn(
            'group relative flex w-full flex-col gap-1 rounded-lg p-1.5 text-left transition-all duration-200',
            viewable(scene) ? 'cursor-pointer' : 'cursor-not-allowed',
            scene.status === 'failed'
              ? 'bg-red-50/30 ring-1 ring-red-100 dark:bg-red-950/20'
              : scene.status === 'complete'
                ? 'bg-gold-50 ring-1 ring-gold-200 dark:bg-gold-900/20'
                : scene.id === activeId
                  ? 'bg-brand-50 ring-1 ring-brand-200 dark:bg-brand-900/20'
                  : viewable(scene)
                    ? 'hover:bg-muted/80 dark:hover:bg-gray-800/50'
                    : 'opacity-80',
          )
        "
        @click="onSelect(scene)"
      >
        <!-- 序号 + 标题 -->
        <div class="flex items-center gap-1.5">
          <span
            :class="
              cn(
                'flex h-4 w-4 shrink-0 items-center justify-center rounded-full text-[10px] font-black',
                scene.status === 'complete'
                  ? 'bg-gold-500 text-white'
                  : scene.id === activeId
                    ? 'bg-brand-700 text-white shadow-sm shadow-brand-300/30'
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
                    ? 'text-brand-800 dark:text-brand-300'
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
          <!-- 正文层：生成完的页画真实内容（与主画布同源），未就绪的页留空，由状态层盖住 -->
          <div class="absolute inset-0 z-10 overflow-hidden bg-white dark:bg-gray-800">
            <!-- 交互页：同样是沙箱 iframe，缩略图模式禁鼠标事件 -->
            <InteractiveIframeRenderer
              v-if="interactiveHTML(scene)"
              :html="interactiveHTML(scene)"
              :interactive="false"
            />
            <div v-else class="pointer-events-none origin-top-left scale-[0.19]" style="width: 526%; height: 526%">
              <SceneRenderer :scene="scene" />
            </div>
          </div>

          <!-- 生成中：骨架 + shimmer + 文案，盖在正文层上（未就绪时下面那层是空的） -->
          <div
            v-if="generating(scene)"
            class="absolute inset-0 z-20 flex flex-col items-center justify-center gap-1.5 bg-white/90 dark:bg-gray-800/90"
          >
            <div class="flex w-3/4 flex-col gap-1">
              <div class="h-1.5 w-full animate-pulse rounded-full bg-gray-200 dark:bg-gray-600" />
              <div class="h-1.5 w-2/3 animate-pulse rounded-full bg-gray-200 dark:bg-gray-600" />
              <div class="h-1.5 w-5/6 animate-pulse rounded-full bg-gray-200 dark:bg-gray-600" />
            </div>
            <span class="text-[9px] font-medium text-gray-500 dark:text-gray-400">
              {{ t('scene.statusGeneratingNow') }}
            </span>
            <div
              class="pointer-events-none absolute inset-0 animate-[shimmer_2s_infinite] bg-gradient-to-r from-transparent via-white/40 to-transparent"
            />
          </div>

          <!-- 失败 -->
          <div
            v-else-if="scene.status === 'failed'"
            class="absolute inset-0 z-20 flex flex-col items-center justify-center gap-1 bg-red-50/90 dark:bg-red-950/60"
          >
            <RefreshCw class="size-4 text-red-400" />
            <span class="text-[9px] font-medium text-red-500">{{ t('scene.statusFailed') }}</span>
            <span
              role="button"
              tabindex="0"
              class="mt-0.5 inline-flex cursor-pointer items-center gap-0.5 rounded border border-red-300 bg-white/90 px-1.5 py-0.5 text-[9px] font-semibold text-red-600 shadow-sm transition-colors hover:bg-red-50 focus:outline-none focus:ring-1 focus:ring-red-400 dark:border-red-800 dark:bg-red-950/80 dark:text-red-300 dark:hover:bg-red-900"
              @click.stop="emit('retry', scene.id)"
              @keydown.enter.stop="emit('retry', scene.id)"
              @keydown.space.prevent.stop="emit('retry', scene.id)"
            >
              <RefreshCw class="size-2.5" />
              {{ t('scene.retry') }}
            </span>
          </div>

          <!-- 完成 -->
          <div
            v-else-if="scene.status === 'complete' || scene.type === 'complete'"
            class="absolute inset-0 z-20 flex items-center justify-center"
          >
            <span class="absolute size-8 animate-pulse rounded-full bg-gold-300/40" />
            <Trophy class="relative size-8 text-gold-500" />
          </div>
        </div>

        <!-- 类型角标 -->
        <div class="flex items-center gap-1 text-[9px] text-gray-400">
          <component :is="TYPE_ICON[scene.type]" class="size-2.5" />
          {{ t(TYPE_LABEL_KEY[scene.type]) }}
          <Check v-if="scene.status === 'complete'" class="ml-auto size-2.5 text-gold-500" />
        </div>
      </button>
    </div>

    <!-- 拖拽调宽手柄 -->
    <div
      class="group absolute top-0 right-0 bottom-0 z-50 w-1.5 cursor-col-resize"
      @mousedown="emit('resize-start', $event)"
    >
      <div
        class="absolute top-1/2 right-0.5 h-8 w-0.5 -translate-y-1/2 rounded-full bg-gray-300 transition-colors group-hover:bg-brand-400"
      />
    </div>
  </aside>

  <!-- 折叠后的展开把手 -->
  <button
    v-if="collapsed"
    type="button"
    class="absolute top-4 left-2 z-30 rounded-md bg-card/80 p-1.5 text-gray-400 shadow-sm ring-1 ring-gray-100 backdrop-blur transition-colors hover:text-gray-700 dark:bg-slate-800/80 dark:ring-gray-700"
    @click="emit('toggle-collapse')"
  >
    <PanelLeftOpen class="size-4" />
  </button>
</template>
