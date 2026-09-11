<script setup lang="ts">
/**
 * WorkspaceChatPane —— 中间对话栏。
 *
 * 修改课件的唯一入口（按产品决策：不做课件区右下角的元素级编辑）。
 * 头部：对话标题 + 折叠按钮；中部：消息流（用户气泡 / AI 文本 /
 * 课程卡片）；底部：composer（输入框 + @ 引用 + 发送）。
 */
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  ArrowUp,
  AtSign,
  BookOpen,
  FileText,
  PanelLeftClose,
  Paperclip,
  Sparkles,
  X,
} from 'lucide-vue-next'

import type { ChatMessage } from '@/data/workspace'
import { cn } from '@/lib/utils'

const props = defineProps<{
  /** null = 空态（尚未选择对话） */
  title: string | null
  messages: ChatMessage[]
  /** 折叠按钮只有存在其他面板时才给 */
  collapsible?: boolean
}>()

const emit = defineEmits<{
  (e: 'collapse'): void
  (e: 'open-course', courseId: string): void
  (e: 'send', text: string): void
}>()

const { t } = useI18n()

const draft = ref('')
const scrollRef = ref<HTMLDivElement | null>(null)

/* ── 课程材料上传（与主页 GenerationToolbar 同款） ── */

interface MaterialItem {
  id: string
  name: string
  size: number
}

const materials = ref<MaterialItem[]>([])
const extractor = ref('mineru')
const materialOpen = ref(false)
const materialDragging = ref(false)
const composerRef = ref<HTMLDivElement | null>(null)

const EXTRACTORS = [
  { id: 'mineru', name: 'MinerU' },
  { id: 'unpdf', name: 'unpdf' },
]

function addFiles(files: File[]) {
  for (const f of files) {
    materials.value = [
      ...materials.value,
      { id: `${f.name}-${f.size}-${Date.now()}`, name: f.name, size: f.size },
    ]
  }
}

function onFilePick(e: Event) {
  const input = e.target as HTMLInputElement
  addFiles(Array.from(input.files ?? []))
  input.value = ''
}

function onDrop(e: DragEvent) {
  materialDragging.value = false
  addFiles(Array.from(e.dataTransfer?.files ?? []))
}

function removeMaterial(id: string) {
  materials.value = materials.value.filter((m) => m.id !== id)
}

function onDocMouseDown(e: MouseEvent) {
  if (composerRef.value && !composerRef.value.contains(e.target as Node)) {
    materialOpen.value = false
  }
}

onMounted(() => document.addEventListener('mousedown', onDocMouseDown))
onBeforeUnmount(() => document.removeEventListener('mousedown', onDocMouseDown))

function send() {
  const text = draft.value.trim()
  if (!text) return
  emit('send', text)
  draft.value = ''
}

watch(
  () => props.messages.length,
  async () => {
    await nextTick()
    scrollRef.value?.scrollTo({ top: scrollRef.value.scrollHeight })
  },
)
</script>

<template>
  <section class="flex h-full min-w-0 flex-col bg-background" data-testid="workspace-chat-pane">
    <!-- 头部 -->
    <header class="flex h-12 shrink-0 items-center gap-2 border-b border-border px-4">
      <p class="min-w-0 flex-1 truncate text-[13px] font-semibold">
        {{ title ?? t('workspace.newSession') }}
      </p>
      <button
        v-if="collapsible"
        type="button"
        :title="t('workspace.collapseChat')"
        class="rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
        @click="emit('collapse')"
      >
        <PanelLeftClose class="size-4" />
      </button>
    </header>

    <!-- 消息流 -->
    <div ref="scrollRef" class="min-h-0 flex-1 overflow-y-auto">
      <!-- 空态 -->
      <div
        v-if="title === null"
        class="flex h-full flex-col items-center justify-center gap-2 px-8 text-center"
      >
        <Sparkles class="size-5 text-violet-400" />
        <p class="text-sm font-medium text-foreground/80">{{ t('workspace.chatEmptyTitle') }}</p>
        <p class="text-xs leading-5 text-muted-foreground">
          {{ t('workspace.chatEmptyDesc') }}
        </p>
      </div>

      <div v-else class="flex flex-col gap-4 px-4 py-4">
        <template v-for="m in messages" :key="m.id">
          <!-- 用户消息 -->
          <div v-if="m.role === 'user'" class="flex justify-end">
            <p
              class="max-w-[85%] rounded-2xl rounded-br-md bg-violet-600 px-3.5 py-2.5 text-[13px] leading-5.5 whitespace-pre-wrap text-white"
            >
              {{ m.text }}
            </p>
          </div>

          <!-- AI 消息 -->
          <div v-else class="flex flex-col gap-2">
            <p class="text-[13px] leading-6 whitespace-pre-wrap text-foreground/90">{{ m.text }}</p>
            <!-- 课程卡片 -->
            <button
              v-if="m.courseCard"
              type="button"
              class="group flex w-full items-center gap-3 rounded-xl border border-violet-200 bg-violet-50/60 px-3.5 py-3 text-left transition-colors hover:border-violet-300 hover:bg-violet-50 dark:border-violet-800 dark:bg-violet-950/30 dark:hover:border-violet-700"
              @click="emit('open-course', m.courseCard!.courseId)"
            >
              <span
                class="flex size-9 shrink-0 items-center justify-center rounded-lg bg-violet-600/10 text-violet-600"
              >
                <BookOpen class="size-4.5" />
              </span>
              <span class="min-w-0 flex-1">
                <span class="block truncate text-[13px] font-semibold text-foreground">
                  {{ m.courseCard.title }}
                </span>
                <span class="block text-[11px] text-muted-foreground">
                  {{ t('workspace.sceneCount', { count: m.courseCard.pageCount }) }}
                </span>
              </span>
              <span
                class="shrink-0 text-[11px] font-medium text-violet-600 opacity-0 transition-opacity group-hover:opacity-100"
              >
                {{ t('workspace.openCourse') }} →
              </span>
            </button>
          </div>
        </template>
      </div>
    </div>

    <!-- Composer -->
    <div class="shrink-0 p-3">
      <div
        ref="composerRef"
        :class="
          cn(
            'relative rounded-xl border border-border bg-background shadow-sm transition-colors',
            'focus-within:border-violet-400 focus-within:ring-2 focus-within:ring-violet-500/15',
          )
        "
      >
        <textarea
          v-model="draft"
          rows="2"
          :placeholder="t('workspace.composerPlaceholder')"
          class="max-h-32 min-h-11 w-full resize-none bg-transparent px-3.5 pt-3 text-[13px] leading-5 outline-none placeholder:text-muted-foreground/70"
          @keydown.enter.exact.prevent="send"
        />
        <div class="flex items-center gap-1 px-2.5 pb-2">
          <!-- 课程材料（与主页 GenerationToolbar 同款弹层） -->
          <button
            type="button"
            :title="t('workspace.attachMaterials')"
            :class="
              cn(
                'relative rounded-md p-1.5 transition-colors',
                materials.length || materialOpen
                  ? 'bg-violet-500/10 text-violet-600 dark:text-violet-300'
                  : 'text-muted-foreground hover:bg-accent hover:text-foreground',
              )
            "
            @click="materialOpen = !materialOpen"
          >
            <Paperclip class="size-4" />
            <span
              v-if="materials.length"
              class="absolute -top-1 -right-1 flex h-3.5 min-w-3.5 items-center justify-center rounded-full bg-violet-600 px-0.5 text-[9px] font-bold text-white"
            >
              {{ materials.length }}
            </span>
          </button>
          <button
            type="button"
            :title="t('workspace.mentionCourse')"
            class="rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
          >
            <AtSign class="size-4" />
          </button>
          <div class="flex-1" />
          <button
            type="button"
            :disabled="!draft.trim()"
            :title="t('workspace.send')"
            class="flex size-7 items-center justify-center rounded-full bg-violet-600 text-white transition-all hover:bg-violet-700 disabled:opacity-30"
            @click="send"
          >
            <ArrowUp class="size-4" />
          </button>
        </div>

        <!-- 材料弹层 -->
        <div
          v-if="materialOpen"
          class="absolute bottom-full left-0 z-50 mb-2 w-72 rounded-xl border border-border bg-popover p-3 shadow-lg"
        >
          <div class="mb-2 flex items-center justify-between gap-2">
            <span class="text-xs font-medium text-muted-foreground/70">{{
              t('toolbar.documentExtractor')
            }}</span>
            <select
              v-model="extractor"
              class="h-7 rounded-md border border-input bg-transparent px-1.5 text-xs outline-none"
            >
              <option v-for="x in EXTRACTORS" :key="x.id" :value="x.id">{{ x.name }}</option>
            </select>
          </div>

          <label
            :class="
              cn(
                'flex cursor-pointer flex-col items-center justify-center gap-1.5 rounded-lg border-2 border-dashed p-4 transition-colors',
                materialDragging
                  ? 'border-violet-400 bg-violet-50 dark:bg-violet-900/20'
                  : 'border-muted-foreground/20 hover:border-violet-300',
              )
            "
            @dragover.prevent="materialDragging = true"
            @dragleave.prevent="materialDragging = false"
            @drop.prevent="onDrop"
          >
            <Paperclip class="size-4 text-muted-foreground/50" />
            <span class="text-center text-[11px] text-muted-foreground/60">
              {{ t('toolbar.materialLimit') }}
            </span>
            <input
              type="file"
              multiple
              class="hidden"
              accept=".pdf,.doc,.docx,.ppt,.pptx,.xls,.xlsx,.txt,.md,.markdown,image/*"
              @change="onFilePick"
            />
          </label>

          <div v-if="materials.length" class="mt-2 max-h-40 space-y-1.5 overflow-y-auto">
            <div
              v-for="m in materials"
              :key="m.id"
              class="flex items-center gap-2 rounded-lg border border-border/50 px-2 py-2"
            >
              <div
                class="flex size-8 shrink-0 items-center justify-center rounded-lg bg-violet-100 dark:bg-violet-900/30"
              >
                <FileText class="size-3.5 text-violet-600 dark:text-violet-300" />
              </div>
              <span class="min-w-0 flex-1 truncate text-xs">{{ m.name }}</span>
              <button
                type="button"
                class="shrink-0 rounded p-1 text-muted-foreground/50 hover:bg-muted hover:text-foreground"
                @click="removeMaterial(m.id)"
              >
                <X class="size-3.5" />
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  </section>
</template>
