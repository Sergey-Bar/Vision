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
			// A fixture with no explicit timing still replays, in file order,
			// so an authoring mistake produces a wrong time rather than a
			// silently dropped event.
			_ = epoch
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

// ExposedScenarioNames lists the scenarios this transport will serve, for a
// replay report that says which fixtures produced a given outcome.
func (t *ReplayTransport) ExposedScenarioNames() []string {
	out := make([]string, 0, len(t.scenarios))
	for _, s := range t.scenarios {
		out = append(out, s.Name)
	}
	return out
}
