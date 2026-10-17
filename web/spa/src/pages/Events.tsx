import { useState } from 'react'
import { PageHeader, Page } from '@/components/Shell'
import { useSearch } from '@/hooks/api'
import { Search, Filter, Clock, ChevronRight } from 'lucide-react'

export function EventsPage() {
  const [query, setQuery] = useState('')
  const { data, isPending, error } = useSearch(query, query.length > 0)

  return (
    <Page>
      <PageHeader title="Events" subtitle="Chronological event timeline" />
      <div className="space-y-4">
        <div className="flex items-center gap-3 max-w-lg">
          <div className="relative flex-1">
            <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-text-muted" />
            <input
              className="input pl-10"
              placeholder="Search events…"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
            <button
              className="absolute right-2 top-1/2 -translate-y-1/2 p-1 text-text-muted hover:text-text"
              onClick={() => setQuery('')}
              aria-label="Clear"
            >
              <ChevronRight size={14} />
            </button>
          </div>
          <button className="px-3 py-2 rounded-md border border-border text-text-muted hover:bg-surface-lift transition-colors">
            <Filter size={16} />
          </button>
        </div>
        {isPending && <div className="flex h-64 items-center justify-center text-text-muted">Loading events…</div>}
        {error && <div className="text-sm text-danger">Unable to load events: {error?.message ?? 'Unknown error'}</div>}
        <div className="border border-border rounded-xl bg-surface overflow-hidden">
          <table className="w-full text-left text-sm">
            <thead className="bg-surface-lift text-text-muted text-xs uppercase tracking-wider">
              <tr>
                <th className="px-4 py-3">Time</th>
                <th className="px-4 py-3">Camera / Zone</th>
                <th className="px-4 py-3">Event</th>
                <th className="px-4 py-3">Severity</th>
                <th className="px-4 py-3">Incident</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {(data?.hits ?? []).map((hit) => (
                <tr key={hit.eventId} className="hover:bg-surface-lift transition-colors group">
                  <td className="px-4 py-3 text-text-muted">{new Date(hit.normalizedAt).toLocaleTimeString('en-US', { timeZone: 'UTC', hour12: false })}</td>
                  <td className="px-4 py-3 text-text">{hit.deviceId ?? hit.siteId}</td>
                  <td className="px-4 py-3 text-text">{hit.eventType}</td>
                  <td className="px-4 py-3">
                    <span className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full border text-[10px] font-medium border-border text-text-muted">
                      <span className="w-1.5 h-1.5 rounded-full bg-accent" /> {hit.correlationId ?? '—'}
                    </span>
                  </td>
                  <td className="px-4 py-3 text-text-muted">{hit.incidentId ?? '—'}</td>
                </tr>
              ))}
              {(data?.hits ?? []).length === 0 && query.length > 0 && (
                <tr>
                  <td colSpan={5} className="px-4 py-8 text-center text-text-muted">No events found</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
        {!query && (
          <div className="flex flex-col items-center justify-center py-20 text-text-muted">
            <Clock size={32} className="mb-3 opacity-50" />
            <p className="text-sm">Run a search to see events from the record.</p>
          </div>
        )}
      </div>
    </Page>
  )
}
