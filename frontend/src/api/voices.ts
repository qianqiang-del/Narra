import type { VoiceItem } from '@/types/voice'

import { request } from './client'

/** 后端 `dto.VoicePreview` 的原样形状 */
interface VoicePreviewDTO {
  voice_id: string
  format: string
  /** base64 编码的音频 */
  audio: string
}

/**
 * 拉取全部可选音色。
 *
 * 不套 DTO：后端返回的就是 `{id, name, gender}`，和 `VoiceItem` 完全一致，没有
 * `roles.ts` 那种下划线命名的映射可做。
 */
export async function fetchVoices(): Promise<VoiceItem[]> {
  return await request<VoiceItem[]>('/voices')
}

/**
 * 试听一个音色，返回可以直接喂给 `new Audio()` 的 data URL。
 *
 * 音频走的是常规 JSON 信封、base64 塞在里面（不是裸字节流），所以这里拼出
 * data URL，组件侧不用知道格式细节。合成要一两秒，调用方得有加载态。
 */
export async function previewVoice(id: string): Promise<string> {
  const dto = await request<VoicePreviewDTO>(`/voices/${encodeURIComponent(id)}/preview`)
  return `data:audio/${dto.format};base64,${dto.audio}`
}
