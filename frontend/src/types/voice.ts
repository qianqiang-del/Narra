/**
 * 音色目录类型。
 *
 * 音色是后端 `GET /api/v1/voices` 提供的数据（来源是后端 `service/voice_service.go`
 * 里的常量）。这里只有类型，没有字面量——原来的 `data/voices.ts` 已经删了。
 */

/** 音色性别，后端只给这两个值 */
export type VoiceGender = '女' | '男'

export interface VoiceItem {
  /** 送给 TTS 服务的就是它 */
  id: string
  name: string
  gender: VoiceGender
}
