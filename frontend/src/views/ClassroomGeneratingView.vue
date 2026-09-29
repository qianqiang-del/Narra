<script setup lang="ts">
/**
 * 课堂生成进行页：把「一次全铺开」改成有节奏的分步流程。
 *
 * 步骤：生成大纲 → 课堂角色 → 生成场景 → 进入课堂，顶部是可回退的节点条。
 * 进度信号全部来自现有接口，不改后端：
 *   - 课堂整体状态走 SSE（GET /classrooms/:id/events，只有 classroom 事件）；
 *   - 大纲与场景走定时轮询。但只在「大纲已就绪、且还有页没出结果」时才轮询：
 *     段一期间场景为空、SSE 会在变 playable 时通知，轮询没意义；全部页出结果后立即停。
 */
import { computed, onMounted, onUnmounted, ref, watch, type Component } from 'vue'
import { useRouter } from 'vue-router'
import {
  AlertCircle, ArrowRight, Check, FileText, Layers, Loader2, Rocket, Sparkles, Users,
} from 'lucide-vue-next'
import {
  fetchClassroom, fetchClassroomAgents, fetchClassroomOutline, fetchClassroomScenes, streamClassroomEvents,
  type ClassroomDTO, type ClassroomOutlineDTO, type ClassroomSceneSummaryDTO, type RoleCardDTO,
} from '@/api/classroom'

const props = defineProps<{ id: string }>()
const router = useRouter()

const classroom = ref<ClassroomDTO | null>(null)
const outline = ref<ClassroomOutlineDTO | null>(null)
const agents = ref<RoleCardDTO[]>([])
const scenes = ref<ClassroomSceneSummaryDTO[]>([])
const loading = ref(true)
const error = ref('')

let eventController: AbortController | undefined
let pollTimer: number | undefined
const timers = new Set<number>()

const classroomId = computed(() => Number(props.id))

/** 生成大纲期间轮播的拟态文案，让等待不显得干。 */
const outlinePhases = ['正在理解你的需求…', '正在规划页面结构…', '正在拟定教学目标…', '正在生成课程大纲…', '就快好了…']
const phaseIndex = ref(0)
let phaseTimer: number | undefined

const title = computed(() => outline.value?.title || classroom.value?.title || '正在生成课堂')
const outlineReady = computed(() => (outline.value?.scenes.length ?? 0) > 0)
const totalScenes = computed(() => scenes.value.length)
const readyCount = computed(() => scenes.value.filter((scene) => scene.status === 'ready').length)
const failedCount = computed(() => scenes.value.filter((scene) => scene.status === 'failed').length)
const settledCount = computed(() => readyCount.value + failedCount.value)
const scenePercent = computed(() => (totalScenes.value === 0 ? 0 : Math.round((settledCount.value / totalScenes.value) * 100)))
/** 有任一页就绪即可进课堂；整课 ready 也算。不拿 playable 当条件——它在页面就绪前就会置上。 */
const canEnter = computed(() => readyCount.value > 0 || classroom.value?.status === 'ready')

/**
 * 生成是否已经不会再有变化——用来停轮询。
 * 只看 ready/failed 不够：后端「部分页失败」的终态是 playable，那样轮询永远停不下来。
 * 所以真正的判据是「所有页都已出结果（ready 或 failed）」。
 */
const generationDone = computed(() => {
  const status = classroom.value?.status
  if (status === 'ready' || status === 'failed') return true
  return totalScenes.value > 0 && settledCount.value === totalScenes.value
})

interface Step { key: string; title: string; hint: string; icon: Component }
const steps: Step[] = [
  { key: 'outline', title: '生成大纲', hint: '规划课程结构', icon: FileText },
  { key: 'roles', title: '课堂角色', hint: '确认授课阵容', icon: Users },
  { key: 'scenes', title: '生成场景', hint: '逐页生成内容与语音', icon: Layers },
  { key: 'enter', title: '进入课堂', hint: '开始学习', icon: Rocket },
]
const activeIndex = ref(0)
const maxReached = ref(0)
const activeStep = computed(() => steps[activeIndex.value])
let rolesAdvanced = false

/** reach 只向前推进：把可达最远点与当前步都推高。 */
function reach(index: number) {
  if (index > maxReached.value) maxReached.value = index
  if (index > activeIndex.value) activeIndex.value = index
}

/** goTo 只允许跳到已经到达过的节点，保证「能回退、不能越级」。 */
function goTo(index: number) {
  if (index < 0 || index > maxReached.value) return
  activeIndex.value = index
}

function later(fn: () => void, delay: number) {
  const id = window.setTimeout(() => {
    timers.delete(id)
    fn()
  }, delay)
  timers.add(id)
}

function stepState(index: number): 'done' | 'active' | 'ready' | 'locked' {
  if (index === activeIndex.value) return 'active'
  if (index < activeIndex.value) return 'done'
  if (index <= maxReached.value) return 'ready'
  return 'locked'
}

function nodeClass(index: number) {
  switch (stepState(index)) {
    case 'done': return 'border-transparent bg-emerald-500 text-white'
    case 'active': return 'border-transparent bg-blue-600 text-white ring-4 ring-blue-500/20'
    case 'ready': return 'border-blue-200 bg-white text-blue-600 hover:border-blue-400 dark:border-slate-700 dark:bg-slate-900 dark:text-blue-300'
    default: return 'border-slate-200 bg-slate-100 text-slate-400 dark:border-slate-800 dark:bg-slate-800/60'
  }
}

function labelClass(index: number) {
  const state = stepState(index)
  if (state === 'locked') return 'text-slate-400'
  if (state === 'active') return 'text-blue-700 dark:text-blue-300'
  return 'text-slate-700 dark:text-slate-200'
}

function statusText(scene: ClassroomSceneSummaryDTO) {
  switch (scene.status) {
    case 'ready': return '已完成'
    case 'generating': return '正在生成…'
    case 'failed': return scene.error_message || '生成失败'
    default: return '等待生成'
  }
}

function typeLabel(type: string) {
  switch (type) {
    case 'quiz': return '测验'
    case 'interactive': return '交互'
    default: return '图文'
  }
}

watch(outlineReady, (ready) => {
  if (!ready) return
  if (phaseTimer) window.clearInterval(phaseTimer)
  startPolling()
  if (activeIndex.value < 1) later(() => reach(1), 1400)
})

watch(activeIndex, (index) => {
  // 角色只展示一小会儿，第一次进入时自动走向场景；回退时不重复触发。
  if (index === 1 && !rolesAdvanced) {
    rolesAdvanced = true
    later(() => reach(2), 2000)
  }
})

watch(canEnter, (ok) => {
  if (ok && activeIndex.value < 3) later(() => reach(3), 700)
})

async function load() {
  const id = classroomId.value
  if (!Number.isInteger(id) || id <= 0) throw new Error('课堂 ID 无效')
  const [current, currentOutline, currentAgents, currentScenes] = await Promise.all([
    fetchClassroom(id), fetchClassroomOutline(id), fetchClassroomAgents(id), fetchClassroomScenes(id),
  ])
  classroom.value = current
  outline.value = currentOutline
  agents.value = currentAgents
  scenes.value = currentScenes
}

/** 刷新中途进入时，直接落到与当前进度相符的节点，不从头播一遍。 */
function syncInitialStep() {
  if (canEnter.value) {
    maxReached.value = 3
    activeIndex.value = 3
    rolesAdvanced = true
    return
  }
  if (outlineReady.value) {
    maxReached.value = 2
    activeIndex.value = 2
    rolesAdvanced = true
    return
  }
  maxReached.value = 0
  activeIndex.value = 0
}

function stopPolling() {
  if (pollTimer) {
    window.clearInterval(pollTimer)
    pollTimer = undefined
  }
}

/** 只有「大纲已就绪、且还有页没出结果」才需要轮询；否则一个请求都不发。 */
function startPolling() {
  if (pollTimer || !outlineReady.value || generationDone.value) return
  pollTimer = window.setInterval(() => void pollProgress(), 1500)
}

async function pollProgress() {
  try {
    const id = classroomId.value
    const [currentScenes, currentOutline] = await Promise.all([fetchClassroomScenes(id), fetchClassroomOutline(id)])
    scenes.value = currentScenes
    if (currentOutline.scenes.length > 0) outline.value = currentOutline
    if (generationDone.value) stopPolling()
  } catch {
    // 轮询失败不打断动画，下一轮再试。
  }
}

function enterClassroom() {
  router.push({ name: 'classroom', params: { id: props.id } })
}

onMounted(async () => {
  try {
    await load()
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '加载课堂生成状态失败'
  }
  loading.value = false
  if (classroom.value?.status === 'failed') error.value = classroom.value.generation_error ?? '课堂生成失败'

  syncInitialStep()
  startPolling()

  phaseTimer = window.setInterval(() => {
    phaseIndex.value = (phaseIndex.value + 1) % outlinePhases.length
  }, 2400)

  eventController = new AbortController()
  try {
    for await (const event of streamClassroomEvents(classroomId.value, eventController.signal)) {
      classroom.value = event
      if (generationDone.value) break
    }
  } catch (cause) {
    if (!eventController.signal.aborted && !error.value) {
      error.value = cause instanceof Error ? cause.message : '状态流连接失败'
    }
  }
})

onUnmounted(() => {
  eventController?.abort()
  stopPolling()
  if (phaseTimer) window.clearInterval(phaseTimer)
  timers.forEach((id) => window.clearTimeout(id))
  timers.clear()
})
</script>

<template>
  <main class="relative min-h-screen overflow-hidden bg-slate-50 p-6 dark:bg-slate-950">
    <div class="pointer-events-none absolute inset-0 overflow-hidden">
      <div class="blob absolute -top-32 left-[12%] size-96 rounded-full bg-blue-400/20 blur-3xl" />
      <div class="blob absolute top-1/3 -right-24 size-96 rounded-full bg-violet-400/20 blur-3xl" style="animation-delay: -3s" />
      <div class="blob absolute -bottom-32 left-1/3 size-96 rounded-full bg-emerald-400/15 blur-3xl" style="animation-delay: -6s" />
    </div>

    <div class="relative mx-auto max-w-3xl space-y-5">
      <header class="text-center">
        <p class="text-xs font-medium tracking-widest text-slate-400 uppercase">Narra · 课堂生成</p>
        <h1 class="mt-1 text-2xl font-semibold text-slate-900 dark:text-slate-50">{{ title }}</h1>
      </header>

      <div v-if="loading" class="flex items-center justify-center gap-3 rounded-2xl border border-slate-200/70 bg-white/80 p-10 text-sm text-slate-500 backdrop-blur dark:border-slate-800 dark:bg-slate-900/60">
        <Loader2 class="size-5 animate-spin" />正在读取课堂状态…
      </div>

      <div v-else-if="error && !classroom" class="flex items-center gap-3 rounded-2xl border border-rose-200 bg-rose-50 p-6 text-sm text-rose-700 dark:border-rose-900 dark:bg-rose-950/40 dark:text-rose-300">
        <AlertCircle class="size-5 shrink-0" />{{ error }}
      </div>

      <template v-else>
        <div v-if="error" class="flex items-center gap-3 rounded-2xl border border-amber-200 bg-amber-50 p-4 text-sm text-amber-800 dark:border-amber-900 dark:bg-amber-950/40 dark:text-amber-300">
          <AlertCircle class="size-5 shrink-0" />{{ error }}
        </div>

        <nav class="rounded-2xl border border-slate-200/70 bg-white/80 p-3 backdrop-blur dark:border-slate-800 dark:bg-slate-900/60">
          <ol class="flex items-center">
            <li v-for="(step, index) in steps" :key="step.key" class="flex flex-1 items-center last:flex-none">
              <button
                type="button"
                :disabled="index > maxReached"
                class="group flex items-center gap-2.5 rounded-xl px-1.5 py-1 text-left transition disabled:cursor-not-allowed disabled:opacity-70"
                @click="goTo(index)"
              >
                <span class="flex size-9 shrink-0 items-center justify-center rounded-full border transition" :class="nodeClass(index)">
                  <Check v-if="stepState(index) === 'done'" class="size-4" />
                  <Loader2 v-else-if="stepState(index) === 'active' && !generationDone" class="size-4 animate-spin" />
                  <component :is="step.icon" v-else class="size-4" />
                </span>
                <span class="hidden sm:block">
                  <span class="block text-sm font-medium" :class="labelClass(index)">{{ step.title }}</span>
                  <span class="block text-xs text-slate-400">{{ step.hint }}</span>
                </span>
              </button>
              <span
                v-if="index < steps.length - 1"
                class="mx-2 h-px min-w-3 flex-1 rounded transition-colors"
                :class="index < activeIndex ? 'bg-emerald-400' : 'bg-slate-200 dark:bg-slate-700'"
              />
            </li>
          </ol>
        </nav>

        <section v-if="activeStep.key === 'outline'" class="anim-fade-up rounded-2xl border border-slate-200/70 bg-white/80 p-8 shadow-sm backdrop-blur dark:border-slate-800 dark:bg-slate-900/60">
          <div class="flex flex-col items-center text-center">
            <div class="relative mb-6 flex size-16 items-center justify-center">
              <span class="absolute inset-0 animate-ping rounded-full bg-blue-500/20" />
              <span class="relative flex size-16 items-center justify-center rounded-full bg-blue-600 text-white shadow-lg shadow-blue-500/30">
                <Sparkles class="size-7" />
              </span>
            </div>
            <h2 class="text-lg font-semibold text-slate-900 dark:text-slate-50">正在生成课程大纲</h2>
            <p class="mt-1.5 h-5 text-sm text-blue-600 dark:text-blue-300">{{ outlinePhases[phaseIndex] }}</p>

            <div class="mt-8 w-full space-y-3">
              <div v-for="line in 4" :key="line" class="shimmer h-4 rounded-full" :style="{ width: `${100 - line * 7}%` }" />
            </div>

            <div class="mt-8 h-1 w-full overflow-hidden rounded-full bg-slate-100 dark:bg-slate-800">
              <div class="indeterminate h-full w-1/3 rounded-full bg-blue-500" />
            </div>
            <p class="mt-3 text-xs text-slate-400">大纲完成后会自动进入下一步</p>
          </div>
        </section>

        <section v-else-if="activeStep.key === 'roles'" class="anim-fade-up rounded-2xl border border-slate-200/70 bg-white/80 p-6 shadow-sm backdrop-blur dark:border-slate-800 dark:bg-slate-900/60">
          <header class="flex items-center gap-2">
            <Users class="size-5 text-blue-600 dark:text-blue-400" />
            <h2 class="text-lg font-semibold text-slate-900 dark:text-slate-50">课堂角色</h2>
            <span class="ml-auto text-xs text-slate-400">{{ agents.length }} 位</span>
          </header>

          <div class="mt-5 grid grid-cols-2 gap-4 sm:grid-cols-3">
            <div
              v-for="(agent, index) in agents"
              :key="agent.agent_key"
              class="anim-fade-up rounded-xl border bg-white p-4 text-center dark:bg-slate-900"
              :style="{ borderColor: agent.color || undefined, animationDelay: `${index * 70}ms` }"
            >
              <img v-if="agent.avatar" :src="agent.avatar" :alt="agent.name" class="mx-auto size-16 rounded-full object-cover" />
              <span
                v-else
                class="mx-auto flex size-16 items-center justify-center rounded-full text-xl font-semibold text-white"
                :style="{ backgroundColor: agent.color || '#94a3b8' }"
              >{{ agent.name.slice(0, 1) }}</span>
              <p class="mt-3 text-sm font-medium text-slate-900 dark:text-slate-100">{{ agent.name }}</p>
              <p class="text-xs text-slate-400">{{ agent.role }}</p>
            </div>
          </div>

          <div class="mt-5 flex items-center justify-between text-xs text-slate-400">
            <button type="button" class="rounded-lg px-2 py-1 transition hover:bg-slate-100 hover:text-slate-600 dark:hover:bg-slate-800" @click="goTo(0)">← 查看大纲</button>
            <span>角色已就绪，正在进入页面生成…</span>
          </div>
        </section>

        <section v-else-if="activeStep.key === 'scenes'" class="anim-fade-up rounded-2xl border border-slate-200/70 bg-white/80 p-6 shadow-sm backdrop-blur dark:border-slate-800 dark:bg-slate-900/60">
          <header class="flex items-center gap-2">
            <Layers class="size-5 text-blue-600 dark:text-blue-400" />
            <h2 class="text-lg font-semibold text-slate-900 dark:text-slate-50">正在生成场景</h2>
            <span class="ml-auto text-sm tabular-nums text-slate-500">{{ readyCount }}/{{ totalScenes }} 完成</span>
          </header>

          <div class="mt-4 h-1.5 w-full overflow-hidden rounded-full bg-slate-100 dark:bg-slate-800">
            <div class="h-full rounded-full bg-gradient-to-r from-blue-500 to-emerald-500 transition-all duration-500" :style="{ width: `${scenePercent}%` }" />
          </div>

          <ul v-if="scenes.length" class="mt-5 space-y-2">
            <li
              v-for="(scene, index) in scenes"
              :key="scene.id"
              class="anim-fade-up flex items-center gap-3 rounded-xl border p-3 transition"
              :class="scene.status === 'ready' ? 'border-emerald-200 bg-emerald-50/50 dark:border-emerald-900 dark:bg-emerald-950/20' : 'border-slate-200 dark:border-slate-800'"
              :style="{ animationDelay: `${index * 50}ms` }"
            >
              <span class="flex size-7 shrink-0 items-center justify-center rounded-full bg-slate-100 text-xs text-slate-500 dark:bg-slate-800">
                <Check v-if="scene.status === 'ready'" class="size-4 text-emerald-600 dark:text-emerald-400" />
                <Loader2 v-else-if="scene.status === 'generating'" class="size-4 animate-spin text-blue-500" />
                <AlertCircle v-else-if="scene.status === 'failed'" class="size-4 text-rose-500" />
                <span v-else>{{ scene.sort_order + 1 }}</span>
              </span>
              <span class="min-w-0 flex-1">
                <span class="block truncate text-sm font-medium text-slate-900 dark:text-slate-100">{{ scene.title || `第 ${scene.sort_order + 1} 页` }}</span>
                <span class="block truncate text-xs text-slate-400">{{ statusText(scene) }}</span>
              </span>
              <span class="shrink-0 rounded-full bg-slate-100 px-2 py-0.5 text-xs text-slate-500 dark:bg-slate-800">{{ typeLabel(scene.type) }}</span>
            </li>
          </ul>
          <p v-else class="mt-5 text-center text-sm text-slate-400">正在准备页面…</p>

          <div class="mt-5 flex items-center justify-between text-xs text-slate-400">
            <button type="button" class="rounded-lg px-2 py-1 transition hover:bg-slate-100 hover:text-slate-600 dark:hover:bg-slate-800" @click="goTo(1)">← 查看角色</button>
            <span v-if="!canEnter">任一页生成完成后即可进入课堂</span>
          </div>

          <div v-if="canEnter" class="mt-6 flex justify-center">
            <button
              type="button"
              class="inline-flex items-center gap-2 rounded-xl bg-blue-600 px-5 py-2.5 text-sm font-medium text-white shadow-lg shadow-blue-500/25 transition hover:bg-blue-700"
              @click="enterClassroom"
            >
              已有页面可以学习，进入课堂<ArrowRight class="size-4" />
            </button>
          </div>
        </section>

        <section v-else class="anim-fade-up rounded-2xl border border-slate-200/70 bg-white/80 p-10 text-center shadow-sm backdrop-blur dark:border-slate-800 dark:bg-slate-900/60">
          <div class="mx-auto flex size-16 items-center justify-center rounded-full bg-emerald-500 text-white shadow-lg shadow-emerald-500/30">
            <Rocket class="size-7" />
          </div>
          <h2 class="mt-4 text-xl font-semibold text-slate-900 dark:text-slate-50">
            {{ failedCount > 0 ? '部分页面已准备好' : '课堂已准备好' }}
          </h2>
          <p class="mt-1.5 text-sm text-slate-500">
            共 {{ totalScenes }} 页，{{ readyCount }} 页已完成<template v-if="failedCount > 0">，{{ failedCount }} 页失败</template>
          </p>

          <button
            type="button"
            class="mt-6 inline-flex items-center gap-2 rounded-xl bg-blue-600 px-6 py-3 text-sm font-medium text-white shadow-lg shadow-blue-500/25 transition hover:bg-blue-700"
            @click="enterClassroom"
          >
            进入课堂<ArrowRight class="size-4" />
          </button>
          <div class="mt-3">
            <button type="button" class="text-xs text-slate-400 transition hover:text-slate-600" @click="goTo(2)">返回查看生成进度</button>
          </div>
        </section>
      </template>
    </div>
  </main>
</template>

<style scoped>
@keyframes blob-float {
  0%, 100% { transform: translateY(0) scale(1); }
  50% { transform: translateY(-26px) scale(1.06); }
}
.blob { animation: blob-float 9s ease-in-out infinite; }

@keyframes fade-up-in {
  from { opacity: 0; transform: translateY(10px); }
  to { opacity: 1; transform: translateY(0); }
}
.anim-fade-up { animation: fade-up-in 0.5s ease both; }

@keyframes shimmer-sweep {
  0% { background-position: -200% 0; }
  100% { background-position: 200% 0; }
}
.shimmer {
  background-image: linear-gradient(90deg, rgba(148, 163, 184, 0.12) 25%, rgba(148, 163, 184, 0.3) 37%, rgba(148, 163, 184, 0.12) 63%);
  background-size: 200% 100%;
  animation: shimmer-sweep 1.6s linear infinite;
}

@keyframes indeterminate-slide {
  0% { transform: translateX(-100%); }
  100% { transform: translateX(300%); }
}
.indeterminate { animation: indeterminate-slide 1.5s ease-in-out infinite; }

@media (prefers-reduced-motion: reduce) {
  .blob, .anim-fade-up, .shimmer, .indeterminate { animation: none; }
}
</style>
