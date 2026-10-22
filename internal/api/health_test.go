package api_test

// The health endpoint, and the loop liveness it reports.
//
// # Why the loops are in the health report
//
// A pipeline loop that has stopped is an outage that looks like a healthy core:
// the process keeps answering /v1/health, the backlog grows, and nothing says
// so. The health endpoint is the one place an operator looks without asking
// anything, so it is where the stopped loops belong.
//
// # Why the status stays "ok"
//
// A readiness probe that turns the platform unready because a loop is down
// moves the failure to every replica at once, and the same reasoning that keeps
// a breached SLO objective out of the status keeps a stopped loop out of it.
// The operator is told; the orchestrator is not lied to.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/api"
)

// TestTheHealthEndpointNamesALoopThatIsNotRunning is the visibility.
//
// A stopped loop is named, and a running one is not.
func TestTheHealthEndpointNamesALoopThatIsNotRunning(t *testing.T) {
	s, _, _ := server(t)
	s.Loops = func() []api.LoopLiveness {
		return []api.LoopLiveness{
			{Name: "ingest", Running: true, LastBeat: time.Now().UTC()},
			{Name: "consume", Running: false, LastBeat: time.Now().UTC().Add(-time.Minute), Panics: 3},
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("the health endpoint answered %d: %s", rec.Code, rec.Body.String())
	}
	var doc struct {
		Status string `json:"status"`
		Loops  struct {
			Running  int      `json:"running"`
			Stopped  []string `json:"stopped"`
			Panicked []string `json:"panicked"`
		} `json:"loops"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("parse the health body: %v\n%s", err, rec.Body.String())
	}
	if doc.Status != "ok" {
		t.Errorf("the status is %q, want ok. A stopped loop is reported, not enforced", doc.Status)
	}
	if len(doc.Loops.Stopped) != 1 || doc.Loops.Stopped[0] != "consume" {
		t.Errorf("the stopped loops are %v, want [consume]. A loop that has stopped is an "+
			"outage that looks like a healthy core", doc.Loops.Stopped)
	}
	if len(doc.Loops.Panicked) != 1 || doc.Loops.Panicked[0] != "consume" {
		t.Errorf("the panicking loops are %v, want [consume]. A restarted loop is not a "+
			"recovered loop", doc.Loops.Panicked)
	}
	if doc.Loops.Running != 1 {
		t.Errorf("the running count is %d, want 1", doc.Loops.Running)
	}
}

// TestTheHealthEndpointSaysNothingAboutLoopsItWasNotToldAbout is the honest
// absence.
//
// An install that wires no pipeline has no loops to report, and the health
// endpoint leaves the member out rather than reporting an empty set: "no loops"
// and "nobody told me about the loops" are different answers.
func TestTheHealthEndpointSaysNothingAboutLoopsItWasNotToldAbout(t *testing.T) {
	s, _, _ := server(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("parse the health body: %v", err)
	}
	if _, present := doc["loops"]; present {
		t.Errorf("the health body carries a loops member with nothing behind it: %v", doc["loops"])
	}
}
