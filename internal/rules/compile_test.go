package rules_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/rules"
)

var siteNow = time.Date(2026, 3, 1, 22, 30, 0, 0, time.UTC)

func load(t *testing.T, path string) *rules.CompiledBundle {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	c := rules.NewCompiler()
	bundle, problems := c.Compile(raw)
	for _, p := range problems {
		if p.Severity == rules.SeverityError {
			t.Errorf("bundle did not compile: %s", p)
		} else {
			t.Logf("warning: %s", p)
		}
	}
	if bundle == nil {
		t.Fatal("bundle is nil")
	}
	return bundle
}

func builtin(t *testing.T) *rules.CompiledBundle {
	t.Helper()
	return load(t, repoPath(t, "rules", "builtin", "bundle.yaml"))
}

func repoPath(t *testing.T, parts ...string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(append([]string{dir}, parts...)...)
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not find the repository root")
	return ""
}

// The built-in bundle is what ships in the release archive, and a bundle that
// does not compile is a release that cannot be installed.
func TestBuiltinBundleCompiles(t *testing.T) {
	b := builtin(t)
	if b.Version == "" {
		t.Error("the built-in bundle has no version")
	}
	if len(b.Compiled) == 0 {
		t.Fatal("the built-in bundle compiled to no rules")
	}
	if len(b.Conflicts) != 0 {
		t.Errorf("the built-in bundle has %d same-priority conflicts: %+v", len(b.Conflicts), b.Conflicts)
	}
}

// The embedded schema and the published one must be the same file. A bundle
// validated against a stale schema is a bundle nobody reviewed.
func TestEmbeddedSchemaMatchesThePublishedContract(t *testing.T) {
	published, err := os.ReadFile(repoPath(t, "contracts", "json-schema", "rule-bundle.schema.json"))
	if err != nil {
		t.Fatalf("read published schema: %v", err)
	}
	embedded, err := os.ReadFile(repoPath(t, "internal", "rules", "schema", "rule-bundle.schema.json"))
	if err != nil {
		t.Fatalf("read embedded schema: %v", err)
	}
	var a, b any
	if err := json.Unmarshal(published, &a); err != nil {
		t.Fatalf("parse published: %v", err)
	}
	if err := json.Unmarshal(embedded, &b); err != nil {
		t.Fatalf("parse embedded: %v", err)
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Error("internal/rules/schema is not the published contract; copy it with " +
			"\"cp contracts/json-schema/rule-bundle.schema.json internal/rules/schema/\"")
	}
}

// ---------------------------------------------------------------------------
// A bundle is untrusted input.
// ---------------------------------------------------------------------------

func compile(t *testing.T, src string) (*rules.CompiledBundle, []rules.Problem) {
	t.Helper()
	return rules.NewCompiler().Compile([]byte(src))
}

const minimalSettings = `
settings:
  clock:
    skewTolerance: 5000
    driftWarnMs: 2000
    driftSevereMs: 10000
    referenceSource: ntp
`

func wrap(rulesYAML string) string {
	return "apiVersion: security.io/v1\nkind: RuleBundle\nversion: 0.1.0\n" +
		"site:\n  siteId: s1\n  timezones: [Asia/Jerusalem]\n" +
		minimalSettings + "rules:\n" + rulesYAML
}

func TestUnknownKeyIsRejected(t *testing.T) {
	// A typo in a key name must not be a silently ignored setting. A rule that
	// does not do what its author believes is worse than a load error.
	_, problems := compile(t, wrap(`
  - id: a.b
    enabled: true
    priority: 10
    typo: true
    when: {eventType: [motion]}
    then: {createIncident: true}
`))
	if !hasProblem(problems, "yaml_parse") {
		t.Errorf("an unknown key was accepted: %+v", problems)
	}
}

func TestMultipleYAMLDocumentsAreRejected(t *testing.T) {
	src := wrap(`
  - id: a.b
    enabled: true
    priority: 10
    when: {eventType: [motion]}
    then: {createIncident: true}
`) + "\n---\napiVersion: security.io/v1\n"
	_, problems := compile(t, src)
	if !hasProblem(problems, "yaml_multiple_documents") {
		t.Errorf("a second YAML document was accepted: %+v", problems)
	}
}

func TestSchemaViolationIsCaught(t *testing.T) {
	// A negative priority is outside the schema's range.
	_, problems := compile(t, wrap(`
  - id: a.b
    enabled: true
    priority: -1
    when: {eventType: [motion]}
    then: {createIncident: true}
`))
	if !hasProblem(problems, "schema_violation") {
		t.Errorf("a schema violation was not caught: %+v", problems)
	}
}

// The plan's most load-bearing rule about bundles: no shell, no SQL, no network,
// no dynamic code, no unbounded regex. There is no regex operator at all, and
// the closed set is enforced rather than documented.
func TestNoUnboundedRegexOperatorExists(t *testing.T) {
	for _, op := range []string{"matches", "regex", "re", "like", "search", "glob"} {
		if rules.FieldOperators[op] {
			t.Errorf("field operator %q exists; an unbounded pattern in an uploaded "+
				"bundle is a denial of service delivered as a YAML file", op)
		}
	}
	_, problems := compile(t, wrap(`
  - id: a.b
    enabled: true
    priority: 10
    when:
      field: {path: vendor.Message, op: matches, value: ".*"}
    then: {createIncident: true}
`))
	if !hasProblem(problems, "field_operator") {
		t.Errorf("a regex operator was accepted: %+v", problems)
	}
}

func TestFieldValueListIsBounded(t *testing.T) {
	var values []string
	for i := 0; i < 300; i++ {
		values = append(values, `"v`+itoa(i)+`"`)
	}
	_, problems := compile(t, wrap(`
  - id: a.b
    enabled: true
    priority: 10
    when:
      field: {path: vendor.Type, op: in, value: [`+strings.Join(values, ",")+`]}
    then: {createIncident: true}
`))
	if !hasProblem(problems, "field_value_too_large") {
		t.Errorf("an unbounded value list was accepted: %+v", problems)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// Semantic validation: a bundle that loads cleanly and cannot work.
// ---------------------------------------------------------------------------

func TestUnknownEventTypeIsRejected(t *testing.T) {
	_, problems := compile(t, wrap(`
  - id: a.b
    enabled: true
    priority: 10
    when: {eventType: [telepathy]}
    then: {createIncident: true}
`))
	if !hasProblem(problems, "unknown_event_type") {
		t.Errorf("a rule naming an event type the build does not produce was accepted: %+v", problems)
	}
}

func TestDuplicateRuleIDIsRejected(t *testing.T) {
	_, problems := compile(t, wrap(`
  - id: same.id
    enabled: true
    priority: 10
    when: {eventType: [motion]}
    then: {createIncident: true, severity: low}
  - id: same.id
    enabled: true
    priority: 20
    when: {eventType: [analytics]}
    then: {createIncident: true, severity: high}
`))
	if !hasProblem(problems, "duplicate_rule_id") {
		t.Errorf("duplicate rule ids were accepted: %+v", problems)
	}
}

func TestTimeWindowMayOnlyReadNormalizedAt(t *testing.T) {
	// observed_at is the source's claim and received_at is ours. Neither is a
	// stable basis for correlation, and a rule that read either would produce
	// results that cannot be reproduced on replay.
	for _, field := range []string{"observed_at", "received_at"} {
		_, problems := compile(t, wrap(`
  - id: a.b
    enabled: true
    priority: 10
    when:
      all:
        - timeWindow: {field: `+field+`, durationSeconds: 30}
        - corroborate: {eventTypes: [analytics], minSignals: 2}
    then: {createIncident: true}
`))
		if !hasProblem(problems, "time_window_field") {
			t.Errorf("a time window over %s was accepted: %+v", field, problems)
		}
	}
}

func TestEmptyConditionAndActionAreRejected(t *testing.T) {
	_, problems := compile(t, wrap(`
  - id: a.b
    enabled: true
    priority: 10
    when: {}
    then: {createIncident: false}
`))
	if !hasProblem(problems, "empty_condition") {
		t.Errorf("an empty condition was accepted: %+v", problems)
	}
	if !hasProblem(problems, "empty_action") {
		t.Errorf("an action that does nothing was accepted: %+v", problems)
	}
}

func TestContradictoryActionIsRejected(t *testing.T) {
	_, problems := compile(t, wrap(`
  - id: a.b
    enabled: true
    priority: 10
    when: {eventType: [motion]}
    then: {createIncident: true, suppress: true}
`))
	if !hasProblem(problems, "contradictory_action") {
		t.Errorf("a rule that both creates and suppresses was accepted: %+v", problems)
	}
}

func TestImpossibleClockBandsAreRejected(t *testing.T) {
	_, problems := compile(t, `
apiVersion: security.io/v1
kind: RuleBundle
version: 0.1.0
site: {siteId: s1, timezones: [UTC]}
settings:
  clock: {skewTolerance: 5000, driftWarnMs: 10000, driftSevereMs: 2000, referenceSource: ntp}
rules: []
`)
	if !hasProblem(problems, "clock_bands") {
		t.Errorf("severe below warn was accepted: %+v", problems)
	}
}

func TestMissingTimezoneIsRejected(t *testing.T) {
	// The host's timezone is never the site's answer.
	_, problems := compile(t, `
apiVersion: security.io/v1
kind: RuleBundle
version: 0.1.0
site: {siteId: s1, timezones: []}
settings:
  clock: {skewTolerance: 5000, driftWarnMs: 2000, driftSevereMs: 10000, referenceSource: ntp}
rules: []
`)
	if !hasProblem(problems, "site_timezones") {
		t.Errorf("a bundle with no site timezone was accepted: %+v", problems)
	}
}

// A rule referring to a zone that does not exist can never match. The check
// only runs when the compiler was given the site's zones, so lint without a
// database does not guess.
func TestUnknownZoneIsRejectedWhenKnown(t *testing.T) {
	c := rules.NewCompiler()
	c.KnownZones = map[string]bool{"zone-real": true}
	_, problems := c.Compile([]byte(wrap(`
  - id: a.b
    enabled: true
    priority: 10
    when: {zone: {id: zone-ghost}}
    then: {createIncident: true}
`)))
	if !hasProblem(problems, "unknown_zone") {
		t.Errorf("a rule naming a zone the site does not have was accepted: %+v", problems)
	}

	// Without site knowledge the check is skipped rather than guessed.
	_, blind := compile(t, wrap(`
  - id: a.b
    enabled: true
    priority: 10
    when: {zone: {id: zone-ghost}}
    then: {createIncident: true}
`))
	if hasProblem(blind, "unknown_zone") {
		t.Errorf("a zone was rejected without site knowledge: %+v", blind)
	}
}

// ---------------------------------------------------------------------------
// The plan's central AI invariant.
// ---------------------------------------------------------------------------

// A rule that consumes a Tier C signal must declare what it does without one.
// There is no default, because defaulting would make the decision on the
// operator's behalf at evaluation time, on a site that has never thought about it.
func TestConsumingAnAIObservationRequiresAnExplicitDegradation(t *testing.T) {
	cases := map[string]string{
		"requiresAIObservation": "            requiresAIObservation: true\n",
		"minEvidenceTier C":     "            minEvidenceTier: C\n",
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			_, problems := compile(t, wrap(`
  - id: a.b
    enabled: true
    priority: 10
    when:
      all:
        - eventType: [motion]
        - corroborate:
            eventTypes: [analytics]
            minSignals: 2
`+extra+`    then: {createIncident: true}
`))
			if !hasProblem(problems, "if_ai_unavailable_missing") {
				t.Errorf("a rule consuming an AI observation with no declared fallback "+
					"was accepted: %+v", problems)
			}
		})
	}
}

func TestAIDegradationMustBeOneOfTwoValues(t *testing.T) {
	_, problems := compile(t, wrap(`
  - id: a.b
    enabled: true
    priority: 10
    when:
      all:
        - eventType: [motion]
        - corroborate:
            eventTypes: [analytics]
            minSignals: 2
            requiresAIObservation: true
            ifAIAvailable: {degradeTo: treat_as_negative}
    then: {createIncident: true}
`))
	if !hasProblem(problems, "degrade_to") {
		t.Errorf("an unrecognised degradeTo was accepted: %+v", problems)
	}
}

func TestProceedWithoutWarns(t *testing.T) {
	// Not forbidden - an operator may want it - but it means incidents are
	// created on less evidence whenever the model is down, and that should be a
	// conscious choice rather than a silent one.
	_, problems := compile(t, wrap(`
  - id: a.b
    enabled: true
    priority: 10
    when:
      all:
        - eventType: [motion]
        - corroborate:
            eventTypes: [analytics]
            minSignals: 2
            requiresAIObservation: true
            ifAIAvailable: {degradeTo: proceed_without}
    then: {createIncident: true}
`))
	if !hasProblem(problems, "degrade_proceeds") {
		t.Errorf("proceed_without did not warn: %+v", problems)
	}
	if hasProblem(problems, "if_ai_unavailable_missing") {
		t.Errorf("proceed_without was not accepted: %+v", problems)
	}
}

func TestAIObservationFieldMustExistOnTheProfile(t *testing.T) {
	c := rules.NewCompiler()
	c.KnownAIFields = map[string]map[string]bool{
		"restricted-person": {"restrictedZoneBreach": true, "accessState": true},
	}
	_, problems := c.Compile([]byte(wrap(`
  - id: a.b
    enabled: true
    priority: 10
    when:
      aiObservation: {profileId: restricted-person, field: hallucinated, equals: true}
    then: {createIncident: true}
`)))
	if !hasProblem(problems, "ai_field_unknown") {
		t.Errorf("a field the profile does not emit was accepted: %+v", problems)
	}
}

// ---------------------------------------------------------------------------
// Deterministic conflict precedence.
// ---------------------------------------------------------------------------

// Same priority, incompatible outcomes: a configuration error. The engine has no
// authority to choose, so the bundle is refused.
func TestSamePriorityIncompatibleRulesAreAConflict(t *testing.T) {
	_, problems := compile(t, wrap(`
  - id: a.create
    enabled: true
    priority: 100
    when: {eventType: [motion]}
    then: {createIncident: true, severity: low}
  - id: a.suppress
    enabled: true
    priority: 100
    when: {eventType: [motion]}
    then: {createIncident: false, suppress: true}
`))
	if !hasProblem(problems, "conflict") {
		t.Errorf("a same-priority incompatible pair was accepted: %+v", problems)
	}
}

// Same priority, compatible outcomes: fine. Two rules that both create at the
// same severity agree, so there is nothing to choose between.
func TestSamePriorityCompatibleRulesAreNotAConflict(t *testing.T) {
	b, problems := compileRules(t, wrap(`
  - id: a.one
    enabled: true
    priority: 100
    when: {eventType: [motion]}
    then: {createIncident: true, severity: medium}
  - id: a.two
    enabled: true
    priority: 100
    when: {eventType: [motion]}
    then: {createIncident: true, severity: medium}
`))
	if hasProblem(problems, "conflict") {
		t.Errorf("compatible same-priority rules were reported as a conflict: %+v", problems)
	}
	if b == nil {
		t.Fatal("a compatible bundle failed to compile")
	}
}

// Different priorities: the higher one wins. That is a decision the author made
// explicitly, so it is not a conflict.
func TestDifferentPrioritiesAreResolvedNotConflicted(t *testing.T) {
	b, problems := compileRules(t, wrap(`
  - id: a.suppress
    enabled: true
    priority: 900
    when: {eventType: [motion]}
    then: {createIncident: false, suppress: true}
  - id: a.create
    enabled: true
    priority: 100
    when: {eventType: [motion]}
    then: {createIncident: true, severity: low}
`))
	if hasProblem(problems, "conflict") {
		t.Errorf("different priorities were reported as a conflict: %+v", problems)
	}
	if b == nil {
		t.Fatal("a bundle with distinct priorities failed to compile")
	}
	if len(b.Compiled) != 2 {
		t.Fatalf("compiled %d rules, want 2", len(b.Compiled))
	}
	if b.Compiled[0].ID != "a.suppress" {
		t.Errorf("rules are not in evaluation order: %s first, want a.suppress", b.Compiled[0].ID)
	}
}

// Evaluation order must be a total order, so a bundle's behaviour does not
// depend on the order its rules happen to be written in.
func TestEvaluationOrderIsStable(t *testing.T) {
	// The two priority-100 rules agree on severity, so the only thing deciding
	// their order is the id tiebreak. That is the property under test: a bundle
	// whose behaviour depends on the order its rules were written in cannot be
	// reviewed.
	forward, _ := compileRules(t, wrap(`
  - id: b.rule
    enabled: true
    priority: 100
    when: {eventType: [motion]}
    then: {createIncident: true, severity: medium}
  - id: a.rule
    enabled: true
    priority: 100
    when: {eventType: [motion]}
    then: {createIncident: true, severity: medium}
  - id: c.rule
    enabled: true
    priority: 500
    when: {eventType: [motion]}
    then: {createIncident: true, severity: medium}
`))
	reverse, _ := compileRules(t, wrap(`
  - id: c.rule
    enabled: true
    priority: 500
    when: {eventType: [motion]}
    then: {createIncident: true, severity: medium}
  - id: a.rule
    enabled: true
    priority: 100
    when: {eventType: [motion]}
    then: {createIncident: true, severity: medium}
  - id: b.rule
    enabled: true
    priority: 100
    when: {eventType: [motion]}
    then: {createIncident: true, severity: medium}
`))
	got := ids(forward)
	want := ids(reverse)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("rule order depends on how the bundle was written: %v vs %v", got, want)
	}
	if strings.Join(got, ",") != "c.rule,a.rule,b.rule" {
		t.Errorf("evaluation order is %v, want [c.rule a.rule b.rule]", got)
	}
}

func ids(b *rules.CompiledBundle) []string {
	out := make([]string, 0, len(b.Compiled))
	for _, r := range b.Compiled {
		out = append(out, r.ID)
	}
	return out
}

func hasProblem(problems []rules.Problem, code string) bool {
	for _, p := range problems {
		if p.Code == code {
			return true
		}
	}
	return false
}

// compileRules compiles and fails the test if the bundle produced errors, for
// cases where compilation is supposed to succeed and the point is what came out.
func compileRules(t *testing.T, src string) (*rules.CompiledBundle, []rules.Problem) {
	t.Helper()
	b, problems := compile(t, src)
	for _, p := range problems {
		if p.Severity == rules.SeverityError {
			t.Fatalf("bundle did not compile: %s", p)
		}
	}
	return b, problems
}
