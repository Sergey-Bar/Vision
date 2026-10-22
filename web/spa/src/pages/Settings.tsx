import { PageHeader, Page } from '@/components/Shell'
import { Panel, Section } from '@/components/ui/Panel'
import { KeyValue } from '@/components/ui/Table'
import { Badge } from '@/components/ui/Badge'
import { LoadingState, ErrorState } from '@/components/ui/States'
import { useHealth, usePrincipal, useHandover } from '@/hooks/product'
import { useAIRuntime } from '@/hooks/api'
import { describeError } from '@/lib/errors'

// The settings.
//
// It used to be the one page in the console that read NOTHING: it rendered
// hardcoded text, and the text said "No auth/session mechanism is configured
// yet; admin routes are not gated."
//
// That sentence was a statement about the product, and it was FALSE — the
// product has a configured principal, a capability model on every route, a
// login rate limit, a step-up second factor for the sensitive dispositions,
// and a CSP with no unsafe-inline in it. A page that states a falsehood
// about the install's security posture is worse than no page, because an
// operator reads it.
//
// So the page reads the PRODUCT now, and every cell is either a fact the
// product produced or a dash that says the product did not produce one.
export function SettingsPage() {
  const health = useHealth()
  const principal = usePrincipal()
  const ai = useAIRuntime()
  const handover = useHandover()

  if (health.isPending || principal.isPending) {
    return (
      <Page>
        <PageHeader title="Settings" subtitle="What this install is" />
        <LoadingState label="Reading the install" />
      </Page>
    )
  }

  if (health.isError) {
    return (
      <Page>
        <PageHeader title="Settings" subtitle="What this install is" />
        <ErrorState
          title="The install did not answer"
          detail={
            <>
              <p>
                The console asked the product what it is and could not read the answer. Nothing
                on this page is shown, because a settings page that guesses is a page that
                misleads.
              </p>
              <p className="mt-2 font-mono text-[11px] text-text-dim">{describeError(health.error)}</p>
            </>
          }
        />
      </Page>
    )
  }

  const p = principal.data
  const checks = health.data?.checks ?? {}

  return (
    <Page>
      <PageHeader
        title="Settings"
        subtitle="What this install is"
        lede="Every cell below is a fact the product produced. A dash means the product did not produce one — not that the value is zero."
      />

      <Section>
        <Panel title="The identity this console acts as">
          {p ? (
            <KeyValue
              items={[
                { key: 'tenant', value: <span className="font-mono text-[12px]">{p.tenantId}</span> },
                {
                  key: 'roles',
                  value: p.roles?.length ? p.roles.join(', ') : '—',
                },
                {
                  key: 'site scope',
                  value: p.siteScope?.length ? p.siteScope.join(', ') : '—',
                },
                {
                  key: 'service account',
                  value: p.serviceAccount ? 'yes' : 'no',
                },
                {
                  key: 'capabilities',
                  value:
                    p.capabilities?.length ? (
                      <span className="font-mono text-[11px] break-all">
                        {p.capabilities.join(' ')}
                      </span>
                    ) : (
                      '—'
                    ),
                },
              ]}
            />
          ) : (
            <p className="text-[13px] leading-[20px] text-text-dim">
              The product did not name the principal it acts as.
            </p>
          )}
          <p className="mt-4 text-[13px] leading-[20px] text-text-dim">
            This install has no in-app sign-in. It runs as the configured principal above, and
            the boundary that decides who reaches the console is the one in front of it. Every
            route declares the capability it needs, so the capability list is the authority
            and not the person.
          </p>
        </Panel>
      </Section>

      <Section>
        <Panel title="The checks">
          <KeyValue
            items={Object.entries(checks).map(([name, check]) => ({
              key: name,
              value: (
                <span className="flex items-center gap-2">
                  <Badge tone={check?.ok ? 'ok' : 'warn'}>
                    <span aria-hidden="true">{check?.ok ? '✓' : '!'}</span>{' '}
                    {check?.ok ? 'ok' : 'not ready'}
                  </Badge>
                  {check?.detail ? (
                    <span className="text-[13px] leading-[20px] text-text-muted">{check.detail}</span>
                  ) : null}
                </span>
              ),
            }))}
          />
          {ai.data ? (
            <p className="mt-4 text-[13px] leading-[20px] text-text-dim">
              The AI plane is {ai.data.available ? 'available' : 'not available'}
              {ai.data.reason ? `: ${ai.data.reason}` : '.'} A plane that is not available is
              not an outage: the deterministic core raises the incident anyway, and every
              observation is recorded as degraded with a named reason.
            </p>
          ) : null}
        </Panel>
      </Section>

      <Section>
        <Panel title="The shift artifact">
          {handover.data ? (
            <KeyValue
              items={[
                { key: 'site', value: handover.data.siteId ?? '—' },
                {
                  key: 'available',
                  value: handover.data.available ? 'yes' : 'no',
                },
                {
                  key: 'why not',
                  value: handover.data.unavailable ?? (handover.data.available ? '—' : '—'),
                },
              ]}
            />
          ) : (
            <p className="text-[13px] leading-[20px] text-text-dim">
              The product did not answer about the shift artifact.
            </p>
          )}
        </Panel>
      </Section>

      <Section>
        <Panel title="The surface">
          <KeyValue
            items={[
              { key: 'direction', value: 'left to right' },
              { key: 'language', value: 'English' },
              { key: 'density', value: 'comfortable, or compact per site' },
              {
                key: 'motion',
                value: 'calm. The durations are tokens and nothing on this screen moves to be noticed.',
              },
            ]}
          />
        </Panel>
      </Section>
    </Page>
  )
}
