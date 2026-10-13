package operatormetrics

// The wire shape of the operator report, and the control that keeps it one
// language.
//
// The bug this file exists to prevent: `efficiencyReport` in
// `cmd/securityctl/efficiency.go` carries JSON tags on its own fields, so the
// top of the document is snake_case — `mt_t_i`... `mt_t_i`, `verification_states`,
// `silence_by_site_and_class`. The structs BEHIND those fields carried none, so
// Go marshalled them under their Go names: `Incidents`, `CappedSeconds`,
// `ByState`, `Cells`, `MedianMTTI`.
//
// The result was a machine-readable report with two naming conventions in one
// document, and the half a console reads was the half it could not find. A page
// reading `attention.incidents` received `undefined`, rendered the honest
// "nothing measured" empty state, and looked correct — which is the failure mode
// this repository keeps re-learning: the claim about the wire was true and the
// wire was not.
//
// So the assertion here is not about one struct. It walks the ENTIRE emitted
// document — through slices, maps and nested structs — and fails on the first key
// that is not snake_case. Adding a report type tomorrow without tags is now a red
// test rather than a console that quietly renders nothing.

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"
)

// snakeKey is the wire convention: lower-case words joined by underscores, which
// is what every field of `efficiencyReport` already uses and what
// `docs/roadmap/engineering/metrics-and-analytics.md` writes.
var snakeKey = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)

// TestTheWholeReportSpeaksOneJSONLanguage is the control.
//
// Because the types under test are marshalled from `cmd/securityctl`, the
// assertion here is on the structs this package EXPORTS, walked with the same
// reflection Go uses to marshal. Go field names reach JSON unchanged unless a
// tag says otherwise, so a field whose tag is missing is visible as a name with
// an upper-case letter — and that is the difference the test looks for.
func TestTheWholeReportSpeaksOneJSONLanguage(t *testing.T) {
	docs := map[string]any{
		"attention":       AttentionReport{Incidents: []IncidentAttention{{}}, Samples: 1},
		"mt_t_i":          LatencyReport{Classes: []LatencyClassAggregate{{}}},
		"mt_t_v":          MTTVReport{Samples: 1},
		"verification":    StateCounts{ByState: map[string]int64{"duplicate": 1}},
		"silence_by_site": StateSliceReport{Cells: []StateSlice{{ByState: map[string]int64{"duplicate": 1}}}},
	}
	for name, doc := range docs {
		blob, err := json.Marshal(doc)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		var tree map[string]any
		if err := json.Unmarshal(blob, &tree); err != nil {
			t.Fatalf("%s did not marshal to an object: %v", name, err)
		}
		walk(t, name, tree)
	}
}

// walk descends a decoded JSON document and applies the convention to every key
// at every depth.
//
// It is written over DECODED JSON rather than over the Go types because the
// failure it is looking for is a key that reached the wire with a capital letter,
// and the only place to see that is the wire.
func walk(t *testing.T, path string, node any) {
	t.Helper()
	switch v := node.(type) {
	case map[string]any:
		for key, child := range v {
			if !snakeKey.MatchString(key) {
				t.Errorf("%s.%s: %q is not snake_case. A struct field with no JSON tag reaches the wire under its Go name, and the half of the document a console reads is then the half it cannot find",
					path, key, key)
				continue
			}
			walk(t, path+"."+key, child)
		}
	case []any:
		for i, child := range v {
			walk(t, path+"["+strings.TrimSpace(itoa(i))+"]", child)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// TestTimestampsCarryLayoutsAndNotNames is the second half: the time fields are
// the only ones whose rendering a reader interprets.
//
// Every timestamp in this report is a moment in UTC from Postgres, and Go
// marshals a time.Time as RFC3339 with an offset. A consumer that re-labels it
// with the reader's own timezone names a moment in two places at once, which is
// the reason the console prints "UTC" beside every clock it renders.
func TestTimestampsCarryLayoutsAndNotNames(t *testing.T) {
	rep := AttentionReport{From: time.Date(2026, 10, 12, 9, 30, 0, 0, time.UTC)}
	blob, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var tree map[string]any
	if err := json.Unmarshal(blob, &tree); err != nil {
		t.Fatalf("decode: %v", err)
	}
	from, ok := tree["from"].(string)
	if !ok {
		t.Fatalf("from is %T, want a string; a timestamp that is not a string is a timestamp a console renders however it likes", tree["from"])
	}
	if !strings.HasSuffix(from, "Z") {
		t.Fatalf("from is %q, want a UTC offset. A moment without one is read in the reader's timezone and named in two places at once", from)
	}
}
