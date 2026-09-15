import type { Role, RoleType } from '@/types/role'

import { request } from './client'

/** 后端 `service.RoleItem` 的原样形状。下划线命名只在本文件出现 */
interface RoleDTO {
  agent_key: string
  name: string
  role: string
  role_type: RoleType
  persona: string
  avatar: string
  color: string
  /** 注意是 voice_id，不是 voice */
  voice_id: string
  sort_order: number
}

/**
 * 拉取整个角色池。
 *
 * 后端只返回 `enabled = true` 的角色，并且已经按 `sort_order` 升序排好——**别在这里重排**，
 * 教师取第一个就靠这个顺序。
 *
 * 这个接口不下发提示词（提示词不在表里，按 role_type 从 markdown 拼），也没有 id / enabled /
 * 时间戳：对外身份就是 `agent_key`。
 */
export async function fetchRoles(): Promise<Role[]> {
  const list = await request<RoleDTO[]>('/roles')
  return list.map((d) => ({
    id: d.agent_key,
    name: d.name,
    role: d.role,
    roleType: d.role_type,
    avatar: d.avatar,
    color: d.color,
    voice: d.voice_id,
    persona: d.persona,
  }))
}
