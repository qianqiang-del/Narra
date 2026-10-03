<script setup lang="ts">
/**
 * ClassroomCover —— 课堂列表卡片的封面：把课程第一页按主画布同款版式缩放着画出来。
 *
 * 内容来自 `GET /classrooms` 的 `cover` 字段（首个内容页），不是另存一张缩略图。
 * 按类型分两路：交互页交给沙箱 iframe；其余用课堂主画布同一个 SceneRenderer，
 * 按 16:9 虚拟画布等比缩放——卡片宽度随栅格变化，缩放比用 ResizeObserver 现量。
 *
 * 这一页还没生成出来（或大纲还没落库）时只画类型占位块：卡片仍然能看出这是一门什么课。
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { BookOpen, MousePointer2, PieChart, Trophy } from 'lucide-vue-next'

import { interactiveHTML, type Scene, type SceneType } from '@/types/scene'
import InteractiveIframeRenderer from '@/components/classroom/InteractiveIframeRenderer.vue'
import SceneRenderer from '@/components/classroom/SceneRenderer.vue'

const props = defineProps<{
  /** 课程第一页；大纲还没落库时为 null 或 undefined */
  scene?: Scene | null
  /** 占位块上的课名；场景自己还没有标题时用它 */
  title?: string
}>()

/** 虚拟画布，与课堂主画布同为 16:9；缩放到卡片尺寸后正好铺满封面 */
const BASE_W = 1053
const BASE_H = 592

const TYPE_ICON: Record<SceneType, typeof BookOpen> = {
  slide: BookOpen,
  quiz: PieChart,
  interactive: MousePointer2,
  complete: Trophy,
}

const hostRef = ref<HTMLDivElement | null>(null)
const scale = ref(0.2)
let resizeObserver: ResizeObserver | null = null

/** 交互页的沙箱 HTML；不是交互页或还没生成出来时为空串 */
const coverHTML = computed(() => (props.scene ? interactiveHTML(props.scene) : ''))

/** 这一页的内容是否已经画得出来；画不出来就退回占位块 */
const canRender = computed(() => {
  const scene = props.scene
  if (!scene) return false
  if (scene.type === 'interactive') return coverHTML.value.length > 0
  if (scene.type === 'quiz') return (scene.quiz?.questions.length ?? 0) > 0
  return Boolean(scene.slide?.blocks.length || scene.slide?.lead || scene.slide?.takeaway)
})

/** 能画出来时给出场景，否则 null；模板靠它把可选场景收窄成必选 */
const drawable = computed<Scene | null>(() => (props.scene && canRender.value ? props.scene : null))

/** 占位块上的图标：还没排到大纲时按讲解页显示 */
const fallbackIcon = computed(() => TYPE_ICON[props.scene?.type ?? 'slide'])
const fallbackTitle = computed(() => props.scene?.title || props.title || '')

onMounted(() => {
  const host = hostRef.value
  if (!host) return
  resizeObserver = new ResizeObserver((entries) => {
    const box = entries[0]?.contentRect
    if (!box) return
    scale.value = Math.min(box.width / BASE_W, box.height / BASE_H)
  })
  resizeObserver.observe(host)
})

onBeforeUnmount(() => resizeObserver?.disconnect())
</script>

<template>
  <div ref="hostRef" class="pointer-events-none relative size-full overflow-hidden select-none">
    <!-- 交互页：沙箱 iframe，缩略图模式禁鼠标事件、进视口才挂载 -->
    <InteractiveIframeRenderer v-if="coverHTML" :html="coverHTML" :interactive="false" />

    <!-- 其余类型：与课堂主画布同一个渲染器，按虚拟画布等比缩放 -->
    <div v-else-if="drawable" class="relative size-full overflow-hidden bg-white dark:bg-slate-900">
      <div
        class="absolute top-0 left-0 origin-top-left"
        :style="{ width: `${BASE_W}px`, height: `${BASE_H}px`, transform: `scale(${scale})` }"
      >
        <SceneRenderer :scene="drawable" />
      </div>
    </div>

    <!-- 这一页还没生成出来：只画类型占位块 -->
    <div
      v-else
      class="flex size-full flex-col items-center justify-center gap-2.5 bg-gradient-to-br from-teal-50 to-blue-50 dark:from-teal-900/20 dark:to-blue-900/20"
    >
      <div
        class="flex size-12 items-center justify-center rounded-2xl bg-gradient-to-br from-teal-100 to-blue-100 dark:from-teal-900/40 dark:to-blue-900/40"
      >
        <component :is="fallbackIcon" class="size-5 text-teal-500/60" />
      </div>
      <p
        v-if="fallbackTitle"
        class="max-w-[80%] truncate text-[11px] font-medium text-teal-500/70 dark:text-teal-300/70"
      >
        {{ fallbackTitle }}
      </p>
    </div>
  </div>
</template>
