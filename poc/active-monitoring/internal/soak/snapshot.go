package soak

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"os"
	"path/filepath"

	"github.com/Sergey-Bar/ai-captain-poc/internal/evidence"
	"github.com/Sergey-Bar/ai-captain-poc/internal/overlay"
	"github.com/Sergey-Bar/ai-captain-poc/internal/store"
)

// The crossing snapshot. T6.3.
//
// It is the picture of the DECISION: the frame the pipeline was looking at when
// it decided this track had crossed, with the APPROVED boxes and the crossing
// line drawn on it, stored beside the clip and pointed at by
// `detections.snapshot_ref`.
//
// The clip answers "what happened". This answers "what did the system see, and
// what did it decide that was a crossing" — which is the question an operator
// asks three days later when the clip has been rotated away and the run is over.
//
// Four things already existed and this wires them together: `overlay.Draw`,
// `evidence.PutKind`, `store.SetSnapshot` and `LastJPEG`. The drawing code lives
// in `internal/overlay` and not here because `internal/ui` needs it too and both
// of them sit above `internal/soak`; `scripts/verify.ps1` has a boundary row for
// exactly that and would fail the build if the drawing were inlined into either.

// storeSnapshot renders, stores and records the snapshot for one detection.
//
// It runs BEFORE `cutAndStore`, and that order is the point: the cut is a
// synchronous ffmpeg re-encode that blocks the frame loop for as long as the
// clip takes, and a snapshot written after it would be a snapshot of a LATER
// frame. The picture of a decision has to be the frame the decision was made on.
//
// W2-5: the picture and the sequence are read TOGETHER and the sequence is
// compared against the frame the decision was made on. The pointer used to be read
// with no sequence at all, so "the picture of the decision" was in fact "the
// newest picture", which is a different thing and is a lie once the snapshot is
// evidence in a proof chain. A mismatch is REFUSED rather than stored: an
// unattributable snapshot is not evidence, and a mis-attributed one is worse than
// none.
//
// A failure here is recorded and the run continues. A missing snapshot is
// visible — the screen renders `no data` — and a run that died because a JPEG
// could not be encoded would have thrown away a real detection over a
// convenience.
func (r *Runner) storeSnapshot(ctx context.Context, id string, ev TriggerEvent, aiState string, frameSeq uint64) {
	if r.opt.Evidence == nil || r.opt.FrameJPEGFor == nil {
		if r.opt.Evidence != nil {
			r.noteSnapshotFault(ctx, ev, "no snapshot was recorded for "+id+" because the frame source cannot produce the "+
				"picture of a NAMED frame; a snapshot that cannot be attributed to an instant is not evidence of one")
		}
		return
	}
	raw, ok := r.opt.FrameJPEGFor(frameSeq)
	if !ok || len(raw) == 0 {
		// REDCHECK RC-5: the snapshot must be the frame the decision was made on.
		//
		// Removing this refusal is what a mis-attributed proof chain looks like:
		// the picture on file is a real frame of the same camera and a different
		// instant, and every check that only asks "is there a picture?" still
		// passes.
		var newest uint64
		if r.opt.LastJPEGSeq != nil {
			_, newest = r.opt.LastJPEGSeq()
		}
		r.noteSnapshotFault(ctx, ev, fmt.Sprintf("no snapshot was recorded for %s: frame %d is no longer held and the newest "+
			"picture is frame %d, so attributing one to the other would file a different instant's picture under this decision",
			id, frameSeq, newest))
		return
	}
	drawn, err := r.drawSnapshot(raw, aiState)
	if err != nil {
		r.noteSnapshotFault(ctx, ev, "the crossing snapshot could not be drawn: "+err.Error())
		return
	}

	staging := filepath.Join(r.opt.DataRoot, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		r.noteSnapshotFault(ctx, ev, "the staging directory for the crossing snapshot could not be created: "+err.Error())
		return
	}
	tmp := filepath.Join(staging, id+"-snapshot.jpg")
	if err := os.WriteFile(tmp, drawn, 0o644); err != nil {
		r.noteSnapshotFault(ctx, ev, "the crossing snapshot could not be written: "+err.Error())
		return
	}
	// PutKind reads the copy back out of the store and hashes it before it
	// returns, so a snapshot that exists is a snapshot whose bytes are on the
	// medium — the same R5 the clip path has always had.
	stored, err := r.opt.Evidence.PutKind(tmp, evidence.CrossingSnapshot)
	if err != nil {
		r.noteSnapshotFault(ctx, ev, "the crossing snapshot could not be stored: "+err.Error())
		return
	}
	ref := r.opt.Evidence.StorageRefKind(stored.Hash, stored.Kind)
	if err := r.opt.Store.SetSnapshot(ctx, id, ref, stored.Kind); err != nil {
		r.noteSnapshotFault(ctx, ev, "the crossing snapshot was stored but not recorded against the detection: "+err.Error())
		return
	}
	r.appendLedger(ctx, store.LedgerClipStored, id,
		"snapshot_ref="+ref, "snapshot_kind="+stored.Kind,
		"snapshot_frame_seq="+itoa64(int64(frameSeq)))
	r.logf("crossing snapshot %s (%d bytes) recorded as %s for frame %d", stored.Hash[:12], stored.Bytes, stored.Kind, frameSeq)
}

func (r *Runner) noteSnapshotFault(ctx context.Context, ev TriggerEvent, detail string) {
	_ = r.opt.Store.RecordFault(ctx, r.opt.RunID, store.Fault{
		At: ev.At, Reason: store.FaultClipRefused, Detail: detail,
	})
	r.logf("%s", detail)
}

// drawSnapshot renders the last frame with the picture the pipeline decided on.
//
// The boxes are `Engine.LastSplit().Approved` — the boxes the pipeline ACTED on,
// not the raw model output. Drawing the raw output would make the snapshot
// evidence for a decision that was never made, which is the same defect D-6
// fixed on the live screen and it would be reintroduced in the one artefact that
// outlives the run.
func (r *Runner) drawSnapshot(raw []byte, aiState string) ([]byte, error) {
	src, err := jpeg.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("the last frame is not decodable: %w", err)
	}
	b := src.Bounds()
	canvas := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(canvas, canvas.Bounds(), src, b.Min, draw.Src)

	spec := overlay.Spec{
		Provenance:   r.opt.Provenance,
		AIState:      aiState,
		AIStateKnown: aiState != "",
		Note:         "the frame the crossing was decided on, with the boxes the pipeline approved",
		// The footer, and the reason a SNAPSHOT carries it is the same as for the
		// live picture: this PNG is the artefact. An operator who receives it three
		// days later has no way to learn which model produced the boxes, what it cost,
		// or whether this run's replay reads a complete per-frame log — and a picture
		// that omits all three is a picture that makes a system which cannot hold its
		// frame rate look like one that can.
		Footer: r.footer(),
	}
	for _, l := range r.opt.Zone.Lines {
		spec.Lines = append(spec.Lines, overlay.Line{Name: l.Name, A: l.A, B: l.B})
	}
	if r.opt.Engine != nil {
		for _, bx := range r.opt.Engine.LastSplit().Approved {
			spec.Boxes = append(spec.Boxes, overlay.TrackedBox{Box: overlay.Box{
				Class: bx.Class, Score: bx.Score, X1: bx.X1, Y1: bx.Y1, X2: bx.X2, Y2: bx.Y2,
			}})
		}
	}
	overlay.Draw(canvas, spec)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, canvas, &jpeg.Options{Quality: 80}); err != nil {
		return nil, fmt.Errorf("the drawn snapshot could not be encoded: %w", err)
	}
	return buf.Bytes(), nil
}
