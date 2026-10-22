import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  Activity,
  ArrowUpRight,
  Cpu,
  Layout,
  Pause,
  Play,
  Search,
  ShieldAlert,
  X,
} from 'lucide-react'
import { Page, PageHeader } from '@/components/Shell'
import { ChiefPanel } from '@/components/Chief'
import { usePalette } from '@/components/CommandPalette'
import { Button } from '@/components/ui/Button'
import { Input } from '@/components/ui/Field'
import { SegmentedControl } from '@/components/ui/SegmentedControl'
import { SeverityBadge, StatusIndicator } from '@/components/ui/Badge'
import { EmptyState, ErrorState, LoadingState } from '@/components/ui/States'
import { cn } from '@/lib/utils'
import { describeError } from '@/lib/errors'
import { useFlip } from '@/lib/motion'
import { useDensity } from '@/lib/density'
import { useChord, useHotkey, isPaletteShortcut, isSearchShortcut } from '@/lib/keys'
import {
  useAttention,
  useCoverage,
  useHealth,
  useHandover,
  useIncidents,
  useRecentEvents,
  useEvidenceSearch,
  type AttentionItem,
} from '@/hooks/product'
import { useAIRuntime, useCameras } from '@/hooks/api'
import {
  useVMSCameras,
  useVMSControl,
  useVMSState,
  vmsFrameUrl,
  type VMSCamera,
  type VMSDetection,
} from '@/hooks/vms'
import type { Camera } from '@/api'

// THE ONE SCREEN.
//
// The console used to answer the operator's first three questions on three
// pages, with three headers, three copies of the same controls and three
// different sets of empty states. The questions were:
//
//   what needs me now?        the Cockpit
//   what is the site doing?    the Live page
//   where was that?            the Events page
//
// They are one page now, in the order the questions are asked:
//
//   THE STRIP   is anything broken?      five cells, five states, a dash
//   THE WALL    what is the site doing?   the VMS's picture and the VMS's boxes
//   THE COLUMN  what needs me, and where   the queue, the record, the site
//
// The wall is on the left because the picture is the thing an operator looks
// at while they work, and the column is on the right because it is the thing
// they read. The wall has its own scroll and stays put; the column scrolls
// with the page.
//
// Every number on this page is read from something. The VMS's boxes are the
// device's assertions about the picture it is looking at; the incidents are
// the product's decisions; the coverage is the product's own view of what it
// is watching. Nothing here is simulated, and a region whose source is
// unreachable says so instead of filling itself in.

// ---------------------------------------------------------------------------
// The strip
// ---------------------------------------------------------------------------

// The status strip: the five numbers that decide whether to keep watching.
//
// Every cell implements the five states, and the rule that matters is the
// one about LOADING: a cell that has not asked yet says so, and says it with
// a dash.
//
// The strip used to render `0 / 0` and `not configured` while the request
// was still in flight, because `cameras` was undefined and undefined
// multiplied by nothing is zero. A zero in an instrument panel is a
// MEASUREMENT, and an operator reading a dashboard cannot tell a measurement
// of nothing from a measurement that was never taken. A dash is honest and a
// zero is not.
function StatusStrip() {
  const { data: health, isPending: healthPending, isError: healthError } = useHealth()
  const { data: vmsState, isPending: vmsPending, isError: vmsError } = useVMSState()
  const { data: cameras, isPending: cameraPending, isError: cameraError } = useVMSCameras(5000)
  const { data: incidents, isPending: incidentPending } = useIncidents(50)

  const live = (cameras ?? []).filter((c) => !c.refusal).length
  const total = (cameras ?? []).length
  const open = (incidents ?? []).filter((i) => i.state === 'open' || i.state === 'detected').length
  const detector = vmsState?.detector

  const dash = '—'
  const cells: { label: string; value: string; tone: 'ok' | 'warn' | 'bad' | 'flat'; note?: string }[] = [
    {
      label: 'Cameras live',
      value: cameraError || cameraPending ? dash : `${live} / ${total}`,
      tone: cameraError ? 'bad' : cameraPending ? 'flat' : live === 0 ? 'bad' : live < total ? 'warn' : 'ok',
      note: cameraError ? 'the VMS is not answering' : cameraPending ? 'asking the VMS' : undefined,
    },
    {
      label: 'Open incidents',
      value: incidentPending ? dash : `${open}`,
      tone: incidentPending ? 'flat' : open > 0 ? 'warn' : 'ok',
      note: incidentPending ? 'asking the product' : undefined,
    },
    {
      label: 'Ingest',
      value: healthError || healthPending ? dash : (health?.slo?.status ?? health?.status ?? 'unknown'),
      tone: healthError ? 'bad' : healthPending ? 'flat' : health?.slo?.status === 'breached' ? 'bad' : 'ok',
      note: healthError ? 'the product is not answering' : healthPending ? 'asking the product' : undefined,
    },
    {
      label: 'Detector',
      value: vmsError || vmsPending ? dash : detector ? `${detector.provider}` : 'not configured',
      tone: vmsError ? 'bad' : vmsPending ? 'flat' : detector ? 'ok' : 'warn',
      note: vmsError ? 'the VMS is not answering' : vmsPending ? 'asking the VMS' : undefined,
    },
    {
      label: 'Frame cost',
      value:
        vmsError || vmsPending || (cameras ?? []).length === 0
          ? dash
          : `${Math.max(...(cameras ?? []).map((c) => c.latencyMs)).toFixed(0)} ms`,
      tone: 'flat',
      note: vmsPending || vmsError ? 'asking the VMS' : undefined,
    },
  ]

  return (
    <section id="strip" aria-label="The status strip" className="scroll-mt-24">
      <div className="grid grid-cols-2 gap-px overflow-hidden rounded-lg border border-line bg-line md:grid-cols-5">
        {cells.map((c) => (
          <div key={c.label} className="bg-bg-panel px-5 py-4">
            <div className="font-mono text-[10px] font-medium uppercase tracking-[0.08em] text-text-dim">
              {c.label}
            </div>
            <div
              className={cn(
                'mt-2 text-[22px] font-semibold leading-[28px] tracking-[-0.015em] tabular-nums',
                c.tone === 'ok' && 'text-ok-ink',
                c.tone === 'warn' && 'text-warn-ink',
                c.tone === 'bad' && 'text-bad-ink',
                c.tone === 'flat' && 'text-text-dim',
              )}
            >
              {c.value}
            </div>
            {c.note && <div className="mt-1 text-[11px] leading-[16px] text-text-dim">{c.note}</div>}
          </div>
        ))}
      </div>
    </section>
  )
}

// ---------------------------------------------------------------------------
// The wall
// ---------------------------------------------------------------------------

// The state colours, spelled ONE way.
//
// The console had two spellings of the same four states -- `bg-danger` and
// `text-danger` for one of them, `bg-apple-red` for another -- and the
// `apple-*` names were removed from the token file, so the camera status
// badges and the detection boxes rendered with NO COLOUR AT ALL. The class
// names compiled, they did nothing, and nothing failed.
//
// So: a state is `ok-ink`, `warn-ink`, `bad-ink` or `unknown-ink`, and the
// FILL names the token file still publishes are the aliases behind them.
function statusStyle(status: string) {
  switch (status) {
    case 'online':
      return { bg: 'bg-ok-ink', dot: 'bg-ok-ink', text: 'text-text', label: 'Online' }
    case 'degraded':
      return { bg: 'bg-warn-ink text-text', dot: 'bg-warn-ink', text: 'text-text', label: 'Degraded' }
    default:
      return { bg: 'bg-bad-ink', dot: 'bg-bad-ink', text: 'text-text', label: 'Offline' }
  }
}

// The boxes the VMS asserted about the frame.
//
// They are the DEVICE's detections, in the device's normalised coordinates,
// and they are drawn where the device says they are. The previous version of
// this page derived boxes from the video element's current time -- `14 + (i *
// 21) % 68` for the left edge and so on -- which is a box that is not on the
// picture, and a detection rate that measures the arithmetic rather than the
// scene.
function BoxOverlay({ detections, filter }: { detections: VMSDetection[]; filter: string }) {
  const visible = detections.filter((d) => filter === 'all' || d.class === filter)
  if (visible.length === 0) return null
  return (
    <div className="absolute inset-0 z-20 pointer-events-none">
      {visible.map((d, i) => {
        const left = d.x1 * 100
        const top = d.y1 * 100
        const width = (d.x2 - d.x1) * 100
        const height = (d.y2 - d.y1) * 100
        const isPerson = d.class === 'person' || d.class === 'person-with-bag'
        return (
          <div
            key={`${d.class}-${i}`}
            className={cn(
              'absolute border-2 rounded-sm',
              isPerson ? 'border-bad-ink bg-bad-ink/40' : 'border-white/40 bg-white/15',
            )}
            style={{ left: `${left}%`, top: `${top}%`, width: `${width}%`, height: `${height}%` }}
          >
            <div
              className={cn(
                'absolute -top-3 left-2 px-2.5 py-1 rounded-t-sm text-[10px] font-semibold',
                isPerson ? 'bg-bad-ink text-text' : 'bg-black/80 text-text',
              )}
            >
              {d.class.toUpperCase()} {(d.confidence * 100).toFixed(0)}%
            </div>
          </div>
        )
      })}
    </div>
  )
}

// The picture, cross-faded on refresh.
//
// The device's frame URL carries the frame's count, so the source changes
// every time the device has a new frame, and the browser swaps the image out
// from under the operator: the picture goes blank for a moment and comes
// back. The old picture stays until the new one has loaded, and the new one
// fades in over it.
//
// It is an opacity transition and nothing else, because the design system
// allows transform and opacity and nothing else, and because a picture that
// slides is a picture that appears to move.
function CrossFadingFrame({ src, alt }: { src: string; alt: string }) {
  const [current, setCurrent] = useState(src)
  const [previous, setPrevious] = useState<string | null>(null)
  const [loaded, setLoaded] = useState(false)

  useEffect(() => {
    if (src === current) return
    setPrevious(current)
    setCurrent(src)
    setLoaded(false)
  }, [src, current])

  return (
    <>
      {previous && (
        <img
          src={previous}
          alt=""
          aria-hidden="true"
          className="absolute inset-0 h-full w-full object-contain"
        />
      )}
      <img
        src={current}
        alt={alt}
        onLoad={() => setLoaded(true)}
        className={cn(
          'relative h-full w-full object-contain transition-opacity duration-300 ease-out',
          loaded ? 'opacity-100' : 'opacity-0',
        )}
      />
    </>
  )
}

// The picture the device is looking at, with the boxes on it.
function VMSFrame({ camera, filter }: { camera: VMSCamera; filter: string }) {
  return (
    <div className="relative h-full w-full bg-black">
      <CrossFadingFrame src={vmsFrameUrl(camera)} alt={`${camera.name} live view`} />
      <BoxOverlay detections={camera.detections} filter={filter} />
    </div>
  )
}

// A camera tile that shows the VMS's own picture.
//
// The active tile carries a 2px ACCENT ring. It is not filled: the design
// system's rule is one filled element per region, and the wall's filled
// element is the column-count control.
function VMSTile({
  camera,
  active,
  onClick,
}: {
  camera: VMSCamera
  active: boolean
  onClick?: () => void
}) {
  const people = camera.detections.filter((d) => d.class === 'person' || d.class === 'person-with-bag').length
  const vehicles = camera.detections.filter((d) => d.class === 'vehicle').length
  const online = !camera.refusal
  return (
    <div
      className={cn(
        'relative overflow-hidden border border-line rounded-lg bg-black aspect-video transition-colors duration-3 hover:border-line-strong cursor-pointer group',
        active ? 'ring-2 ring-accent z-20' : '',
      )}
      onClick={onClick}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault()
          onClick?.()
        }
      }}
      role="button"
      tabIndex={0}
      aria-label={camera.name}
    >
      <VMSFrame camera={camera} filter="all" />
      <div className="absolute top-5 left-5 z-20 flex flex-col gap-3">
        <div
          className={cn(
            'flex items-center gap-3 px-3 py-1.5 rounded-full bg-bg text-[10px] font-semibold border border-line',
            online ? 'bg-ok-ink' : 'bg-bad-ink text-text',
          )}
        >
          <span className={cn('w-3 h-3 rounded-full', online ? 'bg-ok-ink' : 'bg-bad-ink')} />
          <span>{online ? 'Online' : 'No feed'}</span>
        </div>
        <div className="px-3 py-1.5 rounded-full bg-bg text-[10px] font-semibold border border-line text-text-dim">
          {people} people · {vehicles} vehicles · {camera.latencyMs.toFixed(0)} ms
        </div>
      </div>
      <div className="absolute bottom-0 left-0 right-0 p-8 bg-gradient-to-t from-black/95 via-black/60 to-transparent">
        <div className="text-xl font-semibold text-text">{camera.name}</div>
        <div className="text-sm text-text-muted mt-3 font-semibold">{camera.kind}</div>
      </div>
      {active && (
        <div className="absolute inset-0 z-30 flex items-center justify-center gap-5 bg-black/75 opacity-0 group-hover:opacity-100 transition-opacity duration-2">
          <button
            onClick={onClick}
            className="p-4 rounded-lg bg-bg-elevated text-text hover:bg-bg-hover transition-colors duration-2 border border-line"
            aria-label="Open fullscreen"
          >
            <ArrowUpRight size={24} />
          </button>
        </div>
      )}
    </div>
  )
}

// A camera the platform has heard of and the VMS is not playing.
//
// It is drawn as a name and nothing else. There is no picture, because the
// picture is the VMS's to give and the VMS is not giving one, and there is no
// invented frame either -- a synthetic picture on a tile looks exactly like a
// real one, and a reader would watch it for a movement that is not there.
function CameraTile({
  camera,
  active,
  onClick,
}: {
  camera: Camera
  active: boolean
  onClick?: () => void
}) {
  const status = statusStyle(camera.status)

  return (
    <div
      className={cn(
        'relative overflow-hidden border border-line rounded-lg bg-black aspect-video transition-colors duration-3 hover:border-line-strong cursor-pointer group',
        active ? 'ring-2 ring-accent z-20' : '',
      )}
      onClick={onClick}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault()
          onClick?.()
        }
      }}
      role="button"
      tabIndex={0}
      aria-label={camera.name}
    >
      <div className="absolute inset-0 z-10 flex items-center justify-center text-text-dim text-xs font-semibold">
        {camera.id.toUpperCase()}
      </div>
      <div className="absolute top-5 left-5 z-20 flex flex-col gap-3">
        <div
          className={cn(
            'flex items-center gap-3 px-3 py-1.5 rounded-full bg-bg text-[10px] font-semibold border border-line',
            status.bg,
          )}
        >
          <span className={cn('w-3 h-3 rounded-full', status.dot)} />
          <span>{status.label}</span>
        </div>
        <div className="px-3 py-1.5 rounded-full bg-bg text-[10px] font-semibold border border-line text-text-dim">
          {camera.siteId}
        </div>
      </div>
      <div className="absolute bottom-0 left-0 right-0 p-8 bg-gradient-to-t from-black/95 via-black/60 to-transparent">
        <div className="text-xl font-semibold text-text">{camera.name}</div>
        <div className="text-sm text-text-muted mt-3 font-semibold">{camera.siteId}</div>
      </div>
      {active && (
        <div className="absolute inset-0 z-30 flex items-center justify-center gap-5 bg-black/75 opacity-0 group-hover:opacity-100 transition-opacity duration-2">
          <button
            onClick={onClick}
            className="p-4 rounded-lg bg-bg-elevated text-text hover:bg-bg-hover transition-colors duration-2 border border-line"
            aria-label="Open fullscreen"
          >
            <ArrowUpRight size={24} />
          </button>
        </div>
      )}
    </div>
  )
}

const GRID_COLS = {
  1: 'grid-cols-1',
  2: 'grid-cols-2',
  4: 'grid-cols-2 md:grid-cols-4',
  6: 'grid-cols-2 md:grid-cols-3 lg:grid-cols-6',
  9: 'grid-cols-3 md:grid-cols-3 lg:grid-cols-9',
  16: 'grid-cols-4 md:grid-cols-4 lg:grid-cols-16',
} as const

// The wall.
//
// The picture, and the controls that choose what the picture shows. The
// column count is a CONTROL and not a mode: it changes how many pictures
// there are, and it does not change what the page is.
function Wall() {
  const [cols, setCols] = useState<'1' | '2' | '4' | '6' | '9' | '16'>('1')
  const [activeId, setActiveId] = useState<string | null>(null)
  const [searchQuery, setSearchQuery] = useState('')
  const [entityFilter, setEntityFilter] = useState<string>('all')
  const { data: cameras, isPending, error } = useCameras()
  const { data: aiRuntime } = useAIRuntime()
  const control = useVMSControl()

  // The VMS's own cameras, polled. These are the facts the DEVICE holds: the
  // picture it is looking at and the boxes its analytics engine asserted.
  const { data: vmsCameras } = useVMSCameras()

  // The camera list is the VMS's own inventory when the device is reachable,
  // and the platform's device rows otherwise. A camera the device says it has
  // is a camera that is playing; a camera the platform has heard of is a camera
  // somebody once configured.
  const grid = useMemo(
    () =>
      vmsCameras && vmsCameras.length > 0
        ? vmsCameras.map((c) => ({
            id: c.id,
            name: c.name,
            siteId: c.kind,
            status: c.refusal ? 'offline' : 'online',
            vms: c,
          }))
        : (cameras ?? []).map((c) => ({
            id: c.id,
            name: c.name,
            siteId: c.siteId,
            status: c.status,
            vms: undefined,
          })),
    [vmsCameras, cameras],
  )

  const visibleGrid = grid.filter(
    (c) =>
      c.name.toLowerCase().includes(searchQuery.toLowerCase()) ||
      c.siteId.toLowerCase().includes(searchQuery.toLowerCase()),
  )
  const active = visibleGrid.find((c) => c.id === activeId)

  return (
    <section id="wall" aria-label="The camera wall" className="scroll-mt-24 space-y-4">
      <div className="flex items-center gap-3">
        <Cpu size={16} className="text-text-dim" aria-hidden="true" />
        <h2 className="text-[15px] font-semibold text-text">The wall</h2>
        <span className="text-[11px] text-text-dim">
          the VMS's own picture and the VMS's own boxes
        </span>
      </div>

      <div className="flex flex-col gap-4 rounded-lg border border-line bg-bg-panel px-4 py-4">
        <div className="flex flex-wrap items-center gap-4">
          <div className="flex items-center gap-3">
            <span className="font-mono text-[10px] font-medium uppercase tracking-[0.08em] text-text-dim">
              Density
            </span>
            <SegmentedControl
              label="Grid density"
              value={cols}
              onChange={setCols}
              segments={(['1', '2', '4', '6', '9', '16'] as const).map((n) => ({ value: n, label: n }))}
            />
          </div>
          <div className="flex items-center gap-5">
            <span className="text-[10px] font-semibold text-text-dim uppercase tracking-[0.12em]">
              Filter
            </span>
            {(['all', 'person', 'vehicle', 'animal', 'bag'] as const).map((ent) => (
              <button
                key={ent}
                onClick={() => setEntityFilter(ent)}
                className={cn(
                  'px-3 py-2 rounded-full border text-[10px] font-semibold transition-colors duration-1 whitespace-nowrap',
                  entityFilter === ent
                    ? 'bg-accent text-accent-fg border-accent'
                    : 'border-line text-text-dim hover:text-text hover:bg-bg-elevated',
                )}
              >
                {ent}
              </button>
            ))}
          </div>
          <StatusIndicator
            state={aiRuntime?.available ? 'ok' : aiRuntime === undefined ? 'unknown' : 'warn'}
            label={
              aiRuntime?.available
                ? 'AI runtime ready'
                : aiRuntime === undefined
                  ? 'AI runtime — checking'
                  : 'AI runtime absent'
            }
          />
        </div>
        <div className="relative w-full max-w-md">
          <Search
            size={18}
            className="absolute left-4 top-1/2 -translate-y-1/2 text-text-dim pointer-events-none"
            aria-hidden="true"
          />
          <Input
            type="text"
            placeholder="Filter the wall by camera or site"
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            className="pl-11"
            aria-label="Filter the cameras on the wall"
          />
        </div>
      </div>

      {isPending && <LoadingState label="Reading the camera inventory" />}
      {error && (
        <ErrorState
          title="The camera inventory did not answer"
          detail={
            <>
              <p>
                The console asked the product for the cameras and could not read the answer. The
                wall is empty because the inventory is unknown, not because the product has no
                cameras.
              </p>
              <p className="mt-2 font-mono text-[11px] text-text-dim">{describeError(error)}</p>
            </>
          }
        />
      )}
      {!isPending && !error && visibleGrid.length === 0 && (
        <EmptyState
          title="The VMS is not playing any camera"
          detail="The picture is the device's to give. The platform holds the record of what it saw, and the VMS holds the live views."
        />
      )}

      {!isPending && !error && visibleGrid.length > 0 && (
        <div className={cn('grid gap-5', GRID_COLS[cols])}>
          {visibleGrid.map((camera) =>
            camera.vms ? (
              <VMSTile
                key={camera.id}
                camera={camera.vms}
                active={activeId === camera.id}
                onClick={() => setActiveId(camera.id)}
              />
            ) : (
              <CameraTile
                key={camera.id}
                camera={{ id: camera.id, name: camera.name, siteId: camera.siteId, status: camera.status }}
                active={activeId === camera.id}
                onClick={() => setActiveId(camera.id)}
              />
            ),
          )}
        </div>
      )}

      {active && (
        <div className="fixed inset-0 z-50 bg-black flex flex-col page-transition animate-fade-in">
          <div className="flex-1 relative flex items-center justify-center">
            <div className="relative w-full h-full max-w-[1200px] aspect-video">
              {active.vms ? (
                <VMSFrame camera={active.vms} filter={entityFilter} />
              ) : (
                <div className="w-full h-full object-cover bg-black flex items-center justify-center text-text-dim text-sm">
                  This camera has no live feed. The VMS holds the live views; the platform holds
                  the record of what it saw.
                </div>
              )}
              <div className="absolute top-5 left-5 z-30 flex flex-col gap-3">
                <div
                  className={cn(
                    'flex items-center gap-3 px-3 py-1.5 rounded-full bg-bg text-[10px] font-semibold border border-line',
                    statusStyle(active.status).bg,
                  )}
                >
                  <span className={cn('w-3 h-3 rounded-full', statusStyle(active.status).dot)} />
                  <span>{statusStyle(active.status).label}</span>
                </div>
                <div className="px-3 py-1.5 rounded-full bg-bg text-[10px] font-semibold border border-line text-text-dim">
                  {active.siteId}
                </div>
              </div>
              <div className="absolute inset-0 z-30 flex items-center justify-center gap-5 opacity-0 group-hover:opacity-100 transition-opacity duration-2">
                <button
                  onClick={(e) => {
                    e.stopPropagation()
                    setActiveId(null)
                  }}
                  className="p-4 rounded-lg bg-bg-elevated text-text hover:bg-bg-hover transition-colors duration-2 border border-line"
                  aria-label="Exit fullscreen"
                >
                  <ArrowUpRight size={24} />
                </button>
                <button
                  onClick={(e) => {
                    e.stopPropagation()
                    if (!active.vms) return
                    control.mutate({
                      id: active.vms.id,
                      control: active.vms.paused ? 'play' : 'pause',
                    })
                  }}
                  disabled={!active.vms}
                  className="p-4 rounded-lg bg-bg-elevated text-text hover:bg-bg-hover transition-colors duration-2 border border-line disabled:opacity-30"
                  aria-label={active.vms?.paused ? 'Play' : 'Pause'}
                >
                  {active.vms?.paused ? <Play size={24} /> : <Pause size={24} />}
                </button>
              </div>
            </div>
          </div>
          <div className="flex items-center justify-between px-10 py-8 bg-black/70 border-t border-line">
            <div className="flex items-center gap-5">
              <div className="w-16 h-16 rounded-lg bg-bg-elevated flex items-center justify-center overflow-hidden text-text-dim text-xl font-semibold">
                {active.name.slice(0, 2).toUpperCase()}
              </div>
              <div>
                <div className="text-lg font-semibold text-text">{active.name}</div>
                <div className="text-xs text-text-dim font-semibold">{active.id}</div>
              </div>
            </div>
            <div className="flex items-center gap-4 text-[11px] font-semibold">
              <button
                onClick={() => setActiveId(null)}
                className="p-3 rounded-lg bg-bg-elevated text-text hover:bg-bg-hover transition-colors duration-2"
                aria-label="Exit fullscreen"
              >
                <ArrowUpRight size={20} />
              </button>
            </div>
          </div>
        </div>
      )}
    </section>
  )
}

// ---------------------------------------------------------------------------
// The queue
// ---------------------------------------------------------------------------

// The two keys that move the queue.
//
// They are module-level because `useHotkey` re-subscribes its listener when
// the predicate changes, and an inline arrow is a new function on every
// render.
const isJKey = (e: KeyboardEvent) => e.key === 'j'
const isKKey = (e: KeyboardEvent) => e.key === 'k'

// The attention queue: what needs the operator, and why.
//
// It is ONE call, and the order is the product's. The reason chain is in the
// same body, so the panel cannot show a queue whose order and whose reasons
// came from two different places.
//
// The rows FLIP when the product re-ranks them, because a row that jumps
// while the operator is reading it is a row they lose.
function AttentionQueue() {
  const { data: items, isPending, isError, error } = useAttention(20)
  const [selected, setSelected] = useState(0)
  const navigate = useNavigate()
  const rows = useFlip<HTMLDivElement>([items])
  const list = items ?? []

  // j and k move in the queue, and Enter opens what is selected.
  //
  // The queue owns BOTH keys, because the queue owns the selection: a second
  // handler for `k` somewhere else would move a different notion of "the
  // selected row", and the two would disagree.
  //
  // Moving the selection also moves the FOCUS, so that Enter opens the row
  // the operator moved to and not the row they happened to be on. The focus
  // is requested rather than taken, because taking it on every change would
  // steal it from whatever the operator was doing.
  //
  // The predicates are module-level because `useHotkey` re-subscribes when its
  // predicate changes, and an inline arrow is a new function on every render.
  const focusRequested = useRef(false)
  useHotkey(
    isJKey,
    () => {
      focusRequested.current = true
      setSelected((s) => Math.min(list.length - 1, s + 1))
    },
    list.length > 0,
  )
  useHotkey(
    isKKey,
    () => {
      focusRequested.current = true
      setSelected((s) => Math.max(0, s - 1))
    },
    list.length > 0,
  )

  // The selected row is scrolled into view, because a selection that walks
  // off the list is a selection the reader has lost.
  useEffect(() => {
    const node = rows.current?.querySelector<HTMLElement>('[aria-current="true"]')
    if (!node) return
    node.scrollIntoView({ block: 'nearest' })
    if (!focusRequested.current) return
    focusRequested.current = false
    node.focus()
  }, [selected, rows])

  useEffect(() => {
    if (selected > list.length) setSelected(0)
  }, [list.length, selected])

  if (isPending) return <LoadingState label="Asking the product what needs the operator" />
  if (isError) {
    return (
      <ErrorState
        title="The attention queue did not answer"
        detail={
          <>
            <p>
              The console asked the product for the queue and could not read the answer. The
              queue is empty because the product did not say, not because nothing needs the
              operator.
            </p>
            <p className="mt-2 font-mono text-[11px] text-text-dim">{describeError(error)}</p>
          </>
        }
      />
    )
  }
  if (list.length === 0) {
    return (
      <EmptyState
        title="Nothing needs the operator"
        detail="The product raised no incident. That is the product's answer and not the console's guess."
      />
    )
  }

  return (
    <div ref={rows} className="rounded-lg border border-line bg-bg-panel divide-y divide-line overflow-hidden">
      {list.map((i, index) => (
        <QueueRow
          key={i.incidentId}
          item={i}
          selected={index === selected}
          onSelect={() => setSelected(index)}
          onOpen={() => void navigate(`/console/incidents/${encodeURIComponent(i.incidentId)}`)}
        />
      ))}
    </div>
  )
}

function QueueRow({
  item,
  selected,
  onSelect,
  onOpen,
}: {
  item: AttentionItem
  selected: boolean
  onSelect: () => void
  onOpen: () => void
}) {
  return (
    <div
      data-flip-id={item.incidentId}
      role="button"
      tabIndex={0}
      aria-current={selected ? 'true' : undefined}
      onClick={onOpen}
      onFocus={onSelect}
      onKeyDown={(e) => {
        if (e.key === 'Enter') {
          e.preventDefault()
          onOpen()
        }
      }}
      className={cn(
        'px-5 py-cell flex items-start gap-4 transition-colors duration-1 ease-out cursor-pointer',
        selected ? 'bg-bg-elevated' : 'hover:bg-bg-elevated/60',
      )}
    >
      <span className="mt-0.5 w-6 shrink-0 text-[11px] font-semibold tabular-nums text-text-dim">
        {item.rank}
      </span>
      <span
        aria-hidden="true"
        className={cn(
          'mt-1.5 w-2 h-2 rounded-full shrink-0',
          item.severity === 'critical' || item.severity === 'high'
            ? 'bg-bad-ink'
            : item.severity === 'medium'
              ? 'bg-warn-ink'
              : 'bg-accent-ink',
        )}
      />
      <span className="flex-1 min-w-0">
        <span className="block text-sm font-semibold text-text truncate">{item.title}</span>
        <span className="block text-[11px] text-text-dim mt-1">
          {item.siteId} · {item.ruleId} · {new Date(item.createdAt).toLocaleString()}
        </span>
        {item.threatReasonChain && item.threatReasonChain.length > 0 && (
          <span className="block mt-2 space-y-1">
            {item.threatReasonChain.map((s, n) => (
              <span key={n} className="block text-[11px] text-text-muted">
                {s.evidence ? `${s.evidence} — ` : ''}
                {s.rule ?? s.key}
                {s.factor !== undefined ? ` (+${s.factor})` : ''}
              </span>
            ))}
          </span>
        )}
        {item.why?.steps && item.why.steps.length > 0 && (
          <span className="block mt-2 space-y-1">
            {item.why.steps.map((s, n) => (
              <span key={n} className="block text-[11px] text-text-muted">
                {s.evidence ? `${s.evidence} — ` : ''}
                {s.rule ?? s.key}
              </span>
            ))}
          </span>
        )}
        {item.whyUnavailable && (
          <span className="block mt-1 text-[11px] text-warn-ink">
            The product could not explain this one: {item.whyUnavailable}
          </span>
        )}
        {item.degraded && (
          <span className="block mt-1 text-[11px] text-warn-ink">
            Raised without the AI plane
            {item.degradedReasons?.length ? `: ${item.degradedReasons.join(', ')}` : ''}.
          </span>
        )}
      </span>
      <span className="shrink-0 flex flex-col items-end gap-1">
        <SeverityBadge severity={item.severity} />
        <span
          className={cn(
            'text-[10px] font-semibold px-2 py-1 rounded-full border tabular-nums',
            item.threatScore > 0
              ? 'border-accent/40 bg-accent/10 text-accent'
              : 'border-line text-text-dim',
          )}
          title="The autonomy layer's threat score, and the chain that produced it"
        >
          {item.threatScore}
        </span>
      </span>
    </div>
  )
}

// ---------------------------------------------------------------------------
// The record
// ---------------------------------------------------------------------------

// The record.
//
// ONE list, ONE query, TWO framings: the last hour when nothing has been
// asked, and the answer to the question when something has.
//
// The search above it and the palette over it are the same search: the same
// index, the same query, the same history. The console used to have two of
// them, over two different queries, with two different empty states.
function Record() {
  const [query, setQuery] = useState('')
  const [chipQuery, setChipQuery] = useState('')
  const palette = usePalette()
  const asked = query.trim() || chipQuery

  const recent = useRecentEvents(20)
  const search = useEvidenceSearch(asked)

  const hits = asked ? (search.data ?? []) : (recent.data ?? [])

  const entities: { label: string; q: string; color: string }[] = [
    { label: 'Person', q: 'person', color: 'text-accent-ink border-accent-ink/40 bg-accent-ink/10' },
    { label: 'Vehicle', q: 'vehicle', color: 'text-warn-ink border-warn-ink/40 bg-warn-ink/10' },
    { label: 'Animal', q: 'animal', color: 'text-ok-ink border-ok-ink/40 bg-ok-ink/10' },
    { label: 'Bag', q: 'bag', color: 'text-bad-ink border-bad-ink/40 bg-bad-ink/10' },
  ]

  return (
    <section id="record" aria-label="The record" className="scroll-mt-24 space-y-4">
      <div className="flex items-center gap-3">
        <Activity size={16} className="text-text-dim" aria-hidden="true" />
        <h2 className="text-[15px] font-semibold text-text">The record</h2>
        <span className="text-[11px] text-text-dim">
          {asked ? `what answers “${asked}”` : 'the last hour, and every answer to a question'}
        </span>
      </div>

      <div className="rounded-lg border border-line bg-bg-panel px-5 py-5 space-y-4">
        <form
          className="flex items-center gap-3"
          onSubmit={(e) => {
            e.preventDefault()
            palette.open()
          }}
        >
          <Search size={18} className="text-text-dim shrink-0" aria-hidden="true" />
          <Input
            className="flex-1"
            placeholder="Who was carrying a bag near the entrance after 14:00?"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            aria-label="Search the record"
          />
          {query !== '' && (
            <button
              type="button"
              className="p-1.5 text-text-dim hover:text-text transition-colors duration-1 ease-out"
              onClick={() => setQuery('')}
              aria-label="Clear the search"
            >
              <X size={15} />
            </button>
          )}
          <Button type="submit" variant="primary" className="shrink-0">
            Search
          </Button>
        </form>

        <div className="flex gap-4 overflow-x-auto pb-1 scrollbar-hide">
          {entities.map((chip) => (
            <button
              key={chip.label}
              onClick={() => {
                setChipQuery(chip.q)
                setQuery('')
              }}
              className={cn(
                'px-4 py-2 rounded-full border text-[11px] font-semibold transition-colors duration-1 whitespace-nowrap shrink-0',
                chipQuery === chip.q ? chip.color : 'border-line text-text-dim hover:text-text hover:bg-bg-elevated',
              )}
            >
              {chip.label}
            </button>
          ))}
        </div>

        {asked && search.isPending && <LoadingState label="Asking the record" />}
        {asked && search.isError && (
          <ErrorState
            title="The record did not answer"
            detail={
              <>
                <p>
                  The console asked the product for the moments that answer that and could not
                  read the answer. The list below is empty because the record did not say, not
                  because the record holds nothing.
                </p>
                <p className="mt-2 font-mono text-[11px] text-text-dim">
                  {describeError(search.error)}
                </p>
              </>
            }
          />
        )}
        {!asked && recent.isPending && <LoadingState label="Asking the product what it has seen" />}
        {!asked && recent.isError && (
          <ErrorState
            title="The product did not answer"
            detail={
              <>
                <p>
                  The console asked the product for the last hour and could not read the answer.
                  The list below is empty because the product did not say, not because nothing
                  happened.
                </p>
                <p className="mt-2 font-mono text-[11px] text-text-dim">
                  {describeError(recent.error)}
                </p>
              </>
            }
          />
        )}

        {!asked && !recent.isPending && !recent.isError && hits.length === 0 && (
          <p className="text-[13px] leading-[20px] text-text-dim">
            Nothing yet. The platform records what the VMS tells it, and the VMS has not said
            anything.
          </p>
        )}
        {asked && !search.isPending && !search.isError && hits.length === 0 && (
          <p className="text-[13px] leading-[20px] text-text-dim">
            The record holds no moment that answers that. The deterministic path searches the
            canonical event record; it does not guess.
          </p>
        )}

        {hits.length > 0 && (
          <div className="overflow-x-auto">
            <table className="w-full text-left text-[13px]">
              <thead className="text-text-dim text-[10px] font-semibold uppercase tracking-[0.12em]">
                <tr>
                  <th scope="col" className="pb-2 pr-3 font-semibold">Time</th>
                  <th scope="col" className="pb-2 pr-3 font-semibold">Camera / Zone</th>
                  <th scope="col" className="pb-2 pr-3 font-semibold">Event</th>
                  <th scope="col" className="pb-2 font-semibold">Incident</th>
                </tr>
              </thead>
              <tbody>
                {hits.slice(0, 20).map((h, i) => (
                  <tr key={h.eventId ?? i} className="border-t border-line">
                    <td className="h-row py-cell pr-3 tabular-nums text-text-dim">
                      {h.normalizedAt ? new Date(h.normalizedAt).toISOString().slice(11, 19) : '—'}
                    </td>
                    <td className="h-row py-cell pr-3 text-text">{h.deviceId ?? h.siteId ?? '—'}</td>
                    <td className="h-row py-cell pr-3 text-text">{h.eventType}</td>
                    <td className="h-row py-cell text-text-dim">
                      {h.incidentId ? (
                        <a
                          href={`/console/incidents/${encodeURIComponent(h.incidentId)}`}
                          className="text-accent-ink hover:underline"
                        >
                          {h.incidentId}
                        </a>
                      ) : (
                        '—'
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </section>
  )
}

// ---------------------------------------------------------------------------
// The site
// ---------------------------------------------------------------------------

// The site at a glance: what the product is watching, and what it is not.
//
// The hand-over is the brief the shift wrote, and the coverage is the panel
// that says what the system could NOT see. Both are the product's own, read
// over the API: the console does not assemble either of them, because a
// console that assembles the coverage panel is a second reader of the same
// four tables.
function SitePanel() {
  const { data: handover, isError: handoverError, error: handoverErrorDetail } = useHandover()
  const { data: coverage, isError: coverageError, error: coverageErrorDetail } = useCoverage()

  return (
    <section id="site" aria-label="The site" className="scroll-mt-24 space-y-4">
      <div className="flex items-center gap-3">
        <Layout size={16} className="text-text-dim" aria-hidden="true" />
        <h2 className="text-[15px] font-semibold text-text">The site</h2>
        <span className="text-[11px] text-text-dim">context, rather than an answer</span>
      </div>

      <div className="space-y-4">
        {handoverError ? (
          <ErrorState
            title="The hand-over did not answer"
            detail={
              <>
                <p>
                  The console asked the product for the shift's brief and could not read the
                  answer. The panel below is empty because the product did not say, not because
                  the shift wrote nothing.
                </p>
                <p className="mt-2 font-mono text-[11px] text-text-dim">
                  {describeError(handoverErrorDetail)}
                </p>
              </>
            }
          />
        ) : (
          <div className="rounded-lg border border-line bg-bg-panel px-5 py-4">
            <div className="text-[10px] font-semibold uppercase tracking-[0.12em] text-text-dim">
              Hand-over
            </div>
            <div className="mt-2 text-sm text-text">
              {handover?.available
                ? handover.brief?.headline ?? 'The shift wrote a brief with no headline.'
                : handover?.unavailable ?? 'The product has no hand-over for this site.'}
            </div>
            {handover?.available && handover.brief?.open_items && handover.brief.open_items.length > 0 && (
              <div className="mt-2 space-y-1">
                {handover.brief.open_items.slice(0, 4).map((item, i) => (
                  <div key={i} className="text-[11px] text-text-dim">
                    {item.Kind ? `${item.Kind}: ` : ''}
                    {item.Summary}
                  </div>
                ))}
              </div>
            )}
          </div>
        )}
        {coverageError ? (
          <ErrorState
            title="The coverage panel did not answer"
            detail={
              <>
                <p>
                  The console asked the product what it is watching and could not read the
                  answer. The panel below is empty because the product did not say, not because
                  the site is fully covered.
                </p>
                <p className="mt-2 font-mono text-[11px] text-text-dim">
                  {describeError(coverageErrorDetail)}
                </p>
              </>
            }
          />
        ) : (
          <div className="rounded-lg border border-line bg-bg-panel px-5 py-4">
            <div className="text-[10px] font-semibold uppercase tracking-[0.12em] text-text-dim mb-3">
              Coverage
            </div>
            <div className="space-y-2">
              {(coverage?.rows ?? []).slice(0, 8).map((r) => (
                <div key={r.kind} className="flex items-center gap-3 text-xs">
                  <span
                    aria-hidden="true"
                    className={cn('w-2 h-2 rounded-full', r.breaking ? 'bg-bad-ink' : 'bg-ok-ink')}
                  />
                  <span className="text-text flex-1 truncate" title={r.what}>
                    {r.what}
                  </span>
                  <span className="text-text-dim tabular-nums">{r.count}</span>
                </div>
              ))}
              {(coverage?.rows ?? []).length === 0 && (
                <p className="text-xs text-text-dim">The product's coverage panel reports nothing.</p>
              )}
            </div>
          </div>
        )}
      </div>
    </section>
  )
}

// ---------------------------------------------------------------------------
// The page
// ---------------------------------------------------------------------------

/**
 * OperationsPage is the one screen.
 *
 * `focus` is the section a route wants the reader to land on. `/console/live`
 * and `/console/events` are the wall and the record, and they keep answering
 * because a bookmark and a rehearsal's URL list both name them -- but they
 * render THIS page, not a page of their own.
 */
export function OperationsPage({ focus }: { focus?: 'wall' | 'record' }) {
  const [density, setDensity] = useDensity()
  const palette = usePalette()
  const navigate = useNavigate()
  const main = useRef<HTMLDivElement | null>(null)

  // The route's own focus. The section is scrolled into view once the page
  // has laid out, because scrolling before the layout settles scrolls to the
  // wrong place.
  useEffect(() => {
    if (!focus) return
    const id = window.setTimeout(() => {
      const node = document.getElementById(focus)
      node?.scrollIntoView({ behavior: 'smooth', block: 'start' })
    }, 0)
    return () => window.clearTimeout(id)
  }, [focus])

  // ⌘K opens the palette; `/` opens it too, because the operator's hands are
  // on the keyboard and the palette IS the search.
  useHotkey(isPaletteShortcut, palette.open)
  useHotkey(isSearchShortcut, palette.open)

  // `g` then a letter goes to a section: o for the top, i for the queue, c
  // for the wall.
  const goTo = useCallback(
    (id: string) => {
      document.getElementById(id)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
    },
    [],
  )
  useChord('g', (key) => {
    if (key === 'o') goTo('strip')
    else if (key === 'i') goTo('queue')
    else if (key === 'c') goTo('wall')
  })

  return (
    <Page>
      <PageHeader
        title="Operations"
        subtitle="What needs the operator now, what the site is doing, and where it happened."
        actions={
          <SegmentedControl
            label="Row density"
            value={density}
            onChange={setDensity}
            segments={[
              { value: 'comfortable', label: 'comfortable' },
              { value: 'compact', label: 'compact' },
            ]}
          />
        }
      />

      <div ref={main} data-density={density} className="space-y-8">
        <StatusStrip />

        <div className="grid grid-cols-1 xl:grid-cols-3 gap-6">
          {/* The wall. It is the picture, and the picture does not move. */}
          <div className="xl:col-span-2">
            <Wall />
          </div>

          {/* The column. It scrolls with the page, because the answers are
              read in order: the Chief speaks first, then what needs you, then
              the record, then the site. */}
          <div className="xl:col-span-1 space-y-8">
            {/* The Chief is FIRST, because it is the answer to the question
                the operator has before any other: what changed while I was
                away. The queue below it is the detail. */}
            <section id="chief" aria-label="The Chief of Security" className="scroll-mt-24">
              <ChiefPanel />
            </section>

            <section id="queue" aria-label="What needs the operator" className="scroll-mt-24 space-y-4">
              <div className="flex items-center gap-3">
                <ShieldAlert size={16} className="text-text-dim" aria-hidden="true" />
                <h2 className="text-[15px] font-semibold text-text">What needs you</h2>
              </div>
              <AttentionQueue />
            </section>

            <Record />

            <SitePanel />
          </div>
        </div>
      </div>

      {/* The routes the wall and the record used to own, kept so a bookmark
          and the rehearsal's URL list both keep working. */}
      <span className="sr-only">
        <button onClick={() => void navigate('/console')}>Back to operations</button>
      </span>
    </Page>
  )
}
