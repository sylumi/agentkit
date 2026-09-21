import type { SessionEvent } from './api'

export interface LivePart {
  kind: 'text' | 'thinking' | 'tool_call'
  text: string
  thinkingKind?: string
  callID: string
  name: string
  arguments: string
  ended: boolean
}

export interface LiveOutput {
  eventID: string
  invocationID: string
  parts: LivePart[]
  interrupted?: boolean
}

// Preview state never enters Session history. A saved event replaces it by ID.
export function applyPartialEvent(previous: LiveOutput | undefined, event: Extract<SessionEvent, { partial: true }>): LiveOutput {
  const update = event.delta
  const output = previous?.eventID === event.id ? { ...previous, parts: [...previous.parts] }
    : { eventID: event.id, invocationID: event.invocation_id, parts: [] }
  const index = update.data.index
  if (update.type === 'part_start') {
    if (index !== output.parts.length || !['text', 'thinking', 'tool_call'].includes(update.data.kind)) throw new Error('Invalid model block order.')
    output.parts.push({ kind: update.data.kind, thinkingKind: update.data.thinking_kind, text: '', callID: '', name: '', arguments: '', ended: false })
    return output
  }
  const part = output.parts[index]
  if (!part || part.ended) throw new Error('Model update has no open block.')
  const next = { ...part }
  if (update.type === 'part_end') next.ended = true
  else if (update.type === 'tool_call_delta') {
    if (part.kind !== 'tool_call') throw new Error('Invalid tool block update.')
    next.callID += update.data.id ?? ''
    next.name += update.data.name ?? ''
    next.arguments += update.data.arguments ?? ''
  } else {
    if (typeof update.data.delta !== 'string' || part.kind !== (update.type === 'text_delta' ? 'text' : 'thinking')) throw new Error('Invalid text block update.')
    next.text += update.data.delta
  }
  output.parts[index] = next
  return output
}
