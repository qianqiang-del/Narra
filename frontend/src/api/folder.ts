import { request } from './client'

export interface FolderDTO {
  id: number
  name: string
  classroom_count: number
  created_at: string
  updated_at: string
}

export interface FolderDetailDTO extends FolderDTO {
  classrooms: { id: number; title: string; mode: string; status: string; created_at: string; updated_at: string }[]
}

export function fetchFolders(): Promise<FolderDTO[]> {
  return request<FolderDTO[]>('/folders')
}

export function fetchFolder(id: string): Promise<FolderDetailDTO> {
  return request<FolderDetailDTO>(`/folders/${id}`)
}

export function createFolder(name: string): Promise<FolderDTO> {
  return request<FolderDTO>('/folders', { method: 'POST', body: JSON.stringify({ name }) })
}

export function renameFolder(id: string, name: string): Promise<FolderDTO> {
  return request<FolderDTO>(`/folders/${id}`, { method: 'PATCH', body: JSON.stringify({ name }) })
}

export function deleteFolder(id: string): Promise<{ id: number }> {
  return request<{ id: number }>(`/folders/${id}`, { method: 'DELETE' })
}

export function addClassroomToFolder(folderId: string, classroomId: string): Promise<void> {
  return request<void>(`/folders/${folderId}/classrooms/${classroomId}`, { method: 'PUT' })
}

export function removeClassroomFromFolder(folderId: string, classroomId: string): Promise<void> {
  return request<void>(`/folders/${folderId}/classrooms/${classroomId}`, { method: 'DELETE' })
}
