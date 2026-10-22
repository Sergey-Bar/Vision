import { useState } from 'react'
import { CheckCircle2, CircleDashed, TriangleAlert } from 'lucide-react'
import { Page, PageHeader } from '@/components/Shell'
import { Panel, Section } from '@/components/ui/Panel'
import { Badge } from '@/components/ui/Badge'
import { EmptyState, ErrorState, LoadingState } from '@/components/ui/States'
import { useSetup } from '@/hooks/product'
import { describeError } from '@/lib/errors'

// The setup path.
//
// "Plug and play" is a promise about the FIRST day, and a promise nobody can
// check is a promise nobody keeps. So the console asks the PRODUCT what is
// left to do, and renders what the product says.
//
// The discipline the page keeps is the product's discipline:
//
//   - every step is ANSWERED by the product, and the page shows the answer
//     rather than deciding;
//   - an unfinished step names the ONE action that would finish it, because a
//     step that is not done and does not say what to do is a complaint;
//   - the steps are ordered by dependency, and the first unfinished one is the
//     one blocking the rest.
export function SetupPage() {
  const { data, isPending, isError, error } = useSetup()
  const [showDone, setShowDone] = useState(true)

  if (isPending) {
    return (
      <Page>
        <PageHeader title="Setup" subtitle="What is left to do" />
        <LoadingState label="Reading the product's own state" />
      </Page>
    )
  }

  if (isError) {
    return (
      <Page>
        <PageHeader title="Setup" subtitle="What is left to do" />
        <ErrorState
          title="The product did not answer"
          detail={
            <>
              <p>
                The console asked the product what is left to do and could not read the answer.
                The list below is empty because the product did not say, not because everything is
                done.
              </p>
              <p className="mt-2 font-mono text-[11px] text-text-dim">{describeError(error)}</p>
            </>
          }
        />
      </Page>
    )
  }

  const steps = data?.steps ?? []
  const pending = steps.filter((s) => !s.done)
  const finished = steps.filter((s) => s.done)

  return (
    <Page>
      <PageHeader
        title="Setup"
        subtitle="What is left to do"
        lede="Every step here is answered by the product — the migration ledger, the sites, the device registry, the provider policy — and every unfinished step names the one action that would finish it."
        actions={
          data ? (
            <Badge tone={data.ready ? 'ok' : 'warn'}>
              {data.ready ? (
                <CheckCircle2 size={12} aria-hidden="true" />
              ) : (
                <TriangleAlert size={12} aria-hidden="true" />
              )}
              {data.doneCount} of {data.totalCount} steps
            </Badge>
          ) : null
        }
      />

      {steps.length === 0 ? (
        <Section>
          <Panel title="The setup path">
            <EmptyState
              title="This install has no setup path to report"
              detail="The product answered with no steps at all, which is a fact about the install rather than about the site."
            />
          </Panel>
        </Section>
      ) : (
        <>
          {pending.length > 0 && (
            <Section>
              <Panel
                title="What is left"
                action={
                  <span className="font-mono text-[11px] text-text-dim">
                    next: {data?.next}
                  </span>
                }
              >
                <ul className="divide-y divide-line">
                  {pending.map((step) => (
                    <li key={step.id} className="py-4 first:pt-0 last:pb-0">
                      <div className="flex flex-wrap items-start justify-between gap-3">
                        <div className="min-w-0">
                          <div className="flex items-center gap-2">
                            <CircleDashed size={15} className="text-text-dim" aria-hidden="true" />
                            <span className="text-[13px] font-medium text-text">{step.title}</span>
                          </div>
                          <p className="mt-1 text-[13px] leading-[20px] text-text-muted">
                            {step.detail}
                          </p>
                        </div>
                      </div>
                      {step.action ? (
                        <p className="mt-3 rounded border border-line bg-surface-lift px-3 py-2 text-[13px] leading-[20px] text-text">
                          <span className="font-medium text-text-dim">Next:</span> {step.action}
                        </p>
                      ) : null}
                    </li>
                  ))}
                </ul>
              </Panel>
            </Section>
          )}

          {finished.length > 0 && (
            <Section>
              <Panel
                title="What is done"
                action={
                  <button
                    type="button"
                    className="font-mono text-[11px] text-text-dim underline underline-offset-2"
                    onClick={() => setShowDone((v) => !v)}
                  >
                    {showDone ? 'hide' : 'show'}
                  </button>
                }
              >
                {showDone ? (
                  <ul className="divide-y divide-line">
                    {finished.map((step) => (
                      <li key={step.id} className="flex flex-wrap items-baseline justify-between gap-3 py-3 first:pt-0 last:pb-0">
                        <span className="flex items-center gap-2 text-[13px] text-text-muted">
                          <CheckCircle2 size={14} className="text-ok-ink" aria-hidden="true" />
                          {step.title}
                        </span>
                        <span className="font-mono text-[11px] text-text-dim">{step.detail}</span>
                      </li>
                    ))}
                  </ul>
                ) : (
                  <p className="text-[13px] leading-[20px] text-text-dim">
                    {finished.length} step{finished.length === 1 ? '' : 's'} are done.
                  </p>
                )}
              </Panel>
            </Section>
          )}
        </>
      )}
    </Page>
  )
}
