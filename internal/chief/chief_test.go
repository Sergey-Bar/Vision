package chief_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/brief"
	"github.com/Sergey-Bar/vision/internal/chief"
)

var (
	// A fixed instant, so every test in this file renders the same bytes.
	now = time.Date(2026, 10, 21, 9, 30, 0, 0, time.UTC)
	// The cursor: the moment before the operator looked.
	then = time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
)

func anItem(id string, score int, created time.Time) chief.Item {
	return chief.Item{
		IncidentID:  id,
		SiteID:      "site-1",
		State:       "open",
		Severity:    "high",
		Title:       "Loading Bay 2 — restricted zone breach",
		RuleID:      "zone.breach/v1",
		CreatedAt:   created,
		ThreatScore: score,
		QueueScore:  score,
	}
}

func facts() chief.Facts {
	return chief.Facts{
		GeneratedAt: now,
		Since:       then,
		TenantID:    "tenant-1",
		SiteID:      "site-1",
		Attention: []chief.Item{
			anItem("inc-new", 90, now.Add(-2*time.Minute)),
			anItem("inc-old", 30, then.Add(-time.Hour)),
		},
		Brief: &brief.Brief{
			Schema:        brief.Schema,
			GeneratedAt:   now,
			Headline:      "two incidents",
			FleetDegraded: 1,
			Cameras: []brief.Camera{
				{ID: "CAM-3", State: "blind", Reason: "no frames for 6 minutes"},
			},
			Coverage: []brief.ZoneCoverage{
				{ZoneID: "zone-bay2", DeclaredCameras: []string{"CAM-3"}, DegradedCameras: []string{"CAM-3"}, UnderObserved: true},
			},
		},
		Autonomy:    chief.AutonomyBrief,
		HasBaseRate: false,
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	// The same facts must produce the same bytes, twice. A Chief that
	// differed between two renders of the same facts would be a sentence an
	// auditor could not rely on, and the whole package is a record.
	f := facts()
	first, err := json.Marshal(chief.Render(f))
	if err != nil {
		t.Fatalf("render the chief: %v", err)
	}
	f2 := facts()
	second, err := json.Marshal(chief.Render(f2))
	if err != nil {
		t.Fatalf("render the chief again: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("two renders of the same facts differ:\n first: %s\nsecond: %s", first, second)
	}
}

func TestTheHeadlineNamesWhatChangedAndWhatIsStillOpen(t *testing.T) {
	c := chief.Render(facts())
	head := c.Headline
	if !strings.Contains(head, "1 new incident") {
		t.Errorf("the headline does not say what changed. It reads: %q", head)
	}
	if !strings.Contains(head, "2026-10-21 08:00:00Z") {
		t.Errorf("the headline does not say SINCE when. A delta without its cursor is a claim about an unknown window. It reads: %q", head)
	}
	if !strings.Contains(head, "scored 90") {
		t.Errorf("the headline does not say which incident is the most urgent. It reads: %q", head)
	}
	if !strings.Contains(head, "camera is not watching") {
		t.Errorf("the headline does not say what the system saw that is wrong. It reads: %q", head)
	}
}

func TestAQuietDeltaStillSaysWhatIsStillOpen(t *testing.T) {
	// Nothing new since the cursor, and one incident still open. The
	// headline must say BOTH: "nothing new" alone reads as "nothing is
	// happening", which at 02:00 is a dangerous claim.
	f := facts()
	f.Since = now.Add(-time.Minute)
	c := chief.Render(f)
	head := c.Headline
	if !strings.Contains(head, "Nothing new") {
		t.Errorf("the headline does not say that nothing changed. It reads: %q", head)
	}
	if !strings.Contains(head, "2 incidents are still open") {
		t.Errorf("the headline does not say what is still waiting, so 'nothing new' reads as 'nothing is happening'. It reads: %q", head)
	}
}

func TestNoCursorSaysSoRatherThanClaimingNothingChanged(t *testing.T) {
	f := facts()
	f.Since = time.Time{}
	c := chief.Render(f)
	if c.Delta.Quiet {
		t.Fatalf("with no cursor the delta says it is quiet, so a caller could read it as 'nothing changed'. There was no window to measure, so nothing was measured to be still")
	}
	if c.Delta.Measured {
		t.Errorf("with no cursor the delta says it was measured. It was not")
	}
	if c.Delta.Note == "" {
		t.Errorf("with no cursor the delta says nothing about why. A delta that cannot be read as quiet must say what it is instead")
	}
	if !strings.Contains(c.Headline, "no cursor") {
		t.Errorf("the headline does not say there is no cursor: %q", c.Headline)
	}
	if !strings.Contains(c.Headline, "2 incidents") {
		t.Errorf("with no cursor the headline should report the whole picture. It reads: %q", c.Headline)
	}
}

func TestANarrationCitingAnUnknownFactIsRefused(t *testing.T) {
	// THE CONTROL. A narration is a sentence about the record. A sentence
	// about something the record does not contain is a fabrication, and a
	// fabrication reaching an operator is the one failure this product
	// exists to make impossible.
	f := facts()
	f.Narration = &chief.Narration{
		Text:            "A person was seen in the loading bay carrying a case.",
		Provider:        "anthropic",
		Model:           "claude-opus-4",
		ContractVersion: "investigator/v1",
		Temperature:     0.0,
		InputDigest:     "sha256:abc",
		Citations:       []string{"inc-new", "inc-NOT-RECORDED"},
	}
	c := chief.Render(f)
	if c.Narration != nil {
		t.Fatalf("a narration citing an unrecorded fact was accepted. The product would have shown an operator a sentence about something that never happened")
	}
	if c.Degradation == nil || c.Degradation.Reason != chief.DegradationNarrationUnattributed {
		t.Fatalf("the refusal is not named. A silent drop reads as 'the model had nothing to say', which is a different fact: %+v", c.Degradation)
	}
	if !strings.Contains(c.Headline, "degraded") {
		t.Errorf("the refusal is not in the headline, so the Chief is quieter than usual for a reason the operator cannot see. It reads: %q", c.Headline)
	}
}

func TestANarrationWithoutAttributionIsRefused(t *testing.T) {
	// A sentence with no provider, no model, no contract version, no
	// temperature or no input digest cannot be replayed, so it is not
	// something the product may show an operator.
	cases := map[string]func(n *chief.Narration){
		"no provider":     func(n *chief.Narration) { n.Provider = "" },
		"no model":        func(n *chief.Narration) { n.Model = "" },
		"no contract":     func(n *chief.Narration) { n.ContractVersion = "" },
		"no input digest": func(n *chief.Narration) { n.InputDigest = "" },
		"no text":         func(n *chief.Narration) { n.Text = "   " },
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			f := facts()
			f.Narration = &chief.Narration{
				Text:            "nothing is happening at the loading bay",
				Provider:        "anthropic",
				Model:           "claude-opus-4",
				ContractVersion: "investigator/v1",
				Temperature:     0.0,
				InputDigest:     "sha256:abc",
				Citations:       []string{"inc-new"},
			}
			breakIt(f.Narration)
			c := chief.Render(f)
			if c.Narration != nil {
				t.Fatalf("a narration with %s was accepted, so an operator was shown a sentence nobody could replay", name)
			}
		})
	}
}

func TestANarrationCitingOnlyRecordedFactsIsKept(t *testing.T) {
	f := facts()
	f.Narration = &chief.Narration{
		Text:            "Two incidents need you; the loading bay camera has been blind for six minutes.",
		Provider:        "anthropic",
		Model:           "claude-opus-4",
		ContractVersion: "investigator/v1",
		Temperature:     0.0,
		InputDigest:     "sha256:abc",
		Citations:       []string{"inc-new", "inc-old", "CAM-3"},
	}
	c := chief.Render(f)
	if c.Narration == nil {
		t.Fatalf("a narration citing only recorded facts was refused: %+v", c.Degradation)
	}
	if c.Degradation != nil {
		t.Errorf("a good narration still left a degradation on the chief: %+v", c.Degradation)
	}
}

func TestTheChiefSpeaksWithNoAIPlaneAtAll(t *testing.T) {
	// The AI plane's absence is a SUPPORTED state, not a failure. The
	// deterministic form must be complete on its own: facts, delta,
	// headline, attention.
	f := facts()
	f.Narration = nil
	c := chief.Render(f)
	if c.Narration != nil {
		t.Fatalf("there is a narration with no AI plane")
	}
	if c.Headline == "" {
		t.Fatalf("the Chief said nothing with no AI plane. The deterministic brief is the contract, not a consolation")
	}
	if len(c.Attention) != 2 {
		t.Errorf("the queue has %d item(s) with no AI plane, want 2", len(c.Attention))
	}
	if c.Brief == nil {
		t.Errorf("the brief was dropped with no AI plane. The brief is the record")
	}
}

func TestA2IsRefusedAtASiteWithNoLabelledHistory(t *testing.T) {
	// A2 means the Chief disposes of incidents against the site's own base
	// rate. A site with no labelled history has NO prior, so there is
	// nothing to dispose against, and offering A2 there would be the black
	// box this product exists to avoid.
	f := facts()
	f.Autonomy = chief.AutonomyAct
	f.HasBaseRate = false
	c := chief.Render(f)
	if c.EffectiveAutonomy != chief.AutonomyPropose {
		t.Fatalf("the effective autonomy is %q at a site with no labelled history, want %q", c.EffectiveAutonomy, chief.AutonomyPropose)
	}
	if c.Autonomy != chief.AutonomyAct {
		t.Errorf("the CONFIGURED autonomy was changed to %q. The configured level is a fact about the site and must be published as it is", c.Autonomy)
	}
}

func TestA2IsAllowedAtASiteWithALabelledHistory(t *testing.T) {
	f := facts()
	f.Autonomy = chief.AutonomyAct
	f.HasBaseRate = true
	c := chief.Render(f)
	if c.EffectiveAutonomy != chief.AutonomyAct {
		t.Fatalf("the effective autonomy is %q at a site with a labelled history, want %q", c.EffectiveAutonomy, chief.AutonomyAct)
	}
}

func TestAnUnknownAutonomyLevelIsTheMostConservativeOne(t *testing.T) {
	f := facts()
	f.Autonomy = chief.Autonomy("A9")
	c := chief.Render(f)
	if c.EffectiveAutonomy != chief.AutonomyBrief {
		t.Fatalf("an unknown autonomy level produced %q, want %q. A site whose configuration names a level this build does not know must be handled CONSERVATIVELY", c.EffectiveAutonomy, chief.AutonomyBrief)
	}
}

func TestTheQueueOrderIsTheProductsOrder(t *testing.T) {
	// The Chief must NOT re-sort the queue. A second sort is a second
	// judgement, made in a place that has never measured anything, and it is
	// the exact defect the attention queue was repaired for.
	f := facts()
	f.Attention = []chief.Item{
		anItem("inc-low", 10, now),
		anItem("inc-high", 90, now),
	}
	c := chief.Render(f)
	if c.Attention[0].IncidentID != "inc-low" {
		t.Fatalf("the Chief re-sorted the queue. The first item is %q. The product's order is the product's judgement and the Chief does not replace it", c.Attention[0].IncidentID)
	}
}

func TestTheDeltaMeasuresWhatChangedSinceTheCursor(t *testing.T) {
	f := facts()
	c := chief.Render(f)
	if len(c.Delta.NewIncidents) != 1 {
		t.Fatalf("the delta has %d new incident(s), want 1", len(c.Delta.NewIncidents))
	}
	if c.Delta.NewIncidents[0].IncidentID != "inc-new" {
		t.Fatalf("the delta's new incident is %q, want inc-new", c.Delta.NewIncidents[0].IncidentID)
	}
	if c.Delta.Quiet {
		t.Errorf("the delta says it is quiet while carrying %d new incident(s)", len(c.Delta.NewIncidents))
	}
}

func TestTheChiefCarriesNoBriefSaysSoByName(t *testing.T) {
	// A site with no hand-over artifact must not render an empty brief that
	// reads as "nothing happened".
	f := facts()
	f.Brief = nil
	c := chief.Render(f)
	if c.Degradation == nil || c.Degradation.Reason != chief.DegradationNoBrief {
		t.Fatalf("the missing brief is not named: %+v", c.Degradation)
	}
	if !strings.Contains(c.Headline, "degraded") {
		t.Errorf("the missing brief is not in the headline: %q", c.Headline)
	}
}

func TestFactsFromCarriesOnlyTheFactsItWasGiven(t *testing.T) {
	items := []chief.Item{anItem("inc-1", 50, now)}
	got := chief.FactsFrom(items, []string{"CAM-1"}, []string{"zone-1"}, []string{"item-1"})
	if len(got) != 4 {
		t.Fatalf("the fact list has %d entries, want 4: %+v", len(got), got)
	}
	seen := map[string]bool{}
	for _, f := range got {
		if f.ID == "" {
			t.Errorf("a fact carries no id, so nothing could cite it: %+v", f)
		}
		seen[f.ID] = true
	}
	for _, want := range []string{"inc-1", "CAM-1", "zone-1", "item-1"} {
		if !seen[want] {
			t.Errorf("the fact list does not carry %q", want)
		}
	}
}
