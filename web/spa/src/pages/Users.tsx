import { PageHeader, Page } from '@/components/Shell'
import { Shield } from 'lucide-react'
import { usePrincipal } from '@/hooks/product'

// Identity.
//
// There are no users here, and the page says so instead of listing four
// invented people.
//
// The console has no login: the API runs as a CONFIGURED principal, and every
// action in the audit ledger is attributed to that principal rather than to a
// person. A page that showed names, avatars and an MFA badge for people who do
// not exist would be a page asserting that a specific human did a specific
// thing, which is the one thing this product must never do.
//
// What the page does show is the truth: the principal, its role, the sites it
// is assigned to, and the capabilities that role holds -- which are the
// capabilities the API's checks are real against.
export function UsersPage() {
  const { data: principal, isError } = usePrincipal()

  return (
    <Page>
      <PageHeader
        title="Identity"
        subtitle="The principal this install acts as. There is no login and no user."
      />
      <div className="space-y-10">
        {isError ? (
          <div className="rounded-lg p-8 bg-bad-ink/10 border border-bad-ink/40 text-bad-ink text-sm font-medium">
            The API is not answering, so the console cannot say who it is acting as.
          </div>
        ) : (
          <>
            <div className="rounded-lg border border-line bg-bg-panel px-8 py-6">
          <div className="text-[10px] font-semibold uppercase tracking-[0.12em] text-text-dim">
                Configured principal
              </div>
              <div className="mt-3 flex items-center gap-4">
              <div className="w-14 h-14 rounded-lg bg-accent flex items-center justify-center text-text font-semibold">
                  SP
                </div>
                <div>
                  <div className="text-lg font-semibold text-text">
                    {(principal?.roles ?? []).join(', ') || 'unconfigured'}
                  </div>
                  <div className="text-xs text-text-dim mt-1">
                  tenant {principal?.tenantId ?? '—'} · service account
                  </div>
                </div>
              </div>
              <div className="mt-5 text-xs text-text-muted leading-relaxed max-w-2xl">
                Every request the console makes runs under this principal, and every row it
                writes to the ledger names it. There is no session, no password and no second
                factor, because there is no person to authenticate.
              </div>
            </div>

            <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
            <div className="rounded-lg border border-line bg-bg-panel px-8 py-6">
            <div className="text-[10px] font-semibold uppercase tracking-[0.12em] text-text-dim mb-4">
                  Capabilities
                </div>
                <div className="flex flex-wrap gap-2">
                  {(principal?.capabilities ?? []).map((c) => (
                    <span
                      key={c}
                      className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full border border-line text-[10px] font-semibold text-text-dim"
                    >
                      <Shield size={12} className="text-accent" />
                      {c}
                    </span>
                  ))}
                  {(principal?.capabilities ?? []).length === 0 && (
                    <span className="text-xs text-text-dim">
                      The role holds no capabilities, so the API will refuse everything.
                    </span>
                  )}
                </div>
              </div>

              <div className="rounded-lg border border-line bg-bg-panel px-8 py-6">
                <div className="text-[10px] font-semibold uppercase tracking-[0.12em] text-text-dim mb-4">
                  Site scope
                </div>
                <div className="flex flex-wrap gap-2">
                  {(principal?.siteScope ?? []).map((s) => (
                    <span
                      key={s}
                      className="px-3 py-1.5 rounded-full border border-accent/40 bg-accent/10 text-[10px] font-semibold text-accent"
                    >
                      {s}
                    </span>
                  ))}
                  {(principal?.siteScope ?? []).length === 0 && (
                    <span className="text-xs text-text-dim">
                      Every site. The core is single-site per install, so this is the normal
                      case.
                    </span>
                  )}
                </div>
              </div>
            </div>
          </>
        )}
      </div>
    </Page>
  )
}
