/**
 * 最近学习的课程 / 文件夹数据。
 *
 * 当前为 localStorage 持久化的前端状态（原项目挂在 zustand store 上）。
 * 接入 Go 后端后，把 load/save 换成 API 调用即可，对外接口不变。
 */
import { defineStore } from 'pinia'
import { ref, computed, watch } from 'vue'
import { deleteClassroom as deleteClassroomRequest, fetchClassrooms } from '@/api/classroom'

export interface Classroom {
  id: string
  name: string
  /** 幻灯片页数 */
  pages: number
  /** 创建时间戳（ms） */
  createdAt: number
  /** 所属文件夹 id，null 表示未归档 */
  folderId: string | null
  /** 缩略图；无则用占位块 */
  thumbnail?: string
  /** 模式徽章：职教 / 交互 */
  mode?: 'vocational' | 'interactive'
  status?: 'generating' | 'playable' | 'ready' | 'failed'
  generationError?: string | null
  updatedAt?: number
}

export interface Folder {
  id: string
  name: string
  createdAt: number
}

const STORAGE_KEY = 'narra-library'

interface LibraryState {
  classrooms: Classroom[]
  folders: Folder[]
}

/** 归一化时间：今天 / 昨天 / N 天前 / 具体日期 */
export function formatRelativeDate(ts: number, locale: string, t: (k: string, p?: Record<string, unknown>) => string) {
  const now = new Date()
  const startOfToday = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime()
  const days = Math.floor((startOfToday - ts) / 86_400_000)
  if (days <= 0) return t('home.today')
  if (days === 1) return t('home.yesterday')
  if (days < 7) return t('home.daysAgo', { count: days })
  return new Date(ts).toLocaleDateString(locale)
}

function seed(): LibraryState {
  const now = Date.now()
  const day = 86_400_000
  return {
    folders: [
      { id: 'f-math', name: '数学', createdAt: now - 12 * day },
      { id: 'f-pro', name: '专业课', createdAt: now - 5 * day },
    ],
    classrooms: [
      { id: 'c-1', name: '从零学 Python：30 分钟写出第一个程序', pages: 18, createdAt: now - 2 * 3600_000, folderId: null },
      { id: 'c-2', name: '线性代数入门：矩阵与向量空间', pages: 24, createdAt: now - day, folderId: 'f-math', mode: 'interactive' },
      { id: 'c-3', name: '数控车床实操训练', pages: 12, createdAt: now - 2 * day, folderId: 'f-pro', mode: 'vocational' },
      { id: 'c-4', name: '英语口语：日常对话场景', pages: 16, createdAt: now - 3 * day, folderId: null },
      { id: 'c-5', name: '概率论与数理统计', pages: 30, createdAt: now - 6 * day, folderId: 'f-math' },
      { id: 'c-6', name: '计算机网络原理速览', pages: 21, createdAt: now - 14 * day, folderId: null },
    ],
  }
}

function load(): LibraryState {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (raw) return JSON.parse(raw) as LibraryState
  } catch {
    /* 存档损坏则回落到种子数据 */
  }
  return seed()
}

export const useLibraryStore = defineStore('library', () => {
  const initial = load()
  // 课堂数据以服务端为唯一来源，不能使用本地 seed/mock 数据。
  const classrooms = ref<Classroom[]>([])
  const folders = ref<Folder[]>(initial.folders)

  async function loadClassrooms() {
    const items = await fetchClassrooms()
    classrooms.value = items.map((item) => ({
      id: String(item.id), name: item.title, pages: 0,
      createdAt: Date.parse(item.created_at), updatedAt: Date.parse(item.updated_at),
      folderId: item.folder_id == null ? null : String(item.folder_id),
      mode: item.mode === 'interactive' ? 'interactive' : 'vocational',
      status: item.status, generationError: item.generation_error,
    }))
  }

  watch(
    [classrooms, folders],
    () => {
      try {
        localStorage.setItem(
          STORAGE_KEY,
          JSON.stringify({ classrooms: classrooms.value, folders: folders.value }),
        )
      } catch {
        /* 隐私模式忽略 */
      }
    },
    { deep: true },
  )

  const unfiledClassrooms = computed(() => classrooms.value.filter((c) => !c.folderId))

  function inFolder(folderId: string) {
    return classrooms.value.filter((c) => c.folderId === folderId)
  }

  function createFolder(name: string): Folder {
    const folder: Folder = { id: `f-${Date.now()}`, name, createdAt: Date.now() }
    folders.value = [...folders.value, folder]
    return folder
  }

  function renameFolder(id: string, name: string) {
    const f = folders.value.find((x) => x.id === id)
    if (f) f.name = name
  }

  /** 仅删文件夹：课程回落到顶层 */
  function deleteFolderOnly(id: string) {
    folders.value = folders.value.filter((f) => f.id !== id)
    classrooms.value = classrooms.value.map((c) => (c.folderId === id ? { ...c, folderId: null } : c))
  }

  /** 连课程一起删 */
  function deleteFolderWithCourses(id: string) {
    folders.value = folders.value.filter((f) => f.id !== id)
    classrooms.value = classrooms.value.filter((c) => c.folderId !== id)
  }

  function renameClassroom(id: string, name: string) {
    const c = classrooms.value.find((x) => x.id === id)
    if (c) c.name = name
  }

  async function deleteClassroom(id: string) {
    await deleteClassroomRequest(Number(id))
    classrooms.value = classrooms.value.filter((c) => c.id !== id)
  }

  function moveClassroom(id: string, folderId: string | null) {
    const c = classrooms.value.find((x) => x.id === id)
    if (c) c.folderId = folderId
  }

  function renameFolderOf(id: string, name: string) {
    renameFolder(id, name)
  }

  return {
    classrooms,
    folders,
    unfiledClassrooms,
    inFolder,
    createFolder,
    renameFolder,
    deleteFolderOnly,
    deleteFolderWithCourses,
    renameClassroom,
    renameFolderOf,
    deleteClassroom,
    moveClassroom,
    loadClassrooms,
  }
})
