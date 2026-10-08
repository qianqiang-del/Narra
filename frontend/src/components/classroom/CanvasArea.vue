<script setup lang="ts">
/**
 * CanvasArea —— 文档 §6.5（舞台 16:9 盒子 + 底部工具栏）。
 * 播放提示按钮 / 场景序号徽章 / 生成中占位 / 课程完成页在此挂载。
 *
 * 场景内容铺满 16:9 画布，画布本身由外层舞台按可用空间约束。
 */
import { useI18n } from 'vue-i18n'
import { Loader2, Play } from 'lucide-vue-next'

import CanvasToolbar from '@/components/classroom/CanvasToolbar.vue'
import ClassroomComplete from '@/components/classroom/ClassroomComplete.vue'
import SceneRenderer from '@/components/classroom/SceneRenderer.vue'
import WhiteboardPanel from '@/components/classroom/WhiteboardPanel.vue'
import { cn } from '@/lib/utils'
import type { WhiteboardEntry } from '@/lib/classroomDiscussion'
import type { Scene } from '@/types/scene'

const props = defineProps<{
  scene: Scene
  index: number
  total: number
  playing: boolean
  volume: number
  speed: number
  autoPlay: boolean
  whiteboardOpen: boolean
  /** 全屏放映中：幻灯片铺满屏幕，工具栏悬浮 */
  presenting: boolean
  /** 放映时悬浮控件是否可见（鼠标空闲会自动淡出） */
  controlsVisible: boolean
  chatCollapsed: boolean
  /** 底部圆桌区是否收起（收起后舞台用满纵向空间） */
  roundtableCollapsed: boolean
  discussionActive: boolean
  showPlayHint: boolean
  courseComplete: boolean
  stats: { scenes: number; minutes: number; agents: number; messages: number }
  activeContentKey?: string | null
  whiteboards: WhiteboardEntry[]
  selectedWhiteboardId: string | null
}>()

const emit = defineEmits<{
  (e: 'toggle-sidebar'): void
  (e: 'prev'): void
  (e: 'next'): void
  (e: 'toggle-play'): void
  (e: 'update:volume', v: number): void
  (e: 'update:speed', v: number): void
  (e: 'toggle-auto-play'): void
  (e: 'toggle-whiteboard'): void
  (e: 'toggle-fullscreen'): void
  (e: 'toggle-chat'): void
  (e: 'toggle-roundtable'): void
  (e: 'stop-discussion'): void
  (e: 'select-whiteboard', id: string): void
  (e: 'close-whiteboard'): void
}>()

const { t } = useI18n()
</script>

<template>
  <div
    :class="
      cn(
        'group/canvas relative flex size-full flex-col',
        presenting ? 'presenting-canvas bg-black' : 'bg-gray-50 dark:bg-gray-900',
      )
    "
  >
    <!-- 舞台（canvas-stage 是尺寸容器，画布按它的实际可用尺寸适配） -->
    <div
      :class="
        cn(
          'canvas-stage relative flex min-h-0 flex-1 items-center justify-center overflow-hidden transition-colors duration-500',
          presenting ? 'bg-black' : 'bg-gray-50/30 p-2 dark:bg-gray-900/30',
        )
      "
    >
      <div
        :class="
          cn(
            'canvas-frame relative overflow-hidden bg-white transition-all duration-700 dark:bg-gray-800',
            presenting ? 'rounded-none shadow-none' : 'rounded-lg shadow-2xl',
          )
        "
      >
        <div class="scene-stage size-full">
          <!-- 课程完成页 -->
          <ClassroomComplete v-if="courseComplete" :stats="stats" />

          <!-- 场景内容；按场景 id 重建，切页时重置作答状态与 iframe -->
          <template v-else-if="scene.status === 'ready'">
            <SceneRenderer :key="scene.id" :scene="scene" :active-content-key="activeContentKey" />

            <WhiteboardPanel
              v-if="whiteboardOpen"
              :artifacts="whiteboards"
              :selected-id="selectedWhiteboardId"
              @select="emit('select-whiteboard', $event)"
              @close="emit('close-whiteboard')"
            />
          </template>

          <!-- 生成中 -->
          <div
            v-else
            class="flex size-full flex-col items-center justify-center gap-3 text-gray-400"
          >
            <Loader2 class="size-6 animate-spin text-brand-600" />
            <span class="text-[13px]">{{ t('scene.statusGenerating') }}</span>
          </div>

          <!-- 场景序号徽章 -->
          <div
            v-if="!courseComplete"
            class="absolute top-4 right-4 rounded-full bg-black/5 px-2.5 py-0.5 text-[11px] font-semibold text-gray-500 tabular-nums backdrop-blur-sm dark:bg-white/10 dark:text-gray-300"
          >
            {{ index + 1 }} / {{ total }}
          </div>

          <!-- 播放提示按钮 -->
          <div
            v-if="showPlayHint && !courseComplete"
            class="pointer-events-none absolute inset-0 z-[102] flex items-center justify-center"
          >
            <button
              type="button"
              class="pointer-events-auto flex size-16 animate-pulse items-center justify-center rounded-full bg-brand-700/90 text-white shadow-lg shadow-brand-300/30"
              @click="emit('toggle-play')"
            >
              <Play class="ml-1 size-7" />
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- 工具栏：放映时悬浮在幻灯片上，鼠标空闲自动淡出 -->
    <div
      :class="
        cn(
          'shrink-0',
          presenting &&
            'absolute inset-x-0 bottom-0 z-30 border-t border-black/5 bg-white/85 backdrop-blur-md transition-opacity duration-300 dark:border-white/10 dark:bg-gray-900/85',
          presenting && !controlsVisible && 'pointer-events-none opacity-0',
        )
      "
    >
      <CanvasToolbar
        :index="index"
        :total="total"
        :playing="playing"
        :volume="volume"
        :speed="speed"
        :auto-play="autoPlay"
        :whiteboard-open="whiteboardOpen"
        :presenting="presenting"
        :chat-collapsed="chatCollapsed"
        :roundtable-collapsed="roundtableCollapsed"
        :discussion-active="discussionActive"
        @toggle-sidebar="emit('toggle-sidebar')"
        @prev="emit('prev')"
        @next="emit('next')"
        @toggle-play="emit('toggle-play')"
        @update:volume="emit('update:volume', $event)"
        @update:speed="emit('update:speed', $event)"
        @toggle-auto-play="emit('toggle-auto-play')"
        @toggle-whiteboard="emit('toggle-whiteboard')"
        @toggle-fullscreen="emit('toggle-fullscreen')"
        @toggle-chat="emit('toggle-chat')"
        @toggle-roundtable="emit('toggle-roundtable')"
        @stop-discussion="emit('stop-discussion')"
      />
    </div>
  </div>
</template>

<style scoped>
/*
 * 舞台作为尺寸容器：画布直接按舞台的实际可用尺寸取 16:9 适配，
 * 而不是用 100dvh 减常量估算 —— 收起圆桌 / 进入放映时都能立刻用满空间。
 */
.canvas-stage {
  container-type: size;
}

/*
 * 画布同时受舞台宽度和剩余高度约束，取两者中较小的尺寸。
 * 第一行是不支持容器查询时的兜底估算。
 */
.canvas-frame {
  width: min(100%, calc((100dvh - 272px) * 1.7777778));
  width: min(100cqw, calc(100cqh * 1.7777778));
  aspect-ratio: 16 / 9;
  max-height: 100%;
}

/* 放映：外壳全部收起、工具栏悬浮，舞台占满整个视口（两行同上：兜底 + 容器尺寸） */
.presenting-canvas .canvas-frame {
  width: min(100%, calc(100dvh * 1.7777778));
  width: min(100cqw, calc(100cqh * 1.7777778));
  max-height: 100dvh;
}
</style>
