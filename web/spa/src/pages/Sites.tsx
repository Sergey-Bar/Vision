import { PageHeader, Page } from '@/components/Shell'
import { useIncidents, useCoverage, useHandover } from '@/hooks/product'
import { useVMSCameras } from '@/hooks/vms'

// The site.
//
// The product is single-site per install, so this page is about ONE site: the
// one the daemon is configured for, its cameras, what it is watching, and what
// it has raised.
//
// The camera list comes from the VMS's own inventory rather than from a table
// of three invented sites, and the coverage comes from the product's coverage
// panel rather than from a constant. A site that says it has eight cameras when
// the device says it has two is a site that is lying about itself.
export function SitesPage() {
  const { data: incidents } = useIncidents(100)
  const { data: coverage } = useCoverage()
  const { data: handover } = useHandover()
  const { data: vmsCameras } = useVMSCameras(10_000)

  const siteId = handover?.siteId ?? (incidents ?? [])[0]?.siteId ?? ''
  const coverageEntries = coverage?.cameras ?? coverage?.entries ?? []
  const siteCameras = coverageEntries.filter((c) => !siteId || !c.siteId || c.siteId === siteId)
  const open = (incidents ?? []).filter((i) => i.state === 'open' || i.state === 'detected')
  const degraded = (incidents ?? []).filter((i) => i.degraded)

  return (
    <Page>
      <PageHeader title="Site" subtitle="The site this install is configured for" />
      <div className="space-y-10">
        <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
          <Stat label="Site" value={siteId || '—'} />
          <Stat label="Cameras known" value={String(siteCameras.length)} />
          <Stat label="Cameras live" value={String((vmsCameras ?? []).filter((c) => !c.refusal).length)} />
          <Stat label="Open incidents" value={String(open.length)} />
        </div>

        <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
          <div className="rounded-3xl border border-white/10 bg-black/30 overflow-hidden">
            <div className="px-8 py-5 text-[10px] font-black uppercase tracking-[0.12em] text-text-dim border-b border-white/10">
              Cameras
            </div>
            <table className="w-full text-left text-sm">
              <thead className="text-text-dim text-[10px] font-black uppercase tracking-[0.12em]">
                <tr>
                  <th className="px-8 py-4 font-black">Camera</th>
                  <th className="px-8 py-4 font-black">State</th>
                  <th className="px-8 py-4 font-black text-right">Last event</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-white/10">
                {siteCameras.map((c, i) => (
                  <tr key={c.cameraId ?? i} className="hover:bg-white/5 transition-all duration-300">
                    <td className="px-8 py-4 text-text font-black">{c.camera ?? c.cameraId ?? '—'}</td>
                    <td className="px-8 py-4 text-text-dim">{c.state ?? (c.covered ? 'covered' : 'not covered')}</td>
                    <td className="px-8 py-4 text-text-dim text-right">
                      {c.lastEventAt ? new Date(c.lastEventAt).toLocaleString() : '—'}
                    </td>
                  </tr>
                ))}
                {siteCameras.length === 0 && (
                  <tr>
                    <td colSpan={3} className="px-8 py-6 text-text-dim">
                      The product's coverage panel names no camera.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>

          <div className="rounded-3xl border border-white/10 bg-black/30 px-8 py-6 space-y-5">
            <div>
              <div className="text-[10px] font-black uppercase tracking-[0.12em] text-text-dim">
                Hand-over
              </div>
              <div className="mt-2 text-sm text-text">
                {handover?.summary ?? 'The product has no hand-over for this site.'}
              </div>
            </div>
            <div>
              <div className="text-[10px] font-black uppercase tracking-[0.12em] text-text-dim">
                Raised without the AI plane
              </div>
              <div className="mt-2 text-sm text-text">
                {degraded.length} of {(incidents ?? []).length} incidents.
              </div>
              <div className="mt-1 text-[11px] text-text-dim">
                An incident raised without the AI plane is not a defect, but a claim that AI
                reduced operator effort has to be able to say how often the AI was there.
              </div>
            </div>
          </div>
        </div>
      </div>
    </Page>
  )
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-2xl border border-white/10 bg-black/30 px-5 py-4">
      <div className="text-[10px] font-black uppercase tracking-[0.12em] text-text-dim">{label}</div>
      <div className="mt-2 text-xl font-black text-text">{value}</div>
    </div>
  )
}
