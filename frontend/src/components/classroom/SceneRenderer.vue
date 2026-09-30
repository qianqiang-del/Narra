<script setup lang="ts">
/**
 * SceneRenderer —— 文档 §6.5 的场景分发。
 *
 * 原项目由已删除的 `@openmaic/renderer` 包渲染真实课件，
 * 这里按场景类型自绘等效版式（slide / quiz）。
 *
 * `interactive` 例外：它的正文是模型生成的完整 HTML，交给沙箱 iframe 渲染；
 * 只有旧课堂（正文还是控件配置）才回落到底部的兼容渲染器。
 */
import { computed, reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import { Check, Info } from 'lucide-vue-next'

import { interactiveHTML, type Scene } from '@/types/scene'
import { cn } from '@/lib/utils'
import InteractiveIframeRenderer from './InteractiveIframeRenderer.vue'
import InteractiveRenderer from './InteractiveRenderer.vue'

const props = defineProps<{ scene: Scene; activeContentKey?: string | null }>()

const { t } = useI18n()

/** 交互页的沙箱 HTML；旧课堂只写了控件配置，这里为空串。 */
const sandboxHTML = computed(() => interactiveHTML(props.scene))

/** 旧课堂的交互块带控件配置，交给兼容渲染器。 */
const hasControls = computed(() =>
  (props.scene.blocks ?? []).some((block) => (block.interaction?.controls?.length ?? 0) > 0),
)

/** 这一页的题目；每个 `type` 为 quiz 的内容块对应一道。 */
const questions = computed(() => props.scene.quiz?.questions ?? [])

/** 每道题的作答状态，按题目 key 存；切页时组件按 scene.id 重建，状态自然清空。 */
const quizAnswers = reactive<Record<string, { picked: string; revealed: boolean }>>({})

function pickedOf(key: string): string {
  return quizAnswers[key]?.picked ?? ''
}

function revealedOf(key: string): boolean {
  return quizAnswers[key]?.revealed ?? false
}

function pickQuiz(key: string, option: string) {
  quizAnswers[key] = { picked: option, revealed: true }
}

/* ---------- 讲解页的排版 ---------- */

/** 要点卡片里的一行，`label` 为空时按普通条目排版。 */
interface SlideRow { key: string; label: string; text: string }

/** 讲解页正文的一个渲染单元；相邻的 list-item 合成一张要点卡片。 */
interface SlideUnit {
  id: string
  type: 'heading' | 'paragraph' | 'code' | 'callout' | 'list' | 'takeaway'
  /** 小节标题、段落与代码的原文 */
  text: string
  /** `callout` 按「标签：正文」拆出的标签，没有标签时为空串 */
  label: string
  /** `callout` 拆掉标签后的正文 */
  rest: string
  /** 卡片里是否有带标签的行；没有就整张卡片退回普通条目排版 */
  hasLabel: boolean
  rows: SlideRow[]
}

/** 把「标签：正文」拆成两列，让要点排得像表格；标签不像短语时原样返回。 */
function splitLabel(text: string): { label: string; rest: string } {
  const matched = /^([^：:。！？；，,;]{1,20})[：:]\s*(\S[\s\S]*)$/.exec(text.trim())
  return matched ? { label: matched[1], rest: matched[2] } : { label: '', rest: text }
}

const slideUnits = computed<SlideUnit[]>(() => {
  const units: SlideUnit[] = []
  ;(props.scene.slide?.blocks ?? []).forEach((block, index) => {
    if (block.type === 'list-item') {
      const { label, rest } = splitLabel(block.text)
      const row: SlideRow = { key: block.key, label, text: rest }
      const previous = units[units.length - 1]
      if (previous?.type === 'list') {
        previous.rows.push(row)
        previous.hasLabel = previous.hasLabel || Boolean(row.label)
      } else {
        units.push({
          id: `list-${index}`,
          type: 'list',
          text: '',
          label: '',
          rest: '',
          hasLabel: Boolean(row.label),
          rows: [row],
        })
      }
      return
    }
    const type: SlideUnit['type'] =
      block.type === 'heading' || block.type === 'code' || block.type === 'callout' ? block.type : 'paragraph'
    units.push({ id: block.key || `block-${index}`, type, text: block.text, ...splitLabel(block.text), hasLabel: false, rows: [] })
  })
  // 收尾结论永远排在正文最后一块，跟着内容一起滚动。
  const takeaway = props.scene.slide?.takeaway ?? ''
  if (takeaway) {
    units.push({
      id: props.scene.slide?.takeawayKey || 'takeaway',
      type: 'takeaway',
      text: takeaway,
      label: '',
      rest: takeaway,
      hasLabel: false,
      rows: [],
    })
  }
  return units
})
</script>

<template>
  <!-- slide：一页课件，正文块按类型分别排版 -->
  <div
    v-if="scene.type === 'slide' && scene.slide"
    class="flex size-full flex-col justify-center overflow-hidden bg-white px-10 md:px-16 dark:bg-slate-900"
  >
    <!-- 标题区 -->
    <header class="shrink-0 px-0 pt-0 pb-4">
      <h1
        class="text-3xl leading-tight font-bold tracking-tight text-gray-800 md:text-4xl dark:text-gray-100"
      >
        {{ scene.title }}
      </h1>
      <p
        v-if="scene.slide.lead"
        class="mt-2 text-[14px] leading-6 text-slate-500 md:text-[15px] dark:text-slate-400"
      >
        {{ scene.slide.lead }}
      </p>
      <div class="mt-4 h-px bg-gray-200 dark:bg-gray-700" />
    </header>

    <!-- 正文：内容不满一屏时垂直居中，超出时从顶部开始滚动 -->
    <div class="slide-body min-h-0 max-h-[70%] overflow-y-auto px-0">
      <template v-for="unit in slideUnits" :key="unit.id">
        <!-- 小节标题 -->
        <h2
          v-if="unit.type === 'heading'"
          class="flex items-center gap-2.5 pt-1 text-[16px] font-bold text-indigo-900 md:text-[17px] dark:text-indigo-200"
        >
          <span class="h-4 w-1 shrink-0 rounded-full bg-indigo-500" />
          <span>{{ unit.text }}</span>
        </h2>

        <!-- 代码 -->
        <div
          v-else-if="unit.type === 'code'"
          class="overflow-hidden rounded-xl bg-slate-900 ring-1 ring-slate-700/60 dark:bg-slate-950"
        >
          <div class="flex items-center gap-2 border-b border-slate-800/60 px-4 py-2">
            <span class="size-1.5 rounded-full bg-emerald-400" />
            <span class="text-[11px] font-medium tracking-wide text-slate-300">
              {{ t('scene.slideCode') }}
            </span>
          </div>
          <pre class="overflow-x-auto px-4 py-3 font-mono text-[13px] leading-6 text-slate-100 md:text-[14px]">{{ unit.text }}</pre>
        </div>

        <!-- 强调：结论、规律、注意事项 -->
        <div
          v-else-if="unit.type === 'callout'"
          class="flex items-start gap-3 rounded-xl border-l-[3px] border-sky-400 bg-sky-50/60 px-5 py-3.5 dark:bg-sky-950/30"
        >
          <Info class="mt-0.5 size-4 shrink-0 text-sky-500" />
          <p class="text-[14px] leading-[1.8] text-slate-700 md:text-[15px] dark:text-slate-200">
            <span v-if="unit.label" class="font-bold text-sky-900 dark:text-sky-200">{{ unit.label }}：</span>{{ unit.rest }}
          </p>
        </div>

        <!-- 并列要点：一张卡片逐行分隔，带标签的行分两列对齐 -->
        <div
          v-else-if="unit.type === 'list'"
          class="space-y-3"
        >
          <div
            v-for="(row, rowIndex) in unit.rows"
            :key="row.key || rowIndex"
            :class="
              cn(
                'flex items-start gap-3 px-0',
              )
            "
          >
            <span class="mt-2 size-1.5 shrink-0 rounded-full bg-violet-500" />
            <span class="text-[15px] leading-relaxed text-gray-600 dark:text-gray-300"><strong v-if="row.label" class="font-semibold text-gray-800 dark:text-gray-100">{{ row.label }}：</strong>{{ row.text }}</span>
          </div>
        </div>

        <!-- 收尾结论：正文的最后一块 -->
        <div
          v-else-if="unit.type === 'takeaway'"
          class="flex items-start gap-3 rounded-xl border border-emerald-100 bg-emerald-50/60 px-5 py-3.5 dark:border-emerald-900/60 dark:bg-emerald-950/40"
        >
          <Check class="mt-0.5 size-4 shrink-0 text-emerald-500" />
          <div class="min-w-0">
            <p class="text-[11px] font-bold tracking-wider text-emerald-500 uppercase">{{ t('scene.slideTakeaway') }}</p>
            <p class="mt-0.5 text-[14px] leading-6 text-emerald-900 dark:text-emerald-100">{{ unit.text }}</p>
          </div>
        </div>

        <!-- 正文段落 -->
        <p v-else class="text-[14px] leading-[1.85] text-slate-600 md:text-[15px] dark:text-slate-300">
          {{ unit.text }}
        </p>
      </template>
    </div>
  </div>

  <!-- quiz：一页可以有多道选择题，每道题各自作答与揭晓 -->
  <div
    v-else-if="scene.type === 'quiz' && questions.length"
    class="flex size-full flex-col gap-4 overflow-y-auto px-8 py-7 md:px-12"
  >
    <div class="flex shrink-0 items-center gap-2">
      <span class="inline-flex w-fit items-center gap-1.5 rounded-full bg-amber-100 px-3 py-1 text-[11px] font-bold tracking-wide text-amber-700 uppercase dark:bg-amber-900/30 dark:text-amber-300">
        {{ t('scene.typeQuiz') }}
      </span>
      <span class="text-xs text-gray-400 tabular-nums dark:text-gray-500">{{ questions.length }}</span>
    </div>

    <section
      v-for="(item, qi) in questions"
      :key="item.key"
      :class="
        cn(
          'shrink-0 rounded-2xl border bg-white/75 p-5 transition-all duration-300 dark:bg-gray-800/60',
          props.activeContentKey === item.key
            ? 'border-violet-300 ring-2 ring-violet-200/70 dark:border-violet-700 dark:ring-violet-900/50'
            : 'border-gray-200 dark:border-gray-700',
        )
      "
    >
      <div class="flex items-start gap-3">
        <span class="mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-full bg-amber-100 text-xs font-bold text-amber-700 dark:bg-amber-900/40 dark:text-amber-300">
          {{ qi + 1 }}
        </span>
        <h2 class="text-lg font-bold tracking-tight text-gray-800 md:text-xl dark:text-gray-100">
          {{ item.question }}
        </h2>
      </div>

      <div class="mt-4 grid grid-cols-1 gap-2.5 sm:grid-cols-2">
        <button
          v-for="(opt, oi) in item.options"
          :key="oi"
          type="button"
          :class="
            cn(
              'flex items-center gap-3 rounded-xl border px-4 py-2.5 text-left text-sm transition-all',
              revealedOf(item.key) && opt === item.answer
                ? 'border-emerald-400 bg-emerald-50 text-emerald-800 dark:bg-emerald-900/25 dark:text-emerald-200'
                : revealedOf(item.key) && pickedOf(item.key) === opt
                  ? 'border-red-300 bg-red-50 text-red-700 dark:bg-red-900/25 dark:text-red-200'
                  : 'border-gray-200 bg-white text-gray-700 hover:border-violet-300 hover:bg-violet-50/50 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-200 dark:hover:border-violet-600',
            )
          "
          @click="pickQuiz(item.key, opt)"
        >
          <span
            class="flex size-6 shrink-0 items-center justify-center rounded-md border text-xs font-bold"
            :class="
              revealedOf(item.key) && opt === item.answer
                ? 'border-emerald-400 bg-emerald-400 text-white'
                : 'border-gray-300 text-gray-500 dark:border-gray-600'
            "
          >
            <Check v-if="revealedOf(item.key) && opt === item.answer" class="size-3.5" />
            <template v-else>{{ String.fromCharCode(65 + oi) }}</template>
          </span>
          <span class="min-w-0">{{ opt }}</span>
        </button>
      </div>

      <p
        v-if="revealedOf(item.key) && item.explanation"
        class="mt-3 rounded-xl bg-violet-50 px-4 py-2.5 text-[13px] leading-6 text-violet-800 dark:bg-violet-950/30 dark:text-violet-200"
      >
        {{ item.explanation }}
      </p>
    </section>
  </div>

  <!-- interactive：正文是完整 HTML，走沙箱 iframe -->
  <InteractiveIframeRenderer
    v-else-if="scene.type === 'interactive' && sandboxHTML"
    :html="sandboxHTML"
    :interactive="true"
  />

  <!-- 旧课堂的交互页：正文还写在控件配置里 -->
  <InteractiveRenderer v-else-if="scene.type === 'interactive' && hasControls" :scene="scene" />

  <div
    v-else-if="scene.type === 'interactive'"
    class="flex size-full items-center justify-center bg-gray-50 text-xs text-gray-400 dark:bg-gray-800 dark:text-gray-500"
  >
    {{ t('scene.interactiveUnavailable') }}
  </div>

  <div v-else-if="scene.blocks?.length" class="flex size-full flex-col gap-4 overflow-y-auto p-8 md:p-12">
    <h2 class="text-3xl font-bold text-gray-800 dark:text-gray-100">{{ scene.title }}</h2>
    <div v-for="(block, index) in scene.blocks" :key="index" class="text-[15px] leading-relaxed text-gray-700 dark:text-gray-200">
      <h3 v-if="block.type === 'heading'" class="text-xl font-semibold">{{ block.content || block.text }}</h3>
      <div v-else-if="block.type === 'callout'" class="rounded-xl border border-violet-200 bg-violet-50 p-4 text-violet-900 dark:border-violet-800 dark:bg-violet-950/30 dark:text-violet-100">{{ block.content || block.text }}</div>
      <li v-else-if="block.type === 'list-item'" class="ml-5 list-disc">{{ block.content || block.text }}</li>
      <p v-else>{{ block.content || block.text }}</p>
    </div>
  </div>

  <!-- 兜底 -->
  <div v-else class="flex size-full items-center justify-center text-sm text-gray-400">
    {{ t('scene.statusGenerating') }}
  </div>
</template>

<style scoped>
/*
 * 讲解页正文的纵向排布：内容不满一屏时居中，撑满时靠上并出现滚动条。
 * `safe` 关键字让居中对齐在内容超出容器时退回起点，否则第一行会被推到滚动区之上。
 */
.slide-body {
  display: flex;
  flex-direction: column;
  justify-content: safe center;
  gap: 0.75rem;
  padding-block: 0.5rem 1rem;
  scrollbar-color: rgb(148 163 184 / 0.5) transparent;
  scrollbar-width: thin;
}

/*
 * 子项一律不许收缩。flex 子项默认 flex-shrink: 1，内容超出时会被压扁，
 * 而卡片自身是 overflow-hidden —— 结果内容被裁掉、容器却不产生滚动。
 */
.slide-body > * {
  flex-shrink: 0;
}

.slide-body::-webkit-scrollbar {
  height: 8px;
  width: 8px;
}

.slide-body::-webkit-scrollbar-track {
  background: transparent;
}

.slide-body::-webkit-scrollbar-thumb {
  background-color: rgb(148 163 184 / 0.45);
  border-radius: 999px;
}

.slide-body::-webkit-scrollbar-thumb:hover {
  background-color: rgb(148 163 184 / 0.7);
}
</style>
