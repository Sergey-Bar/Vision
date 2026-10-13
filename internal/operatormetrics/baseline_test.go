package operatormetrics_test

// The C8 tests: the comparison against the operator's own VMS-only baseline,
// and the refusal that makes it mean anything.
//
// The plan's acceptance rule is the subject: a speedup measured against a
// different camera stream is not evidence. So the tests are two — the ratio
// computes when the stream is the operator's own, and the ratio is REFUSED when
// it is not — and the refusal is a result rather than an error, because "not
// comparable" is an answer an operator needs and not a fault.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Sergey-Bar/vision/internal/operatormetrics"
)

func store(t *testing.T, files map[string]string) operatormetrics.BaselineStore {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return operatormetrics.BaselineStore{Dir: dir}
}

const goodBaseline = `
version: security.io/mtti-baseline/v1
operator_id: operator-a
stream_ref: camera-18
site_id: site-1
from: "2026-05-01T00:00:00Z"
to: "2026-05-14T00:00:00Z"
median_mtti_seconds: 900
samples: 40
captured_by: operator-a@example.com
captured_at: "2026-05-15T09:00:00Z"
how: vms_operator_log
`

// TestTheSameStreamProducesARatio.
func TestTheSameStreamProducesARatio(t *testing.T) {
	s := store(t, map[string]string{"operator-a.yaml": goodBaseline})

	got, err := operatormetrics.CompareBaseline(s, "operator-a", "camera-18", 300)
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if !got.Comparable {
		t.Fatalf("the same operator and stream was reported not comparable: %s", got.Reason)
	}
	if got.Speedup != 3.0 {
		t.Errorf("the speedup is %v, want 3.0: 900 seconds against 300", got.Speedup)
	}
	if got.BaselineSamples != 40 {
		t.Errorf("the baseline's sample count is %d, want 40: a reader weighs the "+
			"ratio with the arithmetic the gates use", got.BaselineSamples)
	}
}

// TestADifferentStreamIsNotComparable.
//
// The plan's acceptance rule, as a test. The operator has a baseline on
// camera-18; the incident being measured is on camera-42. The comparison must
// refuse rather than borrow.
func TestADifferentStreamIsNotComparable(t *testing.T) {
	s := store(t, map[string]string{"operator-a.yaml": goodBaseline})

	got, err := operatormetrics.CompareBaseline(s, "operator-a", "camera-42", 300)
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if got.Comparable {
		t.Error("a different stream was compared. A speedup measured against a " +
			"different camera stream is not evidence")
	}
	if got.Speedup != 0 {
		t.Errorf("a refused comparison produced a speedup of %v; the emptiness is "+
			"the point", got.Speedup)
	}
	if got.Reason == "" {
		t.Error("the refusal carries no reason; an operator told only \"not " +
			"comparable\" cannot tell which of the two cases they are in")
	}
}

// TestAnotherOperatorsBaselineIsNotUsed.
//
// The comparison is per-operator: the point is the same person doing the same
// job. A fast operator's baseline would make a slow one look better than they
// were, which is the number this harness exists to refuse.
func TestAnotherOperatorsBaselineIsNotUsed(t *testing.T) {
	s := store(t, map[string]string{"operator-a.yaml": goodBaseline})

	got, err := operatormetrics.CompareBaseline(s, "operator-b", "camera-18", 300)
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if got.Comparable {
		t.Error("operator-b was measured against operator-a's baseline. The " +
			"comparison is per-operator because the point is the same person")
	}
}

// TestOurFigureMissingIsRefusedToo.
//
// A comparison needs both halves. Ours not yet measurable is a refusal rather
// than a division by zero.
func TestOurFigureMissingIsRefusedToo(t *testing.T) {
	s := store(t, map[string]string{"operator-a.yaml": goodBaseline})

	got, err := operatormetrics.CompareBaseline(s, "operator-a", "camera-18", 0)
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if got.Comparable || got.Speedup != 0 {
		t.Errorf("a missing figure produced %+v", got)
	}
	if got.BaselineSeconds != 900 {
		t.Error("the refusal still names the baseline it could not compare against; " +
			"a reader should see which half was missing")
	}
}

// TestAnInvalidBaselineRefusesTheWholeLoad.
//
// A directory with one bad baseline fails the load rather than skipping the
// bad file: a baseline that was skipped is a baseline nobody looks at again.
func TestAnInvalidBaselineRefusesTheWholeLoad(t *testing.T) {
	s := store(t, map[string]string{
		"operator-a.yaml": goodBaseline,
		"broken.yaml": `
version: security.io/mtti-baseline/v1
operator_id: operator-c
stream_ref: camera-9
median_mtti_seconds: 0
samples: 12
captured_by: operator-c@example.com
how: guesswork
`,
	})
	if _, err := s.Load(); err == nil {
		t.Fatal("a baseline with a zero median and an unknown method was loaded. " +
			"Every field the validator checks is a comparison that would " +
			"otherwise produce a number about nothing")
	}
}

// TestABaselineWindowThatEndsBeforeItStartsIsRefused.
func TestABaselineWindowThatEndsBeforeItStartsIsRefused(t *testing.T) {
	s := store(t, map[string]string{"reversed.yaml": `
version: security.io/mtti-baseline/v1
operator_id: operator-a
stream_ref: camera-18
from: "2026-05-14T00:00:00Z"
to: "2026-05-01T00:00:00Z"
median_mtti_seconds: 900
samples: 40
captured_by: operator-a@example.com
how: shift_report
`})
	if _, err := s.Load(); err == nil {
		t.Fatal("a window that ends before it starts was loaded")
	}
}
