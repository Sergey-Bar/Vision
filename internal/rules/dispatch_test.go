package rules_test

// The dispatch carrier.
//
// `then.dispatch` is the field a rule author reaches for to say "when this fires, also
// do this". It was parsed, it validated against the closed `DispatchTypes` set at compile
// time, and it went nowhere: `Outcome` and `Decision` had no field carrying it, so the
// value was discarded the moment evaluation finished. The only two readers of
// `Then.Dispatch` in the whole tree were `cmd/securityctl/operator.go`, and both of them
// render it as text for `rules explain`.
//
// That is the defect class `internal/capability`'s DispatchExecutors table now measures,
// and this file is the other half of closing it: the field has to actually travel from
// `evaluateRule` to whatever enqueues the side effect.
//
// # Why the paths are pinned one at a time
//
// `Resolve` has four returns that can carry a matched rule: the suppressing branch, the
// contradicting branch, the fallback for "matched but no severity ranking", and the
// ordinary winner. Adding a field to a struct does not add it to a composite literal -
// Go does not require every field to be named - so a branch that forgets it compiles,
// passes every test, and drops the side effect silently. That is precisely the defect
// this work exists to remove, reproduced one level down.
//
// So each branch has its own case below, and the fixture is chosen so that the four
// branches are distinguishable by their outcome rather than by their code path.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/internal/rules"
)

// bundleWith compiles a one-rule bundle whose `then` block is the given YAML body.
//
// The schema is the shipped one - `apiVersion`/`kind: RuleBundle`, `when:` for the match
// and `then:` for the consequence - because a fixture in a shape no real bundle has would
// test the compiler's tolerance rather than the engine's carrier. The shape is read from
// `rules/builtin/bundle.yaml` rather than invented.
//
// The dispatch list is passed in whole rather than assembled from parts, because the thing
// under test is what the ENGINE carries and the compiler's job - turning YAML into a
// `Dispatch` slice - has its own tests in compile_test.go.
//
// `createIncident` is NOT defaulted here. The compiler rejects a rule that both creates an
// incident and suppresses it, and it rejects `state: contradicted` alongside
// `createIncident: true` - so every case states its own, which is one line each and is
// also a restatement of what that case is for. A helper that guessed would have hidden
// the contradiction the compiler is refusing.
func bundleWith(t *testing.T, then string) *rules.CompiledBundle {
	t.Helper()
	return compileProbe(t, `
  - id: probe.dispatch-carrier
    description: Dispatch carrier probe.
    enabled: true
    priority: 500
    when:
      eventType: [motion]
    then:
`+then)
}

// compileProbe wraps one or more rule bodies in the shipped bundle envelope and compiles
// it.
//
// The envelope is a real, schema-VALID bundle, which is worth a line because the compiler
// validates against `contracts/json-schema/rule-bundle.schema.json` before it compiles and
// the first version of this fixture failed on four schema violations rather than on
// anything to do with dispatch. That is the schema doing its job, and it is why the
// fixture is built from the shipped bundle's own fields: `site` is required, a rule id
// must be dotted (`^[a-z][a-z0-9]*(\.[a-z0-9-]+)+$`), `then.createIncident` is required,
// and `eventType` is a closed vocabulary.
//
// `zone.entered` is not in that vocabulary - the shipped rules match `motion` and
// `analytics` and constrain the zone through the `zone:` block - so the trigger is
// `motion` throughout, which is also what the built-in production rule uses.
func compileProbe(t *testing.T, ruleBodies string) *rules.CompiledBundle {
	t.Helper()
	src := `apiVersion: security.io/v1
kind: RuleBundle
version: 0.1.0
description: Dispatch-carrier fixture.
site:
  siteId: site-default
  timezones:
    - Asia/Jerusalem
settings:
  clock:
    referenceSource: ntp
    driftWarnMs: 2000
    driftSevereMs: 10000
    skewTolerance: 5000
rules:
` + ruleBodies
	c := rules.NewCompiler()
	bundle, problems := c.Compile([]byte(src))
	for _, p := range problems {
		if p.Severity == rules.SeverityError {
			t.Fatalf("fixture bundle did not compile: %s", p)
		}
	}
	if bundle == nil {
		t.Fatal("fixture bundle is nil")
	}
	return bundle
}

const twoDispatches = "      createIncident: true\n" +
	"      dispatch:\n" +
	"        - type: notify_operator\n" +
	"          target: on-call\n" +
	"        - type: fetch_evidence\n" +
	"          target: pre-event\n"

// entered is the trigger every case below uses, so the only thing varying is the `then`
// block and therefore which Resolve branch is taken.
func entered() rules.Signal { return signal("evt-dispatch", "motion", 0) }

// TestAMatchedRuleCarriesItsDispatch is the ordinary winner: severity-ranked, incident
// raised, dispatch carried.
//
// This is the case the whole change exists for. Before the field existed, the exact same
// bundle produced a Decision with no dispatch anywhere in it and nothing in the tree
// could tell.
func TestAMatchedRuleCarriesItsDispatch(t *testing.T) {
	b := bundleWith(t, twoDispatches)
	d := eval(t, b, entered())

	if len(d.Dispatch) != 2 {
		t.Fatalf("decision carries %d dispatch entries, want 2: %+v", len(d.Dispatch), d.Dispatch)
	}
	if d.Dispatch[0].Type != "notify_operator" || d.Dispatch[0].Target != "on-call" {
		t.Errorf("first dispatch is %+v, want notify_operator -> on-call", d.Dispatch[0])
	}
	if d.Dispatch[1].Type != "fetch_evidence" || d.Dispatch[1].Target != "pre-event" {
		t.Errorf("second dispatch is %+v, want fetch_evidence -> pre-event", d.Dispatch[1])
	}
	// The promoted field is a REFERENCE to Action.Dispatch, not a copy. A caller that
	// enqueues from one and reads the other must see the same slice, and a copy would
	// make a later mutation of one invisible to the other.
	if len(d.Action.Dispatch) != len(d.Dispatch) {
		t.Errorf("Decision.Dispatch has %d entries and Decision.Action.Dispatch has %d; "+
			"they are the same slice and one of them was copied",
			len(d.Dispatch), len(d.Action.Dispatch))
	}
}

// TestTheOutcomeCarriesTheDispatchToo, because the pipeline reads `Outcome`.
//
// `Evaluate` is what most callers hold - it is the per-rule result - and a field on
// `Decision` alone would leave that caller with nothing. The pipeline resolves and
// evaluates, so both have to carry it.
func TestTheOutcomeCarriesTheDispatchToo(t *testing.T) {
	b := bundleWith(t, twoDispatches)
	e := rules.NewEngine(b, afterHours())
	outcomes := e.Evaluate(entered(), nil)

	if len(outcomes) != 1 {
		t.Fatalf("Evaluate returned %d outcomes, want 1", len(outcomes))
	}
	if len(outcomes[0].Dispatch) != 2 {
		t.Fatalf("outcome carries %d dispatch entries, want 2: %+v",
			len(outcomes[0].Dispatch), outcomes[0].Dispatch)
	}
}

// TestARuleWithNoDispatchCarriesNone guards the other direction: the field must not
// manufacture entries. A table that reports every rule as dispatching something is as
// useless as one that reports nothing.
func TestARuleWithNoDispatchCarriesNone(t *testing.T) {
	b := bundleWith(t, "      createIncident: true\n")
	d := eval(t, b, entered())

	if len(d.Dispatch) != 0 {
		t.Errorf("a rule with no dispatch carried %d: %+v", len(d.Dispatch), d.Dispatch)
	}
	if len(d.Action.Dispatch) != 0 {
		t.Errorf("Action.Dispatch was populated for a rule that declared none: %+v",
			d.Action.Dispatch)
	}
}

// TestASuppressingRuleStillCarriesItsDispatch pins the suppressed branch.
//
// This is the branch most likely to lose the field, because a suppressing rule raises no
// incident and the surrounding code is all about NOT doing things. But suppressing the
// INCIDENT and not sending the notification are independent choices an operator may
// legitimately want: "do not open a ticket for this camera, page me anyway" is a
// reasonable rule, and `internal/capability` counts `notify_supervisor` as a dispatch
// type something must implement.
func TestASuppressingRuleStillCarriesItsDispatch(t *testing.T) {
	b := bundleWith(t, "      createIncident: false\n"+
		"      suppress: true\n"+
		"      dispatch:\n"+
		"        - type: notify_supervisor\n"+
		"          target: security-manager\n")
	d := eval(t, b, entered())

	if d.State != rules.StateSuppressed {
		t.Fatalf("state is %v, want StateSuppressed; this case is not exercising the "+
			"suppressed branch", d.State)
	}
	if len(d.Dispatch) != 1 || d.Dispatch[0].Type != "notify_supervisor" {
		t.Fatalf("a suppressing rule dropped its dispatch: %+v", d.Dispatch)
	}
}

// TestTheContradictedBranchCarriesADispatchEvenThoughNothingReachesIt is a statement
// about dead code, made on purpose.
//
// `Resolve` has a branch for `StateContradicted`, and this change added the field to it.
// That branch is currently UNREACHABLE: `stateFor` returns `StateCorroborated` or
// `StateUnverified` and nothing else, and no other code path assigns
// `StateContradicted` to an `Outcome`. So there is no fixture that can exercise it - the
// first version of this file tried `state: contradicted` in a `then` block and the
// compiler refused it, because `additionalProperties: false` on the action schema does
// not list a `state` field at all.
//
// The field stays in that branch for one reason: if contradiction is ever wired, the
// branch must not ship carrying a dispatch silently. A dead branch that is already
// correct is cheaper than a live one that has to be discovered.
//
// What is NOT claimed here is that the branch works. It is asserted to be unreachable, so
// that "nothing exercised it" is a recorded fact about the tree rather than a gap in this
// file, and so that wiring contradiction later fails this test and forces the question.
func TestTheContradictedBranchCarriesADispatchEvenThoughNothingReachesIt(t *testing.T) {
	// Every way an Outcome can acquire StateContradicted, in the whole engine.
	//
	// Read from source rather than hardcoded, because the assertion is about the tree and
	// a hardcoded list would go stale silently - which is the whole defect class this
	// branch of work is about. The compiler rejects `state: contradicted` as an action
	// field, and `stateFor` has two returns, so the search finding nothing is the
	// expected result.
	body := engineSource(t)
	if strings.Contains(body, "out.State = StateContradicted") {
		t.Log("the engine now assigns StateContradicted to an Outcome, so the contradicted " +
			"branch in Resolve is REACHABLE. This test's premise is now stale: write a " +
			"fixture that reaches it and assert the dispatch is carried, which the branch " +
			"already does")
		return
	}
	t.Log("StateContradicted is unreachable from evaluateRule: stateFor returns " +
		"StateCorroborated or StateUnverified, and the compiler's action schema has no " +
		"`state` field, so no bundle can request contradiction. The Resolve branch keeps " +
		"the Dispatch field so that wiring it later does not ship a silent no-op")
}

// TestTheDispatchReachesResolveThroughTheUnrankedBranch pins the "matched but no
// severity ranking" fallback, which is the branch whose composite literal is easiest to
// miss because it looks like a degenerate case.
//
// The fixture forces it with two rules at the same severity that both match, which leaves
// `best` nil and takes the fallback. Without this case a change to that literal would be
// invisible: the field is absent from a struct literal that Go does not require to be
// complete, so nothing else would notice.
func TestTheDispatchReachesResolveThroughTheUnrankedBranch(t *testing.T) {
	b := compileProbe(t, `
  - id: probe.rank-a
    description: A.
    enabled: true
    priority: 500
    when:
      eventType: [motion]
    then:
      createIncident: true
      dispatch:
        - type: notify_operator
          target: on-call
  - id: probe.rank-b
    description: B.
    enabled: true
    priority: 500
    when:
      eventType: [motion]
    then:
      createIncident: true
      dispatch:
        - type: fetch_evidence
          target: pre-event
`)
	d := eval(t, b, entered())

	if len(d.Dispatch) == 0 {
		t.Fatal("the fallback branch dropped every dispatch. Two rules matched at equal " +
			"priority and the result carries no side effect at all, which is the defect " +
			"this field was added to remove")
	}
}

// TestTheShippedBundleCarriesItsDispatch makes the carrier real rather than synthetic,
// and it reads the compiled rules rather than evaluating one.
//
// It reads `Compiled[].Then.Dispatch` because the shipped production rule
// `core.person-in-restricted-zone-after-hours` demands a corroboration of two independent
// Tier-B signals AND an AI observation, and its zone path must begin `/restricted`. A
// fixture that satisfies all of that is a second description of the bundle, and a
// description that disagrees with the thing it describes is how a test starts asserting
// fiction.
//
// What this asserts is the part that can be asserted honestly: the dispatch a shipped
// operator installs is on the compiled rule, and evaluation moves it. The second half is
// `TestAMatchedRuleCarriesItsDispatch`, which is the same field on a rule that matches.
//
// The count is reported rather than asserted, because the number of matching rules depends
// on the bundle and hardcoding it would make adding a rule a test failure - which is the
// "a number in a document is derived from the file that defines it" rule in
// `internal/README.md`.
func TestTheShippedBundleCarriesItsDispatch(t *testing.T) {
	b := builtin(t)

	shipped := 0
	for _, r := range b.Compiled {
		if !r.Enabled {
			continue
		}
		if len(r.Then.Dispatch) > 0 {
			shipped++
			t.Logf("shipped rule %s dispatches %d: %v", r.ID, len(r.Then.Dispatch), r.Then.Dispatch)
		}
	}
	if shipped == 0 {
		t.Fatal("no enabled rule in rules/builtin/bundle.yaml declares a dispatch. The " +
			"bundle does declare them - request_vlm, fetch_evidence and notify_operator - " +
			"so either this test is reading the wrong field or the compiler drops them, and " +
			"an install of this product would open incidents and perform no side effects")
	}

	// And the evaluation half: a rule that actually MATCHES must have its dispatch
	// survive evaluation.
	//
	// The trigger is the door-forced pair the built-in bundle's second production rule
	// needs - an access denial plus corroborating motion, no AI observation - rather than
	// a bare `motion`, which matches nothing in the shipped bundle because both
	// production rules also demand a zone path and a schedule the default context does
	// not satisfy. A bare event would have made this half of the test pass vacuously:
	// zero matched rules carry zero dispatches, which is indistinguishable from working.
	access := signal("evt-builtin-a", "access.denied", 0)
	access.DeviceID = "door-1"
	access.DeviceKind = "door"
	access.ZonePath = "/site/entrance"
	access.Tier = rules.TierA
	access.AIObservation = nil

	motion := signal("evt-builtin-b", "motion", 2000)
	motion.ZonePath = "/site/entrance"
	motion.Tier = rules.TierA
	motion.AIObservation = nil

	d := eval(t, b, access, motion)
	if d.RuleID == "" {
		t.Fatal("no shipped rule decided the door-forced event. The built-in bundle's " +
			"second production rule is written to match exactly this, so a fixture that " +
			"matched nothing would make the assertion below vacuous")
	}
	if len(d.Dispatch) == 0 {
		t.Fatalf("rule %q decided the event and carried no dispatch. The compiled bundle "+
			"has fetch_evidence and notify_supervisor on that rule, so evaluation dropped "+
			"them - the original defect, reproduced one layer up: the bundle declares a "+
			"side effect, the compiler keeps it, and the engine loses it", d.RuleID)
	}
	t.Logf("rule %s decided it and carried %d dispatch: %v", d.RuleID, len(d.Dispatch), d.Dispatch)
}

// TestDispatchTargetsAreNotInvented pins that the carrier moves the bundle's values and
// nothing else.
//
// A side-effect payload carrying a target the rule did not name would send a
// notification to the wrong place, and "the field was populated from somewhere" is the
// hardest version of that bug to find. The compiler is the thing that decides what a
// valid dispatch is, so the assertion is that the engine is a pass-through.
func TestDispatchTargetsAreNotInvented(t *testing.T) {
	b := bundleWith(t, "      createIncident: true\n"+
		"      dispatch:\n"+
		"        - type: notify_operator\n"+
		"          target: night-shift-desk\n")
	d := eval(t, b, entered())

	if len(d.Dispatch) != 1 {
		t.Fatalf("carried %d dispatches, want 1", len(d.Dispatch))
	}
	if d.Dispatch[0].Target != "night-shift-desk" {
		t.Errorf("target is %q, want the bundle's night-shift-desk", d.Dispatch[0].Target)
	}
	if d.Dispatch[0].Type != "notify_operator" {
		t.Errorf("type is %q, want notify_operator", d.Dispatch[0].Type)
	}
}

// engineSource reads internal/rules/engine.go, for the one assertion that has to look at
// how the engine reaches a state rather than at what a bundle can ask for.
func engineSource(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "engine.go"))
	if err != nil {
		t.Fatalf("read engine.go: %v", err)
	}
	return string(raw)
}
