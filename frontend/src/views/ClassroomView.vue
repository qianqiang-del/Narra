<script setup lang="ts">
/**
 * 课堂播放页 —— 文档 §6.0（入口壳）。
 *
 * div.h-screen.flex.flex-col.overflow-hidden
 *   三态：loading → 居中文案；error → 文案 + Retry；ok → PlaybackChrome
 *
 * 进得来只说明「至少有一页生成完了」，整门课往往还在生成，所以左侧场景栏把大纲里的每一页都列出来：
 * 已就绪的画真实内容、就地可点；没就绪的显示「正在生成中」、点不进去。订阅生成进度流后，
 * 某一页一落库就补上它的正文并让那一页可点，不需要刷新页面。
 */
import { onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { AlertCircle, Loader2 } from 'lucide-vue-next'

import PlaybackChrome from '@/components/classroom/PlaybackChrome.vue'
import type { Classroom, Scene } from '@/types/scene'
import { toScene } from '@/lib/scene-mapper'
import {
  fetchClassroomAgents,
  fetchClassroomScenes,
  fetchScene,
  retryClassroomScene,
  streamClassroomEvents,
  type ClassroomProgressEvent,
  type ClassroomSceneSummaryDTO,
  type RoleCardDTO,
  type SceneDetailDTO,
} from '@/api/classroom'

const props = defineProps<{ id: string }>()

const { t } = useI18n()
const router = useRouter()

const phase = ref<'loading' | 'error' | 'ok'>('loading')
const classroom = ref<Classroom | null>(null)
const agents = ref<RoleCardDTO[]>([])
const title = ref('课堂')
/** 大纲里每一页的进度，按 sort_order 排；它就是场景栏的骨架，还没生成完的页也在里面 */
const summaries = ref<ClassroomSceneSummaryDTO[]>([])
/** 已就绪页面的正文，按需拉取：没有条目就说明这一页的正文还没到手 */
const sceneDetails = ref<Record<string, SceneDetailDTO>>({})
/** 正在拉的页 id，免得进度流连着催两次同一页 */
const detailInFlight = new Set<string>()
let eventController: AbortController | undefined

function load() {
  phase.value = 'loading'
  void loadReal()
}

async function retryScene(id: string) {
  const scene = await retryClassroomScene(Number(id))
  const index = summaries.value.findIndex((item) => item.id === scene.id)
  if (index >= 0) summaries.value[index] = scene
  rebuildScenes()
  startEvents()
}

async function loadReal() {
  try {
    const [sceneSummaries, currentAgents] = await Promise.all([
      fetchClassroomScenes(Number(props.id)),
      fetchClassroomAgents(Number(props.id)),
    ])
    agents.value = currentAgents
    summaries.value = sceneSummaries
    rebuildScenes()
    await fetchReadyDetails()
    phase.value = 'ok'
  } catch {
    phase.value = 'error'
  }
}

/** 已就绪但还没正文的页，取回正文后就地补进场景栏；单独一页拉失败不影响其余页。 */
async function fetchReadyDetails() {
  const pending = summaries.value.filter(
    (item) => item.status === 'ready' && !sceneDetails.value[String(item.id)] && !detailInFlight.has(String(item.id)),
  )
  if (!pending.length) return
  const fetched = await Promise.all(
    pending.map(async (item) => {
      const key = String(item.id)
      detailInFlight.add(key)
      try {
        return [key, await fetchScene(item.id)] as const
      } catch {
        return null
      } finally {
        detailInFlight.delete(key)
      }
    }),
  )
  let changed = false
  for (const entry of fetched) {
    if (!entry) continue
    sceneDetails.value[entry[0]] = entry[1]
    changed = true
  }
  if (changed) rebuildScenes()
}

/** 用当前的页进度与已到手的正文重建场景栏视图模型。 */
function rebuildScenes() {
  classroom.value = { id: props.id, title: title.value, scenes: summaries.value.map(toViewScene) }
}

/** 一页的视图模型：正文到手就按正文画，没到手就只有标题与生成状态。 */
function toViewScene(item: ClassroomSceneSummaryDTO): Scene {
  const detail = sceneDetails.value[String(item.id)]
  if (detail) return toScene(detail)
  return {
    id: String(item.id),
    title: item.title,
    type: item.type as Scene['type'],
    status: item.status === 'failed' ? 'failed' : 'generating',
  }
}

/**
 * 消费一条进度事件。
 *
 * 就绪与否一律看负载里的 status，不看事件名：后端先把 status 写成 ready、随后才把 phase 推成
 * ready，所以一页的「ready 事件」可能挂在别的名字上，只有 status 是可靠的。
 */
async function applyEvent(event: ClassroomProgressEvent) {
  switch (event.kind) {
    case 'snapshot':
      summaries.value = event.scenes
      rebuildScenes()
      await fetchReadyDetails()
      break
    case 'scene': {
      const index = summaries.value.findIndex((item) => item.id === event.scene.id)
      if (index >= 0) summaries.value[index] = event.scene
      else summaries.value = [...summaries.value, event.scene]
      rebuildScenes()
      await fetchReadyDetails()
      break
    }
    case 'classroom':
      title.value = event.classroom.title || title.value
      break
    default:
      break
  }
}

async function consumeEvents(signal: AbortSignal) {
  try {
    for await (const event of streamClassroomEvents(Number(props.id), signal)) {
      await applyEvent(event)
    }
  } catch {
    if (!signal.aborted) return
  }
}

function startEvents() {
  eventController?.abort()
  eventController = new AbortController()
  void consumeEvents(eventController.signal)
}

onMounted(async () => {
  await load()
  startEvents()
})
onUnmounted(() => eventController?.abort())
</script>

<template>
  <div class="narra-page narra-classroom flex h-screen flex-col overflow-hidden">
    <!-- loading -->
    <div
      v-if="phase === 'loading'"
      class="flex flex-1 items-center justify-center bg-gray-50 dark:bg-gray-900"
    >
      <div class="flex flex-col items-center gap-3">
        <Loader2 class="size-6 animate-spin text-brand-600" />
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
    <PlaybackChrome
      v-else-if="classroom"
      :classroom="classroom"
      :agents="agents"
      :scene-details="sceneDetails"
      :on-retry-scene="retryScene"
    />
  </div>
</template>
