<script setup lang="ts">
/**
 * 课堂生成进行页：把「一次全铺开」改成有节奏的分步流程。
 *
 * 步骤：生成大纲 → 课堂角色 → 生成场景 → 进入课堂，顶部是可回退的节点条。
 * 进度全部由后端的生成进度流推过来，这里一次轮询都不做：
 *   - classroom 事件：整份课堂（状态、标题）；
 *   - scene.snapshot 事件：全部页面的进度（连上时、大纲落库时、页面增减时各来一份）；
 *   - scene.* 事件：单页进度变化（检索资料/写内容/写讲稿/审核/合成语音/完成/失败）。
 * 角色只在挂载时拉一次：它们在受理时就已经写进库，之后再不会变。
 * 标题也跟着流走 —— 大纲落库后后端会把 classrooms.title 回填成模型起的标题。
 * 大纲结构另外取一次 /outline：它带每页简介，比场景摘要更适合只读展示；落库后不再变，不必进流。
 */
import { computed, onMounted, onUnmounted, ref, watch, type Component } from 'vue'
import { useRouter } from 'vue-router'
import {
  AlertCircle, ArrowRight, Check, FileText, Layers, Loader2, Paperclip, Rocket, Sparkles, Users,
} from 'lucide-vue-next'
import {
  fetchClassroom, fetchClassroomAgents, fetchClassroomOutline, fetchClassroomScenes,
  streamClassroomEvents,
  type ClassroomDTO, type ClassroomOutlineDTO, type ClassroomSceneSummaryDTO, type RoleCardDTO,
} from '@/api/classroom'

const props = defineProps<{ id: string }>()
const router = useRouter()

const classroom = ref<ClassroomDTO | null>(null)
const agents = ref<RoleCardDTO[]>([])
const scenes = ref<ClassroomSceneSummaryDTO[]>([])
/** 大纲结构（含每页简介）；拉不到时这一屏退回用 scenes 渲染。 */
const outlineScenes = ref<ClassroomOutlineDTO['scenes']>([])
const loading = ref(true)
const error = ref('')

let eventController: AbortController | undefined
const timers = new Set<number>()

const classroomId = computed(() => Number(props.id))

/** 生成大纲期间轮播的拟态文案，让等待不显得干。 */
const outlinePhases = ['正在理解你的需求…', '正在规划页面结构…', '正在拟定教学目标…', '正在生成课程大纲…', '就快好了…']
const phaseIndex = ref(0)
let phaseTimer: number | undefined

const title = computed(() => classroom.value?.title || '正在生成课堂')
/** 大纲（也就是计划）落库的标志：场景行出现了。 */
const outlineReady = computed(() => scenes.value.length > 0)
const totalScenes = computed(() => scenes.value.length)
const readyCount = computed(() => scenes.value.filter((scene) => scene.status === 'ready').length)
const failedCount = computed(() => scenes.value.filter((scene) => scene.status === 'failed').length)
const settledCount = computed(() => readyCount.value + failedCount.value)
const scenePercent = computed(() => (totalScenes.value === 0 ? 0 : Math.round((settledCount.value / totalScenes.value) * 100)))
/** 有任一页就绪即可进课堂；整课 ready 也算。不拿 playable 当条件——它在页面就绪前就会置上。 */
const canEnter = computed(() => readyCount.value > 0 || classroom.value?.status === 'ready')

/**
 * 大纲这一屏要展示的页。
 *
 * 优先用 /outline 的结果——它有每页简介，且这一屏刻意不显示生成状态（那是「生成场景」的职责）；
 * /outline 没拉到就退回场景摘要，至少把结构与页型列出来。
 */
const outlineRows = computed(() => (
  outlineScenes.value.length
    ? outlineScenes.value.map((scene) => ({ id: scene.id, title: scene.title, type: scene.type, brief: scene.brief }))
    : scenes.value.map((scene) => ({ id: scene.id, title: scene.title, type: scene.type, brief: '' }))
))

/**
 * 生成是否已经不会再有变化——用来停掉动画并断开事件流。
 * 只看 ready/failed 不够：后端「部分页失败」的终态是 playable，那样永远等不到终态。
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

/**
 * 节点是否该转圈。
 *
 * 不能只看「是不是当前选中」——回退到大纲看结构时，它早已完成，还转圈会让人以为在重新生成。
 * 大纲与角色都是一次成型的，只有「生成场景」要一直转到所有页出结果。
 */
function stepBusy(index: number) {
  if (index === 0) return !outlineReady.value
  if (index === 1) return false
  return !generationDone.value
}

function nodeClass(index: number) {
  switch (stepState(index)) {
    case 'done': return 'border-transparent bg-emerald-500 text-white'
    case 'active': return 'border-transparent bg-brand-700 text-white ring-4 ring-brand-600/20'
    case 'ready': return 'border-brand-200 bg-card text-brand-700 hover:border-brand-400 dark:border-slate-700 dark:bg-slate-900 dark:text-brand-300'
    default: return 'border-slate-200 bg-slate-100 text-slate-400 dark:border-slate-800 dark:bg-slate-800/60'
  }
}

function labelClass(index: number) {
  const state = stepState(index)
  if (state === 'locked') return 'text-slate-400'
  if (state === 'active') return 'text-brand-700 dark:text-brand-300'
  return 'text-slate-700 dark:text-slate-200'
}

/** 单页状态说明；还在生成时用后端给的阶段，把笼统的「正在生成」说细一点。 */
function statusText(scene: ClassroomSceneSummaryDTO) {
  switch (scene.status) {
    case 'ready': return '已完成'
    case 'failed': return scene.error_message || '生成失败'
    case 'pending': return '等待生成'
    default: break
  }
  switch (scene.phase) {
    case 'planning': return '正在规划这一页…'
    case 'researching': return '正在检索资料…'
    case 'generating_content': return '正在生成内容…'
    case 'generating_narration': return '正在撰写讲稿…'
    case 'reviewing': return '正在审核…'
    case 'synthesizing': return '正在合成语音…'
    default: return '正在生成…'
  }
}

function typeLabel(type: string) {
  switch (type) {
    case 'quiz': return '测验'
    case 'interactive': return '交互'
    default: return '图文'
  }
}

/** 单页事件带的是这一页的完整摘要，按 id 覆盖即可；没见过的新页按顺序插进去。 */
function upsertScene(scene: ClassroomSceneSummaryDTO) {
  const index = scenes.value.findIndex((item) => item.id === scene.id)
  if (index < 0) {
    scenes.value = [...scenes.value, scene].sort((left, right) => left.sort_order - right.sort_order)
    return
  }
  scenes.value = scenes.value.map((item) => (item.id === scene.id ? scene : item))
}

/**
 * 大纲就绪那一刻：停掉等待动画、取一次结构、把流程往前推。
 *
 * immediate 是必要的：直接进这个页（或刷新）时，大纲在挂载那一刻就已经落库了，
 * 没有「从没就绪变成就绪」这一步，普通侦听不会触发，轮播会一直转下去。
 */
watch(outlineReady, (ready) => {
  if (phaseTimer) {
    window.clearInterval(phaseTimer)
    phaseTimer = undefined
  }
  if (!ready) return
  void loadOutline()
  if (activeIndex.value < 1) later(() => reach(1), 1400)
}, { immediate: true })

watch(activeIndex, (index) => {
  // 角色只展示一小会儿，第一次进入时自动走向场景；回退时不重复触发。
  if (index === 1 && !rolesAdvanced) {
    rolesAdvanced = true
    later(() => reach(2), 2000)
  }
})

/** 第一页就绪后停一下再自动进课堂：让节点条先亮到「进入课堂」，跳得不那么突然。 */
const autoEnterDelayMs = 1200

/**
 * 这一趟进来有没有自动跳过。只在状态由「一页可学的都没有」翻成「有了」的那一刻跳一次。
 *
 * 进来时就已经有页可学却不跳：一门课一旦有过 ready 页，从卡片进会被分流直接送进课堂，
 * 生成进度页就只剩直接敲 URL 这一条路；这里不抢跳，那个页面才留得住——页面上「进入课堂」
 * 的按钮也始终是它的出口。syncInitialStep 因此必须把 autoEntered 一起置位。
 */
let autoEntered = false

watch(canEnter, (ok) => {
  if (!ok) return
  if (activeIndex.value < 3) later(() => reach(3), 700)
  if (autoEntered) return
  autoEntered = true
  later(enterClassroom, autoEnterDelayMs)
})

async function load() {
  const id = classroomId.value
  if (!Number.isInteger(id) || id <= 0) throw new Error('课堂 ID 无效')
  const [current, currentAgents, currentScenes] = await Promise.all([
    fetchClassroom(id), fetchClassroomAgents(id), fetchClassroomScenes(id),
  ])
  classroom.value = current
  agents.value = currentAgents
  scenes.value = currentScenes
}

/** 取一次大纲结构。拉不到就退回场景摘要渲染，不影响这一屏的其它功能。 */
async function loadOutline() {
  try {
    const data = await fetchClassroomOutline(classroomId.value)
    outlineScenes.value = data.scenes
  } catch {
    outlineScenes.value = []
  }
}

/** 刷新中途进入时，直接落到与当前进度相符的节点，不从头播一遍。 */
function syncInitialStep() {
  if (canEnter.value) {
    maxReached.value = 3
    activeIndex.value = 3
    rolesAdvanced = true
    // 已经有页可学还停在进度页，说明是直接进来的，不是刚看着第一页生成完——别抢跳。
    autoEntered = true
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

function enterClassroom() {
  router.push({ name: 'classroom', params: { id: props.id } })
}

onMounted(async () => {
  eventController = new AbortController()
  try {
    await load()
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '加载课堂生成状态失败'
  }
  loading.value = false
  if (classroom.value?.status === 'failed') error.value = classroom.value.generation_error ?? '课堂生成失败'

  syncInitialStep()

  // 轮播只在真等着大纲时才转；进来时它已经就绪就别再假动。
  if (!outlineReady.value) {
    phaseTimer = window.setInterval(() => {
      phaseIndex.value = (phaseIndex.value + 1) % outlinePhases.length
    }, 2400)
  }

  try {
    for await (const event of streamClassroomEvents(classroomId.value, eventController.signal)) {
      switch (event.kind) {
        case 'classroom': classroom.value = event.classroom; break
        case 'snapshot': scenes.value = event.scenes; break
        case 'scene': upsertScene(event.scene); break
        case 'error': if (!error.value) error.value = event.message; break
        // plan-started / plan-completed / stage 目前不单独处理：页面进度与标题已经
        // 由 snapshot、scene 和 classroom 三种事件表达完了。
        default: break
      }
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
  if (phaseTimer) window.clearInterval(phaseTimer)
  timers.forEach((id) => window.clearTimeout(id))
  timers.clear()
})
</script>

<template>
  <main class="narra-page narra-generating relative min-h-screen overflow-hidden bg-slate-50 p-6 dark:bg-slate-950">
    <div class="pointer-events-none absolute inset-0 overflow-hidden">
      <div class="narra-decorative-orb blob absolute -top-32 left-[12%] size-96 rounded-full bg-brand-400/20 blur-3xl" />
      <div class="narra-decorative-orb blob absolute top-1/3 -right-24 size-96 rounded-full bg-brand-400/20 blur-3xl" style="animation-delay: -3s" />
      <div class="narra-decorative-orb blob absolute -bottom-32 left-1/3 size-96 rounded-full bg-emerald-400/15 blur-3xl" style="animation-delay: -6s" />
    </div>

    <div class="relative mx-auto max-w-3xl space-y-5">
      <header class="text-center">
        <p class="text-xs font-medium tracking-widest text-slate-400 uppercase">Narra · 课堂生成</p>
        <h1 class="mt-1 text-2xl font-semibold text-slate-900 dark:text-slate-50">{{ title }}</h1>
        <!-- 本课材料：受理时选中的文件快照；旧课堂没有材料时整行不出现 -->
        <p
          v-if="classroom?.materials?.length"
          class="mt-2 flex flex-wrap items-center justify-center gap-1.5 text-xs text-slate-500 dark:text-slate-400"
        >
          <Paperclip class="size-3.5 shrink-0" />
          <span>本课材料（{{ classroom.materials.length }}）：</span>
          <span
            v-for="material in classroom.materials"
            :key="material.document_id"
            class="rounded-full border border-slate-200 bg-card/70 px-2 py-0.5 text-slate-600 dark:border-slate-700 dark:bg-slate-900/60 dark:text-slate-300"
          >
            {{ material.name }}
          </span>
        </p>
      </header>

      <div v-if="loading" class="flex items-center justify-center gap-3 rounded-2xl border border-slate-200 bg-card/80 p-10 text-sm text-slate-500 backdrop-blur dark:border-slate-800 dark:bg-slate-900/60">
        <Loader2 class="size-5 animate-spin" />正在读取课堂状态…
      </div>

      <div v-else-if="error && !classroom" class="flex items-center gap-3 rounded-2xl border border-rose-200 bg-rose-50 p-6 text-sm text-rose-700 dark:border-rose-900 dark:bg-rose-950/40 dark:text-rose-300">
        <AlertCircle class="size-5 shrink-0" />{{ error }}
      </div>

      <template v-else>
        <div v-if="error" class="flex items-center gap-3 rounded-2xl border border-gold-200 bg-gold-50 p-4 text-sm text-gold-800 dark:border-gold-900 dark:bg-gold-900/40 dark:text-gold-300">
          <AlertCircle class="size-5 shrink-0" />{{ error }}
        </div>

        <nav class="rounded-2xl border border-slate-200 bg-card/80 p-3 backdrop-blur dark:border-slate-800 dark:bg-slate-900/60">
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
                  <Loader2 v-else-if="stepState(index) === 'active' && stepBusy(index)" class="size-4 animate-spin" />
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

        <section v-if="activeStep.key === 'outline'" class="anim-fade-up rounded-2xl border border-slate-200 bg-card/80 p-8 shadow-sm backdrop-blur dark:border-slate-800 dark:bg-slate-900/60">
          <div v-if="!outlineReady" class="flex flex-col items-center text-center">
            <div class="relative mb-6 flex size-16 items-center justify-center">
              <span class="absolute inset-0 animate-ping rounded-full bg-brand-600/20" />
              <span class="relative flex size-16 items-center justify-center rounded-full bg-brand-700 text-white shadow-lg shadow-brand-600/30">
                <Sparkles class="size-7" />
              </span>
            </div>
            <h2 class="text-lg font-semibold text-slate-900 dark:text-slate-50">正在生成课程大纲</h2>
            <p class="mt-1.5 h-5 text-sm text-brand-700 dark:text-brand-300">{{ outlinePhases[phaseIndex] }}</p>

            <div class="mt-8 w-full space-y-3">
              <div v-for="line in 4" :key="line" class="shimmer h-4 rounded-full" :style="{ width: `${100 - line * 7}%` }" />
            </div>

            <div class="mt-8 h-1 w-full overflow-hidden rounded-full bg-slate-100 dark:bg-slate-800">
              <div class="indeterminate h-full w-1/3 rounded-full bg-brand-600" />
            </div>
            <p class="mt-3 text-xs text-slate-400">大纲完成后会自动进入下一步</p>
          </div>

          <div v-else>
            <header class="flex items-center gap-2">
              <FileText class="size-5 text-brand-700 dark:text-brand-400" />
              <h2 class="text-lg font-semibold text-slate-900 dark:text-slate-50">课程大纲</h2>
              <span class="ml-auto text-xs text-slate-400">{{ outlineRows.length }} 页</span>
            </header>

            <ol class="mt-5 space-y-2">
              <li
                v-for="(row, index) in outlineRows"
                :key="row.id"
                class="anim-fade-up flex items-start gap-3 rounded-xl border border-slate-200 p-3 dark:border-slate-800"
                :style="{ animationDelay: `${index * 40}ms` }"
              >
                <span class="mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-full bg-slate-100 text-xs text-slate-500 tabular-nums dark:bg-slate-800">{{ index + 1 }}</span>
                <span class="min-w-0 flex-1">
                  <span class="block text-sm font-medium text-slate-900 dark:text-slate-100">{{ row.title || `第 ${index + 1} 页` }}</span>
                  <span v-if="row.brief" class="mt-0.5 block text-xs leading-5 text-slate-500 dark:text-slate-400">{{ row.brief }}</span>
                </span>
                <span class="shrink-0 rounded-full bg-slate-100 px-2 py-0.5 text-xs text-slate-500 dark:bg-slate-800">{{ typeLabel(row.type) }}</span>
              </li>
            </ol>

            <div class="mt-5 flex items-center justify-between text-xs text-slate-400">
              <span>大纲已落库，页面按这份结构逐页生成</span>
              <button
                v-if="maxReached >= 2"
                type="button"
                class="rounded-lg px-2 py-1 transition hover:bg-slate-100 hover:text-slate-600 dark:hover:bg-slate-800"
                @click="goTo(2)"
              >查看生成进度 →</button>
            </div>
          </div>
        </section>

        <section v-else-if="activeStep.key === 'roles'" class="anim-fade-up rounded-2xl border border-slate-200 bg-card/80 p-6 shadow-sm backdrop-blur dark:border-slate-800 dark:bg-slate-900/60">
          <header class="flex items-center gap-2">
            <Users class="size-5 text-brand-700 dark:text-brand-400" />
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

        <section v-else-if="activeStep.key === 'scenes'" class="anim-fade-up rounded-2xl border border-slate-200 bg-card/80 p-6 shadow-sm backdrop-blur dark:border-slate-800 dark:bg-slate-900/60">
          <header class="flex items-center gap-2">
            <Layers class="size-5 text-brand-700 dark:text-brand-400" />
            <h2 class="text-lg font-semibold text-slate-900 dark:text-slate-50">正在生成场景</h2>
            <span class="ml-auto text-sm tabular-nums text-slate-500">{{ readyCount }}/{{ totalScenes }} 完成</span>
          </header>

          <div class="mt-4 h-1.5 w-full overflow-hidden rounded-full bg-slate-100 dark:bg-slate-800">
            <div class="h-full rounded-full bg-gradient-to-r from-brand-600 to-emerald-500 transition-all duration-500" :style="{ width: `${scenePercent}%` }" />
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
                <Loader2 v-else-if="scene.status === 'generating'" class="size-4 animate-spin text-brand-600" />
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
              class="inline-flex items-center gap-2 rounded-xl bg-brand-700 px-5 py-2.5 text-sm font-medium text-white shadow-lg shadow-brand-600/25 transition hover:bg-brand-700"
              @click="enterClassroom"
            >
              已有页面可以学习，进入课堂<ArrowRight class="size-4" />
            </button>
          </div>
        </section>

        <section v-else class="anim-fade-up rounded-2xl border border-slate-200 bg-card/80 p-10 text-center shadow-sm backdrop-blur dark:border-slate-800 dark:bg-slate-900/60">
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
            class="mt-6 inline-flex items-center gap-2 rounded-xl bg-brand-700 px-6 py-3 text-sm font-medium text-white shadow-lg shadow-brand-600/25 transition hover:bg-brand-700"
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

/*
 * 减少动态效果时停掉装饰性动画。但 indeterminate 那条线不停：它是「后台还在干活」
 * 的唯一信号，静止时会停在轨道三分之一处，比不动更像卡住；同屏的图标转圈也照常跑。
 */
@media (prefers-reduced-motion: reduce) {
  .blob, .anim-fade-up, .shimmer { animation: none; }
}
</style>
