package airuntime_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/internal/airuntime"
	"github.com/Sergey-Bar/vision/internal/observations"
)

// The request's profile schema is the fourth place the investigate field set is
// declared, after the plane's INVESTIGATOR_SCHEMA and this registry's
// ProfileInvestigate. `internal/observations` already asserts the registry agrees
// with the plane; this asserts the schema the worker SENDS agrees with the
// registry, so the three cannot drift. A field the worker validates against that
// the engine does not emit is a plane-side refusal; a field it emits that the sent
// schema omits is a degraded answer. Both are what happened when the worker sent
// no schema at all, and this is what stops a copy from quietly drifting again.
func TestTheInvestigateSchemaAgreesWithTheRegistry(t *testing.T) {
	profile, ok := observations.M1Profiles[observations.ProfileInvestigate]
	if !ok {
		t.Fatal("the registry has no investigate profile to agree with")
	}

	props, ok := airuntime.InvestigatorProfileSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("InvestigatorProfileSchema has no properties dict: %#v",
			airuntime.InvestigatorProfileSchema["properties"])
	}
	var wireFields []string
	for f := range props {
		wireFields = append(wireFields, f)
	}
	var registryFields []string
	for f := range profile.Fields {
		registryFields = append(registryFields, f)
	}
	sort.Strings(wireFields)
	sort.Strings(registryFields)
	if strings.Join(wireFields, ",") != strings.Join(registryFields, ",") {
		t.Errorf("the schema the worker sends declares [%s] and the registry declares [%s]; a "+
			"mismatch is a plane-side refusal of an answer the engine was right to give",
			strings.Join(wireFields, ", "), strings.Join(registryFields, ", "))
	}

	raw, _ := airuntime.InvestigatorProfileSchema["required"].([]any)
	var wireRequired []string
	for _, r := range raw {
		wireRequired = append(wireRequired, r.(string))
	}
	sort.Strings(wireRequired)
	want := append([]string(nil), profile.Required...)
	sort.Strings(want)
	if strings.Join(wireRequired, ",") != strings.Join(want, ",") {
		t.Errorf("the schema the worker sends requires [%s] and the registry requires [%s]",
			strings.Join(wireRequired, ", "), strings.Join(want, ", "))
	}

	// Each declared field carries a JSON-schema type, because the plane validates
	// the answer against these and a typeless property is one it cannot check.
	for f, spec := range props {
		if _, ok := spec.(map[string]any)["type"]; !ok {
			t.Errorf("investigate field %q is declared without a JSON-schema type; the plane "+
				"cannot validate an answer against it", f)
		}
	}
}
