<script setup lang="ts">
/**
 * SceneRenderer —— 文档 §6.5 的场景分发。
 *
 * 原项目由已删除的 `@openmaic/renderer` 包渲染真实课件，
 * 这里按场景类型自绘等效版式（slide / quiz / interactive / pbl）。
 */
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Check, Cpu, MousePointer2, RotateCw } from 'lucide-vue-next'

import type { Scene } from '@/data/scenes'
import { cn } from '@/lib/utils'

const props = defineProps<{ scene: Scene }>()

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
  <div v-if="scene.type === 'slide' && scene.slide" class="flex size-full flex-col justify-center px-10 md:px-16">
    <div class="flex items-start justify-between gap-8">
      <div class="min-w-0 flex-1">
        <h2 class="text-3xl font-bold tracking-tight text-gray-800 md:text-4xl dark:text-gray-100">
          {{ scene.slide.heading }}
        </h2>
        <ul class="mt-6 space-y-3">
          <li
            v-for="(b, i) in scene.slide.bullets"
            :key="i"
            class="flex items-start gap-3 text-[15px] leading-relaxed text-gray-600 dark:text-gray-300"
          >
            <span
              class="mt-1.5 size-1.5 shrink-0 rounded-full bg-violet-500"
            />
            {{ b }}
          </li>
        </ul>
      </div>

      <div
        v-if="scene.slide.accent"
        class="hidden shrink-0 rounded-2xl bg-gradient-to-br from-violet-100 to-blue-100 px-6 py-5 text-center md:block dark:from-violet-900/30 dark:to-blue-900/30"
      >
        <span class="font-mono text-sm font-semibold text-violet-700 dark:text-violet-300">
          {{ scene.slide.accent }}
        </span>
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
  <div v-else-if="scene.type === 'interactive' && scene.interactive" class="flex size-full flex-col">
    <div class="flex shrink-0 items-center gap-2 border-b border-gray-200 bg-gray-100 px-3 py-2 dark:border-gray-700 dark:bg-gray-800">
      <span class="size-2.5 rounded-full bg-red-400" />
      <span class="size-2.5 rounded-full bg-amber-400" />
      <span class="size-2.5 rounded-full bg-emerald-400" />
      <div class="ml-2 flex flex-1 items-center gap-1.5 rounded-md bg-white px-2.5 py-1 dark:bg-gray-900">
        <RotateCw class="size-3 text-gray-400" />
        <span class="truncate font-mono text-[11px] text-gray-500 dark:text-gray-400">
          {{ scene.interactive.url }}
        </span>
      </div>
    </div>
    <div class="flex flex-1 flex-col items-center justify-center gap-3 bg-gradient-to-br from-emerald-50/60 to-teal-50/60 p-8 dark:from-emerald-950/20 dark:to-teal-950/20">
      <div class="flex size-12 items-center justify-center rounded-2xl bg-emerald-100 dark:bg-emerald-900/40">
        <MousePointer2 class="size-6 text-emerald-600 dark:text-emerald-300" />
      </div>
      <h3 class="text-lg font-bold text-gray-800 dark:text-gray-100">{{ scene.interactive.heading }}</h3>
      <p class="max-w-md text-center text-[13px] text-gray-500 dark:text-gray-400">
        {{ scene.interactive.note }}
      </p>
      <div class="mt-2 w-full max-w-md rounded-lg bg-gray-900 px-4 py-3 font-mono text-[12px] text-emerald-300">
        <div>name = "Narra"</div>
        <div>print(f"Hello, &#123;name&#125;!")</div>
        <div class="mt-1 text-gray-500">&gt;&gt; Hello, Narra!</div>
      </div>
    </div>
  </div>

  <!-- pbl：3 列看板 -->
  <div v-else-if="scene.type === 'pbl' && scene.pbl" class="flex size-full flex-col p-8 md:p-10">
    <div class="mb-4 flex items-center gap-2">
      <div class="flex size-7 items-center justify-center rounded-lg bg-blue-100 dark:bg-blue-900/40">
        <Cpu class="size-4 text-blue-600 dark:text-blue-300" />
      </div>
      <h2 class="text-xl font-bold tracking-tight text-gray-800 dark:text-gray-100">
        {{ scene.pbl.heading }}
      </h2>
    </div>

    <div class="grid min-h-0 flex-1 grid-cols-3 gap-4">
      <div
        v-for="(col, ci) in scene.pbl.columns"
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

  <!-- 兜底 -->
  <div v-else class="flex size-full items-center justify-center text-sm text-gray-400">
    {{ t('scene.statusGenerating') }}
  </div>
</template>
