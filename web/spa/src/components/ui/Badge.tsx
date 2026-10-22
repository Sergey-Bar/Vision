import { cn } from '@/lib/utils'

// The badges.
//
// A badge carries THREE channels: a glyph, a word and a colour. The word is
// always present, because a colour is never the only channel — a reader who
// cannot separate the hues must still be able to read the state.
//
// The severity and state maps are CLOSED. An untrusted bundle can put any
// string in a severity field, and a class name built from it is a class name
// an attacker chose. These maps mirror `severityClass` and `stateClass` in
// `internal/api/console.go`, so the product and the console cannot disagree
// about what a severity looks like.

// The product's five severities, and the console's word for each.
const SEVERITY: Record<string, { word: string; glyph: string; className: string }> = {
  info: { word: 'info', glyph: '·', className: 'text-text-muted border-line' },
  low: { word: 'low', glyph: '·', className: 'text-text-muted border-line' },
  medium: { word: 'medium', glyph: '◆', className: 'text-warn-ink border-warn-ink/40' },
  high: { word: 'high', glyph: '▲', className: 'text-warn-ink border-warn-ink/40' },
  critical: { word: 'critical', glyph: '■', className: 'text-bad-ink border-bad-ink/40' },
}

const STATE: Record<string, { word: string; glyph: string; className: string }> = {
  open: { word: 'open', glyph: '●', className: 'text-accent-ink border-accent-ink/40' },
  in_review: { word: 'in review', glyph: '◐', className: 'text-warn-ink border-warn-ink/40' },
  dispositioned: { word: 'dispositioned', glyph: '✓', className: 'text-ok-ink border-ok-ink/40' },
  closed: { word: 'closed', glyph: '○', className: 'text-text-dim border-line' },
}

// The unknown fallbacks. A severity the product does not recognise is shown
// as unknown, which is the honest reading, and it is not the same as low.
const SEVERITY_UNKNOWN = { word: 'unknown', glyph: '?', className: 'text-unknown-ink border-line' }
const STATE_UNKNOWN = { word: 'unknown', glyph: '?', className: 'text-unknown-ink border-line' }

export function severityClass(severity: string) {
  return SEVERITY[severity] ?? SEVERITY_UNKNOWN
}

export function stateClass(state: string) {
  return STATE[state] ?? STATE_UNKNOWN
}

const BASE =
  'inline-flex items-center gap-1.5 rounded-full border bg-bg-elevated px-2 py-0.5 ' +
  'font-mono text-[11px] font-medium leading-4 whitespace-nowrap'

export function Badge({
  children,
  className,
  tone = 'neutral',
}: {
  children: React.ReactNode
  className?: string
  tone?: 'neutral' | 'ok' | 'warn' | 'bad' | 'info' | 'unknown'
}) {
  const tones: Record<string, string> = {
    neutral: 'text-text-muted border-line',
    ok: 'text-ok-ink border-ok-ink/40',
    warn: 'text-warn-ink border-warn-ink/40',
    bad: 'text-bad-ink border-bad-ink/40',
    info: 'text-accent-ink border-accent-ink/40',
    unknown: 'text-unknown-ink border-line',
  }
  return <span className={cn(BASE, tones[tone], className)}>{children}</span>
}

export function SeverityBadge({ severity }: { severity: string }) {
  const s = severityClass(severity)
  return (
    <span className={cn(BASE, s.className)}>
      <span aria-hidden="true">{s.glyph}</span>
      {s.word}
    </span>
  )
}

export function StateBadge({ state }: { state: string }) {
  const s = stateClass(state)
  return (
    <span className={cn(BASE, s.className)}>
      <span aria-hidden="true">{s.glyph}</span>
      {s.word}
    </span>
  )
}

// The status indicator is a dot and a word, and it does not pulse.
//
// The console used to pulse a green dot beside a number that changed. A
// pulsing dot beside a changing number is a page that reflows while somebody
// is reading it, and the word beside it says the same thing without moving.
export function StatusIndicator({
  state,
  label,
}: {
  state: 'ok' | 'warn' | 'bad' | 'unknown'
  label: string
}) {
  // The glyph is TEXT, so the tone is a text colour. A background colour on
  // a glyph renders a coloured BAR where the glyph should be, which is what
  // it did: the indicator read as a vertical stripe.
  const tones: Record<string, string> = {
    ok: 'text-ok-ink',
    warn: 'text-warn-ink',
    bad: 'text-bad-ink',
    unknown: 'text-unknown-ink',
  }
  const glyphs: Record<string, string> = { ok: '●', warn: '▲', bad: '■', unknown: '?' }
  return (
    <span className="inline-flex items-center gap-2 text-[12px] text-text-muted">
      <span aria-hidden="true" className={cn('text-[10px] leading-none', tones[state])}>
        {glyphs[state]}
      </span>
      {label}
    </span>
  )
}
