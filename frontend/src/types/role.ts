/**
 * 角色池类型。
 *
 * 角色是**数据不是常量**：整个池子由后端 `GET /api/v1/roles` 提供（数据库 preset_agents 表，
 * 人工维护），前端每次渲染重新拉。所以这里只有类型，没有字面量——原来的 `data/agents.ts`
 * 里那 6 个写死的角色已经不在了。
 */

/** 角色大类，对应后端 preset_agents.role_type 的 CHECK */
export type RoleType = 'teacher' | 'assistant' | 'student'

export interface Role {
  /** 稳定标识，前后端契约（后端 `agent_key`）。改它等于换了一个角色 */
  id: string
  name: string
  /** 展示定位，如「主讲」「质疑」 */
  role: string
  roleType: RoleType
  avatar: string
  /** 选中时的光环色 / 信息卡徽章底色 */
  color: string
  /** 默认音色 id（后端 `voice_id`） */
  voice: string
  /** 人设与说话风格。既是前端信息卡正文，也是后端提示词里的 `{{persona}}` */
  persona: string
}

/** AgentBar 的两种取角色方式：手选 / 让大模型从池子里挑 */
export type AgentBarMode = 'preset' | 'auto'
