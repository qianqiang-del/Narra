<script setup lang="ts">
/**
 * ClassroomCard —— 文档 §5.8。
 *
 * 16:9 缩略图 + 模式徽章 + hover 操作（移动/删除）+ 信息行。
 */
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Atom, Copy, FolderInput, GripVertical, Trash2, Wrench } from 'lucide-vue-next'

import ClassroomCover from '@/components/home/ClassroomCover.vue'
import UiTooltip from '@/components/ui/UiTooltip.vue'
import { formatRelativeDate, type Classroom } from '@/stores/library'
import { cn } from '@/lib/utils'

const props = defineProps<{
  classroom: Classroom
  /** 可拖入的文件夹列表（移动菜单用） */
  folders: { id: string; name: string }[]
}>()

const emit = defineEmits<{
  (e: 'open', id: string): void
  (e: 'delete', id: string): void
  (e: 'move', id: string, folderId: string | null): void
  (e: 'copied'): void
}>()

const { t, locale } = useI18n()

const confirmingDelete = ref(false)
const moveOpen = ref(false)
const dragging = ref(false)

const dateLabel = computed(() =>
  formatRelativeDate(props.classroom.createdAt, locale.value, t as never),
)

async function copyName() {
  try {
    await navigator.clipboard.writeText(props.classroom.name)
    emit('copied')
  } catch {
    /* 剪贴板不可用则静默 */
  }
}

function onDragStart(e: DragEvent) {
  if (!e.dataTransfer) return
  e.dataTransfer.setData('text/stage-id', props.classroom.id)
  e.dataTransfer.effectAllowed = 'move'
  dragging.value = true
}
</script>

<template>
  <div
    :class="['group relative cursor-grab active:cursor-grabbing', dragging && 'opacity-50']"
    draggable="true"
    @dragstart="onDragStart"
    @dragend="dragging = false"
  >
    <!-- 缩略图 -->
    <div
      class="relative aspect-[16/9] w-full overflow-hidden rounded-2xl bg-muted transition-[transform,box-shadow] duration-200 group-hover:scale-[1.02] group-hover:shadow-soft"
      @click="!confirmingDelete && emit('open', classroom.id)"
    >
      <ClassroomCover :scene="classroom.cover" :title="classroom.name" />

      <span class="pointer-events-none absolute top-2 left-2 z-10 flex size-7 items-center justify-center rounded-full bg-black/30 text-white opacity-0 transition-opacity group-hover:opacity-100" title="拖动课堂到文件夹">
        <GripVertical class="size-3.5" />
      </span>

      <!-- 模式徽章 -->
      <div
        v-if="classroom.mode"
        :class="
          cn(
            'absolute bottom-2 left-2 z-10 inline-flex size-5 items-center justify-center rounded-full bg-card/70 shadow-sm backdrop-blur-sm dark:bg-slate-900/60',
            classroom.mode === 'vocational'
              ? 'text-gold-600 ring-1 ring-gold-500/35'
              : 'text-cyan-600 ring-1 ring-cyan-500/30',
          )
        "
      >
        <Wrench v-if="classroom.mode === 'vocational'" class="size-3" />
        <Atom v-else class="size-3" />
      </div>

      <!-- hover 操作 -->
      <div class="absolute inset-x-2 top-2 flex justify-end gap-1 opacity-0 transition-opacity group-hover:opacity-100">
        <button
          type="button"
          class="flex size-7 items-center justify-center rounded-full bg-black/30 text-white backdrop-blur-sm transition-[border-color,box-shadow,transform] duration-200 hover:-translate-y-0.5 hover:border-brand-300 hover:shadow-soft hover:bg-destructive/80"
          @click.stop="confirmingDelete = true"
        >
          <Trash2 class="size-3.5" />
        </button>
        <UiTooltip content="移动到文件夹">
          <button
            type="button"
            class="flex size-7 items-center justify-center rounded-full bg-black/30 text-white backdrop-blur-sm transition-[border-color,box-shadow,transform] duration-200 hover:-translate-y-0.5 hover:border-brand-300 hover:shadow-soft hover:bg-black/50"
            @click.stop="moveOpen = !moveOpen"
          >
            <FolderInput class="size-3.5" />
          </button>
        </UiTooltip>

      </div>

      <!-- 删除确认遮罩 -->
      <div
        v-if="confirmingDelete"
        class="absolute inset-0 z-10 flex flex-col items-center justify-center gap-3 bg-black/50 backdrop-blur-[6px]"
        @click.stop
      >
        <span class="text-[13px] font-medium text-white/90">{{ t('home.deleteClassroomTitle') }}</span>
        <div class="flex items-center gap-2">
          <button
            type="button"
            class="rounded-lg bg-white/15 px-3.5 py-1 text-[12px] font-medium text-white/80 transition-[border-color,box-shadow,transform] duration-200 hover:-translate-y-0.5 hover:border-brand-300 hover:shadow-soft hover:bg-muted/25"
            @click="confirmingDelete = false"
          >
            {{ t('common.cancel') }}
          </button>
          <button
            type="button"
            class="rounded-lg bg-red-500/90 px-3.5 py-1 text-[12px] font-medium text-white transition-[border-color,box-shadow,transform] duration-200 hover:-translate-y-0.5 hover:border-brand-300 hover:shadow-soft hover:bg-red-500"
            @click="((confirmingDelete = false), emit('delete', classroom.id))"
          >
            {{ t('common.delete') }}
          </button>
        </div>
      </div>
    </div>

    <!-- 菜单位于封面裁剪区域外，文件夹选项才能完整显示并可点击。 -->
    <div
      v-if="moveOpen"
      class="absolute top-10 right-2 z-30 max-h-52 w-40 overflow-y-auto rounded-lg border border-border bg-popover p-1 shadow-lg"
      @click.stop
    >
      <button
        v-for="f in folders.filter((item) => item.id !== classroom.folderId)"
        :key="f.id"
        type="button"
        class="flex w-full items-center rounded-md px-2.5 py-1.5 text-left text-xs hover:bg-muted/60"
        @click="((moveOpen = false), emit('move', classroom.id, f.id))"
      >
        {{ f.name }}
      </button>
      <button
        v-if="classroom.folderId"
        type="button"
        class="flex w-full items-center rounded-md px-2.5 py-1.5 text-left text-xs text-muted-foreground hover:bg-muted/60"
        @click="((moveOpen = false), emit('move', classroom.id, null))"
      >
        移出文件夹
      </button>
    </div>

    <!-- 信息行 -->
    <div class="mt-2.5 flex items-center gap-2 px-1">
      <span
        class="inline-flex shrink-0 items-center rounded-full bg-brand-100 px-2 py-0.5 text-[11px] font-medium text-brand-700 dark:bg-brand-900/30 dark:text-brand-400"
      >
        {{ t('home.classroomCount', { count: classroom.pages, date: dateLabel }) }}
      </span>

      <UiTooltip side="bottom" :content="classroom.name">
        <p
          class="min-w-0 truncate text-[15px] font-medium text-foreground/90"
        >
          {{ classroom.name }}
        </p>
      </UiTooltip>
      <button
        type="button"
        class="shrink-0 rounded p-0.5 text-muted-foreground/40 transition-[border-color,box-shadow,transform] duration-200 hover:-translate-y-0.5 hover:border-brand-300 hover:shadow-soft hover:text-foreground"
        :title="t('home.copyName')"
        @click.stop="copyName"
      >
        <Copy class="size-3" />
      </button>
    </div>
  </div>
</template>
