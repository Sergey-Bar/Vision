package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/api"
)

// The two sensitive dispositions, and the second factor they need.
//
// `escalated` and `dismissed` are refused by `incidents.Record` without a
// step-up, and no route obtained one: the answer to a request for either was
// a 403 with no way to satisfy it. These tests are about the route that now
// obtains one.

// dispositionBody is the JSON a disposition request carries.
func dispositionBody(t *testing.T, outcome, agreement, factor string) string {
	t.Helper()
	payload := map[string]any{
		"outcome":    outcome,
		"reasonCode": "not_a_restricted_person",
		"notes":      "the badge was checked against the roster",
		"agreement":  agreement,
	}
	if factor != "" {
		payload["secondFactor"] = factor
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}

// postDisposition sends a disposition request and returns the recorder.
func postDisposition(t *testing.T, s *api.Server, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/incidents/"+id+"/dispositions",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// A sensitive outcome with no factor is refused, and the refusal says what is
// missing.
//
// Before this, the refusal existed and nothing could satisfy it: the route
// never obtained a step-up, so `escalated` and `dismissed` were unreachable
// from the API. The 403 is not new. What is new is that there is a way
// through it.
func TestASensitiveDispositionWithoutASecondFactorIsRefused(t *testing.T) {
	s, tenant, site := server(t)
	id := seedIncident(t, tenant, site)

	for _, outcome := range []string{"escalated", "dismissed"} {
		t.Run(outcome, func(t *testing.T) {
			rec := postDisposition(t, s, id, dispositionBody(t, outcome, "agreed", ""))

			if rec.Code != http.StatusForbidden {
				t.Fatalf("a %s with no factor returned %d, want 403", outcome, rec.Code)
			}
			var body api.ErrorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.Code != "step_up_required" {
				t.Errorf("the refusal is %q, want step_up_required", body.Code)
			}
		})
	}
}

// With a factor, the disposition IS recorded.
//
// This is the whole point of the change. Before it, the two sensitive outcomes
// could not be recorded at all.
func TestASensitiveDispositionWithASecondFactorIsRecorded(t *testing.T) {
	s, tenant, site := server(t)
	id := seedIncident(t, tenant, site)

	for i, outcome := range []string{"escalated", "dismissed"} {
		t.Run(outcome, func(t *testing.T) {
			// Each outcome needs its own code: a TOTP step is consumed when it
			// is presented, so a code cannot be spent twice. The verifier
			// accepts the step it is at and the half-steps either side, so
			// the second outcome uses the half-step after the first.
			code := totpCode(t, theOperatorSecret, clock.Add(time.Duration(i)*30*time.Second))
			rec := postDisposition(t, s, id, dispositionBody(t, outcome, "agreed", code))

			if rec.Code != http.StatusCreated {
				t.Fatalf("a %s with a factor returned %d, want 201: %s",
					outcome, rec.Code, rec.Body.String())
			}
		})
	}
}

// The factor is single use.
//
// The code is spent when the step-up is obtained, and the step-up is spent
// when the disposition is recorded. Presenting the same code twice therefore
// fails at the second attempt, and the failure is a refusal rather than a
// second escalation: one code, one escalation.
func TestASecondFactorCannotBeSpentTwice(t *testing.T) {
	s, tenant, site := server(t)
	id := seedIncident(t, tenant, site)

	code := totpCode(t, theOperatorSecret, clock)
	first := postDisposition(t, s, id, dispositionBody(t, "escalated", "agreed", code))
	if first.Code != http.StatusCreated {
		t.Fatalf("the first escalation returned %d, want 201: %s", first.Code, first.Body.String())
	}

	second := postDisposition(t, s, id, dispositionBody(t, "escalated", "agreed", code))
	if second.Code != http.StatusForbidden {
		t.Fatalf("the same code produced a second escalation: %d %s", second.Code, second.Body.String())
	}
	var body api.ErrorBody
	if err := json.Unmarshal(second.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Code != "step_up_required" {
		t.Errorf("the second use was refused as %q, want step_up_required", body.Code)
	}
}

// The agreement is required.
//
// It used to be defaulted to "unsure" on the console route, and a default
// there is a way of losing the one piece of evaluation data the product
// exists to produce: a disposition that says nothing about whether the
// operator agreed with the model is not a training signal.
func TestADispositionWithoutAnAgreementIsRefused(t *testing.T) {
	s, tenant, site := server(t)
	id := seedIncident(t, tenant, site)

	rec := postDisposition(t, s, id, dispositionBody(t, "acknowledged", "", ""))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a disposition with no agreement returned %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// The four outcomes that do not need a factor still work without one.
//
// The step-up is a cost on the two sensitive outcomes, not a tax on the
// ordinary ones. A route that demanded a factor for everything would push
// operators towards the outcomes that do not need one.
func TestAnOrdinaryDispositionNeedsNoFactor(t *testing.T) {
	s, tenant, site := server(t)
	id := seedIncident(t, tenant, site)

	rec := postDisposition(t, s, id, dispositionBody(t, "acknowledged", "agreed", ""))
	if rec.Code != http.StatusCreated {
		t.Fatalf("an acknowledgement returned %d, want 201: %s", rec.Code, rec.Body.String())
	}
}
