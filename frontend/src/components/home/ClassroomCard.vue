<script setup lang="ts">
/**
 * ClassroomCard —— 文档 §5.8。
 *
 * 16:9 缩略图 + 模式徽章 + hover 操作（重命名/移动/删除）+ 信息行（页数徽章 + 名称）。
 * 双击名称可重命名；删除有确认遮罩。
 */
import { computed, nextTick, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Atom, Copy, FolderInput, Pencil, Trash2, Wrench } from 'lucide-vue-next'

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
  (e: 'rename', id: string, name: string): void
  (e: 'delete', id: string): void
  (e: 'move', id: string, folderId: string | null): void
  (e: 'copied'): void
}>()

const { t, locale } = useI18n()

const editing = ref(false)
const draft = ref('')
const confirmingDelete = ref(false)
const moveOpen = ref(false)
const nameInputRef = ref<HTMLInputElement | null>(null)

const dateLabel = computed(() =>
  formatRelativeDate(props.classroom.createdAt, locale.value, t as never),
)

function startRename() {
  draft.value = props.classroom.name
  editing.value = true
  nextTick(() => {
    nameInputRef.value?.focus()
    nameInputRef.value?.select()
  })
}

function commitRename() {
  const next = draft.value.trim()
  if (next && next !== props.classroom.name) emit('rename', props.classroom.id, next.slice(0, 100))
  editing.value = false
}

async function copyName() {
  try {
    await navigator.clipboard.writeText(props.classroom.name)
    emit('copied')
  } catch {
    /* 剪贴板不可用则静默 */
  }
}

function onDragStart(e: DragEvent) {
  e.dataTransfer?.setData('text/stage-id', props.classroom.id)
  if (e.dataTransfer) e.dataTransfer.effectAllowed = 'move'
}
</script>

<template>
  <div
    class="group cursor-pointer"
    draggable="true"
    @dragstart="onDragStart"
  >
    <!-- 缩略图 -->
    <div
      class="relative aspect-[16/9] w-full overflow-hidden rounded-2xl bg-slate-100 transition-transform duration-200 group-hover:scale-[1.02] dark:bg-slate-800/80"
      @click="!confirmingDelete && emit('open', classroom.id)"
    >
      <img
        v-if="classroom.thumbnail"
        :src="classroom.thumbnail"
        alt=""
        class="size-full object-cover"
      />
      <div v-else class="flex size-full items-center justify-center">
        <div
          class="flex size-12 items-center justify-center rounded-2xl bg-gradient-to-br from-violet-100 to-blue-100"
        >
          <span class="text-xl opacity-50">📄</span>
        </div>
      </div>

      <!-- 模式徽章 -->
      <div
        v-if="classroom.mode"
        :class="
          cn(
            'absolute bottom-2 left-2 z-10 inline-flex size-5 items-center justify-center rounded-full bg-white/70 shadow-sm backdrop-blur-sm dark:bg-slate-900/60',
            classroom.mode === 'vocational'
              ? 'text-amber-600 ring-1 ring-amber-500/35'
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
          class="flex size-7 items-center justify-center rounded-full bg-black/30 text-white backdrop-blur-sm transition-colors hover:bg-destructive/80"
          @click.stop="confirmingDelete = true"
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

        <UiTooltip content="移动到文件夹">
          <button
            type="button"
            class="flex size-7 items-center justify-center rounded-full bg-black/30 text-white backdrop-blur-sm transition-colors hover:bg-black/50"
            @click.stop="moveOpen = !moveOpen"
          >
            <FolderInput class="size-3.5" />
          </button>
        </UiTooltip>

        <!-- 移动菜单 -->
        <div
          v-if="moveOpen"
          class="absolute top-8 right-0 z-30 w-40 overflow-hidden rounded-lg border border-border bg-popover p-1 shadow-lg"
          @click.stop
        >
          <button
            v-for="f in folders"
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
            class="rounded-lg bg-white/15 px-3.5 py-1 text-[12px] font-medium text-white/80 transition-colors hover:bg-white/25"
            @click="confirmingDelete = false"
          >
            {{ t('common.cancel') }}
          </button>
          <button
            type="button"
            class="rounded-lg bg-red-500/90 px-3.5 py-1 text-[12px] font-medium text-white transition-colors hover:bg-red-500"
            @click="((confirmingDelete = false), emit('delete', classroom.id))"
          >
            {{ t('common.delete') }}
          </button>
        </div>
      </div>
    </div>

    <!-- 信息行 -->
    <div class="mt-2.5 flex items-center gap-2 px-1">
      <span
        class="inline-flex shrink-0 items-center rounded-full bg-violet-100 px-2 py-0.5 text-[11px] font-medium text-violet-600 dark:bg-violet-900/30 dark:text-violet-400"
      >
        {{ t('home.classroomCount', { count: classroom.pages, date: dateLabel }) }}
      </span>

      <input
        v-if="editing"
        ref="nameInputRef"
        v-model="draft"
        type="text"
        maxlength="100"
        class="w-full border-b border-violet-400/60 bg-transparent text-[15px] font-medium text-foreground/90 outline-none placeholder:text-muted-foreground/40"
        @keydown.enter.prevent="commitRename"
        @keydown.esc="editing = false"
        @blur="commitRename"
      />
      <UiTooltip v-else side="bottom" :content="classroom.name">
        <p
          class="min-w-0 cursor-text truncate text-[15px] font-medium text-foreground/90"
          @dblclick.stop="startRename"
        >
          {{ classroom.name }}
        </p>
      </UiTooltip>
      <button
        v-if="!editing"
        type="button"
        class="shrink-0 rounded p-0.5 text-muted-foreground/40 transition-colors hover:text-foreground"
        :title="t('home.copyName')"
        @click.stop="copyName"
      >
        <Copy class="size-3" />
      </button>
    </div>
  </div>
</template>
