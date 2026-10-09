let activeAudio: HTMLAudioElement | null = null
let activeVolume = 1
let activeRate = 1

/**
 * 把后端返回的音频地址解析成 <audio> 能直接用的 src。
 *
 * 兼容两种形态：对象存储部署返回完整 URL（http/https），原样使用；
 * 本地部署返回相对路径（<课堂ID>/<段落>.wav），补 /audio 静态路由前缀。
 */
export function resolveAudioSrc(path: string): string {
  const normalized = path.trim().replaceAll('\\', '/')
  if (/^https?:\/\//i.test(normalized)) return normalized
  return `/audio/${normalized.replace(/^\/+/, '')}`
}

export function stopActiveAudio() {
  if (!activeAudio) return
  activeAudio.pause()
  activeAudio.currentTime = 0
  activeAudio.removeAttribute('src')
  activeAudio.load()
  activeAudio = null
}

/** 暂停当前音频但保留 src 与 currentTime，讨论结束后可从原位置继续。 */
export function pauseActiveAudio() {
  activeAudio?.pause()
}

export function registerAudio(audio: HTMLAudioElement) {
  if (activeAudio && activeAudio !== audio) stopActiveAudio()
  activeAudio = audio
  audio.volume = activeVolume
  audio.playbackRate = activeRate
}

export function unregisterAudio(audio: HTMLAudioElement) {
  if (activeAudio === audio) activeAudio = null
}

export function getActiveAudio() {
  return activeAudio
}

export function setActiveVolume(volume: number) {
  activeVolume = volume
  if (activeAudio) activeAudio.volume = volume
}

export function setActiveRate(rate: number) {
  activeRate = rate
  if (activeAudio) activeAudio.playbackRate = rate
}
