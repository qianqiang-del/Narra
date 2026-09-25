<script setup lang="ts">
/**
 * SceneRenderer —— 文档 §6.5 的场景分发。
 *
 * 原项目由已删除的 `@openmaic/renderer` 包渲染真实课件，
 * 这里按场景类型自绘等效版式（slide / quiz / interactive / pbl）。
 */
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Check, Cpu } from 'lucide-vue-next'

import type { Scene } from '@/types/scene'
import { cn } from '@/lib/utils'
import InteractiveRenderer from './InteractiveRenderer.vue'

const props = defineProps<{ scene: Scene; activeContentKey?: string | null }>()

const { t } = useI18n()

const picked = ref<number | null>(null)
const revealed = ref(false)

function pickQuiz(i: number) {
  picked.value = i
  revealed.value = true
}
</script>

<template>
  <!-- slide -->
  <div v-if="scene.type === 'slide' && scene.slide" class="relative flex size-full flex-col overflow-hidden bg-gradient-to-br from-violet-50 via-white to-blue-50 px-8 py-8 md:px-14 md:py-10 dark:from-violet-950/40 dark:via-gray-900 dark:to-blue-950/40">
    <div class="pointer-events-none absolute -top-24 -right-20 size-64 rounded-full bg-violet-300/20 blur-3xl dark:bg-violet-500/10" />
    <div class="pointer-events-none absolute -bottom-24 -left-16 size-56 rounded-full bg-blue-300/20 blur-3xl dark:bg-blue-500/10" />
    <div class="relative flex min-h-0 flex-1 flex-col">
      <div class="mb-6 flex items-start justify-between gap-8">
        <div class="min-w-0 flex-1">
          <span class="mb-3 inline-flex rounded-full bg-violet-100 px-3 py-1 text-[10px] font-bold tracking-wider text-violet-700 uppercase dark:bg-violet-900/40 dark:text-violet-300">课堂重点</span>
          <h2 class="text-3xl font-bold tracking-tight text-gray-900 md:text-4xl dark:text-gray-100">
          {{ scene.slide.heading }}
          </h2>
        </div>
      </div>
      <ul class="grid min-h-0 flex-1 content-start gap-3 overflow-y-auto pr-1 md:grid-cols-2">
          <li
            v-for="(b, i) in scene.slide.bullets"
            :key="i"
            :class="props.activeContentKey && scene.slide.bulletKeys?.[i] === props.activeContentKey ? 'active-lesson-card border-white/80 bg-white/75 dark:border-gray-700/60 dark:bg-gray-800/60' : 'border-white/80 bg-white/75 dark:border-gray-700/60 dark:bg-gray-800/60'"
          >
            <span
              :class="props.activeContentKey && scene.slide.bulletKeys?.[i] === props.activeContentKey ? 'active-lesson-dot mt-2 size-3 shrink-0 rounded-full bg-violet-500' : 'mt-2 size-2 shrink-0 rounded-full bg-violet-300'"
            />
            <span :class="props.activeContentKey && scene.slide.bulletKeys?.[i] === props.activeContentKey ? 'active-lesson-text min-w-0 font-medium text-violet-700 dark:text-violet-300' : 'min-w-0'">{{ b }}</span>
          </li>
        </ul>
      <div
        v-if="scene.slide.accent"
        class="mt-5 rounded-2xl border border-violet-200/70 bg-violet-100/70 px-5 py-3 text-sm font-medium text-violet-800 dark:border-violet-800/60 dark:bg-violet-900/30 dark:text-violet-200"
      >
        {{ scene.slide.accent }}
      </div>
    </div>
  </div>

  <!-- quiz -->
  <div v-else-if="scene.type === 'quiz' && scene.quiz" class="flex size-full flex-col justify-center px-10 md:px-16">
    <div class="mb-2 inline-flex w-fit items-center gap-1.5 rounded-full bg-amber-100 px-3 py-1 text-[11px] font-bold tracking-wide text-amber-700 uppercase dark:bg-amber-900/30 dark:text-amber-300">
      {{ t('scene.typeQuiz') }}
    </div>
    <h2 class="text-2xl font-bold tracking-tight text-gray-800 md:text-3xl dark:text-gray-100">
      {{ scene.quiz.question }}
    </h2>

    <div class="mt-6 grid grid-cols-1 gap-3 sm:grid-cols-2">
      <button
        v-for="(opt, i) in scene.quiz.options"
        :key="i"
        type="button"
        :class="
          cn(
            'flex items-center gap-3 rounded-xl border px-4 py-3 text-left text-[15px] transition-all',
            revealed && i === scene.quiz.answer
              ? 'border-emerald-400 bg-emerald-50 text-emerald-800 dark:bg-emerald-900/25 dark:text-emerald-200'
              : revealed && i === picked
                ? 'border-red-300 bg-red-50 text-red-700 dark:bg-red-900/25 dark:text-red-200'
                : 'border-gray-200 bg-white text-gray-700 hover:border-violet-300 hover:bg-violet-50/50 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-200 dark:hover:border-violet-600',
          )
        "
        @click="pickQuiz(i)"
      >
        <span
          class="flex size-6 shrink-0 items-center justify-center rounded-md border text-xs font-bold"
          :class="
            revealed && i === scene.quiz.answer
              ? 'border-emerald-400 bg-emerald-400 text-white'
              : 'border-gray-300 text-gray-500 dark:border-gray-600'
          "
        >
          <Check v-if="revealed && i === scene.quiz.answer" class="size-3.5" />
          <template v-else>{{ String.fromCharCode(65 + i) }}</template>
        </span>
        <span class="font-mono">{{ opt }}</span>
      </button>
    </div>
  </div>

  <!-- interactive：伪浏览器 -->
  <InteractiveRenderer v-else-if="scene.type === 'interactive'" :scene="scene" />

  <!-- pbl：3 列看板 -->
  <div v-else-if="scene.type === 'pbl' && scene.pbl" class="flex size-full flex-col p-8 md:p-10">
    <div class="mb-4 flex items-center gap-2">
      <div class="flex size-7 items-center justify-center rounded-lg bg-blue-100 dark:bg-blue-900/40">
        <Cpu class="size-4 text-blue-600 dark:text-blue-300" />
      </div>
      <h2 class="text-xl font-bold tracking-tight text-gray-800 dark:text-gray-100">
        {{ scene.pbl!.heading }}
      </h2>
    </div>

    <div class="grid min-h-0 flex-1 grid-cols-3 gap-4">
      <div
        v-for="(col, ci) in scene.pbl!.columns"
        :key="ci"
        class="flex min-h-0 flex-col rounded-xl border border-blue-100 bg-blue-50/50 p-3 dark:border-blue-900/40 dark:bg-blue-950/20"
      >
        <span class="mb-2 text-[11px] font-bold tracking-wide text-blue-700 uppercase dark:text-blue-300">
          {{ col.title }}
        </span>
        <div class="flex flex-col gap-2 overflow-y-auto">
          <div
            v-for="(item, ii) in col.items"
            :key="ii"
            class="rounded-lg border border-blue-100 bg-white px-3 py-2 text-[13px] text-gray-700 shadow-sm dark:border-blue-900/40 dark:bg-gray-800 dark:text-gray-200"
          >
            {{ item }}
          </div>
        </div>
      </div>
    </div>
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
@keyframes lesson-dot-breathe {
  0%, 100% { transform: scale(0.9); opacity: 0.75; }
  50% { transform: scale(1.15); opacity: 1; }
}

.active-lesson-text { display: inline-block; animation: lesson-text-nudge 1.8s ease-in-out infinite; }
.active-lesson-dot { animation: lesson-dot-breathe 1.6s ease-in-out infinite; }

@keyframes lesson-text-nudge {
  0%, 100% { transform: translateX(0); }
  50% { transform: translateX(3px); }
}
</style>
