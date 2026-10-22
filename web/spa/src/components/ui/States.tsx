import { cn } from '@/lib/utils'

// The five states.
//
// Every region of every screen implements all five, and LOADING IS NEVER
// RENDERED AS DATA.
//
// The Cockpit used to render `0`, `unknown` and `not configured` while the
// request was still in flight. A zero that is really "I have not asked yet"
// is the single most expensive kind of lie in an instrument panel, because it
// is indistinguishable from a measurement.
//
// The five, in the repository's own names:
//
//   Loading    the region says it is loading and says nothing else
//   Empty      the region says the answer is empty and why
//   Partial    some of it arrived; each missing item is a row with its reason
//   Error      the region says what failed, in words a person can act on
//   Unavailable the AI is not reachable; a missing AI observation is NEVER a
//              negative observation, and the region says so

export function LoadingState({ label = 'Loading' }: { label?: string }) {
  return (
    <div role="status" aria-live="polite" className="flex items-center gap-3 py-6 text-[13px] text-text-dim">
      <span
        aria-hidden="true"
        className="h-3 w-3 rounded-full border-2 border-line-strong border-t-accent animate-spin"
        style={{ animationDuration: '800ms' }}
      />
      {label}…
    </div>
  )
}

export function EmptyState({
  title,
  detail,
  action,
}: {
  title: string
  detail?: React.ReactNode
  action?: React.ReactNode
}) {
  return (
    <div className="rounded-lg border border-dashed border-line px-6 py-10 text-center">
      <p className="text-[15px] font-semibold text-text">{title}</p>
      {detail && <p className="mx-auto mt-2 max-w-[60ch] text-[13px] leading-[20px] text-text-muted">{detail}</p>}
      {action && <div className="mt-5 flex justify-center">{action}</div>}
    </div>
  )
}

export function ErrorState({
  title = 'This did not load',
  detail,
  action,
}: {
  title?: string
  detail?: React.ReactNode
  action?: React.ReactNode
}) {
  return (
    <div
      role="alert"
      className="rounded-lg border border-bad-ink/40 bg-bad-ink/5 px-5 py-4"
    >
      <p className="text-[14px] font-semibold text-bad-ink">
        <span aria-hidden="true">■ </span>
        {title}
      </p>
      {detail && <div className="mt-1 text-[13px] leading-[20px] text-text-muted">{detail}</div>}
      {action && <div className="mt-4 flex gap-3">{action}</div>}
    </div>
  )
}

// The partial state is the one that matters most, and the one that is
// hardest to get right: the answer arrived for four of seven items, and each
// missing item is a ROW with its own reason. A gap is not an answer.
export function PartialState({
  children,
  missing,
}: {
  children: React.ReactNode
  missing: { label: string; reason: string }[]
}) {
  return (
    <div className="rounded-lg border border-warn-ink/40">
      <div className="border-b border-warn-ink/40 bg-warn-ink/5 px-5 py-3 text-[13px] text-warn-ink">
        <span aria-hidden="true">▲ </span>
        Some of this did not arrive. Each missing item is a row with its own reason.
      </div>
      <div className="px-5 py-4">{children}</div>
      {missing.length > 0 && (
        <ul className="border-t border-line px-5 py-4 text-[13px] leading-[20px] text-text-muted">
          {missing.map((m) => (
            <li key={m.label} className="flex gap-3 py-1">
              <span className="min-w-[160px] text-text-dim">{m.label}</span>
              <span>{m.reason}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

// The AI-unavailable state. A missing AI observation is never a negative
// observation, and the screen says which it is showing.
export function AiUnavailableState({ reason }: { reason: string }) {
  return (
    <div className="rounded-lg border border-line bg-bg-elevated px-5 py-4 text-[13px] leading-[20px] text-text-muted">
      <p className="text-text">
        <span aria-hidden="true">◇ </span>
        The AI observation is not available.
      </p>
      <p className="mt-1">{reason}</p>
      <p className="mt-2 text-text-dim">
        This is an absence, not a negative answer. Nothing here says the model found
        nothing; it says the model was not asked, or could not be reached.
      </p>
    </div>
  )
}

// The skeleton is the loading state made of shape rather than of words, for
// the rows where the shape is known.
export function Skeleton({ className }: { className?: string }) {
  return <div className={cn('rounded bg-bg-elevated animate-pulse', className)} aria-hidden="true" />
}
