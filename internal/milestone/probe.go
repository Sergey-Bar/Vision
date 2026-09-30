package milestone

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"
)

// The adapter probe.
//
// The plan's P0.5: "one `securityctl adapter probe` that reaches every route
// and reports exactly which assumptions hold, so the neutral claim is testable."
//
// # Why this is worth building at all
//
// Every route in this package is marked `Unverified`, and every comment in it
// says the same thing: nobody has ever reached a live Milestone server from
// this codebase. That honesty is only useful if something turns it into
// confirmed-or-contradicted, and a person clicking through twenty endpoints with
// curl is a person who will get to fourteen of them and stop.
//
// So: a probe that reaches every route, records what came back, and - the part
// that matters - **never changes a flag on its own**. A probe that auto-promoted
// a route to verified would make the `Unverified` field a claim the tool makes
// about itself, and a tool that marks its own homework is worth nothing. The
// probe produces a report; a human turns it into a commit.
//
// # What a confirmed route means
//
// It means the *shape* held: the status, and the field the route was declared to
// expect. It does not mean the semantics are understood. A route can return a
// well-formed array of the wrong things, and the distinction is recorded on the
// result so nobody reads a green probe as more than it is.

const (
	// VerdictConfirmed is a route that answered in the declared shape.
	VerdictConfirmed Verdict = "confirmed"
	// VerdictContradicted is a route that answered in a shape the adapter does
	// not accept. This is the valuable outcome: it turns a guess into a bug
	// report with a request id attached.
	VerdictContradicted Verdict = "contradicted"
	// VerdictUnreachable is a route that could not be reached at all - wrong
	// base URL, no network, wrong credentials. Distinct from contradicted,
	// because "we did not get there" and "we got there and it is different" send
	// an operator to different places.
	VerdictUnreachable Verdict = "unreachable"
	// VerdictSkipped is a route this build has no method for.
	VerdictSkipped Verdict = "skipped"
)

// Verdict is a closed vocabulary, because a probe report is read by a person
// deciding whether to trust a flag.
type Verdict string

// ProbeResult is one route's outcome.
type ProbeResult struct {
	Route   Route   `json:"route"`
	Verdict Verdict `json:"verdict"`
	// Status is the HTTP status, or 0 when the route was never reached.
	Status int `json:"status"`
	// Items is how many entries the route returned, when it returned a list.
	Items int `json:"items,omitempty"`
	// Keys are the field names present on the first item. A report that shows
	// them is what lets a human compare the vendor's fields with the adapter's
	// expectations in one glance, which is the whole job.
	Keys []string `json:"keys,omitempty"`
	// Duration is how long the call took, because a route that takes eleven
	// seconds is a design problem even when it works.
	DurationMS int64 `json:"durationMs"`
	// Detail is the refusal, or a note about what the probe could not check.
	Detail string `json:"detail,omitempty"`
	// NeedsHuman is set on a confirmed route whose *semantics* the probe cannot
	// judge. It is the difference between "this route works" and "this route
	// returns what it said it would, and somebody still has to say whether that
	// is the right data".
	NeedsHuman string `json:"needsHuman,omitempty"`
}

// ProbeReport is the whole run.
type ProbeReport struct {
	ProbedAt   time.Time     `json:"probedAt"`
	Endpoint   string        `json:"endpoint"`
	Results    []ProbeResult `json:"results"`
	Summary    ProbeSummary  `json:"summary"`
	ClockDrift Drift         `json:"clockDrift,omitempty"`
	// Capabilities is what this deployment said it could do.
	Capabilities []string `json:"capabilities"`
}

// ProbeSummary counts the verdicts.
type ProbeSummary struct {
	Confirmed    int `json:"confirmed"`
	Contradicted int `json:"contradicted"`
	Unreachable  int `json:"unreachable"`
	Skipped      int `json:"skipped"`
	Total        int `json:"total"`
	// StillUnverified is how many routes remain marked Unverified. The probe
	// never changes it, so this number only moves when a human edits routes.go.
	StillUnverified int `json:"stillUnverified"`
	// Flaggable is how many confirmed routes a human could promote. It is a
	// count and not a list because the list is the results, and a report that
	// repeated itself would be one more thing to read.
	Flaggable int `json:"flaggable"`
}

// AllConfirmed is the report's own question, and it is false whenever any route
// is contradicted. A probe that reported "11 of 12 routes work" and exited zero
// would be a CI signal that says a system is fine.
func (r ProbeReport) AllConfirmed() bool {
	return r.Summary.Contradicted == 0 && r.Summary.Unreachable == 0 && r.Summary.Confirmed > 0
}

// ProbeOptions configures a probe.
type ProbeOptions struct {
	// Endpoint is what the report says it reached. It is a parameter rather than
	// something read off the transport because the transport may not know, and a
	// report with an empty endpoint is a report nobody can tell apart from one
	// run against a different server.
	Endpoint string
	// Routes defaults to the package's own list, which is the assumption set.
	Routes []Route
	// ItemLimit bounds how much of a page the probe reads. A probe against a
	// 2,000-camera site must not fetch two thousand events to check a shape.
	ItemLimit int
	// IncludeBookmarks and IncludeClock are the write-side routes. They are off
	// by default because a probe that writes is not a probe, and a bookmark
	// placed by a probe is a bookmark an operator has to delete.
	IncludeBookmarks bool
	IncludeClock     bool
}

func (o ProbeOptions) withDefaults() ProbeOptions {
	out := o
	if len(out.Routes) == 0 {
		out.Routes = Routes
	}
	if out.ItemLimit <= 0 {
		out.ItemLimit = 1
	}
	return out
}

// ProbeResult renders one result for a human, in a fixed shape.
func (p ProbeResult) String() string {
	s := fmt.Sprintf("%-8s %-6s %-40s", p.Verdict, statusText(p.Status), describe(p.Route))
	switch {
	case p.Detail != "":
		s += " " + p.Detail
	case p.Items > 0:
		s += fmt.Sprintf(" %d item(s), fields: %v", p.Items, p.Keys)
	}
	if p.NeedsHuman != "" {
		s += " [needs a human: " + p.NeedsHuman + "]"
	}
	return s
}

func statusText(code int) string {
	if code == 0 {
		return "-"
	}
	return fmt.Sprintf("%d", code)
}

// summarizeKeys returns the top-level field names of the first item, sorted, so
// two reports diff cleanly.
func summarizeKeys(item map[string]any) []string {
	out := make([]string, 0, len(item))
	for k := range item {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// decodeEnvelope pulls a page's shape out of a body without interpreting it.
//
// It is the same discipline the adapter has: parse the shape, report the field
// names, and decide nothing about what they mean.
func decodeEnvelope(body []byte) (items int, keys []string, err error) {
	var asArray []map[string]any
	if e := json.Unmarshal(body, &asArray); e == nil {
		if len(asArray) == 0 {
			return 0, nil, nil
		}
		return len(asArray), summarizeKeys(asArray[0]), nil
	}
	var asObject map[string]any
	if e := json.Unmarshal(body, &asObject); e != nil {
		return 0, nil, fmt.Errorf("the body is neither an object nor an array: %w", e)
	}
	// A paged object carries its entries under a name; a single object is one
	// entry. Both are reported as what they are rather than guessed between.
	for _, name := range []string{"Items", "items", "Data", "data", "Results", "results", "Entries", "entries"} {
		if nested, ok := asObject[name].([]any); ok {
			entries := make([]map[string]any, 0, len(nested))
			for _, e := range nested {
				if m, ok := e.(map[string]any); ok {
					entries = append(entries, m)
				}
			}
			if len(entries) == 0 {
				return 0, nil, nil
			}
			return len(entries), summarizeKeys(entries[0]), nil
		}
	}
	return 1, summarizeKeys(asObject), nil
}

// contextWithBudget bounds a probe call.
//
// A probe is an interactive instrument pointed at a device that may be
// misconfigured, and an unbounded read against a device that answers
// everything is a probe that hangs the thing it is diagnosing.
func contextWithBudget(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, d)
}

// Probe reaches every route and reports what came back.
//
// It never changes a route's Unverified flag. That is the design's whole point:
// the flag is a claim a human makes by reading this report and committing the
// change, and a probe that promoted its own findings would make the flag a
// statement the tool makes about itself.
func Probe(ctx context.Context, t Transport, opts ProbeOptions) ProbeReport {
	o := opts.withDefaults()
	report := ProbeReport{
		ProbedAt: time.Now().UTC(),
		Endpoint: o.Endpoint,
		Results:  make([]ProbeResult, 0, len(o.Routes)+2),
	}

	if c, ok := t.(CapabilityReporter); ok {
		for _, name := range AllCapabilities() {
			if c.Capabilities().Has[name] {
				report.Capabilities = append(report.Capabilities, string(name))
			}
		}
	}

	for _, route := range o.Routes {
		report.Results = append(report.Results, probeRoute(ctx, t, route, o.ItemLimit))
	}

	// The write-side routes are separate because reaching them means writing to
	// the customer's recording, and a probe that placed a bookmark on every run
	// would be an instrument nobody would run twice.
	if o.IncludeClock {
		if p, ok := t.(ClockProbe); ok {
			// Only if the read pass did not already reach it. A route listed in
			// both places probed twice is a report with two rows for one answer,
			// and a reader counts rows.
			alreadyProbed := false
			for _, r := range report.Results {
				if r.Route.Path == systemTimeRoute.Path {
					alreadyProbed = true
					break
				}
			}
			if !alreadyProbed {
				report.Results = append(report.Results, probeClock(ctx, p))
			}
		}
	}

	report.Summary = summarise(report.Results, o.Routes)
	return report
}

func probeRoute(ctx context.Context, t Transport, route Route, limit int) ProbeResult {
	res := ProbeResult{Route: route}
	if route.WriteBack {
		// Writing is not probing. A write-side route reached by a read probe
		// either fails on the method or, worse, succeeds and changes the
		// customer's archive.
		res.Verdict = VerdictSkipped
		res.Detail = "a write-side route; run with the write flag to reach it deliberately"
		return res
	}
	started := time.Now()
	cctx, cancel := contextWithBudget(ctx, 20*time.Second)
	defer cancel()

	resp, err := t.Get(cctx, route, Query{Limit: limit})
	res.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		res.Verdict, res.Detail, res.Status = classify(err)
		return res
	}
	res.Status = resp.Status
	if resp.Status < 200 || resp.Status > 299 {
		res.Verdict = VerdictContradicted
		res.Detail = summarizeBody(resp.Body)
		return res
	}
	items, keys, derr := decodeEnvelope(resp.Body)
	if derr != nil {
		res.Verdict = VerdictContradicted
		res.Detail = derr.Error()
		return res
	}
	if route.Expect == "array" && items == 0 {
		res.Verdict = VerdictContradicted
		res.Detail = "the route was declared a list and returned nothing recognisable as one"
		return res
	}
	res.Items = items
	res.Keys = keys
	res.Verdict = VerdictConfirmed
	if route.Unverified {
		// The probe confirmed the *shape*. It cannot know whether these are the
		// right events, and a report that let a reader conflate the two is the
		// report that gets a wrong assumption committed.
		res.NeedsHuman = "the shape matched; whether these are the right records is not something a probe can decide"
	}
	return res
}

func probeClock(ctx context.Context, p ClockProbe) ProbeResult {
	// The route is the clock one; it is not in the read list because it is a
	// separate surface.
	route := systemTimeRoute
	res := ProbeResult{Route: route}
	started := time.Now()
	cctx, cancel := contextWithBudget(ctx, 10*time.Second)
	defer cancel()

	reading, err := p.ServerTime(cctx)
	res.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		res.Verdict = VerdictContradicted
		res.Detail = err.Error()
		return res
	}
	if reading.At.IsZero() {
		res.Verdict = VerdictContradicted
		res.Detail = "the answer parsed but carried no time; a zero clock is worse than none, because it " +
			"looks like a measurement of 1970"
		return res
	}
	res.Verdict = VerdictConfirmed
	res.Keys = []string{reading.At.Format(time.RFC3339), "roundTrip=" + reading.RoundTrip.String()}
	res.NeedsHuman = "a clock reading is a number; whether it is the right clock for this site is a fact only " +
		"the site's own NTP comparison can supply"
	return res
}

// classify turns a transport error into a verdict.
//
// The three cases send an operator to three different places, and a probe that
// reports all of them as "unreachable" is a probe that turns a wrong base URL
// into a vendor bug report.
func classify(err error) (Verdict, string, int) {
	var se *StatusError
	if errors.As(err, &se) {
		// We got there, and it answered with something it should not have. The
		// assumption about this route is what is wrong.
		return VerdictContradicted, err.Error(), se.Status
	}
	var nf *NotFoundError
	if errors.As(err, &nf) {
		// The vendor does not have this route. That is the assumption failing,
		// not the network, and it is the single most useful thing this probe can
		// tell somebody standing in front of a real server.
		return VerdictContradicted, err.Error(), http.StatusNotFound
	}
	var un *UnauthenticatedError
	if errors.As(err, &un) {
		// We could not authenticate. Every route would report the same thing, and
		// twenty identical rows is a report nobody reads.
		return VerdictUnreachable, err.Error(), un.Status
	}
	var me *MalformedError
	if errors.As(err, &me) {
		// The endpoint answered and the body was not what the adapter expects.
		// Contradicted, because the shape is the assumption.
		return VerdictContradicted, err.Error(), 0
	}
	return VerdictUnreachable, err.Error(), 0
}

func summarise(results []ProbeResult, routes []Route) ProbeSummary {
	var s ProbeSummary
	s.Total = len(results)
	for _, r := range results {
		switch r.Verdict {
		case VerdictConfirmed:
			s.Confirmed++
		case VerdictContradicted:
			s.Contradicted++
		case VerdictUnreachable:
			s.Unreachable++
		case VerdictSkipped:
			s.Skipped++
		}
	}
	for _, r := range routes {
		if r.Unverified {
			s.StillUnverified++
		}
	}
	for _, r := range results {
		if r.Verdict == VerdictConfirmed && r.Route.Unverified {
			s.Flaggable++
		}
	}
	return s
}

func summarizeBody(body []byte) string {
	const limit = 160
	s := string(body)
	if len(s) > limit {
		s = s[:limit] + "…"
	}
	return s
}
