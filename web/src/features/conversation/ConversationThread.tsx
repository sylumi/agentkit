import { Fragment, useLayoutEffect, useMemo, useRef } from 'react'
import { ChevronRight } from 'lucide-react'
import type { SessionEvent } from '@/api'
import { pairTools, partKey } from '@/history'
import type { PendingMessage, ToolPair } from '@/history'
import type { LiveOutput } from '@/live'
import { Markdown } from '@/shared/ui/Markdown'
import { Thinking } from './Thinking'
import { ResponseActions } from './ResponseActions'
import { hasKnownUsage, usageByInvocation } from './responseUsage'
import { CopyButton } from './CopyButton'
import { formatMessageTime } from '@/lib/time'

function Tool({ pair, busy }: { pair: ToolPair; busy: boolean }) {
  const status = pair.result ? (pair.result.is_error ? 'Failed' : 'Completed') : busy ? 'Waiting for result' : 'No result received'
  return <details className="group/tool my-2 border-l border-edge pl-3">
    <summary className="flex cursor-pointer list-none items-center gap-2 py-1 text-[0.8125rem] text-ink-muted outline-none focus-visible:bg-canvas-sunken">
      <ChevronRight className="size-3.5 transition-transform group-open/tool:rotate-90 motion-reduce:transition-none" />
      <span className="font-mono text-ink-soft">{pair.call.name}</span><span className="ml-auto text-[0.75rem] text-ink-faint">{status}</span>
    </summary>
    <div className="space-y-3 pt-2 pb-1 text-[0.75rem]">
      <div><div className="mb-1 text-ink-faint">Arguments</div><pre className="overflow-auto whitespace-pre-wrap break-words font-mono text-ink-soft">{JSON.stringify(pair.call.arguments, null, 2)}</pre></div>
      {pair.result && <div><div className="mb-1 text-ink-faint">{pair.result.is_error ? 'Tool error' : 'Result'}</div><pre className="max-h-80 overflow-auto whitespace-pre-wrap break-words font-mono text-ink-soft">{pair.result.content}</pre></div>}
    </div>
  </details>
}

function UserText({ text, sentAt }: { text: string; sentAt: string }) {
  return <section className="flex justify-end" data-testid="user-message">
    <div className="flex min-w-0 max-w-[78%] flex-col items-end gap-2 max-md:max-w-[88%]">
      <div className="rounded-[10px] bg-canvas-sunken px-3 py-2 text-[14px] leading-[22px] whitespace-pre-wrap break-words text-ink">{text}</div>
      <div className="-mt-1 flex h-7 items-center justify-end gap-2 px-0.5 text-[0.75rem] leading-4 tabular-nums" data-testid="user-message-actions">
        <time className="mr-1 shrink-0 text-ink-faint" dateTime={sentAt}>{formatMessageTime(sentAt, 'en-US')}</time>
        <CopyButton value={text} className="size-7 rounded-lg hover:bg-surface-active focus-visible:bg-surface-active" />
      </div>
    </div>
  </section>
}

export function ConversationThread({ events, pending, pendingAt, live, busy }: {
  events: SessionEvent[]
  pending?: PendingMessage
  pendingAt: number
  live?: LiveOutput
  busy: boolean
}) {
  const scroll = useRef<HTMLDivElement>(null)
  const following = useRef(true)
  const tools = useMemo(() => pairTools(events), [events])
  const runUsage = useMemo(() => usageByInvocation(events), [events])
  useLayoutEffect(() => {
    const pane = scroll.current
    if (pane && following.current) pane.scrollTop = pane.scrollHeight
  }, [events, pending, live, busy])

  return <main ref={scroll} onScroll={() => {
    const pane = scroll.current
    if (pane) following.current = pane.scrollHeight - pane.scrollTop - pane.clientHeight < 80
  }} aria-label="Conversation" className="min-h-0 flex-1 overflow-x-hidden overflow-y-auto px-4 md:px-6 md:[scrollbar-gutter:stable_both-edges]">
    <div className="mx-auto flex w-full max-w-[750px] flex-col gap-6 py-7">
      {events.map((event, eventIndex) => {
        const parts = event.message?.parts ?? []
        const complete = !!event.stop_reason && event.stop_reason !== 'tool_calls'
        const usage = runUsage.get(event.invocation_id)
        const showUsage = complete && hasKnownUsage(usage)
        const lastTextIndex = parts.findLastIndex(part => part.kind === 'text')
        const responseText = parts.filter(part => part.kind === 'text').map(part => part.text).join('\n\n')
        const actions = complete && <ResponseActions usage={usage} modelName={event.metadata?.model} responseText={responseText} completedAt={event.timestamp} />
        const visible = parts.some((part, index) => part.kind !== 'tool_result' || !tools.results.has(partKey(event, index))) || event.stop_reason === 'length' || event.stop_reason === 'blocked' || showUsage
        return <Fragment key={event.id}>
          {pending !== undefined && eventIndex === pendingAt && <UserText text={pending.text} sentAt={pending.sentAt} />}
          {visible && <article className="min-w-0" aria-label={event.message?.role === 'user' ? 'Your message' : event.author}>
            {parts.map((part, index) => <Fragment key={index}>
              {part.kind === 'text' && (event.message?.role === 'user' ? <UserText text={part.text} sentAt={event.timestamp} /> : <section className="my-3 animate-[fade-in_160ms_ease-out]" data-testid="assistant-message"><Markdown source={part.text} />{index === lastTextIndex && actions}</section>)}
              {part.kind === 'thinking' && <Thinking text={part.thinking.text} kind={part.thinking.kind} />}
              {part.kind === 'tool_call' && <Tool pair={tools.calls.get(partKey(event, index))!} busy={busy} />}
              {part.kind === 'tool_result' && !tools.results.has(partKey(event, index)) && <details className="my-2 text-[0.8125rem] text-ink-muted"><summary className="cursor-pointer">Unmatched tool result{part.tool_result.is_error ? ' · Failed' : ''}</summary><pre className="mt-2 overflow-auto whitespace-pre-wrap break-words">{part.tool_result.content}</pre></details>}
            </Fragment>)}
            {(event.stop_reason === 'length' || event.stop_reason === 'blocked') && <p className="mt-2 text-[0.8125rem] text-ink-muted">{event.stop_reason === 'length' ? 'The model reached its output limit.' : 'The model blocked this response.'}</p>}
            {lastTextIndex < 0 && actions}
          </article>}
        </Fragment>
      })}
      {pending !== undefined && pendingAt >= events.length && <UserText text={pending.text} sentAt={pending.sentAt} />}
      {live && <article aria-label="Live response" className="min-w-0">
        {live.parts.map((part, index) => <Fragment key={`${live.eventID}:${index}`}>
          {part.kind === 'thinking' && <Thinking text={part.text} kind={part.thinkingKind} streaming={busy && !part.ended} />}
          {part.kind === 'text' && <section className="my-3 animate-[fade-in_160ms_ease-out]" data-testid="assistant-message"><Markdown source={part.text} /></section>}
          {part.kind === 'tool_call' && <details className="my-2 border-l border-edge pl-3 text-[0.8125rem] text-ink-muted"><summary className="cursor-pointer py-1">{part.name || 'Tool call'} · Preparing</summary><pre className="mt-2 overflow-auto font-mono text-[0.75rem] whitespace-pre-wrap break-words">{part.arguments}</pre></details>}
        </Fragment>)}
        {live.interrupted && <p className="mt-2 text-[0.75rem] text-ink-faint">Interrupted response · not saved to history</p>}
      </article>}
      {busy && <p role="status" className="text-[0.8125rem] text-ink-faint">{live ? 'Generating…' : 'Waiting for the agent…'}</p>}
    </div>
  </main>
}
