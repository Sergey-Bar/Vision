import { useState } from 'react'
import { PageHeader, Page } from '@/components/Shell'
import { Maximize, Minimize, ArrowUpRight } from 'lucide-react'
import { cn } from '@/lib/utils'

const DEMO_CAMERAS = [
  { id: 'cam-1', name: 'Entrance North', status: 'online' as const, zone: 'Lobby' },
  { id: 'cam-2', name: 'Entrance South', status: 'online' as const, zone: 'Lobby' },
  { id: 'cam-3', name: 'Aisle 4', status: 'online' as const, zone: 'Retail' },
  { id: 'cam-4', name: 'Aisle 7', status: 'degraded' as const, zone: 'Retail' },
  { id: 'cam-5', name: 'Loading Dock', status: 'offline' as const, zone: 'Warehouse' },
  { id: 'cam-6', name: 'Parking A', status: 'online' as const, zone: 'Exterior' },
  { id: 'cam-7', name: 'Parking B', status: 'online' as const, zone: 'Exterior' },
  { id: 'cam-8', name: 'Back Office', status: 'offline' as const, zone: 'Office' },
]

function statusColor(status: string) {
  switch (status) {
    case 'online': return 'bg-success'
    case 'degraded': return 'bg-warning text-[rgb(0,0,0)]'
    default: return 'bg-danger'
  }
}

export function CameraTile({ camera, active, onClick }: { camera: typeof DEMO_CAMERAS[number]; active: boolean; onClick?: () => void }) {
  const [expanded, setExpanded] = useState(false)

  return (
    <div
      className={cn(
        'relative group overflow-hidden border border-border rounded-xl bg-[rgb(5,7,12)] transition-all duration-300',
        active ? 'ring-2 ring-accent' : '',
        expanded ? 'fixed inset-0 z-50 bg-black' : 'aspect-video',
      )}
      style={expanded ? undefined : { height: '100%' }}
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
      <div className="absolute inset-0 flex items-center justify-center">
        <video
          className="w-full h-full object-cover opacity-40 animate-pulse"
          src="https://commondatastorage.googleapis.com/gtv-videos-bucket/sample/BigBuckBunny.mp4"
          autoPlay
          loop
          muted
          playsInline
        />
        <div className="absolute inset-0 flex items-center justify-center gap-2">
          <button
            onClick={(e) => { e.stopPropagation(); setExpanded(!expanded) }}
            className="p-2 rounded bg-black/60 text-white hover:bg-black/80"
            aria-label={expanded ? 'Collapse' : 'Expand'}
          >
            {expanded ? <Minimize size={20} /> : <Maximize size={20} />}
          </button>
        </div>
        <div className="absolute top-2 left-2 flex flex-col gap-1">
          <div className="flex items-center gap-1.5 px-2 py-1 rounded-lg bg-black/70 text-[10px] font-semibold text-text backdrop-blur">
            <span className={`w-2 h-2 rounded-full ${statusColor(camera.status)}`} />
            <span>{camera.status}</span>
          </div>
          <div className="px-2 py-1 rounded-lg bg-black/70 text-[10px] font-semibold text-text backdrop-blur">
            {camera.zone}
          </div>
        </div>
      </div>
      <div className="absolute bottom-0 left-0 right-0 p-3 bg-gradient-to-t from-black/80 to-transparent">
        <div className="flex items-center justify-between">
          <div>
            <div className="text-sm font-semibold text-text">{camera.name}</div>
            <div className="text-xs text-text-muted">{camera.id}</div>
          </div>
          <div className="flex items-center gap-2 text-xs">
            <button className="p-1.5 rounded bg-black/60 text-text hover:bg-white/10" aria-label="PTZ controls">
              <ArrowUpRight size={14} />
            </button>
          </div>
        </div>
      </div>
    </div>
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

export function LivePage() {
  const [cols, setCols] = useState<'1' | '2' | '4' | '6' | '9' | '16'>('4')
  const [activeId, setActiveId] = useState<string | null>(null)

  return (
    <Page>
      <PageHeader title="Live" subtitle="Multi-camera operator grid" />
      <div className="space-y-4">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <span className="text-xs font-medium text-text-muted uppercase tracking-wider">Grid density</span>
            <div className="flex gap-1 border border-border rounded-lg bg-surface-lift p-1">
              {(['1', '2', '4', '6', '9', '16'] as const).map((n) => (
                <button
                  key={n}
                  onClick={() => setCols(n)}
                  className={cn(
                    'w-8 h-8 rounded-md text-xs font-medium transition-colors flex items-center justify-center',
                    cols === n ? 'bg-accent text-[rgb(0,26,38)]' : 'text-text-muted hover:text-text',
                  )}
                  aria-pressed={cols === n}
                  title={`${n} tiles`}
                >
                  {n}
                </button>
              ))}
            </div>
          </div>
        </div>
        <div
          className={cn(
            'grid gap-3',
            activeId ? 'hidden' : GRID_COLLS[cols],
          )}
          style={{ height: activeId ? undefined : 'calc(100vh - 240px)' }}
        >
          {DEMO_CAMERAS.map((camera) => (
            <CameraTile
              key={camera.id}
              camera={camera}
              active={activeId === camera.id}
              onClick={() => setActiveId(camera.id)}
            />
          ))}
        </div>
        {activeId && (
          <div className="fixed inset-0 z-50 bg-black flex flex-col">
            <div className="flex-1 relative flex items-center justify-center">
              <CameraTile camera={DEMO_CAMERAS.find((c) => c.id === activeId)!} active />
            </div>
            <button
              onClick={() => setActiveId(null)}
              className="absolute top-4 left-4 z-10 p-2 rounded bg-black/60 text-white hover:bg-white/20"
              aria-label="Exit fullscreen"
            >
              <ArrowUpRight size={20} />
            </button>
          </div>
        )}
      </div>
    </Page>
  )
}
