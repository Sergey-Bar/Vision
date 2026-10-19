import { useEffect, useState } from 'react'
import { PageHeader, Page } from '@/components/Shell'
import { useCritical, useSearch } from '@/hooks/api'
import { cn } from '@/lib/utils'
import { Search, Filter } from 'lucide-react'
import type { Camera } from '@/api'

function getUploadedVideos(): Camera[] {
  if (typeof window === 'undefined') return []
  try {
    const raw = localStorage.getItem('vision-uploaded-videos')
    if (!raw) return []
    return JSON.parse(raw)
  } catch {
    return []
  }
}

function getUploadedRules(): any[] {
  if (typeof window === 'undefined') return []
  try {
    const raw = localStorage.getItem('vision-uploaded-rules')
    if (!raw) return []
    return JSON.parse(raw)
  } catch {
    return []
  }
}

export function IncidentsPage() {
  const [query, setQuery] = useState('')
  const [debounced, setDebounced] = useState('')
  const { data, isPending, error } = useCritical(10)
  const { data: searchData } = useSearch(debounced, debounced.length > 0)

  useEffect(() => {
    const timer = setTimeout(() => setDebounced(query), 250)
    return () => clearTimeout(timer)
  }, [query])

  const uploadedVideos = getUploadedVideos()
  const uploadedRules = getUploadedRules()

  const generatedIncidents = uploadedVideos.map((cam, i) => {
    const rule = uploadedRules[i % uploadedRules.length] || null
    const severities = ['critical', 'high', 'medium', 'low']
    const states = ['new', 'triaging', 'resolved']
    const severity = severities[i % severities.length]
    const state = states[i % states.length]
    return {
      incidentId: `inc-${cam.id}`,
      title: rule ? `Detection: ${rule.text}` : `Video ingest: ${cam.name}`,
      siteId: cam.siteId,
      severity,
      state,
      ruleId: rule ? rule.text.substring(0, 30) + '…' : 'auto-ingest',
      firstAt: new Date(Date.now() - i * 3600000).toISOString(),
    }
  })

  const allIncidents = [...(data ?? []), ...generatedIncidents]

  return (
    <Page>
      <PageHeader title="Incidents" subtitle="Open and unverified incidents, ranked by severity" />
      <div className="space-y-10">
        <div className="flex flex-col lg:flex-row lg:items-center justify-between gap-8">
          <div className="relative flex-1 max-w-3xl">
            <Search size={19} className="absolute left-6 top-1/2 -translate-y-1/2 text-text-dim" />
            <input
              type="text"
              placeholder="Search incidents (e.g., 'person running', 'last hour')..."
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              className="input pl-16"
            />
            <button
              onClick={() => setDebounced(query)}
              className="absolute right-5 top-1/2 -translate-y-1/2 p-3 text-text-dim hover:text-text transition-colors duration-300"
              aria-label="Search"
            >
              <Search size={16} />
            </button>
            <button
              className="px-5 py-3.5 rounded-3xl border border-white/10 text-text-dim hover:text-text hover:bg-white/5 transition-all duration-300"
              aria-label="Filters"
            >
              <Filter size={19} />
            </button>
          </div>
        </div>

        {isPending && <div className="flex h-[70vh] items-center justify-center text-text-dim font-medium">Loading incidents…</div>}
        {error && <div className="rounded-3xl p-12 bg-apple-red/10 border border-apple-red/30 text-apple-red text-sm font-medium shadow-lg shadow-apple-red/30">Unable to load incidents: {error?.message ?? 'Unknown error'}</div>}

        <div className="border border-white/10 rounded-3xl bg-black/30 backdrop-blur-sm overflow-hidden shadow-2xl">
          <table className="w-full text-left text-sm">
            <thead className="bg-black/40 text-text-dim text-[10px] font-black uppercase tracking-[0.12em] sticky top-0 z-20">
              <tr>
                <th className="px-8 py-5 font-black">First seen</th>
                <th className="px-8 py-5 font-black">Title</th>
                <th className="px-8 py-5 font-black">Site</th>
                <th className="px-8 py-5 font-black">Severity</th>
                <th className="px-8 py-5 font-black">State</th>
                <th className="px-8 py-5 font-black text-right">Rule</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-white/10">
              {allIncidents.map((inc) => (
                <tr key={inc.incidentId} className="hover:bg-white/5 transition-all duration-300 group border-b border-white/5 last:border-0">
                  <td className="px-8 py-5 text-text-dim">{new Date(inc.firstAt).toLocaleString('en-US', { timeZone: 'UTC', hour12: false, month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}</td>
                  <td className="px-8 py-5 text-text font-black">{inc.title}</td>
                  <td className="px-8 py-5 text-text-dim">{inc.siteId}</td>
                  <td className="px-8 py-5">
                    <span className={cn(
                      'inline-flex items-center gap-3 px-4 py-2 rounded-full border text-[10px] font-black',
                      inc.severity === 'critical' ? 'border-apple-red text-apple-red bg-apple-red/10 shadow-lg shadow-apple-red/20' : '',
                      inc.severity === 'high' ? 'border-[#f59e0b] text-[#fbbf24] bg-[#fbbf24]/10 shadow-lg shadow-[#fbbf24]/20' : '',
                      inc.severity === 'medium' ? 'border-warning text-warning bg-warning/10 shadow-lg shadow-warning/20' : '',
                    )}>
                      {inc.severity}
                    </span>
                  </td>
                  <td className="px-8 py-5 text-text-dim">{inc.state}</td>
                  <td className="px-8 py-5 text-text-dim text-right">{inc.ruleId ?? '—'}</td>
                </tr>
              ))}
              {allIncidents.length === 0 && (
                <tr>
                  <td colSpan={6} className="px-8 py-20 text-center text-text-dim">No open incidents</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>

        {debounced.length > 0 && (
          <div className="border border-white/10 rounded-3xl bg-black/30 backdrop-blur-sm overflow-hidden shadow-2xl">
            <table className="w-full text-left text-sm">
              <thead className="bg-black/40 text-text-dim text-[10px] font-black uppercase tracking-[0.12em]">
                <tr>
                  <th className="px-8 py-5 font-black">Time</th>
                  <th className="px-8 py-5 font-black">Camera / Zone</th>
                  <th className="px-8 py-5 font-black">Event</th>
                  <th className="px-8 py-5 font-black">Incident</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-white/10">
                {(searchData?.hits ?? []).map((hit) => (
                  <tr key={hit.eventId} className="hover:bg-white/5 transition-all duration-300 group border-b border-white/5 last:border-0">
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

        {!debounced.length && !query && allIncidents.length === 0 && (
          <div className="empty">
            <div className="empty-icon">
              <div className="w-12 h-12 rounded-full bg-black/60 flex items-center justify-center text-text-dim">
                <Search size={28} />
              </div>
            </div>
            <div className="empty-title">No open incidents</div>
            <div className="empty-subtitle">Upload a video and compile a detection rule to generate incidents.</div>
          </div>
        )}
        <div className="flex items-center justify-between px-4 py-8 border-t border-white/10 bg-black/20 text-[11px] font-black text-text-dim">
          <span>UTC <span id="ts" /></span>
          <div className="flex items-center gap-2">
            <span className="w-3 h-3 rounded-full bg-apple-red animate-pulse-red" />
            <span>All times in UTC</span>
          </div>
        </div>
      </div>
    </Page>
  )
}
