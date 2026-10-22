import { cn } from '@/lib/utils'

// The surfaces.
//
// A panel is a hairline and a tint step. There is no shadow: on this ground a
// shadow reads as a smudge, and the console used to put `shadow-2xl` on
// nearly every card, which is what made it look like a consumer app rather
// than an instrument.

export function Panel({
  title,
  action,
  children,
  className,
  bodyClassName,
}: {
  title?: React.ReactNode
  action?: React.ReactNode
  children: React.ReactNode
  className?: string
  bodyClassName?: string
}) {
  return (
    <section
      className={cn('rounded-lg border border-line bg-bg-panel', className)}
    >
      {(title ?? action) && (
        <header className="flex items-baseline justify-between gap-4 px-5 pt-4 pb-3">
          {title ? (
            <h2 className="text-[15px] font-semibold tracking-tight text-text">{title}</h2>
          ) : (
            <span />
          )}
          {action}
        </header>
      )}
      <div className={cn('px-5 pb-5', !title && !action && 'pt-5', bodyClassName)}>{children}</div>
    </section>
  )
}

// A section is a heading and its content, with no border. A page is a
// sequence of these, and the spacing between them is the page's structure.
export function Section({
  title,
  eyebrow,
  children,
  className,
}: {
  title?: React.ReactNode
  eyebrow?: React.ReactNode
  children: React.ReactNode
  className?: string
}) {
  return (
    <section className={cn('mb-8', className)}>
      {(eyebrow ?? title) && (
        <div className="mb-3">
          {eyebrow && (
            <p className="font-mono text-[11px] font-medium uppercase tracking-[0.08em] text-text-dim">
              {eyebrow}
            </p>
          )}
          {title && <h2 className="text-[22px] font-semibold tracking-tight text-text">{title}</h2>}
        </div>
      )}
      {children}
    </section>
  )
}

// The page head. One h1 per screen, and it is the first thing on the page.
export function PageHead({
  eyebrow,
  title,
  lede,
  actions,
}: {
  eyebrow?: React.ReactNode
  title: React.ReactNode
  lede?: React.ReactNode
  actions?: React.ReactNode
}) {
  return (
    <header className="mb-8 flex flex-wrap items-end justify-between gap-4">
      <div className="max-w-[68ch]">
        {eyebrow && (
          <p className="font-mono text-[11px] font-medium uppercase tracking-[0.08em] text-text-dim">
            {eyebrow}
          </p>
        )}
        <h1 className="mt-1 text-[34px] font-semibold leading-[40px] tracking-[-0.025em] text-text">
          {title}
        </h1>
        {lede && <p className="mt-3 text-[15px] leading-[22px] text-text-muted">{lede}</p>}
      </div>
      {actions && <div className="flex items-center gap-3">{actions}</div>}
    </header>
  )
}
