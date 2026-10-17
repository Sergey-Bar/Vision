import { PageHeader, Page } from '@/components/Shell'
import { useClips } from '@/hooks/api'
import { Video, Download } from 'lucide-react'

export function ClipsPage() {
  const { data, isPending, error } = useClips()

  return (
    <Page>
      <PageHeader title="Clips" subtitle="Stored evidence clips" />
      <div className="space-y-4">
        {isPending && <div className="flex h-64 items-center justify-center text-text-muted">Loading clips…</div>}
        {error && <div className="text-sm text-danger">Unable to load clips: {error?.message ?? 'Unknown error'}</div>}
        <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
          {(data ?? []).map((clip) => (
            <div key={clip.evidenceId} className="border border-border rounded-xl bg-surface overflow-hidden flex flex-col">
              <div className="aspect-video bg-[rgb(5,7,12)] flex items-center justify-center text-text-muted text-xs">
                <Video size={24} className="mb-1" /> {clip.mediaType ?? 'clip'}
              </div>
              <div className="px-3 py-3">
                <div className="text-sm font-medium text-text truncate">{clip.evidenceId}</div>
                <div className="text-xs text-text-muted truncate">Site {clip.siteId}</div>
                <div className="mt-2 flex items-center justify-between text-xs text-text-muted">
                  <span>{new Date(clip.observedAt).toLocaleString('en-US', { timeZone: 'UTC', hour12: false })}</span>
                  <span>{clip.sizeBytes ? (clip.sizeBytes / 1024 / 1024).toFixed(2) + ' MB' : '—'}</span>
                </div>
              </div>
              <div className="px-3 pb-3 flex gap-2">
                <button                   className="flex-1 px-3 py-2 rounded-md border border-border text-sm text-text hover:bg-surface-lift transition-colors" aria-label="Play clip">
                  <Video size={14} className="mr-2" /> Play
                </button>
                <button className="px-3 py-2 rounded-md border border-border text-accent hover:bg-accent/5 transition-colors" aria-label="Download clip">
                  <Download size={14} className="mr-2" /> Export
                </button>
              </div>
            </div>
          ))}
          {(data ?? []).length === 0 && (
            <div className="col-span-full flex flex-col items-center justify-center py-20 text-text-muted">
              <Video size={32} className="mb-3 opacity-50" />
              <p className="text-sm">No clips available</p>
            </div>
          )}
        </div>
      </div>
    </Page>
  )
}
