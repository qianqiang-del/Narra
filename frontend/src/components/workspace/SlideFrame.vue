<script setup lang="ts">
/**
 * SlideFrame —— 单页课件的渲染载体。
 *
 * 课件是完整 HTML 字符串（1280×720 基准），通过 sandboxed iframe 的
 * srcDoc 渲染（对齐 OpenMAIC 互动场景的做法：allow-scripts，不带
 * allow-same-origin，样式与脚本天然隔离）。
 *
 * 容器尺寸任意：内部按 1280×720 等比 scale 适配，居中显示。
 *
 * 两种模式：
 *   interactive  —— 主画布，可交互（点击/滚动都交给课件自身）
 *   thumbnail    —— 缩略图，pointer-events-none + IntersectionObserver 懒挂载
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

const props = withDefaults(
  defineProps<{
    /** 完整 HTML 文档字符串 */
    html: string
    /** 主画布（true）或缩略图（false） */
    interactive?: boolean
  }>(),
  { interactive: false },
)

/** 课件设计基准（mock 数据的 html 里 html/body 也按这个尺寸写死） */
const BASE_W = 1280
const BASE_H = 720

const hostRef = ref<HTMLDivElement | null>(null)
const scale = ref(0.1)
/** 缩略图懒加载：进入视口才真正挂载 iframe */
const shouldMount = ref(props.interactive)

let resizeObserver: ResizeObserver | null = null
let intersectionObserver: IntersectionObserver | null = null

const frameStyle = computed(() => ({
  width: `${BASE_W}px`,
  height: `${BASE_H}px`,
  transform: `scale(${scale.value})`,
}))

onMounted(() => {
  const host = hostRef.value
  if (!host) return

  resizeObserver = new ResizeObserver((entries) => {
    const box = entries[0]?.contentRect
    if (!box) return
    scale.value = Math.min(box.width / BASE_W, box.height / BASE_H)
  })
  resizeObserver.observe(host)

  if (!props.interactive) {
    intersectionObserver = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) {
          shouldMount.value = true
          intersectionObserver?.disconnect()
          intersectionObserver = null
        }
      },
      { rootMargin: '120px' },
    )
    intersectionObserver.observe(host)
  }
})

onBeforeUnmount(() => {
  resizeObserver?.disconnect()
  intersectionObserver?.disconnect()
})
</script>

<template>
  <div ref="hostRef" class="relative size-full overflow-hidden">
    <iframe
      v-if="shouldMount"
      :srcdoc="html"
      :style="frameStyle"
      :class="interactive ? 'pointer-events-auto' : 'pointer-events-none'"
      class="absolute top-1/2 left-1/2 origin-center -translate-x-1/2 -translate-y-1/2 border-0 bg-white select-none"
      sandbox="allow-scripts"
      referrerpolicy="no-referrer"
      tabindex="-1"
      title=""
    />
    <!-- 缩略图未进入视口时的占位 -->
    <div v-else class="absolute inset-0 animate-pulse bg-gray-100 dark:bg-gray-800" />
  </div>
</template>
