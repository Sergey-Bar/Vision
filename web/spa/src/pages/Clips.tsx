import { useState, useEffect } from 'react'
import { PageHeader, Page } from '@/components/Shell'
import { useClips } from '@/hooks/api'
import { Video, Download, Play, Pause } from 'lucide-react'
import { cn } from '@/lib/utils'
import type { Camera } from '@/api'

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

export function ClipsPage() {
  const { data: apiClips, isPending, error } = useClips()
  const [uploadedVideos, setUploadedVideos] = useState<Camera[]>([])
  const [playingId, setPlayingId] = useState<string | null>(null)
  const [isPlaying, setIsPlaying] = useState(false)

  useEffect(() => {
    setUploadedVideos(getUploadedVideos())
  }, [])

  const allClips = uploadedVideos.map((cam) => ({
    evidenceId: cam.id,
    siteId: cam.siteId,
    mediaType: 'video/mp4',
    observedAt: new Date().toISOString(),
    sizeBytes: 0,
  }))

  const clips = [...(apiClips ?? []), ...allClips]

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
        {error && <div className="rounded-3xl p-12 bg-apple-red/10 border border-apple-red/30 text-apple-red text-sm font-medium shadow-lg shadow-apple-red/30">Unable to load clips: {error?.message ?? 'Unknown error'}</div>}

        <div className="grid gap-8 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
          {clips.map((clip) => (
            <div key={clip.evidenceId} className="group relative overflow-hidden border border-white/10 rounded-3xl bg-black/30 backdrop-blur-sm transition-all duration-500 hover:border-white/30 hover:shadow-2xl hover:-translate-y-2">
              <div className="relative aspect-video bg-black overflow-hidden">
                {playingId === clip.evidenceId ? (
                  <video
                    className="w-full h-full object-cover"
                    src={`/video/clip/${clip.evidenceId}`}
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
                      <span className="text-[11px] font-bold text-text-dim">{clip.mediaType ?? 'clip'}</span>
                    </div>
                  </div>
                )}
                <button
                  onClick={(e) => { e.stopPropagation(); togglePlay(clip.evidenceId) }}
                  className={cn(
                    'absolute bottom-7 right-7 p-4 rounded-3xl',
                    playingId === clip.evidenceId
                      ? 'bg-white/10 text-white hover:bg-white/20 shadow-2xl shadow-black/60'
                      : 'bg-black/60 text-white hover:bg-white/20 shadow-2xl shadow-black/60',
                  )}
                  aria-label={playingId === clip.evidenceId ? 'Pause' : 'Play'}
                >
                  {playingId === clip.evidenceId
                    ? (isPlaying ? <Pause size={22} /> : <Play size={22} />)
                    : <Play size={22} />}
                </button>
                <div className="absolute top-5 left-5 px-4 py-2 rounded-full bg-black/60 backdrop-blur-sm text-[10px] font-black border border-white/10 text-text-dim">
                  {clip.siteId}
                </div>
              </div>
              <div className="px-8 py-8">
                <div className="text-lg font-black text-text truncate">{clip.evidenceId}</div>
                <div className="mt-4 flex items-center justify-between text-xs text-text-dim font-black">
                  <span>{new Date(clip.observedAt).toLocaleString('en-US', { timeZone: 'UTC', hour12: false })}</span>
                  <span>{clip.sizeBytes ? (clip.sizeBytes / 1024 / 1024).toFixed(2) + ' MB' : '—'}</span>
                </div>
              </div>
              <div className="px-8 pb-8">
                <button className="w-full px-5 py-4 rounded-3xl border border-white/10 text-sm font-medium text-text-dim hover:text-text hover:bg-white/5 transition-all duration-300 flex items-center justify-center gap-3" aria-label="Download clip">
                  <Download size={18} className="shrink-0" /> Export
                </button>
              </div>
            </div>
          ))}
          {clips.length === 0 && (
            <div className="col-span-full empty">
              <div className="empty-icon">
                <Video size={40} />
              </div>
              <div className="empty-title">No clips available</div>
              <div className="empty-subtitle">Upload a video to start building your evidence library.</div>
            </div>
          )}
        </div>
      </div>
    </Page>
  )
}
