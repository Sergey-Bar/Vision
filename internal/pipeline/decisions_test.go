package pipeline_test

import (
	"os"
	"strings"
	"testing"

	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
	"github.com/Sergey-Bar/Ai-Captain/internal/testutil"

	"github.com/Sergey-Bar/Ai-Captain/internal/bundles"
	"github.com/Sergey-Bar/Ai-Captain/internal/rules"
)

const oneMotion = `[
	{"Id":"e1","Type":"Motion","Timestamp":"2026-03-01T09:00:00Z","Source":"Entrance Camera 01 [cam-1]"}
]`

func countIncidents(t *testing.T, h harness) int {
	t.Helper()
	var n int
	if err := h.loops.Deps.Pool.For(h.tenant, "test").QueryRow(t.Context(),
		`SELECT count(*) FROM incidents WHERE tenant_id = $1`, h.tenant).Scan(&n); err != nil {
		t.Fatalf("count incidents: %v", err)
	}
	return n
}

// runOnce polls and consumes, which is the path a real site takes on every tick.
func runOnce(t *testing.T, h harness) {
	t.Helper()
	h.loops.PollOnce(t.Context())
	h.loops.ConsumeOnce(t.Context())
}

// A warning is not a failure, and the difference is load-bearing.
//
// The compiler emits warnings for things a rule author has *decided*: a rule
// that degrades to `proceed_without` warns that it creates incidents on less
// evidence, and the semantic validator documents that as the intended shape for
// a rule that must still work with the AI plane down. `degrade_proceeds` and
// `if_ai_unavailable_unused` are both warnings, and both are endorsed.
//
// `activeBundle` refused any bundle with any problem, so one deliberate warning
// by a rule author made `decide` fail for EVERY event, with one log line each.
// The whole AI-degradation warning path was a bundle-killer in production, and
// the bundle had already passed `Store` and `Activate` — which is exactly where
// warnings are supposed to be allowed.
//
// The bundle below is the endorsed warning: a rule that must keep working with
// the model down, and says so.
func TestABundleWithWarningsStillDecides(t *testing.T) {
	h := setup(t, degradingBundle, oneMotion)
	runOnce(t, h)

	if n := countIncidents(t, h); n != 1 {
		t.Errorf("%d incident(s) were raised by a bundle that compiles with a warning, want 1. "+
			"A warning is something a rule author decided; treating it as a failure makes the "+
			"whole AI-degradation path a bundle-killer, and it did so silently", n)
	}
}

// The inverse, because the fix must not become a way to run a broken bundle.
//
// The store refuses an error-level problem, so this asserts the store's gate: a
// bundle that could not be stored is a bundle that cannot reach the pipeline.
func TestABundleWithAnErrorNeverReachesThePipeline(t *testing.T) {
	// A bundle with an error-level problem is refused at the store, and the
	// harness's own `setup` treats a problem as fatal � so the store's gate is
	// what is under test here, and it is the gate that decides whether such a
	// bundle can ever reach the pipeline at all.
	//
	// The pipeline's own side of the same rule lives in `activeBundle` and is
	// covered by TestABundleWithWarningsStillDecides: a warning must not stop the
	// pipeline, and an error must.
	// testutil.Pool skips rather than fails when there is no database, which is
	// what every other suite in this package does.
	testutil.Pool(t)
	cfg := dbx.DefaultConfig()
	if err := cfg.ApplyDSN(os.Getenv(testutil.DatabaseURLEnv)); err != nil {
		t.Fatalf("apply DSN: %v", err)
	}
	pool, err := dbx.Open(t.Context(), cfg, os.Getenv("SP_TEST_DATABASE_PASSWORD"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(pool.Close)

	// `Store` signals refusal through a zero Meta and a nil error, which is what
	// the API handler checks. Asserting on the error alone would pass for a bundle
	// that was silently not stored ? which is the shape of mistake that made a
	// warning look like a failure in the first place.
	meta, _, err := (&bundles.Service{
		Pool:     pool,
		Compiler: rules.NewCompiler(),
	}).Store(t.Context(), "t-refuse", []byte(brokenBundle), "test", "tester", "")
	if err != nil {
		t.Fatalf("store returned a hard error for a bundle that does not compile; it should "+
			"report the problem and store nothing: %v", err)
	}
	if meta.Version != "" {
		t.Fatalf("a bundle with an error-level problem was stored as %q:\n%s",
			meta.Version, strings.TrimSpace(brokenBundle))
	}
}

// Whether an incident is raised is the rule author's decision, not a consequence
// of the rule having matched.
//
// The gate was `RuleID != ""`, which raised an incident for two kinds of decision
// that mean the opposite:
//   - `then: {suppress: true}` resolves to State SUPPRESSED with a non-empty
//     RuleID and a zero Action. The rule whose entire purpose is to *prevent* an
//     incident was what created one, with the default severity medium.
//   - `then: {dispatch: [notify_operator]}` with no `create_incident` is
//     explicitly legal — the validator treats a non-empty dispatch list as a
//     non-empty action — and its Decision carries a RuleID.
//
// `internal/simulate` already modelled this correctly: its diff says
// `IncidentRaised: d.Action.CreateIncident`. So the digest an operator
// acknowledged at activation said "no incident" for a bundle that raised one in
// production, which is the one thing the activation gate exists to prevent.
func TestARuleThatAsksForNoIncidentRaisesNone(t *testing.T) {
	for _, tc := range []struct {
		name   string
		bundle string
	}{
		{"suppress", noIncidentBundle("      suppress: true\n")},
		{"dispatch only", dispatchOnlyBundle()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := setup(t, tc.bundle, oneMotion)
			runOnce(t, h)
			if n := countIncidents(t, h); n != 0 {
				t.Errorf("%d incident(s) were raised by a rule whose action asks for none. The "+
					"gate was `RuleID != \"\"`, which reads a match as a decision to raise one; "+
					"the simulation the operator approved says this bundle raises none", n)
			}
		})
	}
}

// The decision path a rule that DOES ask for an incident is unchanged: the
// control above must not become a way to drop every incident.
func TestARuleThatAsksForAnIncidentStillRaisesOne(t *testing.T) {
	h := setup(t, bundle, oneMotion)
	runOnce(t, h)
	if n := countIncidents(t, h); n != 1 {
		t.Errorf("%d incident(s) were raised, want 1. Gating on CreateIncident must not become "+
			"a way to drop every incident", n)
	}
}

const bundleHeader = `apiVersion: security.io/v1
kind: RuleBundle
version: 0.2.0
site:
  siteId: site-default
  timezones:
    - UTC
settings:
  clock:
    referenceSource: ntp
    driftWarnMs: 2000
    driftSevereMs: 10000
    skewTolerance: 5000
`

// degradingBundle produces `degrade_proceeds`: a rule that must keep working
// with the AI plane down, and warns that it creates incidents on less evidence
// when it is. The warning is the endorsed case ? the semantic validator
// documents `proceed_without` as a legitimate choice and warns about it precisely
// so the author sees it and keeps going.
const degradingBundle = bundleHeader + `rules:
  - id: camera.offline
    enabled: true
    priority: 10
    when:
      all:
        - eventType: [motion]
        - corroborate:
            eventTypes: [motion, analytics]
            minSignals: 1
            minEvidenceTier: B
            ifAIAvailable:
              degradeTo: proceed_without
    then:
      createIncident: true
      severity: high
`

// brokenBundle names an operator the language does not have, which is an
// error-level problem rather than a warning.
const brokenBundle = bundleHeader + `rules:
  - id: camera.offline
    enabled: true
    priority: 10
    when:
      all:
        - field:
            path: vendor.Direction
            op: definitelyNotAnOperator
            value: inbound
    then:
      createIncident: true
      severity: high
`

func noIncidentBundle(action string) string {
	return bundleHeader + `rules:
  - id: camera.offline
    enabled: true
    priority: 10
    when:
      all:
        - eventType: [motion]
    then:
      createIncident: false
` + action
}

// dispatchOnlyBundle is a rule that acts and does not raise: a non-empty dispatch
// list with `createIncident: false`. The schema requires `createIncident` to be
// said explicitly, which is the right shape ? but the pipeline's gate was
// `RuleID != ""`, which did not read it, so a rule that dispatched an alert and
// deliberately raised no incident raised one anyway.
func dispatchOnlyBundle() string {
	return bundleHeader + `rules:
  - id: camera.offline
    enabled: true
    priority: 10
    when:
      all:
        - eventType: [motion]
    then:
      createIncident: false
      dispatch:
        - type: notify_operator
          target: on-call
`
}
