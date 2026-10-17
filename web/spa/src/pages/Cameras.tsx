import { PageHeader, Page } from '@/components/Shell'
import { useCameras } from '@/hooks/api'
import { cn } from '@/lib/utils'
import { Plus, RefreshCw } from 'lucide-react'

export function CamerasPage() {
  const { data, isPending, error } = useCameras()

  return (
    <Page>
      <PageHeader title="Cameras" subtitle="Camera inventory and status" />
      <div className="space-y-4">
        <div className="flex items-center justify-between">
          <button className="flex items-center gap-2 px-3 py-2 rounded-md border border-accent bg-accent text-[rgb(0,26,38)] text-sm font-semibold hover:bg-accent-hover transition-colors">
            <Plus size={14} /> Add camera
          </button>
          <button className="px-2 text-text-muted hover:text-text" aria-label="Refresh">
            <RefreshCw size={16} />
          </button>
        </div>
        {isPending && <div className="flex h-64 items-center justify-center text-text-muted">Loading cameras…</div>}
        {error && <div className="text-sm text-danger">Unable to load cameras: {error?.message ?? 'Unknown error'}</div>}
        <div className="border border-border rounded-xl bg-surface overflow-hidden">
          <table className="w-full text-left text-sm">
            <thead className="bg-surface-lift text-text-muted text-xs uppercase tracking-wider">
              <tr>
                <th className="px-4 py-3">Camera</th>
                <th className="px-4 py-3">Site</th>
                <th className="px-4 py-3">Status</th>
                <th className="px-4 py-3 text-right">Stream</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {(data ?? []).map((cam) => (
                <tr key={cam.id} className="hover:bg-surface-lift transition-colors group">
                  <td className="px-4 py-3 text-text font-medium">{cam.name}</td>
                  <td className="px-4 py-3 text-text-muted">{cam.siteId}</td>
                  <td className="px-4 py-3">
                    <span className={cn(
                      'inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-[10px] font-medium border',
                      cam.status === 'online' ? 'border-success text-success bg-success/10' : cam.status === 'degraded' ? 'border-[#f59e0b] text-[#f59e0b] bg-[#4a2a1a]/40' : 'border-danger text-danger bg-danger-bg/10',
                    )}>
                      <span className={cn('w-1.5 h-1.5 rounded-full', cam.status === 'online' ? 'bg-success' : cam.status === 'degraded' ? 'bg-warning' : 'bg-danger')} /> {cam.status}
                    </span>
                  </td>
                  <td className="px-4 py-3 text-text-muted text-right">{cam.rtsp ?? '—'}</td>
                </tr>
              ))}
              {(data ?? []).length === 0 && (
                <tr>
                  <td colSpan={4} className="px-4 py-8 text-center text-text-muted">No cameras found</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </Page>
  )
}
