<script setup lang="ts">
/**
 * FolderCard —— 文档 §5.9。
 *
 * 16:9 渐变封面 + 最多 3 张扇形堆叠缩略图 + 课程数徽章，
 * hover 可删除/重命名，支持拖拽放置课程。
 */
import { computed, nextTick, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { AlertTriangle, Check, Folder, Pencil, Trash2, X } from 'lucide-vue-next'

import ClassroomCover from '@/components/home/ClassroomCover.vue'
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
  })
}

function commitRename() {
  const next = draft.value.trim()
  if (!next) return
  if (next !== props.folder.name) emit('rename', props.folder.id, next.slice(0, 80))
  editing.value = false
}

function cancelRename() {
  editing.value = false
  draft.value = ''
}

function onDragOver(e: DragEvent) {
  if (!e.dataTransfer?.types.includes('text/stage-id')) return
  e.preventDefault()
  e.dataTransfer.dropEffect = 'move'
  dragOver.value = true
}

function onDragLeave(e: DragEvent) {
  if (!e.currentTarget || (e.relatedTarget instanceof Node && (e.currentTarget as Node).contains(e.relatedTarget))) return
  dragOver.value = false
}

function onDrop(e: DragEvent) {
  dragOver.value = false
  const id = e.dataTransfer?.getData('text/stage-id')
  if (!id) return
  e.preventDefault()
  emit('drop-course', id, props.folder.id)
}
</script>

<template>
  <div
    :class="cn('group cursor-pointer', dragOver && 'relative z-20')"
    @dragover="onDragOver"
    @dragleave="onDragLeave"
    @drop="onDrop"
  >
    <div
      :class="
        cn(
          'relative aspect-[16/9] w-full overflow-hidden rounded-2xl bg-gradient-to-br from-brand-50 to-brand-100/70 ring-1 ring-brand-200/50 transition-[transform,box-shadow] duration-200 group-hover:scale-[1.02] group-hover:shadow-soft dark:from-brand-900/25 dark:to-brand-950/30 dark:ring-brand-800/40',
          dragOver
            ? 'scale-[1.03] ring-2 ring-brand-600 ring-offset-2 ring-offset-background'
            : 'ring-brand-200/50 dark:ring-brand-800/40',
        )
      "
      @click="!deleteOpen && emit('open', folder.id)"
    >
      <!-- 封面堆叠 -->
      <div v-if="stack.length" class="flex size-full items-center justify-center">
        <div
          v-for="(c, i) in stack"
          :key="c.id"
          class="absolute aspect-[16/9] overflow-hidden rounded-xl bg-slate-200 shadow-md ring-1 ring-black/5"
          :style="stackStyle(i)"
        >
          <ClassroomCover :scene="c.cover" :title="c.name" />
        </div>
      </div>

      <!-- 空文件夹 -->
      <div v-else class="flex size-full items-center justify-center">
        <div class="flex size-14 items-center justify-center rounded-2xl bg-brand-100 dark:bg-brand-900/40">
          <Folder class="size-7 text-brand-600" />
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
        class="pointer-events-none absolute inset-0 z-20 flex items-center justify-center bg-brand-600/20 backdrop-blur-[2px]"
      >
        <Folder class="size-8 text-white drop-shadow" />
      </div>

      <!-- hover 操作 -->
      <div class="absolute inset-x-2 top-2 z-20 flex justify-end gap-1 opacity-0 transition-opacity group-hover:opacity-100">
        <button
          type="button"
          class="flex size-7 items-center justify-center rounded-full bg-black/30 text-white backdrop-blur-sm transition-[border-color,box-shadow,transform] duration-200 hover:-translate-y-0.5 hover:border-brand-300 hover:shadow-soft hover:bg-destructive/80"
          @click.stop="deleteOpen = true"
        >
          <Trash2 class="size-3.5" />
        </button>
        <button
          type="button"
          class="flex size-7 items-center justify-center rounded-full bg-black/30 text-white backdrop-blur-sm transition-[border-color,box-shadow,transform] duration-200 hover:-translate-y-0.5 hover:border-brand-300 hover:shadow-soft hover:bg-black/50"
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
          class="flex items-center gap-1.5 text-center text-[11px] text-gold-300"
        >
          <AlertTriangle class="size-3.5 shrink-0" />
          <span>{{ t('home.deleteOnlyFolderDesc') }}</span>
        </div>

        <div class="flex flex-col items-stretch gap-1.5">
          <button
            type="button"
            class="rounded-lg bg-white/15 px-3.5 py-1 text-[12px] font-medium text-white/85 transition-[border-color,box-shadow,transform] duration-200 hover:-translate-y-0.5 hover:border-brand-300 hover:shadow-soft hover:bg-muted/25"
            @click="((deleteOpen = false), emit('delete-only', folder.id))"
          >
            {{ t('home.deleteOnlyFolder') }}
          </button>
          <button
            type="button"
            class="rounded-lg px-3.5 py-1 text-[12px] font-medium text-white/60 transition-[border-color,box-shadow,transform] duration-200 hover:-translate-y-0.5 hover:border-brand-300 hover:shadow-soft hover:text-white/90"
            @click="deleteOpen = false"
          >
            {{ t('common.cancel') }}
          </button>
        </div>
      </div>
    </div>

    <!-- 信息行 -->
    <div class="mt-2.5 flex h-11 min-w-0 items-center gap-2 px-1">
      <span
        v-if="!editing"
        class="inline-flex shrink-0 items-center rounded-full bg-brand-100 px-2 py-0.5 text-[11px] font-medium text-brand-700 dark:bg-brand-900/30 dark:text-brand-400"
      >
        {{ t('home.folder') }}
      </span>

      <div v-if="editing" class="rename-field flex h-11 min-w-0 flex-1 items-center gap-2 rounded-md border border-border bg-card px-3 focus-within:border-brand-500">
        <Folder class="size-4 shrink-0 text-muted-foreground" />
        <input
          ref="nameInputRef"
          v-model="draft"
          type="text"
          maxlength="80"
          :aria-label="t('home.folderNameLabel')"
          class="min-w-0 flex-1 bg-transparent text-sm outline-none"
          @keydown.enter.prevent="commitRename"
          @keydown.esc.prevent="cancelRename"
        />
        <button type="button" :disabled="!draft.trim()" :title="t('common.confirm')" :aria-label="t('common.confirm')" class="flex size-7 shrink-0 items-center justify-center rounded text-brand-700 transition-colors hover:bg-muted disabled:cursor-not-allowed disabled:opacity-40 dark:text-brand-300" @click.stop="commitRename">
          <Check class="size-4" />
        </button>
        <button type="button" :title="t('common.cancel')" :aria-label="t('common.cancel')" class="flex size-7 shrink-0 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-muted hover:text-foreground" @click.stop="cancelRename">
          <X class="size-4" />
        </button>
      </div>
      <p
        v-else
        class="min-w-0 flex-1 cursor-text truncate text-[15px] font-medium text-foreground/90"
        @dblclick.stop="startRename"
      >
        {{ folder.name }}
      </p>
    </div>
  </div>
</template>

<style scoped>
.rename-field input:focus-visible {
  outline: none;
  box-shadow: none;
}
</style>
