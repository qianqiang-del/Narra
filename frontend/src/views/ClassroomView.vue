<script setup lang="ts">
/**
 * 课堂播放页 —— 文档 §6.0（入口壳）。
 *
 * div.h-screen.flex.flex-col.overflow-hidden
 *   三态：loading → 居中文案；error → 文案 + Retry；ok → PlaybackChrome
 *
 * 数据来自 src/data/scenes.ts 的 mock；接入 Go 后端后换成
 * `GET /api/classrooms/:id` + SSE 流式推送即可。
 */
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { AlertCircle, Loader2 } from 'lucide-vue-next'

import PlaybackChrome from '@/components/classroom/PlaybackChrome.vue'
import { getClassroom, type Classroom } from '@/data/scenes'

const props = defineProps<{ id: string }>()

const { t } = useI18n()
const router = useRouter()

const phase = ref<'loading' | 'error' | 'ok'>('loading')
const classroom = ref<Classroom | null>(null)

function load() {
  phase.value = 'loading'
  // 模拟加载；后端接入后替换为真实请求
  window.setTimeout(() => {
    if (!props.id) {
      phase.value = 'error'
      return
    }
    classroom.value = getClassroom(props.id)
    phase.value = 'ok'
  }, 400)
}

onMounted(load)
</script>

<template>
  <div class="flex h-screen flex-col overflow-hidden">
    <!-- loading -->
    <div
      v-if="phase === 'loading'"
      class="flex flex-1 items-center justify-center bg-gray-50 dark:bg-gray-900"
    >
      <div class="flex flex-col items-center gap-3">
        <Loader2 class="size-6 animate-spin text-violet-500" />
        <p class="text-sm text-muted-foreground">{{ t('classroom.loadingClassroom') }}</p>
      </div>
    </div>

    <!-- error -->
    <div
      v-else-if="phase === 'error'"
      class="flex flex-1 items-center justify-center bg-gray-50 dark:bg-gray-900"
    >
      <div class="flex flex-col items-center text-center">
        <AlertCircle class="size-8 text-destructive" />
        <p class="mt-3 mb-4 text-sm font-medium text-destructive">{{ t('classroom.notFound') }}</p>
        <p class="mb-4 text-[13px] text-muted-foreground">{{ t('classroom.notFoundDesc') }}</p>
        <div class="flex gap-2">
          <button
            type="button"
            class="rounded-md bg-primary px-4 py-2 text-sm text-primary-foreground transition-colors hover:bg-primary/90"
            @click="load"
          >
            {{ t('common.retry') }}
          </button>
          <button
            type="button"
            class="rounded-md border border-border px-4 py-2 text-sm transition-colors hover:bg-muted"
            @click="router.push({ name: 'home' })"
          >
            {{ t('common.backToHome') }}
          </button>
        </div>
      </div>
    </div>

    <!-- ok -->
    <PlaybackChrome v-else-if="classroom" :classroom="classroom" />
  </div>
</template>
