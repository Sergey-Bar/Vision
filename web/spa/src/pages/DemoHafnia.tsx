import { Page, PageHeader } from '@/components/Shell'
import { Panel, Section } from '@/components/ui/Panel'
import { Button } from '@/components/ui/Button'
import { StatusIndicator } from '@/components/ui/Badge'
import { EmptyState, ErrorState, LoadingState } from '@/components/ui/States'
import { KeyValue } from '@/components/ui/Table'
import { useDemoHafnia } from '@/hooks/api'
import { cn } from '@/lib/utils'
import type { DemoHafniaResponse } from '@/api'

// The Hafnia demo page.
//
// Runs the Milestone -> clip -> Hafnia VLM answer -> Milestone bookmark
// bridge against synthetic fixtures and renders the four-step result. Every
// byte is synthetic/demo data, marked `SIMULATED`, and never presented as
// automatic detection.
//
// Tesla-minimal: dark ground, hairline borders, tight monospaced numerals,
// generous whitespace, the single blue accent, no shadows or glows.
export function DemoHafniaPage() {
  const { mutate, isPending, isError, error, data } = useDemoHafnia()
  const steps = buildSteps(data)

  return (
    <Page>
      <PageHeader
        eyebrow="SIMULATED"
        title="Hafnia demo"
        lede="Runs the Milestone -> clip -> Hafnia VLM answer -> Milestone bookmark bridge against synthetic fixtures. It is never automatic detection."
        actions={
          <Button
            variant="primary"
            onClick={() => mutate()}
            disabled={isPending}
          >
            {isPending ? 'Running…' : 'Run the bridge'}
          </Button>
        }
      />

      {isPending ? (
        <LoadingState label="Running the bridge" />
      ) : isError ? (
        <ErrorState
          title="The bridge did not run"
          detail={describeError(error)}
          action={
            <Button variant="secondary" size="sm" onClick={() => mutate()}>
              Try again
            </Button>
          }
        />
      ) : data ? (
        <>
          {/* The four-step journey, one card across the width. */}
          <Section title="The four steps">
            <Panel>
              <ol className="flex flex-col gap-0 border-t border-line-strong">
                {steps.map((step, i) => (
                  <li
                    key={step.id}
                    className={cn(
                      'flex items-center gap-5 border-b border-line last:border-b-0 py-5 first:pt-0 last:pb-0',
                      i === 0 ? 'pt-6' : '',
                      i === steps.length - 1 ? 'pb-6' : '',
                    )}
                  >
                    <div className="flex h-11 w-11 shrink-0 items-center justify-center rounded-full border border-line-strong bg-bg-elevated">
                      <span className="font-mono text-[12px] font-semibold text-text">
                        {step.num}
                      </span>
                    </div>
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2.5">
                        <StatusIndicator state={step.state} label={step.label} />
                        <span className="font-mono text-[12px] font-medium text-text-muted">
                          {step.title}
                        </span>
                      </div>
                      {step.detail && (
                        <p className="mt-1.5 font-mono text-[11px] text-text-dim leading-[17px]">
                          {step.detail}
                        </p>
                      )}
                    </div>
                  </li>
                ))}
              </ol>
            </Panel>
          </Section>

          {/* The three data panels side by side on wide screens. */}
          <Section className="grid grid-cols-1 lg:grid-cols-3 gap-6">
            <Panel title="Event">
              <KeyValue
                items={[
                  { key: 'id', value: <span className="font-mono text-text">{data.event.id}</span> },
                  { key: 'type', value: <span className="font-mono text-text">{data.event.type}</span> },
                  { key: 'camera', value: <span className="font-mono text-text">{data.event.camera}</span> },
                  {
                    key: 'occurred at',
                    value: <span className="font-mono text-text">{data.event.occurredAt}</span>,
                  },
                  {
                    key: 'simulated',
                    value: <span className="font-mono text-accent-ink">{String(data.simulated)}</span>,
                  },
                ]}
              />
            </Panel>

            <Panel title="Hafnia answer">
              <KeyValue
                items={[
                  {
                    key: 'degraded',
                    value: <span className="font-mono text-text">{String(data.hafnia.degraded)}</span>,
                  },
                  { key: 'reason', value: <span className="font-mono text-text-dim">{data.hafnia.reason}</span> },
                  {
                    key: 'detail',
                    value: (
                      <span className="font-mono text-[11px] text-text-muted break-words">
                        {data.hafnia.detail}
                      </span>
                    ),
                  },
                  { key: 'model', value: <span className="font-mono text-text">{data.hafnia.model}</span> },
                ]}
              />
              {data.hafnia.structured && (
                <dl className="mt-6 grid grid-cols-[140px_1fr] gap-x-5 gap-y-2.5 text-[13px] leading-[19px]">
                  <dt className="font-mono text-[11px] uppercase tracking-[0.07em] text-text-dim">
                    structured
                  </dt>
                  <dd className="bg-bg-elevated rounded border border-line px-4 py-3 font-mono text-[11px] text-text overflow-x-auto">
                    {JSON.stringify(data.hafnia.structured, null, 2)}
                  </dd>
                </dl>
              )}
              {data.hafnia.degraded && data.hafnia.structured == null && (
                <p className="mt-6 text-[13px] leading-[20px] text-text-muted">
                  The Hafnia answer is degraded: no structured output was produced, and none
                  is fabricated. The detail carries the named reason.
                </p>
              )}
            </Panel>

            <Panel title="Bookmark write-back">
              <KeyValue
                items={[
                  {
                    key: 'placed',
                    value: <span className="font-mono text-text">{String(data.bookmark.placed)}</span>,
                  },
                  {
                    key: 'ref',
                    value: <span className="font-mono text-text">{data.bookmark.ref ?? '—'}</span>,
                  },
                  {
                    key: 'refusal',
                    value: <span className="font-mono text-text-dim">{data.bookmark.refusal ?? '—'}</span>,
                  },
                ]}
              />
            </Panel>
          </Section>
        </>
      ) : (
        <EmptyState
          title="The bridge has not run yet"
          detail="Click 'Run the bridge' to poll the synthetic Milestone scenario, send the test-pattern clip to the local Hafnia test server, and write the bookmark back to the camera timeline. All data is synthetic/demo and is marked SIMULATED."
        />
      )}
    </Page>
  )
}

interface Step {
  id: string
  num: string
  state: 'ok' | 'warn' | 'bad' | 'unknown'
  label: string
  title: string
  detail?: React.ReactNode
}

function buildSteps(data: DemoHafniaResponse | undefined): Step[] {
  const notRun: Step = {
    id: 'event',
    num: '1',
    state: 'unknown',
    label: 'not yet run',
    title: 'Event',
  }
  const notRunClip: Step = {
    id: 'clip',
    num: '2',
    state: 'unknown',
    label: 'not yet run',
    title: 'Clip',
  }
  const notRunHafnia: Step = {
    id: 'hafnia',
    num: '3',
    state: 'unknown',
    label: 'not yet run',
    title: 'Hafnia',
  }
  const notRunBookmark: Step = {
    id: 'bookmark',
    num: '4',
    state: 'unknown',
    label: 'not yet run',
    title: 'Bookmark',
  }

  if (!data) {
    return [notRun, notRunClip, notRunHafnia, notRunBookmark]
  }

  const clipState: 'ok' | 'warn' | 'bad' | 'unknown' = data.hafnia.degraded &&
    data.hafnia.reason === 'absent'
    ? 'warn'
    : 'ok'
  const hafniaState: 'ok' | 'warn' | 'bad' | 'unknown' = data.hafnia.degraded
    ? 'warn'
    : 'ok'
  const bookmarkState: 'ok' | 'warn' | 'bad' | 'unknown' = data.bookmark.placed
    ? 'ok'
    : data.bookmark.refusal !== ''
    ? 'warn'
    : 'unknown'

  return [
    {
      id: 'event',
      num: '1',
      state: 'ok',
      label: 'event',
      title: 'Motion detected',
      detail: <span className="font-mono text-[11px] text-text">{data.event.camera}</span>,
    },
    {
      id: 'clip',
      num: '2',
      state: clipState,
      label: clipState === 'warn' ? 'no evidence' : 'evidence captured',
      title: 'Clip',
      detail:
        clipState === 'warn'
          ? 'No evidence for this event, so Hafnia was not asked.'
          : 'Test-pattern clip sent to the local Hafnia test server.',
    },
    {
      id: 'hafnia',
      num: '3',
      state: hafniaState,
      label: hafniaState === 'warn' ? 'degraded' : 'answer',
      title: 'Hafnia',
      detail:
        hafniaState === 'warn'
          ? <span className="font-mono text-[11px] text-text-dim">reason={data.hafnia.reason}</span>
          : <span className="font-mono text-[11px] text-text">{hafniaLine(data.hafnia)}</span>,
    },
    {
      id: 'bookmark',
      num: '4',
      state: bookmarkState,
      label: bookmarkState === 'ok' ? 'bookmark placed' : 'refused',
      title: 'Bookmark',
      detail:
        bookmarkState === 'ok'
          ? <span className="font-mono text-[11px] text-text">{data.bookmark.ref}</span>
          : bookmarkState === 'warn'
            ? <span className="font-mono text-[11px] text-text-dim">{data.bookmark.refusal}</span>
            : <span className="font-mono text-[11px] text-text-dim">no bookmark writer configured</span>,
    },
  ]
}

function hafniaLine(h: DemoHafniaResponse['hafnia']): string {
  if (h.structured && typeof h.structured.summary === 'string') {
    return h.structured.summary
  }
  return h.model
}

function describeError(error: unknown): string {
  if (error instanceof Error) {
    return error.message
  }
  if (typeof error === 'string') {
    return error
  }
  return 'unknown error'
}
