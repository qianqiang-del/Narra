/**
 * 用户资料（昵称 / 头像 / 简介）。
 *
 * 全应用共用一份，并同步到 localStorage（键 narra-profile）跨会话保留。
 * 首页的个人资料面板写它、提交生成与课堂圆桌读它——集中在这里，是为了避免
 * 各组件各自解析同一份存档（`narra-profile` 这个键名一旦散落成多处字符串约定，
 * 改一处漏一处就会静默读不到）。
 */
import { defineStore } from 'pinia'
import { computed, ref, watch } from 'vue'

/** 可选头像，都是 `public/avatars` 下真实存在的文件 */
export const AVATAR_OPTIONS = [
  'teacher-2',
  'assist-2',
  'clown-2',
  'curious-2',
  'note-taker-2',
  'thinker-2',
].map((name) => `/avatars/${name}.png`)

const DEFAULT_NAME = '同学'
const STORAGE_KEY = 'narra-profile'

interface Profile {
  name: string
  avatar: string
  bio: string
}

export const useProfileStore = defineStore('profile', () => {
  const profile = ref<Profile>({
    name: DEFAULT_NAME,
    avatar: AVATAR_OPTIONS[0],
    bio: '',
  })

  /** 载入本地存档；首帧同步执行，避免头像先闪默认再跳。 */
  function load() {
    try {
      const raw = localStorage.getItem(STORAGE_KEY)
      if (!raw) return
      const parsed = JSON.parse(raw) as Partial<Profile>
      if (typeof parsed.name === 'string' && parsed.name.trim()) profile.value.name = parsed.name
      if (typeof parsed.avatar === 'string' && parsed.avatar) profile.value.avatar = parsed.avatar
      if (typeof parsed.bio === 'string') profile.value.bio = parsed.bio
    } catch {
      /* 损坏的存档直接忽略 */
    }
  }
  load()

  watch(
    profile,
    (value) => {
      try {
        localStorage.setItem(STORAGE_KEY, JSON.stringify(value))
      } catch {
        /* 隐私模式下写入失败，忽略 */
      }
    },
    { deep: true },
  )

  /** 用于问候语与圆桌展示；名字被清空时回退到默认称呼 */
  const displayName = computed(() => profile.value.name.trim() || DEFAULT_NAME)

  return { profile, displayName, load }
})
