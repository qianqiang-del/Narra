<script setup lang="ts">
/**
 * RecentSection —— 文档 §5.7「我的课堂」。
 *
 * 常驻首页、不折叠；卡片网格每页 10 门课，翻页在本地做（列表一次拉全）。
 * 支持：文件夹下钻、搜索、新建文件夹。
 */
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { ChevronLeft, ChevronRight, Clock, FolderPlus, Search, X } from 'lucide-vue-next'
import { toast } from 'vue-sonner'

import ClassroomCard from '@/components/home/ClassroomCard.vue'
import FolderCard from '@/components/home/FolderCard.vue'
import NewFolderDialog from '@/components/home/NewFolderDialog.vue'
import { useLibraryStore } from '@/stores/library'

const emit = defineEmits<{
  (e: 'open-classroom', id: string): void
  (e: 'toast', message: string): void
}>()

const { t } = useI18n()
const library = useLibraryStore()

/** 每页课堂数 */
const PAGE_SIZE = 10

const currentFolderId = ref<string | null>(null)
const searchOpen = ref(false)
const keyword = ref('')
const searchInputRef = ref<HTMLInputElement | null>(null)
const newFolderOpen = ref(false)
const creatingFolder = ref(false)
const page = ref(1)

const currentFolder = computed(() =>
  currentFolderId.value ? library.folders.find((f) => f.id === currentFolderId.value) : null,
)

/** 标题旁的数量 */
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

/** 参与分页的那一列：搜索时是全部命中，否则是当前目录下的课堂 */
const listedClassrooms = computed(() => (searching.value ? searchResults.value : visibleClassrooms.value))

const pageCount = computed(() => Math.max(1, Math.ceil(listedClassrooms.value.length / PAGE_SIZE)))

/** 本页要渲染的课堂。文件夹不参与分页 —— 它们数量少，且属于列表上方另一层结构 */
const pagedClassrooms = computed(() => {
  const start = (page.value - 1) * PAGE_SIZE
  return listedClassrooms.value.slice(start, start + PAGE_SIZE)
})

const gridsEmpty = computed(() =>
  searching.value
    ? listedClassrooms.value.length === 0
    : listedClassrooms.value.length === 0 && visibleFolders.value.length === 0,
)

const emptyText = computed(() => {
  if (searching.value) return t('home.noSearchResult')
  if (currentFolderId.value) return t('home.emptyFolder')
  return t('home.noCourses')
})

// 换目录或换搜索词就回到第一页：留在旧页码上会落在一个已经不存在的页
watch([currentFolderId, keyword], () => { page.value = 1 })

// 删到当前页空了（最后一页被删空）时向前收一页，别停在空白页
watch(pageCount, (count) => { if (page.value > count) page.value = count })

function goToPage(next: number) {
  page.value = Math.min(Math.max(next, 1), pageCount.value)
}

function openFolder(id: string) {
  currentFolderId.value = id
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

function showError(error: unknown) {
  toast.error(error instanceof Error ? error.message : '操作失败，请重试')
}

async function onCreateFolder(name: string) {
  if (creatingFolder.value) return
  creatingFolder.value = true
  try {
    await library.createFolder(name)
    newFolderOpen.value = false
    emit('toast', '文件夹已创建')
  } catch (error) {
    showError(error)
  } finally {
    creatingFolder.value = false
  }
}

async function onRenameFolder(id: string, name: string) {
  try { await library.renameFolder(id, name) } catch (error) { showError(error) }
}

async function onDeleteFolder(id: string) {
  try { await library.deleteFolderOnly(id) } catch (error) { showError(error) }
}

async function onMoveClassroom(id: string, folderId: string | null) {
  try { await library.moveClassroom(id, folderId) } catch (error) { showError(error) }
}

function onCopied() {
  emit('toast', t('home.copySuccess'))
}
</script>

<template>
  <div class="relative z-10 mt-10 flex w-full max-w-6xl flex-col items-center">
    <!-- 分隔线标题条：常驻展示，不再折叠 -->
    <div class="flex h-9 w-full items-center gap-4">
      <div class="h-px flex-1 bg-border/40" />

      <div class="flex shrink-0 items-center gap-3 text-[13px] text-muted-foreground/60 select-none">
        <!-- 标题 / 面包屑 -->
        <div class="flex items-center gap-1.5">
          <Clock class="size-3.5" />
          <button
            v-if="currentFolder"
            type="button"
            class="transition-colors hover:text-foreground/80"
            @click="backToRoot"
          >
            {{ t('home.recentClassrooms') }}
          </button>
          <span v-else>{{ t('home.recentClassrooms') }}</span>
          <ChevronRight v-if="currentFolder" class="size-3 opacity-50" />
          <span v-if="currentFolder" class="font-medium text-foreground/70">{{ currentFolder.name }}</span>
          <span class="text-[11px] tabular-nums opacity-60">
            {{ searching ? searchResults.length : totalCount }}
          </span>
        </div>

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

      <div class="h-px flex-1 bg-border/40" />
    </div>

    <!-- 列表 -->
    <div class="w-full">
      <!-- 空态 -->
      <div v-if="gridsEmpty" class="pt-8 pb-2 text-center text-[13px] text-muted-foreground/60">
        {{ emptyText }}
      </div>

      <div v-else class="pt-8">
        <!-- 面包屑（搜索时） -->
        <div v-if="searching" class="-mt-3 mb-4 text-center text-[12px] text-muted-foreground/50">
          {{ t('home.searchResultTitle') }}
        </div>

        <!-- 网格 -->
        <div class="grid grid-cols-2 gap-x-5 gap-y-8 md:grid-cols-3 lg:grid-cols-4">
          <template v-if="!searching">
            <FolderCard
              v-for="f in visibleFolders"
              :key="f.id"
              :folder="f"
              :courses="library.inFolder(f.id)"
              @open="openFolder"
              @rename="onRenameFolder"
              @delete-only="onDeleteFolder"
              @drop-course="onMoveClassroom"
            />
          </template>
          <ClassroomCard
            v-for="c in pagedClassrooms"
            :key="c.id"
            :classroom="c"
            :folders="library.folders"
            @open="emit('open-classroom', $event)"
            @delete="library.deleteClassroom"
            @move="onMoveClassroom"
            @copied="onCopied"
          />
        </div>

        <!-- 翻页：只有一页时不出现，别给一个点不动的控件 -->
        <div
          v-if="pageCount > 1"
          class="mt-9 flex items-center justify-center gap-4 text-[12px] text-muted-foreground/70"
        >
          <button
            type="button"
            class="inline-flex h-7 items-center gap-1 rounded-full border border-border/50 px-3 transition-colors hover:bg-muted/60 hover:text-foreground disabled:cursor-not-allowed disabled:opacity-40 disabled:hover:bg-transparent disabled:hover:text-muted-foreground/70"
            :disabled="page <= 1"
            @click="goToPage(page - 1)"
          >
            <ChevronLeft class="size-3.5" />
            {{ t('home.prevPage') }}
          </button>

          <span class="tabular-nums">
            {{ t('home.pageIndicator', { page: page, total: pageCount }) }}
          </span>

          <button
            type="button"
            class="inline-flex h-7 items-center gap-1 rounded-full border border-border/50 px-3 transition-colors hover:bg-muted/60 hover:text-foreground disabled:cursor-not-allowed disabled:opacity-40 disabled:hover:bg-transparent disabled:hover:text-muted-foreground/70"
            :disabled="page >= pageCount"
            @click="goToPage(page + 1)"
          >
            {{ t('home.nextPage') }}
            <ChevronRight class="size-3.5" />
          </button>
        </div>
      </div>
    </div>

    <NewFolderDialog v-model:open="newFolderOpen" :submitting="creatingFolder" @create="onCreateFolder" />
  </div>
</template>
