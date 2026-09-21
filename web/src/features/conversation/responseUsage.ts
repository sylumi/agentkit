import type { SessionEvent } from '@/api'

export type Usage = NonNullable<SessionEvent['usage']>

export function hasKnownUsage(usage?: Usage): usage is Usage {
  return !!usage && Object.values(usage).some(value => typeof value === 'number')
}

// The desktop's response footer reports the whole run, including tool rounds.
// A missing count in any model call makes that run's corresponding total unknown.
export function usageByInvocation(events: SessionEvent[]): Map<string, Usage> {
  const totals = new Map<string, Usage>()
  for (const event of events) {
    if (!event.stop_reason) continue
    const previous = totals.get(event.invocation_id)
    const current = event.usage ?? {}
    if (!previous) {
      totals.set(event.invocation_id, { ...current })
      continue
    }
    const next: Usage = {}
    for (const key of ['input_tokens', 'output_tokens', 'cached_input_tokens', 'reasoning_output_tokens'] as const) {
      if (previous[key] !== undefined && current[key] !== undefined) next[key] = previous[key] + current[key]
    }
    totals.set(event.invocation_id, next)
  }
  return totals
}
