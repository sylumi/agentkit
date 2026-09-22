import { useEffect, useRef, useState } from 'react'
import { api, APIError } from './api'
import type { AgentInfo, SessionEvent, SessionSnapshot, SessionSummary } from './api'
import { savedSubmission, sessionTitle } from './history'
import type { PendingMessage, Submission } from './history'
import { applyPartialEvent } from './live'
import type { LiveOutput } from './live'

type Phase = 'loading' | 'idle' | 'running' | 'stopping' | 'syncing' | 'unsynced'
const selectionKey = 'agentkit-session'

function remember(id?: string) {
  try { if (id) localStorage.setItem(selectionKey, id); else localStorage.removeItem(selectionKey) } catch { /* Storage is optional. */ }
}

function remembered() {
  try { return localStorage.getItem(selectionKey) } catch { return null }
}

const message = (error: unknown) => error instanceof Error ? error.message : 'Could not connect to the backend.'

function pause(signal: AbortSignal) {
  return new Promise<void>((resolve, reject) => {
    const abort = () => { clearTimeout(timer); reject(signal.reason) }
    const timer = setTimeout(() => { signal.removeEventListener('abort', abort); resolve() }, 400)
    if (signal.aborted) abort()
    else signal.addEventListener('abort', abort, { once: true })
  })
}

export function useSession() {
  const [agent, setAgent] = useState<AgentInfo>()
  const [sessions, setSessions] = useState<SessionSummary[]>([])
  const [titles, setTitles] = useState<Record<string, string>>({})
  const [current, setCurrent] = useState<SessionSnapshot>()
  const [draft, setDraftValue] = useState('')
  const [pending, setPending] = useState<PendingMessage>()
  const [live, setLive] = useState<LiveOutput>()
  const [phase, setPhaseValue] = useState<Phase>('loading')
  const [error, setError] = useState<string>()
  const [ready, setReady] = useState(false)
  const currentRef = useRef<SessionSnapshot | undefined>(undefined)
  const drafts = useRef(new Map<string, string>())
  const submission = useRef<Submission | undefined>(undefined)
  const phaseRef = useRef<Phase>('loading')
  const sequence = useRef(0)
  const controller = useRef<AbortController | undefined>(undefined)
  const runController = useRef<AbortController | undefined>(undefined)
  const liveRef = useRef<LiveOutput | undefined>(undefined)

  function showLive(output?: LiveOutput) { liveRef.current = output; setLive(output) }

  function phaseTo(next: Phase) { phaseRef.current = next; setPhaseValue(next) }
  function setDraft(value: string) {
    drafts.current.set(currentRef.current?.id ?? '', value)
    setDraftValue(value)
  }
  function show(snapshot?: SessionSnapshot) {
    currentRef.current = snapshot
    setCurrent(snapshot)
    remember(snapshot?.id)
    setDraftValue(drafts.current.get(snapshot?.id ?? '') ?? '')
    if (snapshot) {
      const { events, ...summary } = snapshot
      setTitles(previous => ({ ...previous, [snapshot.id]: sessionTitle(events) }))
      setSessions(previous => [summary, ...previous.filter(item => item.id !== summary.id)].sort((a, b) => b.updated_at.localeCompare(a.updated_at)))
    }
  }
  function begin(next: Phase) {
    controller.current?.abort()
    controller.current = new AbortController()
    const token = ++sequence.current
    phaseTo(next)
    setError(undefined)
    return { token, signal: controller.current.signal }
  }
  const valid = (token: number) => token === sequence.current

  async function sync(id: string, token: number, signal: AbortSignal) {
    // Cancellation can precede the final storage commit. Wait for the server's flag.
    for (let attempt = 0; attempt < 25; attempt++) {
      const snapshot = await api.session(id, signal)
      if (!valid(token)) return
      show(snapshot)
      if (snapshot.events.some(event => event.id === liveRef.current?.eventID)) showLive(undefined)
      if (submission.current?.sessionID === id && savedSubmission(snapshot.events, submission.current)) setPending(undefined)
      if (!snapshot.running) {
        const sent = submission.current
        if (sent?.sessionID === id) {
          setDraft(savedSubmission(snapshot.events, sent) ? '' : sent.text)
          submission.current = undefined
          setPending(undefined)
        }
        phaseTo('idle')
        return
      }
      await pause(signal)
    }
    throw new Error('The session is still running. Refresh its status before continuing.')
  }

  function syncFailure(cause: unknown, token: number) {
    if (!valid(token)) return
    if (cause instanceof APIError && cause.status === 404) {
      const text = submission.current?.text ?? drafts.current.get(currentRef.current?.id ?? '') ?? ''
      submission.current = undefined
      showLive(undefined)
      setPending(undefined)
      const missingID = currentRef.current?.id ?? remembered()
      show(undefined)
      setDraft(text)
      setSessions(previous => previous.filter(item => item.id !== missingID))
      setError('This session no longer exists. Your unsent draft is available in a new session.')
      phaseTo('idle')
    } else {
      setError(`${message(cause)} History is not synchronized. Refresh status before sending again.`)
      phaseTo('unsynced')
    }
  }

  async function refresh(initial = false) {
    if (!initial && !['idle', 'unsynced'].includes(phaseRef.current)) return
    const { token, signal } = begin('loading')
    try {
      const [info, listed] = await Promise.all([api.agent(signal), api.sessions(signal)])
      if (!valid(token)) return
      setAgent(info); setSessions(listed.sessions); setReady(true)
      const rememberedID = remembered()
      const candidateID = currentRef.current?.id ?? rememberedID
      if (candidateID && listed.sessions.some(item => item.id === candidateID)) {
        phaseTo('syncing')
        await sync(candidateID, token, signal)
      } else {
        if (rememberedID && !currentRef.current) remember(undefined)
        if (currentRef.current?.id) {
          syncFailure(new APIError(404, 'session_not_found', 'Session not found.'), token)
        } else {
          show(undefined)
          phaseTo('idle')
        }
      }
    } catch (cause) {
      if (!valid(token)) return
      if (currentRef.current || submission.current) syncFailure(cause, token)
      else { setError('Cannot reach the backend. Start the Go API, then reconnect.'); setReady(false); phaseTo('idle') }
    }
  }

  useEffect(() => {
    void refresh(true)
    return () => { sequence.current++; controller.current?.abort(); runController.current?.abort() }
    // This effect owns the network lifetime; all later actions use refs and tokens.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  async function select(id: string) {
    if (phaseRef.current !== 'idle' || id === currentRef.current?.id) return
    const { token, signal } = begin('loading')
    try {
      const snapshot = await api.session(id, signal)
      if (!valid(token)) return
      showLive(undefined)
      show(snapshot)
      if (snapshot.running) { phaseTo('syncing'); await sync(id, token, signal) }
      else phaseTo('idle')
    } catch (cause) {
      if (!valid(token)) return
      if (currentRef.current?.id === id) syncFailure(cause, token)
      else { setError(message(cause)); phaseTo('idle') }
    }
  }

  function newSession() {
    if (phaseRef.current !== 'idle') return
    setPending(undefined); setError(undefined); showLive(undefined); show(undefined)
  }

  async function remove(id: string) {
    if (phaseRef.current !== 'idle') return
    const { token, signal } = begin('loading')
    try {
      await api.remove(id, signal)
      if (!valid(token)) return
      drafts.current.delete(id)
      if (currentRef.current?.id === id) { showLive(undefined); show(undefined) }
      setSessions(previous => previous.filter(item => item.id !== id))
      phaseTo('idle')
    } catch (cause) {
      if (!valid(token)) return
      setError(message(cause)); phaseTo('idle')
    }
  }

  async function send() {
    if (phaseRef.current !== 'idle' || !ready) return
    const text = drafts.current.get(currentRef.current?.id ?? '') ?? ''
    if (!text.trim() || [...text].length > 4000) return
    const { token, signal } = begin('running')
    showLive(undefined)
    let id = currentRef.current?.id
    try {
      if (!id) {
        const created = await api.create(signal)
        if (!valid(token)) return
        id = created.id
        drafts.current.set(id, text); drafts.current.delete('')
        show(created)
      }
      submission.current = { sessionID: id, text, previousIDs: new Set(currentRef.current?.events.map(event => event.id)) }
      setPending({ text, sentAt: new Date().toISOString() })
      runController.current = controller.current
      const outcome = await api.run(id, text, signal, (event: SessionEvent) => {
        const snapshot = currentRef.current
        if (!valid(token) || !snapshot || snapshot.id !== id) return
        if (event.partial) {
          showLive(applyPartialEvent(liveRef.current, event))
          return
        }
        if (!snapshot.events.some(saved => saved.id === event.id)) {
          const next = { ...snapshot, running: true, events: [...snapshot.events, event] }
          currentRef.current = next; setCurrent(next)
        }
        if (event.id === liveRef.current?.eventID) showLive(undefined)
      })
      if (valid(token) && outcome.status !== 'finished') setError(outcome.error?.message ?? 'Run stopped.')
    } catch (cause) {
      if (!valid(token)) return
      setError(signal.aborted ? 'Run stopped.' : message(cause))
    } finally {
      if (valid(token)) {
        if (liveRef.current) showLive({ ...liveRef.current, interrupted: true })
        runController.current = undefined
        if (id) {
          phaseTo('syncing')
          controller.current = new AbortController()
          try { await sync(id, token, controller.current.signal) } catch (cause) { syncFailure(cause, token) }
        } else phaseTo('idle')
      }
    }
  }

  function stop() {
    if (phaseRef.current !== 'running') return
    phaseTo('stopping')
    controller.current?.abort()
  }

  return {
    agent, sessions, titles, current, draft, pending, live, phase, error, ready,
    pendingAt: submission.current?.previousIDs.size ?? 0,
    busy: phase !== 'idle', setDraft, refresh: () => void refresh(),
    select: (id: string) => void select(id), remove: (id: string) => void remove(id),
    newSession, send: () => void send(), stop,
  }
}
