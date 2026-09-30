import assert from 'node:assert/strict'
import test from 'node:test'
import * as playback from '../src/lib/audioPlayback.ts'

test('discussion pause keeps the current lecture audio and its position for manual resume', () => {
  const audio = { currentTime: 12.5, src: '/audio/lecture.mp3', paused: false, pause() { this.paused = true } }
  playback.registerAudio(audio)
  assert.equal(typeof playback.pauseActiveAudio, 'function')
  playback.pauseActiveAudio()
  assert.equal(audio.paused, true)
  assert.equal(audio.currentTime, 12.5)
  assert.equal(audio.src, '/audio/lecture.mp3')
  assert.equal(playback.getActiveAudio(), audio)
  playback.unregisterAudio(audio)
})
