import { useState } from 'react'
import { ShieldCheck } from 'lucide-react'
import { Page, PageHeader } from '@/components/Shell'
import { Panel, Section } from '@/components/ui/Panel'
import { Badge } from '@/components/ui/Badge'
import { Select } from '@/components/ui/Field'
import { EmptyState, ErrorState, LoadingState } from '@/components/ui/States'
import { KeyValue } from '@/components/ui/Table'
import { RecordingPlayer } from '@/components/RecordingPlayer'
import {
  useDevices,
  useRecordingChain,
  useRecordingClip,
  useRecordingTimeline,
  type RecordingChain,
  type RecordingTimeline,
} from '@/hooks/product'
import { describeError } from '@/lib/errors'

// The recording.
//
// The question this page answers is the one a VMS exists to answer: "is it
// recording?"
//
// The discipline the page keeps is the discipline the product keeps. A
// recording is judged on whether it is CONTINUOUS, and continuity is a
// MEASURED fact about the gaps between segments rather than a summary of
// whether any incident fired. So:
//
//   - a camera that recorded NOTHING says "no recording", and NOT "healthy" —
//     a camera that was never asked and a camera that failed are different
//     facts, and only one of them is a hole;
//   - a camera whose recording has a HOLE says so, and publishes the hole
//     with its size, because "broken" without a size is not actionable;
//   - the chain is the PROOF, and it is recomputed by the product from the
//     rows rather than read from a stored head.
export function RecordingsPage() {
  const devices = useDevices()
  const [deviceId, setDeviceId] = useState<string | null>(null)

  const known = devices.data?.items ?? []
  // The selected camera, and the first one when nothing is selected — so the
  // page is never empty for the sake of a control nobody touched.
  const selected = deviceId ?? known[0]?.deviceId ?? null

  const timeline = useRecordingTimeline(selected)
  const chain = useRecordingChain(selected)
  // The CLIP: the last minute of the recording, with its pre-roll.
  //
  // It is the last minute because that is the window an operator opens the
  // page for, and it is the window the product's own route answers.
  const now = new Date()
  const from = new Date(now.getTime() - 60_000).toISOString()
  const to = now.toISOString()
  const clip = useRecordingClip(selected, from, to)

  if (devices.isPending) {
    return (
      <Page>
        <PageHeader title="Recording" subtitle="What the product is writing down" />
        <LoadingState label="Reading the device registry" />
      </Page>
    )
  }

  if (devices.isError) {
    return (
      <Page>
        <PageHeader title="Recording" subtitle="What the product is writing down" />
        <ErrorState
          title="The device registry did not answer"
          detail={
            <>
              <p>
                The console asked the product which cameras it is responsible for and could not
                read the answer. Nothing below is empty because there is no recording — it is
                empty because the product did not say.
              </p>
              <p className="mt-2 font-mono text-[11px] text-text-dim">{describeError(devices.error)}</p>
            </>
          }
        />
      </Page>
    )
  }

  return (
    <Page>
      <PageHeader
        title="Recording"
        subtitle="What the product is writing down"
        lede="A recording is judged on whether it is continuous, and continuity is measured from the gaps between segments — not inferred from whether any incident fired."
      />

      <Section>
        <Panel
          title="The camera"
          action={
            known.length > 0 ? (
              <label className="block" htmlFor="recording-device">
                <span className="sr-only">Camera</span>
                <Select
                  id="recording-device"
                  value={selected ?? ''}
                  onChange={(e) => setDeviceId(e.target.value)}
                  aria-label="Camera"
                >
                  {known.map((d) => (
                    <option key={d.deviceId} value={d.deviceId}>
                      {d.displayName ?? d.deviceId}
                    </option>
                  ))}
                </Select>
              </label>
            ) : null
          }
        >
          {known.length === 0 ? (
            <EmptyState
              title="No device is registered"
              detail="The product is responsible for no camera, so there is nothing it could be recording. That is a fact about the site, and it is not the same as a site whose recorder is not configured."
            />
          ) : (
            <p className="text-[13px] leading-[20px] text-text-muted">
              The product owns the camera path, so it can say whether the recording is whole.
              The bytes live on the recorder's disk, in this site's own storage; what the
              console shows is the index over them.
            </p>
          )}
        </Panel>
      </Section>

      {selected && (
        <>
          <Section>
            <Panel
              title="The last minute"
              action={
                <span className="font-mono text-[11px] text-text-dim">
                  {clip.data ? `${clip.data.segments.length} segment(s)` : ''}
                </span>
              }
            >
              {clip.isPending ? (
                <LoadingState label="Reading the clip" />
              ) : clip.isError ? (
                <ErrorState
                  title="The clip did not answer"
                  detail={
                    <>
                      <p>
                        The console asked the product for the last minute of this camera's recording
                        and could not read the answer.
                      </p>
                      <p className="mt-2 font-mono text-[11px] text-text-dim">
                        {describeError(clip.error)}
                      </p>
                    </>
                  }
                />
              ) : (
                <RecordingPlayer
                  segments={clip.data?.segments ?? []}
                  preRoll={clip.data?.preRoll ?? null}
                />
              )}
              {clip.data?.preRoll ? (
                <p className="mt-3 text-[13px] leading-[20px] text-text-dim">
                  The clip opens with the <span className="font-mono">pre-roll</span>: the segment
                  immediately before the window, which is where the lead-in usually is.
                </p>
              ) : null}
            </Panel>
          </Section>

          <Section>
            <Panel
              title="Is it recording"
              action={
                <span className="font-mono text-[11px] text-text-dim">
                  {timeline.data?.continuity
                    ? `${timeline.data.continuity.segmentCount} segment${
                        timeline.data.continuity.segmentCount === 1 ? '' : 's'
                      }`
                    : ''}
                </span>
              }
            >
              {timeline.isPending ? (
                <LoadingState label="Reading the recording" />
              ) : timeline.isError ? (
                <ErrorState
                  title="The recording index did not answer"
                  detail={
                    <>
                      <p>
                        The console asked the product what this camera recorded and could not read
                        the answer. The panel below is empty because the product did not say, not
                        because the camera recorded nothing.
                      </p>
                      <p className="mt-2 font-mono text-[11px] text-text-dim">
                        {describeError(timeline.error)}
                      </p>
                    </>
                  }
                />
              ) : timeline.data?.available === false ? (
                <EmptyState
                  title="This install has no recording index"
                  detail={
                    timeline.data.unavailable ??
                    'The product says it has no recording index configured, which is a fact about the install and not about the camera.'
                  }
                />
              ) : (
                <ContinuityPanel timeline={timeline.data} />
              )}
            </Panel>
          </Section>

          {timeline.data?.available !== false && (
            <Section>
              <Panel title="Can it be proved">
                {chain.isPending ? (
                  <LoadingState label="Verifying the chain" />
                ) : chain.isError ? (
                  <ErrorState
                    title="The chain did not answer"
                    detail={
                      <>
                        <p>
                          The console asked the product whether this recording verifies and could
                          not read the answer.
                        </p>
                        <p className="mt-2 font-mono text-[11px] text-text-dim">
                          {describeError(chain.error)}
                        </p>
                      </>
                    }
                  />
                ) : (
                  <ChainPanel chain={chain.data} />
                )}
              </Panel>
            </Section>
          )}
        </>
      )}
    </Page>
  )
}

// ContinuityPanel renders the verdict and the holes.
//
// The verdict is one of three states and they are three DIFFERENT facts, so
// they get three different readings. "No recording" is not a failure state
// with a warning colour — it is an absence, and it is rendered as one.
function ContinuityPanel({ timeline }: { timeline: RecordingTimeline }) {
  const verdict = timeline.verdict
  const continuity = timeline.continuity

  const tone = verdict.state === 'continuous' ? 'ok' : verdict.state === 'broken' ? 'bad' : 'unknown'
  const glyph = verdict.state === 'continuous' ? '✓' : verdict.state === 'broken' ? '!' : '?'

  return (
    <div>
      <div className="flex flex-wrap items-center gap-3">
        <Badge tone={tone}>
          <span aria-hidden="true">{glyph}</span> {verdict.state.replace('_', ' ')}
        </Badge>
        <span className="text-[13px] leading-[20px] text-text-muted">{verdict.detail}</span>
      </div>

      <div className="mt-4">
        <KeyValue
          items={[
            { key: 'segments', value: String(continuity.segmentCount) },
            { key: 'covered', value: formatDuration(continuity.coveredMs) },
            { key: 'window', value: formatDuration(continuity.windowMs) },
            {
              key: 'holes',
              value:
                continuity.gapCount === 0
                  ? 'none'
                  : `${continuity.gapCount} (${formatDuration(continuity.gapMs)})`,
            },
          ]}
        />
      </div>

      {timeline.gaps.length > 0 && (
        <ul className="mt-4 divide-y divide-line">
          {timeline.gaps.map((gap) => (
            <li key={`${gap.startedAt}-${gap.endedAt}`} className="flex flex-wrap items-baseline justify-between gap-3 py-2 first:pt-0">
              <span className="font-mono text-[12px] text-text-muted">
                {gap.startedAt} → {gap.endedAt}
              </span>
              <span className="font-mono text-[12px] text-bad-ink">
                {formatDuration(gap.durationMs)} of silence
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

// ChainPanel renders the proof.
//
// The chain is per camera per day, and the head is sealed into the audit
// ledger at day close. The page shows the recomputed head and the recorded
// head side by side when they differ, because a mismatch the operator cannot
// see is a mismatch they cannot act on.
function ChainPanel({ chain }: { chain: RecordingChain }) {
  if (!chain.verified) {
    return (
      <div>
        <Badge tone="unknown">
          <span aria-hidden="true">?</span> not verified
        </Badge>
        <p className="mt-3 text-[13px] leading-[20px] text-text-muted">
          {chain.reason ??
            'The product could not verify this recording. A chain that cannot be verified is a recording that cannot be proved.'}
        </p>
        {chain.brokenAtSeq ? (
          <p className="mt-3 text-[13px] leading-[20px] text-bad-ink">
            The chain first differs at segment {chain.brokenAtSeq}. Everything from that segment
            on is not covered by the proof.
          </p>
        ) : null}
      </div>
    )
  }

  return (
    <div>
      <div className="flex flex-wrap items-center gap-3">
        <Badge tone="ok">
          <ShieldCheck size={12} aria-hidden="true" /> verified
        </Badge>
        <span className="text-[13px] leading-[20px] text-text-muted">
          {chain.segmentCount ?? 0} segment{(chain.segmentCount ?? 0) === 1 ? '' : 's'} on{' '}
          {chain.day}. The head is recomputed by the product from the rows and matches the one it
          stored.
        </span>
      </div>
      {chain.recordedHead ? (
        <p className="mt-3 break-all font-mono text-[11px] text-text-dim">head {chain.recordedHead}</p>
      ) : null}
    </div>
  )
}

// formatDuration renders a millisecond count as a human duration.
//
// It is the duration the operator reads, and it is rendered in the units a
// person uses: seconds, minutes, hours. "0.4 ms" is a number nobody can
// picture, and a hole of four minutes has to read as four minutes.
//
// A whole number reads as a whole number. "1.0 min" is a rendering of a
// number that was already whole, and it makes the reader do the arithmetic
// to find out that it was.
function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return '—'
  if (ms < 1000) return `${Math.round(ms)} ms`
  const seconds = ms / 1000
  if (seconds < 60) return `${whole(seconds)} s`
  const minutes = seconds / 60
  if (minutes < 60) return `${whole(minutes)} min`
  const hours = minutes / 60
  if (hours < 24) return `${whole(hours)} h`
  return `${whole(hours / 24)} days`
}

// whole renders a number without a decimal when it has no fraction.
function whole(n: number): string {
  return Number.isInteger(n) ? String(n) : n.toFixed(1)
}


