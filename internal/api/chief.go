package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision/internal/chief"
	"github.com/Sergey-Bar/vision/internal/identity"
)

// The Chief of Security.
//
// The handler is thin and it is thin ON PURPOSE. It assembles facts from the
// readers the product already has and hands them to `internal/chief`, which
// is pure. The Chief's judgement is not made here, and this file must not
// grow a second opinion about it.
//
// # The two readers, and why there are exactly two
//
//	`d.Incidents.ListAttention` — the same reader `/v1/attention` uses, so
//	the Chief's queue and the queue on the screen are the same queue in the
//	same order.
//
//	`d.Handover.Read` — the same reader `/v1/handover` uses, so the Chief's
//	brief and the hand-over on the screen are the same brief.
//
// A third reader for the same tables would be a third opinion about them,
// and the console's own rule is that two readers of one set of tables is a
// defect. The Chief reads through the readers that already exist.

// ChiefPrior answers whether a site has a labelled history.
//
// It is the question the autonomy dial asks before it lets the Chief dispose
// of an incident by itself. A site whose operators have never recorded
// whether an incident was real has NO base rate, and a Chief disposing of
// incidents against a rate it cannot know is the black box this product
// exists to make impossible.
//
// A nil reader means NO. A missing reader must never be the reason a site
// is given more autonomy than it has earned.
type ChiefPrior interface {
	// SiteHasHistory reports whether the site has any labelled history.
	SiteHasHistory(ctx context.Context, site string) (bool, error)
}

// handleChief answers: what changed since you last looked, what needs you
// now, and what does the system not know.
//
// # The cursor
//
// `since` is a query parameter. It is NOT stored.
//
// A cursor in the browser's storage would be a second reader of the record,
// and this console has no browser-side persistence at all. A cursor on the
// server would be a table saying "what this operator looked at", which is a
// record of a person that the product would then have to keep, and the
// ledger would have to chain. The URL is durable, bookmarkable, survives a
// reload and survives an interruption, and it is gone when the operator
// closes the tab — which is the correct lifetime for "where I was".
//
// A malformed cursor is REFUSED with the reason. Parsing it as the zero time
// would widen the window to everything, which is the one answer this route
// must never give: it would report "everything is new" for a request that
// asked "what is new".
func (s *Server) handleChief(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	q := r.URL.Query()
	since, err := parseChiefCursor(q.Get("since"))
	if err != nil {
		WriteError(w, http.StatusBadRequest, "bad_cursor", err.Error(), requestID(r))
		return
	}
	limit := parseLimit(q.Get("limit"), 20, 50)

	// The queue, from the product's own reader, in the product's own order.
	scope := strings.Join(p.SiteScope, ",")
	items, err := d.Incidents.ListAttention(r.Context(), p.TenantID, scope, limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "list_failed", err.Error(), requestID(r))
		return
	}

	// The brief, from the hand-over's own reader. Its absence is a NAMED
	// degradation rather than an error: a site with no shift artifact is a
	// site the Chief can still speak about, and it must say that the camera
	// and coverage history is unknown rather than rendering an empty brief
	// that reads as "nothing happened".
	view, briefErr := s.chiefBrief(r.Context(), p, d)

	attention := make([]chief.Item, 0, len(items))
	for _, item := range items {
		attention = append(attention, chief.Item{
			IncidentID:     item.IncidentID,
			SiteID:         item.SiteID,
			State:          item.State,
			Severity:       item.Severity,
			Title:          item.Title,
			RuleID:         item.RuleID,
			CreatedAt:      item.CreatedAt,
			Degraded:       item.Degraded,
			DegradedReason: item.DegradedReasons,
			ThreatScore:    item.ThreatScore,
			QueueScore:     item.QueueScore,
		})
	}

	// The site the Chief is speaking about: the hand-over's site when there
	// is one the principal may see, and the principal's own first site
	// otherwise.
	siteID := firstSite(p)
	if briefErr == nil && view != nil && view.Brief != nil && view.Brief.SiteID != "" {
		siteID = view.Brief.SiteID
	}

	facts := chief.Facts{
		GeneratedAt: s.clock(),
		Since:       since,
		TenantID:    p.TenantID,
		SiteID:      siteID,
		Attention:   attention,
		Autonomy:    chief.AutonomyBrief,
	}
	if briefErr == nil && view != nil && view.Brief != nil {
		facts.Brief = view.Brief
	}

	// The autonomy level, and whether the site has earned it.
	configured, earned := s.chiefAutonomy(r.Context(), d, siteID)
	facts.Autonomy = configured
	facts.HasBaseRate = earned

	// The narration is an INPUT to the render, not a step inside it, and the
	// render is what checks it. That is the whole point: the Chief does not
	// ask the model to be careful, it refuses the answer if the model was
	// not.
	narration, degradation := s.chiefNarration(r.Context(), p, d, facts, attention)
	facts.Narration = narration
	facts.Degradation = degradation

	WriteJSON(w, http.StatusOK, chief.Render(facts))
}

// chiefBrief reads the brief through the hand-over's reader.
//
// The site scope is checked against the site the reader READS, not against
// the principal's own scope, for the reason the hand-over handler gives:
// the reader serves the deployment's configured site, so that is the site the
// authorization decision has to be made against. A 404 rather than a 403,
// because a 403 confirms the artifact exists.
func (s *Server) chiefBrief(ctx context.Context, p identity.Principal, d Deps) (*HandoverView, error) {
	if d.Handover == nil {
		return nil, nil
	}
	siteID := d.Handover.SiteID()
	if !p.CanSeeSite(siteID) {
		return nil, nil
	}
	view, err := d.Handover.Read(ctx, p.TenantID, siteID)
	if err != nil {
		return nil, err
	}
	return &view, nil
}

// chiefNarration asks the AI plane for the Chief's voice.
//
// Three outcomes, and they are three DIFFERENT facts:
//
//  1. A narration. It is returned and the render checks it against the
//     facts. A narration citing something the product did not record is
//     refused there.
//  2. A DEGRADED answer. The plane was asked and could not answer. The
//     reason is NAMED, so the Chief says it is quieter than usual and why.
//  3. No narrator at all. The plane is not configured for this install.
//     The Chief speaks deterministically, which is a supported mode.
//
// The third is NOT the second, and collapsing them is the defect this
// repository keeps refusing: "the model was not there" is not "the model said
// no".
func (s *Server) chiefNarration(ctx context.Context, p identity.Principal, d Deps,
	facts chief.Facts, attention []chief.Item) (*chief.Narration, *chief.Degradation) {

	if d.ChiefNarrator == nil {
		return nil, nil
	}

	req := chief.NarrationRequest{
		TenantID: p.TenantID,
		SiteID:   facts.SiteID,
		Since:    facts.Since,
		// The headline is computed from the facts ALONE, before the
		// narration exists — because the narration is an input to the
		// render and the headline is what the narration expands on. There is
		// one way to produce it, so the sentence the narrator was given is
		// the sentence the Chief shows.
		Headline: chief.Headline(facts),
	}
	// The closed set of facts the answer may cite, built from the same facts
	// the Chief renders.
	req.Facts = chief.FactsFrom(attention, nil, nil, nil)
	if facts.Brief != nil {
		cameras := make([]string, 0, len(facts.Brief.Cameras))
		for _, c := range facts.Brief.Cameras {
			cameras = append(cameras, c.ID)
		}
		zones := make([]string, 0, len(facts.Brief.Coverage))
		for _, z := range facts.Brief.Coverage {
			zones = append(zones, z.ZoneID)
		}
		req.Facts = append(req.Facts, chief.FactsFrom(nil, cameras, zones, nil)...)
	}

	narration, err := d.ChiefNarrator.Narrate(ctx, req)
	if err != nil {
		return nil, &chief.Degradation{
			Reason: chief.DegradationNarrationUnavailable,
			Detail: "the AI plane could not answer: " + err.Error(),
		}
	}
	if narration == nil {
		// NO VOICE, which is not the same as a voice that failed.
		//
		// A site with no provider chain has no plane, and the Chief speaks
		// DETERMINISTICALLY over it. That is a supported configuration and
		// it is NOT degraded: "the model was not there" and "the model said
		// no" are different facts, and the product does not collapse them.
		return nil, nil
	}
	if narration == nil {
		return nil, &chief.Degradation{
			Reason: chief.DegradationNarrationUnavailable,
			Detail: "the AI plane was asked and could not answer",
		}
	}
	return narration, nil
}

// chiefAutonomy is the site's configured level and whether it has earned the
// one it asked for.
//
// The configured level comes from the site's own configuration and never from
// a request. Whether the site has EARNED it is a question about the site's
// own labelled history, and the answer is no when the history cannot be
// read: a missing reader must never be the reason a site is given more
// autonomy than it has earned.
func (s *Server) chiefAutonomy(ctx context.Context, d Deps, siteID string) (chief.Autonomy, bool) {
	configured := d.ChiefAutonomy
	if !configured.Valid() {
		// An unconfigured site, or a configuration naming a level this build
		// does not know, is A0. The conservative level is the one that only
		// reports.
		configured = chief.AutonomyBrief
	}
	// A0 and A1 do not need a base rate. Only A2 disposes of anything by
	// itself, and only A2 is earned.
	if configured != chief.AutonomyAct {
		return configured, true
	}
	if d.ChiefPrior == nil || siteID == "" {
		return configured, false
	}
	has, err := d.ChiefPrior.SiteHasHistory(ctx, siteID)
	if err != nil {
		// A history that cannot be read is an ABSENCE, not a negative
		// observation about the site. The Chief falls back to A1 and says
		// why, rather than disposing of an incident against a rate it does
		// not have.
		return configured, false
	}
	return configured, has
}

// parseChiefCursor reads the `since` parameter.
//
// An absent cursor is the zero time, and the Chief reports the whole picture
// and says it had no cursor. A present one must be an RFC3339 instant: the
// cursor is compared against timestamps, and a cursor that is not a timestamp
// cannot be compared against one.
func parseChiefCursor(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, &cursorError{raw: raw}
	}
	return t.UTC(), nil
}

// cursorError is the malformed-cursor refusal. It is its own type so the
// message names the parameter rather than the parser.
type cursorError struct{ raw string }

func (e *cursorError) Error() string {
	return "since is not an RFC3339 instant: " + e.raw +
		". The cursor is compared against timestamps, and a cursor that is not a timestamp cannot be compared against one."
}
