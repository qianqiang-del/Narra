<script setup lang="ts">
/**
 * 首页 / 落地页 —— 对应旧 app/page.tsx（1896 行）。
 * 完整结构见 docs/UI-还原文档.md §5。
 *
 * 组装顺序：根容器 → 背景光斑 → 胶囊工具栏 → Hero(logo/slogan)
 *          → Composer（GreetingBar + textarea + AgentBar + 工具栏 + 发送）
 *          → 最近学习折叠区 → 页脚。
 */
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { ArrowUp, Atom, Check, Loader2, Mic } from 'lucide-vue-next'
import { toast } from 'vue-sonner'

import AgentBar from '@/components/home/AgentBar.vue'
import GenerationToolbar from '@/components/home/GenerationToolbar.vue'
import GreetingBar from '@/components/home/GreetingBar.vue'
import ProBadge from '@/components/home/ProBadge.vue'
import RecentSection from '@/components/home/RecentSection.vue'
import SettingsDialog from '@/components/home/SettingsDialog.vue'
import TopPillToolbar from '@/components/home/TopPillToolbar.vue'
import UiTooltip from '@/components/ui/UiTooltip.vue'
import { cn } from '@/lib/utils'

const { t } = useI18n()
const router = useRouter()

const settingsOpen = ref(false)
const requirement = ref('')
const textareaRef = ref<HTMLTextAreaElement | null>(null)
const generating = ref(false)

/** 深度交互模式 */
const interactiveMode = ref(false)

/** 生成配置（透传给 GenerationToolbar / AgentBar） */
const providerId = ref('openai')
const modelId = ref('gpt-4o-mini')
const hasProvider = ref(true)
const webSearch = ref(false)
const searchEngine = ref('tavily')
const extractor = ref('mineru')
const materials = ref<{ id: string; name: string; size: number }[]>([])

const agentMode = ref<'preset' | 'auto'>('preset')
const selectedRoleIds = ref<string[]>(['assist', 'curious'])
const ttsEnabled = ref(true)
const teacherVoice = ref('voxcpm-zh-female-warm')

const canSubmit = computed(() => requirement.value.trim().length > 0 && !generating.value)

/** textarea 自增高（140~300px） */
function autoGrow() {
  const el = textareaRef.value
  if (!el) return
  el.style.height = 'auto'
  el.style.height = `${Math.min(Math.max(el.scrollHeight, 140), 300)}px`
}
watch(requirement, () => nextTick(autoGrow))

async function submit() {
  if (!canSubmit.value) return
  generating.value = true
  // 生成本身由课堂页承载，这里先落到预览页/课堂页
  const id = `c-${Date.now()}`
  await router.push({ name: 'generation-preview' })
  generating.value = false
  void id
}

function onKeydown(e: KeyboardEvent) {
  if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
    e.preventDefault()
    submit()
  }
}

function openClassroom(id: string) {
  router.push({ name: 'classroom', params: { id } })
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
        class="absolute right-1/4 bottom-0 h-96 w-96 animate-pulse rounded-full bg-purple-500/10 blur-3xl"
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
        class="w-full rounded-2xl border border-border/60 bg-white/80 shadow-xl shadow-black/[0.03] backdrop-blur-xl transition-shadow focus-within:shadow-2xl focus-within:shadow-violet-500/[0.06] dark:bg-slate-900/80 dark:shadow-black/20"
      >
        <!-- 顶部行：GreetingBar（左） / AgentBar（右） -->
        <div class="relative z-20 flex items-start justify-between">
          <GreetingBar />
          <div class="shrink-0 pt-3.5 pr-3">
            <AgentBar
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
              v-model:search-engine="searchEngine"
              v-model:extractor="extractor"
              v-model:materials="materials"
              :has-provider="hasProvider"
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
              {{ generating ? t('home.generating') : t('home.enterClassroom') }}
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
    <SettingsDialog v-model:open="settingsOpen" />
  </div>
</template>
