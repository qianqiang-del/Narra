<script setup lang="ts">
/**
 * 首页 / 落地页 —— 对应旧 app/page.tsx（1896 行）。
 * 完整结构见 docs/UI-还原文档.md §5。
 *
 * 组装顺序：根容器 → 背景光斑 → 胶囊工具栏 → Hero(logo/slogan)
 *          → Composer（GreetingBar + textarea + AgentBar + 工具栏 + 发送）
 *          → 最近学习折叠区 → 页脚。
 */
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { ArrowUp, Atom, Check, Loader2, Mic } from 'lucide-vue-next'
import { toast } from 'vue-sonner'

import { createClassroom } from '@/api/classroom'
import { ApiError } from '@/api/client'
import {
  fetchKnowledgeDocument,
  retryKnowledgeDocument,
  uploadKnowledgeFiles,
} from '@/api/knowledge'
import AgentBar from '@/components/home/AgentBar.vue'
import GenerationToolbar from '@/components/home/GenerationToolbar.vue'
import GreetingBar from '@/components/home/GreetingBar.vue'
import ProBadge from '@/components/home/ProBadge.vue'
import RecentSection from '@/components/home/RecentSection.vue'
import SettingsDialog from '@/components/home/SettingsDialog.vue'
import TopPillToolbar from '@/components/home/TopPillToolbar.vue'
import UiTooltip from '@/components/ui/UiTooltip.vue'
import { pollDocumentUntilSettled } from '@/lib/ingest-wait'
import { MATERIAL_PURPOSE, type SelectedMaterial } from '@/lib/materials'
import { cn } from '@/lib/utils'
import { useLlmStore } from '@/stores/llm'
import { useProfileStore } from '@/stores/profile'
import { useLibraryStore } from '@/stores/library'
import { storeToRefs } from 'pinia'

const { t } = useI18n()
const router = useRouter()
const llmStore = useLlmStore()
const profileStore = useProfileStore()
const library = useLibraryStore()
const { availableModels } = storeToRefs(llmStore)

const settingsOpen = ref(false)
const settingsSection = ref<'theme' | 'llm' | 'embedding' | 'mcp'>('theme')
const requirement = ref('')
const textareaRef = ref<HTMLTextAreaElement | null>(null)
const generating = ref(false)
/** 材料准备阶段：上传 → 等收录；idle 表示当前不在准备材料 */
const prepareStage = ref<'idle' | 'uploading' | 'waiting'>('idle')

const submitLabel = computed(() => {
  if (!generating.value) return t('home.enterClassroom')
  if (prepareStage.value === 'uploading') return t('home.uploadingMaterials')
  if (prepareStage.value === 'waiting') return t('home.processingMaterials')
  return t('home.generating')
})

/** 深度交互模式 */
const interactiveMode = ref(false)

/** 生成配置（透传给 GenerationToolbar / AgentBar） */
const providerId = ref<number | null>(null)
const modelId = ref('')
const webSearch = ref(false)
const materials = ref<SelectedMaterial[]>([])

const agentMode = ref<'preset' | 'auto'>('preset')
/** 预设模式勾选的角色，默认一个都不选 */
const selectedRoleIds = ref<string[]>([])
const ttsEnabled = ref(true)
/**
 * 教师音色。空串表示「还没定」，由 AgentBar 在角色池拉回来之后填该教师的默认音色。
 * 这里不能写死音色 ID——池子里那个教师的 voice_id 改了，前端要跟着变。
 */
const teacherVoice = ref('')

/** AgentBar 实例；每个角色的音色由它自己管，要读它暴露的 roleVoices */
const agentBarRef = ref<InstanceType<typeof AgentBar> | null>(null)

const hasProvider = computed(() => availableModels.value.length > 0)
const canSubmit = computed(() => requirement.value.trim().length > 0 && hasProvider.value && providerId.value !== null && modelId.value !== '' && !generating.value)

function selectFirstAvailableModel() {
  const currentStillExists = availableModels.value.some(
    (item) => item.providerId === providerId.value && item.modelId === modelId.value,
  )
  if (currentStillExists) return
  const first = availableModels.value[0]
  providerId.value = first?.providerId ?? null
  modelId.value = first?.modelId ?? ''
}

onMounted(async () => {
  const results = await Promise.allSettled([
    llmStore.loadAvailableModels(), library.loadClassrooms(), library.loadFolders(),
  ])
  if (results[2]?.status === 'rejected') toast.error('加载文件夹失败')
  selectFirstAvailableModel()
})

watch(availableModels, selectFirstAvailableModel)
watch(settingsOpen, async (value, oldValue) => {
  if (!value && oldValue) {
    try { await llmStore.loadAvailableModels() } catch { /* 保留当前空状态 */ }
  }
})

function openModelSettings() {
  settingsSection.value = 'llm'
  settingsOpen.value = true
}

/** textarea 自增高（140~300px） */
function autoGrow() {
  const el = textareaRef.value
  if (!el) return
  el.style.height = 'auto'
  el.style.height = `${Math.min(Math.max(el.scrollHeight, 140), 300)}px`
}
watch(requirement, () => nextTick(autoGrow))

/** 只取已勾选角色的音色；没勾的不发，缺省交给后端用角色默认音色。 */
function pickedRoleVoices(): Record<string, string> {
  if (agentMode.value !== 'preset') return {}
  const voices = agentBarRef.value?.roleVoices ?? {}
  const picked: Record<string, string> = {}
  for (const key of selectedRoleIds.value) {
    const voice = voices[key]
    if (voice) picked[key] = voice
  }
  return picked
}

/**
 * 材料状态的轮询间隔。比单篇重试的 1s 略慢：首页可能同时有多份材料在等，
 * 与知识库批量上传的 1.5s 保持同一个节奏。
 */
const MATERIAL_POLL_INTERVAL_MS = 1500

/** 材料上传的共享 Promise：上传进行中时，提交与 watcher 都等同一轮，不重复发请求。 */
let materialUploadPromise: Promise<void> | null = null

/**
 * 把待上传的材料送进知识库（选中即上传，不等到提交）。
 *
 * 失败/被拒的材料留在列表里由用户处置（重试会退回 queued、再次触发这里）；
 * 上传只负责入库，等收录完成是 waitPendingMaterials 的事。
 */
async function runMaterialUpload(): Promise<void> {
  // 循环而不是只跑一轮：上传期间用户可能又选了文件（watcher 拿到的是同一个 Promise）。
  for (;;) {
    const queued = materials.value.filter((m) => m.status === 'queued')
    if (queued.length === 0) break
    for (const material of queued) material.status = 'uploading'
    try {
      const batch = await uploadKnowledgeFiles(
        queued.map((m) => m.file),
        undefined,
        MATERIAL_PURPOSE,
      )
      batch.items.forEach((item, index) => {
        const material = queued[index]
        if (!material) return
        if (item.status === 'pending' && item.documentId !== null) {
          material.documentId = item.documentId
          material.status = 'pending'
          material.error = ''
        } else {
          material.status = 'rejected'
          material.error = item.error || t('toolbar.materialRejected')
        }
      })
      // 返回条目数与文件数对不上时，没被服务端接住的按失败处理（理论上不会发生）
      for (const material of queued) {
        if (material.status === 'uploading') {
          material.status = 'failed'
          material.error = t('toolbar.materialUploadFailed')
        }
      }
      // 传完立刻开始后台等待收录：状态实时收敛，不必等用户点生成
      // （与知识库页批量上传的效果一致，只是这里逐篇轮询文档状态）。
      void waitPendingMaterials()
    } catch (error) {
      for (const material of queued) {
        material.status = 'failed'
        material.error = error instanceof ApiError ? error.message : t('toolbar.materialUploadFailed')
      }
    }
  }
}

/** 材料收录等待的共享 Promise：后台轮询与提交前等待复用同一轮。 */
let materialWaitPromise: Promise<void> | null = null

/**
 * 后台轮询待收录材料直到终态（ready / failed），把状态实时刷到列表上。
 *
 * 与知识库页共用 lib/ingest-wait 的等待口径：首次上传 PDF 等文件时后端在准备
 * 解析环境（分钟级），这段时间不算"材料处理超时"，准备结束才重新计时。不用等
 * 用户点生成，材料自己从「处理中」收敛到「已就绪 / 失败」；失败项由用户重试
 * （有文档的原地重排，没有的退回 queued 重新上传）。
 */
async function runMaterialWait(): Promise<void> {
  for (;;) {
    const pending = materials.value.filter((m) => m.status === 'pending' && m.documentId !== null)
    if (pending.length === 0) break
    await Promise.all(
      pending.map(async (material) => {
        try {
          const document = await pollDocumentUntilSettled(material.documentId as number, {
            intervalMs: MATERIAL_POLL_INTERVAL_MS,
          })
          if (document.status === 'ready') {
            material.status = 'ready'
            material.error = ''
          } else {
            material.status = 'failed'
            material.error = document.error || t('toolbar.materialProcessFailed')
          }
        } catch (error) {
          material.status = 'failed'
          // 超时/准备卡住带的是具体原因（见 lib/ingest-wait），原样展示。
          material.error = error instanceof Error ? error.message : t('toolbar.materialTimeout')
        }
      }),
    )
  }
}

function waitPendingMaterials(): Promise<void> {
  if (!materialWaitPromise) {
    materialWaitPromise = runMaterialWait().finally(() => {
      materialWaitPromise = null
    })
  }
  return materialWaitPromise
}

function uploadQueuedMaterials(): Promise<void> {
  if (!materialUploadPromise) {
    materialUploadPromise = runMaterialUpload().finally(() => {
      materialUploadPromise = null
    })
  }
  return materialUploadPromise
}

/**
 * 正在原地重排的材料 key。防连点：第二次请求会撞上"已不是失败态"的 409，
 * 把第一次已经成功的重排覆盖成失败。
 */
const retryingMaterials = new Set<string>()

/**
 * 重试一份失败/被拒的材料。
 *
 * 有服务端文档的走**原地重排**（与知识库弹层的重试同一条路）：先回读文档的真实状态 ——
 *   - ready：它其实已经收录完了（列表状态是上次等待超时留下的），直接收敛；
 *   - pending / processing：之前那次"失败"是前端等待超时误判，服务端还在照常收录，
 *     接着等即可，不重新上传；
 *   - failed：确实是收录失败，调 retry 让后端复用服务器上的原件原地重跑，
 *     不新建文档、不重传文件。
 *
 * 探测期间材料保持 failed 不置 pending：后台等待循环只认 pending，提前置上会让它
 * 抢在 retry 落库前看到旧文档的 failed 终态，把材料重新定死成失败。
 *
 * 没有 documentId 的（上传就没成功、被服务端拒绝）服务器上没有可重排的对象，
 * 退回 queued 由 runMaterialUpload 重传 —— 这是本地文件唯一的用法。
 */
async function retryMaterial(key: string): Promise<void> {
  const material = materials.value.find((item) => item.key === key)
  if (!material || (material.status !== 'failed' && material.status !== 'rejected')) return
  if (retryingMaterials.has(key)) return

  if (material.documentId === null) {
    material.status = 'queued'
    material.error = ''
    return
  }

  retryingMaterials.add(key)
  try {
    const document = await fetchKnowledgeDocument(material.documentId)
    if (document.status === 'ready') {
      material.status = 'ready'
      material.error = ''
      return
    }
    if (document.status === 'failed') {
      await retryKnowledgeDocument(material.documentId)
    }
    // 服务端确认在收录（原地重排成功，或本来还在跑）：置 pending 进入等待。
    material.status = 'pending'
    material.error = ''
    void waitPendingMaterials()
  } catch (error) {
    material.status = 'failed'
    material.error = error instanceof ApiError ? error.message : t('toolbar.materialRetryFailed')
  } finally {
    retryingMaterials.delete(key)
  }
}

// 选中文件（或点重试退回 queued）后立刻上传：解析与向量化在用户写需求的这段时间里
// 就已经在跑，点生成时大多已经 ready，不必再等。
watch(
  () => materials.value.filter((m) => m.status === 'queued').length,
  (count) => {
    if (count > 0) void uploadQueuedMaterials()
  },
)

/**
 * 提交前的材料准备：确保都上传过，再等它们收录完成。
 *
 * 返回真正能带进建课请求的材料（ready）；失败/被拒/超时的材料留在列表里由用户
 * 处置，本次跳过并提示。材料出问题不该把"生成课堂"整个拦死。
 */
async function prepareMaterials(): Promise<SelectedMaterial[]> {
  if (materialUploadPromise) prepareStage.value = 'uploading'
  await uploadQueuedMaterials()

  // 后台轮询可能已经在跑（上传完成就开始了）；这里复用同一轮，通常无需再等。
  if (materials.value.some((m) => m.status === 'pending' && m.documentId !== null)) {
    prepareStage.value = 'waiting'
    await waitPendingMaterials()
  }
  prepareStage.value = 'idle'

  const usable = materials.value.filter((m) => m.status === 'ready' && m.documentId !== null)
  const skipped = materials.value.length - usable.length
  if (skipped > 0) toast.warning(t('toolbar.materialSkipped', { count: skipped }))
  return usable
}

async function submit() {
  if (!hasProvider.value) {
    openModelSettings()
    toast.error(t('home.modelRequired'))
    return
  }
  if (!canSubmit.value) return
  const provider = providerId.value
  if (provider === null) return
  generating.value = true
  try {
    const usableMaterials = await prepareMaterials()
    const created = await createClassroom({
      requirement: requirement.value.trim(),
      mode: interactiveMode.value ? 'interactive' : 'vocational',
      llm_provider_id: provider,
      llm_model_id: modelId.value,
      web_search: webSearch.value,
      bio: profileStore.profile.bio.trim(),
      materials: usableMaterials.map((material) => ({
        document_id: material.documentId as number,
        name: material.name,
        size: material.size,
      })),
      agent_mode: agentMode.value,
      role_ids: agentMode.value === 'preset' ? selectedRoleIds.value : [],
      role_voices: pickedRoleVoices(),
      teacher_voice: teacherVoice.value,
    })
    await router.push({ name: 'classroom-generating', params: { id: String(created.id) } })
  } catch (error) {
    toast.error(error instanceof ApiError ? error.message : t('home.generateFailed'))
  } finally {
    generating.value = false
    prepareStage.value = 'idle'
  }
}

function onKeydown(e: KeyboardEvent) {
  if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
    e.preventDefault()
    submit()
  }
}

/**
 * 点课堂卡片去哪：有页生成好了就直接进课堂，一页都没好就先去生成页等。
 *
 * 判据是「已就绪页数」，不是 classrooms.status —— playable 在大纲刚落库、一页都还没生成时
 * 就会置上，拿它当条件会把还没内容的课也放进课堂。列表里查不到这门课（存档没刷新到）时
 * 也走生成页：它自己会拉最新状态，好了就给「进入课堂」，不然也能说明卡在哪。
 */
function openClassroom(id: string) {
  const target = library.classrooms.find((item) => item.id === id)
  const name = target && target.readyPages > 0 ? 'classroom' : 'classroom-generating'
  router.push({ name, params: { id } })
}
</script>

<template>
  <!-- §5.0 根容器 -->
  <div
    class="relative flex min-h-[100dvh] w-full flex-col items-center overflow-x-hidden bg-gradient-to-b from-slate-50 to-slate-100 p-4 pt-16 md:p-8 md:pt-16 dark:from-slate-950 dark:to-slate-900"
  >
    <!-- §5.2 背景光斑装饰 -->
    <div class="pointer-events-none absolute inset-0 overflow-hidden">
      <div
        class="absolute top-0 left-1/4 h-96 w-96 animate-pulse rounded-full bg-blue-500/10 blur-3xl"
        style="animation-duration: 4s"
      />
      <div
        class="absolute right-1/4 bottom-0 h-96 w-96 animate-pulse rounded-full bg-teal-500/10 blur-3xl"
        style="animation-duration: 6s"
      />
    </div>

    <!-- §5.1 右上角悬浮胶囊工具栏 -->
    <TopPillToolbar @open-settings="settingsOpen = true" />

    <!-- §5.3 Hero 区 -->
    <div class="relative z-20 mt-[10vh] flex w-full max-w-[800px] flex-col items-center">
      <!-- Logo + Pro 徽章 -->
      <div class="relative">
        <img src="/logo-horizontal.png" alt="Narra" class="mb-2 -ml-2 h-12 md:-ml-3 md:h-16" />
        <div class="absolute top-0 left-full mt-[10px] ml-1.5 md:mt-[14px] md:ml-2">
          <UiTooltip content="专业模式">
            <ProBadge
              :active="false"
              interactive
              @toggle="router.push({ name: 'workspace' })"
            />
          </UiTooltip>
        </div>
      </div>

      <!-- Slogan -->
      <p class="mb-8 text-sm text-muted-foreground/60">{{ t('home.slogan') }}</p>

      <!-- §5.3 Composer 统一输入卡片 -->
      <div
        class="w-full rounded-2xl border border-border/60 bg-white/80 shadow-xl shadow-black/[0.03] backdrop-blur-xl transition-shadow focus-within:shadow-2xl focus-within:shadow-teal-500/[0.06] dark:bg-slate-900/80 dark:shadow-black/20"
      >
        <!-- 顶部行：GreetingBar（左） / AgentBar（右） -->
        <div class="relative z-20 flex items-start justify-between">
          <GreetingBar />
          <div class="shrink-0 pt-3.5 pr-3">
            <AgentBar
              ref="agentBarRef"
              v-model:mode="agentMode"
              v-model:selected-ids="selectedRoleIds"
              v-model:tts="ttsEnabled"
              v-model:teacher-voice="teacherVoice"
            />
          </div>
        </div>

        <!-- 输入区 -->
        <textarea
          ref="textareaRef"
          v-model="requirement"
          rows="4"
          :placeholder="t('home.requirementPlaceholder')"
          class="max-h-[300px] min-h-[140px] w-full resize-none border-0 bg-transparent px-4 pt-1 pb-2 text-[13px] leading-relaxed placeholder:text-muted-foreground/40 focus:outline-none"
          @keydown="onKeydown"
        />

        <!-- 底部工具行 -->
        <div class="flex items-end gap-2 px-3 pb-3">
          <!-- §5.6 GenerationToolbar -->
          <div class="min-w-0 flex-1">
            <GenerationToolbar
              v-model:provider-id="providerId"
              v-model:model-id="modelId"
              v-model:web-search="webSearch"
              v-model:materials="materials"
              :available-models="availableModels"
              @configure="openModelSettings"
              @retry-material="retryMaterial"
            />
          </div>

          <!-- 深度交互按钮 -->
          <UiTooltip :content="t('toolbar.depthInteractiveHint')">
            <button
              type="button"
              :class="
                cn(
                  'relative inline-flex h-8 shrink-0 cursor-pointer items-center gap-1.5 rounded-full border px-3 py-1.5 text-xs font-medium whitespace-nowrap transition-all select-none active:scale-95',
                  interactiveMode
                    ? 'border-cyan-400 bg-cyan-100 text-cyan-900 shadow-sm shadow-cyan-200/60 dark:border-cyan-200 dark:bg-cyan-400 dark:text-slate-950'
                    : 'border-cyan-600 bg-transparent text-cyan-700 hover:bg-cyan-50 dark:border-cyan-700 dark:text-cyan-300 dark:hover:bg-cyan-950/50',
                )
              "
              @click="interactiveMode = !interactiveMode"
            >
              <Check v-if="interactiveMode" class="size-3.5" />
              <Atom v-else class="size-3.5" />
              {{ t('toolbar.depthInteractive') }}
            </button>
          </UiTooltip>

          <!-- 语音输入 SpeechButton（§5.2，全站唯一的麦克风入口） -->
          <UiTooltip :content="t('voice.startListening')">
            <button
              type="button"
              class="relative flex size-8 shrink-0 cursor-pointer items-center justify-center rounded-lg text-muted-foreground/60 transition-all duration-200 hover:bg-muted/80 hover:text-muted-foreground"
            >
              <Mic class="size-4" />
            </button>
          </UiTooltip>

          <!-- 发送按钮 -->
          <button
            type="button"
            :class="
              cn(
                'flex h-8 shrink-0 items-center justify-center gap-1.5 rounded-lg px-3 transition-all',
                canSubmit
                  ? 'cursor-pointer bg-primary text-primary-foreground shadow-sm hover:opacity-90'
                  : 'cursor-not-allowed bg-muted text-muted-foreground/40',
              )
            "
            :disabled="!canSubmit"
            @click="submit"
          >
            <Loader2 v-if="generating" class="size-3.5 animate-spin" />
            <ArrowUp v-else class="size-3.5" />
            <span class="text-xs font-medium">
              {{ submitLabel }}
            </span>
          </button>
        </div>
      </div>
    </div>

    <!-- §5.7–§5.9 最近学习折叠区 -->
    <RecentSection @open-classroom="openClassroom" @toast="toast" />

    <!-- 页脚 -->
    <div class="mt-auto pt-12 pb-4 text-center text-xs text-muted-foreground/40">
      {{ t('home.footer') }}
    </div>

    <!-- §5.10 设置弹窗 -->
    <SettingsDialog v-model:open="settingsOpen" v-model:section="settingsSection" />
  </div>
</template>
