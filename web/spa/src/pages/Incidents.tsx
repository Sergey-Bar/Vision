import { useEffect, useState } from 'react'
import { PageHeader, Page } from '@/components/Shell'
import { useSearch } from '@/hooks/api'
import { useIncidents } from '@/hooks/product'
import { describeError } from '@/lib/errors'
import { EmptyState, ErrorState, LoadingState } from '@/components/ui/States'
import { SeverityBadge, StateBadge } from '@/components/ui/Badge'
import { Input } from '@/components/ui/Field'
import { Search } from 'lucide-react'

export function IncidentsPage() {
  const [query, setQuery] = useState('')
  const [debounced, setDebounced] = useState('')
  const { data, isPending, error } = useIncidents(100)
  const { data: searchData } = useSearch(debounced, debounced.length > 0)

  useEffect(() => {
    const timer = setTimeout(() => setDebounced(query), 250)
    return () => clearTimeout(timer)
  }, [query])

  // The incidents are the PRODUCT's. This page used to add rows the BROWSER
  // made up -- one per uploaded video, with the severity and the state taken
  // round-robin from a list and the timestamp taken from the wall clock -- and
  // those rows sat in the same table as the real ones, looking exactly like
  // them. A reader would have taken them for incidents the product raised.
  const incidents = data ?? []

  return (
    <Page>
      <PageHeader title="Incidents" subtitle="The product's incident list, newest first" />
      <div className="space-y-10">
        <div className="flex flex-col lg:flex-row lg:items-center justify-between gap-4">
          <div className="relative flex-1 max-w-3xl">
            <Search size={18} className="absolute left-4 top-1/2 -translate-y-1/2 text-text-dim pointer-events-none" />
            <Input
              type="text"
              placeholder="Search incidents (e.g., 'person running', 'last hour')..."
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              className="pl-11"
              aria-label="Search the incidents"
            />
          </div>
          <button
            onClick={() => setDebounced(query)}
            className="inline-flex h-10 items-center gap-2 rounded border border-line px-4 text-[13px] text-text-muted transition-colors duration-1 ease-out hover:border-line-strong hover:text-text"
          >
            <Search size={15} aria-hidden="true" />
            Search
          </button>
        </div>

        {isPending && <LoadingState label="Reading the incident list" />}
        {error && (
          <ErrorState
            title="The product did not answer with the incident list"
            detail={
              <>
                <p>
                  The console asked the product for the incidents and could not read the
                  answer. The list is empty because the list is unknown, not because the
                  product raised nothing.
                </p>
                <p className="mt-2 font-mono text-[11px] text-text-dim">{describeError(error)}</p>
              </>
            }
          />
        )}

        {!error && (
        <div className="border border-line rounded-lg bg-bg-panel overflow-hidden">
          <table className="w-full text-left text-sm">
          <thead className="bg-bg-panel text-text-dim text-[10px] font-semibold uppercase tracking-[0.12em] sticky top-0 z-20">
              <tr>
                <th className="px-8 py-5 font-semibold">First seen</th>
              <th className="px-8 py-5 font-semibold">Title</th>
              <th className="px-8 py-5 font-semibold">Site</th>
              <th className="px-8 py-5 font-semibold">Severity</th>
              <th className="px-8 py-5 font-semibold">State</th>
              <th className="px-8 py-5 font-semibold text-right">Rule</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-white/10">
            {incidents.map((inc) => (
                <tr key={inc.incidentId} className="hover:bg-bg-elevated transition-colors duration-2 group border-b border-line last:border-0">
                <td className="px-8 py-5 text-text-dim">{new Date(inc.firstEventAt).toLocaleString('en-US', { timeZone: 'UTC', hour12: false, month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}</td>
                <td className="px-8 py-5 text-text font-semibold">{inc.title}</td>
                  <td className="px-8 py-5 text-text-dim">{inc.siteId}</td>
                  <td className="px-8 py-5">
                    <SeverityBadge severity={inc.severity} />
                  </td>
                  <td className="px-8 py-5">
                    <StateBadge state={inc.state} />
                  </td>
                  <td className="px-8 py-5 text-text-dim text-right">{inc.ruleId ?? '—'}</td>
                </tr>
              ))}
              {incidents.length === 0 && (
                <tr>
                  <td colSpan={6} className="px-8 py-20 text-center text-text-dim">The product has raised no incident</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
        )}

        {debounced.length > 0 && (
          <div className="border border-line rounded-lg bg-bg-panel overflow-hidden">
            <table className="w-full text-left text-sm">
            <thead className="bg-bg-panel text-text-dim text-[10px] font-semibold uppercase tracking-[0.12em]">
                <tr>
                  <th className="px-8 py-5 font-semibold">Time</th>
                <th className="px-8 py-5 font-semibold">Camera / Zone</th>
                <th className="px-8 py-5 font-semibold">Event</th>
                <th className="px-8 py-5 font-semibold">Incident</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-white/10">
                {(searchData?.hits ?? []).map((hit) => (
                  <tr key={hit.eventId} className="hover:bg-bg-elevated transition-colors duration-2 group border-b border-line last:border-0">
                    <td className="px-8 py-5 text-text-dim">{new Date(hit.normalizedAt).toLocaleTimeString('en-US', { timeZone: 'UTC', hour12: false })}</td>
                    <td className="px-8 py-5 text-text">{hit.deviceId ?? hit.siteId}</td>
                    <td className="px-8 py-5 text-text">{hit.eventType}</td>
                    <td className="px-8 py-5 text-text-dim">{hit.incidentId ?? '—'}</td>
                  </tr>
                ))}
                {(searchData?.hits ?? []).length === 0 && (
                  <tr>
                    <td colSpan={4} className="px-8 py-20 text-center text-text-dim">No events found</td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        )}

        {!error && !debounced.length && !query && incidents.length === 0 && (
          <EmptyState
            title="The product has raised no incident"
            detail="The incidents on this page are the product's own. An install that has ingested nothing has nothing to show, and the page says so rather than filling itself in."
          />
        )}
        <div className="flex items-center justify-between px-4 py-8 border-t border-line bg-bg-elevated text-[11px] font-semibold text-text-dim">
          <span>UTC <span id="ts" /></span>
          <div className="flex items-center gap-2">
          <span className="w-3 h-3 rounded-full bg-bad-ink" />
            <span>All times in UTC</span>
          </div>
        </div>
      </div>
    </Page>
  )
}
