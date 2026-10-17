import { PageHeader, Page } from '@/components/Shell'

export function AuditPage() {
  return (
    <Page>
      <PageHeader title="Audit Log" subtitle="Append-only, hash-chained ledger" />
      <div className="space-y-4">
        <div className="border border-border rounded-xl bg-surface overflow-hidden">
          <table className="w-full text-left text-sm">
            <thead className="bg-surface-lift text-text-muted text-xs uppercase tracking-wider sticky top-0 z-10">
              <tr>
                <th className="px-4 py-3">Time</th>
                <th className="px-4 py-3">Actor</th>
                <th className="px-4 py-3">Action</th>
                <th className="px-4 py-3">Target</th>
                <th className="px-4 py-3 text-right">Hash</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {[...Array(8).keys()].map((i) => (
                <tr key={i} className="hover:bg-surface-lift transition-colors group">
                  <td className="px-4 py-3 text-text-muted">{new Date(Date.now() - i * 900000).toLocaleString('en-US', { timeZone: 'UTC', hour12: false, hour: '2-digit', minute: '2-digit' })}</td>
                  <td className="px-4 py-3 text-text-muted">operator</td>
                  <td className="px-4 py-3 text-text">clip.export</td>
                  <td className="px-4 py-3 text-text-muted">clip-00{i}</td>
                  <td className="px-4 py-3 text-text-muted text-right font-mono text-xs">a1b2…{i}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <div className="px-4 py-3 border-t border-border bg-surface/50 text-xs text-text-muted">
          Entries are append-only and hash-chained. Editing any row fails checkpoint verification.
        </div>
      </div>
    </Page>
  )
}
