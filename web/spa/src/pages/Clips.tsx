import { useState } from 'react'
import { PageHeader, Page } from '@/components/Shell'
import { useClips } from '@/hooks/api'
import { Video, Download, Play, Pause } from 'lucide-react'
import { cn } from '@/lib/utils'
import { EmptyState, ErrorState, LoadingState } from '@/components/ui/States'
import { Button } from '@/components/ui/Button'

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
        {isPending && <LoadingState label="Loading clips" />}
        {error && (
          <ErrorState
            title="Unable to load clips"
            detail={error?.message ?? 'Unknown error'}
          />
        )}

        {!isPending && !error && (
          <div className="grid gap-6 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
            {clips.map((clip) => (
              <div
                key={clip.evidenceId}
                className="group relative overflow-hidden border border-line rounded-lg bg-bg-panel transition-all duration-2 hover:border-line-strong"
              >
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
                      onEnded={() => {
                        setPlayingId(null)
                        setIsPlaying(false)
                      }}
                    />
                  ) : (
                    <div className="absolute inset-0 flex items-center justify-center text-text-dim">
                      <div className="flex flex-col items-center gap-3">
                        <Video size={32} className="text-text-dim" />
                        <span className="text-[11px] font-medium text-text-dim">
                          {clip.mediaType ?? 'clip'}
                        </span>
                      </div>
                    </div>
                  )}
                  <button
                    onClick={(e) => {
                      e.stopPropagation()
                      togglePlay(clip.evidenceId)
                    }}
                    className={cn(
                      'absolute bottom-4 right-4 p-2.5 rounded-lg transition-colors duration-1',
                      playingId === clip.evidenceId
                        ? 'bg-bg-elevated text-text hover:bg-bg-hover'
                        : 'bg-bg-panel/90 backdrop-blur-sm text-text hover:bg-bg-elevated',
                    )}
                    aria-label={playingId === clip.evidenceId ? 'Pause' : 'Play'}
                  >
                    {playingId === clip.evidenceId ? (
                      isPlaying ? (
                        <Pause size={18} />
                      ) : (
                        <Play size={18} />
                      )
                    ) : (
                      <Play size={18} />
                    )}
                  </button>
                  <div className="absolute top-3 left-3 px-2.5 py-1 rounded-full bg-bg-panel/90 backdrop-blur-sm text-[10px] font-medium border border-line text-text-dim">
                    {clip.siteId}
                  </div>
                </div>
                <div className="p-4">
                  <div className="text-[13px] font-medium text-text truncate">
                    {clip.evidenceId}
                  </div>
                  <div className="mt-2 flex items-center justify-between text-[11px] text-text-dim">
                    <span>
                      {new Date(clip.observedAt).toLocaleString('en-US', {
                        timeZone: 'UTC',
                        hour12: false,
                        month: 'short',
                        day: 'numeric',
                        hour: '2-digit',
                        minute: '2-digit',
                      })}
                    </span>
                    <span>
                      {clip.sizeBytes ? (clip.sizeBytes / 1024 / 1024).toFixed(2) + ' MB' : '—'}
                    </span>
                  </div>
                </div>
                <div className="px-4 pb-4">
                  <Button
                    asChild
                    variant="secondary"
                    size="sm"
                    className="w-full"
                  >
                    <a
                      href={`/v1/evidence/${encodeURIComponent(clip.evidenceId)}/content`}
                      aria-label="Download clip"
                    >
                      <Download size={14} />
                      Export
                    </a>
                  </Button>
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
        )}
      </div>
    </Page>
  )
}
