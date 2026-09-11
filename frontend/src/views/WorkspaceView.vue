<script setup lang="ts">
/**
 * Pro 工作区 —— 对应旧 app/workspace/page.tsx。
 * 三栏布局（左导航 + 中对话 + 右课堂），完整结构见 docs/UI-还原文档.md §8。
 * 这里先落根容器与占位，后续补 WorkspaceRail / ChatPane / ClassroomPane / Home。
 */
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
</script>

<template>
  <!-- §8.1 根容器：--ws-rail-w 由后续 WorkspaceRail 提供 -->
  <div
    class="ws-root flex h-[100dvh] w-full overflow-hidden"
    style="--ws-rail-w: 260px"
    data-testid="pro-workspace"
  >
    <div class="flex flex-1 items-center justify-center bg-background">
      <p class="text-sm text-muted-foreground">{{ t('workspace.newSession') }} · 待实现</p>
    </div>
  </div>
</template>
