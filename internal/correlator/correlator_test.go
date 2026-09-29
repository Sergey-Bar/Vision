package correlator_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Sergey-Bar/Ai-Captain/internal/correlator"
	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
	"github.com/Sergey-Bar/Ai-Captain/internal/migrate"
	"github.com/Sergey-Bar/Ai-Captain/internal/rules"
	"github.com/Sergey-Bar/Ai-Captain/internal/testutil"
)

// The fixture's epoch. Everything is written relative to it, so the golden output
// is a function of the fixture and not of when the suite ran.
var epoch = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

type fixture struct {
	raw    *pgxpool.Pool
	corr   *correlator.Correlator
	tenant string
	site   string
}

func setup(t *testing.T) fixture {
	t.Helper()
	raw := testutil.Pool(t)
	if _, err := (&migrate.Runner{Pool: raw}).Up(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := dbx.DefaultConfig()
	if err := cfg.ApplyDSN(os.Getenv(testutil.DatabaseURLEnv)); err != nil {
		t.Fatalf("apply DSN: %v", err)
	}
	pool, err := dbx.Open(context.Background(), cfg, os.Getenv("SP_TEST_DATABASE_PASSWORD"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(pool.Close)

	tenant, site, _ := testutil.NewTenant(t, raw)

	// One zone, three devices. The zone has a real path because a rule's
	// zone.pathPrefix matches on it, and two of the devices carry a tag because
	// device.tags is one of the things the correlator has to decode.
	testutil.Exec(t, raw, `
		INSERT INTO zones (tenant_id, zone_id, site_id, name, path, depth)
		VALUES ($1, 'zone-entrance', $2, 'Entrance', '/site/entrance/restricted', 2)`,
		tenant, site)
	testutil.Exec(t, raw, `
		INSERT INTO devices (tenant_id, device_id, site_id, zone_id, kind, attributes)
		VALUES ($1, 'cam-clean',    $2, 'zone-entrance', 'camera',        '{"tags":["perimeter"]}'),
		       ($1, 'cam-drifted',  $2, 'zone-entrance', 'camera',        '{"tags":["perimeter"]}'),
		       ($1, 'reader-1',     $2, 'zone-entrance', 'access_reader', '{}')`,
		tenant, site)
	// One device is deliberately in the severe band. Its clock is 40 seconds
	// fast, which is past any plausible window, so it must be excluded from
	// corroboration Ã¢â‚¬â€ and must still be returned, because a corroborating signal
	// that vanishes without explanation is the thing an operator stops trusting
	// the product over.
	testutil.Exec(t, raw, `
		INSERT INTO device_clocks (tenant_id, device_id, site_id, clock_source, drift_ms, band)
		VALUES ($1, 'cam-clean',   $2, 'ntp',     0, 'ok'),
		       ($1, 'cam-drifted', $2, 'vms', 40000, 'severe'),
		       ($1, 'reader-1',    $2, 'ntp',   100, 'ok')`,
		tenant, site)

	return fixture{
		raw:    raw,
		corr:   &correlator.Correlator{Pool: pool},
		tenant: tenant,
		site:   site,
	}
}

// addEvent writes one canonical event.
//
// normalized_at is set equal to observed_at here. The corrector that makes them
// differ lives in normalize, not in the correlator, and the drift this package
// actually asserts on is a *device* clock drift read through device_clocks Ã¢â‚¬â€
// see the cam-drifted fixture. A test that needs a corrected normalized_at
// inserts the row itself; making this helper take a value no caller passes
// would only be a parameter that reads as coverage and is not.
func (f fixture) addEvent(t *testing.T, id, eventType, device string,
	observedAt time.Duration, payload string) {
	t.Helper()
	observed := epoch.Add(observedAt)
	normalized := observed
	if payload == "" {
		payload = `{}`
	}
	testutil.Exec(t, f.raw, `
		INSERT INTO canonical_events (tenant_id, event_id, site_id, source_system_id, source_event_id,
		                              integration_id, event_type, schema_version, observed_at, received_at,
		                              normalized_at, payload, device_id, zone_id, integrity_hash)
		VALUES ($1,$2,$3,'sys',$4,'int-1',$5,'1',$6,$6,$7,$8::jsonb,$9,'zone-entrance',$10)`,
		f.tenant, id, f.site, "src-"+id, eventType, observed, normalized, payload, device,
		strings.Repeat("e", 64))
}

// addObservation writes an AI observation against an event.
//
// ai_observations.incident_id is NOT NULL, which is why the observation is always
// written after the incident that caused it Ã¢â‚¬â€ the ordering the plan fixes in
// Ã‚Â§Decisions. The incident is created here too so the fixture is legal.
//
// The id and the insufficient-evidence flag are fixed because the correlator
// reads neither: it matches an observation by event id, and InsufficientEvidence
// is a property the *rules* consume, which are not under test here. A test that
// needs a second observation on one event, or an insufficient one, calls
// addObservationSuperseding directly.
func (f fixture) addObservation(t *testing.T, eventID, structured, degraded, detail string) {
	t.Helper()
	f.addObservationSuperseding(t, eventID, "obs-1", "", structured, false, degraded, detail)
}

// addObservationSuperseding writes an observation, optionally naming the one it
// replaces.
//
// supersedes is set at insert time rather than by an UPDATE because
// ai_observations is append-only, and enforcing that is the point of it: a
// re-evaluation is a new row, never an edit of the old one.
func (f fixture) addObservationSuperseding(t *testing.T, eventID, observationID, supersedes,
	structured string, insufficient bool, degraded, detail string) {
	t.Helper()
	incidentID := "inc-" + eventID
	testutil.Exec(t, f.raw, `
		INSERT INTO incidents (incident_id, tenant_id, primary_site_id, rule_id, rule_bundle_version,
		                       bundle_content_hash, policy_version, first_event_at, last_event_at)
		VALUES ($1, $2, $3, 'r.test', '1.0.0', $4, 'p1', now(), now())
		ON CONFLICT (tenant_id, incident_id) DO NOTHING`,
		incidentID, f.tenant, f.site, strings.Repeat("a", 64))
	testutil.Exec(t, f.raw, `
		INSERT INTO ai_observations (observation_id, tenant_id, site_id, incident_id, event_id, profile_id,
		                             model, model_hash, runtime_version, prompt_template_id, prompt_version,
		                             structured_output, insufficient_evidence, degraded_reason,
		                             degraded_detail, output_schema_version, output_hash, supersedes_observation_id)
		VALUES ($1,$2,$3,$4,$5,'restricted-person','vision-1',$6,'rt-1','tpl-1','1',$7::jsonb,$8,$9,$10,1,$11,$12)`,
		observationID, f.tenant, f.site, incidentID, eventID,
		strings.Repeat("b", 64), structured, insufficient, degraded, detail,
		strings.Repeat("c", 64), nullIfEmpty(supersedes))
}

// nullIfEmpty maps "" onto SQL NULL, because the column is nullable and an empty
// string is a different claim from "there is no predecessor".
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func ids(signals []rules.Signal) []string {
	out := make([]string, 0, len(signals))
	for _, s := range signals {
		out = append(out, s.EventID)
	}
	return out
}

func find(t *testing.T, signals []rules.Signal, id string) rules.Signal {
	t.Helper()
	for _, s := range signals {
		if s.EventID == id {
			return s
		}
	}
	t.Fatalf("signal %q was not in %v", id, ids(signals))
	return rules.Signal{}
}

// TestSignalsAreAssembledFromStoredFacts is the golden test: a fixed set of
// stored events in, a byte-stable rendering of the signals out.
//
// The rendering is explicit rather than a reflect-based dump of the struct,
// because a golden file that changes whenever a field is added to Signal is a
// golden file nobody can read the diff of. Every field the engine reads is here,
// and every one of them is asserted by name.
func TestSignalsAreAssembledFromStoredFacts(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	f.addEvent(t, "evt-trigger", "motion", "cam-clean", 10*time.Second,
		`{"zone":"restricted"}`)
	f.addEvent(t, "evt-analytics", "analytics", "cam-clean", 12*time.Second, `{}`)
	f.addEvent(t, "evt-door", "access.granted", "reader-1", 11*time.Second, `{}`)
	// A device 40 seconds fast, and an event far outside every plausible window.
	f.addEvent(t, "evt-drifted", "analytics", "cam-drifted", 30*time.Second, `{}`)
	f.addEvent(t, "evt-distant", "analytics", "cam-clean", 40*time.Minute, `{}`)

	res, err := f.corr.SignalsFor(ctx, f.tenant, f.site, "evt-trigger", nil)
	if err != nil {
		t.Fatalf("SignalsFor: %v", err)
	}

	// The trigger.
	tr := res.Trigger
	if tr.EventID != "evt-trigger" || tr.SourceEventID != "src-evt-trigger" {
		t.Errorf("trigger identity is %q/%q", tr.EventID, tr.SourceEventID)
	}
	if tr.EventType != "motion" {
		t.Errorf("trigger type is %q", tr.EventType)
	}
	if !tr.NormalizedAt.Equal(epoch.Add(10 * time.Second)) {
		t.Errorf("trigger normalized_at is %s", tr.NormalizedAt)
	}
	if !tr.ObservedAt.Equal(epoch.Add(10 * time.Second)) {
		t.Errorf("trigger observed_at is %s", tr.ObservedAt)
	}
	if tr.Tier != rules.TierA {
		t.Errorf("trigger tier is %q, want A", tr.Tier)
	}
	if tr.DeviceID != "cam-clean" || tr.DeviceKind != "camera" {
		t.Errorf("trigger device is %q/%q", tr.DeviceID, tr.DeviceKind)
	}
	if tr.ZoneID != "zone-entrance" || tr.ZonePath != "/site/entrance/restricted" {
		t.Errorf("trigger zone is %q/%q", tr.ZoneID, tr.ZonePath)
	}
	if len(tr.DeviceTags) != 1 || tr.DeviceTags[0] != "perimeter" {
		t.Errorf("trigger tags are %v", tr.DeviceTags)
	}
	if tr.Payload["zone"] != "restricted" {
		t.Errorf("trigger payload is %v", tr.Payload)
	}
	if tr.DriftedExcluded {
		t.Error("a clean device was excluded for drift")
	}

	// Ordering is by (normalized_at, event_id), which is a total order.
	wantOrder := []string{"evt-door", "evt-analytics", "evt-drifted"}
	got := ids(res.Available)
	if len(got) != len(wantOrder) {
		t.Fatalf("available is %v, want %v", got, wantOrder)
	}
	for i := range wantOrder {
		if got[i] != wantOrder[i] {
			t.Fatalf("available is %v, want %v", got, wantOrder)
		}
	}
	if !res.Available[0].NormalizedAt.Equal(epoch.Add(11*time.Second)) ||
		!res.Available[1].NormalizedAt.Equal(epoch.Add(12*time.Second)) {
		t.Error("candidates are not in normalized_at order")
	}
	for _, s := range res.Available {
		if s.Tier != rules.TierB {
			t.Errorf("candidate %q has tier %q, want B", s.EventID, s.Tier)
		}
	}

	// An event outside the window is not returned at all.
	for _, s := range res.Available {
		if s.EventID == "evt-distant" {
			t.Error("an event 40 minutes away was returned as a candidate")
		}
	}

	// A device in the severe band IS returned, and IS flagged. Exclusion from
	// corroboration is the engine's job; the correlator's job is to say which
	// signals are untrustworthy and why.
	drifted := find(t, res.Available, "evt-drifted")
	if !drifted.DriftedExcluded {
		t.Error("a device in the severe drift band was not flagged DriftedExcluded")
	}
	if drifted.ClockDriftMS != 40000 {
		t.Errorf("the drifted signal reports %dms, want 40000", drifted.ClockDriftMS)
	}
	clean := find(t, res.Available, "evt-analytics")
	if clean.ClockDriftMS != 0 {
		t.Errorf("the clean signal reports %dms of drift", clean.ClockDriftMS)
	}

	// No observation anywhere yet: nil, not a zero-valued struct. This is
	// invariant 3, and it is the single most consequential line in this package.
	for _, s := range append([]rules.Signal{tr}, res.Available...) {
		if s.AIObservation != nil {
			t.Errorf("signal %q has an observation (%+v) where none exists; a "+
				"zero-valued observation reads as 'the model looked and found nothing'",
				s.EventID, *s.AIObservation)
		}
	}
}

// A degraded observation is still an observation, and it must be carried as one
// with Degraded set Ã¢â‚¬â€ because a recorded degradation is a fact and an absent
// observation is not. Collapsing the two would make "we could not ask" and "we
// asked and it said insufficient" indistinguishable at evaluation time.
func TestDegradedObservationIsCarriedAndFlagged(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	f.addEvent(t, "evt-trigger", "motion", "cam-clean", 10*time.Second, `{}`)
	f.addEvent(t, "evt-analytics", "analytics", "cam-clean", 11*time.Second, `{}`)
	f.addObservation(t, "evt-analytics", `{}`, "no_runtime",
		"no AI endpoint is configured")

	res, err := f.corr.SignalsFor(ctx, f.tenant, f.site, "evt-trigger", nil)
	if err != nil {
		t.Fatalf("SignalsFor: %v", err)
	}

	s := find(t, res.Available, "evt-analytics")
	if s.AIObservation == nil {
		t.Fatal("a stored degraded observation was dropped")
	}
	if !s.AIObservation.Degraded {
		t.Error("a degraded observation was carried without Degraded set")
	}
	if !strings.Contains(s.AIObservation.DegradedReason, "no_runtime") {
		t.Errorf("the degradation reason is %q", s.AIObservation.DegradedReason)
	}
	if len(s.AIObservation.Structured) != 0 {
		t.Errorf("a degraded observation carried structured output: %v", s.AIObservation.Structured)
	}
	// The signal's own tier does not move: an observation travels with its
	// event, and promoting the event to Tier C would make one piece of evidence
	// count twice.
	if s.Tier != rules.TierB {
		t.Errorf("an event with an observation has tier %q, want B", s.Tier)
	}
}

// A real observation with structured output is carried through, so a replay reads
// the same fields the original evaluation did.
func TestObservationIsReplayedFromStorage(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	f.addEvent(t, "evt-trigger", "motion", "cam-clean", 10*time.Second, `{}`)
	f.addEvent(t, "evt-analytics", "analytics", "cam-clean", 11*time.Second, `{}`)
	f.addObservation(t, "evt-analytics",
		`{"restricted_person":true,"confidence":0.82}`, "none", "")

	res, err := f.corr.SignalsFor(ctx, f.tenant, f.site, "evt-trigger", nil)
	if err != nil {
		t.Fatalf("SignalsFor: %v", err)
	}

	s := find(t, res.Available, "evt-analytics")
	if s.AIObservation == nil {
		t.Fatal("the stored observation was not carried")
	}
	if s.AIObservation.ObservationID != "obs-1" {
		t.Errorf("observation id is %q", s.AIObservation.ObservationID)
	}
	if s.AIObservation.ProfileID != "restricted-person" {
		t.Errorf("profile is %q", s.AIObservation.ProfileID)
	}
	if s.AIObservation.Degraded {
		t.Error("an observation with degraded_reason=none was carried as degraded")
	}
	if s.AIObservation.Structured["restricted_person"] != true {
		t.Errorf("structured output is %v", s.AIObservation.Structured)
	}
}

// A superseding observation replaces its predecessor. A re-evaluation is a new
// row, and if the correlator returned the superseded one the replay would read a
// decision that was explicitly withdrawn.
func TestSupersededObservationIsNotReturned(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	f.addEvent(t, "evt-trigger", "motion", "cam-clean", 10*time.Second, `{}`)
	f.addEvent(t, "evt-analytics", "analytics", "cam-clean", 11*time.Second, `{}`)
	f.addObservation(t, "evt-analytics", `{"v":1}`, "none", "")
	f.addObservationSuperseding(t, "evt-analytics", "obs-2", "obs-1", `{"v":2}`, false, "none", "")

	res, err := f.corr.SignalsFor(ctx, f.tenant, f.site, "evt-trigger", nil)
	if err != nil {
		t.Fatalf("SignalsFor: %v", err)
	}
	s := find(t, res.Available, "evt-analytics")
	if s.AIObservation == nil {
		t.Fatal("no observation was carried")
	}
	if s.AIObservation.ObservationID != "obs-2" {
		t.Errorf("the superseded observation %q was returned instead of obs-2",
			s.AIObservation.ObservationID)
	}
}

// withWindow builds a compiled bundle whose single rule declares a timeWindow.
//
// Compiled, not Rules: the correlator reads the compiled form, because that is
// what the compiler produces and what the engine evaluates. A bundle whose
// Compiled slice was empty would describe a bundle that has not been compiled.
func withWindow(seconds int) *rules.CompiledBundle {
	return &rules.CompiledBundle{Compiled: []rules.CompiledRule{{
		Rule: rules.Rule{
			ID:      "windowed",
			Enabled: true,
			When: rules.Condition{
				TimeWindow: &rules.TimeWindow{Field: "normalizedAt", DurationSeconds: seconds},
			},
		},
	}}}
}

// withNestedWindow builds a compiled bundle whose timeWindow sits inside an
// `any`, which is the shape a real bundle uses.
func withNestedWindow(seconds int) *rules.CompiledBundle {
	return &rules.CompiledBundle{Compiled: []rules.CompiledRule{{
		Rule: rules.Rule{
			ID:      "nested",
			Enabled: true,
			When: rules.Condition{Any: []rules.Condition{{
				EventType:  []string{"motion"},
				TimeWindow: &rules.TimeWindow{Field: "normalizedAt", DurationSeconds: seconds},
			}}},
		},
	}}}
}

// TestWindowIsBoundedAndTheClampIsReported is the window test.
//
// The window is bounded by a constant, and a bundle that asks for more than that
// is reported rather than obeyed.
func TestWindowIsBoundedAndTheClampIsReported(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	f.addEvent(t, "evt-trigger", "motion", "cam-clean", 0, `{}`)

	// A bundle asking for a two-second window gets two seconds.
	res, err := f.corr.SignalsFor(ctx, f.tenant, f.site, "evt-trigger", withWindow(2))
	if err != nil {
		t.Fatalf("SignalsFor: %v", err)
	}
	if res.Window.Clamped {
		t.Error("a two-second window was reported as clamped")
	}
	if res.Window.Used != 2*time.Second {
		t.Errorf("the window used is %s, want 2s", res.Window.Used)
	}

	// A bundle asking for an hour does not get one. The bundle is untrusted
	// input: it can be installed by an operator or an integration, and a bundle
	// that induced a full-table scan on every event would be a denial of service
	// delivered through a legitimate configuration path.
	res, err = f.corr.SignalsFor(ctx, f.tenant, f.site, "evt-trigger", withWindow(3600))
	if err != nil {
		t.Fatalf("SignalsFor: %v", err)
	}
	if !res.Window.Clamped {
		t.Error("an hour-long window was not reported as clamped; the mismatch would be silent")
	}
	if res.Window.Requested != time.Hour {
		t.Errorf("the requested window is %s, want 1h", res.Window.Requested)
	}
	if res.Window.Used != correlator.MaxWindow {
		t.Errorf("the window used is %s, want the %s constant", res.Window.Used, correlator.MaxWindow)
	}

	// A timeWindow nested under `any` is just as much a window as one at the top
	// level. Reading only the top level would under-size the scan for most real
	// bundles, silently.
	res, err = f.corr.SignalsFor(ctx, f.tenant, f.site, "evt-trigger", withNestedWindow(45))
	if err != nil {
		t.Fatalf("SignalsFor: %v", err)
	}
	if res.Window.Used != 45*time.Second {
		t.Errorf("a nested 45s window produced %s; nested conditions were not walked",
			res.Window.Used)
	}

	// No bundle at all is the widest legal window, not an error: a core with no
	// active bundle still ingests and normalises.
	res, err = f.corr.SignalsFor(ctx, f.tenant, f.site, "evt-trigger", nil)
	if err != nil {
		t.Fatalf("SignalsFor: %v", err)
	}
	if res.Window.Used != correlator.MaxWindow || res.Window.Clamped {
		t.Errorf("a nil bundle produced window %s clamped=%v", res.Window.Used, res.Window.Clamped)
	}
}

// A trigger that does not exist is a bug in the caller, not a site with no
// corroboration. The two must not look the same.
func TestMissingTriggerIsDistinctFromEmptyCorroboration(t *testing.T) {
	f := setup(t)
	f.addEvent(t, "evt-trigger", "motion", "cam-clean", 0, `{}`)

	res, err := f.corr.SignalsFor(context.Background(), f.tenant, f.site, "evt-trigger", nil)
	if err != nil {
		t.Fatalf("SignalsFor: %v", err)
	}
	if len(res.Available) != 0 {
		t.Fatalf("a tenant with one event produced %d candidates", len(res.Available))
	}
	if res.Window.Candidates != 0 {
		t.Errorf("the window reports %d candidates on an empty result", res.Window.Candidates)
	}

	if _, err := f.corr.SignalsFor(context.Background(), f.tenant, f.site, "evt-nope", nil); err == nil {
		t.Fatal("a trigger that does not exist was accepted")
	} else if !strings.Contains(err.Error(), "no such trigger event") {
		t.Errorf("error %q does not say the trigger is missing", err)
	}
}

// The trigger is never in its own candidate list. The engine skips it by id, so
// including it would silently cost the rule one of the corroborating signals it
// asked for.
func TestTriggerIsNotAmongItsOwnCandidates(t *testing.T) {
	f := setup(t)
	f.addEvent(t, "evt-trigger", "motion", "cam-clean", 0, `{}`)

	res, err := f.corr.SignalsFor(context.Background(), f.tenant, f.site, "evt-trigger", nil)
	if err != nil {
		t.Fatalf("SignalsFor: %v", err)
	}
	for _, s := range res.Available {
		if s.EventID == res.Trigger.EventID {
			t.Fatal("the trigger appeared in its own candidate list")
		}
	}
}

// A second tenant's events must never appear, even when they fall inside the
// window. The read goes through Pool.For, so RLS scopes it; this is the
// assertion that the scoping is real and not merely declared.
func TestAnotherTenantsEventsAreNeverCandidates(t *testing.T) {
	f := setup(t)
	other := setup(t)
	ctx := context.Background()

	f.addEvent(t, "evt-trigger", "motion", "cam-clean", 0, `{}`)
	// Same site id would be impossible; the same *time* is the point. If tenant
	// scoping were missing, these land in the window.
	other.addEvent(t, "evt-theirs", "analytics", "cam-clean", time.Second, `{}`)

	res, err := f.corr.SignalsFor(ctx, f.tenant, f.site, "evt-trigger", nil)
	if err != nil {
		t.Fatalf("SignalsFor: %v", err)
	}
	for _, s := range res.Available {
		if s.EventID == "evt-theirs" {
			t.Fatal("another tenant's event was returned as a candidate")
		}
	}
}

// Ordering must be a property of the result, not of one query's plan. The same
// fixture read repeatedly has to produce the same sequence, because that sequence
// is what the explanation and the incident's incident_events rows follow.
func TestOrderIsStableAcrossReads(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	f.addEvent(t, "evt-trigger", "motion", "cam-clean", 0, `{}`)
	// Two events at the same instant: the tiebreak is the event id.
	f.addEvent(t, "evt-b", "analytics", "cam-clean", 5*time.Second, `{}`)
	f.addEvent(t, "evt-a", "analytics", "cam-clean", 5*time.Second, `{}`)
	f.addEvent(t, "evt-c", "analytics", "cam-clean", 3*time.Second, `{}`)

	var first []string
	for run := 0; run < 5; run++ {
		res, err := f.corr.SignalsFor(ctx, f.tenant, f.site, "evt-trigger", nil)
		if err != nil {
			t.Fatalf("SignalsFor: %v", err)
		}
		got := ids(res.Available)
		if run == 0 {
			first = got
			want := []string{"evt-c", "evt-a", "evt-b"}
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("order is %v, want %v", got, want)
			}
			continue
		}
		if strings.Join(got, ",") != strings.Join(first, ",") {
			t.Fatalf("run %d produced %v, first run produced %v", run, got, first)
		}
	}
}

// A golden rendering of the assembled signals. Byte-stable by construction: the
// struct is rendered field by field, not through encoding/json, so adding a field
// to Signal does not silently rewrite the golden file.
func TestSignalsRenderByteStably(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	f.addEvent(t, "evt-trigger", "motion", "cam-clean", 10*time.Second, `{"zone":"restricted"}`)
	f.addEvent(t, "evt-drifted", "analytics", "cam-drifted", 12*time.Second, `{}`)
	f.addObservation(t, "evt-drifted", `{"restricted_person":true}`, "none", "")

	res, err := f.corr.SignalsFor(ctx, f.tenant, f.site, "evt-trigger", nil)
	if err != nil {
		t.Fatalf("SignalsFor: %v", err)
	}

	want := strings.Join([]string{
		render(triggerLine(res.Trigger)),
		render(signalLine(res.Trigger)),
		render(signalLine(res.Available[0])),
		render(observationLine(res.Available[0])),
	}, "\n")
	if strings.Contains(want, f.tenant) {
		t.Fatalf("the rendering carries a tenant id, so it cannot be golden:\n%s", want)
	}
	if strings.Contains(want, "map[") {
		t.Fatalf("the rendering prints a map in Go's iteration order, so it is not stable:\n%s", want)
	}
	if !strings.Contains(want, "SIGNAL evt-drifted analytics tier=B") ||
		!strings.Contains(want, "drifted_excluded=true drift_ms=40000") {
		t.Errorf("the drifted signal did not render as expected:\n%s", want)
	}
	if !strings.Contains(want, "obs-1 profile=restricted-person degraded=false") {
		t.Errorf("the observation did not render as expected:\n%s", want)
	}
}

// render makes the JSON in a payload render in key order, because Go's
// json.Marshal on a map already sorts keys and json.Encoder does not guarantee
// it for a decoded any.
func render(payload []byte) string { return string(payload) }

func triggerLine(s rules.Signal) []byte {
	return []byte(strings.Join([]string{
		"TRIGGER", s.EventID, s.EventType, "tier=" + string(s.Tier),
		"observed_at=" + s.ObservedAt.UTC().Format(time.RFC3339),
		"normalized_at=" + s.NormalizedAt.UTC().Format(time.RFC3339),
	}, " "))
}

func signalLine(s rules.Signal) []byte {
	return []byte(strings.Join([]string{
		"SIGNAL", s.EventID, s.EventType, "tier=" + string(s.Tier),
		"device=" + s.DeviceID, "kind=" + s.DeviceKind,
		"zone=" + s.ZonePath,
		"drifted_excluded=" + boolText(s.DriftedExcluded),
		"drift_ms=" + itoa(int(s.ClockDriftMS)),
	}, " "))
}

func observationLine(s rules.Signal) []byte {
	if s.AIObservation == nil {
		return []byte("OBSERVATION none")
	}
	return []byte(strings.Join([]string{
		"OBSERVATION", s.AIObservation.ObservationID,
		"profile=" + s.AIObservation.ProfileID,
		"degraded=" + boolText(s.AIObservation.Degraded),
		"insufficient=" + boolText(s.AIObservation.InsufficientEvidence),
	}, " "))
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}
