package api

// The absence panel: what the system did NOT look at, and when it was blind.
//
// # Why this is a page and not a field
//
// Everything else the console shows is a thing that HAPPENED. An incident, a
// disposition, a dead letter. An operator who reads only those pages learns what the
// system saw and nothing about what it missed — and a security product whose page
// shows no failures is indistinguishable from a security product with no failures,
// which is exactly the confusion this product exists to prevent.
//
// So this page exists to answer one question: **for this site, right now, what could
// not be seen, and for how long?**
//
// # What it is NOT
//
// It is not a health score, and there is deliberately no single number at the top.
// "9/10 healthy" would be a claim this product cannot derive from these facts, and a
// claim at the top of a page is the one a reader remembers. Four rows, each with its
// own count and its own reason, and the operator decides what they add up to.
//
// # The one thing it refuses to do
//
// **It never renders a degraded observation as a negative one.** There is no
// "no intrusions detected" anywhere on this page, because that sentence is not
// derivable from these facts — the model was not there, so nobody knows. What it
// says is "7 observations were degraded, with these reasons", which is a statement
// about coverage rather than about the world.
//
// # Why the counts come from an injected reader
//
// Every statement here is a COUNT or a MAX over a site-scoped table, so the panel
// cannot become a way to read rows a principal should not see: it reports how much,
// never which. That is true of the SQL, which is why the SQL lives in a reader this
// package does not own — `AbsenceReader` is a `Deps` field, exactly like
// `DeadLetterReader` and `ClockDriftReader`, and the same reasoning applies: the
// console states the surface it needs and the application supplies the storage that
// owns those tables.

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Sergey-Bar/vision/internal/identity"
)

// AbsenceView is one row of the panel.
type AbsenceView struct {
	// Kind is the stable machine name, so a test can assert on it and a reader can
	// search for it.
	Kind string
	// What an operator is meant to do about it. A row with no action is a
	// decoration.
	What string
	// Count is how many. Zero is a legitimate row and is still rendered: a row that
	// disappears when it is fine is a row that is only interesting when it is
	// broken, which trains a reader to ignore the table.
	Count int
	// Worst is the most serious instance, named. Empty when Count is zero.
	Worst string
	// Since is the instant the oldest instance began, and Age is derived from it.
	Since time.Time
	// Seen is whether this is MEASURED at all. A feed that has never been polled is
	// measured and unknown; the difference between "we do not know yet" and "we know
	// and it is wrong" is the whole value of the field.
	Seen bool
	// Breaking is whether this row is a finding rather than a fact.
	Breaking bool
	// SinceAge is `Now - Since`, rendered by the template rather than precomputed,
	// so the page does not carry a duration that went stale while it was open.
	SinceAge time.Duration
}

// absenceKind is the fixed row order.
//
// Fixed, and enumerated rather than accumulated: a panel whose rows appear and
// vanish with the data cannot be scanned, and a row order that changes between two
// loads of the same page makes an operator re-learn the table every time they look
// at it.
type absenceKind struct {
	name string
	what string
}

var absenceKinds = []absenceKind{
	{"vendor_feed", "The vendor is not being read. Nothing entered the pipeline from it."},
	{"ai_plane", "The model did not answer these questions. The deterministic half still decided; the model half did not."},
	{"terminal_failures", "Work reached a terminal failure. The event is not lost, but nothing further happened to it."},
	{"clock_drift", "A device's clock is out of band. Time-window predicates read the corrected time, so the window itself is wrong."},
}

// AbsenceReader is the storage the panel reads.
//
// Four named methods rather than one that returns a slice, because each kind has a
// genuinely different shape: the feed's unit is an integration, the AI plane's is an
// observation with a reason, dead letters have their own listing service already, and
// drift is a band on a device. A single `Absence() ([]AbsenceView, error)` would push
// four different queries behind one signature and make the SQL impossible to find.
type AbsenceReader interface {
	// BlindIntegrations returns the integrations that are not being read, with the
	// age of the oldest unpolled one.
	BlindIntegrations(ctx context.Context, tenantID, siteID string) (n int, worst string, oldest time.Time, err error)
	// DegradedObservations returns the observations that were degraded, and the most
	// common reason among them. The REASON is the point: "7 degraded" is a number
	// and "7 degraded because there is no runtime" is a fact.
	DegradedObservations(ctx context.Context, tenantID, siteID string) (n int, worst string, oldest time.Time, err error)
	// DriftedDevices returns the devices whose clock is out of band.
	DriftedDevices(ctx context.Context, tenantID, siteID string) (n int, worst string, oldest time.Time, err error)
}

// Absence assembles the panel for the sites a principal can see.
//
// Per-site rather than tenant-wide, and summed rather than queried once, because the
// pool is site-bound since migration 0021: there is no tenant-wide connection that
// can see a site-scoped row. That constraint is the reason this loops, and it is also
// the reason a tenant admin and a site operator running this page at the same moment
// get the same rows for their own site and neither gets the other's.
func (s *Server) Absence(ctx context.Context, reader AbsenceReader, deadLetters DeadLetterReader, tenantID string, sites []string) ([]AbsenceView, error) {
	out := make([]AbsenceView, 0, len(absenceKinds))
	for _, kind := range absenceKinds {
		v := AbsenceView{Kind: kind.name, What: kind.what, Seen: true}
		if len(sites) > 0 && reader != nil {
			var err error
			switch kind.name {
			case "vendor_feed":
				v.Count, v.Worst, v.Since, err = reader.BlindIntegrations(ctx, tenantID, sites[0])
			case "ai_plane":
				v.Count, v.Worst, v.Since, err = reader.DegradedObservations(ctx, tenantID, sites[0])
			case "clock_drift":
				v.Count, v.Worst, v.Since, err = reader.DriftedDevices(ctx, tenantID, sites[0])
			case "terminal_failures":
				// Dead letters have their own reader, and a panel that counted them
				// with its own SQL would be a second reader for a listing that
				// already exists and already has a capability of its own.
				v.Count, v.Worst, v.Since = s.deadLetterAbsence(ctx, deadLetters, tenantID, sites[0])
			}
			if err != nil {
				return nil, fmt.Errorf("absence: read %s: %w", kind.name, err)
			}
		}
		v.Breaking = v.Count > 0
		if !v.Since.IsZero() {
			v.SinceAge = s.now().Sub(v.Since)
		}
		out = append(out, v)
	}
	return out, nil
}

// deadLetterAbsence counts the terminal failures through the reader the console
// already has.
//
// `Deps.DeadLetters` is a capability of its own — an operator who may triage
// incidents should not automatically be able to replay work — so this goes through it
// rather than round-tripping the inbox tables.
func (s *Server) deadLetterAbsence(ctx context.Context, reader DeadLetterReader, tenantID, siteID string) (int, string, time.Time) {
	if reader == nil {
		return 0, "", time.Time{}
	}
	letters, err := reader.Read(ctx, tenantID, siteID, "", 200)
	if err != nil {
		return 0, "", time.Time{}
	}
	if len(letters) == 0 {
		return 0, "", time.Time{}
	}
	// The OLDEST is the worst, not the newest. A failure an hour ago that nobody
	// looked at is more concerning than one a minute ago, and a panel that surfaces
	// the most recent failure is a panel that always looks fresh.
	oldest := letters[0].UpdatedAt
	worst := letters[0].Reason
	for _, l := range letters {
		if l.UpdatedAt.Before(oldest) {
			oldest = l.UpdatedAt
			worst = l.Reason
		}
	}
	return len(letters), worst, oldest
}

// handleConsoleAbsence is the page.
//
// A GET, no side effects, and no CSRF field: there is nothing to submit, and a form
// that cannot act is a form that can only be replayed.
func (s *Server) handleConsoleAbsence(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	data := s.consoleData(r, p, "Coverage", d)

	views, err := s.Absence(r.Context(), d.Absence, d.DeadLetters, p.TenantID, p.SiteScope)
	if err != nil {
		data.Error = "The coverage panel could not be read: " + err.Error()
		s.render(w, http.StatusInternalServerError, "absence.html", data)
		return
	}
	data.Absence = views
	// Sites is on the data so the template can say WHICH sites these counts cover.
	// A count with no scope is a number nobody can check.
	data.AbsenceSites = p.SiteScope

	if d.ClockDrift != nil {
		if drift, err := d.ClockDrift.List(r.Context(), p.TenantID, 20); err == nil {
			data.Drift = drift
		}
	}
	s.render(w, http.StatusOK, "absence.html", data)
}

// handleCoverage is the same panel as JSON.
//
// The page above and this route read the SAME facts through the SAME assembly --
// `s.Absence` -- so the two cannot disagree. A console with its own coverage
// reader would be a second reader for the same four tables, and two readers for
// one set of tables drift the way two clocks do.
//
// The rows are the four fixed ones, always all four, in the same order. A row
// that disappears when it is fine is a row that is only interesting when it is
// broken, which trains a reader to ignore the table.
func (s *Server) handleCoverage(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	views, err := s.Absence(r.Context(), d.Absence, d.DeadLetters, p.TenantID, p.SiteScope)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "coverage_unavailable",
			"The coverage panel could not be read: "+err.Error(), requestID(r))
		return
	}
	rows := make([]map[string]any, 0, len(views))
	for _, v := range views {
		row := map[string]any{
			"kind":  v.Kind,
			"what":  v.What,
			"count": v.Count,
			"seen":  v.Seen,
		}
		if v.Worst != "" {
			row["worst"] = v.Worst
		}
		if !v.Since.IsZero() {
			// UTC, and the offset is explicit: the site's clock is the site's
			// clock, and a timestamp without one is not a fact anybody can place.
			row["since"] = v.Since.UTC().Format(time.RFC3339)
		}
		if v.Breaking {
			row["breaking"] = true
		}
		rows = append(rows, row)
	}
	sites := p.SiteScope
	if sites == nil {
		sites = []string{}
	}
	WriteJSON(w, http.StatusOK, map[string]any{"sites": sites, "rows": rows})
}
