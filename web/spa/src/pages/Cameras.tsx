import { PageHeader, Page } from '@/components/Shell'
import { useCameras } from '@/hooks/api'
import { cn } from '@/lib/utils'
import { Plus, RefreshCw } from 'lucide-react'

export function CamerasPage() {
  const { data, isPending, error } = useCameras()

  return (
    <Page>
      <PageHeader title="Cameras" subtitle="Camera inventory and status" />
      <div className="space-y-10">
        <div className="flex items-center justify-between">
          <button className="flex items-center gap-4 px-6 py-4 rounded-3xl border border-accent bg-accent text-black font-black hover:bg-accent-hover transition-all duration-300 shadow-lg shadow-accent/40">
            <Plus size={17} className="mr-2" /> Add camera
          </button>
          <button className="p-3 text-text-muted hover:text-text hover:bg-white/5 rounded-2xl transition-all duration-300" aria-label="Refresh">
            <RefreshCw size={19} />
          </button>
        </div>
        {isPending && <div className="flex h-[70vh] items-center justify-center text-text-dim font-medium">Loading cameras…</div>}
        {error && <div className="rounded-3xl p-12 bg-danger/10 border border-danger/30 text-danger text-sm font-medium shadow-lg shadow-danger/30">Unable to load cameras: {error?.message ?? 'Unknown error'}</div>}

        <div className="border border-white/10 rounded-3xl bg-black/30 backdrop-blur-sm overflow-hidden shadow-2xl">
          <table className="w-full text-left text-sm">
            <thead className="bg-black/40 text-text-dim text-[10px] font-black uppercase tracking-[0.12em] sticky top-0 z-20">
              <tr>
                <th className="px-8 py-5 font-black">Camera</th>
                <th className="px-8 py-5 font-black">Site</th>
                <th className="px-8 py-5 font-black">Status</th>
                <th className="px-8 py-5 font-black text-right">Stream</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-white/10">
              {(data ?? []).map((cam) => (
                <tr key={cam.id} className="hover:bg-white/5 transition-all duration-300 group border-b border-white/5 last:border-0">
                  <td className="px-8 py-5 text-text font-black">{cam.name}</td>
                  <td className="px-8 py-5 text-text-dim">{cam.siteId}</td>
                  <td className="px-8 py-5">
                    <span className={cn(
                      'inline-flex items-center gap-3 px-4 py-2 rounded-full border text-[10px] font-black',
                      cam.status === 'online' ? 'border-success text-success bg-success/10 shadow-lg shadow-success/20' : cam.status === 'degraded' ? 'border-[#f59e0b] text-[#fbbf24] bg-[#fbbf24]/10 shadow-lg shadow-[#fbbf24]/20' : 'border-danger text-danger bg-danger/10 shadow-lg shadow-danger/20',
                    )}>
                      <span className={`w-3 h-3 rounded-full ${cam.status === 'online' ? 'bg-success' : cam.status === 'degraded' ? 'bg-warning' : 'bg-danger'}`} /> {cam.status}
                    </span>
                  </td>
                  <td className="px-8 py-5 text-text-dim text-right font-medium">{cam.rtsp ?? '—'}</td>
                </tr>
              ))}
              {(data ?? []).length === 0 && (
                <tr>
                  <td colSpan={4} className="px-8 py-20 text-center text-text-dim">No cameras found</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </Page>
  )
}
