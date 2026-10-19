import { useEffect, useRef, useState } from 'react'
import { PageHeader, Page } from '@/components/Shell'
import { ArrowUpRight, Play, Pause, Volume2, VolumeX, Search, Layout } from 'lucide-react'
import { cn } from '@/lib/utils'
import { useCameras, useAIRuntime } from '@/hooks/api'
import { useVMSCameras, vmsFrameUrl, type VMSCamera, type VMSDetection } from '@/hooks/vms'
import type { Camera } from '@/api'

function statusStyle(status: string) {
  switch (status) {
    case 'online': return { bg: 'bg-apple-green', dot: 'bg-apple-green', text: 'text-black', label: 'Online' }
    case 'degraded': return { bg: 'bg-apple-yellow text-black', dot: 'bg-apple-yellow', text: 'text-black', label: 'Degraded' }
    default: return { bg: 'bg-apple-red', dot: 'bg-apple-red', text: 'text-white', label: 'Offline' }
  }
}

// Shared uploaded videos - synced with Upload page via localStorage
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

// Client-side AI detection simulation for uploaded videos.
//
// This is gone. The boxes on a camera are now the VMS's own assertions about
// the frame it is looking at, fetched from the device. A detection the browser
// invented is a detection nobody can check, and the number of them on the
// screen was a number the browser made up.

// The boxes the VMS asserted about the frame.
//
// They are the DEVICE's detections, in the device's normalised coordinates, and
// they are drawn where the device says they are. The previous version of this
// page derived boxes from the video element's current time -- `14 + (i * 21) % 68`
// for the left edge and so on -- which is a box that is not on the picture, and
// a detection rate that measures the arithmetic rather than the scene.
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
              isPerson
                ? 'border-apple-red bg-apple-red/40 shadow-xl shadow-apple-red/50'
                : 'border-white/40 bg-white/15 shadow-xl shadow-black/50',
            )}
            style={{ left: `${left}%`, top: `${top}%`, width: `${width}%`, height: `${height}%` }}
          >
            <div
              className={cn(
                'absolute -top-3 left-2 px-2.5 py-1 rounded-t-sm text-[10px] font-black shadow-lg',
                isPerson ? 'bg-apple-red text-white shadow-apple-red/50' : 'bg-black/80 text-white',
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

// The picture the device is looking at, with the boxes on it.
function VMSFrame({ camera, filter }: { camera: VMSCamera; filter: string }) {
  return (
    <div className="relative w-full h-full bg-black">
      <img
        src={vmsFrameUrl(camera)}
        alt={`${camera.name} live view`}
        className="w-full h-full object-contain"
      />
      <BoxOverlay detections={camera.detections} filter={filter} />
    </div>
  )
}

// A camera tile that shows the VMS's own picture.
function VMSTile({ camera, active, onClick }: { camera: VMSCamera; active: boolean; onClick?: () => void }) {
  const people = camera.detections.filter((d) => d.class === 'person' || d.class === 'person-with-bag').length
  const vehicles = camera.detections.filter((d) => d.class === 'vehicle').length
  const online = !camera.refusal
  return (
    <div
      className={cn(
        'relative overflow-hidden border border-white/10 rounded-3xl bg-black aspect-video transition-all duration-500 hover:border-white/30 hover:shadow-2xl hover:-translate-y-2 cursor-pointer group',
        active ? 'ring-2 ring-apple-red shadow-2xl shadow-apple-red/50 scale-105 z-20' : '',
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
            'flex items-center gap-3 px-3 py-1.5 rounded-full bg-black/60 backdrop-blur-sm text-[10px] font-black border border-white/10',
            online ? 'bg-apple-green' : 'bg-apple-red text-white',
          )}
        >
          <span className={cn('w-3 h-3 rounded-full', online ? 'bg-apple-green' : 'bg-apple-red')} />
          <span>{online ? 'Online' : 'No feed'}</span>
        </div>
        <div className="px-3 py-1.5 rounded-full bg-black/60 backdrop-blur-sm text-[10px] font-black border border-white/10 text-text-dim">
          {people} people · {vehicles} vehicles · {camera.latencyMs.toFixed(0)} ms
        </div>
      </div>
      <div className="absolute bottom-0 left-0 right-0 p-8 bg-gradient-to-t from-black/95 via-black/60 to-transparent">
        <div className="text-xl font-black text-text">{camera.name}</div>
        <div className="text-sm text-text-muted mt-3 font-black">{camera.kind}</div>
      </div>
      {active && (
        <div className="absolute inset-0 z-30 flex items-center justify-center gap-5 bg-black/75 backdrop-blur-sm opacity-0 group-hover:opacity-100 transition-opacity duration-300">
          <button
            onClick={onClick}
            className="p-4 rounded-3xl bg-white/10 text-white hover:bg-white/20 transition-all duration-300 backdrop-blur-sm border border-white/10 shadow-xl shadow-black/60"
            aria-label="Open fullscreen"
          >
            <ArrowUpRight size={24} />
          </button>
        </div>
      )}
    </div>
  )
}

function CameraTile({ camera, active, onClick }: { camera: Camera; active: boolean; onClick?: () => void }) {
  const status = statusStyle(camera.status)
  const isUploaded = camera.id.startsWith('upload-')
  const videoUrl = isUploaded ? `/video/clip/${camera.id}` : undefined

  return (
    <div
      className={cn(
        'relative overflow-hidden border border-white/10 rounded-3xl bg-black aspect-video transition-all duration-500 hover:border-white/30 hover:shadow-2xl hover:-translate-y-2 cursor-pointer group',
        active ? 'ring-2 ring-apple-red shadow-2xl shadow-apple-red/50 scale-105 z-20' : '',
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
      {isUploaded && videoUrl ? (
        <video
          className="w-full h-full object-cover"
          src={videoUrl}
          autoPlay
          playsInline
          muted
          loop
        />
      ) : (
        <div className="absolute inset-0 z-10 flex items-center justify-center text-text-dim text-xs font-black">
          {camera.id.toUpperCase()}
        </div>
      )}
      <div className="absolute top-5 left-5 z-20 flex flex-col gap-3">
        <div className={cn('flex items-center gap-3 px-3 py-1.5 rounded-full bg-black/60 backdrop-blur-sm text-[10px] font-black border border-white/10', status.bg)}>
          <span className={`w-3 h-3 rounded-full ${status.dot}`} />
          <span>{status.label}</span>
        </div>
        <div className="px-3 py-1.5 rounded-full bg-black/60 backdrop-blur-sm text-[10px] font-black border border-white/10 text-text-dim">
          {camera.siteId}
        </div>
      </div>
      <div className="absolute bottom-0 left-0 right-0 p-8 bg-gradient-to-t from-black/95 via-black/60 to-transparent">
        <div className="text-xl font-black text-text">{camera.name}</div>
        <div className="text-sm text-text-muted mt-3 font-black">{camera.siteId}</div>
      </div>
      {active && (
        <div className="absolute inset-0 z-30 flex items-center justify-center gap-5 bg-black/75 backdrop-blur-sm opacity-0 group-hover:opacity-100 transition-opacity duration-300">
          <button
            onClick={onClick}
            className="p-4 rounded-3xl bg-white/10 text-white hover:bg-white/20 transition-all duration-300 backdrop-blur-sm border border-white/10 shadow-xl shadow-black/60"
            aria-label="Open fullscreen"
          >
            <ArrowUpRight size={24} />
          </button>
        </div>
      )}
    </div>
  )
}

export function LivePage() {
  const [cols, setCols] = useState<'1' | '2' | '4' | '6' | '9' | '16'>('1')
  const [activeId, setActiveId] = useState<string | null>(null)
  const [searchQuery, setSearchQuery] = useState('')
  const [isPlaying, setIsPlaying] = useState(true)
  const [isMuted, setIsMuted] = useState(false)
  const [entityFilter, setEntityFilter] = useState<string>('all')
  const videoRef = useRef<HTMLVideoElement | null>(null)
  const pausedRef = useRef(false)
  const { data: cameras, isPending, error } = useCameras()
  const { data: aiRuntime } = useAIRuntime()
  const listSource = cameras ?? []

  // Shared uploaded videos - synced with Upload page via localStorage
  const [uploadedVideos, setUploadedVideos] = useState<Camera[]>([])
  useEffect(() => {
    setUploadedVideos(getUploadedVideos())
  }, [])

  const allCameras = [...uploadedVideos, ...listSource]

  const list = allCameras.filter((camera) =>
    camera.name.toLowerCase().includes(searchQuery.toLowerCase()) ||
    camera.siteId.toLowerCase().includes(searchQuery.toLowerCase())
  )

  // The VMS's own cameras, polled. These are the facts the DEVICE holds: the
  // picture it is looking at and the boxes its analytics engine asserted.
  const { data: vmsCameras } = useVMSCameras()

  // The camera list is the VMS's own inventory when the device is reachable,
  // and the platform's device rows otherwise. A camera the device says it has
  // is a camera that is playing; a camera the platform has heard of is a camera
  // somebody once configured.
  const grid = vmsCameras && vmsCameras.length > 0
    ? vmsCameras.map((c) => ({
        id: c.id,
        name: c.name,
        siteId: c.kind,
        status: (c.refusal ? 'offline' : 'online') as Camera['status'],
        vms: c,
      }))
    : list.map((c) => ({ id: c.id, name: c.name, siteId: c.siteId, status: c.status, vms: undefined }))
  const visibleGrid = grid.filter((c) =>
    c.name.toLowerCase().includes(searchQuery.toLowerCase()) ||
    c.siteId.toLowerCase().includes(searchQuery.toLowerCase())
  )
  const active = visibleGrid.find((c) => c.id === activeId)

  return (
    <Page>
      <PageHeader title="Live" subtitle="Multi-camera operator grid with AI detection" />
      <div className="space-y-10">
        <div className="flex flex-col lg:flex-row lg:items-center justify-between gap-8">
          <div className="flex items-center gap-5">
            <span className="text-[10px] font-black text-text-dim uppercase tracking-[0.12em]">Grid density</span>
            <div className="flex gap-3 border border-white/10 rounded-full bg-black/40 p-3 backdrop-blur-sm">
              {(['1', '2', '4', '6', '9', '16'] as const).map((n) => (
                <button
                  key={n}
                  onClick={() => setCols(n)}
                  className={cn(
                    'w-12 h-12 rounded-2xl text-[10px] font-black transition-all duration-300 flex items-center justify-center',
                    cols === n ? 'bg-apple-red text-white shadow-xl shadow-apple-red/50' : 'text-text-dim hover:text-text hover:bg-white/5',
                  )}
                  aria-pressed={cols === n}
                  title={`${n} tiles`}
                >
                  {n}
                </button>
              ))}
            </div>
          </div>
          <div className="flex items-center gap-5 w-full max-w-3xl">
            <div className="relative flex-1">
              <Search size={19} className="absolute left-5 top-1/2 -translate-y-1/2 text-text-dim" />
              <input
                type="text"
                placeholder="Search cameras…"
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                className="input pl-14"
              />
            </div>
            <div className={cn(
              'flex items-center gap-4 px-5 py-3 rounded-full text-[11px] font-black border',
              aiRuntime?.available ? 'bg-apple-green/10 border-apple-green/30 text-apple-green shadow-xl shadow-apple-green/20' : 'bg-apple-red/10 border-apple-red/30 text-apple-red shadow-lg shadow-apple-red/30',
            )}>
              <span className={`w-4 h-4 rounded-full ${aiRuntime?.available ? 'bg-apple-green animate-pulse-green' : 'bg-apple-red animate-pulse-red'}`} />
              <span>{aiRuntime?.available ? 'AI Runtime Connected' : 'AI Runtime Simulation Active'}</span>
            </div>
          </div>
          <div className="flex items-center gap-3">
            <span className="text-[10px] font-black text-text-dim uppercase tracking-[0.12em]">Filter</span>
            {(['all', 'person', 'vehicle', 'animal', 'bag'] as const).map((ent) => (
              <button
                key={ent}
                onClick={() => setEntityFilter(ent)}
                className={cn(
                  'px-3 py-2 rounded-full border text-[10px] font-black transition-all duration-200 whitespace-nowrap',
                  entityFilter === ent
                    ? 'bg-apple-red text-white border-apple-red shadow-xl shadow-apple-red/50'
                    : 'border-white/10 text-text-dim hover:text-text hover:bg-white/5',
                )}
              >
                {ent}
              </button>
            ))}
          </div>
        </div>

        {isPending && <div className="flex h-[70vh] items-center justify-center text-text-dim font-medium">Loading cameras…</div>}
        {error && (
          <div className="rounded-3xl p-12 bg-apple-red/10 border border-apple-red/30 text-apple-red text-sm font-medium shadow-lg shadow-apple-red/30">
            Unable to load cameras: {error?.message ?? 'Unknown error'}
          </div>
        )}
        {!isPending && !error && visibleGrid.length === 0 && (
          <div className="empty">
            <div className="empty-icon">
              <Layout size={40} />
            </div>
            <div className="empty-title">No cameras yet</div>
            <div className="empty-subtitle">Upload a video on the Upload page to start building your live grid.</div>
          </div>
        )}

        <div
          className={cn(
            'grid gap-5',
            activeId ? 'hidden' : GRID_COLLS[cols],
          )}
          style={{ height: activeId ? undefined : 'calc(100vh - 380px)' }}
        >
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
            )
          )}
          {visibleGrid.length === 0 && (
            <div className="col-span-full empty">
              <div className="empty-icon">
                <Layout size={40} />
              </div>
              <div className="empty-title">No cameras found</div>
              <div className="empty-subtitle">Try adjusting your search or filter.</div>
            </div>
          )}
        </div>

        {active && (
          <div className="fixed inset-0 z-50 bg-black flex flex-col page-transition animate-fade-in">
            <div className="flex-1 relative flex items-center justify-center">
              <div className="relative w-full h-full max-w-[1200px] aspect-video">
                {active.vms ? (
                  <VMSFrame camera={active.vms} filter={entityFilter} />
                ) : active.id.startsWith('upload-') ? (
                  <video
                    ref={videoRef}
                    className="w-full h-full object-cover"
                    src={`/video/clip/${active.id}`}
                    autoPlay
                    playsInline
                    muted
                    loop
                  />
                ) : (
                  <div className="w-full h-full object-cover bg-black flex items-center justify-center text-text-dim text-sm">
                    This camera has no live feed. The VMS emulator holds the live views; the
                    platform holds the record of what it saw.
                  </div>
                )}
                <div className="absolute top-5 left-5 z-30 flex flex-col gap-3">
                  <div className={cn('flex items-center gap-3 px-3 py-1.5 rounded-full bg-black/60 backdrop-blur-sm text-[10px] font-black border border-white/10', statusStyle(active.status).bg)}>
                    <span className={`w-3 h-3 rounded-full ${statusStyle(active.status).dot}`} />
                    <span>{statusStyle(active.status).label}</span>
                  </div>
                  <div className="px-3 py-1.5 rounded-full bg-black/60 backdrop-blur-sm text-[10px] font-black border border-white/10 text-text-dim">
                    {active.siteId}
                  </div>
                </div>
                <div className="absolute inset-0 z-30 flex items-center justify-center gap-5 opacity-0 group-hover:opacity-100 transition-opacity duration-300">
                  <button
                    onClick={(e) => { e.stopPropagation(); setActiveId(null) }}
                    className="p-4 rounded-3xl bg-white/10 text-white hover:bg-white/20 transition-all duration-300 backdrop-blur-sm border border-white/10 shadow-xl shadow-black/60"
                    aria-label="Exit fullscreen"
                  >
                    <ArrowUpRight size={24} />
                  </button>
                  <button
                    onClick={() => { pausedRef.current = !pausedRef.current; setIsPlaying(!isPlaying) }}
                    className="p-4 rounded-3xl bg-white/10 text-white hover:bg-white/20 transition-all duration-300 backdrop-blur-sm border border-white/10 shadow-xl shadow-black/60"
                    aria-label={isPlaying ? 'Pause' : 'Play'}
                  >
                    {isPlaying ? <Pause size={24} /> : <Play size={24} />}
                  </button>
                  <button
                    onClick={() => setIsMuted(!isMuted)}
                    className="p-4 rounded-3xl bg-white/10 text-white hover:bg-white/20 transition-all duration-300 backdrop-blur-sm border border-white/10 shadow-xl shadow-black/60"
                    aria-label={isMuted ? 'Unmute' : 'Mute'}
                  >
                    {isMuted ? <Volume2 size={24} /> : <VolumeX size={24} />}
                  </button>
                </div>
              </div>
            </div>
            <div className="flex items-center justify-between px-10 py-8 bg-black/70 backdrop-blur-sm border-t border-white/10">
              <div className="flex items-center gap-5">
                <div className="w-16 h-16 rounded-3xl bg-white/5 flex items-center justify-center overflow-hidden shadow-xl shadow-black/60 text-text-dim text-xl font-black">
                  {active.name.slice(0, 2).toUpperCase()}
                </div>
                <div>
                  <div className="text-lg font-black text-text">{active.name}</div>
                  <div className="text-xs text-text-dim font-black">{active.id}</div>
                </div>
              </div>
              <div className="flex items-center gap-4 text-[11px] font-black">
                <button
                  onClick={() => setActiveId(null)}
                  className="p-3 rounded-2xl bg-white/10 text-white hover:bg-white/20 transition-all duration-300"
                  aria-label="Exit fullscreen"
                >
                  <ArrowUpRight size={20} />
                </button>
              </div>
            </div>
          </div>
        )}
      </div>
    </Page>
  )
}

const GRID_COLLS = {
  1: 'grid-cols-1',
  2: 'grid-cols-2',
  4: 'grid-cols-2 md:grid-cols-4',
  6: 'grid-cols-2 md:grid-cols-3 lg:grid-cols-6',
  9: 'grid-cols-3 md:grid-cols-3 lg:grid-cols-9',
  16: 'grid-cols-4 md:grid-cols-4 lg:grid-cols-16',
} as const
