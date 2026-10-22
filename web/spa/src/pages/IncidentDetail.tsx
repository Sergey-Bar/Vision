import { useParams, Link } from 'react-router-dom'
import { Page, PageHeader } from '@/components/Shell'
import { Panel, Section } from '@/components/ui/Panel'
import { StateBadge, SeverityBadge, Badge } from '@/components/ui/Badge'
import { KeyValue } from '@/components/ui/Table'
import { EmptyState, ErrorState, LoadingState } from '@/components/ui/States'
import { Button } from '@/components/ui/Button'
import { useIncident, useIncidentExplanation } from '@/hooks/product'
import { formatSiteTime } from '@/lib/site'

// The incident.
//
// This is the M1 product: one incident, and everything a reader needs to
// decide whether to believe it.
//
// The screen is in the order the questions are asked:
//
//   1. WHAT is it          the fact header: id, state, severity, site, rule
//   2. WHY does it exist   the stored decision, the reason code, the chain
//   3. WHAT is it made of  the events, the observations, the cited evidence
//   4. WHAT has been done  the dispositions
//
// The focus lives in the URL, not in the browser's storage. The focus is the
// incident id, it is a link somebody can send to a colleague, and it does
// not survive a reload as a surprise.
export function IncidentDetailPage() {
  const { id = '' } = useParams()
  const incident = useIncident(id)
  const explanation = useIncidentExplanation(id)

  if (incident.isPending) {
    return (
      <Page>
        <PageHeader title="Incident" />
        <LoadingState label="Reading the incident" />
      </Page>
    )
  }

  if (incident.isError) {
    return (
      <Page>
        <PageHeader title="Incident" />
        <ErrorState
          title="The product did not answer with this incident"
          detail={
            <>
              <p>
                The product refused or could not read incident{' '}
                <code className="font-mono text-[12px]">{id}</code>. An incident that is
                not yours and an incident that does not exist are the same answer on
                purpose, so this screen cannot tell you which it was.
              </p>
            </>
          }
          action={
            <Link to="/console/incidents">
              <Button variant="secondary">Back to incidents</Button>
            </Link>
          }
        />
      </Page>
    )
  }

  const d = incident.data
  const verification = d.verification
  const why = explanation.data
  const degraded = d.degraded || Boolean(verification?.aiDegradation)

  return (
    <Page>
      <PageHeader
        eyebrow="Incident"
        title={d.title || d.incidentId}
        lede={
          <>
            {d.incidentId} · {d.siteId} · rule{' '}
            <span className="font-mono text-[13px]">{d.ruleId}</span>
          </>
        }
        actions={
          <>
            <StateBadge state={d.state} />
            <SeverityBadge severity={d.severity} />
          </>
        }
      />

      {degraded && (
        <div className="mb-6 rounded-lg border border-warn-ink/40 bg-warn-ink/5 px-5 py-4 text-[13px] leading-[20px] text-warn-ink">
          <p>
            <span aria-hidden="true">▲ </span>
            This decision is degraded.
          </p>
          {d.degradedReasons && d.degradedReasons.length > 0 && (
            <ul className="mt-2 space-y-1 text-text-muted">
              {d.degradedReasons.map((r) => (
                <li key={r}>— {r}</li>
              ))}
            </ul>
          )}
          {verification?.aiDegradation && (
            <p className="mt-2 text-text-muted">
              The AI observation was degraded: {verification.aiDegradation}
            </p>
          )}
        </div>
      )}

      <Section title="The facts">
        <Panel>
          <KeyValue
            items={[
              { key: 'state', value: d.state },
              { key: 'severity', value: d.severity },
              { key: 'site', value: d.siteId },
              { key: 'rule', value: <span className="font-mono text-[12px]">{d.ruleId}</span> },
              { key: 'bundle', value: <span className="font-mono text-[12px]">{d.ruleBundleVersion}</span> },
              { key: 'policy', value: <span className="font-mono text-[12px]">{d.policyVersion}</span> },
              { key: 'raised', value: formatSiteTime(d.createdAt) },
              {
                key: 'first event',
                value: d.firstEventAt ? formatSiteTime(d.firstEventAt) : '—',
              },
              {
                key: 'decision',
                value: verification?.evaluatedAt ? formatSiteTime(verification.evaluatedAt) : '—',
              },
            ]}
          />
        </Panel>
      </Section>

      <Section title="Why this incident exists">
        <Panel>
          {verification ? (
            <>
              <div className="mb-4 flex flex-wrap items-center gap-3">
                <Badge tone={verification.state === 'verified' ? 'ok' : 'neutral'}>
                  <span aria-hidden="true">{verification.state === 'verified' ? '✓' : '?'}</span>
                  {verification.state}
                </Badge>
                <span className="font-mono text-[12px] text-text-dim">{verification.reasonCode}</span>
              </div>
              {verification.explanation && (
                <p className="mb-4 text-[14px] leading-[21px] text-text-muted">
                  {verification.explanation}
                </p>
              )}
              {why?.chain && why.chain.length > 0 && (
                <ol className="space-y-2">
                  {why.chain.map((step, i) => (
                    <li key={i} className="flex gap-3 text-[13px] leading-[20px] text-text-muted">
                      <span className="font-mono text-[11px] text-text-dim">{String(i + 1).padStart(2, '0')}</span>
                      <span>{step}</span>
                    </li>
                  ))}
                </ol>
              )}
              {why?.summary && (
                <p className="mt-4 border-t border-line pt-4 text-[13px] leading-[20px] text-text-dim">
                  {why.summary}
                </p>
              )}
              {why?.whyUnavailable && (
                <p className="mt-4 text-[13px] leading-[20px] text-warn-ink">
                  <span aria-hidden="true">▲ </span>
                  {why.whyUnavailable}
                </p>
              )}
            </>
          ) : (
            <EmptyState
              title="No stored decision yet"
              detail="The product has the incident but has not recorded a verification for it. That is a different answer from a decision that says no."
            />
          )}
        </Panel>
      </Section>

      <Section title="The record it is made of">
        <div className="grid gap-4 lg:grid-cols-2">
          <Panel title="Events">
            {d.events && d.events.length > 0 ? (
              <ul className="space-y-2">
                {d.events.map((e) => (
                  <li key={e.eventId} className="text-[13px] leading-[20px]">
                    <span className="font-mono text-[12px] text-text">{e.eventType}</span>
                    <span className="text-text-dim"> · {e.deviceId ?? e.zoneId ?? 'no device'}</span>
                    <span className="text-text-dim"> · {formatSiteTime(e.observedAt)}</span>
                  </li>
                ))}
              </ul>
            ) : (
              <EmptyState title="No events" detail="This incident cites no stored event." />
            )}
          </Panel>

          <Panel title="Evidence">
            {d.evidence && d.evidence.length > 0 ? (
              <ul className="space-y-2">
                {d.evidence.map((ev) => (
                  <li key={ev.evidenceId} className="text-[13px] leading-[20px]">
                    <Link
                      to={`/console/evidence/${encodeURIComponent(ev.evidenceId)}`}
                      className="font-mono text-[12px] text-accent"
                    >
                      {ev.evidenceId}
                    </Link>
                    {ev.kind && <span className="text-text-dim"> · {ev.kind}</span>}
                    {ev.sha256 && (
                      <span className="block font-mono text-[11px] text-text-dim break-all">
                        {ev.sha256}
                      </span>
                    )}
                  </li>
                ))}
              </ul>
            ) : (
              <EmptyState
                title="No evidence"
                detail="Nothing has been fetched for this incident. Evidence arrives from the vendor, and the absence of it is a fact about the vendor rather than about the incident."
              />
            )}
          </Panel>
        </div>
      </Section>

      <Section title="What has been done">
        <Panel>
          {d.dispositions && d.dispositions.length > 0 ? (
            <ul className="space-y-2">
              {d.dispositions.map((x) => (
                <li key={x.dispositionId} className="text-[13px] leading-[20px] text-text-muted">
                  <span className="text-text">{x.outcome}</span>
                  <span className="text-text-dim"> · agreement {x.agreement}</span>
                  {x.at && <span className="text-text-dim"> · {formatSiteTime(x.at)}</span>}
                  {x.note && <span className="block text-text-dim">{x.note}</span>}
                </li>
              ))}
            </ul>
          ) : (
            <EmptyState
              title="Nothing has been decided"
              detail="An incident that nobody has dispositioned is an incident nobody has answered for."
            />
          )}
        </Panel>
      </Section>
    </Page>
  )
}
