/**
 * 课堂页共享类型。
 */

export interface ChatSession {
  id: string
  title: string
  type: 'qa' | 'discussion' | 'lecture'
  preview: string
  active?: boolean
}

export interface ChatNote {
  id: string
  title: string
  body: string
}

export interface ClassroomStats {
  scenes: number
  minutes: number
  agents: number
  messages: number
}

/** 圆桌区一条发言气泡 */
export interface Bubble {
  id: string
  from: 'user' | 'agent' | 'teacher'
  name?: string
  text: string
}

/** 圆桌参与者（教师 / 学员）—— 用于头像与信息卡展示 */
export interface Participant {
  id: string
  /** 显示名 */
  name: string
  /** 角色大类：教师 / 助教 / 学生（信息卡徽章） */
  roleType: 'teacher' | 'assistant' | 'student'
  /** 角色定位小字（辅助 / 吐槽 / 提问 ...） */
  role?: string
  avatar: string
  /** 标识色（徽章底色 / 头像高亮描边） */
  color: string
  /** 人设简介（信息卡正文；真实场景由后端按 id 返回） */
  persona?: string
}
