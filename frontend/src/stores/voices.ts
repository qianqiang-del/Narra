/**
 * 音色目录 + 试听。
 *
 * 目录部分与 `stores/roles.ts` 规则相同：「谁需要谁调 `load()`」，load 幂等，
 * 多处同时调用只会发一个请求。试听也放这里，因为播放器必须全局只有一个。
 */
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { ApiError } from '@/api/client'
import { fetchVoices, previewVoice } from '@/api/voices'
import type { VoiceItem } from '@/types/voice'

/**
 * 播放器放在模块级而不是 state 里：它是 DOM 对象，不需要响应式，而且「同时只响一个」
 * 得靠一个共享实例保证。
 */
let audio: HTMLAudioElement | null = null

/**
 * 每次「开始一次试听」就 +1，用来作废在途的那一次。
 *
 * 合成是秒级的。用户点了播放又立刻关掉面板之后，请求回来时如果照样播，声音会凭空
 * 响起来，而界面上已经没有停止按钮了。
 */
let previewToken = 0

export const useVoicesStore = defineStore('voices', () => {
  const voices = ref<VoiceItem[]>([])
  const loading = ref(false)
  const error = ref<ApiError | null>(null)
  /** 是否成功拉到过。失败时为 false，所以重试不需要额外的 force 参数 */
  const loaded = ref(false)

  /** pill 上显示中文名，但 v-model 绑的是 id，所以要能反查 */
  const nameById = computed(() => new Map(voices.value.map((v) => [v.id, v.name])))

  /** 查不到就退回 id 本身——音色被后端下架时，至少还看得出当初选的是哪个 */
  function nameOf(id: string): string {
    return nameById.value.get(id) ?? id
  }

  /** 在途请求。并发的 load() 共享同一个 Promise，避免重复打接口 */
  let inflight: Promise<void> | null = null

  async function run() {
    loading.value = true
    error.value = null
    try {
      voices.value = await fetchVoices()
      loaded.value = true
    } catch (e) {
      error.value = e instanceof ApiError ? e : new ApiError(-1, String(e))
    } finally {
      loading.value = false
    }
  }

  function load(): Promise<void> {
    if (loaded.value) return Promise.resolve()
    if (!inflight) inflight = run().finally(() => (inflight = null))
    return inflight
  }

  // ===== 试听 =====

  /** 音色 id -> data URL。试听文案是固定的，合成过一次就不用再合成 */
  const previewUrls = ref<Record<string, string>>({})
  /** 正在合成的音色，null 表示没有 */
  const previewLoadingId = ref<string | null>(null)
  /** 正在播放的音色，null 表示没有 */
  const playingId = ref<string | null>(null)

  /**
   * 停掉试听。带 id 时只在「正在合成或播放的正是它」时才停——面板里每个角色一行，
   * 卸载时挨个触发，不加这个判断的话先卸的那行会把别的行正在放的声音掐掉。
   */
  function stopPreview(id?: string) {
    if (id !== undefined && playingId.value !== id && previewLoadingId.value !== id) return

    previewToken += 1
    previewLoadingId.value = null
    if (audio) {
      audio.pause()
      audio = null
    }
    playingId.value = null
  }

  /** 点播放键：同一个音色再点是停止，换一个音色则打断前一个。 */
  async function togglePreview(id: string) {
    if (playingId.value === id) {
      stopPreview()
      return
    }

    stopPreview()
    const token = (previewToken += 1)
    previewLoadingId.value = id

    try {
      let url = previewUrls.value[id]
      if (!url) {
        url = await previewVoice(id)
        previewUrls.value[id] = url
      }
      // 合成回来时可能已经不是用户要的那一次了
      if (token !== previewToken) return

      const player = new Audio(url)
      player.onended = () => {
        if (audio === player) stopPreview()
      }
      audio = player
      playingId.value = id
      await player.play()
    } catch (e) {
      if (token === previewToken) stopPreview()
      throw e
    } finally {
      // 比 id 而不是比 token：期间的 stopPreview 已经清过一次了，别把它重新盖上
      if (previewLoadingId.value === id) previewLoadingId.value = null
    }
  }

  return {
    voices,
    loading,
    error,
    loaded,
    nameOf,
    load,
    previewLoadingId,
    playingId,
    togglePreview,
    stopPreview,
  }
})
