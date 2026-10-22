import { Page, PageHeader } from '@/components/Shell'
import { Panel, Section } from '@/components/ui/Panel'
import { Badge } from '@/components/ui/Badge'
import { EmptyState, ErrorState, LoadingState } from '@/components/ui/States'
import { KeyValue } from '@/components/ui/Table'
import { useAIChain } from '@/hooks/product'
import { describeError } from '@/lib/errors'

// The provider chain.
//
// # What this page is for
//
// The chain is the site's BRAIN, and the ORDER of its providers is a policy:
// it says what may leave the site, and it is the thing an auditor asks
// about.
//
// So the page shows the order as a LIST in that order, and not as a set. A
// set has no order, and an order with no order is a policy nobody can check.
//
// # The egress log
//
// What left the site: which provider, which observation, how much, and the
// DIGESTS of the evidence that went.
//
// It never carries the content, and the page says so — because a log that
// carried the content would be a second copy of the evidence somewhere nobody
// chose to put it.
export function ChainPage() {
  const { data, isPending, isError, error } = useAIChain()

  if (isPending) {
    return (
      <Page>
        <PageHeader title="The chain" subtitle="What answers, and what left the site" />
        <LoadingState label="Reading the provider chain" />
      </Page>
    )
  }

  if (isError) {
    return (
      <Page>
        <PageHeader title="The chain" subtitle="What answers, and what left the site" />
        <ErrorState
          title="The chain did not answer"
          detail={
            <>
              <p>
                The console asked the product which models may answer for this site and could not
                read the answer. The panels below are empty because the product did not say, not
                because nothing is configured.
              </p>
              <p className="mt-2 font-mono text-[11px] text-text-dim">{describeError(error)}</p>
            </>
          }
        />
      </Page>
    )
  }

  const providers = data?.providers ?? []
  const egress = data?.egress ?? []

  return (
    <Page>
      <PageHeader
        title="The chain"
        subtitle="What answers, and what left the site"
        lede="The order is the policy: it says which provider the site prefers, what it falls back to, and what may leave the machine at all."
      />

      <Section>
        <Panel
          title="The chain"
          action={
            <span className="font-mono text-[11px] text-text-dim">
              {providers.length} provider{providers.length === 1 ? '' : 's'}
            </span>
          }
        >
          {data?.available === false ? (
            <EmptyState
              title="This install has no provider chain"
              detail={data.unavailable ?? 'The product says it has no chain configured.'}
            />
          ) : providers.length === 0 ? (
            <EmptyState
              title="The site has no named chain"
              detail={
                data?.policyUnavailable ??
                'The site runs the local runtime, which is a supported configuration — a named chain is what lets a site fall back.'
              }
            />
          ) : (
            <ul className="divide-y divide-line">
              {providers.map((p) => (
                <li key={`${p.position}-${p.kind}`} className="flex flex-wrap items-center justify-between gap-3 py-3 first:pt-0 last:pb-0">
                  <div className="flex min-w-0 items-center gap-3">
                    <span className="font-mono text-[11px] text-text-dim">{p.position}</span>
                    <div className="min-w-0">
                      <div className="flex items-center gap-2">
                        <span className="font-mono text-[13px] text-text">{p.kind}</span>
                        {p.model ? (
                          <span className="font-mono text-[11px] text-text-dim">{p.model}</span>
                        ) : null}
                      </div>
                      {p.baseUrl ? (
                        <p className="mt-1 break-all font-mono text-[11px] text-text-dim">
                          {p.baseUrl}
                        </p>
                      ) : null}
                    </div>
                  </div>
                  <Badge tone={p.credentialConfigured ? 'ok' : 'unknown'}>
                    {p.credentialConfigured ? 'credential set' : 'no credential'}
                  </Badge>
                </li>
              ))}
            </ul>
          )}

          {data?.chainAvailable === false ? (
            <p className="mt-4 text-[13px] leading-[20px] text-bad-ink">
              The chain does not build: {data.chainUnavailable}
            </p>
          ) : null}
        </Panel>
      </Section>

      {data?.available !== false && (
        <Section>
          <Panel title="What may leave the machine">
            <KeyValue
              items={[
                {
                  key: 'egress',
                  value: data?.egressAllowed ? 'allowed' : 'refused',
                },
                {
                  key: 'system prompt',
                  value: data?.systemPromptConfigured ? 'configured' : 'none',
                },
                {
                  key: 'deadline',
                  value: data?.timeoutSeconds ? `${data.timeoutSeconds}s per provider` : '—',
                },
              ]}
            />
            <p className="mt-4 text-[13px] leading-[20px] text-text-dim">
              Egress is refused by default and the refusal is enforced in the product, not only
              here. A local model on this machine needs no permission; a hosted one does.
            </p>
          </Panel>
        </Section>
      )}

      <Section>
        <Panel
          title="What left the site"
          action={
            <span className="font-mono text-[11px] text-text-dim">
              {egress.length} record{egress.length === 1 ? '' : 's'}
            </span>
          }
        >
          {egress.length === 0 ? (
            <EmptyState
              title="Nothing has left the site"
              detail="The log records every call to a provider that is not on this machine. It is empty because nothing has been sent."
            />
          ) : (
            <ul className="divide-y divide-line">
              {egress.map((e) => (
                <li key={e.egressId} className="py-3 first:pt-0 last:pb-0">
                  <div className="flex flex-wrap items-baseline justify-between gap-3">
                    <span className="font-mono text-[13px] text-text">{e.provider}</span>
                    <span className="font-mono text-[11px] text-text-dim">{e.requestedAt}</span>
                  </div>
                  <p className="mt-1 text-[13px] leading-[20px] text-text-muted">
                    {e.evidenceCount} piece(s) of evidence, {e.evidenceBytes} bytes, for{' '}
                    <span className="font-mono">{e.observationId}</span>
                  </p>
                  {e.evidenceHashes.length > 0 ? (
                    <p className="mt-1 break-all font-mono text-[11px] text-text-dim">
                      {e.evidenceHashes.join(' ')}
                    </p>
                  ) : null}
                </li>
              ))}
            </ul>
          )}
          <p className="mt-4 text-[13px] leading-[20px] text-text-dim">
            The log carries the DIGESTS of what left and never the content. That is enough to
            answer whether a given piece of evidence is what was sent, and it keeps the log from
            becoming a second copy of the evidence somewhere nobody chose to put it.
          </p>
        </Panel>
      </Section>
    </Page>
  )
}
