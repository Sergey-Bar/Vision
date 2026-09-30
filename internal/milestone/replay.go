package milestone

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/fixture"
)

// ReplayTransport serves envelopes from a fixture directory instead of a socket.
//
// It is not a stub. It is the same code path with a different byte source, which
// is what makes the two modes comparable: there is no second parser, no second
// mapping table, and no way for a fixture to exercise something a live server
// would not also exercise.
type ReplayTransport struct {
	scenarios []fixture.Scenario
	// epoch overrides the scenario's own epoch. The replay harness sets it so a
	// replay in 2027 produces the same relative timing as a capture in 2026 -
	// an absolute timestamp in a fixture is a fixture that quietly stops being
	// deterministic.
	epoch         *epochOverride
	cursorByRoute map[string]int
	byName        map[string]fixture.Scenario
}

type epochOverride struct {
	SiteID string
	At     epochTime
}

type epochTime = fixtureTime

// fixtureTime aliases the time type the fixture package uses, so this file does
// not need to import time just to name the field type.
type fixtureTime = time.Time

// NewReplayTransport builds a transport over a fixture set.
func NewReplayTransport(scenarios []fixture.Scenario) *ReplayTransport {
	byName := make(map[string]fixture.Scenario, len(scenarios))
	ordered := make([]fixture.Scenario, 0, len(scenarios))
	for _, s := range scenarios {
		byName[s.Name] = s
		ordered = append(ordered, s)
	}
	return &ReplayTransport{
		scenarios:     ordered,
		cursorByRoute: map[string]int{},
		byName:        byName,
	}
}

// WithEpoch makes every scenario replay as if it had been captured at a
// different wall-clock instant. The relative timing inside a scenario - which
// is what correlation depends on - is untouched.
func (t *ReplayTransport) WithEpoch(at time.Time) *ReplayTransport {
	t.epoch = &epochOverride{At: at}
	return t
}

// WithSiteID rewrites the site the fixtures belong to, so one captured scenario
// can be replayed against a site that was set up by a test rather than captured.
func (t *ReplayTransport) WithSiteID(siteID string) *ReplayTransport {
	if t.epoch == nil {
		t.epoch = &epochOverride{}
	}
	t.epoch.SiteID = siteID
	return t
}

// Close satisfies Transport. A fixture transport holds no connection, so there
// is nothing to release.
func (*ReplayTransport) Close() error { return nil }

// Get returns the next page of the named route.
//
// It walks the scenario's events in offset order and serves each one once, then
// reports the route as exhausted by returning an empty page rather than an
// error. An exhausted feed is a normal state at the end of a replay; an error
// would make every scenario look like a source outage at the last event.
func (t *ReplayTransport) Get(_ context.Context, route Route, query Query) (*Response, error) {
	key := route.Path
	start := t.cursorByRoute[key]
	t.cursorByRoute[key] = start + 1

	var matched []fixture.Event
	for _, sc := range t.scenarios {
		if t.byName[sc.Name].Name == "" {
			continue
		}
		epoch := sc.Epoch
		if t.epoch != nil {
			epoch = t.epoch.At
		}
		for _, ev := range sc.Events {
			if ev.Kind != key && ev.Method+" "+ev.Kind != key {
				continue
			}
			// A fixture's timing is its Epoch plus the event's OffsetMS, and the
			// envelope is rebased onto it before the adapter sees it. The
			// envelope is where the timestamp actually lives, so a rebase that did
			// not happen here left every fixture carrying the wall-clock instant it
			// happened to be captured at - which means every time-window test in
			// the tree was measuring the capture date, not the window, and passing
			// for the wrong reason. `epoch` used to be computed and then thrown
			// away on this line, and nothing failed.
			rebased, err := rebaseEnvelope(ev.Envelope, epoch.Add(time.Duration(ev.OffsetMS)*time.Millisecond))
			if err != nil {
				// A malformed envelope is the fixture's problem, and a fixture that will
				// not re-encode is one whose bytes the adapter cannot read.
				// Returning it is the refusal; skipping it would deliver a
				// scenario with a hole in it, which is the one failure a replay
				// must not have.
				return nil, fmt.Errorf("milestone: replay %s %s: %w", sc.Name, ev.Kind, err)
			}
			ev.Envelope = rebased
			matched = append(matched, ev)
		}
	}
	if start >= len(matched) {
		return &Response{Status: 200, Body: json.RawMessage(`[]`), Route: route}, nil
	}

	page := matched[start:]
	if query.Limit > 0 && len(page) > query.Limit {
		page = page[:query.Limit]
		t.cursorByRoute[key] = start + len(page)
	}

	items := make([]json.RawMessage, 0, len(page))
	for _, ev := range page {
		items = append(items, ev.Envelope)
	}
	body, err := json.Marshal(items)
	if err != nil {
		return nil, fmt.Errorf("milestone: replay %s: %w", key, err)
	}
	return &Response{Status: 200, Body: body, Route: route}, nil
}

// rebaseEnvelope moves an envelope's timestamp onto the instant the replay is
// running at, preserving the shape the source wrote it in.
//
// Preserving the shape is the point. If the captured value said "+02:00", the
// rebased value says "+02:00" too, because SourceTimezone is derived from the
// string the source sent and re-rendering it in UTC would turn a declared zone
// into an absent one. A naive value stays naive, and keeps being read as UTC with
// an empty zone - which is what it was when it was captured.
//
// A value that does not parse is left exactly as it is. The normalizer already
// has a documented path for that: it records a zero time, falls back to
// received_at, and marks the record as lacking a trustworthy observation. This
// code does not get a second, quieter opinion about what the timestamp meant.
func rebaseEnvelope(envelope json.RawMessage, at time.Time) (json.RawMessage, error) {
	if len(envelope) == 0 {
		return envelope, nil
	}
	var item map[string]any
	if err := json.Unmarshal(envelope, &item); err != nil {
		// Not a JSON object. The adapter will reject it on its own terms, and
		// rewriting bytes this code does not understand would only lose the
		// original envelope that a failing test needs to see.
		//
		//nolint:nilerr // deliberately not an error: see above
		return envelope, nil
	}
	changed := false
	for _, key := range milestoneTimestampKeys {
		raw, ok := item[key].(string)
		if !ok || raw == "" {
			continue
		}
		for _, layout := range milestoneTimeLayouts {
			parsed, err := time.Parse(layout, raw)
			if err != nil {
				continue
			}
			// The source's own numeric offset, rebuilt as a fixed zone.
			//
			// It is tempting to write `at.In(parsed.Location())`, and that is a
			// trap: for a timestamp like "…+02:00", Go's parser hands back a
			// Location that shares the *host's* zone rules under the name "IST",
			// and `In` then re-resolves it for the new instant. On a host at
			// UTC+3 in July that turns a source that declared +02:00 into a
			// string declaring +03:00 - a different fact, silently, and a different
			// one on a different machine. FixedZone with the parsed offset cannot
			// consult any database, so the rendered offset is the source's and
			// only the source's.
			_, offset := parsed.Zone()
			item[key] = at.In(time.FixedZone("", offset)).Format(layout)
			changed = true
			break
		}
	}
	if !changed {
		return envelope, nil
	}
	out, err := json.Marshal(item)
	if err != nil {
		return nil, fmt.Errorf("re-encode the envelope: %w", err)
	}
	return out, nil
}

// ExposedScenarioNames lists the scenarios this transport will serve, for a
// replay report that says which fixtures produced a given outcome.
func (t *ReplayTransport) ExposedScenarioNames() []string {
	out := make([]string, 0, len(t.scenarios))
	for _, s := range t.scenarios {
		out = append(out, s.Name)
	}
	return out
}
