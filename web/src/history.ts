import type { Part, SessionEvent } from './api'

export type ToolCall = Extract<Part, { kind: 'tool_call' }>['tool_call']
export type ToolResult = Extract<Part, { kind: 'tool_result' }>['tool_result']
export interface ToolPair {
  call: ToolCall
  result?: ToolResult
}

export const partKey = (event: SessionEvent, index: number) => JSON.stringify([event.id, index])

// Match the latest preceding unmatched call within its invocation. IDs can be reused.
export function pairTools(events: SessionEvent[]) {
  const calls = new Map<string, ToolPair>()
  const results = new Set<string>()
  const pending = new Map<string, ToolPair[]>()
  for (const event of events) {
    event.message?.parts.forEach((part, index) => {
      if (part.kind === 'tool_call') {
        const key = JSON.stringify([event.invocation_id, part.tool_call.id])
        const pair: ToolPair = { call: part.tool_call }
        calls.set(partKey(event, index), pair)
        pending.set(key, [...(pending.get(key) ?? []), pair])
      } else if (part.kind === 'tool_result') {
        const key = JSON.stringify([event.invocation_id, part.tool_result.call_id])
        const pair = pending.get(key)?.pop()
        if (pair) {
          pair.result = part.tool_result
          results.add(partKey(event, index))
        }
      }
    })
  }
  return { calls, results }
}

export function sessionTitle(events: SessionEvent[]): string {
  const text = events.find(event => event.message?.role === 'user')?.message?.parts
    .filter(part => part.kind === 'text').map(part => part.text).join(' ')
  return text?.replace(/\s+/g, ' ').trim().slice(0, 64) || 'New session'
}

export interface Submission { sessionID: string; text: string; previousIDs: Set<string> }
export interface PendingMessage { text: string; sentAt: string }

export function savedSubmission(events: SessionEvent[], submission: Submission): boolean {
  return events.some(event => !submission.previousIDs.has(event.id) && event.message?.role === 'user'
    && event.message.parts.some(part => part.kind === 'text' && part.text === submission.text))
}
