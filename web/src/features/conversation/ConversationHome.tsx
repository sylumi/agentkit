import type { ReactNode } from 'react'

export function ConversationHome({ title, description, children }: {
  title: string
  description: string
  children: ReactNode
}) {
  return (
    <main className="h-full overflow-x-hidden overflow-y-auto px-3 md:px-6 md:[scrollbar-gutter:stable_both-edges]">
      <div className="conversation-rail mx-auto grid min-h-full w-full max-w-[750px] place-items-center pt-5 pb-9 max-md:pt-4 max-md:pb-7">
        <div className="flex w-full -translate-y-[3vh] flex-col items-center gap-9">
          <div className="max-w-lg text-center">
            <h1 className="m-0 text-[1.75rem] leading-tight font-medium tracking-[-0.03em] text-ink max-sm:text-2xl">
              {title}
            </h1>
            <p className="mt-2.5 text-[0.9375rem] leading-6 text-ink-muted">
              {description}
            </p>
          </div>
          {children}
        </div>
      </div>
    </main>
  )
}
