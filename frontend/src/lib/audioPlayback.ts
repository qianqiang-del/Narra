let activeAudio: HTMLAudioElement | null = null
let activeVolume = 1
let activeRate = 1

export function stopActiveAudio() {
  if (!activeAudio) return
  activeAudio.pause()
  activeAudio.currentTime = 0
  activeAudio.removeAttribute('src')
  activeAudio.load()
  activeAudio = null
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
