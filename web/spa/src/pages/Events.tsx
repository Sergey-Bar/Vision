import { useState } from 'react'
import { PageHeader, Page } from '@/components/Shell'
import { useSearch } from '@/hooks/api'
import { Search, Filter, Clock, ChevronRight } from 'lucide-react'
import { cn } from '@/lib/utils'

const ENTITY_CHIPS: { label: string; q: string; color: string }[] = [
  { label: 'Person', q: 'person', color: 'text-accent border-accent/30 bg-accent/10 shadow-xl shadow-accent/40' },
  { label: 'Vehicle', q: 'vehicle', color: 'text-[#fbbf24] border-[#fbbf24]/30 bg-[#fbbf24]/10 shadow-xl shadow-[#fbbf24]/30' },
  { label: 'Animal', q: 'animal', color: 'text-[#4ade80] border-[#4ade80]/30 bg-[#4ade80]/10 shadow-xl shadow-[#4ade80]/30' },
  { label: 'Bag', q: 'bag', color: 'text-[#fb7185] border-[#fb7185]/30 bg-[#fb7185]/10 shadow-xl shadow-[#fb7185]/30' },
]

export function EventsPage() {
  const [query, setQuery] = useState('')
  const [chipQuery, setChipQuery] = useState('')
  const { data, isPending, error } = useSearch(query || chipQuery, (query || chipQuery).length > 0)

  return (
    <Page>
      <PageHeader title="Events" subtitle="Chronological event timeline with free-text search" />
      <div className="space-y-10">
        <div className="flex flex-col lg:flex-row lg:items-center justify-between gap-8">
          <div className="relative flex-1 max-w-3xl">
            <Search size={19} className="absolute left-6 top-1/2 -translate-y-1/2 text-text-dim" />
            <input
              className="input pl-16"
              placeholder="Search events (e.g., 'person', 'vehicle', 'bag')..."
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
            <button
              className="absolute right-5 top-1/2 -translate-y-1/2 p-3 text-text-dim hover:text-text transition-colors duration-300"
              onClick={() => setQuery('')}
              aria-label="Clear"
            >
              <ChevronRight size={16} />
            </button>
          </div>
          <div className="flex items-center gap-5 w-full max-w-md">
            <button className="px-5 py-3.5 rounded-3xl border border-white/10 text-text-dim hover:text-text hover:bg-white/5 transition-all duration-300">
              <Filter size={19} />
            </button>
          </div>
        </div>

        <div className="flex gap-4 overflow-x-auto pb-4 scrollbar-hide">
          {ENTITY_CHIPS.map((chip) => (
            <button
              key={chip.label}
              onClick={() => {
                setChipQuery(chip.q)
                setQuery('')
              }}
              className={cn(
                'px-5 py-2.5 rounded-full border text-[11px] font-black transition-all duration-200 whitespace-nowrap shrink-0',
                chipQuery === chip.label.toLowerCase()
                  ? `${chip.color}`
                  : 'border-white/10 text-text-dim hover:text-text hover:bg-white/5',
              )}
            >
              {chip.label}
            </button>
          ))}
        </div>

        {isPending && <div className="flex h-[70vh] items-center justify-center text-text-dim font-medium">Loading events…</div>}
        {error && <div className="rounded-3xl p-12 bg-danger/10 border border-danger/30 text-danger text-sm font-medium shadow-lg shadow-danger/30">Unable to load events: {error?.message ?? 'Unknown error'}</div>}

        <div className="border border-white/10 rounded-3xl bg-black/30 backdrop-blur-sm overflow-hidden shadow-2xl">
          <table className="w-full text-left text-sm">
            <thead className="bg-black/40 text-text-dim text-[10px] font-black uppercase tracking-[0.12em]">
              <tr>
                <th className="px-8 py-5 font-black">Time</th>
                <th className="px-8 py-5 font-black">Camera / Zone</th>
                <th className="px-8 py-5 font-black">Event</th>
                <th className="px-8 py-5 font-black">Severity</th>
                <th className="px-8 py-5 font-black">Incident</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-white/10">
              {(data?.hits ?? []).map((hit) => (
                <tr key={hit.eventId} className="hover:bg-white/5 transition-all duration-300 group border-b border-white/5 last:border-0">
                  <td className="px-8 py-5 text-text-dim">{new Date(hit.normalizedAt).toLocaleTimeString('en-US', { timeZone: 'UTC', hour12: false })}</td>
                  <td className="px-8 py-5 text-text">{hit.deviceId ?? hit.siteId}</td>
                  <td className="px-8 py-5 text-text">{hit.eventType}</td>
                  <td className="px-8 py-5">
                    <span className="inline-flex items-center gap-3 px-4 py-2 rounded-full border border-white/10 text-[10px] font-black text-text-dim">
                      <span className="w-2.5 h-2.5 rounded-full bg-accent" /> {hit.correlationId ?? '—'}
                    </span>
                  </td>
                  <td className="px-8 py-5 text-text-dim">{hit.incidentId ?? '—'}</td>
                </tr>
              ))}
              {(data?.hits ?? []).length === 0 && query.length > 0 && (
                <tr>
                  <td colSpan={5} className="px-8 py-20 text-center text-text-dim">No events found</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>

        {!query && !chipQuery && (
          <div className="empty">
            <div className="empty-icon">
              <Clock size={40} />
            </div>
            <div className="empty-title">Run a search</div>
            <div className="empty-subtitle">Search for people, vehicles, animals, or bags in the event record.</div>
          </div>
        )}
      </div>
    </Page>
  )
}
