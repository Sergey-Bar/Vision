import { useEffect, useRef, useState } from 'react'
import { Play, Square } from 'lucide-react'
import { Button } from '@/components/ui/Button'
import { Badge } from '@/components/ui/Badge'
import type { RecordingClipSegment } from '@/hooks/product'

// The recording player.
//
// # Why the clip is played as a SEQUENCE
//
// Each segment is an independently playable fragmented MP4, and the product's
// own recorder wrote them that way on purpose: a segment that cannot be
// decoded on its own is not evidence.
//
// A browser's `<video>` plays ONE source. So the player plays the segments
// one after another, and the PRE-ROLL first — the seconds before the window,
// which is where the lead-in is.
//
// # What the player does NOT do
//
// It does not stitch the bytes into one file. Stitching means re-muxing, and
// a re-mux is a second implementation of a muxer that already exists. The
// segments are CONTIGUOUS by construction — the keyframe that ends one is the
// first frame of the next — so playing them in sequence is playing a
// continuous recording.

// The player's props.
export interface RecordingPlayerProps {
  // segments are the clip's segments, in order.
  segments: RecordingClipSegment[]
  // preRoll is the segment before the window, when there is one.
  preRoll?: RecordingClipSegment | null
  // onError reports a segment that would not play.
  onError?: (message: string) => void
}

export function RecordingPlayer({ segments, preRoll, onError }: RecordingPlayerProps) {
  const videoRef = useRef<HTMLVideoElement | null>(null)
  const [index, setIndex] = useState(0)
  const [playing, setPlaying] = useState(false)

  // The pre-roll comes FIRST, and it is part of the clip: the lead-in is
  // what the operator came for.
  const queue = [...(preRoll ? [preRoll] : []), ...segments]
  const current = queue[index]

  // A new clip starts from the beginning.
  useEffect(() => {
    setIndex(0)
    setPlaying(false)
  }, [segments, preRoll])

  // And the next segment plays when this one ends.
  useEffect(() => {
    const video = videoRef.current
    if (!video) return
    const onEnded = () => {
      setIndex((i) => {
        if (i + 1 >= queue.length) {
          setPlaying(false)
          return i
        }
        return i + 1
      })
    }
    video.addEventListener('ended', onEnded)
    return () => video.removeEventListener('ended', onEnded)
  }, [queue.length])

  // And the next segment is LOADED when the index moves, so the play is
  // continuous rather than a gap between two fetches.
  useEffect(() => {
    const video = videoRef.current
    if (!video || !current) return
    video.src = current.contentUrl
    video.load()
    if (playing) {
      void video.play().catch(() => {
        setPlaying(false)
        onError?.('The browser refused to play this segment.')
      })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [index])

  if (queue.length === 0) {
    return (
      <p className="text-[13px] leading-[20px] text-text-dim">
        There is nothing recorded in this window to play.
      </p>
    )
  }

  return (
    <div>
      {/* There is NO caption track, and that is a real gap rather than an
          oversight.

          The linter is right that a video without captions is not accessible,
          and the product does not make captions for the recording: the AI
          plane produces OBSERVATIONS, and an observation is a statement about
          a moment, not a timed caption track over a ten-second video.
          Serving one would mean the product inventing timings it does not
          have.

          The gap is recorded here rather than hidden behind an empty track:
          an empty <track> is a caption file that says nothing, and a caption
          file that says nothing is worse than an absent one, because a screen
          reader announces that captions exist and then nothing. */}
      {/* eslint-disable-next-line jsx-a11y/media-has-caption */}
      <video
        ref={videoRef}
        className="w-full rounded border border-line bg-black"
        controls
        playsInline
        onError={() => onError?.('This segment would not play.')}
      />
      <div className="mt-3 flex flex-wrap items-center gap-3">
        <Button
          variant="secondary"
          size="sm"
          onClick={() => {
            const video = videoRef.current
            if (!video) return
            if (playing) {
              video.pause()
              setPlaying(false)
              return
            }
            setPlaying(true)
            void video.play().catch(() => {
              setPlaying(false)
              onError?.('The browser refused to play this segment.')
            })
          }}
        >
          {playing ? (
            <Square size={14} aria-hidden="true" />
          ) : (
            <Play size={14} aria-hidden="true" />
          )}
          {playing ? 'Pause' : 'Play'}
        </Button>
        <Badge tone="unknown">
          segment {index + 1} of {queue.length}
        </Badge>
        {preRoll ? (
          <span className="font-mono text-[11px] text-text-dim">including the pre-roll</span>
        ) : null}
        {current ? (
          <span className="font-mono text-[11px] text-text-dim">
            {current.startedAt} → {current.endedAt}
          </span>
        ) : null}
      </div>
    </div>
  )
}
