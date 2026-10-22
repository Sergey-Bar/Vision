package airuntime

import (
	"fmt"
	"sort"
	"strings"
)

// The answer's validator.
//
// An answer is EVIDENCE or it is not, and the difference is decided by the
// schema the profile declared. A model that answers in prose, or that names
// a field the schema does not declare, or that omits one the schema requires,
// has NOT answered — and the observation records that rather than storing
// something the product cannot vouch for.
//
// # Why it is here and not in the Python runtime
//
// The runtime validates the output of an ENGINE, and this validates the
// output of a HOSTED provider. They are two different trust boundaries, and a
// hosted provider is the one that needs validating most: its answer arrives
// as text from somewhere the product does not control, and the schema is the
// only thing standing between that text and the record.
//
// The rules are the same ones the runtime applies, and they are stated here
// rather than imported because they are about a JSON Schema's SMALLEST
// useful subset — the subset every profile in this product uses. A profile
// that needs more than this says so, and a validator that pretended to
// understand more than it does would be a validator that says "valid" about a
// shape it never checked.

// validateAgainstSchema checks an answer against a profile's schema.
//
// It returns "" when the answer holds, and a sentence naming what is wrong
// when it does not. The sentence is what the observation records as the
// degradation's detail, so it has to say WHICH field and WHY — a detail that
// says "invalid" is a detail the operator cannot act on.
func validateAgainstSchema(answer map[string]any, schema map[string]any) string {
	if len(schema) == 0 {
		return ""
	}
	props, _ := schema["properties"].(map[string]any)
	required := stringList(schema["required"])

	// A required field that is absent is the first thing to say: it is the
	// field the profile exists for.
	for _, name := range required {
		if _, present := answer[name]; !present {
			return fmt.Sprintf("the answer does not carry the required field %q", name)
		}
	}

	// A field the schema does not declare is the second thing to say: the
	// answer says something the profile does not have a slot for, and
	// storing it would be storing an unvouched-for field.
	if props != nil {
		extra := make([]string, 0)
		for name := range answer {
			if _, declared := props[name]; !declared {
				extra = append(extra, name)
			}
		}
		if len(extra) > 0 {
			sort.Strings(extra)
			return fmt.Sprintf("the answer carries the field(s) %s, which the profile's schema "+
				"does not declare", strings.Join(extra, ", "))
		}
	}

	// And the declared fields' own types, where the schema says one.
	for _, name := range sortedKeys(props) {
		value, present := answer[name]
		if !present {
			continue
		}
		declared, _ := props[name].(map[string]any)
		if declared == nil {
			continue
		}
		if problem := typeMatches(name, value, declared); problem != "" {
			return problem
		}
	}
	return ""
}

// typeMatches checks one field's type against the schema's declaration.
//
// It handles the types a profile actually declares: string, number, integer,
// boolean, array and object. A type it does not recognise is NOT a failure,
// because a validator that refused what it did not understand would refuse
// every schema written after it.
func typeMatches(name string, value any, declared map[string]any) string {
	want, _ := declared["type"].(string)
	if want == "" {
		return ""
	}
	switch want {
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Sprintf("the field %q is not a string", name)
		}
	case "number":
		switch value.(type) {
		case float64, float32, int, int32, int64:
		default:
			return fmt.Sprintf("the field %q is not a number", name)
		}
	case "integer":
		switch value.(type) {
		case int, int32, int64:
		default:
			// A JSON number arrives as a float64, and an integral float is
			// an integer. Anything else is not.
			if f, ok := value.(float64); !ok || f != float64(int64(f)) {
				return fmt.Sprintf("the field %q is not an integer", name)
			}
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Sprintf("the field %q is not a boolean", name)
		}
	case "array":
		if _, ok := value.([]any); !ok {
			return fmt.Sprintf("the field %q is not an array", name)
		}
	case "object":
		if _, ok := value.(map[string]any); !ok {
			return fmt.Sprintf("the field %q is not an object", name)
		}
	}
	return ""
}

// stringList reads a schema's `required` array.
func stringList(raw any) []string {
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// sortedKeys returns a map's keys in order.
//
// The order is the order the failure message lists them in, and a message
// that names the fields in a different order on two runs is a message nobody
// can compare.
func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
