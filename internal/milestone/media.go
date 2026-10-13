package milestone

import (
	"context"
	"errors"
	"io"
	"time"
)

// MediaRange is what a probe found: the vendor's recording exists over part of
// the window asked about, or none of it.
//
// The parts are reported rather than summarised because a gap is a fact. A
// camera that was down for six of the ten minutes asked about has a recording
// that covers four, and an investigation that believed ten is an investigation
// that asserts a negative from an absence.
type MediaRange struct {
	// CameraRef is the vendor's own camera reference, echoed from the ask.
	CameraRef string
	// Available is the union of the intervals the vendor says it holds. Empty
	// means the vendor holds nothing for the window, which is a different answer
	// from "the probe failed".
	Available []Interval
	// Source is where the answer came from, so a caller can tell a recording
	// index from an estimate. An estimate is a hint and a recording index is a
	// fact.
	Source string
}

// Interval is one continuous span of recording.
type Interval struct {
	From time.Time
	To   time.Time
}

// Contains reports whether an instant falls inside any available interval.
//
// The instants compared are the caller's, in UTC, because a probe answers about
// the vendor's clock and the caller's window is in the record's corrected time —
// the caller is responsible for the translation and this method for the
// arithmetic.
func (r MediaRange) Contains(at time.Time) bool {
	at = at.UTC()
	for _, iv := range r.Available {
		if !at.Before(iv.From.UTC()) && !at.After(iv.To.UTC()) {
			return true
		}
	}
	return false
}

// MediaClip is one fetch's outcome: the bytes, what they are, and where they
// came from.
//
// The bytes are carried rather than written by this package because the adapter
// is the single path to the vendor and the evidence store is the single path to
// storage — a fetch that wrote to a filesystem would be a second storage path
// with a second retention policy.
type MediaClip struct {
	// Kind is what was fetched. The two the evidence schema declares are
	// `clip` and `snapshot`; nothing else is written by this path.
	Kind string
	// ContentType is the vendor's own.
	ContentType string
	// Size is the byte count. Reported by the transport, recorded on the
	// receipt, and never inferred from what was written.
	Size int64
	// Reader is the payload. The caller closes it.
	Reader io.Reader
	// Range is the interval the bytes actually cover. It is not the interval
	// asked for, and the difference matters: a vendor may return what it holds
	// rather than what was requested, and a receipt that recorded the ask would
	// record a claim about coverage nobody made.
	Range Interval
}

// Media kinds, matching the evidence schema's allowed set.
const (
	MediaClipKind     = "clip"
	MediaSnapshotKind = "snapshot"
)

// MediaReader is the optional vendor media surface, discovered by type
// assertion for the same reason BookmarkWriter is: a transport that stands in
// for a vendor that was never reachable has no recording to probe, and forcing
// it to implement one is forcing it to lie.
//
// It exists because C1's evidence engine needs the bytes, not only the pointer.
// The bookmark says "look here"; the probe says "there is something to look
// at"; the fetch says what it was. Without the probe an investigation cannot
// tell a gap from a negative — the recording exists over part of the window, or
// it does not exist at all, and those are different answers to "what happened".
type MediaReader interface {
	// ProbeRange asks what the vendor holds on one camera over one window.
	//
	// A probe that fails is an error, and an empty answer is a fact. The
	// distinction is the caller's to make and this signature's to allow.
	ProbeRange(ctx context.Context, cameraRef string, from, to time.Time) (MediaRange, error)
	// FetchClip fetches the bytes for a window, as the narrower of what was
	// asked for and what the vendor holds.
	//
	// The narrower-of rule is why the probe exists: fetching a window the vendor
	// does not hold returns an error from every documented build of this API, and
	// a worker that fetched without probing would dead-letter on every gap
	// instead of recording the gap.
	FetchClip(ctx context.Context, cameraRef string, from, to time.Time) (MediaClip, error)
	// FetchSnapshot fetches one still at one instant. The instant is the record's
	// corrected time, translated by the caller.
	FetchSnapshot(ctx context.Context, cameraRef string, at time.Time) (MediaClip, error)
}

// ErrNoMedia is what a transport without a media surface answers through the
// helper below. It is an error so a caller cannot mistake it for a probe that
// found nothing.
var ErrNoMedia = errors.New("milestone: this transport has no media surface")

// MediaFor returns the transport's media surface, or ErrNoMedia.
//
// A helper rather than an inline assertion so the answer is one line at every
// call site and the same one, and so a second vendor's transport is discovered
// the same way the first one is.
func MediaFor(t Transport) (MediaReader, error) {
	if m, ok := t.(MediaReader); ok {
		return m, nil
	}
	return nil, ErrNoMedia
}
