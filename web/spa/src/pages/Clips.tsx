import { useState } from 'react'
import { PageHeader, Page } from '@/components/Shell'
import { useClips } from '@/hooks/api'
import { Video, Download, Play, Pause } from 'lucide-react'
import { cn } from '@/lib/utils'
import { EmptyState } from '@/components/ui/States'

// The clips are the platform's own stored evidence.
//
// The page used to add a clip for every video the browser remembered uploading,
// with the observation time set to the moment the page was opened and the size
// set to zero. Both were invented, and a clip with a real-looking timestamp on
// it is the kind of thing a reader takes for a recording.
export function ClipsPage() {
  const { data: apiClips, isPending, error } = useClips()
  const [playingId, setPlayingId] = useState<string | null>(null)
  const [isPlaying, setIsPlaying] = useState(false)

  const clips = apiClips ?? []

  const togglePlay = (id: string) => {
    if (playingId === id) {
      setPlayingId(null)
      setIsPlaying(false)
    } else {
      setPlayingId(id)
      setIsPlaying(true)
    }
  }

  return (
    <Page>
      <PageHeader title="Clips" subtitle="Stored evidence clips" />
      <div className="space-y-10">
      {isPending && <div className="flex h-[70vh] items-center justify-center text-text-dim font-medium">Loading clips…</div>}
      {error && <div className="rounded-lg p-12 bg-bad-ink/10 border border-bad-ink/40 text-bad-ink text-sm font-medium">Unable to load clips: {error?.message ?? 'Unknown error'}</div>}

        <div className="grid gap-8 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
          {clips.map((clip) => (
            <div key={clip.evidenceId} className="group relative overflow-hidden border border-line rounded-lg bg-bg-panel transition-colors duration-3 hover:border-white/40 hover: hover:-translate-y-2">
              <div className="relative aspect-video bg-black overflow-hidden">
                {playingId === clip.evidenceId ? (
                  <video
                    className="w-full h-full object-cover"
                    src={`/v1/evidence/${encodeURIComponent(clip.evidenceId)}/content`}
                    autoPlay
                    playsInline
                    muted
                    onPlay={() => setIsPlaying(true)}
                    onPause={() => setIsPlaying(false)}
                    onEnded={() => { setPlayingId(null); setIsPlaying(false) }}
                  />
                ) : (
                  <div className="absolute inset-0 flex items-center justify-center text-text-dim">
                    <div className="flex flex-col items-center gap-4">
                      <Video size={40} className="text-text-dim" />
                      <span className="text-[11px] font-semibold text-text-dim">{clip.mediaType ?? 'clip'}</span>
                    </div>
                  </div>
                )}
                <button
                  onClick={(e) => { e.stopPropagation(); togglePlay(clip.evidenceId) }}
                  className={cn(
                    'absolute bottom-7 right-7 p-4 rounded-lg',
                    playingId === clip.evidenceId
                      ? 'bg-bg-elevated text-text hover:bg-white/20  '
                      : 'bg-bg text-text hover:bg-white/20  ',
                  )}
                  aria-label={playingId === clip.evidenceId ? 'Pause' : 'Play'}
                >
                  {playingId === clip.evidenceId
                    ? (isPlaying ? <Pause size={22} /> : <Play size={22} />)
                    : <Play size={22} />}
                </button>
                <div className="absolute top-5 left-5 px-4 py-2 rounded-full bg-bg text-[10px] font-semibold border border-line text-text-dim">
                  {clip.siteId}
                </div>
              </div>
              <div className="px-8 py-8">
              <div className="text-lg font-semibold text-text truncate">{clip.evidenceId}</div>
              <div className="mt-4 flex items-center justify-between text-xs text-text-dim font-semibold">
                  <span>{new Date(clip.observedAt).toLocaleString('en-US', { timeZone: 'UTC', hour12: false })}</span>
                  <span>{clip.sizeBytes ? (clip.sizeBytes / 1024 / 1024).toFixed(2) + ' MB' : '—'}</span>
                </div>
              </div>
              <div className="px-8 pb-8">
              <a href={`/v1/evidence/${encodeURIComponent(clip.evidenceId)}/content`} className="w-full px-5 py-4 rounded-lg border border-line text-sm font-medium text-text-dim hover:text-text hover:bg-bg-elevated transition-colors duration-2 flex items-center justify-center gap-3" aria-label="Download clip">
                  <Download size={18} className="shrink-0" /> Export
                  </a>
              </div>
            </div>
          ))}
          {clips.length === 0 && (
            <div className="col-span-full">
              <EmptyState
                title="No clips stored"
                detail="The platform stores a clip when the vendor gives it one. Nothing has been fetched yet."
              />
            </div>
          )}
        </div>
      </div>
    </Page>
  )
}
