package main

// The console's operator surface.
//
// Everything the console did before this file existed ran a CLI subcommand and
// printed its output into a monospace box. That is a control panel for an
// engineer who already knows what to run. What an operator is looking at is a
// different question, and it is the question this whole product exists to
// answer: **where is the system staying quiet, and can I see it without reading
// a terminal.**
//
// So these handlers shell out to `securityctl` the same way the others do — one
// binary, one trust model, no second implementation of the arithmetic in
// JavaScript — and the page renders the answer rather than the transcript.
//
// The three routes are chosen for the same reason: each one is a claim in
// `docs/status.md` that a judge can check by looking at a screen.
//
//   /api/efficiency  the attention and latency numbers, the five verification
//                    states named with the two silent ones, and the per-site,
//                    per-class slice. This is the route that turns "the system
//                    stayed quiet on 40% of decisions" into a sentence about a
//                    site.
//   /api/incident    one incident: its rule, its severity, its corroboration
//                    state, and the version of the arithmetic that ranked it.
//   /api/explain     why an incident was NOT raised. "The system decided not to
//                    speak" is a state with a reason, and a reason an operator
//                    can ask about is the difference between an exception queue
//                    and a mute button.
//
// The window is the only free parameter and it is bounded, because a query with
// an unbounded window is a query nobody can predict the cost of, and a console
// is exactly where an unbounded query gets typed.

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
)

// maxEfficiencyWindow is the largest window the console will ask Postgres for.
//
// 90 days is a quarter, which is longer than any operator judgement and shorter
// than any retention discussion. The server refuses a larger value rather than
// clamping it silently: a clamped window renders numbers the reader believes are
// about the period they asked for.
const maxEfficiencyWindow = 24 * 90

// incidentIDMaxLen bounds the id an operator may look up.
//
// An id is a UUID-ish string the console echoes into a subcommand argument, and
// the lookup path is only reachable with a well-formed one. A length bound here
// is not the control — `securityctl incident show` rejects anything it cannot
// find, and there is no shell involved, the argument is an exec element — it is
// the cheap way to refuse the obvious junk before forking a process.
const incidentIDMaxLen = 128

// efficiencyRequest is the whole request. One field, because one question.
type efficiencyRequest struct {
	Hours int `json:"hours"`
}

// decodeJSONBody decodes a request body into v, refusing anything that is not
// JSON and refusing an unknown FIELD.
//
// The unknown-field refusal is not pedantry. A console that silently ignores a
// misspelled field renders a number the reader believes is about the parameter
// they typed, which is the failure mode of every dashboard that has ever been
// accused of lying. Both of them cost one method call.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return err
	}
	return nil
}

// methodIs is the method guard every handler here shares, named so the guard and
// its reason are one thing.
func methodIs(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodPost {
		return true
	}
	http.Error(w, "POST only", http.StatusMethodNotAllowed)
	return false
}

// panicking turns a panic in a handler into a 500 with the reason, which is what
// a console is for: the operator sees that the page failed and why, rather than
// a connection reset and a stack trace in a log nobody is reading.
func panicking(w http.ResponseWriter, what string) {
	if rec := recover(); rec != nil {
		log.Printf("PANIC in %s: %v", what, rec)
		http.Error(w, fmt.Sprintf("Internal error: %v", rec), http.StatusInternalServerError)
	}
}

// handleEfficiency is the route that answers "where does this system stay
// quiet".
func handleEfficiency(securityctlPath, repoRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer panicking(w, "handleEfficiency")
		if !methodIs(w, r) {
			return
		}
		var req efficiencyRequest
		if err := decodeJSONBody(w, r, &req); err != nil {
			return
		}
		// Over the ceiling is REFUSED rather than clamped. A clamped window
		// renders numbers the reader believes are about the period they asked
		// for, and it is the kind of quiet lie a console can spend a whole page
		// telling. Below or at zero is defaulted, because a page that posts no
		// window should get the same answer every time it fails to say.
		if req.Hours > maxEfficiencyWindow {
			http.Error(w, fmt.Sprintf("hours must be %d or less", maxEfficiencyWindow), http.StatusBadRequest)
			return
		}
		hours := efficiencyWindow(req.Hours, maxEfficiencyWindow)
		// The answer is sent through respondSubprocess with the others, and the
		// status it returns is 502 rather than 500: a subprocess that exited
		// non-zero is a proxy in front of a thing that said no, and 500 says "this
		// page is broken", which is a different claim and the wrong one to show an
		// operator. The first draft of this handler returned 500 and its own test
		// said so, which is how the two shapes ended up disagreeing.
		output, err := runCmd(r.Context(), securityctlPath, repoRoot,
			"efficiency", "--hours", strconv.Itoa(hours), "--json")
		respondSubprocess(w, "efficiency", output, err)
	}
}

// handleIncident is one incident, addressed by id.
func handleIncident(securityctlPath, repoRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer panicking(w, "handleIncident")
		if !methodIs(w, r) {
			return
		}
		var req struct {
			ID string `json:"id"`
		}
		if err := decodeJSONBody(w, r, &req); err != nil {
			return
		}
		if req.ID == "" {
			http.Error(w, "id required", http.StatusBadRequest)
			return
		}
		if len(req.ID) > incidentIDMaxLen {
			http.Error(w, "id too long", http.StatusBadRequest)
			return
		}
		output, err := runCmd(r.Context(), securityctlPath, repoRoot,
			"incident", "show", req.ID, "--json")
		respondSubprocess(w, "incident", output, err)
	}
}

// handleExplain answers the follow-up question, which is the one this product
// claims to be able to answer: why was nothing raised.
func handleExplain(securityctlPath, repoRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer panicking(w, "handleExplain")
		if !methodIs(w, r) {
			return
		}
		var req struct {
			ID string `json:"id"`
		}
		if err := decodeJSONBody(w, r, &req); err != nil {
			return
		}
		if req.ID == "" {
			http.Error(w, "id required", http.StatusBadRequest)
			return
		}
		if len(req.ID) > incidentIDMaxLen {
			http.Error(w, "id too long", http.StatusBadRequest)
			return
		}
		output, err := runCmd(r.Context(), securityctlPath, repoRoot,
			"incident", "explain", req.ID, "--json")
		respondSubprocess(w, "explain", output, err)
	}
}

// respondSubprocess sends a subcommand's answer, and distinguishes the two
// failure shapes.
//
// A non-zero exit with a message is the CLI telling you something and is a 502:
// the console is a proxy and the thing behind it said no. A non-zero exit with
// output that is not JSON is the same thing, and it must not be forwarded as
// though it were an answer — that is how a failed lookup becomes a rendered
// "incident not found" in a page nobody can distinguish from the real one.
func respondSubprocess(w http.ResponseWriter, what string, output []byte, err error) {
	if err != nil {
		log.Printf("%s: the CLI failed: %v", what, err)
		http.Error(w, fmt.Sprintf("%s failed", what), http.StatusBadGateway)
		return
	}
	var doc any
	if err := json.Unmarshal(output, &doc); err != nil {
		log.Printf("%s: the CLI wrote something that is not JSON (%d bytes)", what, len(output))
		http.Error(w, fmt.Sprintf("%s did not come back as JSON", what), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(output)
}

// efficiencyWindow validates and defaults the window, and it is what the ROUTE
// calls rather than a rule restated inside the handler.
//
// The ceiling lives here and the default lives here because a page that defaults
// to 168 and a route that defaults to 24 render two different answers to the same
// click — the class of defect that makes a dashboard disagree with itself.
//
// The `ceiling` parameter is named that rather than `max` because Go 1.21 made
// `max` a builtin, and shadowing a builtin is a reader's second glance when the
// body is a comparison.
func efficiencyWindow(want, ceiling int) int {
	if want <= 0 {
		return defaultEfficiencyWindow
	}
	if want > ceiling {
		return ceiling
	}
	return want
}

// defaultEfficiencyWindow is the window a request with no hours uses, and it is
// the same number the page's select box uses for "24 hours".
const defaultEfficiencyWindow = 24
