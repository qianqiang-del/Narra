<script setup lang="ts">
/**
 * InteractiveIframeRenderer —— 交互页的沙箱宿主。
 *
 * 模型生成的是一次完整的 HTML 文档，不能注入主页面，只能通过 iframe 的 srcdoc 运行。
 * sandbox 只给 allow-scripts 与 allow-forms，不带 allow-same-origin：脚本照常执行，
 * 但文档处于独立空来源，读不到 Narra 的 Cookie、LocalStorage 与父页面 DOM。
 *
 * 内部按 1280×720 逻辑画布等比缩放，容器尺寸任意，居中显示。两种模式：
 *   interactive —— 课堂主画布，可交互
 *   thumbnail   —— 侧栏缩略图，禁鼠标事件，进入视口才挂载 iframe
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

const props = withDefaults(
  defineProps<{
    /** 完整 HTML 文档字符串 */
    html?: string
    /** 主画布（true）或缩略图（false） */
    interactive?: boolean
  }>(),
  { html: '', interactive: false },
)

/** 交互 HTML 的设计基准，与生成提示词约定的画布一致 */
const BASE_W = 1280
const BASE_H = 720

const { t } = useI18n()

const hostRef = ref<HTMLDivElement | null>(null)
const scale = ref(0)
/** 缩略图懒加载：进入视口才真正挂载 iframe */
const shouldMount = ref(false)
const frameReady = ref(false)
const inViewport = ref(props.interactive)
const frameKey = ref(0)

let resizeObserver: ResizeObserver | null = null
let intersectionObserver: IntersectionObserver | null = null
let mountFrame = 0
let paintFrame = 0

const sandboxHTML = computed(() => props.html?.trim() ?? '')

const frameStyle = computed(() => ({
  width: `${BASE_W}px`,
  height: `${BASE_H}px`,
  transform: `translate(-50%, -50%) scale(${scale.value * (frameReady.value ? 1 : 0.999)})`,
}))

function updateScale() {
  const box = hostRef.value?.getBoundingClientRect()
  if (!box || box.width <= 0 || box.height <= 0) return false
  scale.value = Math.min(box.width / BASE_W, box.height / BASE_H)
  return true
}

function mountFrameWhenSized() {
  window.cancelAnimationFrame(mountFrame)
  window.cancelAnimationFrame(paintFrame)
  shouldMount.value = false
  frameReady.value = false
  if (!sandboxHTML.value) return
  if (!props.interactive && !inViewport.value) return
  frameKey.value += 1
  void nextTick(() => {
    mountFrame = window.requestAnimationFrame(() => {
      if (updateScale()) shouldMount.value = true
    })
  })
}

function onFrameLoad() {
  if (!updateScale()) return
  // iframe 可能先加载完，而它的缩放层还没来得及绘制。
  paintFrame = window.requestAnimationFrame(() => {
    paintFrame = window.requestAnimationFrame(() => {
      frameReady.value = true
    })
  })
}

onMounted(() => {
  const host = hostRef.value
  if (!host) return

  resizeObserver = new ResizeObserver((entries) => {
    const box = entries[0]?.contentRect
    if (!box) return
    if (box.width > 0 && box.height > 0) {
      scale.value = Math.min(box.width / BASE_W, box.height / BASE_H)
      if (!shouldMount.value && inViewport.value) mountFrameWhenSized()
    }
  })
  resizeObserver.observe(host)
  updateScale()

  if (props.interactive) {
    mountFrameWhenSized()
  } else {
    intersectionObserver = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
          inViewport.value = true
          mountFrameWhenSized()
          intersectionObserver?.disconnect()
          intersectionObserver = null
        }
      },
      { rootMargin: '120px' },
    )
    intersectionObserver.observe(host)
  }
})

watch([sandboxHTML, () => props.interactive], () => {
  inViewport.value ||= props.interactive
  mountFrameWhenSized()
})

onBeforeUnmount(() => {
  window.cancelAnimationFrame(mountFrame)
  window.cancelAnimationFrame(paintFrame)
  resizeObserver?.disconnect()
  intersectionObserver?.disconnect()
})
</script>

<template>
  <div ref="hostRef" class="relative size-full overflow-hidden bg-white dark:bg-gray-800">
    <iframe
      v-if="shouldMount && sandboxHTML"
      :key="frameKey"
      :srcdoc="sandboxHTML"
      :style="frameStyle"
      :class="[interactive ? 'pointer-events-auto' : 'pointer-events-none', frameReady ? 'opacity-100' : 'opacity-0']"
      class="absolute top-1/2 left-1/2 origin-center border-0 bg-white select-none"
      sandbox="allow-scripts allow-forms"
      referrerpolicy="no-referrer"
      tabindex="-1"
      title=""
      @load="onFrameLoad"
    />
    <!-- 没有 HTML：旧课堂没这一列，或这次生成没交出可用文档 -->
    <div
      v-else-if="!sandboxHTML"
      class="flex size-full items-center justify-center bg-gray-50 text-xs text-gray-400 dark:bg-gray-800 dark:text-gray-500"
    >
      {{ t('scene.interactiveUnavailable') }}
    </div>
    <!-- 缩略图未进入视口 -->
    <div v-if="sandboxHTML && !frameReady" class="pointer-events-none absolute inset-0 animate-pulse bg-gray-100 dark:bg-gray-800" />
  </div>
</template>
