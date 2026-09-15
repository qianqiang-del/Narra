/**
 * 回放页专用的角色静态数据 —— **临时的，别再往这里加东西**。
 *
 * 角色池已经搬到后端（`GET /api/v1/roles`），首页的 AgentBar 从那儿取，这里不再服务它。
 *
 * 现在这个文件只剩 `PlaybackChrome.vue` 一个消费者，因为回放页要的是「**这堂课**用了哪几个
 * 角色、各自什么音色」——那是**课堂角色快照**，和全局角色池不是一回事，那个接口还没写。
 * 拿 `/roles` 顶替是错的：它会显示整池角色，而不是这堂课实际选中的。
 *
 * 等课堂角色快照接口有了就把这个文件删掉。
 *
 * @deprecated 等课堂角色快照接口
 */
import type { Role } from '@/types/role'

/** 回放页圆桌的占位参与者。顺序即展示顺序 */
export const PRESET_ROLES: Role[] = [
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
    persona: '永远在问“为什么”的那一个，喜欢追问概念的边界和例外情况，往往把讨论推向更深入。',
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
