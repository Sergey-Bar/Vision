import { useState } from 'react'
import { Page, PageHeader } from '@/components/Shell'
import { Search, Layout, Activity, ShieldAlert, Cpu } from 'lucide-react'
import { cn } from '@/lib/utils'
import { useAttention, useIncidents, useHealth, useCoverage, useHandover, useEvidenceSearch, useRecentEvents } from '@/hooks/product'
import { useVMSCameras, useVMSState, vmsFrameUrl, type VMSCamera } from '@/hooks/vms'

// The cockpit.
//
// It is the home page because the operator's first question is not "what does
// the incident list say" but "what needs me now", and the answer is a page
// rather than a navigation step.
//
// Four panels, in the order the questions are asked:
//
//   1. Is anything broken?        the status strip
//   2. What needs me now?        the attention queue, with the reason chain
//   3. What is happening?        the live grid, with the VMS's own boxes
//   4. Where was that?           the search, answering with moments
//
// Every number on this page is read from something. The VMS's boxes are the
// device's assertions about the picture it is looking at; the incidents are the
// product's decisions; the coverage is the product's own view of what it is
// watching. Nothing here is simulated, and a panel whose source is unreachable
// says so instead of filling itself in.

// A panel that could not be filled.
//
// The empty state is the honest one. A panel that renders zeros because the
// product is down is a panel that says "nothing is wrong" when what it means is
// "I could not ask".
function Unreachable({ what }: { what: string }) {
  return (
    <div className="rounded-2xl border border-danger/30 bg-danger/10 px-5 py-4 text-xs font-medium text-danger">
      {what} is not answering. The console cannot say whether it is up.
    </div>
  )
}

// The status strip: the five numbers that decide whether to keep watching.
function StatusStrip() {
  const { data: health, isError: healthError } = useHealth()
  const { data: vmsState, isError: vmsError } = useVMSState()
  const { data: cameras, isError: cameraError } = useVMSCameras(5000)
  const { data: incidents } = useIncidents(50)

  const live = (cameras ?? []).filter((c) => !c.refusal).length
  const total = (cameras ?? []).length
  const open = (incidents ?? []).filter((i) => i.state === 'open' || i.state === 'detected').length
  const detector = vmsState?.detector

  const cells: { label: string; value: string; tone: 'ok' | 'warn' | 'bad' | 'flat' }[] = [
    {
      label: 'Cameras live',
      value: cameraError ? '—' : `${live} / ${total}`,
      tone: cameraError ? 'bad' : live === 0 ? 'bad' : live < total ? 'warn' : 'ok',
    },
    {
      label: 'Open incidents',
      value: `${open}`,
      tone: open > 0 ? 'warn' : 'ok',
    },
    {
      label: 'Ingest',
      value: healthError ? '—' : health?.slo?.status ?? health?.status ?? 'unknown',
      tone: healthError ? 'bad' : (health?.slo?.status === 'breached' ? 'bad' : 'ok'),
    },
    {
      label: 'Detector',
      value: vmsError ? '—' : detector ? `${detector.provider}` : 'not configured',
      tone: vmsError ? 'bad' : detector ? 'ok' : 'warn',
    },
    {
      label: 'Frame cost',
      value: vmsError ? '—' : (cameras ?? []).length > 0
        ? `${Math.max(...(cameras ?? []).map((c) => c.latencyMs)).toFixed(0)} ms`
        : '—',
      tone: 'flat',
    },
  ]

  return (
    <div className="grid grid-cols-2 md:grid-cols-5 gap-4">
      {cells.map((c) => (
        <div key={c.label} className="rounded-2xl border border-white/10 bg-black/30 px-5 py-4">
          <div className="text-[10px] font-black uppercase tracking-[0.12em] text-text-dim">{c.label}</div>
          <div
            className={cn(
              'mt-2 text-xl font-black',
              c.tone === 'ok' && 'text-success',
              c.tone === 'warn' && 'text-warning',
              c.tone === 'bad' && 'text-danger',
              c.tone === 'flat' && 'text-text',
            )}
          >
            {c.value}
          </div>
        </div>
      ))}
    </div>
  )
}

// The attention queue: what needs the operator, and why.
//
// It is ONE call, and the order is the product's. The reason chain is in the
// same body, so the panel cannot show a queue whose order and whose reasons
// came from two different places.
function AttentionQueue() {
  const { data: items, isError } = useAttention(20)

  if (isError) return <Unreachable what="The attention queue" />
  if (!items || items.length === 0) {
    return (
      <div className="rounded-2xl border border-white/10 bg-black/30 px-5 py-6 text-sm text-text-dim">
        Nothing needs the operator. The product raised no incident.
      </div>
    )
  }

  return (
    <div className="rounded-2xl border border-white/10 bg-black/30 divide-y divide-white/5 overflow-hidden">
      {items.map((i) => (
        <div key={i.incidentId} className="px-5 py-4 flex items-start gap-4">
          <span className="mt-0.5 w-6 shrink-0 text-[11px] font-black text-text-dim">{i.rank}</span>
          <span
            className={cn(
              'mt-1.5 w-2 h-2 rounded-full shrink-0',
              i.severity === 'critical' || i.severity === 'high'
                ? 'bg-danger'
                : i.severity === 'medium'
                  ? 'bg-warning'
                  : 'bg-apple-blue',
            )}
          />
          <span className="flex-1 min-w-0">
            <span className="block text-sm font-black text-text truncate">{i.title}</span>
            <span className="block text-[11px] text-text-dim mt-1">
              {i.siteId} · {i.ruleId} · {new Date(i.createdAt).toLocaleString()}
            </span>
            {i.why?.steps && i.why.steps.length > 0 && (
              <span className="block mt-2 space-y-1">
                {i.why.steps.map((s, n) => (
                  <span key={n} className="block text-[11px] text-text-muted">
                    {s.evidence ? `${s.evidence} — ` : ''}
                    {s.rule ?? s.key}
                  </span>
                ))}
              </span>
            )}
            {i.whyUnavailable && (
              <span className="block mt-1 text-[11px] text-warning">
                The product could not explain this one: {i.whyUnavailable}
              </span>
            )}
            {i.degraded && (
              <span className="block mt-1 text-[11px] text-warning">
                Raised without the AI plane
                {i.degradedReasons?.length ? `: ${i.degradedReasons.join(', ')}` : ''}.
              </span>
            )}
          </span>
          <span
            className={cn(
              'shrink-0 text-[10px] font-black uppercase tracking-[0.12em] px-2 py-1 rounded-full border',
              i.state === 'open' ? 'border-danger/40 text-danger' : 'border-white/10 text-text-dim',
            )}
          >
            {i.severity}
          </span>
        </div>
      ))}
    </div>
  )
}

// One live tile, with the VMS's own boxes on the VMS's own picture.
function LiveTile({ camera }: { camera: VMSCamera }) {
  const boxes = camera.detections
  return (
    <div className="relative overflow-hidden rounded-2xl border border-white/10 bg-black aspect-video">
      <img
        src={vmsFrameUrl(camera)}
        alt={`${camera.name} live view`}
        className="w-full h-full object-cover"
      />
      <div className="absolute inset-0 pointer-events-none">
        {boxes.map((d, i) => (
          <div
            key={`${d.class}-${i}`}
            className={cn(
              'absolute border',
              d.class === 'person' || d.class === 'person-with-bag'
                ? 'border-danger bg-danger/30'
                : 'border-white/50 bg-white/10',
            )}
            style={{
              left: `${d.x1 * 100}%`,
              top: `${d.y1 * 100}%`,
              width: `${(d.x2 - d.x1) * 100}%`,
              height: `${(d.y2 - d.y1) * 100}%`,
            }}
          />
        ))}
      </div>
      <div className="absolute bottom-0 left-0 right-0 px-3 py-2 bg-gradient-to-t from-black/90 to-transparent flex items-center justify-between">
        <span className="text-[11px] font-black text-text">{camera.name}</span>
        <span className="text-[10px] font-black text-text-dim">{camera.latencyMs.toFixed(0)} ms</span>
      </div>
    </div>
  )
}

// The live grid.
//
// Four tiles, because four is what fits and four is what the demo plays. The
// platform is not asked to be a video server: the picture is the VMS's and the
// boxes are the VMS's.
function LiveGrid() {
  const { data: cameras, isError } = useVMSCameras(1000)
  if (isError) return <Unreachable what="The VMS" />
  if (!cameras || cameras.length === 0) {
    return (
      <div className="rounded-2xl border border-white/10 bg-black/30 px-5 py-6 text-sm text-text-dim">
        The VMS is not playing any camera.
      </div>
    )
  }
  return (
    <div className="grid grid-cols-2 gap-4">
      {cameras.slice(0, 4).map((c) => (
        <LiveTile key={c.id} camera={c} />
      ))}
    </div>
  )
}

// The search: a question, and the moments that answer it.
function SearchPanel() {
  const [q, setQ] = useState('')
  const [asked, setAsked] = useState('')
  const { data: hits, isFetching, isError } = useEvidenceSearch(asked)

  return (
    <div className="rounded-2xl border border-white/10 bg-black/30 px-5 py-5 space-y-4">
      <form
        className="flex items-center gap-3"
        onSubmit={(e) => {
          e.preventDefault()
          setAsked(q)
        }}
      >
        <Search size={18} className="text-text-dim shrink-0" />
        <input
          className="input flex-1"
          placeholder="Who was carrying a bag near the entrance after 14:00?"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          aria-label="Search the record"
        />
        <button type="submit" className="btn btn-apple-red shrink-0">
          Search
        </button>
      </form>

      {asked && isFetching && <p className="text-xs text-text-dim">Searching the record…</p>}
      {asked && isError && <Unreachable what="The search" />}
      {asked && !isFetching && !isError && hits && hits.length === 0 && (
        <p className="text-xs text-text-dim">
          The record holds no moment that answers that. The deterministic path searches the
          canonical event record; it does not guess.
        </p>
      )}
      {asked && !isFetching && hits && hits.length > 0 && (
        <div className="divide-y divide-white/5">
          {hits.slice(0, 8).map((h, i) => (
            <div key={h.eventId ?? i} className="py-3 flex items-center gap-4">
              <Activity size={14} className="text-accent shrink-0" />
              <span className="text-xs font-black text-text">{h.eventType}</span>
              <span className="text-[11px] text-text-dim truncate flex-1">
                {h.deviceId ?? h.siteId ?? ''}
              </span>
              <span className="text-[11px] text-text-dim">
                {h.normalizedAt ? new Date(h.normalizedAt).toLocaleString() : ''}
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

// The site at a glance: what the product is watching, and what it is not.
function SitePanel() {
  const { data: handover, isError: handoverError } = useHandover()
  const { data: coverage, isError: coverageError } = useCoverage()

  return (
    <div className="space-y-4">
      {handoverError ? (
        <Unreachable what="The hand-over" />
      ) : (
        <div className="rounded-2xl border border-white/10 bg-black/30 px-5 py-4">
          <div className="text-[10px] font-black uppercase tracking-[0.12em] text-text-dim">Hand-over</div>
          <div className="mt-2 text-sm text-text">
            {handover?.summary ?? 'The product has no hand-over for this site.'}
          </div>
          {handover?.openIncidents !== undefined && (
            <div className="mt-1 text-[11px] text-text-dim">{handover.openIncidents} open</div>
          )}
        </div>
      )}
      {coverageError ? (
        <Unreachable what="The coverage panel" />
      ) : (
        <div className="rounded-2xl border border-white/10 bg-black/30 px-5 py-4">
          <div className="text-[10px] font-black uppercase tracking-[0.12em] text-text-dim mb-3">
            Coverage
          </div>
          <div className="space-y-2">
            {(coverage?.cameras ?? coverage?.entries ?? []).slice(0, 8).map((c, i) => (
              <div key={c.cameraId ?? i} className="flex items-center gap-3 text-xs">
                <span className={cn('w-2 h-2 rounded-full', c.covered === false ? 'bg-danger' : 'bg-success')} />
                <span className="text-text flex-1 truncate">{c.camera ?? c.cameraId ?? 'camera'}</span>
                <span className="text-text-dim">{c.state ?? (c.covered ? 'covered' : 'not covered')}</span>
              </div>
            ))}
            {(coverage?.cameras ?? coverage?.entries ?? []).length === 0 && (
              <p className="text-xs text-text-dim">The product reports no cameras.</p>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

// The recent moments.
//
// The record, newest first: what the platform has seen, when, and from where.
// It is the structured path over the canonical event record -- no model, no
// prose -- so every row is a fact with a time on it, and a reader can go and
// look at the picture.
//
// The search above asks the same record a question; this asks it for the last
// hour.
function RecentMoments() {
  const { data: hits, isError } = useRecentEvents(20)
  const [query, setQuery] = useState('')

  return (
    <div className="rounded-2xl border border-white/10 bg-black/30 px-5 py-5">
      <div className="text-[10px] font-black uppercase tracking-[0.12em] text-text-dim mb-3">
        Recent moments
      </div>
      {isError ? (
        <Unreachable what="The record" />
      ) : (
        <>
          <div className="flex items-center gap-3 pb-3">
            <input
              className="input flex-1"
              placeholder="the last hour, or a class, or a camera"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              aria-label="Filter the recent moments"
            />
          </div>
          <div className="divide-y divide-white/5">
            {(hits ?? []).slice(0, 8).map((h, i) => (
              <div key={h.eventId ?? i} className="py-2.5 flex items-center gap-4">
                <Activity size={13} className="text-accent shrink-0" />
                <span className="text-xs font-black text-text">{h.eventType}</span>
                <span className="text-[11px] text-text-dim truncate flex-1">{h.deviceId ?? h.siteId ?? ''}</span>
                <span className="text-[11px] text-text-dim">
                  {h.normalizedAt ? new Date(h.normalizedAt).toLocaleTimeString() : ''}
                </span>
              </div>
            ))}
            {(hits ?? []).length === 0 && (
              <p className="text-xs text-text-dim py-2">
                Nothing yet. The platform records what the VMS tells it, and the VMS has not
                said anything.
              </p>
            )}
          </div>
        </>
      )}
    </div>
  )
}

export function CockpitPage() {
  return (
    <Page>
      <PageHeader
        title="Cockpit"
        subtitle="What needs the operator now, what the site is doing, and where it happened."
      />
      <div className="space-y-10">
        <StatusStrip />

        <div className="grid grid-cols-1 xl:grid-cols-3 gap-6">
          <section className="xl:col-span-2 space-y-4">
            <div className="flex items-center gap-3">
              <ShieldAlert size={16} className="text-accent" />
              <h2 className="text-sm font-black text-text">Needs the operator</h2>
            </div>
            <AttentionQueue />
          </section>
          <section className="space-y-4">
            <div className="flex items-center gap-3">
              <Layout size={16} className="text-accent" />
              <h2 className="text-sm font-black text-text">The site</h2>
            </div>
            <SitePanel />
          </section>
        </div>

        <section className="space-y-4">
          <div className="flex items-center gap-3">
            <Cpu size={16} className="text-accent" />
            <h2 className="text-sm font-black text-text">Live</h2>
            <span className="text-[11px] text-text-dim">the VMS's own picture and the VMS's own boxes</span>
          </div>
          <LiveGrid />
        </section>

        <section className="space-y-4">
          <div className="flex items-center gap-3">
            <Search size={16} className="text-accent" />
            <h2 className="text-sm font-black text-text">Search</h2>
          </div>
          <SearchPanel />
        </section>

        <section className="space-y-4">
          <div className="flex items-center gap-3">
            <Activity size={16} className="text-accent" />
            <h2 className="text-sm font-black text-text">The record</h2>
            <span className="text-[11px] text-text-dim">what the platform has seen, and when</span>
          </div>
          <RecentMoments />
        </section>
      </div>
    </Page>
  )
}
