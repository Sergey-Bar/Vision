package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/overlay"
	"github.com/Sergey-Bar/ai-captain-poc/internal/soak"
	"github.com/Sergey-Bar/ai-captain-poc/internal/store"
)

// DrawOverlay draws the approved boxes, the tracked markers and the lines onto the
// last frame and encodes a JPEG.
//
// The drawing itself is `internal/overlay`, which is a leaf package. It is not
// here any more and that is the fix for two defects at once:
//
//   - D6. This used to draw the RAW model boxes — below threshold, outside the
//     approved class list — so the operator saw what the detector produced and
//     never what the pipeline decided. Now `Live.Boxes` carries the APPROVED boxes
//     and `Live.Ghosts` the dimmed rejects, and the picture and the decision
//     agree.
//   - D7. The font lived in `font_windows.go` under `//go:build windows`, so this
//     package did not compile on Linux at all. The face is now in
//     `internal/overlay/font.go` with no build tag.
//
// The JPEG encode stays here because the screen owns the HTTP response; the
// overlay owns the pixels.
func DrawOverlay(l Live) ([]byte, error) {
	if len(l.Frame) == 0 {
		return nil, fmt.Errorf("ui: no frame to draw on")
	}
	src, err := jpeg.Decode(bytes.NewReader(l.Frame))
	if err != nil {
		return nil, fmt.Errorf("ui: the last frame is not decodable: %w", err)
	}
	bounds := src.Bounds()
	canvas := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(canvas, canvas.Bounds(), src, bounds.Min, draw.Src)

	spec := l.OverlaySpec()
	// The note line is composed here rather than in the overlay because it needs
	// the count of what the cap left off, which is the overlay's decision and this
	// is where the text about it is written.
	if note := overlay.SuppressionText(spec.Suppressed); note != "" {
		spec.Note = note
	}
	overlay.Draw(canvas, spec)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, canvas, &jpeg.Options{Quality: 80}); err != nil {
		return nil, fmt.Errorf("ui: the drawn frame could not be encoded: %w", err)
	}
	return buf.Bytes(), nil
}

// OverlaySpec converts the screen's live state into the leaf package's drawing
// description. It is the ONLY place the two shapes meet, so a field added to one
// and not the other is a compile error rather than a silently missing picture
// element.
func (l Live) OverlaySpec() overlay.Spec {
	chosen, suppressed := overlay.Select(overlay.Spec{Boxes: l.TrackedBoxes(), MaxBoxes: l.MaxBoxesDrawn})
	var lines []overlay.Line
	for _, ln := range l.Lines {
		lines = append(lines, overlay.Line{Name: ln.Name, A: ln.A, B: ln.B})
	}
	var ghosts []overlay.Box
	for _, g := range l.Ghosts {
		ghosts = append(ghosts, g.Box())
	}
	return overlay.Spec{
		Lines:          lines,
		Boxes:          chosen,
		Ghosts:         ghosts,
		MaxBoxes:       l.MaxBoxesDrawn,
		Suppressed:     suppressed,
		Provenance:     l.Provenance,
		AIState:        l.AIState,
		At:             l.AtLabel(),
		AIStateKnown:   l.AIState != "",
		Verdict:        l.Verdict,
		VerdictKnown:   l.VerdictKnown,
		VerdictSummary: l.VerdictSummary,
		PushResult:     l.PushResult,
		Counters:       l.Counters,
	}
}

// TrackedBoxes pairs each approved box with the track that owns it, so the label
// plate can name the track id (T8.2) and the velocity arrow has an origin (T8.3).
//
// A box with no track is drawn with no `T` at all rather than `T0`: T0 is a track
// that does not exist, and printing it would put a number on the picture that
// matches no row in the table.
func (l Live) TrackedBoxes() []overlay.TrackedBox {
	byGeometry := make(map[OverlayBox]int, len(l.Tracks))
	rows := make(map[int]TrackRow, len(l.Tracks))
	for _, t := range l.Tracks {
		byGeometry[t.Box] = t.ID
		rows[t.ID] = t
	}
	out := make([]overlay.TrackedBox, 0, len(l.Boxes))
	for _, b := range l.Boxes {
		tb := overlay.TrackedBox{Box: b.Box()}
		if id, ok := byGeometry[b]; ok {
			tb.Track = overlay.TrackID(id)
			tb.Seen = rows[id].Seen
			if rows[id].HasPrev {
				tb.HasPrev, tb.PrevX, tb.PrevY = true, rows[id].PrevX, rows[id].PrevY
			}
		}
		out = append(out, tb)
	}
	return out
}

// Box converts a screen box into the leaf package's.
func (b OverlayBox) Box() overlay.Box {
	return overlay.Box{Class: b.Class, Score: b.Score, X1: b.X1, Y1: b.Y1, X2: b.X2, Y2: b.Y2}
}

// AtLabel is the frame's instant, printed on the picture so a screenshot is
// self-describing. It is the frame's own clock — the one Go stamped when it read
// the frame — and never a second reading of time taken at render time.
func (l Live) AtLabel() string {
	if l.At.IsZero() {
		return ""
	}
	return l.At.UTC().Format("2006-01-02T15:04:05Z")
}

// evidenceRoot is the content-addressed store beside the database, which is
// where `evidence.Store` writes. It is derived from the store's own path rather
// than configured, so the screen cannot be pointed at a different directory than
// the one the pipeline stored into.
func (s *Server) evidenceRoot() string {
	return filepath.Join(filepath.Dir(s.Store.Path()), "evidence")
}

// snapshotRefShape is the ONLY shape a storage reference may have: two levels of
// directory, a 64-character lowercase hex hash, and the extension the evidence
// kind chose.
//
// This is a guard, not a validation of niceness. The reference arrives in a
// query string, and a screen that joins a query string onto a path is one
// `?ref=../../poc.db` away from serving the database — so the shape is checked
// against a pattern here rather than trusted because the value came out of a
// column this process wrote.
//
// A reference that does not match is REFUSED, and the message says the shape,
// because a refusal nobody can act on is a refusal nobody will act on.
var snapshotRefShape = regexp.MustCompile(`^[0-9a-f]{2}/[0-9a-f]{2}/[0-9a-f]{64}\.(jpg|mp4|bin)$`)

// serveSnapshot returns the rendered crossing snapshot for a detection.
//
// It is the picture of the DECISION and it is read-only, from the evidence
// store, by a reference that has already been checked against the store's own
// naming rule. A reference with no file behind it is `404` with the phrase the
// screen uses everywhere else for "not recorded" — not a grey placeholder,
// which reads as "there was no object there".
func (s *Server) serveSnapshot(w http.ResponseWriter, r *http.Request) {
	ref := r.URL.Query().Get("ref")
	if !snapshotRefShape.MatchString(ref) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("no data — a snapshot reference is " + snapshotRefShape.String() +
			", and " + strconv.Quote(ref) + " is not one. The reference is content-derived and is never chosen by a caller."))
		return
	}
	body, err := os.ReadFile(filepath.Join(s.evidenceRoot(), filepath.FromSlash(ref)))
	if err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("no data — this detection has a snapshot reference but the file is not in the evidence store. " +
			"The disk sweep is age-based and may have taken it; the row records the reference, not a promise the bytes are still here."))
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}

func inputsFrom(d Data) soak.Inputs {
	in := soak.Inputs{
		Provenance:            d.Live.Provenance,
		PushTargetIsSimulator: d.Live.Simulator,
		SourceKind:            d.Live.Source,
		EvidenceStored:        d.Evidence.Stored,
		EvidenceVerified:      d.Evidence.Verified,
		EvidenceMismatched:    d.Evidence.Mismatched,
		PushAttempts:          d.Push.Total,
		PushesWithResult:      d.Push.WithResult,
		UnattributedPushes:    d.Push.Unattributed,
		DeadLettered:          d.Push.DeadLetter,
		HeartbeatCount:        d.Heartbeats,
		ResourceSamples:       d.Peaks.Samples,
		PeakRSSBytes:          d.Peaks.RSSBytes,
		PeakDBBytes:           d.Peaks.DB,
		PeakDiskBytes:         d.Peaks.DiskBytes,
		ProcessExits:          d.Counts["process_exit"],
	}
	for _, g := range d.Gaps {
		in.HeartbeatGaps = append(in.HeartbeatGaps, soak.GapMeasurement{From: g.From, To: g.To, Seconds: g.Seconds})
	}
	for _, s := range d.Stalled {
		in.StalledDetections = append(in.StalledDetections, soak.StalledDetection{
			ID: s.ID, Status: s.Status, Age: time.Since(s.UpdatedAt),
		})
	}
	in.TotalDetections = 0
	for _, v := range d.Counts {
		in.TotalDetections += v
	}
	if d.Run != nil {
		in.Target = time.Duration(d.Run.TargetSeconds) * time.Second
		end := time.Now().UTC()
		if d.Run.EndedAt != nil {
			end = *d.Run.EndedAt
		}
		in.Elapsed = end.Sub(d.Run.StartedAt)
	}
	// The screen does not know the declared ceilings or the rehearsal outcomes;
	// those live in the run's configuration, and a screen that guessed them
	// would be a screen scoring against numbers nobody declared.
	return in
}

func verdictOf(in soak.Inputs) soak.Verdict {
	return soak.Overall(in, soak.Score(in)).Verdict
}

func criteriaFor(d Data) []soak.Criterion {
	in := inputsFrom(d)
	cs := soak.Score(in)
	out := make([]soak.Criterion, 0, len(cs))
	for _, c := range cs {
		// The screen labels the criteria it cannot fully compute as NOT RUN with
		// a reason, rather than showing a number it got from nowhere.
		if !hasDeclaredCeilings(d) && (c.ID == "AC5" || c.ID == "AC6" || c.ID == "AC7") {
			c.Verdict = soak.VerdictNotRun
			c.Missing = "the screen reads only the run's rows; the declared ceilings and the rehearsal outcomes live in the run configuration, and the screen will not invent them"
			c.Observed = "not computed on the screen"
			c.Clauses = nil
		}
		out = append(out, c)
	}
	return out
}

func hasDeclaredCeilings(d Data) bool {
	// The peaks table carries samples; the ceilings live in the process
	// configuration. A run with no samples at all cannot be scored at all.
	return d.Peaks.Samples > 0
}

var _ = store.Terminal
