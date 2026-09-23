import type { LucideIcon } from 'lucide-react'
import { cn } from '@/lib/utils'

export function SidebarNavItem({
  icon: Icon,
  label,
  collapsed = false,
  onClick,
  disabled = false,
}: {
  icon: LucideIcon
  label: string
  collapsed?: boolean
  onClick?: () => void
  disabled?: boolean
}) {
  return (
    <button
      className={cn(
        'group flex h-8 w-full cursor-pointer items-center gap-2.5 rounded-[10px] px-2.5 text-left text-[0.875rem] font-normal text-ink-soft outline-none transition-[background-color,color,transform] duration-100 active:scale-[0.985] focus-visible:bg-canvas-strong/60 focus-visible:text-ink disabled:cursor-not-allowed disabled:opacity-40',
        !collapsed && 'hover:bg-surface-hover hover:text-ink',
      )}
      type="button"
      title={label}
      onClick={onClick}
      disabled={disabled}
    >
      <span className="relative shrink-0">
        <span
          className={cn(
            'pointer-events-none absolute -inset-1.5 rounded-[9px] transition-colors duration-100',
            collapsed && 'group-hover:bg-surface-hover',
          )}
          aria-hidden="true"
        />
        <Icon
          className="relative size-4 text-ink-soft"
          strokeWidth={1.85}
          aria-hidden="true"
        />
      </span>
      <span
        className={cn(
          'whitespace-nowrap transition-opacity duration-100 ease-out motion-reduce:transition-none',
          collapsed ? 'opacity-0' : 'opacity-100',
        )}
      >
        {label}
      </span>
    </button>
  )
}
