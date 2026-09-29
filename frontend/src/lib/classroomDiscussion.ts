import type { ConversationEvent, ConversationMessage } from '../api/conversation'
import type { Bubble } from '../types/classroom'

type Speaker = { name: string; role: string }

export interface DiscussionDisplay {
  bubbles: Bubble[]
  lastSequence: number
  thinking: boolean
  speaking: 'teacher' | 'agent' | null
  yourTurn: boolean
  participants: Map<number, Speaker>
  turns: Map<number, Speaker>
  streamingIds: Set<string>
}

function speakerFromSnapshot(snapshot: Record<string, unknown> | null | undefined): Speaker {
  return {
    name: typeof snapshot?.name === 'string' ? snapshot.name : 'Agent',
    role: typeof snapshot?.role === 'string' ? snapshot.role : '',
  }
}

export function createDiscussionDisplay(history: Pick<ConversationMessage, 'id' | 'senderType' | 'senderSnapshot' | 'content'>[]): DiscussionDisplay {
  return {
    bubbles: history.map((message) => {
      if (message.senderType === 'user') {
        return { id: `message-${message.id}`, from: 'user', text: message.content }
      }
      const speaker = speakerFromSnapshot(message.senderSnapshot)
      return {
        id: `message-${message.id}`,
        from: speaker.role === 'teacher' ? 'teacher' : 'agent',
        name: speaker.name,
        text: message.content,
      }
    }),
    lastSequence: 0,
    thinking: false,
    speaking: null,
    yourTurn: false,
    participants: new Map(),
    turns: new Map(),
    streamingIds: new Set(),
  }
}

export function applyDiscussionEvent(display: DiscussionDisplay, event: ConversationEvent): void {
  if (event.sequenceNo <= display.lastSequence) return
  display.lastSequence = event.sequenceNo

  switch (event.eventType) {
    case 'run.started':
      display.participants = new Map(event.payload.participants.map((item) => [item.agent_id, { name: item.name, role: item.role }]))
      display.thinking = true
      display.yourTurn = false
      break
    case 'agent.started': {
      const speaker = display.participants.get(event.payload.agent_id) ?? { name: event.payload.agent_name, role: '' }
      display.turns.set(event.payload.turn_id, speaker)
      display.speaking = speaker.role === 'teacher' ? 'teacher' : 'agent'
      display.thinking = true
      break
    }
    case 'message.delta': {
      const id = `message-${event.payload.message_id}`
      const existing = display.bubbles.find((bubble) => bubble.id === id)
      if (existing && display.streamingIds.has(id)) existing.text += event.payload.delta
      else if (!existing) {
        const speaker = display.turns.get(event.payload.turn_id) ?? { name: 'Agent', role: '' }
        display.bubbles.push({ id, from: speaker.role === 'teacher' ? 'teacher' : 'agent', name: speaker.name, text: event.payload.delta })
        display.streamingIds.add(id)
      }
      break
    }
    case 'message.completed': {
      const id = `message-${event.payload.message_id}`
      const existing = display.bubbles.find((bubble) => bubble.id === id)
      if (existing) existing.text = event.payload.content
      else {
        const speaker = display.turns.get(event.payload.turn_id) ?? { name: 'Agent', role: '' }
        display.bubbles.push({ id, from: speaker.role === 'teacher' ? 'teacher' : 'agent', name: speaker.name, text: event.payload.content })
      }
      display.streamingIds.delete(id)
      break
    }
    case 'agent.completed':
      display.speaking = null
      break
    case 'run.waiting_user':
      display.yourTurn = true
      display.thinking = false
      display.speaking = null
      break
    case 'run.completed':
      display.thinking = false
      display.speaking = null
      break
    case 'run.failed':
      display.thinking = false
      display.speaking = null
      display.bubbles.push({ id: `run-error-${event.sequenceNo}`, from: 'agent', name: '系统', text: event.payload.error || '讨论执行失败' })
      break
  }
}
