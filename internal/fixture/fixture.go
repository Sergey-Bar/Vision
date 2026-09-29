// Package fixture is the on-disk format for captured and replayed source events.
//
// A fixture is the boundary that decouples the pipeline from a Milestone server.
// The same scenario must produce the same canonical event whether it arrived over
// HTTP from a real VMS or was read from a file in CI, and that equality is a
// Definition of Done item. So the fixture has to be the adapter's *input*, not a
// parallel path that re-implements the adapter.
//
// Three properties make that possible:
//
//   - The envelope is stored verbatim, in the same bytes the source produced. The
//     adapter parses the envelope from the fixture exactly as it parses one from
//     the network. A fixture format with a pre-parsed, "clean" payload would be a
//     second parser, and the two would drift.
//   - The envelope is sanitized at capture time, not at read time. Anything that
//     must never be committed - credentials, MAC addresses, person images,
//     absolute paths - is removed once, by a code path with its own tests, and
//     the commit is safe by construction rather than by reviewer vigilance.
//   - Timing is explicit and relative. A fixture stores offsets from a scenario
//     epoch, never absolute wall-clock timestamps, so replaying a year from now
//     produces the same events. An absolute timestamp in a fixture is a fixture
//     that quietly stops being deterministic.
//
// The format is versioned and signed by content hash, not by a signature: a
// fixture's hash is recorded in the scenario manifest, and a changed envelope is
// a changed scenario.
package fixture

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// FormatVersion is the fixture format version. A reader refuses a version it
// does not know rather than guessing: silently misreading a fixture produces a
// canonical event that is subtly wrong, which is the failure mode this whole
// design exists to prevent.
const FormatVersion = 1

// Scenario is one named, ordered sequence of source envelopes.
type Scenario struct {
	// FormatVersion is the fixture format version, not the product version.
	FormatVersion int    `json:"formatVersion"`
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	// Source names the system the envelopes came from. "synthetic" is a legal
	// and expected value: a fixture authored rather than captured says so, and
	// the golden tests record which scenarios are real captures.
	Source string `json:"source"`
	// CapturedAt is when the capture happened. It is metadata about the fixture,
	// never an input to event timing.
	CapturedAt time.Time `json:"capturedAt"`
	// Epoch is the wall-clock instant offset zero refers to. Replay substitutes
	// it, so the same fixture can be replayed on any date and still produce the
	// same normalized timestamps relative to one another.
	Epoch time.Time `json:"epoch"`
	// Site and Tenant identify the site the scenario belongs to. They are
	// fixture metadata, deliberately not part of the envelope: the tenant comes
	// from the session at runtime and is never read from a request or a file.
	SiteID string `json:"siteId"`

	Events []Event `json:"events"`
}

// Event is one captured source envelope plus its timing.
type Event struct {
	// OffsetMS is milliseconds from the scenario epoch. A negative offset is
	// legal and is how a delayed or out-of-order arrival is expressed: the
	// envelope claims an observed time later than the time the platform saw it.
	OffsetMS int64 `json:"offsetMs"`
	// Kind is the API path the envelope came from, e.g. "events/1", "alarms/2".
	// The adapter dispatches on it, exactly as it dispatches on an HTTP route,
	// so one code path serves both transports.
	Kind string `json:"kind"`
	// Method is GET or POST. Recorded because a few Milestone endpoints change
	// meaning by method, and a fixture that lost that would test something other
	// than the adapter.
	Method string `json:"method"`
	// Envelope is the raw body, stored as JSON so a fixture stays reviewable in
	// a diff. It is what the adapter parses; nothing upstream of the adapter
	// interprets it.
	Envelope json.RawMessage `json:"envelope"`
	// Headers the source returned that the adapter actually reads. Only
	// allow-listed headers are kept, so an unexpected one cannot smuggle a
	// credential into a committed fixture.
	Headers map[string]string `json:"headers,omitempty"`
}

// ObservedAt is the wall-clock time this event was received during replay. It is
// derived, not stored: storing it would be the first step toward a fixture that
// stops reproducing.
func (e Event) ObservedAt(epoch time.Time) time.Time {
	return epoch.Add(time.Duration(e.OffsetMS) * time.Millisecond)
}

// ContentHash is the SHA-256 over the canonical serialization of the scenario,
// excluding the field that holds it. Two scenarios with the same hash are the
// same scenario, byte for byte, which is what the golden tests assert.
func (s Scenario) ContentHash() (string, error) {
	clone := s
	clone.Events = append([]Event(nil), s.Events...)
	sort.SliceStable(clone.Events, func(i, j int) bool { return clone.Events[i].OffsetMS < clone.Events[j].OffsetMS })
	body, err := json.Marshal(clone)
	if err != nil {
		return "", fmt.Errorf("fixture: hash scenario %q: %w", s.Name, err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

// Validate rejects a fixture that would replay into something other than what it
// claims to be. Each check below corresponds to a way a fixture can be subtly
// wrong in a way that still looks valid.
func (s Scenario) Validate() error {
	switch {
	case s.FormatVersion != FormatVersion:
		return fmt.Errorf("fixture: scenario %q has format version %d, this build reads %d",
			s.Name, s.FormatVersion, FormatVersion)
	case strings.TrimSpace(s.Name) == "":
		return errors.New("fixture: scenario name is required")
	case s.Epoch.IsZero():
		return fmt.Errorf("fixture: scenario %q has no epoch; offset zero would be ambiguous", s.Name)
	case s.SiteID == "":
		return fmt.Errorf("fixture: scenario %q has no site id", s.Name)
	case len(s.Events) == 0:
		return fmt.Errorf("fixture: scenario %q contains no events", s.Name)
	}

	for i, e := range s.Events {
		switch {
		case e.Method == "":
			return fmt.Errorf("fixture: scenario %q event %d has no method", s.Name, i)
		case !validMethod(e.Method):
			return fmt.Errorf("fixture: scenario %q event %d has method %q, want GET or POST",
				s.Name, i, e.Method)
		case e.Kind == "":
			return fmt.Errorf("fixture: scenario %q event %d has no kind", s.Name, i)
		case len(e.Envelope) == 0:
			return fmt.Errorf("fixture: scenario %q event %d has an empty envelope", s.Name, i)
		case !json.Valid(e.Envelope):
			// A malformed envelope is a legitimate scenario — "malformed" is one
			// of the required fixture cases - but it has to be valid JSON that
			// decodes to something wrong, not a file that will not parse at all.
			// Otherwise a typo and a deliberate malformation are indistinguishable
			// and the scenario silently stops testing anything.
			return fmt.Errorf("fixture: scenario %q event %d envelope is not valid JSON", s.Name, i)
		}
		for k := range e.Headers {
			if !allowedHeader(k) {
				return fmt.Errorf("fixture: scenario %q event %d carries header %q, which is not on the allow-list",
					s.Name, i, k)
			}
		}
	}
	return nil
}

func validMethod(m string) bool {
	return m == "GET" || m == "POST"
}

// Manifest is the index of a fixture set, and the thing a test asserts against.
type Manifest struct {
	FormatVersion int             `json:"formatVersion"`
	GeneratedAt   time.Time       `json:"generatedAt"`
	Source        string          `json:"source"`
	Scenarios     []ManifestEntry `json:"scenarios"`
	Notes         string          `json:"notes,omitempty"`
}

// ManifestEntry indexes one scenario by content hash.
type ManifestEntry struct {
	Name   string `json:"name"`
	File   string `json:"file"`
	Hash   string `json:"hash"`
	Events int    `json:"events"`
	// Covers names the behaviours this scenario is here to prove, e.g.
	// "duplicate", "out-of-order". A scenario whose purpose is unstated is a
	// scenario nobody will notice has stopped testing anything.
	Covers []string `json:"covers,omitempty"`
}
