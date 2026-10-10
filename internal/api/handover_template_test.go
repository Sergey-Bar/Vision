package api

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/brief"
	"github.com/Sergey-Bar/Ai-Captain/internal/dutylog"
)

// The hand-over page is the console's inheritance of the same packages the POC
// writes with, so its test renders the console's own templates over a brief the
// same code produced — not a fixture written to look like one. If the template
// and the brief disagree, the console stops showing the shift's record, which
// is the failure this test exists for.

func renderHandover(t *testing.T, data consoleData) string {
	t.Helper()
	var buf bytes.Buffer
	if err := consoleTemplates.ExecuteTemplate(&buf, "handover.html", data); err != nil {
		t.Fatalf("render the hand-over page: %v", err)
	}
	return buf.String()
}

func handoverFixture() (consoleData, *brief.Brief) {
	b := brief.Render(brief.Facts{
		Shift: "2026-10-10-night", TenantID: "tenant-1", SiteID: "site-1",
		GeneratedAt: time.Date(2026, 10, 11, 6, 0, 0, 0, time.UTC),
		Incidents: []brief.Incident{{
			ID: "inc-bbb", Title: "Dwell, restricted-1", CameraIDs: []string{"cam-01", "cam-02"},
			Anomalies: []string{"dwell"}, ThreatScore: 62, Action: "VERIFY", AutonomyState: "VERIFYING",
			Reason: "42s dwell in restricted-1, corroborated by two cameras", State: "ACTIVE",
		}},
		Actions: []brief.Action{{
			TS: time.Date(2026, 10, 10, 23, 5, 0, 0, time.UTC), Actor: "autonomy", Subject: "inc-bbb",
			Action: "VERIFY/VERIFYING", Reason: "score 62",
		}},
		Cameras: []brief.Camera{
			{ID: "cam-01", State: "fresh"},
			{ID: "cam-02", State: "blind", Reason: "near-uniform frame (lens covered, blackout or white-out)"},
		},
		Coverage: []brief.ZoneCoverage{{
			ZoneID: "restricted-1", DeclaredCameras: []string{"cam-01", "cam-02"},
			DegradedCameras: []string{"cam-02"}, UnderObserved: true,
		}},
	})
	data := consoleData{
		Title: "Shift hand-over",
		Now:   b.GeneratedAt,
		Brief: &b,
		DutyLog: &DutyLogState{
			Path:  "/data/dutylog/dutylog-site-1-2026-10-10-night.jsonl",
			Shift: "2026-10-10-night", SiteID: "site-1", Lines: 3,
			HeadHash:     strings.Repeat("a", 64),
			Verification: "ok",
			Dump: dutylog.Dump([]dutylog.Event{{
				TS: b.GeneratedAt, Kind: dutylog.KindTransition, Actor: "autonomy", Subject: "inc-bbb",
				Action: "VERIFY/VERIFYING", Reason: "score 62",
			}}),
		},
	}
	return data, &b
}

// TestTheHandoverPageRendersTheBriefAndTheArtifact is the console's version of
// the brief's golden test: the same facts render the same sentences, and the
// artifact's chain state is on the page.
func TestTheHandoverPageRendersTheBriefAndTheArtifact(t *testing.T) {
	data, b := handoverFixture()
	body := renderHandover(t, data)

	for _, want := range []string{
		b.Headline,
		"the chain verifies",
		data.DutyLog.Path,
		"cam-02",
		"near-uniform frame (lens covered, blackout or white-out)",
		"restricted-1",
		"under-observed",
		"inc-bbb",
		"dutylog", // the dump's rendering of the transition line
		"not a summary written by a",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the hand-over page does not render %q.\nA console that answers 200 and omits "+
				"the shift's record is the failure this test exists for", want)
		}
	}
}

// TestTheHandoverPageSaysWhyItIsEmpty: a site with no artifact configured says
// so by name, because an empty hand-over reads as "nothing happened".
func TestTheHandoverPageSaysWhyItIsEmpty(t *testing.T) {
	body := renderHandover(t, consoleData{Title: "Shift hand-over",
		DutyLogErr: "The hand-over artifact is not configured for this site."})
	if !strings.Contains(body, "not configured for this site") {
		t.Error("the page does not name why it is empty")
	}
	if strings.Contains(body, "no intrusions detected") {
		t.Error("the page renders the sentence this product must never render")
	}
	// And with a brief but no duty log, the half that exists still renders.
	_, b := handoverFixture()
	body = renderHandover(t, consoleData{Title: "Shift hand-over", Brief: b,
		DutyLogErr: "The duty log could not be read: permission denied"})
	if !strings.Contains(body, b.Headline) {
		t.Error("the brief half of a half-configured hand-over did not render")
	}
	if !strings.Contains(body, "permission denied") {
		t.Error("the missing half is not named")
	}
}
