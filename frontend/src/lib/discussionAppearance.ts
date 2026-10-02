export type DiscussionStatus = 'speaking' | 'working' | 'waiting' | 'ready'

export function discussionStatus(state: {
  running: boolean
  sending: boolean
  thinking: boolean
  yourTurn: boolean
  speakingName?: string
}): DiscussionStatus {
  if (state.running && state.speakingName) return 'speaking'
  if (state.running || state.sending || state.thinking) return 'working'
  if (state.yourTurn) return 'waiting'
  return 'ready'
}
