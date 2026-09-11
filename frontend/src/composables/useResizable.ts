/**
 * 拖拽调宽（文档 §6.2：SceneSidebar 默认 220 / 170~400，ChatArea 默认 340 / 240~560）。
 *
 * 用法：
 *   const panel = useResizable({ initial: 220, min: 170, max: 400, side: 'left' })
 *   <div :style="{ width: panel.displayWidth.value }">
 *   <div class="... cursor-col-resize" @mousedown="panel.start" />
 */
import { computed, onBeforeUnmount, ref } from 'vue'

interface Options {
  initial: number
  min: number
  max: number
  /**
   * 'left'  —— 手柄在面板右侧，向右拖变宽（左侧栏）
   * 'right' —— 手柄在面板左侧，向左拖变宽（右侧栏）
   */
  side: 'left' | 'right'
}

export function useResizable({ initial, min, max, side }: Options) {
  const width = ref(initial)
  const collapsed = ref(false)
  const dragging = ref(false)

  const displayWidth = computed(() =>
    collapsed.value ? '0px' : dragging.value ? `${width.value}px` : `${width.value}px`,
  )

  let startX = 0
  let startWidth = 0

  function onMouseMove(e: MouseEvent) {
    const delta = side === 'left' ? e.clientX - startX : startX - e.clientX
    width.value = Math.min(max, Math.max(min, startWidth + delta))
  }

  function onMouseUp() {
    dragging.value = false
    window.removeEventListener('mousemove', onMouseMove)
    window.removeEventListener('mouseup', onMouseUp)
    document.body.style.cursor = ''
    document.body.style.userSelect = ''
  }

  function start(e: MouseEvent) {
    if (collapsed.value) return
    dragging.value = true
    startX = e.clientX
    startWidth = width.value
    window.addEventListener('mousemove', onMouseMove)
    window.addEventListener('mouseup', onMouseUp)
    document.body.style.cursor = 'col-resize'
    document.body.style.userSelect = 'none'
  }

  function toggle() {
    collapsed.value = !collapsed.value
  }

  onBeforeUnmount(onMouseUp)

  return { width, collapsed, dragging, displayWidth, start, toggle }
}
