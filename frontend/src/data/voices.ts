/**
 * 音色静态数据（原项目从 VoxCPM 服务动态拉取）。
 *
 * 接入 Go 后端后，替换为：
 *   const res = await fetch('/api/voices').then(r => r.json())
 * 组件侧只消费 VoiceGroup[] 与 voiceName()，接口不用动。
 */
export interface VoiceItem {
  id: string
  name: string
  lang: string
}

export interface VoiceGroup {
  label: string
  voices: VoiceItem[]
}

export const VOICE_GROUPS: VoiceGroup[] = [
  {
    label: 'VoxCPM',
    voices: [
      { id: 'voxcpm-zh-female-warm', name: '温婉女声', lang: '中文' },
      { id: 'voxcpm-zh-female-clear', name: '清亮女声', lang: '中文' },
      { id: 'voxcpm-zh-female-calm', name: '沉静女声', lang: '中文' },
      { id: 'voxcpm-zh-male-lively', name: '活泼男声', lang: '中文' },
      { id: 'voxcpm-zh-male-young', name: '少年音', lang: '中文' },
      { id: 'voxcpm-zh-male-deep', name: '低沉男声', lang: '中文' },
    ],
  },
]

/** 按 id 查音色中文名，查不到就退回 id 本身。 */
export function voiceName(id: string): string {
  for (const g of VOICE_GROUPS) {
    const hit = g.voices.find((v) => v.id === id)
    if (hit) return hit.name
  }
  return id
}
