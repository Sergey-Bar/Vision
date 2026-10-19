import { Page, PageHeader } from '@/components/Shell'
import { useQuery } from '@tanstack/react-query'
import { get } from '@/lib/api'
import { useVMSCameras } from '@/hooks/vms'
import { cn } from '@/lib/utils'

// The site.
//
// The zones are the OPERATOR'S OWN drawing: the YAML they walked the site with,
// served by the product. The live state is the VMS's, because the VMS is the
// device that has it. A product that published a camera's live state would be
// publishing a state it does not have.
//
// The map is drawn in the site's own coordinates. That is the point of a site
// model: the numbers mean metres, so two zones that touch on the map touch in
// the building, and a camera's position is where the camera is rather than
// where somebody put it on a picture.
interface SiteZone {
  id: string
  type: string
  name: string
  polygon: { x: number; y: number }[]
}

interface SiteCamera {
  id: string
  zoneIds: string[]
  position: { x: number; y: number }
}

interface SiteRoute {
  id: string
  points: { x: number; y: number }[]
  allowedDirection: string
}

interface SiteModel {
  siteId: string
  zones: SiteZone[]
  cameras: SiteCamera[]
  routes: SiteRoute[]
}

const ZONE_COLOURS: Record<string, string> = {
  restricted: 'stroke-danger fill-danger/15',
  choke: 'stroke-warning fill-warning/15',
  monitor: 'stroke-accent fill-accent/10',
  asset: 'stroke-white/30 fill-white/5',
}

export function SitePage() {
  const { data: site, isError } = useQuery({
    queryKey: ['site-model'],
    queryFn: async () => {
      const res = await get('/v1/site')
      return (await res.json()) as SiteModel
    },
    retry: 0,
  })
  const { data: cameras } = useVMSCameras(2000)

  if (isError) {
    return (
      <Page>
        <PageHeader title="Site" subtitle="The operator's own drawing of the site" />
        <div className="rounded-2xl p-8 bg-danger/10 border border-danger/30 text-danger text-sm font-medium">
          This install has no site model, so there is no site to draw. Set SP_SITE_MODEL to the
          operator's site map.
        </div>
      </Page>
    )
  }
  if (!site) {
    return (
      <Page>
        <PageHeader title="Site" subtitle="The operator's own drawing of the site" />
        <div className="rounded-2xl border border-white/10 bg-black/30 px-8 py-6 text-sm text-text-dim">
          Loading the site model…
        </div>
      </Page>
    )
  }

  // The drawing's extent, so the map fills the space it is given. A fixed
  // 1000x1000 box would draw a small site in the corner of a large one, which
  // is how a map stops being a map.
  const points = [
    ...site.zones.flatMap((z) => z.polygon),
    ...site.cameras.map((c) => c.position),
    ...site.routes.flatMap((r) => r.points),
  ]
  const minX = points.length ? Math.min(...points.map((p) => p.x)) : 0
  const maxX = points.length ? Math.max(...points.map((p) => p.x)) : 1000
  const minY = points.length ? Math.min(...points.map((p) => p.y)) : 0
  const maxY = points.length ? Math.max(...points.map((p) => p.y)) : 1000
  const pad = 40
  const width = Math.max(maxX - minX, 1) + pad * 2
  const height = Math.max(maxY - minY, 1) + pad * 2
  const live = new Map((cameras ?? []).map((c) => [c.id, !c.refusal]))

  return (
    <Page>
      <PageHeader title="Site" subtitle={`${site.siteId} · ${site.zones.length} zones · ${site.cameras.length} cameras`} />
      <div className="grid grid-cols-1 xl:grid-cols-3 gap-6">
        <div className="xl:col-span-2 rounded-3xl border border-white/10 bg-black/30 p-6">
          <svg viewBox={`${minX - pad} ${minY - pad} ${width} ${height}`} className="w-full h-auto">
            {site.zones.map((z) => (
              <polygon
                key={z.id}
                points={z.polygon.map((p) => `${p.x},${p.y}`).join(' ')}
                className={cn('stroke-2', ZONE_COLOURS[z.type] ?? 'stroke-white/20 fill-white/5')}
              />
            ))}
            {site.routes.map((r) => (
              <polyline
                key={r.id}
                points={r.points.map((p) => `${p.x},${p.y}`).join(' ')}
                className="fill-none stroke-accent/60 stroke-2 stroke-dasharray"
              />
            ))}
            {site.cameras.map((c) => (
              <g key={c.id}>
                <circle
                  cx={c.position.x}
                  cy={c.position.y}
                  r={14}
                  className={live.get(c.id) ? 'fill-success/40 stroke-success' : 'fill-white/10 stroke-white/30'}
                  strokeWidth={2}
                />
                <text
                  x={c.position.x}
                  y={c.position.y + 34}
                  textAnchor="middle"
                  className="fill-text-dim"
                  fontSize={22}
                  fontWeight={800}
                >
                  {c.id}
                </text>
              </g>
            ))}
            {site.zones.map((z) => (
              <text
                key={`label-${z.id}`}
                x={z.polygon[0]?.x ?? 0}
                y={(z.polygon[0]?.y ?? 0) - 12}
                className="fill-text-muted"
                fontSize={22}
                fontWeight={800}
              >
                {z.name || z.id}
              </text>
            ))}
          </svg>
        </div>

        <div className="space-y-4">
          <div className="rounded-3xl border border-white/10 bg-black/30 px-6 py-5">
            <div className="text-[10px] font-black uppercase tracking-[0.12em] text-text-dim mb-3">Zones</div>
            <div className="space-y-2">
              {site.zones.map((z) => (
                <div key={z.id} className="flex items-center gap-3 text-xs">
                  <span
                    className={cn(
                      'w-2.5 h-2.5 rounded-sm',
                      z.type === 'restricted' ? 'bg-danger' : z.type === 'choke' ? 'bg-warning' : 'bg-accent',
                    )}
                  />
                  <span className="text-text flex-1 truncate">{z.name || z.id}</span>
                  <span className="text-text-dim">{z.type}</span>
                </div>
              ))}
            </div>
          </div>
          <div className="rounded-3xl border border-white/10 bg-black/30 px-6 py-5">
            <div className="text-[10px] font-black uppercase tracking-[0.12em] text-text-dim mb-3">Cameras</div>
            <div className="space-y-2">
              {site.cameras.map((c) => (
                <div key={c.id} className="flex items-center gap-3 text-xs">
                  <span className={cn('w-2 h-2 rounded-full', live.get(c.id) ? 'bg-success' : 'bg-white/20')} />
                  <span className="text-text flex-1 truncate">{c.id}</span>
                  <span className="text-text-dim">{c.zoneIds.join(', ') || '—'}</span>
                </div>
              ))}
              {site.cameras.length === 0 && (
                <p className="text-xs text-text-dim">The site model declares no camera.</p>
              )}
            </div>
          </div>
        </div>
      </div>
    </Page>
  )
}
