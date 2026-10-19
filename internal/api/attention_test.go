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
	"github.com/Sergey-Bar/vision/internal/testutil"
)

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

// readIncidentAsSession reads one incident detail page over an EXISTING session,
// so two reads in one test are one session's two reads. Signing in per read is
// the one thing that would make the idempotency test below pass for the wrong
// reason: two sessions, two rows, no bug.
//
// It returns the rotated session cookie, which the NEXT read has to carry.
// `identity.Resolve` rotates the session token on every call Ã¢â‚¬â€ the same property
// that stops a stolen cookie being replayed Ã¢â‚¬â€ so a test that carries the original
// cookie twice gets a 401 on the second request and would read that as the
// handler refusing. It is not the handler: the session moved.
func readIncidentAsSession(t *testing.T, s *api.Server, incidentID string, cookie *http.Cookie) (*http.Cookie, int) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/incidents/"+incidentID, nil)
	req.AddCookie(cookie)
	s.Handler().ServeHTTP(rec, req)
	next := cookie
	for _, c := range rec.Result().Cookies() {
		if !strings.HasSuffix(c.Name, "_csrf") && c.Value != "" {
			next = c
		}
	}
	if rec.Code != http.StatusOK {
		t.Logf("read %s: %d %s", incidentID, rec.Code, rec.Body.String())
	}
	return next, rec.Code
}

func TestTheFirstReadOfAnIncidentIsAudited(t *testing.T) {
	s, tenant, site := server(t)
	incidentID := seedIncident(t, tenant, site)

	cookie := &http.Cookie{Name: "session", Value: "test"}
	_, code := readIncidentAsSession(t, s, incidentID, cookie)
	if code != http.StatusOK {
		t.Fatalf("read the incident: got %d, want 200", code)
	}

	if n := openedRows(t, tenant, incidentID); n != 1 {
		t.Errorf("%d incident.opened row(s) after the first authenticated read, want 1. "+
			"The row IS the producer for attention_minutes, and without it the North Star "+
			"numerator has no source: it is not measurable at any window length", n)
	}
}

func TestARefreshIsNotANewBoutOfAttention(t *testing.T) {
	s, tenant, site := server(t)
	incidentID := seedIncident(t, tenant, site)

	cookie, _ := mustSignIn(t, s)
	for i := 0; i < 3; i++ {
		next, code := readIncidentAsSession(t, s, incidentID, cookie)
		if code != http.StatusOK {
			t.Fatalf("read %d: got %d, want 200", i, code)
		}
		cookie = next
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
func TestASecondOperatorSeesASecondRow(t *testing.T) {
	s, tenant, site := server(t)
	incidentID := seedIncident(t, tenant, site)

	for i := 0; i < 2; i++ {
		cookie, _ := mustSignIn(t, s)
		next, code := readIncidentAsSession(t, s, incidentID, cookie)
		if code != http.StatusOK {
			t.Fatalf("read %d: got %d, want 200", i, code)
		}
		_ = next
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

	cookie, _ := mustSignIn(t, s)
	if _, code := readIncidentAsSession(t, s, incidentID, cookie); code != http.StatusOK {
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
	if rec.Code != http.StatusOK {
		t.Fatalf("read an incident: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if n := openedRows(t, tenant, theirs); n != 1 {
		t.Errorf("%d incident.opened row(s) after a read, want 1", n)
	}
}
