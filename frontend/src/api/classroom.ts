import { request, streamEvents } from './client'
import type { SceneContentSource } from '@/lib/scene-mapper'

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

/** 列表里的一项：课堂本身，加上卡片要用的页数、已就绪页数与封面首页 */
export interface ClassroomListItemDTO extends ClassroomDTO {
  /** 页数，不含代码追加的课程完成页 */
  pages: number
  /** 已生成好的页数；大于 0 说明课堂进得去 */
  ready_pages: number
  /** 首个内容页，供卡片按主画布同款版式渲染封面；大纲还没落库时为 null */
  cover: ClassroomCoverSceneDTO | null
}

export function fetchClassrooms(): Promise<ClassroomListItemDTO[]> {
  return request<ClassroomListItemDTO[]>('/classrooms')
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
  /** 这一页生成到哪一步：status 说成不成，phase 说走到哪一步（planning/researching/…/synthesizing） */
  phase: string
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

/**
 * 生成进度流的一条事件。
 *
 * 后端只推「变化」：连上时先给整份 classroom 与整份 scene.snapshot，之后每有新变化才推一条。
 * 所以消费方不需要（也不该）自己轮询——连接建立那一刻拿到的快照就是当时的最新全量状态；
 * 断线重连也是同理，重连拿到的新快照就是最新状态，不必回放。
 *
 * kind 与后端事件名的对应：
 *   classroom       ← classroom（受理时一帧，之后每次状态变化各一帧）
 *   plan-started    ← classroom.plan.started（大纲还没落库）
 *   plan-completed  ← classroom.plan.completed（场景行刚建出来）
 *   stage           ← classroom.playable / classroom.ready / classroom.failed
 *   snapshot        ← scene.snapshot（大纲落库、页面增减时重发整份）
 *   scene           ← scene.started / scene.researching / scene.content.started /
 *                     scene.content.completed / scene.narration.completed /
 *                     scene.reviewing.completed / scene.ready / scene.failed
 *   error           ← error（服务端读库失败，流随即关闭）
 */
export type ClassroomProgressEvent =
  | { kind: 'classroom'; classroom: ClassroomDTO }
  | { kind: 'plan-started' }
  | { kind: 'plan-completed'; sceneCount: number }
  | { kind: 'stage'; status: ClassroomStatus }
  | { kind: 'snapshot'; scenes: ClassroomSceneSummaryDTO[] }
  | { kind: 'scene'; scene: ClassroomSceneSummaryDTO }
  | { kind: 'error'; message: string }

/** 订阅一门课的生成进度，逐条产出变化；走到终态时由服务端收流。 */
export async function* streamClassroomEvents(id: number, signal?: AbortSignal): AsyncGenerator<ClassroomProgressEvent> {
  for await (const event of streamEvents(`/classrooms/${id}/events`, signal)) {
    if (event.event.startsWith('scene.') && event.event !== 'scene.snapshot') {
      yield { kind: 'scene', scene: JSON.parse(event.data) as ClassroomSceneSummaryDTO }
      continue
    }
    switch (event.event) {
      case 'classroom':
        yield { kind: 'classroom', classroom: JSON.parse(event.data) as ClassroomDTO }
        break
      case 'classroom.plan.started':
        yield { kind: 'plan-started' }
        break
      case 'classroom.plan.completed':
        yield { kind: 'plan-completed', sceneCount: Number((JSON.parse(event.data) as { scene_count?: number }).scene_count ?? 0) }
        break
      case 'classroom.playable':
      case 'classroom.ready':
      case 'classroom.failed':
        yield { kind: 'stage', status: event.event.slice('classroom.'.length) as ClassroomStatus }
        break
      case 'scene.snapshot':
        yield { kind: 'snapshot', scenes: (JSON.parse(event.data) as { scenes: ClassroomSceneSummaryDTO[] }).scenes }
        break
      case 'error':
        yield { kind: 'error', message: (JSON.parse(event.data) as { message?: string }).message ?? '状态流中断' }
        break
      default:
        break
    }
  }
}

/** 列表卡片封面要画的首页；与场景详情同源，只少讲稿与审核结论 */
export type ClassroomCoverSceneDTO = SceneContentSource

/** 场景详情；正文部分（content 与 interactive_html）由 SceneContentSource 给出 */
export interface SceneDetailDTO extends SceneContentSource {
  sort_order: number
  brief: string
  narration: { id: number; content_key: string; sort_order: number; text: string; status: string; audio_path: string | null }[]
  error_message: string | null
}

export function fetchScene(id: number): Promise<SceneDetailDTO> {
  return request<SceneDetailDTO>(`/scenes/${id}`)
}
