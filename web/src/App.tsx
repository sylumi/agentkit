import { useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { Monitor, Moon, PanelLeft, Sun, X } from 'lucide-react'
import { AppSidebar } from './app/AppSidebar'
import { Composer } from './features/composer/Composer'
import { ConversationHome } from './features/conversation/ConversationHome'
import { SidebarToggleButton } from './shared/ui/SidebarToggleButton'
import { cn } from './lib/utils'
import { useSession } from './useSession'
import { pairTools } from './history'
import { ConversationThread } from './features/conversation/ConversationThread'

type Theme = 'light' | 'dark' | 'system'
type Panel = 'settings' | 'model'

function Dialog({ title, children, onClose }: { title: string; children: ReactNode; onClose: () => void }) {
  const ref = useRef<HTMLDialogElement>(null)
  useEffect(() => { ref.current?.showModal() }, [])
  return (
    <dialog ref={ref} onCancel={onClose} onClose={onClose} aria-labelledby="panel-title" className="m-auto max-h-[calc(100dvh-2rem)] w-[min(560px,calc(100vw-2rem))] overflow-auto rounded-2xl border border-edge bg-canvas p-5 text-ink shadow-xl backdrop:bg-scrim/20">
      <div className="mb-4 flex items-center justify-between gap-4">
        <h2 id="panel-title" className="text-base font-medium">{title}</h2>
        <button type="button" className="grid size-7 cursor-pointer place-items-center rounded-md text-ink-muted hover:bg-canvas-sunken focus-visible:bg-canvas-sunken" aria-label="Close" onClick={onClose}><X className="size-4" /></button>
      </div>
      {children}
    </dialog>
  )
}

export function App() {
  const chat = useSession()
  const [sidebarOpen, setSidebarOpen] = useState(true)
  const [mobileOpen, setMobileOpen] = useState(false)
  const [panel, setPanel] = useState<Panel>()
  const mobileRef = useRef<HTMLDialogElement>(null)
  const [theme, setTheme] = useState<Theme>(() => {
    const value = document.documentElement.dataset.theme
    return value === 'dark' || value === 'light' ? value : 'system'
  })

  useEffect(() => {
    document.documentElement.dataset.theme = theme
    try { localStorage.setItem('agentkit-theme', theme) } catch { /* Theme still applies for this tab. */ }
  }, [theme])

  useEffect(() => {
    const dialog = mobileRef.current
    if (mobileOpen) dialog?.showModal()
    else dialog?.close()
  }, [mobileOpen])

  useEffect(() => {
    const media = matchMedia('(min-width: 768px)')
    const resize = () => { if (media.matches) setMobileOpen(false) }
    media.addEventListener('change', resize)
    return () => media.removeEventListener('change', resize)
  }, [])

  const openPanel = (next: Panel) => { setMobileOpen(false); setPanel(next) }
  const newSession = () => { chat.newSession(); setMobileOpen(false) }
  const events = chat.current?.events ?? []
  const incomplete = !chat.busy && pairTools(events).incomplete
  const tooLong = [...chat.draft].length > 4000
  const title = chat.current ? (chat.titles[chat.current.id] ?? 'Session') : 'New session'
  const status = chat.phase === 'running' ? 'Agent is running' : chat.phase === 'stopping' ? 'Stopping…'
    : chat.phase === 'syncing' ? 'Synchronizing history…' : chat.phase === 'unsynced' ? 'History needs to be synchronized'
    : chat.phase === 'loading' ? 'Connecting…' : !chat.ready ? 'Backend not connected'
    : tooLong ? 'Messages can contain up to 4,000 characters' : 'Enter to send · Shift+Enter for a new line'
  const composer = <Composer value={chat.draft} onChange={chat.setDraft}
    onOpenModel={() => openPanel('model')}
    onSend={chat.send} onStop={chat.stop} busy={chat.busy}
    canSend={chat.ready && !chat.busy && !incomplete && !tooLong && !!chat.draft.trim()}
    canStop={chat.phase === 'running'} model={chat.agent?.model}
    status={status} />
  const notice = (chat.error || incomplete) && <div role="status" className="mx-auto mb-3 w-full max-w-[750px] text-[0.8125rem] leading-5 text-ink-muted">
    <p>{incomplete ? 'A tool call has no result. Start a new session to continue.' : chat.error}</p>
    {incomplete ? <button type="button" className="mt-1 cursor-pointer underline underline-offset-4" onClick={newSession}>New session</button>
      : (chat.phase === 'idle' || chat.phase === 'unsynced') && <button type="button" className="mt-1 cursor-pointer underline underline-offset-4" onClick={chat.refresh}>{chat.ready ? 'Refresh status' : 'Reconnect'}</button>}
  </div>
  const sidebar = (mobile = false) => <AppSidebar
    onCollapse={() => mobile ? setMobileOpen(false) : setSidebarOpen(false)}
    onNewSession={newSession}
    onOpenSettings={() => openPanel('settings')}
    sessions={chat.sessions} titles={chat.titles} selected={chat.current?.id} busy={chat.busy}
    onSelect={id => { chat.select(id); setMobileOpen(false) }} onDelete={chat.remove}
  />

  return (
    <div className={cn('grid h-dvh min-h-0 grid-cols-[minmax(0,1fr)] overflow-hidden bg-canvas', sidebarOpen && 'md:grid-cols-[240px_minmax(0,1fr)]')}>
      {sidebarOpen && <div className="hidden min-h-0 md:block">{sidebar()}</div>}
      <div className="relative flex h-full min-h-0 min-w-0 flex-col overflow-hidden" data-testid="conversation-pane">
        <header className="conversation-header window-titlebar z-20 flex h-[45px] shrink-0 items-center gap-3 border-b border-edge/80 bg-canvas py-0 pr-2 pl-6 max-md:h-12 max-md:px-2 max-md:pl-4">
          {!sidebarOpen && <SidebarToggleButton expanded={false} className="hidden md:grid" onToggle={() => setSidebarOpen(true)} />}
          <div className="conversation-title-group flex min-w-0 flex-1 select-none items-center gap-2.5">
            <button type="button" className="-ml-1 grid size-7 shrink-0 cursor-pointer place-items-center rounded-md text-ink-muted hover:bg-canvas-sunken md:hidden" aria-label="Open sessions" onClick={() => setMobileOpen(true)}><PanelLeft className="size-4" /></button>
            <span className="truncate text-[0.9375rem] font-medium tracking-[-0.015em] text-ink">{title}</span>
          </div>
        </header>
        <div className="relative flex min-h-0 flex-1 flex-col">
          {!events.length && chat.pending === undefined && !chat.live ? <ConversationHome title="What should we work on?" description="Start a conversation with your agent.">
            <div className="w-full">{notice}{composer}</div>
          </ConversationHome> : <>
            <ConversationThread key={chat.current?.id} events={events} pending={chat.pending} pendingAt={chat.pendingAt} live={chat.live} busy={chat.phase === 'running'} />
            <div className="shrink-0 px-4 pt-3 pb-4 md:px-6">{notice}{composer}</div>
          </>}
        </div>
      </div>
      <dialog ref={mobileRef} onCancel={() => setMobileOpen(false)} onClose={() => setMobileOpen(false)} aria-label="Sessions" className="fixed inset-y-0 left-0 m-0 h-dvh max-h-none w-[17.5rem] max-w-[88vw] border-0 bg-canvas p-0 text-ink backdrop:bg-scrim/15 backdrop:backdrop-blur-[1px]">
        {sidebar(true)}
      </dialog>
      {panel && <Dialog title={panel === 'model' ? 'Model' : 'Settings'} onClose={() => setPanel(undefined)}>
        {panel === 'settings' && <div className="flex items-center justify-between gap-4 text-[0.875rem]">
          <span>Appearance</span>
          <div className="flex gap-1" role="group" aria-label="Color theme">
            {([['light', Sun], ['dark', Moon], ['system', Monitor]] as const).map(([value, Icon]) => <button key={value} type="button" aria-label={`${value} theme`} aria-pressed={theme === value} onClick={() => setTheme(value)} className={cn('grid size-8 cursor-pointer place-items-center rounded-md text-ink-muted hover:bg-canvas-sunken', theme === value && 'bg-surface-selected text-ink')}><Icon className="size-4" /></button>)}
          </div>
        </div>}
        {panel === 'model' && <dl className="grid grid-cols-[auto_1fr] gap-x-5 gap-y-3 text-[0.875rem]"><dt className="text-ink-muted">Agent</dt><dd>{chat.agent?.name || 'Unavailable'}</dd><dt className="text-ink-muted">Model</dt><dd>{chat.agent?.model || 'Not provided'}</dd></dl>}
      </Dialog>}
    </div>
  )
}
