/**
 * 课堂角色静态数据。
 *
 * 原项目同样写死在前端（`lib/orchestration/registry/store.ts`），5 个预设角色 + 教师
 * 各自带 id / name / role / avatar / color / voice，外加一段 persona 系统提示词。
 *
 * 这里**只保留展示元数据**，不含 persona —— persona 是提示词，属于后端（Go + Eino）
 * 的职责，按同一个 id 去索引。所以 `id` 是前后端之间的约定，改 id 要两边一起改。
 */
export type AgentBarMode = 'preset' | 'auto'

export interface AgentRole {
  id: string
  /** 角色显示名 */
  name: string
  /** 角色定位（列表右侧小字） */
  role: string
  /** 角色大类（信息卡徽章：教师 / 助教 / 学生） */
  roleType: 'teacher' | 'assistant' | 'student'
  avatar: string
  /** 选中时的光环色 / 信息卡徽章底色 */
  color: string
  /** 默认音色 id */
  voice: string
  /**
   * 人设简介（信息卡正文，**仅展示用文案**）。
   * 完整的 system prompt（persona 提示词）属于后端（Go + Eino），按同一个 id 索引。
   */
  persona: string
}

export const TEACHER: AgentRole = {
  id: 'teacher',
  name: '陈老师',
  role: '主讲',
  roleType: 'teacher',
  avatar: '/avatars/teacher-2.png',
  color: '#722ed1',
  voice: 'voxcpm-zh-female-warm',
  persona: '主讲老师，负责整体讲解与节奏把控，会把零散知识点串成体系，并在讨论里引导方向、把握深浅。',
}

export const PRESET_ROLES: AgentRole[] = [
  {
    id: 'assist',
    name: '小助手',
    role: '辅助',
    roleType: 'assistant',
    avatar: '/avatars/assist-2.png',
    color: '#13c2c2',
    voice: 'voxcpm-zh-female-clear',
    persona: '老师的得力助手，负责补充细节、梳理步骤，并在同学卡顿时给出恰到好处的分步提示。',
  },
  {
    id: 'clown',
    name: '气氛组',
    role: '吐槽',
    roleType: 'student',
    avatar: '/avatars/clown-2.png',
    color: '#fa8c16',
    voice: 'voxcpm-zh-male-lively',
    persona: '课堂里的开心果，擅长用生活化的类比把抽象概念讲得妙趣横生，也会适时活跃气氛、化解冷场。',
  },
  {
    id: 'curious',
    name: '好奇宝宝',
    role: '提问',
    roleType: 'student',
    avatar: '/avatars/curious-2.png',
    color: '#52c41a',
    voice: 'voxcpm-zh-male-young',
    persona: '永远在问"为什么"的那一个，喜欢追问概念的边界和例外情况，往往把讨论推向更深入。',
  },
  {
    id: 'note-taker',
    name: '笔记君',
    role: '记录',
    roleType: 'student',
    avatar: '/avatars/note-taker-2.png',
    color: '#2f54eb',
    voice: 'voxcpm-zh-female-calm',
    persona: '认真的记录者，会把零散的要点整理成结构清晰的脉络图，方便同学回看和复习。',
  },
  {
    id: 'thinker',
    name: '杠精同学',
    role: '质疑',
    roleType: 'student',
    avatar: '/avatars/thinker-2.png',
    color: '#eb2f96',
    voice: 'voxcpm-zh-male-deep',
    persona: '习惯从反例与边界条件切入思考，喜欢质疑表面结论，锻炼大家的严谨性。',
  },
]
