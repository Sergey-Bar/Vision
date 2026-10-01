package soak

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/detect"
	"github.com/Sergey-Bar/ai-captain-poc/internal/evidence"
	"github.com/Sergey-Bar/ai-captain-poc/internal/novelty"
	"github.com/Sergey-Bar/ai-captain-poc/internal/store"
	"github.com/Sergey-Bar/ai-captain-poc/internal/vlm"
)

// Trigger is the local novelty engine in the trigger path: cheap perception that
// PROPOSES, and never one that decides.
//
// It is an interface over internal/novelty rather than the concrete type so the
// pipeline can be exercised without pixels, and so a third trigger — a second
// camera mode, a test double — is a new implementation rather than a new set of
// conditionals in the frame loop.
type Trigger interface {
	// Candidates returns the measured regions this frame proposes, in normalised
	// 0..1 coordinates. Temporal triggers only.
	Candidates(rgb []byte, w, h int) ([]novelty.Candidate, novelty.Score)
	// Watch reports a sustained change of the whole view. Reference triggers only:
	// a global change has no region, so there is nothing to propose.
	Watch(at time.Time, rgb []byte, w, h int) (novelty.Finding, bool)
	// PinAndStore pins the reference view and records it, returning the content
	// hash so the decision record can name the picture every later measurement was
	// made against. A reference trigger with no pinned view measures nothing.
	PinAndStore(ctx context.Context, runID, cameraGUID string, rgb []byte, w, h int, jpegBytes []byte) (sha string, err error)
	// Config is the measurement settings, for the report.
	Config() novelty.Config
	// Last is the most recent score, for the screen.
	Last() novelty.Score
	// LastFinding is the most recent trigger event, for the screen.
	LastFinding() novelty.Finding
	// Pinned reports whether a reference view is pinned.
	Pinned() bool
}

// NoveltyTrigger adapts internal/novelty.Engine to [Trigger] and owns the two
// things an engine cannot: storing the pinned view as evidence, and recording that
// it was stored.
type NoveltyTrigger struct {
	engine *novelty.Engine
	store  *store.Store
	// evidenceStore owns the bytes. It is separate from the row store because the
	// two have different failure modes and a trigger missing one says which.
	evidenceStore *evidence.Store
	// DataRoot is where the reference JPEG is written before it is handed to the
	// evidence store. The evidence store is the only place bytes are written;
	// this is a staging path like every other cut.
	DataRoot string
}

// NewNoveltyTrigger builds a trigger over an engine.
func NewNoveltyTrigger(e *novelty.Engine, st *store.Store, ev *evidence.Store, dataRoot string) *NoveltyTrigger {
	return &NoveltyTrigger{engine: e, store: st, evidenceStore: ev, DataRoot: dataRoot}
}

// Candidates implements [Trigger].
func (t *NoveltyTrigger) Candidates(rgb []byte, w, h int) ([]novelty.Candidate, novelty.Score) {
	return t.engine.Candidates(rgb, w, h)
}

// Watch implements [Trigger].
func (t *NoveltyTrigger) Watch(at time.Time, rgb []byte, w, h int) (novelty.Finding, bool) {
	return t.engine.Watch(at, rgb, w, h)
}

// PinAndStore writes the reference view to the evidence store, records it, and
// pins the engine from the same bytes.
//
// The order matters: store first, then pin, and pin from the bytes just stored.
// Pinning first and storing after would leave the possibility of a measurement
// against a reference nobody can be shown, which is the whole thing a reference is
// for.
func (t *NoveltyTrigger) PinAndStore(ctx context.Context, runID, cameraGUID string, rgb []byte, w, h int, jpegBytes []byte) (string, error) {
	if t.store == nil {
		return "", errors.New("soak: a reference trigger with no store cannot record the view it measures against, and a " +
			"reference nobody can be handed is not a measurement")
	}
	staging := filepath.Join(t.DataRoot, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return "", fmt.Errorf("soak: cannot create the staging directory for the reference view: %w", err)
	}
	src := filepath.Join(staging, "reference.jpg")
	// The frame's own JPEG is what is stored when there is one, because that is
	// the picture a reviewer should be shown. The frame is re-encoded from pixels
	// as the fallback, and it is a fallback rather than the rule because a raw
	// pixel buffer is not a picture.
	if len(jpegBytes) > 0 {
		if err := os.WriteFile(src, jpegBytes, 0o644); err != nil {
			return "", fmt.Errorf("soak: cannot stage the reference view: %w", err)
		}
	} else if err := novelty.WriteRGBJPEG(rgb, w, h, src); err != nil {
		return "", err
	}
	stored, err := t.evidence().PutKind(src, evidence.Frame)
	if err != nil {
		return "", err
	}
	if err := t.store.PinReference(ctx, store.Reference{
		RunID: runID, CameraGUID: cameraGUID,
		StorageRef: evidence.StorageRefKindOf(stored.Hash, evidence.Frame),
		SHA256:     stored.Hash, PinnedAt: time.Now().UTC(), Source: "first_frame",
	}); err != nil {
		return "", err
	}
	if err := t.engine.Pin(rgb, w, h, stored.Hash, time.Now().UTC()); err != nil {
		return "", err
	}
	return stored.Hash, nil
}

// evidence is the byte store, held separately from the row store so a trigger can
// be built without one and say so rather than nil-panic on the first frame.
func (t *NoveltyTrigger) evidence() *evidence.Store { return t.evidenceStore }

// Config implements [Trigger].
func (t *NoveltyTrigger) Config() novelty.Config { return t.engine.Config() }

// Last implements [Trigger].
func (t *NoveltyTrigger) Last() novelty.Score { return t.engine.Last() }

// LastFinding implements [Trigger].
func (t *NoveltyTrigger) LastFinding() novelty.Finding { return t.engine.LastFinding() }

// Pinned implements [Trigger].
func (t *NoveltyTrigger) Pinned() bool { return t.engine.Pinned() }

// Observer is the VLM as the pipeline needs it: one question about one clip.
//
// It is an interface for the same reason [Trigger] is, and for one more: the
// replay in the proof chain takes NO observer at all, so the replay cannot call a
// provider even by accident. The absence of this parameter is the guarantee.
type Observer interface {
	// Observe asks one question about one clip.
	Observe(ctx context.Context, detectionID, class string, clip []byte, mediaType string) vlm.Observation
	// PromptIDFor resolves an incident class to a prompt, so the runner can record
	// the prompt version without reaching into the client's prompt set.
	PromptIDFor(class string) (id, version string, err error)
}

// NameOnly is the one thing about a verifier that a banner wants and the pipeline
// itself does not need. It is checked for rather than called, so an observer that
// does not care about being named does not have to implement it.
type NameOnly interface{ Name() string }

// NameOf returns the observer's name for a banner, or "the configured verifier".
func NameOf(o Observer) string {
	if n, ok := o.(NameOnly); ok {
		return n.Name()
	}
	return "the configured verifier"
}

// VerdictLine is what the screen burns into the picture.
//
// It lives in this package rather than in internal/ui because the dependency
// runs the other way: internal/ui imports internal/soak for its report, and a
// verdict type that internal/soak imported FROM internal/ui would be a cycle.
// The struct is plain strings on purpose — it crosses a package boundary into a
// drawing routine and a string cannot drag a dependency with it.
type VerdictLine struct {
	// Known is false when no answer has been recorded yet. It is what
	// distinguishes "the model said false_positive" from "the model was not
	// asked", and both have an empty Verdict.
	Known       bool
	Class       string
	PromptID    string
	Verdict     string
	Summary     string
	State       string
	Confidence  float64
	PushResult  string
	DetectionID string
}

// Verdict returns the most recent verdict for the screen.
//
// It is read from the store rather than remembered in a field, because the store
// is the thing the proof chain reads and the screen must not be able to show
// something the store does not hold.
func (r *Runner) Verdict(ctx context.Context) VerdictLine {
	if r == nil || r.opt.Store == nil {
		return VerdictLine{}
	}
	obs, err := r.opt.Store.LatestObservation(ctx, r.opt.RunID)
	if err != nil || obs == nil {
		return VerdictLine{}
	}
	out := VerdictLine{
		Known: true, Class: obs.PromptID, PromptID: obs.PromptID,
		Verdict: obs.Verdict, Summary: obs.Summary, State: obs.AIState,
		Confidence: obs.Confidence, DetectionID: obs.DetectionID,
	}
	// The push result is read from the same store for the same reason. "The model
	// confirmed it" and "the VMS accepted it" are different claims, and a
	// screenshot that carries only the first has been cropped to hide a failure.
	if res, detail, err := r.opt.Store.PushResultFor(ctx, obs.DetectionID); err == nil {
		out.PushResult = res
		if res == "" {
			out.PushResult = firstLineOr(detail, "push pending")
		}
	}
	return out
}

func firstLineOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}

// TriggerEvent is one crossing being turned into a recorded incident.
//
// It exists because the two incident classes produce the same thing from different
// arithmetic, and threading a `detect.Crossing` plus three parallel fields through
// six functions is how the two classes end up with two code paths.
type TriggerEvent struct {
	// DetectionClass is the detector's own name for what it measured. It is NOT
	// the incident class: the incident class is PromptID, and the plan is explicit
	// that the class is carried by the prompt rather than by a schema field.
	DetectionClass string
	// PromptID is the incident class, and the ONLY thing that differs between one
	// class and another.
	PromptID string
	// Line and Direction are for the crossing event's rule name and for the
	// report. A whole-frame change has no line, and says so.
	Line      string
	Direction string
	At        time.Time
	Box       [4]float64 // normalised 0..1: x1, y1, x2, y2
	TrackID   int
	Frames    int
	Score     float64
	// Novelty records the measurement, so the decision row can be written from
	// the same values the arithmetic used.
	Novelty novelty.Score
	// Streak is the sustained-frame count a reference trigger fired on.
	Streak int
	// RegionSource is `measured` or `whole_frame`, and it is carried rather than
	// inferred so the pushed event cannot imply a measurement that did not happen.
	RegionSource novelty.RegionSource
}

// EventTagFor is the idempotency key the read-back matches on, derived from the
// trigger's content.
//
// Content-derived, never a counter, because the API has no idempotency key and a
// counter would collide across a restart.
func (t TriggerEvent) EventTagFor(provenance string) string {
	h := hashParts("tag", provenance, t.DetectionClass, t.PromptID, t.Line, t.Direction,
		t.At.UTC().Format(time.RFC3339Nano),
		fmt.Sprintf("%.6f|%.6f|%.6f|%.6f", t.Box[0], t.Box[1], t.Box[2], t.Box[3]))
	return "aicap-" + strings.ToLower(sanitizeTag(t.PromptID)) + "-" + h[:16]
}

// DetectionIDFor is the content-derived identity of the incident.
func (t TriggerEvent) DetectionIDFor(provenance string) string {
	h := hashParts("det", provenance, t.DetectionClass, t.PromptID, t.Line, t.Direction,
		t.At.UTC().Format(time.RFC3339Nano),
		fmt.Sprintf("%.6f|%.6f|%.6f|%.6f", t.Box[0], t.Box[1], t.Box[2], t.Box[3]))
	return "det-" + h[:24]
}

func hashParts(parts ...string) string {
	return store.HashOf([]byte(strings.Join(parts, "\x00")))
}

// itoa64 is here rather than in the store because the ledger payload is built by
// the runner and the store already exports HashOf for the same reason: a helper
// shared by two packages is a helper both of them own.
func itoa64(n int64) string { return strconv.FormatInt(n, 10) }

// sanitizeTag keeps a prompt id usable in an XProtect tag: the tag is a string a
// human reads in Smart Client, and a prompt id with a space in it makes that a
// puzzle.
func sanitizeTag(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + 32)
		case r == '-' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if out == "" {
		return "event"
	}
	return out
}

// toTriggerEvent converts a line crossing into the shared shape.
//
// It is a conversion rather than a second implementation: the crossing already
// carries everything a temporal trigger event needs, and a trigger event that was
// built by hand from a crossing would be a second place for the box to be scaled.
func toTriggerEvent(c detect.Crossing, promptID string, score novelty.Score) TriggerEvent {
	return TriggerEvent{
		DetectionClass: c.Class, PromptID: promptID, Line: c.Line, Direction: c.Direction,
		At: c.At, Box: [4]float64{c.Box.X1, c.Box.Y1, c.Box.X2, c.Box.Y2},
		TrackID: c.TrackID, Frames: c.Frames, Score: c.Confidence(),
		Novelty: score, RegionSource: novelty.RegionMeasured,
	}
}

// decisionFrom builds the stored decision inputs from a trigger event.
//
// Everything here is arithmetic on the measurement, and none of it is the model:
// the model verified an alert that was already raised, and the decision to raise
// it was made from the score and the threshold alone. That is what makes the
// replay in W3-4 possible with zero provider calls.
func decisionFrom(t TriggerEvent, runID string, cfg novelty.Config, frameSeq uint64, refSHA string) store.Decision {
	top := int(t.Box[1] * 1000)
	left := int(t.Box[0] * 1000)
	bottom := int(t.Box[3] * 1000)
	right := int(t.Box[2] * 1000)
	if t.RegionSource == novelty.RegionWholeFrame {
		// A whole-frame region is stated as the whole frame rather than left to
		// whatever the arithmetic happened to produce. Zero is not the honest box
		// for "everything changed".
		top, left, bottom, right = 0, 0, 1000, 1000
	}
	return store.Decision{
		DetectionID: t.DetectionIDFor(""), RunID: runID,
		TriggerMode: string(cfg.Mode), TriggerClass: t.DetectionClass,
		Score: t.Novelty.Value, Threshold: cfg.Threshold, Release: cfg.Release,
		Sustain: cfg.Sustain, Streak: t.Streak, ScoreReason: string(t.Novelty.Reason),
		RegionSource: string(t.RegionSource),
		RegionTop:    top, RegionLeft: left, RegionBottom: bottom, RegionRight: right,
		FrameSeq: int64(frameSeq), ReferenceSHA: refSHA,
	}
}

// ledgerEntryFor renders a ledger payload with a FIXED key order.
//
// A JSON object built from a map has whatever order the encoder chooses, so two
// runs over the same incident produce different bytes and the chain hash of an
// otherwise identical run differs. That would make the chain verify and still be
// unable to say the two runs did the same thing.
func ledgerEntryFor(kind, subject string, fields ...string) LedgerPayload {
	return LedgerPayload{Kind: kind, Subject: subject, Fields: fields}
}

// LedgerPayload is one recorded decision step's payload, already rendered.
type LedgerPayload struct {
	Kind    string
	Subject string
	Fields  []string
}

// Append records one ledger step. It is a method so a run cannot write to the
// ledger except through the store that owns it.
func (r *Runner) appendLedger(ctx context.Context, kind, subject string, fields ...string) {
	if r.opt.Store == nil {
		return
	}
	p := ledgerEntryFor(kind, subject, fields...)
	payload, err := json.Marshal(p)
	if err != nil {
		// A payload that will not marshal is a step that cannot be recorded, and
		// recording it as an empty payload would put a row in the chain that says
		// nothing about what happened.
		_ = r.opt.Store.RecordFault(ctx, r.opt.RunID, store.Fault{
			At: time.Now(), Reason: store.FaultClipRefused,
			Detail: fmt.Sprintf("the ledger step %q for %s could not be encoded and was NOT recorded as an empty row: %v", kind, subject, err),
		})
		return
	}
	if _, err := r.opt.Store.AppendLedger(ctx, store.LedgerEntry{
		At: time.Now(), Kind: p.Kind, Subject: p.Subject, Payload: string(payload),
	}); err != nil {
		_ = r.opt.Store.RecordFault(ctx, r.opt.RunID, store.Fault{
			At: time.Now(), Reason: store.FaultDBUnavailable,
			Detail: fmt.Sprintf("the ledger step %q for %s could not be appended: %v", kind, subject, err),
		})
	}
}
