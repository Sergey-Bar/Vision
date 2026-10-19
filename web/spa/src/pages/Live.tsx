import { useEffect, useRef, useState } from 'react'
import { PageHeader, Page } from '@/components/Shell'
import { ArrowUpRight, Play, Pause, Volume2, VolumeX, Search, Layout } from 'lucide-react'
import { cn } from '@/lib/utils'
import { useCameras, useAIRuntime } from '@/hooks/api'
import type { Camera, Detection } from '@/api'

// WebSocket with automatic reconnect and error suppression
function useWebSocket(url: string, onMessage: (data: any) => void) {
  const wsRef = useRef<WebSocket | null>(null)
  const reconnectRef = useRef<ReturnType<typeof setInterval> | null>(null)

  const connect = () => {
    try {
      wsRef.current = new WebSocket(url)
      wsRef.current.onopen = () => {
        console.log('[WebSocket] connected')
        if (reconnectRef.current) {
          clearInterval(reconnectRef.current)
          reconnectRef.current = null
        }
      }
      wsRef.current.onmessage = (event) => {
        try {
          const data = JSON.parse(event.data)
          onMessage(data)
        } catch {
          // Ignore parse errors
        }
      }
      wsRef.current.onclose = () => {
        wsRef.current = null
        // Auto-reconnect after 3 seconds
        reconnectRef.current = setInterval(() => {
          connect()
        }, 3000)
      }
      wsRef.current.onerror = () => {
        // Suppress proxy tunnel errors - silent retry
        if (wsRef.current) {
          wsRef.current.close()
        }
      }
    } catch {
      // WebSocket not available - silent
    }
  }

  useEffect(() => {
    connect()
    return () => {
      if (reconnectRef.current) clearInterval(reconnectRef.current)
      if (wsRef.current) wsRef.current.close()
    }
  }, [url, onMessage])

  return wsRef.current
}

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

// Client-side AI detection simulation for uploaded videos
function simulateDetection(videoElement: HTMLVideoElement): Detection[] | null {
  if (!videoElement || videoElement.readyState < 2) return null
  
  const detections: Detection[] = []
  const width = videoElement.videoWidth || 640
  const height = videoElement.videoHeight || 360
  const currentTime = videoElement.currentTime
  
  const seed = Math.floor(currentTime * 10) % 100
  
  if (seed < 35) {
    detections.push({
      label: 'person',
      confidence: 0.85 + (seed % 15) / 100,
      bbox: [width * 0.3, height * 0.3, width * 0.5, height * 0.5],
    })
  }
  if (seed > 60 && seed < 80) {
    detections.push({
      label: 'vehicle',
      confidence: 0.78 + (seed % 20) / 100,
      bbox: [width * 0.1, height * 0.5, width * 0.4, height * 0.3],
    })
  }
  if (seed > 85) {
    detections.push({
      label: 'bag',
      confidence: 0.72 + (seed % 15) / 100,
      bbox: [width * 0.5, height * 0.4, width * 0.2, height * 0.2],
    })
  }
  
  return detections.length > 0 ? detections : null
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
  const [frame, setFrame] = useState<{ people?: number; vehicles?: number; detections?: Detection[] } | null>(null)
  const [isPlaying, setIsPlaying] = useState(true)
  const [isMuted, setIsMuted] = useState(false)
  const [entityFilter, setEntityFilter] = useState<string>('all')
  const videoRef = useRef<HTMLVideoElement | null>(null)
  const detectionIntervalRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const pausedRef = useRef(false)
  const { data: cameras, isPending, error } = useCameras()
  const { data: aiRuntime } = useAIRuntime()
  const listSource = cameras ?? []

  // WebSocket connection (silent retry on failure)
  useWebSocket(`ws://127.0.0.1:8080/ws/live?protocol=2`, (data) => {
    if (data && typeof data === 'object' && !('type' in data)) {
      setFrame(data as { people?: number; vehicles?: number; detections?: Detection[] })
    }
  })

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

  const active = list.find((camera) => camera.id === activeId)

  // Detection simulation loop for active video
  useEffect(() => {
    if (!active || !active.id.startsWith('upload-')) {
      if (detectionIntervalRef.current) {
        clearInterval(detectionIntervalRef.current)
        detectionIntervalRef.current = null
      }
      setFrame(null)
      return
    }

    const videoEl = document.querySelector(`video[src*="${active.id}"]`) as HTMLVideoElement | null
    if (!videoEl) {
      videoRef.current = null
      return
    }

    videoRef.current = videoEl

    detectionIntervalRef.current = setInterval(() => {
      if (videoEl.paused || videoEl.ended) return
      const detections = simulateDetection(videoEl)
      if (detections) {
        setFrame({
          people: detections.filter(d => d.label === 'person').length,
          vehicles: detections.filter(d => d.label === 'vehicle').length,
          detections,
        })
      } else {
        setFrame({ people: 0, vehicles: 0, detections: [] })
      }
    }, 1000)

    return () => {
      if (detectionIntervalRef.current) clearInterval(detectionIntervalRef.current)
    }
  }, [active])

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
        {!isPending && !error && list.length === 0 && (
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
          {list.map((camera) => (
            <CameraTile
              key={camera.id}
              camera={camera}
              active={activeId === camera.id}
              onClick={() => setActiveId(camera.id)}
            />
          ))}
          {list.length === 0 && (
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
                {active.id.startsWith('upload-') ? (
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
                  <div className="w-full h-full object-cover bg-black">
                    {frame && frame.people !== undefined ? (
                      <div className="w-full h-full flex items-center justify-center text-text-dim text-sm font-medium">
                        Live feed — {frame.people} people, {frame.vehicles ?? 0} vehicles
                      </div>
                    ) : (
                      <div className="w-full h-full flex items-center justify-center text-text-dim text-sm">
                        Live feed unavailable
                      </div>
                    )}
                    {frame?.detections?.length ? (
                      <div className="absolute inset-0 z-20 pointer-events-none">
                        {frame.detections
                          .filter((d) => entityFilter === 'all' || d.label === entityFilter)
                          .map((d, i) => {
                            const x = 14 + (i * 21) % 68
                            const y = 14 + (i * 17) % 68
                            const w = 16 + (i * 9) % 20
                            const h = 14 + (i * 11) % 18
                            return (
                              <div
                                key={i}
                                className={cn(
                                  'absolute border-2 border-apple-red bg-apple-red/30 rounded-sm',
                                  d.label === 'person' ? 'border-apple-red bg-apple-red/40 shadow-xl shadow-apple-red/50' : 'border-white/30 bg-white/15 shadow-xl shadow-black/50',
                                )}
                                style={{ left: `${x}%`, top: `${y}%`, width: `${w}%`, height: `${h}%` }}
                              >
                                <div className="absolute -top-3 left-2 px-2.5 py-1 rounded-t-sm text-[10px] font-black bg-apple-red text-white shadow-lg shadow-apple-red/50">
                                  {d.label.toUpperCase()} {(d.confidence * 100).toFixed(0)}%
                                </div>
                              </div>
                            )
                          })}
                      </div>
                    ) : null}
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
                <div className="w-16 h-16 rounded-3xl bg-white/5 flex items-center justify-center overflow-hidden shadow-xl shadow-black/60">
                  <img src={`https://ui-avatars.com/api/?name=${encodeURIComponent(active.name)}&background=random&size=512`} alt="" className="w-full h-full object-cover" />
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
