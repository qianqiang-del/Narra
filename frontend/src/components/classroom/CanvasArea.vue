<script setup lang="ts">
/**
 * CanvasArea —— 文档 §6.5（舞台 16:9 盒子 + 底部工具栏）。
 * 播放提示按钮 / 场景序号徽章 / 生成中占位 / 课程完成页在此挂载。
 */
import { useI18n } from 'vue-i18n'
import { Loader2, Play } from 'lucide-vue-next'

import CanvasToolbar from '@/components/classroom/CanvasToolbar.vue'
import ClassroomComplete from '@/components/classroom/ClassroomComplete.vue'
import SceneRenderer from '@/components/classroom/SceneRenderer.vue'
import type { Scene } from '@/data/scenes'

defineProps<{
  scene: Scene
  index: number
  total: number
  playing: boolean
  volume: number
  speed: number
  autoPlay: boolean
  whiteboardOpen: boolean
  fullscreen: boolean
  chatCollapsed: boolean
  showPlayHint: boolean
  courseComplete: boolean
  stats: { scenes: number; minutes: number; agents: number; messages: number }
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
}>()

const { t } = useI18n()
</script>

<template>
  <div class="group/canvas flex size-full flex-col bg-gray-50 dark:bg-gray-900">
    <!-- 舞台 -->
    <div
      class="relative flex min-h-0 flex-1 items-center justify-center overflow-hidden bg-gray-50/30 p-2 transition-colors duration-500 dark:bg-gray-900/30"
    >
      <div
        class="relative aspect-[16/9] max-h-full max-w-full overflow-hidden rounded-lg bg-white shadow-2xl transition-all duration-700 dark:bg-gray-800"
        style="height: 100%"
      >
        <!-- 课程完成页 -->
        <ClassroomComplete v-if="courseComplete" :stats="stats" />

        <!-- 场景内容 -->
        <SceneRenderer v-else-if="scene.status === 'ready'" :scene="scene" />

        <!-- 生成中 -->
        <div
          v-else
          class="flex size-full flex-col items-center justify-center gap-3 text-gray-400"
        >
          <Loader2 class="size-6 animate-spin text-violet-500" />
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
        <button
          v-if="showPlayHint && !courseComplete"
          type="button"
          class="absolute inset-0 z-[102] flex items-center justify-center"
          @click="emit('toggle-play')"
        >
          <span
            class="flex size-16 animate-pulse items-center justify-center rounded-full bg-violet-600/90 text-white shadow-lg shadow-violet-500/30"
          >
            <Play class="ml-1 size-7" />
          </span>
        </button>
      </div>
    </div>

    <!-- 工具栏 -->
    <CanvasToolbar
      :index="index"
      :total="total"
      :playing="playing"
      :volume="volume"
      :speed="speed"
      :auto-play="autoPlay"
      :whiteboard-open="whiteboardOpen"
      :fullscreen="fullscreen"
      :chat-collapsed="chatCollapsed"
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
    />
  </div>
</template>
