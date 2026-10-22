package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The step-up interstitial's own page.
//
// It is tested here, in the package, because it is rendered by an unexported
// helper: the console's disposition route is the only caller, and the
// coordinator wires that route. A test that could only reach the page through
// that route would be a test that cannot run until somebody else has wired
// it, which is the same shape as a control nobody has run.
//
// The words are the contract with the design system (ux-and-design-system
// §3.1.2): what will happen, what will be recorded, who is told, the code,
// and the sentence about what cannot be taken back.

func TestTheStepUpInterstitialSaysWhatCannotBeTakenBack(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/incidents/inc-1/disposition", nil)

	s.renderStepUpPrompt(rec, req, consoleData{Title: "Confirm", RequestID: "req-1"},
		StepUpForm{
			IncidentID: "inc-1",
			Outcome:    "escalated",
			ReasonCode: "not_a_restricted_person",
			Notes:      "the badge was checked against the roster",
			Agreement:  "agreed",
		})

	// The interstitial is the BODY of a refusal, and the refusal is a 403.
	//
	// The JSON route answers 403 `step_up_required` for the same thing, and a
	// console that answers 200 for a permission the other route refuses is a
	// console an operator has to learn two things about -- and an access log
	// that says 200 for a refused escalation says the wrong thing about the
	// one event the log exists for.
	if rec.Code != http.StatusForbidden {
		t.Fatalf("the interstitial rendered as %d, want 403: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	for _, want := range []string{
		"Your second factor is required again",
		"This can be undone, but the notification cannot.",
		// The code field, and the form action that spends it.
		`name="factor"`,
		`action="/incidents/inc-1/disposition"`,
		// What will happen, and who is told.
		"This will escalate an incident.",
		"The duty manager on shift will be notified.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the interstitial is missing %q:\n%s", want, body)
		}
	}

	// The form carries NO CSRF field, and this is asserted rather than left
	// alone.
	//
	// The interstitial used to render `<input type="hidden" name="csrf_token"
	// value="">`, because the console's page data carried a token field and the
	// configured principal has no session to mint one from. An empty token field
	// is worse than no token field: a reader auditing the markup concludes the
	// console refuses a cross-site POST, and it does not. The console's identity
	// comes from the boundary in front of it, and `docs/threat-model.md` A3 says
	// so in the words an auditor reads.
	//
	// A field that appears here again is a claim the product cannot back.
	if strings.Contains(body, `name="csrf_token"`) {
		t.Errorf("the interstitial renders a CSRF field:\n%s", body)
	}

	// The console has no client JavaScript and no inline style, and the
	// Content-Security-Policy forbids both. An interstitial that broke that
	// would render a form the browser refuses to submit.
	if strings.Contains(body, "<script") {
		t.Error("the interstitial contains a script element")
	}
	if strings.Contains(body, "style=") {
		t.Error("the interstitial contains an inline style attribute")
	}
}

// The interstitial carries what the operator already typed, so confirming
// records what they chose rather than what survived a round trip.
func TestTheStepUpInterstitialCarriesTheDispositionForward(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/incidents/inc-1/disposition", nil)

	s.renderStepUpPrompt(rec, req, consoleData{Title: "Confirm"},
		StepUpForm{
			IncidentID: "inc-1",
			Outcome:    "dismissed",
			ReasonCode: "not_a_restricted_person",
			Notes:      "the badge was checked against the roster",
			Agreement:  "disagreed",
		})

	body := rec.Body.String()
	for _, want := range []string{
		`name="outcome" value="dismissed"`,
		`name="agreement" value="disagreed"`,
		`name="reasonCode" value="not_a_restricted_person"`,
		`name="notes" value="the badge was checked against the roster"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the interstitial does not carry %q forward:\n%s", want, body)
		}
	}
}

// A dismissal is not an escalation, and the copy must not be the same
// sentence for both. What they share is the record.
func TestTheStepUpCopyDiffersBetweenTheTwoSensitiveOutcomes(t *testing.T) {
	escalate, notifyEscalate := stepUpCopy("escalated")
	dismiss, notifyDismiss := stepUpCopy("dismissed")

	if escalate == dismiss {
		t.Error("an escalation and a dismissal are described with the same sentence")
	}
	if !strings.Contains(notifyEscalate, "duty manager") {
		t.Errorf("an escalation does not say who is told: %q", notifyEscalate)
	}
	if notifyDismiss == notifyEscalate {
		t.Error("a dismissal notifies the same people an escalation does")
	}
}
