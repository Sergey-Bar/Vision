import { PageHeader, Page } from '@/components/Shell'
import { useAuditLedger } from '@/hooks/product'

// The ledger.
//
// It is the PRODUCT's ledger, read over the API: the append-only,
// hash-chained record of what the system did. The previous version of this page
// listed eight invented rows with invented actors and invented hashes, which is
// the worst kind of fake: it looks exactly like the real thing, and a reader
// would take it as evidence that the system records what it does.
//
// The actor column says who acted. With the console's configured principal, that
// is the principal -- not a person, because there is no person.
export function AuditPage() {
  const { data: entries, isError } = useAuditLedger(50)

  return (
    <Page>
      <PageHeader title="Audit Log" subtitle="Append-only, hash-chained ledger" />
      <div className="space-y-10">
        {isError ? (
          <div className="rounded-2xl p-8 bg-danger/10 border border-danger/30 text-danger text-sm font-medium">
            The ledger is not answering. The console cannot show it, and it will not invent it.
          </div>
        ) : (
          <div className="border border-white/10 rounded-3xl bg-black/30 backdrop-blur-sm overflow-hidden shadow-2xl">
            <table className="w-full text-left text-sm">
              <thead className="bg-black/40 text-text-dim text-[10px] font-black uppercase tracking-[0.12em] sticky top-0 z-20">
                <tr>
                  <th className="px-8 py-5 font-black">Time</th>
                  <th className="px-8 py-5 font-black">Actor</th>
                  <th className="px-8 py-5 font-black">Action</th>
                  <th className="px-8 py-5 font-black">Target</th>
                  <th className="px-8 py-5 font-black text-right">Hash</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-white/10">
                {(entries ?? []).map((raw, i) => {
                  const e = raw as {
                    sequence?: number
                    ts?: string
                    actor?: string
                    actorType?: string
                    action?: string
                    targetType?: string
                    targetId?: string
                    resultStateHash?: string
                    reason?: string
                  }
                  return (
                    <tr
                      key={e.sequence ?? i}
                      className="hover:bg-white/5 transition-all duration-300 group border-b border-white/5 last:border-0"
                    >
                      <td className="px-8 py-5 text-text-dim">
                        {e.ts ? new Date(e.ts).toLocaleString() : '—'}
                      </td>
                      <td className="px-8 py-5 text-text-dim">
                        {e.actor ?? '—'}
                        {e.actorType ? (
                          <span className="ml-2 text-[10px] font-black text-text-dim/70">{e.actorType}</span>
                        ) : null}
                      </td>
                      <td className="px-8 py-5 text-text">{e.action ?? '—'}</td>
                      <td className="px-8 py-5 text-text-dim">
                        {e.targetId ?? e.targetType ?? '—'}
                      </td>
                      <td className="px-8 py-5 text-text-dim text-right font-mono text-xs">
                        {e.resultStateHash ? `${e.resultStateHash.slice(0, 8)}…` : '—'}
                      </td>
                    </tr>
                  )
                })}
                {(entries ?? []).length === 0 && (
                  <tr>
                    <td colSpan={5} className="px-8 py-8 text-text-dim">
                      The ledger is empty. Nothing has been recorded yet, which is a fact
                      about the system rather than a missing page.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        )}
        <div className="px-8 py-5 border-t border-white/10 bg-black/20 text-[11px] font-black text-text-dim">
          Entries are append-only and hash-chained. Editing any row fails checkpoint verification.
        </div>
      </div>
    </Page>
  )
}
