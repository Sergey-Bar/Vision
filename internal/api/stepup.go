package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/Sergey-Bar/vision/internal/identity"
)

// The step-up interstitial, and the step-up itself.
//
// # Why this file exists and does not touch console.go
//
// The interstitial is the console's half of the step-up, and `console.go` is
// owned by somebody else at the moment. Everything the coordinator needs to
// wire it in therefore lives here: the markup, the form action, the helper
// that renders it, and the helper that obtains and spends the proof. The
// coordinator's edit to `console.go` is three calls and is written out at the
// bottom of this file.
//
// # What the interstitial is for
//
// `escalated` and `dismissed` need a fresh second factor, because escalating
// an incident is somebody else's problem now. The design system says what the
// page has to say, and it is not decoration:
//
//   - what will happen, in one line, before anything is asked;
//   - what will be recorded, and who, and when;
//   - who is told, because the notification is the part that cannot be taken
//     back;
//   - the code, and nothing else to fill in;
//   - and the sentence that says the record can be superseded but the
//     notification cannot.
//
// An operator who is about to wake a duty manager at 02:00 should be able to
// read all of that on one page, and an operator who mis-keys Enter must not
// be the thing that wakes them.

// StepUpForm is what the interstitial carries between the form that asked for
// the disposition and the form that asks for the code.
//
// Everything the operator already typed is carried through, so that
// confirming does not make them retype it and so that the disposition that is
// finally recorded is the one they chose rather than the one that survived a
// round trip.
type StepUpForm struct {
	// IncidentID is the incident being dispositioned.
	IncidentID string
	// Outcome is the disposition the operator chose.
	Outcome string
	// ReasonCode and Notes are the operator's own words about it.
	ReasonCode string
	Notes      string
	// Agreement is the operator's answer to "did you agree with the AI?".
	Agreement string
	// Headline is what will happen, in one line, and it is filled in from the
	// outcome when the caller leaves it empty.
	Headline string
	// Notification is who is told. It is filled in from the outcome when the
	// caller leaves it empty.
	Notification string
}

// stepUpCopy is what the page says about a given outcome.
//
// The two outcomes are not the same act and the copy must not be the same
// sentence for both: escalating wakes somebody, and dismissing does not. What
// they share is the record, and the record is the thing that cannot be
// quietly undone.
func stepUpCopy(outcome string) (headline, notification string) {
	switch outcome {
	case "escalated":
		return "This will escalate an incident.",
			"The duty manager on shift will be notified."
	case "dismissed":
		return "This will dismiss an incident.",
			"No one is notified, and the dismissal is recorded against your name."
	default:
		return "This will record a disposition.",
			"No one is notified."
	}
}

// stepUpData is what the interstitial template receives.
//
// It embeds consoleData so the shared head and foot render exactly as they do
// on every other console page. A page with a different topbar is a page an
// operator has to learn a second time, at the moment they are about to do
// something that wakes somebody.
type stepUpData struct {
	consoleData
	StepUp StepUpForm
}

// stepUpMarkup is the interstitial.
//
// It is parsed into the console's own template set rather than being a file of
// its own, so it can use the shared head and foot, and it is a Go string
// rather than a template file so that it lives beside the helpers that render
// it and the coordinator's wiring instructions.
//
// There is no client JavaScript and no inline style anywhere in it. The
// Content-Security-Policy forbids both, and the console has no script at all
// by design: every interaction here is a form POST and a full page render.
const stepUpMarkup = `{{define "stepup"}}
{{template "head" .}}
<main class="narrow">
  <h1>{{.StepUp.Headline}}</h1>
  {{if .Incident}}
  <p class="hint">{{.Incident.IncidentID}} &middot; {{.Incident.Title}}</p>
  <p class="hint">{{.Incident.Verification.State}}{{if .StepUp.ReasonCode}} &middot; reason: {{.StepUp.ReasonCode}}{{end}}</p>
  {{else}}
  <p class="hint">{{.StepUp.IncidentID}}</p>
  {{end}}
  <dl class="facts">
    <dt>Recorded as</dt>
    <dd>{{.Principal.Username}}, {{.StepUp.Outcome}}, {{.Time .Now}}</dd>
    <dt>Notified</dt>
    <dd>{{.StepUp.Notification}}</dd>
  </dl>
  {{template "messages" .}}
  <form method="post" action="/incidents/{{.StepUp.IncidentID}}/disposition" class="stack">
    <input type="hidden" name="outcome" value="{{.StepUp.Outcome}}">
    <input type="hidden" name="reasonCode" value="{{.StepUp.ReasonCode}}">
    <input type="hidden" name="notes" value="{{.StepUp.Notes}}">
    <input type="hidden" name="agreement" value="{{.StepUp.Agreement}}">
    <label>Your second factor is required again.
      <input type="text" name="factor" value="" autocomplete="one-time-code"
             inputmode="numeric" maxlength="6" pattern="[0-9]{6}" required>
    </label>
    <button type="submit">Confirm</button>
    <a href="/incidents/{{.StepUp.IncidentID}}">Cancel</a>
  </form>
  <p class="hint">This can be undone, but the notification cannot.</p>
</main>
{{template "foot" .}}
{{end}}`

// stepUpTemplate is the interstitial, parsed into the console's template set.
//
// `consoleTemplates.New` associates the template with the same set, so
// `{{template "head" .}}` and `{{template "foot" .}}` resolve against the
// shared definitions rather than being copied here and drifting.
//
// It is parsed at init, like the console's own templates: a template that
// parses and then fails at runtime is a console that breaks on the one page
// an operator needs at 02:00.
var stepUpTemplate = template.Must(consoleTemplates.New("stepup").Parse(stepUpMarkup))

// renderStepUpPrompt renders the step-up interstitial.
//
// The coordinator calls this from `handleConsoleDisposition` when the chosen
// outcome needs a factor and the form did not carry one. The wiring is:
//
//	if incidents.OutcomeNeedsStepUp(outcome) {
//	    factor := strings.TrimSpace(r.PostFormValue("factor"))
//	    if factor == "" {
//	        s.renderStepUpPrompt(w, r, data, StepUpForm{
//	            IncidentID: id, Outcome: outcome, ReasonCode: reasonCode,
//	            Notes: notes, Agreement: agreement,
//	        })
//	        return
//	    }
//	    if err := s.stepUpForDisposition(r.Context(), p, d, id, factor); err != nil {
//	        data.Error = err.Error()
//	        s.renderStepUpPrompt(w, r, data, StepUpForm{
//	            IncidentID: id, Outcome: outcome, ReasonCode: reasonCode,
//	            Notes: notes, Agreement: agreement,
//	        })
//	        return
//	    }
//	    stepUpVerified = true
//	}
//
// and then `StepUpVerified: stepUpVerified` on the disposition.
//
// The form action is the disposition route itself, and the fields the operator
// already filled in are carried through as hidden inputs, so confirming
// records what they chose rather than what survived a round trip.
//
// The one thing this does not do is put the operator's form back the way it
// was when they press Cancel. The design system asks for that, and the
// console has no session to hold a half-filled form in and no client
// JavaScript to hold it in the page. Cancel returns to the incident page
// with an empty form, which is a worse answer than the design system wanted
// and the honest one: the alternative is carrying the operator's notes in a
// query string, and a query string is in the browser history and in every
// access log between here and the operator's laptop.
func (s *Server) renderStepUpPrompt(w http.ResponseWriter, r *http.Request, data consoleData, form StepUpForm) {
	if form.Headline == "" || form.Notification == "" {
		headline, notification := stepUpCopy(form.Outcome)
		if form.Headline == "" {
			form.Headline = headline
		}
		if form.Notification == "" {
			form.Notification = notification
		}
	}
	if data.Title == "" {
		data.Title = "Confirm"
	}

	var buf bytes.Buffer
	if err := stepUpTemplate.ExecuteTemplate(&buf, "stepup", stepUpData{consoleData: data, StepUp: form}); err != nil {
		if s.Log != nil {
			s.Log.Error("console step-up render failed", "error", err)
		}
		WriteError(w, http.StatusInternalServerError, "render_failed",
			"The page could not be rendered.", requestID(r))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The console is a security surface; a cached page is a stale authorization
	// decision served to whoever opens the machine next.
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	// The refusal is 403, and the page IS the refusal's body.
	//
	// The JSON route answers 403 `step_up_required` for the same thing, and a
	// console that answers 200 for a permission the other route refuses is a
	// console an operator has to learn two things about: the page rendered,
	// so the disposition looks recorded, and it was not. The interstitial
	// still renders -- a 403 with a body is a page -- and it still carries the
	// form the operator needs to SATISFY the refusal.
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write(buf.Bytes())
}

// stepUpForDisposition obtains a step-up proof for a sensitive disposition
// and spends it.
//
// The proof is minted and spent inside this one call, and it is spent BEFORE
// the disposition is recorded rather than after: a proof that could be
// presented twice would let one six-digit code escalate a second incident
// after the operator had walked away from the first.
//
// The code is verified by the identity service and nowhere else. The API does
// not know what a TOTP step is, and it must not: a second implementation of a
// second-factor check is a second set of rules about what a code is, and the
// one that is wrong is the one nobody tests.
//
// The factor is the CODE and not a proof, because a proof is a bearer token
// and putting one in an HTML form means putting it in a page, a DOM and
// possibly a browser history.
func (s *Server) stepUpForDisposition(ctx context.Context, p identity.Principal, d Deps, incidentID, factor string) error {
	if d.Identities == nil {
		return errors.New("this install has no identity service, so a second factor cannot be verified")
	}
	factor = strings.TrimSpace(factor)
	if factor == "" {
		return errors.New("this outcome requires a second factor: the six-digit code your authenticator is showing")
	}
	proof, err := d.Identities.StepUp(ctx, p.TenantID, p.UserID, "", factor, "incident.disposition", incidentID)
	if err != nil {
		return fmt.Errorf("the second factor was refused: %w", err)
	}
	if err := d.Incidents.ConsumeStepUp(ctx, d.Identities, p.TenantID, p.UserID, proof); err != nil {
		return fmt.Errorf("the step-up could not be spent: %w", err)
	}
	return nil
}
