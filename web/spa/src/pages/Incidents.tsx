import { PageHeader, Page } from '@/components/Shell'
import { useCritical } from '@/hooks/api'
import { cn } from '@/lib/utils'
import { AlertCircle } from 'lucide-react'

export function IncidentsPage() {
  const { data, isPending, error } = useCritical(10)

  return (
    <Page>
      <PageHeader title="Incidents" subtitle="Open and unverified incidents, ranked by severity" />
      <div className="space-y-4">
        {isPending && <div className="flex h-64 items-center justify-center text-text-muted">Loading incidents…</div>}
        {error && <div className="text-sm text-danger">Unable to load incidents: {error?.message ?? 'Unknown error'}</div>}
        <div className="border border-border rounded-xl bg-surface overflow-hidden">
          <table className="w-full text-left text-sm">
            <thead className="bg-surface-lift text-text-muted text-xs uppercase tracking-wider sticky top-0 z-10">
              <tr>
                <th className="px-4 py-3">First seen</th>
                <th className="px-4 py-3">Title</th>
                <th className="px-4 py-3">Site</th>
                <th className="px-4 py-3">Severity</th>
                <th className="px-4 py-3">State</th>
                <th className="px-4 py-3 text-right">Rule</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {(data ?? []).map((inc) => (
                <tr key={inc.incidentId} className="hover:bg-surface-lift transition-colors group">
                  <td className="px-4 py-3 text-text-muted">{new Date(inc.firstAt).toLocaleString('en-US', { timeZone: 'UTC', hour12: false, month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}</td>
                  <td className="px-4 py-3 text-text font-medium">{inc.title}</td>
                  <td className="px-4 py-3 text-text-muted">{inc.siteId}</td>
                  <td className="px-4 py-3">
                    <span className={cn(
                      'inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-[10px] font-medium border',
                      inc.severity === 'critical' ? 'border-danger text-danger bg-danger-bg' : '',
                      inc.severity === 'high' ? 'border-[#f59e0b] text-[#f59e0b] bg-[#4a2a1a]' : '',
                    )}>
                      {inc.severity}
                    </span>
                  </td>
                  <td className="px-4 py-3 text-text-muted">{inc.state}</td>
                  <td className="px-4 py-3 text-text-muted text-right">{inc.ruleId ?? '—'}</td>
                </tr>
              ))}
              {(data ?? []).length === 0 && (
                <tr>
                  <td colSpan={6} className="px-4 py-8 text-center text-text-muted">No open incidents</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
        <div className="flex items-center justify-between px-4 py-3 border-t border-border bg-surface/50 text-xs text-text-muted">
          <span>UTC <span id="ts" /></span>
          <div className="flex items-center gap-2">
            <AlertCircle size={14} className="text-text-muted" />
            <span>All times in UTC</span>
          </div>
        </div>
      </div>
    </Page>
  )
}
