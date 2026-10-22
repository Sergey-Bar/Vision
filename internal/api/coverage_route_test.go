package api_test

// The coverage panel, over a REAL database.
//
// The three defects this file's neighbours found were all on this one route,
// and all three were invisible to the suite:
//
//  1. the panel read the server's clock as a nil FIELD, and panicked;
//  2. the dead-letter read with an empty kind recursed for ever;
//  3. the clock-drift statement named a column (`sampled_at`) that has never
//     existed on `device_clocks`.
//
// Each one hid the next, because the panel reads its rows in order and the
// first row crashed before the second was reached. And all three were only
// reachable when the panel had something to report.
//
// So the test here is the one that would have caught all of them: it reads
// the ROUTE, over the real schema, with the real reader wired.
//
// A panel test that stubs the reader proves the SHAPE. A panel test over the
// real schema proves the STATEMENTS. Both are needed, and the second is the
// one that was missing.

import (
	"net/http"
	"testing"
)

func TestTheCoverageRouteReadsOverTheRealSchema(t *testing.T) {
	s, _, _ := server(t)

	rec := call(t, s, http.MethodGet, "/v1/coverage", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("read the coverage panel: got %d: %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	rows, _ := body["rows"].([]any)
	if len(rows) != 4 {
		t.Fatalf("the panel has %d row(s), want 4: %s", len(rows), rec.Body)
	}
	// Every row is MEASURED, even when it is zero. A row that is only
	// interesting when it is broken is a row a reader learns to skip.
	for _, r := range rows {
		row, _ := r.(map[string]any)
		if row["seen"] != true {
			t.Errorf("the %v row does not say it was measured: %v", row["kind"], row)
		}
	}
}

func TestTheCoverageRouteAnswersWhenTheSiteHasNothingToReport(t *testing.T) {
	// A healthy site is the case the panel is usually in, and it is the case
	// in which every one of the three defects was invisible.
	s, _, _ := server(t)

	rec := call(t, s, http.MethodGet, "/v1/coverage", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("read the coverage panel: got %d: %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	rows, _ := body["rows"].([]any)
	for _, r := range rows {
		row, _ := r.(map[string]any)
		if row["count"] != float64(0) {
			t.Errorf("the %v row counts %v on a site with nothing to report", row["kind"], row["count"])
		}
	}
}
