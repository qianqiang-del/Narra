import { request, streamEvents } from './client'

/**
 * 课堂的创建与查询。
 *
 * 受理是异步的：`POST /classrooms` 立刻返回、status 为 `generating`，
 * 大纲与场景内容由后台任务生成，要拿进度就轮询 `GET /classrooms/:id`。
 * 响应字段与后端 `responsedto.Classroom` 一一对应，下划线命名只在本文件出现。
 */

/** 创建课堂的入参，与后端 `request.CreateClassroom` 对齐 */
export interface CreateClassroomInput {
  requirement: string
  /** 深度交互：`interactive` 会在大纲里多排可交互页 */
  mode: 'vocational' | 'interactive'
  llm_provider_id: number
  llm_model_id: string
  /** 联网搜索；关掉时不给模型挂任何 MCP 工具 */
  web_search: boolean
  /** 用户简介（首页个人资料里那段自我介绍）；会进大纲提示词，影响讲解深浅，可空 */
  bio: string
  /** `preset` 用 role_ids 指定的角色，`auto` 从角色池随机挑 */
  agent_mode: 'preset' | 'auto'
  role_ids: string[]
  /** `preset` 模式下各角色的音色：agent_key → voice_id；缺省用角色默认音色 */
  role_voices: Record<string, string>
  /** 教师音色；缺省用教师默认音色 */
  teacher_voice: string
}

/** 课堂状态；`generating` 表示后台仍在生成 */
export type ClassroomStatus = 'generating' | 'playable' | 'ready' | 'failed'

/** 课堂里的一个角色：`agent_key` 拿去角色池取展示信息，`voice_id` 是本课程选定的音色 */
export interface ClassroomAgentBrief {
  agent_key: string
  voice_id: string
}

/** 后端 `responsedto.Classroom` 的原样形状 */
export interface ClassroomDTO {
  id: number
  folder_id?: number | null
  title: string
  requirement: string
  mode: string
  status: ClassroomStatus
  generation_error: string | null
  /** 本课程的角色，按角色池顺序；auto 模式或未指定时为空数组 */
  agents: ClassroomAgentBrief[]
  created_at: string
  updated_at: string
}

/** 受理一门课的生成；返回时内容还没生成，status 是 generating。 */
export function createClassroom(input: CreateClassroomInput): Promise<ClassroomDTO> {
  return request<ClassroomDTO>('/classrooms', {
    method: 'POST',
    body: JSON.stringify(input),
  })
}

/** 查询一门课；生成期间轮询它拿状态。 */
export function fetchClassroom(id: number): Promise<ClassroomDTO> {
  return request<ClassroomDTO>(`/classrooms/${id}`)
}

export function fetchClassrooms(): Promise<ClassroomDTO[]> {
  return request<ClassroomDTO[]>('/classrooms')
}

export function deleteClassroom(id: number): Promise<{ id: number }> {
  return request<{ id: number }>(`/classrooms/${id}`, { method: 'DELETE' })
}

export interface ClassroomOutlineDTO {
  classroom_id: number
  title: string
  scenes: { id: number; sort_order: number; type: string; title: string; brief: string; status: string }[]
}

export interface ClassroomSceneSummaryDTO {
  id: number
  sort_order: number
  type: string
  title: string
  status: 'pending' | 'generating' | 'ready' | 'failed'
  error_message: string | null
}

export function fetchClassroomOutline(id: number): Promise<ClassroomOutlineDTO> {
  return request<ClassroomOutlineDTO>(`/classrooms/${id}/outline`)
}

export function fetchClassroomAgents(id: number): Promise<RoleCardDTO[]> {
  return request<RoleCardDTO[]>(`/classrooms/${id}/agents`)
}

export interface RoleCardDTO {
  agent_key: string
  name: string
  role: string
  role_type: string
  persona: string
  avatar: string
  color: string
  voice_id: string
  sort_order: number
}

export function fetchClassroomScenes(id: number): Promise<ClassroomSceneSummaryDTO[]> {
  return request<ClassroomSceneSummaryDTO[]>(`/classrooms/${id}/scenes`)
}

export async function* streamClassroomEvents(id: number, signal?: AbortSignal) {
  for await (const event of streamEvents(`/classrooms/${id}/events`, signal)) {
    if (event.event === 'classroom') yield JSON.parse(event.data) as ClassroomDTO
  }
}

export interface SceneDetailDTO {
  id: number; sort_order: number; type: string; title: string; brief: string; status: string
  content: { blocks?: { key?: string; type?: string; content?: string; text?: string; interaction?: { kind?: string; controls?: Record<string, unknown>[]; options?: string[]; answer?: string; config?: Record<string, unknown> } }[] }
  narration: { id: number; content_key: string; sort_order: number; text: string; status: string; audio_path: string | null }[]
  error_message: string | null
}

export function fetchScene(id: number): Promise<SceneDetailDTO> {
  return request<SceneDetailDTO>(`/scenes/${id}`)
}
