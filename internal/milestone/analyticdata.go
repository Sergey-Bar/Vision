package milestone

import (
	"encoding/json"
	"fmt"
	"strings"
)

// analyticObject is what the vendor's Events API asserts about an analytics
// event, read out of the envelope's `analyticData` subtree.
//
// It is a projection, not a copy. The vendor carries the object's `value` and
// `data` alongside the class fields, and `value` is where licence-plate text
// lives — `master-roadmap.md` M2 §"What the analytics actually gives": *"M2
// targets object class and counts, and LPR is an M3 item routed to the
// compliance track first, with a data-residency consequence, before any code."*
// So the plate-bearing fields are redacted at the ingest boundary
// (`redactAnalyticPII`) and never projected here. A filter that happened to
// exist is not a decision, and this one was made in the document before it was
// made in the code.
//
// What survives is what makes the M1 incident class deterministic: the vendor's
// own class name. That is the whole point of the exercise —
// *"`restricted-person` becomes a geometric test on a typed event —
// deterministic, no VLM, no GPU, no 8-second inference, and a geometric test
// cannot hallucinate a person who is not there."*
type analyticObject struct {
	// Vendor is which engine produced this — the VMS's analytics, not our model.
	// The provenance distinction the roadmap asks for: a decision whose reading
	// is "the VMS's own analytics asserted this" is a categorically different
	// thing from one that reads "our model said so".
	Vendor string
	// ClassID is the vendor's stable identifier for the class.
	ClassID string
	// ClassName is the site-visible name for the class.
	ClassName string
	// RuleName is the VMS-side rule that fired, when the feed names one.
	RuleName string
	// Confidence is the engine's own confidence, when it published one. It is
	// the engine's number about its own answer and is carried as provenance, not
	// used as a gate — the gate is the typed event, not a score.
	Confidence    float64
	HasConfidence bool
}

// analyticKeys are the spellings the analyticData subtree is read under. Four
// names because which one a given build uses is one of the unverified
// assumptions `securityctl adapter-probe` exists to settle, and a read that
// only handles the one the docs published is a read that returns nothing at a
// site that ships the other.
var (
	analyticKeys  = []string{"analyticData", "AnalyticData", "analytic_data"}
	objectKeys    = []string{"object", "Object"}
	valueKeys     = []string{"value", "Value"}
	vendorKeys    = []string{"vendor", "Vendor"}
	classIDKeys   = []string{"ClassId", "classId", "class_id", "ClassID"}
	classKeyNames = []string{"ClassName", "className", "class_name"}
	ruleNameKeys  = []string{"ruleName", "RuleName", "rule", "Rule"}
	confKeys      = []string{"Confidence", "confidence"}
)

// analyticRedactionMarker is what a redacted field becomes. It is deliberately
// the fixture package's marker rather than a new one: a reader who has learned
// to read one envelope's markers reads them all.
const analyticRedactionMarker = "[redacted]"

// allowedAnalyticPaths is the shape that survives the redaction, as
// dot-separated lowercased key paths under the analyticData subtree.
//
// It is an allowlist and not a denylist, for the same reason
// `internal/identity` is closed by default: the fields the platform actually
// consumes are four or five, the vendor's free-form payload is everything
// else, and *everything else* is where the plate lives. A denylist of known
// plate keys is a redaction that stops working the first time the vendor ships
// a new one — the vendor ships new fields as a matter of routine, which is the
// whole reason the route table carries `Unverified` flags.
//
// A site that spells a class differently, or carries it under a key this list
// does not name, degrades: the event keeps the generic analytics type it always
// had and the field is marked. The platform does not guess, and the event is
// not lost.
var allowedAnalyticPaths = map[string]bool{
	"vendor":                       true,
	"rulename":                     true,
	"object":                       true,
	"object.type":                  true,
	"object.value":                 true,
	"object.value.classid":         true,
	"object.value.classname":       true,
	"object.value.confidence":      true,
	"object.value.deviceobject":    true,
	"object.value.deviceobject.id": true,
}

// parseAnalyticObject reads the analytic object off an event item. The second
// return is false when the item carries no analyticData at all, which is the
// common case: Analytics is per-site configuration and is frequently not
// enabled, and the platform degrades to "VMS alarms only" when it is absent —
// invariant 3, already the design.
func parseAnalyticObject(item map[string]any) (analyticObject, bool) {
	analytic, ok := mapAt(item, analyticKeys)
	if !ok {
		return analyticObject{}, false
	}
	obj := analyticObject{
		Vendor:   stringField(analytic, vendorKeys...),
		RuleName: stringField(analytic, ruleNameKeys...),
	}
	if inner, ok := mapAt(analytic, objectKeys); ok {
		obj.Vendor = firstNonEmpty(obj.Vendor, stringField(inner, vendorKeys...))
		obj.RuleName = firstNonEmpty(obj.RuleName, stringField(inner, ruleNameKeys...))
		obj.ClassID = stringField(inner, classIDKeys...)
		obj.ClassName = stringField(inner, classKeyNames...)
		if conf, ok := numberField(inner, confKeys...); ok {
			obj.Confidence = conf
			obj.HasConfidence = true
		}
		if value, ok := mapAt(inner, valueKeys); ok {
			// The class fields live inside `value` in the shape the vendor
			// publishes and the live test server synthesises; the vendor and
			// rule names are read from both levels because which one carries
			// them is another of the unverified assumptions.
			obj.ClassID = firstNonEmpty(obj.ClassID, stringField(value, classIDKeys...))
			obj.ClassName = firstNonEmpty(obj.ClassName, stringField(value, classKeyNames...))
			obj.Vendor = firstNonEmpty(obj.Vendor, stringField(value, vendorKeys...))
			if conf, ok := numberField(value, confKeys...); ok {
				obj.Confidence = conf
				obj.HasConfidence = true
			}
		}
	}
	// A class name is the one field that makes this worth having. An analytic
	// object with no class is provenance about an assertion nobody can act on,
	// and is carried as the generic analytics event it always was rather than
	// refined on a class that isn't there.
	if strings.TrimSpace(obj.ClassName) == "" && strings.TrimSpace(obj.ClassID) == "" {
		return analyticObject{}, false
	}
	return obj, true
}

// analyticCanonicalClasses is the closed table from a vendor class name to a
// canonical event type.
//
// One entry, on purpose. `restricted-person` is the M1 incident class, and it
// is the one the roadmap names: *"two of its three classes are now cheap
// because `analyticData` made them deterministic rather than modelled."* A
// second class is a decision with a rule behind it, not a default — and a
// defaulted class is a fabricated fact wearing the vendor's authority, which is
// the exact failure mode `eventtypes.go` refuses for GUIDs.
var analyticCanonicalClasses = map[string]string{
	"restricted-person": "analytics.restricted_person",
}

// analyticCanonicalClass maps a parsed object's class onto a canonical event
// type. It returns false for a class the taxonomy does not name, and the caller
// keeps the generic analytics event it already had — the event is not lost and
// nothing is guessed.
func analyticCanonicalClass(obj analyticObject) (string, bool) {
	for _, candidate := range []string{obj.ClassName, obj.ClassID} {
		key := strings.ToLower(strings.TrimSpace(candidate))
		if key == "" {
			continue
		}
		if canonical, ok := analyticCanonicalClasses[key]; ok {
			return canonical, true
		}
	}
	return "", false
}

// redactAnalyticPII replaces everything under an item's analyticData subtree
// that is not on the allowlist, in place, before the envelope is re-encoded and
// stored.
//
// It runs here rather than in a projection because the envelope is stored
// verbatim in `source_events` and is the bytes a replay re-derives from: a
// projection that leaves the plate text in the stored envelope has redacted
// nothing. It runs before the marshal in `toRecord`, so the redacted bytes are
// what the inbox claims and what every later reader sees.
//
// The class fields survive. That is what makes the redaction safe to run on the
// replay path too: `RecordFromPayload` re-derives the same typed event from the
// redacted envelope weeks later, because the refinement reads the class name
// and not the value.
func redactAnalyticPII(item map[string]any) {
	analytic, ok := mapAt(item, analyticKeys)
	if !ok {
		return
	}
	redactNotAllowed(analytic, "")
}

// redactNotAllowed walks a subtree, replacing with the marker every key whose
// dotted path from analyticData is not on the allowlist. A container key that
// is allowed is descended into; one that is not is replaced whole, so an
// unknown map cannot smuggle a field past the check by nesting it deeper.
func redactNotAllowed(node map[string]any, prefix string) {
	for k, v := range node {
		path := strings.ToLower(k)
		if prefix != "" {
			path = prefix + "." + path
		}
		if !allowedAnalyticPaths[path] {
			node[k] = analyticRedactionMarker
			continue
		}
		if child, ok := v.(map[string]any); ok {
			redactNotAllowed(child, path)
		}
	}
}

// mapAt returns the first map found under any of the key spellings.
func mapAt(m map[string]any, keys []string) (map[string]any, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if child, ok := v.(map[string]any); ok {
				return child, true
			}
		}
	}
	return nil, false
}

// numberField reads the first numeric field found under the key spellings.
// JSON numbers arrive as float64 through encoding/json, and a number that
// arrived as a string is read too because a camera is an untrusted peer and
// this reader has already been surprised by less.
func numberField(m map[string]any, keys ...string) (float64, bool) {
	for _, k := range keys {
		switch v := m[k].(type) {
		case float64:
			return v, true
		case json.Number:
			f, err := v.Float64()
			return f, err == nil
		case string:
			var f float64
			if _, err := fmt.Sscanf(v, "%g", &f); err == nil {
				return f, true
			}
		}
	}
	return 0, false
}
