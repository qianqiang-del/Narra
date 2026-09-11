<script setup lang="ts">
/**
 * CanvasToolbar —— 文档 §6.6（舞台底部工具栏，h-9）。
 * 左：侧栏切换 + 页码；中：音量/倍速/翻页/播放/讨论控制；右：全屏/聊天。
 */
import { onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  ChevronLeft,
  ChevronRight,
  LayoutList,
  Maximize2,
  MessageSquare,
  Minimize2,
  Pause,
  PencilLine,
  Play,
  Repeat,
  Volume1,
  Volume2,
  VolumeX,
} from 'lucide-vue-next'

import { cn } from '@/lib/utils'

const props = defineProps<{
  index: number
  total: number
  playing: boolean
  volume: number
  speed: number
  autoPlay: boolean
  whiteboardOpen: boolean
  fullscreen: boolean
  chatCollapsed: boolean
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

const speedOptions = [0.5, 1, 1.5, 2]
const volumeOpen = ref(false)

/**
 * 音量弹层的悬停开关。
 *
 * ⚠️ 关键：关闭必须**延迟 300ms**，不能立即关。弹层在按钮正上方、中间有 4px 空隙，
 * 光标从按钮往上移的瞬间会先触发 button 的 mouseleave；若此时立刻关闭并卸载弹层，
 * 光标就永远进不去滑块（曾因此导致"音量调节不成功"）。
 * 配合「弹层常驻 + opacity/pointer-events 切换」（见模板），光标才能顺利移入。
 */
let volumeTimer: ReturnType<typeof setTimeout> | undefined

function onVolumeEnter() {
  clearTimeout(volumeTimer)
  volumeOpen.value = true
}

function onVolumeLeave() {
  clearTimeout(volumeTimer)
  volumeTimer = setTimeout(() => {
    volumeOpen.value = false
  }, 300)
}

onBeforeUnmount(() => clearTimeout(volumeTimer))

const ctrlBtn =
  'relative flex size-7 cursor-pointer items-center justify-center rounded-md outline-none transition-all duration-150 hover:bg-gray-500/[0.08] active:scale-90'
const divider = 'mx-0.5 h-3 w-px shrink-0 bg-gray-200/80 dark:bg-gray-700'

/**
 * 竖向 range 输入。`appearance-none` 会连默认滑块一起抹掉，
 * 必须自己写 `::-webkit-slider-thumb` / `::-moz-range-thumb`，否则只有一根细轨道、没法拖。
 */
const volumeSlider =
  'h-16 w-1 cursor-pointer appearance-none rounded-full bg-gray-200 dark:bg-gray-600 ' +
  '[writing-mode:vertical-lr] [direction:rtl] ' +
  '[&::-webkit-slider-thumb]:h-3 [&::-webkit-slider-thumb]:w-3 [&::-webkit-slider-thumb]:appearance-none ' +
  '[&::-webkit-slider-thumb]:cursor-pointer [&::-webkit-slider-thumb]:rounded-full ' +
  '[&::-webkit-slider-thumb]:bg-violet-500 [&::-webkit-slider-thumb]:shadow-sm ' +
  'dark:[&::-webkit-slider-thumb]:bg-violet-400 ' +
  '[&::-moz-range-thumb]:h-3 [&::-moz-range-thumb]:w-3 [&::-moz-range-thumb]:rounded-full ' +
  '[&::-moz-range-thumb]:border-0 [&::-moz-range-thumb]:bg-violet-500 ' +
  'dark:[&::-moz-range-thumb]:bg-violet-400'

const volumeIcon = () => (props.volume === 0 ? VolumeX : props.volume < 0.5 ? Volume1 : Volume2)

function cycleSpeed() {
  const i = speedOptions.indexOf(props.speed)
  emit('update:speed', speedOptions[(i + 1) % speedOptions.length])
}
</script>

<template>
  <div class="flex h-9 shrink-0 items-center gap-1 px-2">
    <!-- 左 -->
    <button type="button" :class="ctrlBtn" @click="emit('toggle-sidebar')">
      <LayoutList class="size-4 text-gray-400" />
    </button>
    <span class="ml-1 text-[11px] text-gray-400 tabular-nums">
      {{ index + 1 }}/{{ total }}
    </span>

    <div class="flex-1" />

    <!-- 中：居中胶囊 -->
    <div class="inline-flex h-7 items-center gap-0.5 rounded-lg bg-gray-100/60 px-1 dark:bg-gray-800/60">
      <!-- 音量 -->
      <div
        class="relative flex items-center"
        @mouseenter="onVolumeEnter"
        @mouseleave="onVolumeLeave"
      >
        <button
          type="button"
          :class="cn(ctrlBtn, volume === 0 && 'text-red-500')"
          :aria-label="volume === 0 ? 'Unmute' : 'Mute'"
        >
          <component :is="volumeIcon()" class="size-4" />
        </button>

        <!-- 竖向音量条：常驻 DOM，只切 opacity / pointer-events -->
        <div
          :class="
            cn(
              'absolute bottom-full left-1/2 z-50 mb-2 flex -translate-x-1/2 flex-col items-center',
              'transition-all duration-200 ease-out',
              volumeOpen ? 'pointer-events-auto opacity-100' : 'pointer-events-none opacity-0',
            )
          "
        >
          <div
            class="flex flex-col items-center gap-1.5 rounded-lg border border-gray-200 bg-white px-2 py-2.5 shadow-lg dark:border-gray-700 dark:bg-gray-800"
          >
            <span class="text-[10px] font-medium text-gray-400 tabular-nums select-none dark:text-gray-500">
              {{ Math.round(volume * 100) }}
            </span>
            <input
              type="range"
              min="0"
              max="1"
              step="0.05"
              :value="volume"
              :class="volumeSlider"
              @input="emit('update:volume', Number(($event.target as HTMLInputElement).value))"
            />
          </div>
          <!-- 指向按钮的小箭头 -->
          <div
            class="-mt-[5px] size-2 rotate-45 border-r border-b border-gray-200 bg-white dark:border-gray-700 dark:bg-gray-800"
          />
        </div>
      </div>

      <!-- 倍速 -->
      <button
        type="button"
        :class="
          cn(
            'h-5 w-8 cursor-pointer rounded text-[11px] font-semibold tabular-nums transition-colors',
            speed !== 1
              ? 'bg-violet-500/10 text-violet-600 dark:text-violet-300'
              : 'text-gray-500 hover:bg-gray-500/[0.08] dark:text-gray-400',
          )
        "
        @click="cycleSpeed"
      >
        {{ speed }}x
      </button>

      <div :class="divider" />

      <!-- 翻页 -->
      <button type="button" :class="cn(ctrlBtn, 'disabled:opacity-20')" :disabled="index === 0" @click="emit('prev')">
        <ChevronLeft class="size-4 text-gray-500" />
      </button>
      <button
        type="button"
        :class="cn(ctrlBtn, 'disabled:opacity-20')"
        :disabled="index >= total - 1"
        @click="emit('next')"
      >
        <ChevronRight class="size-4 text-gray-500" />
      </button>

      <!-- 播放/暂停 -->
      <button type="button" :class="cn(ctrlBtn, playing && 'text-violet-600')" @click="emit('toggle-play')">
        <Pause v-if="playing" class="size-4" />
        <Play v-else class="size-4 text-gray-500" />
      </button>

      <div :class="divider" />

      <!-- 结束讨论 -->
      <button
        type="button"
        class="flex h-6 cursor-pointer items-center gap-1.5 rounded-md bg-red-500/10 px-2.5 text-[11px] font-semibold text-red-600 transition-colors hover:bg-red-500/20 dark:text-red-400"
      >
        {{ t('roundtable.stopDiscussion') }}
      </button>
    </div>

    <div class="flex-1" />

    <!-- 右 -->
    <button
      type="button"
      :class="cn(ctrlBtn, autoPlay && 'text-violet-600')"
      @click="emit('toggle-auto-play')"
    >
      <Repeat class="size-4" :class="!autoPlay && 'text-gray-400'" />
    </button>
    <button
      type="button"
      :class="cn(ctrlBtn, whiteboardOpen && 'text-violet-600')"
      @click="emit('toggle-whiteboard')"
    >
      <PencilLine class="size-4" :class="!whiteboardOpen && 'text-gray-400'" />
    </button>
    <div :class="divider" />
    <button type="button" :class="ctrlBtn" @click="emit('toggle-fullscreen')">
      <Minimize2 v-if="fullscreen" class="size-4 text-gray-500" />
      <Maximize2 v-else class="size-4 text-gray-400" />
    </button>
    <button
      type="button"
      :class="cn(ctrlBtn, chatCollapsed && 'opacity-50')"
      @click="emit('toggle-chat')"
    >
      <MessageSquare class="size-4 text-gray-400" />
    </button>
  </div>
</template>
