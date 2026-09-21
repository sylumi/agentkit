import { useLayoutEffect, useRef } from 'react'
import { ArrowUp, ChevronDown, Square } from 'lucide-react'
import { composerMenuTriggerClass } from '@/shared/ui/composerControlStyles'
import { ComposerControlTooltip } from './ComposerControlTooltip'

export function Composer({ value, onChange, onOpenModel, onSend, onStop, busy, canSend, canStop, model, status }: {
  value: string
  onChange: (value: string) => void
  onOpenModel: () => void
  onSend: () => void
  onStop: () => void
  busy: boolean
  canSend: boolean
  canStop: boolean
  model?: string
  status: string
}) {
  const ref = useRef<HTMLTextAreaElement>(null)

  useLayoutEffect(() => {
    const textarea = ref.current
    if (!textarea) return
    textarea.style.height = 'auto'
    textarea.style.height = `${Math.min(textarea.scrollHeight, 240)}px`
  }, [value])

  return (
    <footer data-testid="composer" className="z-30 w-full bg-transparent p-0">
      <div className="relative mx-auto flex w-full max-w-[750px] flex-col gap-2">
        <div
          data-testid="composer-surface"
          className="relative min-h-[100px] rounded-[22px] border border-edge bg-canvas shadow-[0_10px_32px_-24px_rgba(28,25,23,0.32)] [container-type:inline-size]"
        >
          <div className="grid min-h-[98px] grid-cols-[minmax(0,1fr)] grid-rows-[auto_2.5rem] items-center gap-y-1 px-3 pt-2.5 pb-1.5">
            <div className="col-start-1 row-start-1 flex min-w-0 flex-col gap-2">
              <div className="flex min-w-0 items-start px-1">
                <textarea
                  ref={ref}
                  rows={1}
                  value={value}
                  readOnly={busy}
                  aria-label="Message your agent"
                  aria-describedby="connection-status"
                  className="block max-h-[15rem] min-h-8 min-w-0 flex-1 resize-none overflow-y-auto border-0 bg-transparent px-1 py-1.5 text-[13px] leading-5 font-normal text-ink outline-none placeholder:text-ink-faint disabled:cursor-not-allowed disabled:bg-transparent"
                  placeholder="Ask anything…"
                  onChange={event => onChange(event.target.value)}
                  onKeyDown={event => {
                    if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing && event.keyCode !== 229) {
                      event.preventDefault()
                      if (canSend) onSend()
                    }
                  }}
                />
              </div>
            </div>
            <div className="col-start-1 row-start-2 flex min-w-0 items-center">
              <div className="ml-auto flex min-w-0 items-center gap-1">
                {model && <button type="button" className={composerMenuTriggerClass} onClick={onOpenModel} aria-label="Model information">
                  <span className="min-w-0 truncate">{model}</span>
                  <ChevronDown className="size-3 shrink-0" aria-hidden="true" />
                  <ComposerControlTooltip align="end">Model information</ComposerControlTooltip>
                </button>}
                <button
                  data-testid="composer-send"
                  className="group relative grid size-[30px] shrink-0 cursor-pointer place-items-center rounded-full bg-canvas-inverse text-ink-inverse outline-none focus-visible:ring-2 focus-visible:ring-ink-muted focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-25"
                  type="button"
                  aria-label={canStop ? 'Stop run' : 'Send prompt'}
                  onClick={canStop ? onStop : onSend}
                  disabled={!canStop && !canSend}
                >
                  {canStop ? <Square className="size-3 fill-current" aria-hidden="true" /> : <ArrowUp className="size-4" aria-hidden="true" />}
                </button>
              </div>
            </div>
          </div>
        </div>
        <p id="connection-status" role="status" className="m-0 min-h-5 text-center text-[0.75rem] leading-5 text-ink-faint">{status}</p>
      </div>
    </footer>
  )
}
