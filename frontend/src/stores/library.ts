import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { deleteClassroom as deleteClassroomRequest, fetchClassrooms } from '@/api/classroom'
import {
  addClassroomToFolder, createFolder as createFolderRequest, deleteFolder as deleteFolderRequest,
  fetchFolders, removeClassroomFromFolder, renameFolder as renameFolderRequest,
  type FolderDTO,
} from '@/api/folder'
import { toScene } from '@/lib/scene-mapper'
import type { Scene } from '@/types/scene'

export interface Classroom {
  id: string
  name: string
  /** 幻灯片页数，不含课程完成页 */
  pages: number
  /** 已生成好的页数；大于 0 就能进课堂，0 则要先看生成进度 */
  readyPages: number
  /** 创建时间戳（ms） */
  createdAt: number
  /** 所属文件夹 id，null 表示未归档 */
  folderId: string | null
  /** 封面：首个内容页的真实内容，卡片按主画布同款版式渲染；大纲还没落库时为 null */
  cover?: Scene | null
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

function toFolder(item: FolderDTO): Folder {
  return { id: String(item.id), name: item.name, createdAt: Date.parse(item.created_at) }
}

export const useLibraryStore = defineStore('library', () => {
  const classrooms = ref<Classroom[]>([])
  const folders = ref<Folder[]>([])

  async function loadClassrooms() {
    const items = await fetchClassrooms()
    classrooms.value = items.map((item) => ({
      id: String(item.id), name: item.title, pages: item.pages, readyPages: item.ready_pages,
      createdAt: Date.parse(item.created_at), updatedAt: Date.parse(item.updated_at),
      folderId: item.folder_id == null ? null : String(item.folder_id),
      mode: item.mode === 'interactive' ? 'interactive' : 'vocational',
      status: item.status, generationError: item.generation_error,
      cover: item.cover ? toScene(item.cover) : null,
    }))
  }

  async function loadFolders() {
    folders.value = (await fetchFolders()).map(toFolder)
  }

  const unfiledClassrooms = computed(() => classrooms.value.filter((c) => !c.folderId))

  function inFolder(folderId: string) {
    return classrooms.value.filter((c) => c.folderId === folderId)
  }

  async function createFolder(name: string): Promise<Folder> {
    const folder = toFolder(await createFolderRequest(name))
    folders.value = [...folders.value, folder]
    return folder
  }

  async function renameFolder(id: string, name: string) {
    const updated = toFolder(await renameFolderRequest(id, name))
    folders.value = folders.value.map((folder) => folder.id === id ? updated : folder)
  }

  /** 仅删文件夹：课程回落到顶层 */
  async function deleteFolderOnly(id: string) {
    await deleteFolderRequest(id)
    folders.value = folders.value.filter((f) => f.id !== id)
    classrooms.value = classrooms.value.map((c) => (c.folderId === id ? { ...c, folderId: null } : c))
  }

  async function deleteClassroom(id: string) {
    await deleteClassroomRequest(Number(id))
    classrooms.value = classrooms.value.filter((c) => c.id !== id)
  }

  async function moveClassroom(id: string, folderId: string | null) {
    const c = classrooms.value.find((x) => x.id === id)
    if (!c || c.folderId === folderId) return
    if (folderId) await addClassroomToFolder(folderId, id)
    else if (c.folderId) await removeClassroomFromFolder(c.folderId, id)
    c.folderId = folderId
  }

  return {
    classrooms,
    folders,
    unfiledClassrooms,
    inFolder,
    createFolder,
    renameFolder,
    deleteFolderOnly,
    deleteClassroom,
    moveClassroom,
    loadClassrooms,
    loadFolders,
  }
})
