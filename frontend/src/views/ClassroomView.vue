<script setup lang="ts">
/**
 * 课堂播放页 —— 文档 §6.0（入口壳）。
 *
 * div.h-screen.flex.flex-col.overflow-hidden
 *   三态：loading → 居中文案；error → 文案 + Retry；ok → PlaybackChrome
 *
 * 数据来自 src/data/scenes.ts 的 mock；接入 Go 后端后换成
 * `GET /api/classrooms/:id` + SSE 流式推送即可。
 */
import { onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { AlertCircle, Loader2 } from 'lucide-vue-next'

import PlaybackChrome from '@/components/classroom/PlaybackChrome.vue'
import type { Classroom, Scene } from '@/types/scene'
import { fetchClassroomAgents, fetchClassroomScenes, fetchScene, streamClassroomEvents, type RoleCardDTO, type SceneDetailDTO } from '@/api/classroom'

const props = defineProps<{ id: string }>()

const { t } = useI18n()
const router = useRouter()

const phase = ref<'loading' | 'error' | 'ok'>('loading')
const classroom = ref<Classroom | null>(null)
const agents = ref<RoleCardDTO[]>([])
const sceneDetails = ref<Record<string, SceneDetailDTO>>({})
let eventController: AbortController | undefined

function load() {
  phase.value = 'loading'
  // 模拟加载；后端接入后替换为真实请求
  void loadReal()
}

async function loadReal() {
  try {
    const [summaries, currentAgents] = await Promise.all([fetchClassroomScenes(Number(props.id)), fetchClassroomAgents(Number(props.id))])
    const ready = summaries.filter((item) => item.status === 'ready')
    if (ready.length === 0) throw new Error('当前还没有生成完成的页面')
    const details = await Promise.all(ready.map((item) => fetchScene(item.id)))
    agents.value = currentAgents
    sceneDetails.value = Object.fromEntries(details.map((item) => [String(item.id), item]))
    const scenes: Scene[] = details.map((item) => {
      const type = (item.type as Scene['type']) || 'slide'
      const blocks = item.content.blocks ?? []
      const text = blocks.map((block) => block.content ?? block.text ?? '').filter(Boolean)
      const scene: Scene = {
        id: String(item.id), title: item.title, type, status: item.status as Scene['status'], blocks,
      }
      if (type === 'interactive') {
        scene.interactive = { url: 'interactive://classroom', heading: item.title, note: text.join(' ') }
      } else if (type === 'quiz') {
        scene.quiz = { question: text[0] || item.title, options: text.slice(1, 5), answer: 0 }
      } else if (type === 'pbl') {
        scene.pbl = { heading: item.title, columns: [{ title: '课堂内容', items: text }] }
      } else {
        scene.slide = { heading: item.title, bullets: text, bulletKeys: blocks.map((block) => block.key ?? '') }
      }
      return scene
    })
    classroom.value = { id: props.id, title: '课堂', scenes }
    phase.value = 'ok'
  } catch {
    phase.value = 'error'
  }
}

async function refreshScenes() {
  const summaries = await fetchClassroomScenes(Number(props.id))
  const ready = summaries.filter((item) => item.status === 'ready')
  if (!ready.length) return
  const details = await Promise.all(ready.map((item) => fetchScene(item.id)))
  const nextDetails = Object.fromEntries(details.map((item) => [String(item.id), item]))
  sceneDetails.value = { ...sceneDetails.value, ...nextDetails }
  if (classroom.value) {
    classroom.value.scenes = details.map((item) => ({
      id: String(item.id), title: item.title, type: (item.type as Scene['type']) || 'slide', status: item.status as Scene['status'], blocks: item.content.blocks ?? [],
      slide: { heading: item.title, bullets: (item.content.blocks ?? []).map((block) => block.content ?? block.text ?? '').filter(Boolean), bulletKeys: (item.content.blocks ?? []).map((block) => block.key ?? '') },
    }))
  }
}

onMounted(async () => {
  await load()
  eventController = new AbortController()
  try {
    for await (const event of streamClassroomEvents(Number(props.id), eventController.signal)) {
      if (event.status === 'ready' || event.status === 'playable' || event.status === 'generating') await refreshScenes()
      if (event.status === 'ready' || event.status === 'failed') break
    }
  } catch {
    if (!eventController.signal.aborted) return
  }
})
onUnmounted(() => eventController?.abort())
</script>

<template>
  <div class="flex h-screen flex-col overflow-hidden">
    <!-- loading -->
    <div
      v-if="phase === 'loading'"
      class="flex flex-1 items-center justify-center bg-gray-50 dark:bg-gray-900"
    >
      <div class="flex flex-col items-center gap-3">
        <Loader2 class="size-6 animate-spin text-violet-500" />
        <p class="text-sm text-muted-foreground">{{ t('classroom.loadingClassroom') }}</p>
      </div>
    </div>

    <!-- error -->
    <div
      v-else-if="phase === 'error'"
      class="flex flex-1 items-center justify-center bg-gray-50 dark:bg-gray-900"
    >
      <div class="flex flex-col items-center text-center">
        <AlertCircle class="size-8 text-destructive" />
        <p class="mt-3 mb-4 text-sm font-medium text-destructive">{{ t('classroom.notFound') }}</p>
        <p class="mb-4 text-[13px] text-muted-foreground">{{ t('classroom.notFoundDesc') }}</p>
        <div class="flex gap-2">
          <button
            type="button"
            class="rounded-md bg-primary px-4 py-2 text-sm text-primary-foreground transition-colors hover:bg-primary/90"
            @click="load"
          >
            {{ t('common.retry') }}
          </button>
          <button
            type="button"
            class="rounded-md border border-border px-4 py-2 text-sm transition-colors hover:bg-muted"
            @click="router.push({ name: 'home' })"
          >
            {{ t('common.backToHome') }}
          </button>
        </div>
      </div>
    </div>

    <!-- ok -->
    <PlaybackChrome v-else-if="classroom" :classroom="classroom" :agents="agents" :scene-details="sceneDetails" />
  </div>
</template>
