// Package correlator assembles the signals a rule evaluation may read, from
// stored facts.
//
// It exists as its own package rather than as a method on rules because
// internal/rules must not import dbx, and the engine has to stay pure for replay
// to be reproducible. A DB-reading correlator outside rules is what keeps that
// property structurally true rather than true by discipline: there is no way for
// evaluation logic to acquire a database handle, because the only thing that
// crosses the boundary is a []rules.Signal.
//
// # What it reads, and only that
//
// Stored canonical events, device clocks, device and zone metadata, and stored AI
// observations. It reads nothing from a source system, calls nothing over the
// network, and never calls the model. A decision produced from its output is a
// decision replayed from stored facts, which is invariant 2.
//
// # Time
//
// Every time predicate reads normalized_at and nothing else. observed_at is what
// the source claimed, which is the thing under suspicion; received_at is our
// ingest and says nothing about when anything happened at the site.
package correlator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/vision/internal/clock"
	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/rules"
)

// ErrNoTrigger is returned when the trigger event does not exist for this
// tenant.
//
// It is a distinct error rather than an empty result, because "the event I was
// asked to correlate is not here" and "the event is here and nothing corroborates
// it" are different states and the second one is a finding while the first is a
// bug.
var ErrNoTrigger = errors.New("correlator: no such trigger event")

// Bounds on the candidate scan.
//
// These are constants, not configuration and not bundle-supplied values. A rule
// bundle is untrusted input: an operator or an integration can install one, and
// a bundle that asked for a thirty-day window would turn every event into a
// table scan of every other event. The window is therefore bounded by something
// the untrusted input cannot raise.
//
// Widening a window is never the right fix for corroboration that under-counts.
// A narrower site, or a better clock, is. These numbers are the boundary that
// makes "this bundle wants more than the core will read" a visible
// configuration problem rather than a slow query nobody attributes.
const (
	// MaxWindow is the largest candidate window, either side of the trigger.
	MaxWindow = 5 * time.Minute
	// MaxCandidates bounds the rows one evaluation may read. A site at the
	// capacity target - 2,000 cameras, 20 events per second - produces far more
	// than this inside a five-minute window, so this bound will bind, and the
	// engine's own window check still applies on top of it.
	MaxCandidates = 2000
)

// TierPolicy assigns an evidence tier to each assembled signal.
//
// The tiers are the ones the plan defines: A is authoritative state reported by
// the source of truth, B is a deterministic fact the core derived, C is an AI
// observation. A canonical event is a derived fact - it is a normalization of a
// raw envelope, not the device's own report — so candidates are Tier B. The
// trigger is Tier A, because it is the fact the rule is being evaluated against
// rather than corroboration for it.
//
// A Tier C signal is not produced here at all. The AI observation travels
// alongside its event in Signal.AIObservation, and the engine reads it there.
// Assigning the observation to the signal's tier as well would make one piece of
// evidence count twice.
type TierPolicy struct {
	// TriggerTier is the tier given to the event under evaluation.
	TriggerTier rules.Tier
	// CandidateTier is the tier given to the events available to corroborate it.
	CandidateTier rules.Tier
}

// DefaultTiers is the standard assignment.
func DefaultTiers() TierPolicy {
	return TierPolicy{TriggerTier: rules.TierA, CandidateTier: rules.TierB}
}

// Correlator reads stored facts and produces signals.
type Correlator struct {
	Pool *dbx.Pool
	// Tiers assigns evidence tiers. Zero value means DefaultTiers.
	Tiers TierPolicy
}

// WindowReport describes the window that was actually used, and whether the
// bundle asked for a wider one than the core will read.
//
// This exists because the alternative is silent. A bundle whose timeWindow is
// twenty minutes, clamped to five, under-counts corroboration - which means
// incidents that should have been corroborated are raised as UNVERIFIED, which
// means an operator sees a camera that never corroborates anything and has no
// way to learn why. Reporting the clamp makes it a configuration finding.
type WindowReport struct {
	// Requested is the bundle's widest timeWindow, in seconds.
	Requested time.Duration
	// Used is the window the query actually ran with.
	Used time.Duration
	// Clamped is true when Requested exceeded Used.
	Clamped bool
	// Candidates is how many rows the query returned before the cap.
	Candidates int
	// Truncated is true when the candidate cap bound the query.
	Truncated bool
}

// Result is one correlation's output.
type Result struct {
	// Trigger is the event under evaluation.
	Trigger rules.Signal
	// Available are the candidate signals, ordered by (normalized_at, event_id).
	Available []rules.Signal
	// Window describes what was searched.
	Window WindowReport
	// Gap is the clock gap assessment for the correlation window.
	Gap clock.Gap
}

// SignalsFor assembles the trigger and its candidate window.
//
// The trigger is returned separately from the candidates so a caller cannot
// accidentally pass a slice containing it to the engine, which would then skip
// it by id - silently costing the rule one of the corroborating signals it asked
// for.
func (c *Correlator) SignalsFor(
	ctx context.Context, tenant, site, triggerEventID string, b *rules.CompiledBundle,
) (Result, error) {
	var out Result
	if tenant == "" || site == "" || triggerEventID == "" {
		return out, fmt.Errorf("correlator: tenant, site and trigger event id are all required")
	}

	tiers := c.Tiers
	if tiers.TriggerTier == "" {
		tiers = DefaultTiers()
	}

	trigger, err := c.readTrigger(ctx, tenant, site, triggerEventID)
	if err != nil {
		return out, err
	}
	trigger.Tier = tiers.TriggerTier
	out.Trigger = trigger

	window, report := windowFor(b)
	out.Window = report

	available, gap, err := c.readCandidates(ctx, tenant, site, triggerEventID,
		trigger.NormalizedAt, window, tiers.CandidateTier, &out.Window)
	if err != nil {
		return out, err
	}
	out.Available = available
	out.Gap = gap
	return out, nil
}

// windowFor resolves the scan window from the bundle's widest timeWindow, bounded
// by MaxWindow.
//
// The bundle's own value is used when it is narrower than the constant, because
// a site that correlates on a two-second window should not read five minutes of
// events and throw most of them away.
func windowFor(b *rules.CompiledBundle) (time.Duration, WindowReport) {
	report := WindowReport{Used: MaxWindow}
	if b == nil {
		return MaxWindow, report
	}
	widest := widestWindow(b)
	if widest <= 0 {
		return MaxWindow, report
	}
	requested := time.Duration(widest) * time.Second
	report.Requested = requested
	if requested > MaxWindow {
		report.Used = MaxWindow
		report.Clamped = true
		return report.Used, report
	}
	report.Used = requested
	return report.Used, report
}

// widestWindow walks every rule's condition tree, including the nested
// all/any/not branches, and returns the largest timeWindow in seconds.
//
// The walk is recursive rather than a flat scan of the top level because a
// timeWindow nested under an `any` is just as much a window as one at the top.
// Reading only the top level would silently under-size the scan for any bundle
// that nests its conditions, which is most of them.
func widestWindow(b *rules.CompiledBundle) int {
	widest := 0
	for _, rule := range b.Compiled {
		if d := conditionWindow(rule.When); d > widest {
			widest = d
		}
	}
	return widest
}

func conditionWindow(c rules.Condition) int {
	widest := 0
	if c.TimeWindow != nil && c.TimeWindow.DurationSeconds > widest {
		widest = c.TimeWindow.DurationSeconds
	}
	for _, nested := range c.All {
		if d := conditionWindow(nested); d > widest {
			widest = d
		}
	}
	for _, nested := range c.Any {
		if d := conditionWindow(nested); d > widest {
			widest = d
		}
	}
	if c.Not != nil {
		if d := conditionWindow(*c.Not); d > widest {
			widest = d
		}
	}
	return widest
}

// triggerColumns is the select list for an event row, shared by the trigger read
// and the candidate read so the two cannot drift apart.
//
// `e.observed_at` is not in this list, and its absence is the point. It used to
// be here, scanned into a `rules.Signal` field that nothing in the rule engine
// read: a window predicate's input is the drift-corrected instant, and carrying
// the source's own claim alongside it is an invitation to the next person to
// write a condition against the camera's belief about itself. `internal/clock`
// explains why, and `internal/predicatecov` fails the build if the column comes
// back.
//
// devices has no tags column: `attributes` is a JSONB bag and the tag list lives
// under its `tags` key. Selecting the whole document and decoding it in Go is
// deliberate - a site that puts something else in attributes is not a query
// error, and a rule that wants a tag nobody has set simply matches nothing.
const triggerColumns = `
	e.event_id, e.source_event_id, e.event_type, e.normalized_at,
	COALESCE(e.device_id, ''), COALESCE(e.zone_id, ''), e.clock_drift_ms, e.payload,
	COALESCE(d.kind, ''), COALESCE(d.attributes, '{}'::jsonb), COALESCE(z.path, '')`

const triggerJoins = `
	LEFT JOIN devices  d ON d.tenant_id = e.tenant_id AND d.device_id = e.device_id
	LEFT JOIN zones    z ON z.tenant_id = e.tenant_id AND z.zone_id   = e.zone_id`

// observationColumns joins the most recent observation per event. The `NOT
// EXISTS` guard picks the newest by created_at and skips any that has been
// superseded, so a re-evaluated observation replaces its predecessor instead of
// appearing beside it.
const observationJoins = `
	LEFT JOIN LATERAL (
	    SELECT o.observation_id, o.profile_id, o.structured_output,
	           o.insufficient_evidence, o.degraded_reason, o.degraded_detail
	      FROM ai_observations o
	     WHERE o.tenant_id = e.tenant_id AND o.event_id = e.event_id
	       AND NOT EXISTS (SELECT 1 FROM ai_observations s
	                        WHERE s.tenant_id = o.tenant_id
	                          AND s.supersedes_observation_id = o.observation_id)
	     ORDER BY o.created_at DESC, o.observation_id DESC
	     LIMIT 1
	) o ON TRUE`

// clockJoin brings in the current drift measurement for the event's device. It
// is the *current* measurement, not the one that was current when the event
// arrived, and that is a real limitation: a device corrected since the event was
// normalized is judged against today's clock. It is the same number the console
// shows, so at least the operator sees the same drift the decision used.
const clockJoin = `
	LEFT JOIN device_clocks dc ON dc.tenant_id = e.tenant_id AND dc.device_id = e.device_id`

// scanColumns is the full scan list for both reads.
const scanColumns = triggerColumns + `,
	COALESCE(dc.band, ''), COALESCE(dc.drift_ms, 0),
	COALESCE(o.observation_id, ''), COALESCE(o.profile_id, ''), COALESCE(o.structured_output, '{}'::jsonb),
	COALESCE(o.insufficient_evidence, FALSE),
	COALESCE(o.degraded_reason, ''), COALESCE(o.degraded_detail, '')`

func (c *Correlator) readTrigger(ctx context.Context, tenant, site, eventID string) (rules.Signal, error) {
	// The read is scoped to the site, not only to the tenant.
	//
	// Every table this query touches is site-scoped, and the RLS policy meant to
	// enforce that is PERMISSIVE, so it is ORed away by `tenant_isolation` and the
	// site constraint does not exist at the database layer. Binding it here means
	// the statement is correct on its own terms and needs no change when
	// `site_isolation` becomes RESTRICTIVE.
	//
	// The query's own `site_id` predicate is kept as well. The policy is meant to
	// be a second line, not the only one, and a statement that relies on a GUC
	// three layers down for its scope is a statement whose scope is invisible to
	// whoever reads it.
	scope := c.Pool.WithSites(tenant, "correlator", []string{site})
	var s rules.Signal
	var payload, attributes []byte
	var band, obsID, profile string
	var degraded string
	var structured []byte
	var insufficient bool
	var detail string
	var drift int32

	err := scope.QueryRow(ctx, `
		SELECT `+triggerColumns+`,
		       COALESCE(dc.band, ''), COALESCE(dc.drift_ms, 0),
		       COALESCE(o.observation_id, ''), COALESCE(o.profile_id, ''),
		       COALESCE(o.structured_output, '{}'::jsonb),
		       COALESCE(o.insufficient_evidence, FALSE),
		       COALESCE(o.degraded_reason, ''), COALESCE(o.degraded_detail, '')
		  FROM canonical_events e`+triggerJoins+clockJoin+observationJoins+`
		 WHERE e.tenant_id = $1 AND e.event_id = $2`,
		tenant, eventID).Scan(
		&s.EventID, &s.SourceEventID, &s.EventType, &s.NormalizedAt,
		&s.DeviceID, &s.ZoneID, &s.ClockDriftMS, &payload,
		&s.DeviceKind, &attributes, &s.ZonePath,
		&band, &drift, &obsID, &profile, &structured,
		&insufficient, &degraded, &detail,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return s, fmt.Errorf("%w: %s", ErrNoTrigger, eventID)
		}
		return s, fmt.Errorf("correlator: read trigger: %w", err)
	}

	_ = json.Unmarshal(payload, &s.Payload)
	if s.Payload == nil {
		s.Payload = map[string]any{}
	}
	s.DeviceTags = decodeTags(attributes)
	// A missing measurement is no correction, not a confident correction of zero.
	if drift > 0 || band != "" {
		s.ClockDriftMS = drift
	}
	s.DriftedExcluded = band == string(clock.BandSevere)
	s.AIObservation = buildObservation(obsID, profile, structured, insufficient, degraded, detail)
	return s, nil
}

func (c *Correlator) readCandidates(
	ctx context.Context, tenant, site, triggerEventID string,
	at time.Time, window time.Duration, tier rules.Tier, report *WindowReport,
) ([]rules.Signal, clock.Gap, error) {
	scope := c.Pool.WithSites(tenant, "correlator", []string{site})
	rows, err := scope.Query(ctx, `
		SELECT `+scanColumns+`
		  FROM canonical_events e`+triggerJoins+clockJoin+observationJoins+`
		 WHERE e.tenant_id = $1
		   AND e.site_id = $2
		   AND e.event_id <> $3
		   AND e.normalized_at >= $4
		   AND e.normalized_at <= $5
		 ORDER BY e.normalized_at ASC, e.event_id ASC
		 LIMIT $6`,
		tenant, site, triggerEventID,
		at.Add(-window), at.Add(window), MaxCandidates+1)
	if err != nil {
		return nil, clock.Gap{}, fmt.Errorf("correlator: read candidates: %w", err)
	}
	defer rows.Close()

	var out []rules.Signal
	for rows.Next() {
		var s rules.Signal
		var payload, attributes, structured []byte
		var band, obsID, profile, degraded, detail string
		var drift int32
		var insufficient bool

		if err := rows.Scan(
			&s.EventID, &s.SourceEventID, &s.EventType, &s.NormalizedAt,
			&s.DeviceID, &s.ZoneID, &s.ClockDriftMS, &payload,
			&s.DeviceKind, &attributes, &s.ZonePath,
			&band, &drift, &obsID, &profile, &structured,
			&insufficient, &degraded, &detail,
		); err != nil {
			return nil, clock.Gap{}, fmt.Errorf("correlator: scan candidate: %w", err)
		}

		if drift > 0 || band != "" {
			s.ClockDriftMS = drift
		}
		s.Tier = tier
		s.DriftedExcluded = band == string(clock.BandSevere)
		_ = json.Unmarshal(payload, &s.Payload)
		if s.Payload == nil {
			s.Payload = map[string]any{}
		}
		s.DeviceTags = decodeTags(attributes)
		s.AIObservation = buildObservation(obsID, profile, structured, insufficient, degraded, detail)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, clock.Gap{}, fmt.Errorf("correlator: candidate rows: %w", err)
	}

	// One more than the cap was requested precisely so "we stopped" is
	// distinguishable from "that was everything".
	if len(out) > MaxCandidates {
		out = out[:MaxCandidates]
		report.Truncated = true
	}
	report.Candidates = len(out)

	// Assess the clock gap for this correlation window.
	// The window is centered on the trigger event; we use the same bounds
	// that the SQL query used.
	windowStart := at.Add(-window)
	windowEnd := at.Add(window)
	gap := clock.AssessGap(clock.GapState{
		EventCount:      len(out),
		Window:          clock.Window{From: windowStart, To: windowEnd},
		State:           nil, // Device state not available per-event; excluded by drift check already.
		EvidenceFetched: true,
		EvidenceNeeded:  true,
	})

	// The SQL orders already, and it is re-applied here because the order is a
	// property of the result rather than of the query that produced it: the
	// engine's explanation and the incident's incident_events rows both follow
	// this sequence, and a caller that assembled the same slice by another route
	// must get the same one. (normalized_at, event_id) is a total order because
	// event_id is unique.
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].NormalizedAt.Equal(out[j].NormalizedAt) {
			return out[i].NormalizedAt.Before(out[j].NormalizedAt)
		}
		return out[i].EventID < out[j].EventID
	})
	return out, gap, nil
}

// decodeTags pulls the tag list out of a device's attributes document.
//
// A site that stores tags somewhere else in attributes gets no tags and no
// error: a rule matching on a tag nobody has set matches nothing, which is the
// correct answer and not a failure to surface.
func decodeTags(attributes []byte) []string {
	if len(attributes) == 0 {
		return nil
	}
	var doc struct {
		Tags []string `json:"tags"`
	}
	if err := json.Unmarshal(attributes, &doc); err != nil {
		return nil
	}
	return doc.Tags
}

// buildObservation maps a stored observation onto the evaluator's view of one,
// and returns nil when there is none.
//
// nil is load-bearing. A zero-valued AIObservation would say "the model looked
// and found nothing", which is the one thing a missing observation must never
// mean (invariant 3). Absent is nil; present-but-degraded is a pointer to an
// observation whose Degraded is set, because a recorded degradation is a fact and
// an absent observation is not.
func buildObservation(id, profile string, structured []byte, insufficient bool, degraded, detail string) *rules.AIObservation {
	if id == "" {
		return nil
	}
	obs := &rules.AIObservation{
		ObservationID:        id,
		ProfileID:            profile,
		InsufficientEvidence: insufficient,
		Degraded:             degraded != "" && degraded != "none",
		DegradedReason:       degraded,
	}
	if len(structured) > 0 {
		_ = json.Unmarshal(structured, &obs.Structured)
	}
	if obs.Structured == nil {
		obs.Structured = map[string]any{}
	}
	// The reason is the machine-readable half and the detail is the human one,
	// and they are different fields because they are different facts.
	//
	// They were concatenated here, which corrupts the machine half.
	// `DegradedReason` is a member of a closed set — `none|model_unavailable|
	// no_runtime|timeout|error|absent` — and `internal/rules/match.go` switches on
	// the exact string, so "model_unavailable: the VLM worker exited 137" falls
	// through to the `default` arm and is recorded on the incident as `absent`.
	// `ai_observations.degraded_reason` is CHECK-constrained to the same set, so
	// the database would have refused the write; the corruption happened in memory
	// first and then reached the incident's verification row and the replay
	// narrative, which are free text and accepted it.
	if detail != "" {
		if obs.DegradedReason == "" {
			// A detail with no class is a degradation the caller named in prose.
			// `error` is the honest member of the closed set: something went wrong
			// and the caller said so.
			obs.DegradedReason = "error"
		}
		obs.DegradedDetail = strings.TrimSpace(obs.DegradedDetail + " " + detail)
	}
	return obs
}
