package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/airuntime"
	"github.com/Sergey-Bar/Ai-Captain/internal/dutylog"
)

func dutyEventForTest() dutylog.Event {
	return dutylog.Event{
		Kind: dutylog.KindTransition, Actor: "autonomy", Subject: "inc-test",
		Action: "VERIFY/VERIFYING", Reason: "dwell in a restricted zone",
	}
}

// fakeRuntime serves the airuntime wire shape over a question, so the
// investigator's behaviour is tested against the same client and the same
// response shape the real plane uses — no VLM required, because the guarantees
// under test are the citation checks and the named degradations, not the model.
func fakeRuntime(t *testing.T, answer func(req map[string]any) map[string]any) *airuntime.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(answer(req))
	}))
	t.Cleanup(srv.Close)
	client, err := airuntime.New(srv.URL, 5*time.Second, false)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	return client
}

func runtimeAnswer(structured map[string]any) map[string]any {
	return map[string]any{
		"apiVersion":       airuntime.APIVersion,
		"observationId":    "investigate-x",
		"profileId":        InvestigatorProfile,
		"structuredOutput": structured,
		"degradedReason":   "none",
		"model":            "local-vlm",
		"runtimeVersion":   "ai-runtime/1",
	}
}

// TestInvestigateWithoutAPlaneNamesItsReason is the "never an empty box" rule:
// with no runtime configured, the answer is the named reason a dashboard can
// render.
func TestInvestigateWithoutAPlaneNamesItsReason(t *testing.T) {
	s := &StreamIngest{config: &Config{}}
	out := s.Investigate(context.Background(), "which cameras are dark?")
	if !out.Degraded || out.DegradedReason != "no_runtime" {
		t.Fatalf("no-plane answer = %+v, want a no_runtime degradation", out)
	}
	if out.DegradedDetail == "" {
		t.Error("the degradation carries no detail an operator can act on")
	}

	out = s.Investigate(context.Background(), "   ")
	if !out.Degraded || out.DegradedReason != "absent" {
		t.Errorf("an empty question = %+v, want an absent degradation", out)
	}
}

// TestInvestigateRefusesWhenThereAreNoFacts: a question with nothing stored to
// answer from is refused by name, not answered from nothing.
func TestInvestigateRefusesWhenThereAreNoFacts(t *testing.T) {
	s := &StreamIngest{
		config: &Config{},
		aiClient: fakeRuntime(t, func(map[string]any) map[string]any {
			t.Error("the runtime was called with no facts to show it")
			return runtimeAnswer(nil)
		}),
	}
	out := s.Investigate(context.Background(), "what happened?")
	if !out.Degraded || out.DegradedReason != "absent" {
		t.Errorf("no-facts answer = %+v, want an absent degradation", out)
	}
}

// TestInvestigateReturnsACitedAnswer checks the happy path: the facts are
// assembled with ids, the answer comes back with those ids as citations, and
// the model's attribution is carried.
func TestInvestigateReturnsACitedAnswer(t *testing.T) {
	var seenFacts []string
	client := fakeRuntime(t, func(req map[string]any) map[string]any {
		for _, e := range req["evidence"].([]any) {
			seenFacts = append(seenFacts, e.(map[string]any)["evidenceId"].(string))
		}
		return runtimeAnswer(map[string]any{
			"answer":     "two cameras, 42s dwell in the restricted zone",
			"citations":  []any{seenFacts[0]},
			"confidence": 0.8,
		})
	})
	s := newHandoverIngest(t)
	s.aiClient = client
	c := s.fleet.get("cam-01")
	c.frames.Store(120)
	c.lastFrame.Store(time.Now().UTC().Add(-time.Second).UnixNano())

	out := s.Investigate(context.Background(), "what happened this shift?")
	if out.Degraded {
		t.Fatalf("a cited answer was refused: %+v", out)
	}
	if !strings.Contains(out.Answer, "42s dwell") {
		t.Errorf("answer = %q", out.Answer)
	}
	if len(out.Citations) == 0 {
		t.Error("the answer carries no citations")
	}
	if len(out.Facts) == 0 {
		t.Error("the response does not say which facts the question rested on")
	}
	if out.Model != "local-vlm" {
		t.Errorf("model = %q, want the plane's own attribution", out.Model)
	}
}

// TestInvestigateRefusesAnUncitableOrUncitedAnswer is the anti-fabrication
// guard, on the POC's side of the seam as well as the plane's: an answer that
// cites a fact it was not shown, or that cites nothing, is refused.
func TestInvestigateRefusesAnUncitableOrUncitedAnswer(t *testing.T) {
	s := newHandoverIngest(t)
	c := s.fleet.get("cam-01")
	c.frames.Store(5)
	c.lastFrame.Store(time.Now().UTC().Add(-time.Second).UnixNano())

	s.aiClient = fakeRuntime(t, func(map[string]any) map[string]any {
		return runtimeAnswer(map[string]any{
			"answer":    "a contractor was on site",
			"citations": []any{"incident-that-never-existed"},
		})
	})
	out := s.Investigate(context.Background(), "who was it?")
	if !out.Degraded || !strings.Contains(out.DegradedDetail, "incident-that-never-existed") {
		t.Errorf("an uncited fact was displayed: %+v", out)
	}

	s.aiClient = fakeRuntime(t, func(map[string]any) map[string]any {
		return runtimeAnswer(map[string]any{"answer": "a contractor was on site", "citations": []any{}})
	})
	out = s.Investigate(context.Background(), "who was it?")
	if !out.Degraded || out.DegradedReason != "absent" {
		t.Errorf("an answer with no citations was kept: %+v", out)
	}
}

// TestInvestigatePassesTheDegradationThrough: when the plane answers that it
// could not answer, the reason reaches the caller verbatim — that is the whole
// point of the degraded field.
func TestInvestigatePassesTheDegradationThrough(t *testing.T) {
	s := newHandoverIngest(t)
	c := s.fleet.get("cam-01")
	c.frames.Store(5)
	c.lastFrame.Store(time.Now().UTC().Add(-time.Second).UnixNano())
	s.aiClient = fakeRuntime(t, func(map[string]any) map[string]any {
		return map[string]any{
			"apiVersion":       airuntime.APIVersion,
			"observationId":    "investigate-x",
			"profileId":        InvestigatorProfile,
			"degradedReason":   "no_runtime",
			"degradedDetail":   "SP_ENGINE_PATH is unset: no inference engine is bound",
			"runtimeVersion":   "ai-runtime/1",
			"structuredOutput": map[string]any{},
		}
	})
	out := s.Investigate(context.Background(), "what happened?")
	if !out.Degraded || out.DegradedReason != "no_runtime" {
		t.Fatalf("plane degradation = %+v, want it passed through", out)
	}
	if !strings.Contains(out.DegradedDetail, "SP_ENGINE_PATH") {
		t.Errorf("detail = %q, want the plane's own named reason", out.DegradedDetail)
	}
}

// TestInvestigatorFactsAreIdsAndText: a fact is an id a citation can name and
// text a model can read, and the duty log's lines are among them.
func TestInvestigatorFactsAreIdsAndText(t *testing.T) {
	s := newHandoverIngest(t)
	if _, err := s.handover.duty.Append(dutyEventForTest()); err != nil {
		t.Fatalf("append: %v", err)
	}
	facts := s.investigatorFacts(time.Now().UTC())
	if len(facts) == 0 {
		t.Fatal("no facts were assembled")
	}
	seen := map[string]bool{}
	for _, f := range facts {
		if f.ID == "" || f.Text == "" {
			t.Errorf("fact %+v has no id or no text", f)
		}
		seen[f.ID] = true
	}
	if !seen["cam-01"] {
		t.Error("the fleet's camera health is not among the facts")
	}
}
