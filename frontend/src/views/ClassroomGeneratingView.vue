<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { AlertCircle, Check, Loader2 } from 'lucide-vue-next'
import { fetchClassroom, fetchClassroomAgents, fetchClassroomOutline, fetchClassroomScenes, streamClassroomEvents, type ClassroomDTO, type ClassroomOutlineDTO, type ClassroomSceneSummaryDTO, type RoleCardDTO } from '@/api/classroom'

const props = defineProps<{ id: string }>()
const router = useRouter()
const classroom = ref<ClassroomDTO | null>(null)
const outline = ref<ClassroomOutlineDTO | null>(null)
const agents = ref<RoleCardDTO[]>([])
const scenes = ref<ClassroomSceneSummaryDTO[]>([])
const loading = ref(true)
const error = ref('')
let eventController: AbortController | undefined

const readyCount = computed(() => scenes.value.filter((scene) => scene.status === 'ready').length)
const canEnter = computed(() => readyCount.value > 0 || classroom.value?.status === 'playable' || classroom.value?.status === 'ready')
const finished = computed(() => classroom.value?.status === 'ready' || classroom.value?.status === 'failed')

async function load() {
  const id = Number(props.id)
  if (!Number.isInteger(id) || id <= 0) throw new Error('课堂 ID 无效')
  const [current, currentOutline, currentAgents, currentScenes] = await Promise.all([
    fetchClassroom(id), fetchClassroomOutline(id), fetchClassroomAgents(id), fetchClassroomScenes(id),
  ])
  classroom.value = current
  outline.value = currentOutline
  agents.value = currentAgents
  scenes.value = currentScenes
}

async function refresh() {
  try {
    await load()
    loading.value = false
    if (finished.value && classroom.value?.status === 'failed') error.value = classroom.value.generation_error ?? '课堂生成失败'
  } catch (cause) {
    loading.value = false
    error.value = cause instanceof Error ? cause.message : '加载课堂生成状态失败'
  }
}

function enterClassroom() {
  router.push({ name: 'classroom', params: { id: props.id } })
}

onMounted(async () => {
  await refresh()
  eventController = new AbortController()
  try {
    for await (const event of streamClassroomEvents(Number(props.id), eventController.signal)) {
      classroom.value = event
      if (finished.value) break
      const currentScenes = await fetchClassroomScenes(Number(props.id))
      scenes.value = currentScenes
    }
  } catch (cause) {
    if (!eventController.signal.aborted) error.value = cause instanceof Error ? cause.message : '状态流连接失败'
  }
})
onUnmounted(() => eventController?.abort())
</script>

<template>
  <main class="min-h-screen bg-gradient-to-b from-slate-50 to-slate-100 p-6 dark:from-slate-950 dark:to-slate-900">
    <div class="mx-auto max-w-4xl space-y-6">
      <section class="rounded-2xl border border-border/60 bg-background/80 p-6 shadow-sm">
        <div v-if="loading" class="flex items-center gap-3 text-muted-foreground"><Loader2 class="size-5 animate-spin" />正在加载课堂生成状态...</div>
        <div v-else-if="error" class="flex items-center gap-3 text-destructive"><AlertCircle class="size-5" />{{ error }}</div>
        <template v-else>
          <h1 class="text-2xl font-semibold">{{ outline?.title || classroom?.title || '正在生成课堂' }}</h1>
          <p class="mt-2 text-sm text-muted-foreground">{{ canEnter ? '已有页面生成完成，可以进入课堂' : '正在生成课堂内容，请稍候...' }}</p>
          <button v-if="canEnter" type="button" class="mt-5 rounded-lg bg-primary px-4 py-2 text-sm text-primary-foreground" @click="enterClassroom">进入课堂</button>
        </template>
      </section>

      <section v-if="outline" class="rounded-2xl border border-border/60 bg-background/80 p-6">
        <h2 class="text-lg font-medium">课程大纲</h2>
        <div class="mt-4 space-y-3">
          <div v-for="scene in outline.scenes" :key="scene.id" class="rounded-lg border border-border/50 p-3">
            <div class="flex items-center gap-2 text-sm font-medium"><Check v-if="scenes.find((item) => item.id === scene.id)?.status === 'ready'" class="size-4 text-emerald-500" /><span>{{ scene.sort_order + 1 }}. {{ scene.title }}</span></div>
            <p class="mt-1 text-xs text-muted-foreground">{{ scene.brief }}</p>
          </div>
        </div>
      </section>

      <section class="rounded-2xl border border-border/60 bg-background/80 p-6">
        <h2 class="text-lg font-medium">课堂角色</h2>
        <div class="mt-4 grid grid-cols-2 gap-3 md:grid-cols-4">
          <div v-for="agent in agents" :key="agent.agent_key" class="rounded-xl border border-border/50 p-3" :style="{ borderColor: agent.color }">
            <img :src="agent.avatar" :alt="agent.name" class="mx-auto size-14 rounded-full object-cover" />
            <p class="mt-2 text-center text-sm font-medium">{{ agent.name }}</p>
            <p class="text-center text-xs text-muted-foreground">{{ agent.role }}</p>
          </div>
        </div>
      </section>
    </div>
  </main>
</template>
