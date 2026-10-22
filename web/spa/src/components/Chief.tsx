import { useState } from 'react'
import { Eye, Bot, ShieldAlert } from 'lucide-react'
import { useChief } from '@/hooks/product'
import { useCursor } from '@/lib/cursor'
import { Panel } from '@/components/ui/Panel'
import { Badge } from '@/components/ui/Badge'
import { Button } from '@/components/ui/Button'
import { LoadingState } from '@/components/ui/States'
import { describeError } from '@/lib/errors'

// The Chief.
//
// The Chief is NOT a page. It is a presence on the one screen, and it says
// three things in three lines:
//
//   1. what changed since you last looked   -- the DELTA
//   2. what needs you now                   -- the headline
//   3. what the system does not know        -- the degradation, NAMED
//
// The third line is the one that makes the other two trustworthy. A Chief
// that is quieter than usual and does not say why is the exact failure this
// product exists to prevent, and a customer discovers it at the worst
// possible moment.
//
// # The cursor lives in the URL
//
// `?since=<RFC3339>` is the moment the operator last looked. It is not
// stored anywhere: not in the browser's storage, which this console does not
// use at all, and not on the server, which would be a table of what each
// operator looked at.
//
// It is durable for as long as the URL is — a bookmark survives it, a reload
// survives it, and an interruption survives it — and it is gone when the
// operator closes the tab. That is the correct lifetime for "where I was".
export function ChiefPanel() {
  const [since, markSeen] = useCursor()
  const { data, isPending, isError, error } = useChief(since)
  const [marking, setMarking] = useState(false)

  // Marking as seen advances the cursor to NOW and puts it in the URL.
  //
  // It is an explicit act rather than an automatic one. A cursor that
  // advanced on every poll would be a cursor nobody chose, and the delta
  // would be empty by construction — the Chief would report "nothing new"
  // forever, which is the one thing a Chief must never do.
  const onMarkSeen = () => {
    setMarking(true)
    markSeen()
    setMarking(false)
  }

  if (isPending) return <LoadingState label="Asking the Chief" />

  if (isError) {
    return (
      <Panel title="The Chief">
        <p className="text-[13px] leading-[20px] text-text-muted">
          The console asked the product for the Chief and could not read the answer. What
          needs you is below, and it is the product's own queue — but the Chief is silent,
          and a silent Chief is not a Chief with nothing to say.
        </p>
        <p className="mt-2 font-mono text-[11px] text-text-dim">{describeError(error)}</p>
      </Panel>
    )
  }

  if (!data) return null

  const degraded = data.degradation
  const newCount = data.delta?.newIncidents?.length ?? 0

  return (
    <Panel
      title={
        <span className="flex items-center gap-2">
          <Bot size={16} className="text-accent" aria-hidden="true" />
          The Chief
        </span>
      }
      action={
        <span className="flex items-center gap-2">
          {degraded && (
            <Badge tone="warn">
              <span aria-hidden="true">!</span> degraded
            </Badge>
          )}
          <span title="How much the Chief is permitted to do on its own">
            <Badge tone="neutral">{data.effectiveAutonomy}</Badge>
          </span>
        </span>
      }
    >
      {/* The headline is the product's own sentence, and it is deterministic:
          the same facts always produce the same words. */}
      <p className="text-[15px] leading-[22px] text-text">{data.headline}</p>

      {/* The narration, when there is one. It is the AI plane's voice over the
          facts, and it carries its attribution: a sentence nobody can replay
          may not be shown to an operator. The product refuses a narration
          that cites something it did not record, so a narration on this screen
          is one the product checked. */}
      {data.narration && (
        <div className="mt-4 border-l-2 border-accent pl-4">
          <p className="text-[15px] leading-[22px] text-text">{data.narration.text}</p>
          <p className="mt-2 font-mono text-[11px] text-text-dim">
            {data.narration.provider} · {data.narration.model} ·{' '}
            {data.narration.contractVersion} · temp {data.narration.temperature}
          </p>
        </div>
      )}

      {/* What the system does not know, NAMED. */}
      {degraded && (
        <p className="mt-4 flex items-start gap-2 text-[13px] leading-[20px] text-warn-ink">
          <ShieldAlert size={15} className="mt-0.5 shrink-0" aria-hidden="true" />
          <span>
            <span className="font-mono text-[11px]">{degraded.reason}</span>
            {degraded.detail ? ` — ${degraded.detail}` : ''}
          </span>
        </p>
      )}

      {/* The delta, and the control that moves the cursor on. */}
      <div className="mt-4 flex items-center justify-between gap-3 border-t border-line pt-3">
        <p className="text-[12px] text-text-dim">
          {data.delta?.measured
            ? `${newCount} new since ${formatSince(data.delta.since)}`
            : 'No cursor yet — this is the whole picture, not a change in it'}
        </p>
        <Button
          variant="ghost"
          size="sm"
          onClick={onMarkSeen}
          disabled={marking}
          title="Move the cursor to now, so the Chief reports only what is new"
        >
          <Eye size={14} aria-hidden="true" />
          Mark as seen
        </Button>
      </div>
    </Panel>
  )
}

// formatSince renders the cursor for a person.
//
// The site's clock is the site's clock, but the cursor is an absolute instant
// the product compares against timestamps, so it is rendered in the reader's
// own zone with the zone named. A time without a zone is a time the reader
// has to guess at.
function formatSince(iso: string): string {
  const at = new Date(iso)
  if (Number.isNaN(at.getTime())) return iso
  return at.toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}
