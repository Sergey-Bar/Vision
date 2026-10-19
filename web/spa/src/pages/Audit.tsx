import { PageHeader, Page } from '@/components/Shell'

export function AuditPage() {
  return (
    <Page>
      <PageHeader title="Audit Log" subtitle="Append-only, hash-chained ledger" />
      <div className="space-y-10">
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
              {[...Array(8).keys()].map((i) => (
                <tr key={i} className="hover:bg-white/5 transition-all duration-300 group border-b border-white/5 last:border-0">
                  <td className="px-8 py-5 text-text-dim">{new Date(Date.now() - i * 900000).toLocaleString('en-US', { timeZone: 'UTC', hour12: false, hour: '2-digit', minute: '2-digit' })}</td>
                  <td className="px-8 py-5 text-text-dim">operator</td>
                  <td className="px-8 py-5 text-text">clip.export</td>
                  <td className="px-8 py-5 text-text-dim">clip-00{i}</td>
                  <td className="px-8 py-5 text-text-dim text-right font-mono text-xs">a1b2…{i}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <div className="px-8 py-5 border-t border-white/10 bg-black/20 text-[11px] font-black text-text-dim">
          Entries are append-only and hash-chained. Editing any row fails checkpoint verification.
        </div>
      </div>
    </Page>
  )
}
