<script setup lang="ts">
/**
 * 生成预览页 —— 对应旧 app/generation-preview/page.tsx（1554 行）。
 * 4 个互斥分支：加载 / 空态 / 大纲审阅 / 主生成流程。
 * 完整结构见 docs/UI-还原文档.md §7（含 6 种 StepVisualizer 与大纲编辑器）。
 */
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
</script>

<template>
  <!-- §7.1 主视图根容器 -->
  <div
    class="narra-page narra-preview relative flex min-h-[100dvh] w-full flex-col items-center justify-center overflow-hidden bg-background p-4 text-center"
  >
    <!-- 背景装饰层 -->
    <div class="pointer-events-none fixed inset-0 z-0 overflow-hidden">
      <div
        class="narra-decorative-orb absolute top-0 left-1/4 h-96 w-96 animate-pulse rounded-full bg-brand-600/10 blur-3xl"
        style="animation-duration: 4s"
      />
      <div
        class="narra-decorative-orb absolute right-1/4 bottom-0 h-96 w-96 animate-pulse rounded-full bg-brand-600/10 blur-3xl"
        style="animation-duration: 6s"
      />
    </div>

    <div class="z-10 w-full max-w-lg space-y-8">
      <div
        class="relative flex min-h-[400px] flex-col items-center justify-center overflow-hidden rounded-xl border border-muted/40 bg-card/80 p-8 shadow-2xl backdrop-blur-xl md:p-12 dark:bg-slate-900/80"
      >
        <h2 class="text-2xl font-bold tracking-tight">{{ t('generation.aiWorking') }}</h2>
        <p class="mt-3 text-base text-muted-foreground">{{ t('generation.sessionNotFoundDesc') }}</p>
      </div>
    </div>
  </div>
</template>
