package api_test

// The Chief of Security, at the edge.
//
// The package tests live in `internal/chief` and cover the render. These
// tests cover the EDGE: the route's contract with the caller, the two
// readers it uses, the cursor it takes, and the three ways the AI plane can
// be absent — which are three DIFFERENT facts and must not collapse into
// one.

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/api"
	"github.com/Sergey-Bar/vision/internal/brief"
	"github.com/Sergey-Bar/vision/internal/chief"
)

// aNarrator is a narrator that answers with a fixed narration, and remembers
// what it was asked.
type aNarrator struct {
	answer    *chief.Narration
	err       error
	gotFacts  []chief.Fact
	gotHeader string
	calls     int
}

func (n *aNarrator) Narrate(ctx context.Context, req chief.NarrationRequest) (*chief.Narration, error) {
	n.calls++
	n.gotFacts = req.Facts
	n.gotHeader = req.Headline
	if n.err != nil {
		return nil, n.err
	}
	return n.answer, nil
}

// aPrior answers whether a site has a labelled history.
type aPrior struct {
	known bool
	err   error
}

func (p aPrior) SiteHasHistory(ctx context.Context, site string) (bool, error) {
	return p.known, p.err
}

// aHandover serves a fixed brief, so the Chief's facts half is exercised
// without a duty-log artifact on disk.
//
// The fixture is late-bound because the site the hand-over serves is the one
// the server was built with, and the server cannot be built until the
// dependency set is complete.
type handoverFixture struct {
	site string
	view api.HandoverView
}

type aHandover struct{ fx *handoverFixture }

func (h aHandover) SiteID() string { return h.fx.site }

func (h aHandover) Read(ctx context.Context, tenantID, siteID string) (api.HandoverView, error) {
	return h.fx.view, nil
}

func chiefServer(t *testing.T, narrator chief.Narrator, prior api.ChiefPrior, autonomy chief.Autonomy) (*api.Server, string, string) {
	t.Helper()
	fx := &handoverFixture{}
	s, tenant, site, _ := serverWithRegistry(t, nil, func(d *api.Deps) {
		d.ChiefNarrator = narrator
		d.ChiefPrior = prior
		d.ChiefAutonomy = autonomy
		d.Handover = aHandover{fx: fx}
	})
	fx.site = site
	fx.view = api.HandoverView{
		Brief: &brief.Brief{
			Schema:        brief.Schema,
			TenantID:      tenant,
			SiteID:        site,
			GeneratedAt:   clock,
			Headline:      "one incident",
			FleetDegraded: 1,
			Cameras:       []brief.Camera{{ID: "CAM-3", State: "blind"}},
			Coverage:      []brief.ZoneCoverage{{ZoneID: "zone-bay2", UnderObserved: true}},
		},
	}
	return s, tenant, site
}

func TestTheChiefSpeaksWithNoAIPlaneAtAll(t *testing.T) {
	// No narrator is configured. The Chief must still speak: the facts, the
	// delta and the queue are the record, and the narration is an
	// enhancement over it. A Chief that goes silent without a model would
	// make the product's best asset conditional on the thing it says it does
	// not depend on.
	s, tenant, site := chiefServer(t, nil, nil, chief.AutonomyBrief)
	seedScoredIncident(t, tenant, site, 90, "high")

	rec := call(t, s, http.MethodGet, "/v1/chief", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("read the chief: got %d: %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	if body["headline"] == "" {
		t.Fatalf("the Chief said nothing with no AI plane: %s", rec.Body)
	}
	if body["narration"] != nil {
		t.Errorf("there is a narration with no AI plane: %v", body["narration"])
	}
	if body["degradation"] != nil {
		t.Errorf("the Chief is degraded with no AI plane, which is a SUPPORTED mode: %v", body["degradation"])
	}
}

func TestTheChiefReportsWhatChangedSinceTheCursor(t *testing.T) {
	s, tenant, site := chiefServer(t, nil, nil, chief.AutonomyBrief)
	id := seedScoredIncident(t, tenant, site, 90, "high")

	// A cursor long before the incident: it is new.
	rec := call(t, s, http.MethodGet, "/v1/chief?since=2000-01-01T00:00:00Z", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("read the chief: got %d: %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	delta, _ := body["delta"].(map[string]any)
	newIncidents, _ := delta["newIncidents"].([]any)
	if len(newIncidents) != 1 {
		t.Fatalf("the delta carries %d new incident(s), want 1: %s", len(newIncidents), rec.Body)
	}
	first, _ := newIncidents[0].(map[string]any)
	if first["incidentId"] != id {
		t.Errorf("the delta's new incident is %v and the seeded one is %q", first["incidentId"], id)
	}
	if delta["measured"] != true {
		t.Errorf("the delta says it was not measured, but a cursor was given: %v", delta)
	}
}

func TestTheChiefWithNoCursorReportsTheWholePictureAndSaysSo(t *testing.T) {
	s, tenant, site := chiefServer(t, nil, nil, chief.AutonomyBrief)
	seedScoredIncident(t, tenant, site, 90, "high")

	rec := call(t, s, http.MethodGet, "/v1/chief", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("read the chief: got %d: %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	delta, _ := body["delta"].(map[string]any)
	if delta["measured"] != false {
		t.Errorf("with no cursor the delta says it was measured. It was not: %v", delta)
	}
	if delta["quiet"] == true {
		t.Errorf("with no cursor the delta says it is quiet, so a caller could read it as 'nothing changed': %v", delta)
	}
	if delta["note"] == nil || delta["note"] == "" {
		t.Errorf("with no cursor the delta does not say why: %v", delta)
	}
}

func TestAMalformedCursorIsRefusedWithTheReason(t *testing.T) {
	// A cursor is compared against timestamps. A cursor that is not a
	// timestamp cannot be compared against one, and parsing it as the zero
	// time would widen the window to everything — which is the one answer
	// this route must never give.
	s, _, _ := chiefServer(t, nil, nil, chief.AutonomyBrief)
	rec := call(t, s, http.MethodGet, "/v1/chief?since=yesterday", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a malformed cursor was accepted: got %d: %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	msg, _ := body["message"].(string)
	if !strings.Contains(msg, "RFC3339") {
		t.Errorf("the refusal does not say what a cursor must be: %q", msg)
	}
}

func TestANarrationCitingAnUnrecordedFactIsRefusedAtTheEdge(t *testing.T) {
	// THE CONTROL, at the boundary. The narrator is asked a question it was
	// given the facts for, and it answers about something the product never
	// recorded. The answer must not reach the operator, and the refusal must
	// be NAMED — a silent drop reads as "the model had nothing to say",
	// which is a different fact and a worse one.
	narrator := &aNarrator{answer: &chief.Narration{
		Text:            "A person was seen in the loading bay carrying a case.",
		Provider:        "anthropic",
		Model:           "claude-opus-4",
		ContractVersion: "investigator/v1",
		Temperature:     0.0,
		InputDigest:     "sha256:abc",
		Citations:       []string{"inc-NEVER-RECORDED"},
	}}
	s, tenant, site := chiefServer(t, narrator, nil, chief.AutonomyBrief)
	seedScoredIncident(t, tenant, site, 90, "high")

	rec := call(t, s, http.MethodGet, "/v1/chief", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("read the chief: got %d: %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	if body["narration"] != nil {
		t.Fatalf("a narration citing an unrecorded fact reached the operator: %v", body["narration"])
	}
	degradation, _ := body["degradation"].(map[string]any)
	if degradation == nil || degradation["reason"] != chief.DegradationNarrationUnattributed {
		t.Fatalf("the refusal is not named: %v", body["degradation"])
	}
	headline, _ := body["headline"].(string)
	if !strings.Contains(headline, "degraded") {
		t.Errorf("the refusal is not in the headline, so the Chief is quieter than usual for a reason the operator cannot see: %q", headline)
	}
}

func TestANarrationThePlaneCouldNotAnswerIsNamed(t *testing.T) {
	// The plane was ASKED and could not answer. That is a degradation, and
	// it is a DIFFERENT fact from there being no plane at all. Collapsing
	// them is the defect this product was built to stop committing.
	narrator := &aNarrator{err: context.DeadlineExceeded}
	s, _, _ := chiefServer(t, narrator, nil, chief.AutonomyBrief)

	rec := call(t, s, http.MethodGet, "/v1/chief", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("read the chief: got %d: %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	degradation, _ := body["degradation"].(map[string]any)
	if degradation == nil || degradation["reason"] != chief.DegradationNarrationUnavailable {
		t.Fatalf("the failed narration is not named: %v", body["degradation"])
	}
}

func TestTheNarratorIsGivenTheFactsAndTheHeadline(t *testing.T) {
	// The narration is an INPUT to the render, and the narrator is told the
	// closed set of facts it may talk about. A narrator that was not told
	// would have to guess what it may cite, and the check in the render would
	// then refuse good answers for the wrong reason.
	narrator := &aNarrator{answer: &chief.Narration{
		Text:            "One incident needs you.",
		Provider:        "anthropic",
		Model:           "claude-opus-4",
		ContractVersion: "investigator/v1",
		Temperature:     0.0,
		InputDigest:     "sha256:abc",
		Citations:       nil,
	}}
	s, tenant, site := chiefServer(t, narrator, nil, chief.AutonomyBrief)
	id := seedScoredIncident(t, tenant, site, 90, "high")

	rec := call(t, s, http.MethodGet, "/v1/chief", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("read the chief: got %d: %s", rec.Code, rec.Body)
	}
	if narrator.calls != 1 {
		t.Fatalf("the narrator was asked %d time(s), want 1", narrator.calls)
	}
	if narrator.gotHeader == "" {
		t.Errorf("the narrator was not given the headline to expand on")
	}
	found := false
	for _, f := range narrator.gotFacts {
		if f.ID == id {
			found = true
		}
	}
	if !found {
		t.Errorf("the narrator was not given the incident as a fact it may cite: %+v", narrator.gotFacts)
	}
}

func TestTheChiefDoesNotReSortTheQueue(t *testing.T) {
	// The product's order is the product's judgement. A second sort in the
	// Chief would be a second judgement, made in a place that has never
	// measured anything.
	s, tenant, site := chiefServer(t, nil, nil, chief.AutonomyBrief)
	first := seedScoredIncident(t, tenant, site, 90, "low")
	second := seedScoredIncident(t, tenant, site, 20, "critical")

	rec := call(t, s, http.MethodGet, "/v1/chief", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("read the chief: got %d: %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	items, _ := body["attention"].([]any)
	if len(items) != 2 {
		t.Fatalf("the Chief's queue has %d item(s), want 2: %s", len(items), rec.Body)
	}
	top, _ := items[0].(map[string]any)
	if top["incidentId"] != first {
		t.Fatalf("the Chief re-sorted the queue. Its first item is %v and the incident that "+
			"scored 90 is %q. The product's order is the product's judgement", top["incidentId"], first)
	}
	if second == first {
		t.Fatal("the two seeded incidents share an id, so the test is asserting nothing")
	}
}

func TestA2IsRefusedAtASiteWithNoLabelledHistory(t *testing.T) {
	// A2 means the Chief disposes of incidents against the site's own base
	// rate. A site with no labelled history has NO prior, so there is
	// nothing to dispose against, and offering A2 there would be the black
	// box this product exists to avoid.
	s, _, _ := chiefServer(t, nil, aPrior{known: false}, chief.AutonomyAct)

	rec := call(t, s, http.MethodGet, "/v1/chief", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("read the chief: got %d: %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	if body["effectiveAutonomy"] != string(chief.AutonomyPropose) {
		t.Fatalf("the effective autonomy is %v at a site with no labelled history, want %q",
			body["effectiveAutonomy"], chief.AutonomyPropose)
	}
	if body["autonomy"] != string(chief.AutonomyAct) {
		t.Errorf("the CONFIGURED autonomy was changed to %v. It is a fact about the site and must be published as it is", body["autonomy"])
	}
}

func TestA2IsAllowedAtASiteWithALabelledHistory(t *testing.T) {
	s, _, _ := chiefServer(t, nil, aPrior{known: true}, chief.AutonomyAct)

	rec := call(t, s, http.MethodGet, "/v1/chief", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("read the chief: got %d: %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	if body["effectiveAutonomy"] != string(chief.AutonomyAct) {
		t.Fatalf("the effective autonomy is %v at a site with a labelled history, want %q",
			body["effectiveAutonomy"], chief.AutonomyAct)
	}
}

func TestAMissingPriorReaderNeverGrantsAutonomy(t *testing.T) {
	// A missing reader is an absence, not a yes. If it were a yes, a site
	// with the reader uninstalled would be handed more autonomy than a site
	// that has one.
	s, _, _ := chiefServer(t, nil, nil, chief.AutonomyAct)

	rec := call(t, s, http.MethodGet, "/v1/chief", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("read the chief: got %d: %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	if body["effectiveAutonomy"] == string(chief.AutonomyAct) {
		t.Fatalf("a site with no prior reader was granted A2")
	}
}

func TestTheChiefDoesNotTakeASiteOrTenantFromTheRequest(t *testing.T) {
	// The scope is the principal's, resolved from the configuration. A header
	// or a query parameter naming a site would be an instruction, not an
	// authorisation.
	s, _, _ := chiefServer(t, nil, nil, chief.AutonomyBrief)
	rec := call(t, s, http.MethodGet, "/v1/chief?tenantId=someone-else&siteId=someone-else", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("read the chief: got %d: %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	if body["tenantId"] == "someone-else" {
		t.Errorf("the Chief took the tenant from the request: %v", body["tenantId"])
	}
	if body["siteId"] == "someone-else" {
		t.Errorf("the Chief took the site from the request: %v", body["siteId"])
	}
}

func TestTheChiefIsServedAtTheDocumentedPath(t *testing.T) {
	// The route is a published contract surface. A route that exists and is
	// not declared is a surface nobody can reach from a generated client.
	s, _, _ := chiefServer(t, nil, nil, chief.AutonomyBrief)
	rec := call(t, s, http.MethodGet, "/v1/chief", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("read the chief: got %d: %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	if body["schema"] != chief.Schema {
		t.Errorf("the Chief's schema is %v, want %q", body["schema"], chief.Schema)
	}
	if _, ok := body["generatedAt"].(string); !ok {
		t.Errorf("the Chief carries no generatedAt: %v", body["generatedAt"])
	}
	if _, ok := body["since"].(string); !ok {
		t.Errorf("the Chief carries no since: %v", body["since"])
	}
}

// The cursor is a URL, and a URL is durable. This test pins the one thing
// that makes it so: the route does not write the cursor anywhere.
//
// A cursor stored on the server would be a table of what each operator
// looked at, and the audit ledger would have to chain it. A cursor in browser
// storage would be a second reader of the record. The URL is the answer that
// has neither problem.
func TestTheCursorIsNotStoredAnywhere(t *testing.T) {
	s, _, _ := chiefServer(t, nil, nil, chief.AutonomyBrief)
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	rec := call(t, s, http.MethodGet, "/v1/chief?since="+when.Format(time.RFC3339), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("read the chief: got %d: %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	since, _ := body["since"].(string)
	if !strings.HasPrefix(since, "2026-01-02T03:04:05") {
		t.Errorf("the echoed cursor is %q, want the one that was asked for", since)
	}
}
