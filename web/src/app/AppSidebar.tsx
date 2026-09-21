import { Settings, SquarePen, Trash2 } from 'lucide-react'
import type { SessionSummary } from '@/api'
import { cn } from '@/lib/utils'
import { SidebarToggleButton } from '@/shared/ui/SidebarToggleButton'
import { SidebarNavItem } from '@/shared/ui/SidebarNavItem'

export function AppSidebar({ onCollapse, onNewSession, onOpenSettings, sessions, titles, selected, busy, onSelect, onDelete }: {
  onCollapse: () => void
  onNewSession: () => void
  onOpenSettings: () => void
  sessions: SessionSummary[]
  titles: Record<string, string>
  selected?: string
  busy: boolean
  onSelect: (id: string) => void
  onDelete: (id: string) => void
}) {
  return (
    <aside className="app-sidebar relative flex h-full w-[240px] min-h-0 min-w-0 flex-col overflow-hidden border-r border-edge/75 bg-canvas text-ink-soft max-md:w-[17.5rem]" aria-label="Sessions">
      <div className="app-sidebar-header relative h-[45px] w-full shrink-0">
        <SidebarToggleButton expanded className="absolute top-2 right-3" onToggle={onCollapse} />
      </div>
      <div className="min-h-0 flex-1 overflow-x-hidden overflow-y-auto">
        <div className="w-full px-3 pb-3">
          <SidebarNavItem icon={SquarePen} label="New session" onClick={onNewSession} disabled={busy} />
        </div>
        <div className="w-full px-5 pt-2 pb-2 text-[0.8125rem] font-medium tracking-[-0.01em] whitespace-nowrap text-ink-faint">Chats</div>
        <nav className="w-full px-3 pb-2" aria-label="Chats">
          {!sessions.length && <div className="flex h-8 items-center px-2.5 text-[0.84375rem] text-ink-faint">No chats</div>}
          {sessions.map(item => <div key={item.id} className={cn('group flex items-center rounded-[10px] hover:bg-surface-hover', selected === item.id && 'bg-surface-selected')}>
            <button type="button" disabled={busy} aria-current={selected === item.id ? 'page' : undefined} onClick={() => onSelect(item.id)} title={titles[item.id] ?? item.id} className="flex h-9 min-w-0 flex-1 cursor-pointer items-center gap-2 rounded-[10px] px-2.5 text-left text-[0.84375rem] outline-none focus-visible:bg-surface-hover disabled:cursor-not-allowed disabled:opacity-60">
              <span className="truncate">{titles[item.id] ?? `Session ${item.id.slice(0, 8)}`}</span>
              {item.running && <span className="size-1.5 shrink-0 rounded-full bg-ink-muted" aria-label="Running" />}
            </button>
            <button type="button" disabled={busy || item.running} onClick={() => onDelete(item.id)} aria-label={`Delete ${titles[item.id] ?? 'session ' + item.id.slice(0, 8)}`} className="mr-1 grid size-7 shrink-0 cursor-pointer place-items-center rounded-md text-ink-faint opacity-0 outline-none group-hover:opacity-100 hover:text-ink focus-visible:opacity-100 focus-visible:bg-canvas-sunken disabled:cursor-not-allowed disabled:opacity-20 max-md:opacity-100"><Trash2 className="size-3.5" /></button>
          </div>)}
        </nav>
      </div>
      <div className="w-full shrink-0 border-t border-edge/70 px-3 py-2">
        <button type="button" className="flex h-9 w-full cursor-pointer items-center gap-2.5 rounded-[10px] px-2.5 text-[0.875rem] outline-none hover:bg-surface-hover focus-visible:bg-surface-hover" onClick={onOpenSettings} aria-label="Settings">
          <span className="flex-1 text-left">agentkit</span>
          <Settings className="size-4 text-ink-muted" aria-hidden="true" />
        </button>
      </div>
    </aside>
  )
}
