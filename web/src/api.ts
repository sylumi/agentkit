import { readRunStream } from './sse'

export type Part =
  | { kind: 'text'; text: string }
  | { kind: 'thinking'; thinking: { kind?: string; text: string } }
  | { kind: 'tool_call'; tool_call: { id: string; name: string; arguments: unknown } }
  | { kind: 'tool_result'; tool_result: { call_id: string; content: string; is_error: boolean } }

interface EventFields {
  id: string
  invocation_id: string
  author: string
  timestamp: string
  message?: { role: 'user' | 'assistant' | 'tool'; parts: Part[] }
  stop_reason?: string
  usage?: { input_tokens?: number; output_tokens?: number; cached_input_tokens?: number; reasoning_output_tokens?: number }
  metadata?: { provider?: string; model?: string; response_id?: string }
}

export type SessionEvent = EventFields & (
  | { partial: true; delta: ModelDelta }
  | { partial?: false; delta?: never }
)

export interface AgentInfo {
  name: string
  description: string
  model?: string
}

export interface SessionSummary { id: string; updated_at: string; running: boolean }
export interface SessionSnapshot extends SessionSummary { events: SessionEvent[] }
export interface RunDone { status: 'finished' | 'error' | 'cancelled'; error?: { code: string; message: string } }

export type ModelDelta =
  | { type: 'part_start'; data: { index: number; kind: 'text' | 'thinking' | 'tool_call'; thinking_kind?: string } }
  | { type: 'text_delta' | 'thinking_delta'; data: { index: number; delta: string } }
  | { type: 'tool_call_delta'; data: { index: number; id?: string; name?: string; arguments?: string } }
  | { type: 'part_end'; data: { index: number } }

export class APIError extends Error {
  readonly status: number
  readonly code: string
  constructor(status: number, code: string, message: string) {
    super(message)
    this.status = status
    this.code = code
  }
}

async function checked(response: Response): Promise<Response> {
  if (response.ok) return response
  const body = await response.json().catch(() => null)
  throw new APIError(response.status, body?.error?.code ?? 'http_error', body?.error?.message ?? `Request failed (${response.status}).`)
}

async function json<T>(path: string, signal: AbortSignal, method = 'GET', body?: unknown): Promise<T> {
  const response = await checked(await fetch(`/api${path}`, {
    method, signal: AbortSignal.any([signal, AbortSignal.timeout(15_000)]), headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  }))
  return response.json() as Promise<T>
}

const sessionPath = (id: string) => `/sessions/${encodeURIComponent(id)}`

export const api = {
  agent: (signal: AbortSignal) => json<AgentInfo>('/agent', signal),
  sessions: (signal: AbortSignal) => json<{ sessions: SessionSummary[] }>('/sessions', signal),
  session: (id: string, signal: AbortSignal) => json<SessionSnapshot>(sessionPath(id), signal),
  create: (signal: AbortSignal) => json<SessionSnapshot>('/sessions', signal, 'POST'),
  async remove(id: string, signal: AbortSignal) {
    await checked(await fetch(`/api${sessionPath(id)}`, { method: 'DELETE', signal: AbortSignal.any([signal, AbortSignal.timeout(15_000)]) }))
  },
  async run(id: string, text: string, signal: AbortSignal, onEvent: (event: SessionEvent) => void): Promise<RunDone> {
    const response = await checked(await fetch(`/api${sessionPath(id)}/run`, {
      method: 'POST', signal, headers: { 'Content-Type': 'application/json', Accept: 'text/event-stream' }, body: JSON.stringify({ text }),
    }))
    if (!response.headers.get('Content-Type')?.startsWith('text/event-stream') || !response.body) {
      throw new Error('The server did not return an event stream.')
    }
    return readRunStream(response.body, onEvent)
  },
}
