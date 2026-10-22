package api_test

// The attention producer.
//
// `attention_minutes` is the North Star numerator and until this pass it had no
// producer at all: nothing recorded when an operator STARTED paying attention.
// The dispositions table records when they finished, the incidents table records
// when the incident appeared, and the interval between them ÃƒÂ¢Ã¢â€šÂ¬Ã¢â‚¬Â the human minutes
// the product exists to reduce ÃƒÂ¢Ã¢â€šÂ¬Ã¢â‚¬Â existed in no table anywhere.
//
// These tests are the ones that would have caught the gap, and the two shapes
// they assert are the two ways the producer can quietly go wrong later:
//
//   - not written at all, which is the defect as it was;
//   - written on EVERY read instead of the first, which turns the metric into a
//     count of page refreshes and rewards an operator for spamming them.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/api"
	"github.com/Sergey-Bar/vision/internal/incidents"
	"github.com/Sergey-Bar/vision/internal/rules"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

// seedScoredIncident raises one incident with a chosen score and severity.
//
// The score and the severity are the two things the queue's order used to be
// decided by, and the test needs to be able to set them independently.
func seedScoredIncident(t *testing.T, tenant, site string, score int, severity string) string {
	t.Helper()
	id, err := (&incidents.Service{Pool: adminPool(t)}).CreateIncident(ctx(), incidents.Create{
		TenantID: tenant, SiteID: site,
		Decision: rules.Decision{
			State: rules.StateCorroborated, RuleID: "core.camera-offline",
			Severity: severity, Reason: "scored " + severity,
			Action:           rules.Action{CreateIncident: true, Severity: severity},
			SupportingEvents: []string{"evt-1"},
			DegradedReason:   rules.DegradedNone,
		},
		RuleBundleVersion: "0.1.0",
		BundleContentHash: strings.Repeat("d", 64),
		PolicyVersion:     "p1",
		Title:             "Camera offline",
		ThreatScore:       score,
	})
	if err != nil {
		t.Fatalf("seed the incident: %v", err)
	}
	return id
}

// TestTheAttentionQueueIsOrderedByTheProductsJudgement
//
// The queue is ordered by the PRODUCT'S score.
//
// It used to be ordered by severity: the API re-sorted the queue after the
// product had ordered it, so the queue an operator read was a severity list
// with the product's judgement thrown away. The score is the autonomy
// layer's answer to "which of these needs a person", and the severity is the
// rule's CLAIM -- how bad the incident would be if it were real. The claim is
// not the answer, and the answer is the only number anybody has ever
// measured.
//
// The two incidents here are seeded so that the two orderings disagree: the
// higher-scoring one has the LOWER severity. A queue ordered by the score
// puts it first. A queue ordered by the severity puts the other one first,
// which is the defect.
//
// The severity is a tie-break INSIDE the score, and there is a test for that
// below.
func TestTheAttentionQueueIsOrderedByTheProductsJudgement(t *testing.T) {
	s, tenant, site := server(t)
	first := seedScoredIncident(t, tenant, site, 90, "low")
	second := seedScoredIncident(t, tenant, site, 20, "critical")

	rec := call(t, s, http.MethodGet, "/v1/attention", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("read the attention queue: got %d: %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	items, _ := body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("the queue has %d item(s), want 2: %s", len(items), rec.Body)
	}
	top, _ := items[0].(map[string]any)
	if top["incidentId"] != first {
		t.Errorf("the queue's first item is %v and the incident that scored 90 is %v. "+
			"The queue is ordered by severity and not by the product's score, so the "+
			"autonomy layer's judgement is thrown away and the operator reads a list of "+
			"the rule's claims instead", top["incidentId"], first)
	}
	if second == first {
		t.Fatal("the two seeded incidents share an id, so the test is asserting nothing")
	}
}

// TestTheSeverityBreaksATieInsideTheScore
//
// Two incidents with the SAME score are ordered by the severity.
//
// The severity did not stop mattering when it stopped being the order. It
// became what it always should have been: the second key. Two incidents the
// product scored alike are not equally urgent, and the rule's own severity is
// the best available answer to which is which.
func TestTheSeverityBreaksATieInsideTheScore(t *testing.T) {
	s, tenant, site := server(t)
	first := seedScoredIncident(t, tenant, site, 50, "critical")
	seedScoredIncident(t, tenant, site, 50, "low")

	rec := call(t, s, http.MethodGet, "/v1/attention", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("read the attention queue: got %d: %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	items, _ := body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("the queue has %d item(s), want 2: %s", len(items), rec.Body)
	}
	top, _ := items[0].(map[string]any)
	if top["incidentId"] != first {
		t.Errorf("the queue's first item is %v and the critical one is %v: two incidents "+
			"with the same score are not ordered by the severity", top["incidentId"], first)
	}
}

// openedRows counts the incident.opened rows for one incident.
func openedRows(t *testing.T, tenant, incidentID string) int {
	t.Helper()
	var n int
	if err := adminPool(t).For(tenant, "audit_ledger").QueryRow(ctx(), `
		SELECT count(*) FROM audit_ledger
		 WHERE action = 'incident.opened' AND target_id = $1`, incidentID).Scan(&n); err != nil {
		t.Fatalf("count opened rows: %v", err)
	}
	return n
}

// readIncident reads one incident detail page.
//
// There is no session to carry: the server answers under the CONFIGURED
// principal, whose session is part of its configuration and does not move
// between two reads. That is what makes two reads in one test one session's
// two reads -- which is the property the idempotency test below is about.
//
// It used to take a session cookie and return the ROTATED one, because
// `identity.Resolve` rotates the session token on every call so a stolen
// cookie cannot be replayed. There is no session to steal and nothing to
// rotate, so the rotation is gone with the cookie.
func readIncident(t *testing.T, s *api.Server, incidentID string) int {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/incidents/"+incidentID, nil)
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Logf("read %s: %d %s", incidentID, rec.Code, rec.Body.String())
	}
	return rec.Code
}

// The session the configured principal acts in.
//
// The attention metric counts one bout of attention per session, and the
// session is the CONFIGURED principal's. A test that wants a second operator
// varies this, because a second operator is a second session and the metric
// has always meant that.
func actAs(s *api.Server, session string) {
	s.PrincipalSession = session
}

func TestTheFirstReadOfAnIncidentIsAudited(t *testing.T) {
	s, tenant, site := server(t)
	incidentID := seedIncident(t, tenant, site)

	if code := readIncident(t, s, incidentID); code != http.StatusOK {
		t.Fatalf("read the incident: got %d, want 200", code)
	}

	if n := openedRows(t, tenant, incidentID); n != 1 {
		t.Errorf("%d incident.opened row(s) after the first read, want 1. "+
			"The row IS the producer for attention_minutes, and without it the North Star "+
			"numerator has no source: it is not measurable at any window length", n)
	}
}

func TestARefreshIsNotANewBoutOfAttention(t *testing.T) {
	s, tenant, site := server(t)
	incidentID := seedIncident(t, tenant, site)

	for i := 0; i < 3; i++ {
		if code := readIncident(t, s, incidentID); code != http.StatusOK {
			t.Fatalf("read %d: got %d, want 200", i, code)
		}
	}

	// One session, however many reads: the metric would otherwise be a refresh
	// counter, and a refresh counter rewards an operator for pressing F5.
	if n := openedRows(t, tenant, incidentID); n != 1 {
		t.Errorf("%d incident.opened row(s) after three reads in ONE session, want 1", n)
	}
}

// A second operator opening the same incident is a second bout of attention, and
// attention_minutes is the quotient the cap and the North Star are both computed
// over. Collapsing two operators into one row would under-count every incident
// two people looked at, which is the shape most incidents have.
//
// A second operator is a second SESSION, which is now a configuration value
// rather than a cookie: the session the configured principal acts in is part of
// the principal, and the metric deduplicates on it exactly as it did on the
// cookie's session before.
func TestASecondOperatorSeesASecondRow(t *testing.T) {
	s, tenant, site := server(t)
	incidentID := seedIncident(t, tenant, site)

	for _, session := range []string{"operator-one", "operator-two"} {
		actAs(s, session)
		if code := readIncident(t, s, incidentID); code != http.StatusOK {
			t.Fatalf("read as %s: got %d, want 200", session, code)
		}
	}

	if n := openedRows(t, tenant, incidentID); n != 2 {
		t.Errorf("%d incident.opened row(s) after two sessions opened it, want 2: one row "+
			"per operator, or a second operator's attention is not counted at all", n)
	}
}

// The row must reach the ledger in the read path, and it must name the session and
// the timestamp the way every other audited action does. An attention interval is
// a subtraction, so a row with no timestamp or no session measures nothing.
func TestTheOpenedRowNamesTheSessionAndTheInstant(t *testing.T) {
	s, tenant, site := server(t)
	incidentID := seedIncident(t, tenant, site)

	if code := readIncident(t, s, incidentID); code != http.StatusOK {
		t.Fatalf("read: got %d, want 200", code)
	}

	var actor, actorType, action, target, sessionID string
	var ts time.Time
	err := adminPool(t).For(tenant, "audit_ledger").QueryRow(ctx(), `
		SELECT actor, actor_type, action, target_type, session_id, ts FROM audit_ledger
		 WHERE action = 'incident.opened' AND target_id = $1`, incidentID).
		Scan(&actor, &actorType, &action, &target, &sessionID, &ts)
	if err != nil {
		t.Fatalf("read the row: %v", err)
	}
	if actor == "" || actorType != "operator" {
		t.Errorf("the row names actor %q as %q, want a named operator", actor, actorType)
	}
	if action != "incident.opened" || target != "incident" {
		t.Errorf("the row says %q on %q, want incident.opened on incident", action, target)
	}
	if sessionID == "" {
		t.Error("the row names no session, so a second read from the same session looks " +
			"like a new bout of attention and attention_minutes counts refreshes")
	}
	if ts.IsZero() {
		t.Error("the row carries no timestamp, so there is no interval to measure from")
	}
}

// The read is refused with 404 for an incident at another site, and no row is
// written for it. Attention on an incident the operator may not see is not a
// metric: it is a record that this id exists, written to the ledger.
//
// The principal is assigned to ONE site, so the other site is out of scope and
// the read is refused. The test used to assert the opposite -- a 200 and a row
// -- because an unscoped principal saw everything, and "unscoped sees
// everything" was the reading the field's own comment contradicted.
func TestNoRowIsWrittenForAnIncidentAtAnotherSite(t *testing.T) {
	s, tenant, _ := server(t)
	siteB := "site-not-mine"
	if _, err := testutil.Pool(t).Exec(ctx(),
		`INSERT INTO sites (site_id, tenant_id, name, timezone) VALUES ($1, $2, 'Other', 'UTC')`,
		siteB, tenant); err != nil {
		t.Fatalf("create the second site: %v", err)
	}
	theirs := seedIncident(t, tenant, siteB)

	rec := callAs(t, s, http.MethodGet,
		"/v1/incidents/"+theirs, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("read an incident at an unassigned site: got %d, want 404: %s",
			rec.Code, rec.Body.String())
	}
	if n := openedRows(t, tenant, theirs); n != 0 {
		t.Errorf("%d incident.opened row(s) after a refused read, want 0: attention on an "+
			"incident the operator may not see is a record that the id exists", n)
	}
}
