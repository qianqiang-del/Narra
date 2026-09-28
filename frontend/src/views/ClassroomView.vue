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
import type { Classroom, Scene, SlideContent } from '@/types/scene'
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

/** 讲解页的内容块；后端把整页正文都放在这个有序数组里。 */
type SceneBlockDTO = NonNullable<SceneDetailDTO['content']['blocks']>[number]

/** 比较标题时忽略空白与结尾标点。 */
function sameAsTitle(text: string, title: string): boolean {
  const normalize = (value: string) => value.replace(/\s+/g, '').replace(/[：:。.、]+$/, '')
  return normalize(text) === normalize(title)
}

/**
 * 组装讲解页正文：剔除与页面标题重复的标题块，拆出副标题与收尾结论。
 *
 * 后端每页的第一个块几乎都是与页面标题同名的 heading，直接渲染会让标题出现两次，
 * 所以这里把它记成 `titleKey`（讲稿讲到它时高亮大标题）；剩下的首块若仍是 heading，
 * 说明它是比标题更具体的一句话，当作副标题；正文末尾的 callout 拿出来做结论条。
 */
function toSlideContent(title: string, blocks: SceneBlockDTO[]): SlideContent {
  const kept = blocks
    .map((block) => ({ key: block.key ?? '', type: block.type ?? 'paragraph', text: (block.content ?? block.text ?? '').trim() }))
    .filter((block) => block.text.length > 0)

  const titleIndex = kept.findIndex((block) => block.type === 'heading' && sameAsTitle(block.text, title))
  const titleKey = titleIndex >= 0 ? kept[titleIndex].key : ''
  const body = kept.filter((_, index) => index !== titleIndex)

  const lead = body[0]?.type === 'heading' ? body.shift()!.text : ''
  const last = body[body.length - 1]
  const takeaway = last?.type === 'callout' ? body.pop()!.text : ''

  return { titleKey, lead, blocks: body, takeaway, takeawayKey: takeaway ? last.key : '' }
}

/** 把一个场景详情响应组装成前端视图模型；三种类型的正文来源各不相同。 */
function toScene(item: SceneDetailDTO): Scene {
  const type = (item.type as Scene['type']) || 'slide'
  const blocks = item.content.blocks ?? []
  const scene: Scene = {
    id: String(item.id), title: item.title, type, status: item.status as Scene['status'], blocks,
  }
  if (type === 'interactive') {
    scene.interactive = { html: item.interactive_html ?? '' }
  } else if (type === 'quiz') {
    scene.quiz = {
      questions: blocks
        .filter((block) => block.type === 'quiz')
        .map((block) => ({
          key: block.key ?? '',
          question: block.content ?? block.text ?? '',
          options: Array.isArray(block.interaction?.options) ? block.interaction.options : [],
          answer: block.interaction?.answer ?? '',
          explanation: typeof block.interaction?.config?.explanation === 'string' ? block.interaction.config.explanation : '',
        })),
    }
  } else {
    scene.slide = toSlideContent(item.title, blocks)
  }
  return scene
}

async function loadReal() {
  try {
    const [summaries, currentAgents] = await Promise.all([fetchClassroomScenes(Number(props.id)), fetchClassroomAgents(Number(props.id))])
    const ready = summaries.filter((item) => item.status === 'ready')
    if (ready.length === 0) throw new Error('当前还没有生成完成的页面')
    const details = await Promise.all(ready.map((item) => fetchScene(item.id)))
    agents.value = currentAgents
    sceneDetails.value = Object.fromEntries(details.map((item) => [String(item.id), item]))
    classroom.value = { id: props.id, title: '课堂', scenes: details.map(toScene) }
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
  sceneDetails.value = { ...sceneDetails.value, ...Object.fromEntries(details.map((item) => [String(item.id), item])) }
  if (classroom.value) classroom.value.scenes = details.map(toScene)
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
