import { Page, PageHeader } from '@/components/Shell'
import { Panel, Section } from '@/components/ui/Panel'
import { Badge } from '@/components/ui/Badge'
import { Table } from '@/components/ui/Table'
import { EmptyState, ErrorState, LoadingState } from '@/components/ui/States'
import { Button } from '@/components/ui/Button'
import { useRuleBundles } from '@/hooks/product'
import { formatSiteTime } from '@/lib/site'

// The rules.
//
// STORE is not ACTIVATE.
//
// A bundle on disk is a proposal. A bundle that is running is a decision, and
// the difference between the two is the whole point of the screen: an
// operator who cannot tell them apart will believe that a rule that was
// written is a rule that is running.
//
// The screen therefore has two blocks, never one table:
//
//   the bundles that are stored
//   the bundle that is active
//
// and the second one is named as such, in the heading.
export function RulesPage() {
  const bundles = useRuleBundles()

  if (bundles.isPending) {
    return (
      <Page>
        <PageHeader title="Rules" />
        <LoadingState label="Reading the rule bundles" />
      </Page>
    )
  }

  if (bundles.isError) {
    return (
      <Page>
        <PageHeader title="Rules" />
        <ErrorState
          title="The product did not answer with the rule bundles"
          detail="The rules are the product's, and the console does not keep a copy of them. Without the product's answer there is no way to know which bundle is running."
        />
      </Page>
    )
  }

  const stored = bundles.data
  const active = stored.find((b) => b.active)

  return (
    <Page>
      <PageHeader
        eyebrow="Rules"
        title="The rules that are running, and the ones that are not"
        lede="A stored bundle is a proposal on disk. An active bundle is the one the product is running. The console shows both and never merges them."
      />

      <Section title="The active bundle">
        <Panel>
          {active ? (
            <div className="flex flex-wrap items-center gap-4">
              <Badge tone="ok">
                <span aria-hidden="true">●</span> running
              </Badge>
              <span className="font-mono text-[14px] text-text">{active.version}</span>
              {active.ruleCount !== undefined && (
                <span className="text-[13px] text-text-dim">{active.ruleCount} rules</span>
              )}
              {active.activatedAt && (
                <span className="text-[13px] text-text-dim">
                  since {formatSiteTime(active.activatedAt)}
                </span>
              )}
              {active.digest && (
                <span className="font-mono text-[11px] text-text-dim break-all">{active.digest}</span>
              )}
            </div>
          ) : (
            <EmptyState
              title="No bundle is active"
              detail="The product has bundles on disk and none of them is running. That is a different answer from having no bundles at all, and it is a state an operator has to be able to see."
            />
          )}
        </Panel>
      </Section>

      <Section title="The bundles that are stored">
        <Panel>
          {stored.length > 0 ? (
            <Table
              rows={stored}
              rowKey={(b) => b.version}
              columns={[
                {
                  key: 'version',
                  header: 'Version',
                  render: (b) => <span className="font-mono text-[12px] text-text">{b.version}</span>,
                },
                {
                  key: 'state',
                  header: 'State',
                  render: (b) =>
                    b.active ? (
                      <Badge tone="ok">
                        <span aria-hidden="true">●</span> running
                      </Badge>
                    ) : (
                      <Badge tone="neutral">
                        <span aria-hidden="true">○</span> stored
                      </Badge>
                    ),
                },
                {
                  key: 'rules',
                  header: 'Rules',
                  numeric: true,
                  render: (b) => (b.ruleCount ?? '—'),
                },
                {
                  key: 'activated',
                  header: 'Activated',
                  render: (b) => (b.activatedAt ? formatSiteTime(b.activatedAt) : '—'),
                },
                {
                  key: 'digest',
                  header: 'Digest',
                  render: (b) => (
                    <span className="font-mono text-[11px] text-text-dim break-all">
                      {b.digest ?? '—'}
                    </span>
                  ),
                },
              ]}
            />
          ) : (
            <EmptyState
              title="No bundles are stored"
              detail="The product has no rule bundle on disk. Rules are written by a human and activated by a human; the console does neither."
              action={
                <Button variant="secondary" disabled>
                  Write a bundle
                </Button>
              }
            />
          )}
        </Panel>
      </Section>
    </Page>
  )
}
