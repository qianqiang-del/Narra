<script setup lang="ts">
/**
 * RecentSection —— 文档 §5.7「最近学习」折叠区。
 *
 * 收起时是一条居中的分隔线触发条；展开后渲染面包屑 + 卡片网格。
 * 支持：文件夹下钻、搜索、新建文件夹、导入（课堂 / PPTX 占位）。
 */
import { computed, nextTick, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ChevronDown, ChevronRight, Clock, FolderPlus, Search, X } from 'lucide-vue-next'

import ClassroomCard from '@/components/home/ClassroomCard.vue'
import FolderCard from '@/components/home/FolderCard.vue'
import NewFolderDialog from '@/components/home/NewFolderDialog.vue'
import { useLibraryStore } from '@/stores/library'
import { cn } from '@/lib/utils'

const emit = defineEmits<{
  (e: 'open-classroom', id: string): void
  (e: 'toast', message: string): void
}>()

const { t } = useI18n()
const library = useLibraryStore()

const expanded = ref(false)
const currentFolderId = ref<string | null>(null)
const searchOpen = ref(false)
const keyword = ref('')
const searchInputRef = ref<HTMLInputElement | null>(null)
const newFolderOpen = ref(false)

const currentFolder = computed(() =>
  currentFolderId.value ? library.folders.find((f) => f.id === currentFolderId.value) : null,
)

/** 折叠区标题旁的数量 */
const totalCount = computed(() => library.classrooms.length)

const searching = computed(() => keyword.value.trim().length > 0)

const visibleFolders = computed(() =>
  currentFolderId.value ? [] : library.folders,
)

const visibleClassrooms = computed(() => {
  const base = currentFolderId.value
    ? library.inFolder(currentFolderId.value)
    : library.unfiledClassrooms
  if (!searching.value) return base
  const kw = keyword.value.trim().toLowerCase()
  return base.filter((c) => c.name.toLowerCase().includes(kw))
})

/** 搜索时跨全部课程（含文件夹内） */
const searchResults = computed(() => {
  const kw = keyword.value.trim().toLowerCase()
  if (!kw) return []
  return library.classrooms.filter((c) => c.name.toLowerCase().includes(kw))
})

const gridsEmpty = computed(() =>
  searching.value
    ? searchResults.value.length === 0 && visibleClassrooms.value.length === 0
    : visibleClassrooms.value.length === 0 && visibleFolders.value.length === 0,
)

const emptyText = computed(() => {
  if (searching.value) return t('home.noSearchResult')
  if (currentFolderId.value) return t('home.emptyFolder')
  return t('home.noCourses')
})

function toggleExpand() {
  expanded.value = !expanded.value
  if (!expanded.value) {
    currentFolderId.value = null
    keyword.value = ''
    searchOpen.value = false
  }
}

function openFolder(id: string) {
  currentFolderId.value = id
  if (!expanded.value) expanded.value = true
}

function backToRoot() {
  currentFolderId.value = null
}

function toggleSearch() {
  searchOpen.value = !searchOpen.value
  if (searchOpen.value) nextTick(() => searchInputRef.value?.focus())
  else keyword.value = ''
}

function clearSearch() {
  keyword.value = ''
  nextTick(() => searchInputRef.value?.focus())
}

function onCreateFolder(name: string) {
  library.createFolder(name)
  emit('toast', '文件夹已创建')
}

function onCopied() {
  emit('toast', t('home.copySuccess'))
}
</script>

<template>
  <div class="relative z-10 mt-10 flex w-full max-w-6xl flex-col items-center">
    <!-- 分隔线触发条 -->
    <div class="group flex h-9 w-full items-center gap-4">
      <div class="h-px flex-1 bg-border/40 transition-colors group-hover:bg-border/70" />

      <div class="flex shrink-0 items-center gap-3 text-[13px] text-muted-foreground/60 select-none">
        <!-- 标题 / 面包屑 -->
        <button
          type="button"
          class="flex items-center gap-1.5 transition-colors hover:text-foreground/80"
          @click="toggleExpand"
        >
          <Clock class="size-3.5" />
          <span>{{ t('home.recentClassrooms') }}</span>
          <ChevronRight v-if="currentFolder" class="size-3 opacity-50" />
          <span v-if="currentFolder" class="font-medium text-foreground/70">{{ currentFolder.name }}</span>
          <span class="text-[11px] tabular-nums opacity-60">
            {{ searching ? searchResults.length : totalCount }}
          </span>
          <ChevronDown :class="cn('size-3.5 transition-transform duration-200', expanded && 'rotate-180')" />
        </button>

        <!-- 搜索切换 -->
        <div class="flex items-center">
          <button
            v-if="!searchOpen"
            type="button"
            class="flex size-6 items-center justify-center rounded-full text-muted-foreground/50 transition-colors hover:bg-muted/50 hover:text-foreground/70"
            @click="toggleSearch"
          >
            <Search class="size-3.5" />
          </button>
          <div v-else class="relative flex items-center">
            <Search
              class="pointer-events-none absolute left-2 size-3 text-muted-foreground/50"
            />
            <input
              ref="searchInputRef"
              v-model="keyword"
              type="text"
              :placeholder="t('home.searchCourses')"
              class="h-7 w-[200px] rounded-full border-transparent bg-muted/40 pr-6 pl-7 text-[12px] shadow-none outline-none transition-colors hover:bg-muted/60 focus:bg-muted/60"
            />
            <button
              v-if="keyword"
              type="button"
              class="absolute right-1.5 text-muted-foreground/50 hover:text-foreground"
              @click="clearSearch"
            >
              <X class="size-3" />
            </button>
          </div>
        </div>

        <!-- 新建文件夹 -->
        <button
          type="button"
          class="inline-flex size-7 items-center justify-center rounded-full bg-muted/40 text-muted-foreground ring-1 ring-border/50 transition-colors hover:bg-muted hover:text-foreground hover:ring-border"
          @click="newFolderOpen = true"
        >
          <FolderPlus class="size-3.5" />
        </button>
      </div>

      <div class="h-px flex-1 bg-border/40 transition-colors group-hover:bg-border/70" />
    </div>

    <!-- 展开内容 -->
    <Transition
      enter-active-class="transition-[height,opacity] duration-[400ms] ease-out overflow-hidden"
      enter-from-class="h-0 opacity-0"
      enter-to-class="h-auto opacity-100"
      leave-active-class="transition-[height,opacity] duration-300 ease-in overflow-hidden"
      leave-from-class="h-auto opacity-100"
      leave-to-class="h-0 opacity-0"
    >
      <div v-if="expanded" class="w-full overflow-hidden">
        <!-- 空态 -->
        <div v-if="gridsEmpty" class="pt-8 pb-2 text-center text-[13px] text-muted-foreground/60">
          {{ emptyText }}
        </div>

        <div v-else class="pt-8">
          <!-- 面包屑（搜索时） -->
          <div v-if="searching" class="-mt-3 mb-4 text-center text-[12px] text-muted-foreground/50">
            {{ t('home.searchResultTitle') }}
          </div>
          <div v-else-if="currentFolder" class="-mt-3 mb-4 flex items-center justify-center gap-1.5 text-[12px] text-muted-foreground/50">
            <button type="button" class="transition-colors hover:text-foreground/80" @click="backToRoot">
              {{ t('home.recentClassrooms') }}
            </button>
            <ChevronRight class="size-3 opacity-50" />
            <span class="text-foreground/70">{{ currentFolder.name }}</span>
          </div>

          <!-- 网格 -->
          <div class="grid grid-cols-2 gap-x-5 gap-y-8 md:grid-cols-3 lg:grid-cols-4">
            <!-- 搜索模式：平铺结果 -->
            <template v-if="searching">
              <ClassroomCard
                v-for="c in searchResults"
                :key="c.id"
                :classroom="c"
                :folders="library.folders"
                @open="emit('open-classroom', $event)"
                @rename="library.renameClassroom"
                @delete="library.deleteClassroom"
                @move="library.moveClassroom"
                @copied="onCopied"
              />
            </template>

            <template v-else>
              <FolderCard
                v-for="f in visibleFolders"
                :key="f.id"
                :folder="f"
                :courses="library.inFolder(f.id)"
                @open="openFolder"
                @rename="library.renameFolder"
                @delete-only="library.deleteFolderOnly"
                @delete-with-courses="library.deleteFolderWithCourses"
                @drop-course="(classroomId, folderId) => library.moveClassroom(classroomId, folderId)"
              />
              <ClassroomCard
                v-for="c in visibleClassrooms"
                :key="c.id"
                :classroom="c"
                :folders="library.folders"
                @open="emit('open-classroom', $event)"
                @rename="library.renameClassroom"
                @delete="library.deleteClassroom"
                @move="library.moveClassroom"
                @copied="onCopied"
              />
            </template>
          </div>
        </div>
      </div>
    </Transition>

    <NewFolderDialog v-model:open="newFolderOpen" @create="onCreateFolder" />
  </div>
</template>
