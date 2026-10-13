package observations_test

import (
	"testing"

	"github.com/Sergey-Bar/vision/internal/observations"
)

// TestTheInvestigatorProfileStartsShadow is the promotion rule from the plan
// stated as code: a new VLM profile starts shadow, and the function the
// incident path uses says so. The promotion itself is a human decision through
// evaluation.DefaultGates; nothing in the tree promotes anything.
func TestTheInvestigatorProfileStartsShadow(t *testing.T) {
	p, ok := observations.M1Profiles["investigate/v1"]
	if !ok {
		t.Fatal("the registry has no investigate profile; the investigator has no declared field set")
	}
	if !p.Shadow {
		t.Error("the investigator profile is not shadow; an unmeasured profile may not affect state")
	}
	if observations.IsProductionProfile("investigate/v1") {
		t.Error("IsProductionProfile says the investigator is production")
	}
	// The field set is the citation contract: an investigate answer is a
	// reading of the record plus the ids it read, and nothing about who, what
	// or whether something was allowed.
	for _, want := range []string{"answer", "citations", "confidence"} {
		if !p.Fields[want] {
			t.Errorf("the investigate profile does not declare %q", want)
		}
	}
	// And the one production profile is still production: adding a shadow
	// profile must not quietly demote the measured one.
	if !observations.IsProductionProfile("restricted-person") {
		t.Error("restricted-person stopped being production")
	}
}
