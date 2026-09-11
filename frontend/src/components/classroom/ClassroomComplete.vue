<script setup lang="ts">
/**
 * ClassroomComplete —— 文档 §6.9（课程完成页）。
 * 55 片 confetti + 金杯 + 标题 + 4 张统计卡。
 *
 * 原项目用 framer-motion 做入场，这里用纯 CSS 动画等效实现：
 * confetti 的随机参数在 setup 阶段一次性生成（不随重渲染变化）。
 */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Trophy } from 'lucide-vue-next'

const props = defineProps<{
  stats: { scenes: number; minutes: number; agents: number; messages: number }
}>()

const { t } = useI18n()

const CONFETTI_COLORS = [
  '#f59e0b',
  '#fb923c',
  '#ef4444',
  '#ec4899',
  '#a855f7',
  '#3b82f6',
  '#10b981',
]

interface ConfettiPiece {
  id: number
  left: number
  delay: number
  duration: number
  color: string
  size: number
  rotate: number
  rounded: boolean
}

const confetti = computed<ConfettiPiece[]>(() =>
  Array.from({ length: 55 }, (_, i) => ({
    id: i,
    left: (i * 37) % 100,
    delay: (i % 13) * 0.01,
    duration: 1.0 + ((i * 7) % 10) / 10,
    color: CONFETTI_COLORS[i % CONFETTI_COLORS.length],
    size: 6 + (i % 3) * 2,
    rotate: (i * 53) % 360,
    rounded: i % 2 === 0,
  })),
)

const statCards = computed(() => [
  { value: props.stats.scenes, label: t('scene.typeSlide') },
  { value: `${props.stats.minutes}`, label: 'min' },
  { value: props.stats.agents, label: t('roundtable.teacher') },
  { value: props.stats.messages, label: t('chat.chat') },
])
</script>

<template>
  <section class="absolute inset-0 z-[105] flex items-center justify-center overflow-auto">
    <!-- 背景 -->
    <div
      class="absolute inset-0 bg-gradient-to-br from-amber-50 via-white to-orange-50 dark:from-amber-950/20 dark:via-gray-900 dark:to-amber-950/30"
    />

    <!-- confetti -->
    <div class="pointer-events-none absolute inset-0 overflow-hidden">
      <span
        v-for="c in confetti"
        :key="c.id"
        class="absolute top-[-10%] animate-[confetti-fall_linear_forwards]"
        :style="{
          left: `${c.left}%`,
          width: `${c.size}px`,
          height: `${c.size}px`,
          backgroundColor: c.color,
          borderRadius: c.rounded ? '9999px' : '2px',
          transform: `rotate(${c.rotate}deg)`,
          animationDuration: `${c.duration}s`,
          animationDelay: `${c.delay}s`,
        }"
      />
    </div>

    <!-- 内容 -->
    <div class="relative flex flex-col items-center px-8 py-10 text-center">
      <!-- 金杯 -->
      <div class="relative mb-6">
        <span class="absolute inset-0 -z-10 animate-pulse rounded-full bg-amber-300/30 blur-2xl" />
        <Trophy class="size-20 text-amber-500 drop-shadow-lg md:size-24" />
      </div>

      <!-- Ribbon -->
      <span
        class="rounded-full bg-gradient-to-r from-amber-400 via-orange-400 to-amber-500 px-4 py-1.5 text-xs font-bold tracking-wider text-white uppercase shadow-lg shadow-amber-500/30"
      >
        {{ t('stage.courseComplete') }}
      </span>

      <!-- 标题 -->
      <h2
        class="mt-5 bg-gradient-to-br from-amber-700 via-orange-600 to-amber-800 bg-clip-text text-3xl font-black tracking-tight text-transparent md:text-4xl"
      >
        {{ t('stage.courseComplete') }}
      </h2>

      <!-- 统计卡 -->
      <div class="mt-8 grid grid-cols-2 gap-3 sm:grid-cols-4">
        <div
          v-for="(s, i) in statCards"
          :key="i"
          class="rounded-2xl border border-amber-100 bg-white/90 px-4 py-4 shadow-sm backdrop-blur-sm dark:border-amber-900/40 dark:bg-gray-800/90"
        >
          <div class="text-3xl font-black text-amber-600 tabular-nums dark:text-amber-400">
            {{ s.value }}
          </div>
          <div class="mt-1 text-[11px] font-medium tracking-wide text-gray-500 uppercase dark:text-gray-400">
            {{ s.label }}
          </div>
        </div>
      </div>
    </div>
  </section>
</template>
