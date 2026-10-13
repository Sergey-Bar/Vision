package milestone

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision/internal/clock"
)

// The write-back on the incident path.
//
// # Why this lives here and not in a caller
//
// The plan's P4.4 is "the write-back bookmark lands on the incident the operator
// is looking at, with the clip pre-rolled and the time pre-rolled into the
// vendor's own recorder." Two halves of that are this package's job and one is
// not:
//
//   - deciding *what* to bookmark (the incident, not a clip the caller built),
//   - refusing to place one that would be wrong.
//   - computing the vendor's time for it, from the vendor's own clock.
//
// The caller supplies the facts. This decides the bookmark, because a decision
// made in two places is a decision that will be made differently in the second
// place, and the whole cost of the design is that the operator must be able to
// find the moment without scrubbing.

// BookmarkRequest is what an incident knows about itself.
type BookmarkRequest struct {
	// IncidentID is the identity the operator sees. It goes in the label, so the
	// bookmark list is searchable by the same string the operator already has in
	// front of them.
	IncidentID string
	// SiteRef is the vendor's own reference for the camera, from the adapter.
	// It is never taken from a request body.
	SiteRef string
	// SiteID is our reference, carried for the label and for the audit trail.
	SiteID string
	// WindowStart and WindowEnd are the incident's correlation window on the
	// reference clock.
	WindowStart, WindowEnd time.Time
	// PreRoll is how far before the window to open. The vendor's recorder is a
	// recording of what already happened, so a bookmark at the start of the
	// window is a bookmark at the moment the operator does not yet care about.
	PreRoll time.Duration
	// PostRoll is how far after. The event's tail is usually where the evidence
	// of what it was is.
	PostRoll time.Duration
	// Summary is one line, for the operator's bookmark list.
	Summary string
	// Drift is the measured difference between the vendor's clock and ours. The
	// correction is applied here rather than left to the caller, because a
	// bookmark placed at the wrong second is a bookmark that does not open on
	// the event, and a bookmark that does not open looks exactly like a system
	// that has no evidence.
	Drift Drift
}

// BookmarkDecision is what was placed, or why not.
type BookmarkDecision struct {
	Placed bool
	Ref    BookmarkRef
	// OpenedAt is the instant in the vendor's own recorder the operator will land
	// on, which is the number the whole feature exists to produce.
	OpenedAt time.Time
	// Refusal is the reason no bookmark was placed, or empty. Every refusal path
	// names itself: a bookmark silently not placed is an operator who follows
	// an incident and finds nothing, and the absence of a bookmark is
	// indistinguishable from the absence of evidence.
	Refusal string
}

// ErrNoBookmark is returned when the request cannot produce a bookmark at all.
var ErrNoBookmark = errors.New("milestone: no bookmark could be decided")

// Refusal reasons, all strings because they are read by a human and by nothing
// else. Nothing parses them, and nothing may.
const (
	// RefusalDriftSevere: the clock is wrong badly enough that a bookmark placed
	// now would open at the wrong second. Better no bookmark than a wrong one.
	RefusalDriftSevere = "clock_drift_severe"
	// RefusalNoSiteRef: without the vendor's own camera reference there is
	// nothing to point at.
	RefusalNoSiteRef = "no_site_reference"
	// RefusalNoWindow: a bookmark with no window is a bookmark at an unknown
	// instant.
	RefusalNoWindow = "no_window"
)

// DecideBookmark computes the bookmark an incident implies, and refuses the ones
// that would be wrong.
//
// It is a pure function: same facts, same decision. That is what makes the
// refusal testable, and a refusal nobody can test is a refusal nobody can trust.
func DecideBookmark(req BookmarkRequest, policy clock.Policy) (BookmarkDecision, time.Time, time.Time) {
	switch {
	case strings.TrimSpace(req.SiteRef) == "":
		return BookmarkDecision{Refusal: RefusalNoSiteRef}, time.Time{}, time.Time{}
	case req.WindowStart.IsZero() || req.WindowEnd.IsZero() || !req.WindowEnd.After(req.WindowStart):
		return BookmarkDecision{Refusal: RefusalNoWindow}, time.Time{}, time.Time{}
	}

	// The drift gate comes before the arithmetic, because the arithmetic is
	// exact and the clock it corrects with is not.
	if allowed, exclusion := policy.CorrelationAllowed(&clock.DeviceState{
		DeviceID: req.SiteRef, DriftMs: req.DriftMsFrom(req.Drift), Band: req.Band(policy),
	}); !allowed {
		reason := RefusalDriftSevere
		if exclusion != nil && exclusion.Reason != "" {
			reason = exclusion.Reason
		}
		return BookmarkDecision{Refusal: reason}, time.Time{}, time.Time{}
	}

	// The window is widened by the pre-roll and post-roll the plan asks for, and
	// the drift is subtracted, because the vendor stamped its own recording on
	// its own clock.
	from := req.WindowStart.Add(-req.PreRoll)
	to := req.WindowEnd.Add(req.PostRoll)
	if req.Drift.Offset != 0 {
		from = from.Add(req.Drift.Offset)
		to = to.Add(req.Drift.Offset)
	}
	return BookmarkDecision{Placed: true, OpenedAt: from}, from, to
}

// DriftMsFrom is the drift in the units the policy bands are expressed in.
func (r BookmarkRequest) DriftMsFrom(d Drift) int32 {
	return int32(d.Offset.Milliseconds()) //nolint:gosec // a drift figure is milliseconds
}

// Band is the drift's band under a policy.
func (r BookmarkRequest) Band(p clock.Policy) clock.Band {
	return p.Band(r.DriftMsFrom(r.Drift))
}

// BuildBookmark renders the decision into the wire shape.
//
// The idempotency key is derived from the incident and the window rather than
// minted, because a retry after a timeout must not create a second bookmark and
// a second bookmark at the same second is an operator-visible defect. A derived
// key survives a process restart; a random one does not.
func BuildBookmark(req BookmarkRequest, openedAt time.Time) Bookmark {
	label := fmt.Sprintf("%s: %s", req.IncidentID, firstLine(req.Summary))
	if len(label) > BookmarkLabelMax {
		label = label[:BookmarkLabelMax]
	}
	return Bookmark{
		IdempotencyKey: BookmarkKey(req.IncidentID, req.SiteRef, openedAt),
		SiteRef:        req.SiteRef,
		Time:           openedAt,
		Label:          label,
		Description: fmt.Sprintf(
			"Unverified change on %s, correlation window %s to %s. This bookmark opens at the "+
				"start of the pre-rolled window, not at the moment the window begins, because the "+
				"thing that caused it is usually earlier than the window that detected it.",
			req.SiteID,
			req.WindowStart.UTC().Format(time.RFC3339),
			req.WindowEnd.UTC().Format(time.RFC3339)),
	}
}

// BookmarkLabelMax bounds a vendor-side label.
//
// A label is a single line in somebody else's UI, and an unbounded one is a
// bookmark list nobody can scan.
const BookmarkLabelMax = 120

// BookmarkKey derives the idempotency key for an incident's bookmark.
//
// Three inputs and no clock: the incident, the camera, and the instant. A key
// that varied with anything else would produce a second bookmark on a retry, and
// a key that varied with *nothing* else would collide across incidents.
func BookmarkKey(incidentID, siteRef string, at time.Time) string {
	return "bmk_" + incidentID + "_" + shortHash(siteRef+"\x00"+at.UTC().Format(time.RFC3339Nano))
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// PlaceBookmark decides and, if the decision is one, places it.
//
// The two halves are separated so the decision can be tested without a vendor,
// and so a caller that has a dry-run mode can get everything but the network.
func PlaceBookmark(ctx context.Context, req BookmarkRequest, w BookmarkWriter, policy clock.Policy) (BookmarkDecision, error) {
	decision, openedAt, _ := DecideBookmark(req, policy)
	if decision.Refusal != "" {
		return decision, fmt.Errorf("%w: %s", ErrNoBookmark, decision.Refusal)
	}
	ref, err := w.WriteBookmark(ctx, BuildBookmark(req, openedAt))
	if err != nil {
		// The bookmark may or may not have landed. The caller has to be told
		// which, because "we do not know whether the operator will find a
		// bookmark" is a sentence an operator cannot act on.
		return BookmarkDecision{Refusal: "write_failed", OpenedAt: openedAt},
			fmt.Errorf("milestone: the bookmark was not confirmed: %w", err)
	}
	decision.Placed = true
	decision.Ref = ref
	return decision, nil
}
