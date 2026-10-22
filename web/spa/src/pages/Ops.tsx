import { Page, PageHeader } from '@/components/Shell'
import { Panel, Section } from '@/components/ui/Panel'
import { Badge } from '@/components/ui/Badge'
import { Table } from '@/components/ui/Table'
import { EmptyState, ErrorState, LoadingState } from '@/components/ui/States'
import { useClockDrift, useDeadLetters, useReadiness } from '@/hooks/product'
import { formatSiteTime } from '@/lib/site'

// Operations.
//
// The things that are not working, in the order an operator would want them:
// is the product ready, what is stuck, and how far the clocks have drifted.
//
// The DEAD LETTERS are the first block because a dead letter is an event
// that reached a terminal state that is not "handled", and the repository's
// first invariant is that nothing that reaches the inbox is lost. A dead
// letter is the visible form of that invariant under pressure, so it is the
// first thing on the screen rather than the last.
export function OpsPage() {
  const readiness = useReadiness()
  const deadLetters = useDeadLetters()
  const drift = useClockDrift()

  const checks = Object.entries(readiness.data?.checks ?? {})

  return (
    <Page>
      <PageHeader
        eyebrow="Operations"
        title="What is not working"
        lede="The product's own answers: what it is ready for, what got stuck, and how far the clocks have drifted from the one the platform trusts."
      />

      <Section title="Dead letters">
        <Panel>
          {deadLetters.isPending ? (
            <LoadingState label="Reading the dead letters" />
          ) : deadLetters.isError ? (
            <ErrorState
              title="The dead-letter reader did not answer"
              detail="That is not an empty list: an empty list and a broken reader look the same to an operator, and only one of them is true."
            />
          ) : deadLetters.data.length === 0 ? (
            <EmptyState
              title="Nothing is stuck"
              detail="Every event that reached the inbox has reached a terminal state. The one that did not would be here."
            />
          ) : (
            <Table
              rows={deadLetters.data}
              rowKey={(d, i) => d.id ?? String(i)}
              columns={[
                {
                  key: 'kind',
                  header: 'Kind',
                  render: (d) => <span className="font-mono text-[12px] text-text">{d.kind ?? '—'}</span>,
                },
                { key: 'site', header: 'Site', render: (d) => d.siteId ?? '—' },
                {
                  key: 'reason',
                  header: 'Why it stopped',
                  render: (d) => d.reason ?? '—',
                },
                {
                  key: 'received',
                  header: 'Received',
                  render: (d) => (d.receivedAt ? formatSiteTime(d.receivedAt) : '—'),
                },
                {
                  key: 'attempts',
                  header: 'Attempts',
                  numeric: true,
                  render: (d) => d.attempts ?? '—',
                },
              ]}
            />
          )}
        </Panel>
      </Section>

      <Section title="Clock drift">
        <Panel>
          {drift.isPending ? (
            <LoadingState label="Reading the clock drift" />
          ) : drift.isError ? (
            <ErrorState title="The clock drift reader did not answer" />
          ) : drift.data.length === 0 ? (
            <EmptyState
              title="No drift is recorded"
              detail="Nothing has drifted far enough to be corrected. The correction is applied before the record is written, so a corrected moment and an uncorrected one are both already in the site's zone."
            />
          ) : (
            <Table
              rows={drift.data}
              rowKey={(d, i) => d.source ?? String(i)}
              columns={[
                {
                  key: 'source',
                  header: 'Source',
                  render: (d) => <span className="font-mono text-[12px] text-text">{d.source ?? '—'}</span>,
                },
                {
                  key: 'skew',
                  header: 'Skew',
                  numeric: true,
                  render: (d) => (d.skewMs === undefined ? '—' : `${d.skewMs} ms`),
                },
                {
                  key: 'corrected',
                  header: 'Corrected',
                  render: (d) =>
                    d.corrected ? (
                      <Badge tone="ok">
                        <span aria-hidden="true">✓</span> corrected
                      </Badge>
                    ) : (
                      <Badge tone="warn">
                        <span aria-hidden="true">▲</span> not corrected
                      </Badge>
                    ),
                },
                {
                  key: 'observed',
                  header: 'Observed',
                  render: (d) => (d.observedAt ? formatSiteTime(d.observedAt) : '—'),
                },
              ]}
            />
          )}
        </Panel>
      </Section>

      <Section title="Readiness">
        <Panel>
          {readiness.isPending ? (
            <LoadingState label="Asking the product" />
          ) : readiness.isError ? (
            <ErrorState title="The product did not answer the readiness probe" />
          ) : checks.length === 0 ? (
            <EmptyState title="The product reported no checks" />
          ) : (
            <ul className="grid gap-2 sm:grid-cols-2">
              {checks.map(([name, check]) => (
                <li
                  key={name}
                  className="flex items-center justify-between gap-3 rounded border border-line px-4 py-3"
                >
                  <span className="font-mono text-[12px] text-text-muted">{name}</span>
                  {check.ok ? (
                    <Badge tone="ok">
                      <span aria-hidden="true">✓</span> ready
                    </Badge>
                  ) : (
                    <Badge tone="bad">
                      <span aria-hidden="true">■</span> not ready
                    </Badge>
                  )}
                </li>
              ))}
            </ul>
          )}
        </Panel>
      </Section>
    </Page>
  )
}
