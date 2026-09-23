// Usage includes cache/reasoning in input/output.
import { useEffect, useRef, useState } from 'react'
import {
  Check,
  Copy,
  ThumbsDown,
  ThumbsUp,
  type LucideIcon,
} from 'lucide-react'
import { Tooltip } from 'radix-ui'
import {
  TOOLTIP_DELAY_MS,
  TOOLTIP_SKIP_DELAY_MS,
} from '@/shared/ui/tooltipTiming'
import { hasKnownUsage, type Usage } from './responseUsage'
import { formatMessageTime } from '@/lib/time'
import { cn } from '@/lib/utils'

export function ResponseActions({
  usage,
  modelName,
  responseText,
  completedAt,
}: {
  usage?: Usage
  modelName?: string
  responseText: string
  completedAt?: string
}) {
  const locale = 'en-US'
  const formatNumber = (value: number, options?: Intl.NumberFormatOptions) => value.toLocaleString(locale, options)
  const [copied, setCopied] = useState(false)
  const [feedback, setFeedback] = useState<'up' | 'down'>()
  const resetCopyRef = useRef<number>(undefined)
  // Input/output already include cached input/reasoning in Agentkit.
  const totalTokens = usage?.input_tokens !== undefined && usage.output_tokens !== undefined
    ? usage.input_tokens + usage.output_tokens : undefined
  const cacheHitRate = usage?.cached_input_tokens !== undefined && usage.input_tokens !== undefined && usage.input_tokens > 0
    ? usage.cached_input_tokens / usage.input_tokens : undefined
  const completedTime = completedAt ? formatMessageTime(completedAt, locale) : ''

  useEffect(
    () => () => {
      if (resetCopyRef.current) window.clearTimeout(resetCopyRef.current)
    },
    [],
  )

  const copyResponse = async () => {
    if (!responseText) return
    try {
      await navigator.clipboard.writeText(responseText)
      setCopied(true)
      if (resetCopyRef.current) window.clearTimeout(resetCopyRef.current)
      resetCopyRef.current = window.setTimeout(() => setCopied(false), 1600)
    } catch {
      setCopied(false)
    }
  }

  return (
    <div
      className="mt-0.5 flex h-7 min-w-0 animate-[fade-in_160ms_ease-out] items-center gap-0.5 overflow-hidden"
      data-testid="response-actions"
    >
      <Tooltip.Provider
        delayDuration={TOOLTIP_DELAY_MS}
        skipDelayDuration={TOOLTIP_SKIP_DELAY_MS}
      >
        <div
          className="flex shrink-0 items-center gap-0.5"
          data-testid="response-message-actions"
        >
          <ActionButton
            icon={copied ? Check : Copy}
            label={copied ? 'Copied' : 'Copy response'}
            disabled={!responseText}
            onClick={() => void copyResponse()}
          />
          <ActionButton
            icon={ThumbsUp}
            label={'Good response'}
            pressed={feedback === 'up'}
            onClick={() => setFeedback((current) => (current === 'up' ? undefined : 'up'))}
          />
          <ActionButton
            icon={ThumbsDown}
            label={'Bad response'}
            pressed={feedback === 'down'}
            onClick={() => setFeedback((current) => (current === 'down' ? undefined : 'down'))}
          />
          {completedTime && (
            <time
              className="ml-1.5 shrink-0 pr-1 text-[0.75rem] leading-5 text-ink-faint tabular-nums"
              dateTime={completedAt}
            >
              {completedTime}
            </time>
          )}

          {usage && hasKnownUsage(usage) && (
            <span className="mx-1 h-3 w-px shrink-0 bg-canvas-strong" aria-hidden="true" />
          )}
        </div>

        {usage && hasKnownUsage(usage) && (
          <Tooltip.Root>
            <Tooltip.Trigger asChild>
              <button
                className="inline-flex h-7 min-w-0 max-w-full shrink items-center rounded-lg px-2 text-[0.75rem] leading-5 text-ink-faint tabular-nums outline-none transition-colors hover:bg-surface-active hover:text-ink-muted focus-visible:bg-surface-active focus-visible:text-ink-muted data-[state=delayed-open]:bg-surface-active data-[state=delayed-open]:text-ink-muted"
                type="button"
                aria-label={'Show run token usage details'}
                data-testid="response-usage-trigger"
              >
                <span
                  className="block min-w-0 truncate text-left whitespace-nowrap"
                  data-testid="response-usage-summary"
                >
                  <span className="font-medium text-ink-muted">{'Run usage'}</span>
                  <span className="mx-1.5 text-ink-ghost">·</span>
                  <span>
                    {totalTokens === undefined ? '--' : formatCompactNumber(totalTokens, formatNumber)} {'tokens'}
                  </span>
                  {modelName && (
                    <>
                      <span className="mx-1.5 text-ink-ghost">·</span>
                      <span>{modelName}</span>
                    </>
                  )}
                </span>
              </button>
            </Tooltip.Trigger>

            <Tooltip.Portal>
              <Tooltip.Content
                side="bottom"
                align="start"
                sideOffset={7}
                collisionPadding={10}
                className="z-[150] animate-[fade-in_110ms_ease-out] rounded-lg border border-edge/80 bg-canvas px-2.5 py-1.5 text-[0.6875rem] leading-4 text-ink-soft tabular-nums shadow-[0_10px_28px_-20px_rgba(28,25,23,0.4)] outline-none"
              >
                <div className="flex items-center gap-2.5 whitespace-nowrap">
                  <Metric
                    label={'Input'}
                    value={usage.input_tokens === undefined ? '--' : formatNumber(usage.input_tokens)}
                  />
                  <Metric label={'Output'} value={usage.output_tokens === undefined ? '--' : formatNumber(usage.output_tokens)} />
                  {usage.cached_input_tokens !== undefined && usage.cached_input_tokens > 0 && (
                    <Metric label={'Cache read'} value={formatNumber(usage.cached_input_tokens)} />
                  )}
                  {cacheHitRate !== undefined && (
                    <Metric
                      label={'Cache hit'}
                      value={formatNumber(cacheHitRate, {
                        style: 'percent',
                        maximumFractionDigits: 1,
                      })}
                    />
                  )}
                  {usage.reasoning_output_tokens !== undefined && (
                    <Metric label="Reasoning output" value={formatNumber(usage.reasoning_output_tokens)} />
                  )}
                </div>
              </Tooltip.Content>
            </Tooltip.Portal>
          </Tooltip.Root>
        )}
      </Tooltip.Provider>
    </div>
  )
}

function ActionButton({
  icon: Icon,
  label,
  pressed,
  disabled,
  iconClassName,
  onClick,
}: {
  icon: LucideIcon
  label: string
  pressed?: boolean
  disabled?: boolean
  iconClassName?: string
  onClick: () => void
}) {
  return (
    <Tooltip.Root>
      <Tooltip.Trigger asChild>
        <button
          className={cn(
            'grid size-7 shrink-0 cursor-pointer place-items-center rounded-lg text-ink-faint outline-none transition-colors hover:bg-surface-active hover:text-ink-soft focus-visible:bg-surface-active focus-visible:text-ink-soft disabled:cursor-not-allowed disabled:opacity-30',
            pressed && 'bg-surface-selected text-ink-soft',
          )}
          type="button"
          aria-label={label}
          aria-pressed={pressed}
          disabled={disabled}
          onClick={onClick}
        >
          <Icon className={cn('size-[0.9375rem]', iconClassName)} aria-hidden="true" />
        </button>
      </Tooltip.Trigger>
      <Tooltip.Portal>
        <Tooltip.Content
          side="bottom"
          sideOffset={6}
          collisionPadding={8}
          className="z-[150] animate-[fade-in_100ms_ease-out] rounded-md bg-canvas-inverse px-2 py-1 text-[0.6875rem] leading-4 font-medium whitespace-nowrap text-ink-inverse shadow-lg"
        >
          {label}
        </Tooltip.Content>
      </Tooltip.Portal>
    </Tooltip.Root>
  )
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <span className="flex items-baseline gap-1">
      <span className="text-ink-faint">{label}</span>
      <span className="font-medium text-ink-soft">{value}</span>
    </span>
  )
}

type NumberFormatter = (value: number, options?: Intl.NumberFormatOptions) => string

function formatCompactNumber(value: number, formatNumber: NumberFormatter): string {
  if (value >= 1_000_000) return `${formatDecimal(value / 1_000_000, formatNumber)}m`
  if (value >= 1_000) return `${formatDecimal(value / 1_000, formatNumber)}k`
  return formatNumber(Math.round(value))
}

function formatDecimal(value: number, formatNumber: NumberFormatter): string {
  return formatNumber(value, { maximumFractionDigits: value >= 100 ? 0 : 1 })
}
