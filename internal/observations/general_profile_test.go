package observations_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/observations"
)

// P4.3, the plan's "invariant-3 trap".
//
// A detector suppression or a cap stop means the model was **never asked**. The
// only correct record of that is a degraded observation with an explicit reason.
// The failure the plan names - and calls "the single worst failure this product
// has" - is recording it as "no problem detected", which fabricates a negative
// answer from a model that did not run.
//
// The control under test is in Validate: a degraded observation with no reason is
// refused. These tests exist so that removing the reason is a failing test rather
// than a passing one, and so that the direction of the failure is visible.

// timeFixture is any fixed instant; CreatedAt is checked for presence, not value.
var timeFixture = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

// readSchema reads a published schema from the repository's contracts tree.
func readSchema(rel string) ([]byte, error) {
	return os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(rel)))
}

// aCompleteObservation is one that validates, so each test below changes exactly
// one thing and any failure is about that thing.
func aCompleteObservation() observations.Observation {
	return observations.Observation{
		ObservationID: "obs-1", TenantID: "t1", SiteID: "s1",
		IncidentID: "inc-1", EventID: "evt-1",
		ProfileID:            "general-monitoring",
		Model:                "hafnia-8b",
		ModelIdentity:        "hafnia-8b",
		ModelContractVersion: "3f2a...",
		RuntimeVersion:       "hafnia/1",
		PromptTemplateID:     "hafnia-traffic",
		PromptVersion:        "v1",
		OutputSchemaVersion:  1,
		InputEvidenceHashes:  []string{"abc123"},
		StructuredOutput:     map[string]any{"insufficientEvidence": false, "summary": "a person crossed"},
		CreatedAt:            timeFixture,
	}
}

func markDegraded(o *observations.Observation, reason observations.Degradation, detail string) {
	o.Degraded = reason
	o.DegradedDetail = detail
	o.StructuredOutput = map[string]any{}
}

// TestADegradedObservationWithNoReasonIsRefused is the red-check itself.
//
// Remove the check in Validate and this is the only assertion in the tree that
// fails. Everything else still passes: the row is still attributed, still
// validated as an observation, and still stored - it is simply a row that says
// "degraded" and does not say why, and a consumer that branches on the reason
// gets the empty string.
func TestADegradedObservationWithNoReasonIsRefused(t *testing.T) {
	for _, reason := range []observations.Degradation{
		observations.DegradationNoRuntime,
		observations.DegradationModelUnavailable,
		observations.DegradationTimeout,
		observations.DegradationAbsent,
		observations.DegradationError,
	} {
		o := aCompleteObservation()
		markDegraded(&o, reason, "")
		err := o.Validate()
		if err == nil {
			t.Errorf("a %q degradation with no reason validated; the reason is the only thing "+
				"that says the model was never asked", reason)
			continue
		}
		if !strings.Contains(err.Error(), "never asked") {
			t.Errorf("the refusal %q does not explain what is lost by having no reason", err)
		}
	}
}

// TestANonDegradedObservationWithAReasonIsAlsoRefused is the mirror. A reason
// attached to a row that claims to be fine is either a field that was not cleared
// or a reason that was attached to the wrong row, and both are facts about the
// record rather than about the run.
func TestANonDegradedObservationWithAReasonIsAlsoRefused(t *testing.T) {
	o := aCompleteObservation()
	o.DegradedDetail = "the model was down"
	if err := o.Validate(); err == nil {
		t.Error("an observation that claims no degradation but carries a reason validated")
	}
}

// TestTheThreeAbsencesStayDistinguishable is the invariant stated positively: a
// degraded record, a failed record and an absent record are three facts, and the
// vocabulary has to carry all three.
func TestTheThreeAbsencesStayDistinguishable(t *testing.T) {
	if observations.DegradationNoRuntime == observations.DegradationAbsent {
		t.Error("'the model was never asked' and 'there was nothing to ask about' are the same string")
	}
	for _, d := range []observations.Degradation{
		observations.DegradationNoRuntime,
		observations.DegradationAbsent,
		observations.DegradationModelUnavailable,
	} {
		if !observations.Degradations[d] {
			t.Errorf("the vocabulary does not contain %q", d)
		}
	}
	if len(observations.Degradations) < 3 {
		t.Errorf("the vocabulary holds %d values; it cannot keep the three absences apart", len(observations.Degradations))
	}
}

// TestMarkDegradedLeavesInsufficientEvidenceTrue is the shape the writer
// produces, asserted so the two paths cannot drift.
func TestMarkDegradedLeavesInsufficientEvidenceTrue(t *testing.T) {
	o := aCompleteObservation()
	o.MarkDegraded(observations.DegradationNoRuntime, "the AI plane is not configured")
	if o.Degraded != observations.DegradationNoRuntime {
		t.Error("MarkDegraded did not mark it")
	}
	if !o.InsufficientEvidence {
		t.Error("a degraded observation is not marked insufficient; a consumer that forgets to " +
			"check the degradation will read an empty output as a negative answer")
	}
	if o.StructuredOutput == nil {
		t.Error("no output map; a nil output is a panic for a consumer that reads it")
	}
	if len(o.StructuredOutput) != 0 {
		t.Errorf("MarkDegraded wrote %v into the output of a run that produced no output", o.StructuredOutput)
	}
	// And it is writable: the shape the store accepts.
	observations.ApplyDegradedPlaceholders(&o)
	if err := o.Validate(); err != nil {
		t.Errorf("a properly marked degraded observation failed validation: %v", err)
	}
}

// TestTheGeneralProfileCarriesNoPerConceptFields is the product claim, asserted
// against the registry rather than asserted in a document.
//
// The plan's claim is "no rules to write per scenario - one general policy, not
// one rule per concept". A profile with a field per concept is a schema per
// concept, which is the rule engineering the work set out to eliminate. So the
// test is: the general profile's field set is small, and none of its fields names
// a detection concept.
func TestTheGeneralProfileCarriesNoPerConceptFields(t *testing.T) {
	p, ok := observations.M1Profiles["general-monitoring"]
	if !ok {
		t.Fatal("the general profile is not registered")
	}
	if !p.Shadow {
		t.Error("the general profile is not shadow; it has never been measured against the 200 labelled " +
			"cases the plan requires, and a profile allowed to create incidents before that has a " +
			"false-positive rate that is a guess")
	}
	// The concepts this profile must NOT have a field for. A field named after
	// any of these is a rule per concept wearing a schema.
	forbidden := []string{
		"intruder", "theft", "trespass", "perimeter", "weapon", "fire", "smoke",
		"flood", "abandoned", "loiter", "unauthorised", "unauthorized", "vehicle",
		"person", "package", "animal", "personCount", "vehicleCount",
	}
	for field := range p.Fields {
		lowered := strings.ToLower(field)
		for _, f := range forbidden {
			if strings.Contains(lowered, strings.ToLower(f)) {
				t.Errorf("the general profile has a field %q; a concept in the schema is a rule per concept", field)
			}
		}
	}
	// And the fields it does have are the ones the plan names, plus none.
	for _, want := range []string{
		"summary", "confidence", "insufficientEvidence", "recommendedAction",
		"entities", "hazardVisible", "abnormalEvent", "sceneDescription",
	} {
		if !p.Fields[want] {
			t.Errorf("the general profile is missing %q", want)
		}
	}
	if len(p.Fields) != 8 {
		t.Errorf("the general profile has %d fields; a general profile is small", len(p.Fields))
	}
}

// TestTheGeneralProfileFieldSetMatchesItsPublishedSchema, because a registry
// entry and a schema that disagree is a rule that compiles against fields no
// output will ever carry.
func TestTheGeneralProfileFieldSetMatchesItsPublishedSchema(t *testing.T) {
	raw, err := readSchema("contracts/json-schema/ai-observations/general-monitoring.schema.json")
	if err != nil {
		t.Fatalf("read the published schema: %v", err)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("parse the published schema: %v", err)
	}
	p := observations.M1Profiles["general-monitoring"]
	// schemaVersion and profileId are the envelope's own fields: they describe
	// the message rather than the answer, and no rule has ever referenced
	// "what version of the schema is this" - a rule that did would be checking
	// the plumbing instead of the scene.
	envelope := map[string]bool{"profileId": true, "schemaVersion": true}
	for name := range schema.Properties {
		if envelope[name] {
			continue
		}
		if !p.Fields[name] {
			t.Errorf("the published schema declares %q and the registry does not list it, so a rule "+
				"naming it would fail to compile against a field no output carries", name)
		}
	}
	for name := range p.Fields {
		if envelope[name] {
			continue
		}
		if _, ok := schema.Properties[name]; !ok {
			t.Errorf("the registry lists %q and the published schema does not declare it", name)
		}
	}
}

// TestTheGeneralProfileIsNotProduction, which is the same fact stated where a
// consumer checks it. IsProductionProfile is the function the incident path uses.
func TestTheGeneralProfileIsNotProduction(t *testing.T) {
	if observations.IsProductionProfile("general-monitoring") {
		t.Error("the general profile is treated as production; it has never been measured")
	}
	if !observations.IsProductionProfile("restricted-person") {
		t.Error("the one measured production profile is not production")
	}
}
