import type { RunDone, SessionEvent } from './api'

// A run POST must never be retried here: it may already have committed input.
export async function readRunStream(stream: ReadableStream<Uint8Array>, onEvent: (event: SessionEvent) => void): Promise<RunDone> {
  const reader = stream.getReader()
  const decoder = new TextDecoder('utf-8', { fatal: true })
  const seen = new Set<string>()
  let buffer = ''
  try {
    while (true) {
      const { value, done } = await reader.read()
      buffer += done ? decoder.decode() : decoder.decode(value, { stream: true })
      let boundary: RegExpExecArray | null
      while ((boundary = /\r?\n\r?\n/.exec(buffer))) {
        const frame = buffer.slice(0, boundary.index)
        buffer = buffer.slice(boundary.index + boundary[0].length)
        let type = ''
        const data: string[] = []
        for (const line of frame.split(/\r?\n/)) {
          if (line.startsWith(':')) continue
          const colon = line.indexOf(':')
          const key = colon < 0 ? line : line.slice(0, colon)
          const value = colon < 0 ? '' : line.slice(colon + 1).replace(/^ /, '')
          if (key === 'event') type = value
          if (key === 'data') data.push(value)
        }
        if (!data.length || !['event', 'done'].includes(type)) continue
        const payload = JSON.parse(data.join('\n'))
        if (type === 'event') {
          if (!payload || typeof payload.id !== 'string' || typeof payload.invocation_id !== 'string') throw new Error('Invalid event received.')
          if (payload.partial === true) {
            if (!Number.isInteger(payload.delta?.data?.index) || payload.delta.data.index < 0
              || !['part_start', 'text_delta', 'thinking_delta', 'tool_call_delta', 'part_end'].includes(payload.delta.type)) throw new Error('Invalid partial event received.')
            onEvent(payload as SessionEvent)
          } else {
            if ((payload.partial !== undefined && payload.partial !== false) || payload.delta != null) throw new Error('Invalid complete event received.')
            if (!seen.has(payload.id)) { seen.add(payload.id); onEvent(payload as SessionEvent) }
          }
        } else {
          if (!payload || !['finished', 'error', 'cancelled'].includes(payload.status)) throw new Error('Invalid run outcome received.')
          return payload as RunDone
        }
      }
      if (done) throw new Error('Connection closed before the run finished. History will be refreshed.')
    }
  } finally {
    await reader.cancel().catch(() => {})
    reader.releaseLock()
  }
}
