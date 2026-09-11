<script setup lang="ts">
/**
 * FolderCard —— 文档 §5.9。
 *
 * 16:9 渐变封面 + 最多 3 张扇形堆叠缩略图 + 课程数徽章，
 * hover 可删除/重命名，支持拖拽放置课程。
 */
import { computed, nextTick, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { AlertTriangle, Folder, Pencil, Trash2 } from 'lucide-vue-next'

import type { Classroom, Folder as FolderType } from '@/stores/library'
import { cn } from '@/lib/utils'

const props = defineProps<{
  folder: FolderType
  courses: Classroom[]
}>()

const emit = defineEmits<{
  (e: 'open', id: string): void
  (e: 'rename', id: string, name: string): void
  (e: 'delete-only', id: string): void
  (e: 'delete-with-courses', id: string): void
  (e: 'drop-course', classroomId: string, folderId: string): void
}>()

const { t } = useI18n()

const editing = ref(false)
const draft = ref('')
const deleteOpen = ref(false)
const dragOver = ref(false)
const nameInputRef = ref<HTMLInputElement | null>(null)

const count = computed(() => props.courses.length)

/** 扇形堆叠：最多取 3 张缩略图 */
const stack = computed(() => props.courses.slice(0, 3))

/** 扇形错位的位移/旋转参数（前张 66% 宽，后张 60%） */
const stackStyle = (i: number) => {
  const mid = (stack.value.length - 1) / 2
  const offset = i - mid
  return {
    width: i === stack.value.length - 1 ? '66%' : '60%',
    transform: `translateX(${offset * 8}%) rotate(${offset * 5}deg)`,
    zIndex: i + 1,
    opacity: 0.85,
  }
}

function startRename() {
  draft.value = props.folder.name
  editing.value = true
  nextTick(() => {
    nameInputRef.value?.focus()
    nameInputRef.value?.select()
  })
}

function commitRename() {
  const next = draft.value.trim()
  if (next && next !== props.folder.name) emit('rename', props.folder.id, next.slice(0, 80))
  editing.value = false
}

function onDrop(e: DragEvent) {
  dragOver.value = false
  const id = e.dataTransfer?.getData('text/stage-id')
  if (id) emit('drop-course', id, props.folder.id)
}
</script>

<template>
  <div class="group cursor-pointer">
    <div
      :class="
        cn(
          'relative aspect-[16/9] w-full overflow-hidden rounded-2xl bg-gradient-to-br from-violet-50 to-blue-50 ring-1 transition-transform duration-200 group-hover:scale-[1.02] dark:from-violet-900/20 dark:to-blue-900/20',
          dragOver
            ? 'scale-[1.03] ring-2 ring-violet-500 ring-offset-2 ring-offset-background'
            : 'ring-violet-200/50 dark:ring-violet-800/40',
        )
      "
      @click="!deleteOpen && emit('open', folder.id)"
      @dragover.prevent="dragOver = true"
      @dragleave.prevent="dragOver = false"
      @drop.prevent="onDrop"
    >
      <!-- 封面堆叠 -->
      <div v-if="stack.length" class="flex size-full items-center justify-center">
        <div
          v-for="(c, i) in stack"
          :key="c.id"
          class="absolute aspect-[16/9] overflow-hidden rounded-xl bg-slate-200 shadow-md ring-1 ring-black/5"
          :style="stackStyle(i)"
        >
          <img v-if="c.thumbnail" :src="c.thumbnail" alt="" class="size-full object-cover" />
        </div>
      </div>

      <!-- 空文件夹 -->
      <div v-else class="flex size-full items-center justify-center">
        <div class="flex size-14 items-center justify-center rounded-2xl bg-violet-100 dark:bg-violet-900/40">
          <Folder class="size-7 text-violet-500" />
        </div>
      </div>

      <!-- 课程数徽章 -->
      <span
        class="absolute right-2 bottom-2 z-10 inline-flex items-center rounded-full bg-black/40 px-2 py-0.5 text-[11px] font-medium text-white backdrop-blur-sm"
      >
        {{ t('home.folderCourseCount', { count }) }}
      </span>

      <!-- 拖拽悬停遮罩 -->
      <div
        v-if="dragOver"
        class="absolute inset-0 z-20 flex items-center justify-center bg-violet-500/20 backdrop-blur-[2px]"
      >
        <Folder class="size-8 text-white drop-shadow" />
      </div>

      <!-- hover 操作 -->
      <div class="absolute inset-x-2 top-2 z-20 flex justify-end gap-1 opacity-0 transition-opacity group-hover:opacity-100">
        <button
          type="button"
          class="flex size-7 items-center justify-center rounded-full bg-black/30 text-white backdrop-blur-sm transition-colors hover:bg-destructive/80"
          @click.stop="deleteOpen = true"
        >
          <Trash2 class="size-3.5" />
        </button>
        <button
          type="button"
          class="flex size-7 items-center justify-center rounded-full bg-black/30 text-white backdrop-blur-sm transition-colors hover:bg-black/50"
          @click.stop="startRename"
        >
          <Pencil class="size-3.5" />
        </button>
      </div>

      <!-- 删除确认遮罩 -->
      <div
        v-if="deleteOpen"
        class="absolute inset-0 z-30 flex flex-col items-center justify-center gap-3 bg-black/55 px-3 backdrop-blur-[6px]"
        @click.stop
      >
        <span class="text-[13px] font-medium text-white/90">{{ t('home.deleteFolderTitle') }}</span>

        <div
          v-if="count > 0"
          class="flex items-center gap-1.5 text-center text-[11px] text-amber-300"
        >
          <AlertTriangle class="size-3.5 shrink-0" />
          <span>{{ t('home.deleteFolderWarning') }}</span>
        </div>

        <div class="flex flex-col items-stretch gap-1.5">
          <button
            type="button"
            class="rounded-lg bg-white/15 px-3.5 py-1 text-[12px] font-medium text-white/85 transition-colors hover:bg-white/25"
            @click="((deleteOpen = false), emit('delete-only', folder.id))"
          >
            {{ t('home.deleteOnlyFolder') }}
          </button>
          <button
            v-if="count > 0"
            type="button"
            class="rounded-lg bg-red-500/90 px-3.5 py-1 text-[12px] font-medium text-white transition-colors hover:bg-red-500"
            @click="((deleteOpen = false), emit('delete-with-courses', folder.id))"
          >
            {{ t('home.deleteFolderWithCourses', { count }) }}
          </button>
          <button
            type="button"
            class="rounded-lg px-3.5 py-1 text-[12px] font-medium text-white/60 transition-colors hover:text-white/90"
            @click="deleteOpen = false"
          >
            {{ t('common.cancel') }}
          </button>
        </div>
      </div>
    </div>

    <!-- 信息行 -->
    <div class="mt-2.5 flex items-center gap-2 px-1">
      <span
        class="inline-flex shrink-0 items-center rounded-full bg-violet-100 px-2 py-0.5 text-[11px] font-medium text-violet-600 dark:bg-violet-900/30 dark:text-violet-400"
      >
        {{ t('home.folder') }}
      </span>

      <input
        v-if="editing"
        ref="nameInputRef"
        v-model="draft"
        type="text"
        maxlength="80"
        class="w-full border-b border-violet-400/60 bg-transparent text-[15px] font-medium text-foreground/90 outline-none placeholder:text-muted-foreground/40"
        @keydown.enter.prevent="commitRename"
        @keydown.esc="editing = false"
        @blur="commitRename"
      />
      <p
        v-else
        class="min-w-0 cursor-text truncate text-[15px] font-medium text-foreground/90"
        @dblclick.stop="startRename"
      >
        {{ folder.name }}
      </p>
    </div>
  </div>
</template>
