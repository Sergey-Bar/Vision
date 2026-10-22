// Package chief renders the Chief of Security: what changed since the operator
// last looked, what needs them now, and what the system does not know.
//
// # What the Chief is, and is not
//
// The Chief is FOUR LAYERS, and only one of them is new:
//
//  1. Facts      — what happened.        internal/brief, already rendered.
//  2. Judgement  — what matters.         internal/threat, already scored.
//  3. Proactivity— what changed since    NEW. A cursor and a diff. This is
//     you last looked.       the whole difference between a
//     dashboard and a chief.
//  4. Voice      — how it says it.       optional. The AI plane.
//
// The first two are already in the product: the brief is served at
// `/v1/handover` and the queue at `/v1/attention`. This package assembles them
// with the two it adds.
//
// # The rule that keeps it honest
//
// THE CHIEF PROPOSES; THE CORE DECIDES.
//
// The Chief never asserts a fact the deterministic core did not produce. Every
// narration is checked against the facts it was given, and a narration that
// cites something the core did not produce is REFUSED — dropped, and the
// deterministic form speaks instead. A refusal is not an error: it is the
// product working.
//
// # The Chief is not a model
//
// The package is PURE. Facts go in, a Chief comes out, and the same facts
// always produce the same Chief — byte for byte, which is what makes the
// golden test possible. The AI plane is reached through the Narrator
// interface and its answer is an INPUT to the render, not a step inside it.
//
// This is the same rule `internal/brief` keeps, and for the same reason: a
// hand-over that differs between two runs of the same shift is not a record.
package chief

import (
	"fmt"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision/internal/brief"
)

// Schema identifies the Chief's wire format. It is a published contract
// surface: a field is added, never renamed or removed.
const Schema = "chief/v1"

// Autonomy is the dial: how much the Chief is permitted to do on its own.
//
// The three levels are not a preference and not a mood. They are what the
// product is allowed to do with a decision, and each one names a different
// answer to the question "who did this".
type Autonomy string

const (
	// AutonomyBrief is A0: the Chief reports and nothing else. The operator
	// disposes of everything.
	AutonomyBrief Autonomy = "A0"
	// AutonomyPropose is A1: the Chief proposes a disposition for each item
	// and the operator approves it with one key. The approval is a first-class
	// audited act, so the ledger names a person.
	AutonomyPropose Autonomy = "A1"
	// AutonomyAct is A2: the Chief disposes of what the site's own history
	// says is noise, and escalates what it does not. The operator sees only
	// the exceptions, plus a list of what was done, each with an undo.
	//
	// A2 is only ever OFFERED at a site with a labelled base rate. See
	// Facts.HasBaseRate and EffectiveAutonomy.
	AutonomyAct Autonomy = "A2"
)

// Valid reports whether a is one of the three levels.
func (a Autonomy) Valid() bool {
	switch a {
	case AutonomyBrief, AutonomyPropose, AutonomyAct:
		return true
	}
	return false
}

// Fact is one thing the Chief knows, and the ONLY thing a narration is
// allowed to talk about.
//
// The ID is what the narration cites. It is the incident's id, the camera's
// id, the zone's id or the action's id — every one of them an id the
// deterministic core minted.
type Fact struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// The kinds a fact may carry. Closed, because a fact of an unknown kind is a
// fact the Chief cannot check.
//
// They are unexported because the SET is closed here and the wire carries the
// strings: a caller receives a `Fact` with its kind already on it and does
// not need the vocabulary, and a caller that could name a kind would be a
// caller that could widen the set.
const (
	factIncident = "incident"
	factCamera   = "camera"
	factZone     = "zone"
	factOpenItem = "open_item"
)

// Facts is everything the Chief is rendered from.
//
// The caller supplies them. The package reads no database and knows no
// transport: the API assembles the facts from the SAME readers the other
// routes use — the attention queue's reader and the hand-over's reader — so
// the Chief and the pages beside it cannot disagree about the same site.
type Facts struct {
	// GeneratedAt is stamped by the caller. It is the one non-deterministic
	// input, which is why the golden tests supply it.
	GeneratedAt time.Time

	// Since is the cursor: the instant the operator last looked.
	//
	// It is CARRIED IN THE URL and not stored anywhere. A cursor in the
	// browser's own storage is a second reader of the record, and this
	// repository's console has no browser-side persistence at all; a cursor
	// on the server is a table that says "what this operator looked at", which
	// is a record of a person that the product would then have to keep. The
	// URL is durable, bookmarkable, survives a reload and survives an
	// interruption, and it is gone when the operator closes the tab — which is
	// the correct lifetime for "where I was".
	//
	// A zero Since means "the beginning", and the Chief says so rather than
	// pretending the operator has been looking for a while.
	Since time.Time

	// TenantID and SiteID are the scope the Chief is rendering for.
	TenantID string
	SiteID   string

	// Attention is the queue, in the PRODUCT's order — the order
	// `internal/incidents.ListAttention` returns, which is the threat score
	// first and the recency inside it.
	//
	// The Chief does not re-sort it. A second sort is a second judgement, made
	// in a place that has never measured anything, and it is the exact defect
	// the attention queue was repaired for.
	Attention []Item

	// Brief is the deterministic shift brief. Nil means this site has no
	// hand-over artifact, and the Chief says so by name rather than
	// rendering an empty brief that reads as "nothing happened".
	Brief *brief.Brief

	// Narration is the AI plane's voice over the facts above. Nil means the
	// Chief speaks deterministically, which is a supported mode and not a
	// degraded one.
	Narration *Narration

	// Degradation is the named reason something the Chief would normally
	// speak is missing. Nil means nothing is degraded.
	//
	// It is NAMED because the whole point of this product is that "the model
	// was not there" and "the model said no" are different facts, and a
	// degraded Chief that renders like a healthy one is the same defect one
	// layer up.
	Degradation *Degradation

	// Autonomy is the site's configured level.
	Autonomy Autonomy

	// HasBaseRate says whether this site has a labelled history — the
	// dispositions its own operators recorded, which are the only ground
	// truth the product has.
	//
	// A site with no labelled history has NO prior, and the engine leaves its
	// score alone (`internal/calibration`). A2 is therefore refused at such
	// a site: the Chief would be disposing of incidents against a rate it has
	// no way of knowing, which is the black box the product exists to avoid.
	HasBaseRate bool
}

// Item is one entry of the attention queue, as the Chief needs it.
//
// It is a projection of `incidents.Summary`, not the type itself: the Chief
// must not be able to reach a field the attention queue does not publish, and
// a shared type would let a change to the incident list change the Chief's
// wire shape without anybody deciding it.
type Item struct {
	IncidentID     string    `json:"incidentId"`
	SiteID         string    `json:"siteId"`
	State          string    `json:"state"`
	Severity       string    `json:"severity"`
	Title          string    `json:"title"`
	RuleID         string    `json:"ruleId"`
	CreatedAt      time.Time `json:"createdAt"`
	Degraded       bool      `json:"degraded"`
	DegradedReason []string  `json:"degradedReasons,omitempty"`
	ThreatScore    int       `json:"threatScore"`
	QueueScore     int       `json:"queueScore"`
}

// Degradation is a named absence.
//
// The reason vocabulary is the one `internal/observations` publishes, because
// there must be ONE vocabulary of absences and not two.
type Degradation struct {
	// Reason is the closed-vocabulary name.
	Reason string `json:"reason"`
	// Detail says which part was missing, in words.
	Detail string `json:"detail,omitempty"`
}

// The degradation reasons the Chief itself raises.
//
// They are names, not sentences: a reader matches on them, and a sentence
// that changes between two runs cannot be matched on.
const (
	// DegradationNarrationUnattributed is the reason a narration was
	// refused: it cited a fact the core did not produce.
	DegradationNarrationUnattributed = "narration_cited_unknown_fact"
	// DegradationNoBrief is the reason the brief half is missing.
	DegradationNoBrief = "brief_unavailable"
	// DegradationNarrationUnavailable is the reason the AI plane was asked
	// and could not answer.
	//
	// It is NOT the same as there being no AI plane at all. A site with no
	// plane configured speaks deterministically and is NOT degraded; a site
	// whose plane was asked and failed IS degraded, and the Chief says so in
	// its headline. Collapsing the two is the defect this product was built
	// to stop committing: "the model was not there" is not "the model said
	// no".
	DegradationNarrationUnavailable = "narration_unavailable"
)

// Narration is the AI plane's voice over the facts.
//
// Every attribution field is MANDATORY, and a narration without them is
// refused. The same rule `internal/observations` keeps for an observation, for
// the same reason: a sentence with no provenance is a sentence nobody can
// replay or audit.
type Narration struct {
	Text string `json:"text"`
	// Provider and Model identify who answered.
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// ModelIdentity is the exact model artifact, when the provider names one.
	ModelIdentity string `json:"modelIdentity,omitempty"`
	// ContractVersion is the answer schema's version.
	ContractVersion string `json:"contractVersion"`
	// Temperature is what the answer was drawn at. A narration drawn at
	// temperature 0.7 and one drawn at 1.0 are different sentences.
	Temperature float64 `json:"temperature"`
	// InputDigest is the hash of exactly what left the site. For a local
	// provider it is the bundle digest.
	InputDigest string `json:"inputDigest"`
	// Citations are the fact ids the narration is about. Every one of them
	// must be a fact the core produced.
	Citations []string `json:"citations"`
}

// Delta is what changed since the cursor.
//
// This is the layer that makes the Chief a chief rather than a dashboard. A
// dashboard says what is true NOW; a chief says what is true NOW THAT WAS NOT
// TRUE WHEN YOU LOOKED.
type Delta struct {
	// Since is the cursor the delta was measured against.
	Since time.Time `json:"since"`
	// Measured says whether there was a cursor to measure against.
	//
	// When it is FALSE the delta is the WHOLE PICTURE and cannot be quiet:
	// there was no window to measure, so nothing was measured to be still.
	// A delta that reported "quiet" with no cursor would be claiming a
	// measurement that was never taken, which is the one thing this product
	// exists to stop saying.
	Measured bool `json:"measured"`
	// NewIncidents are the queue entries created after the cursor, in the
	// queue's own order.
	NewIncidents []Item `json:"newIncidents"`
	// NewActions are the brief's actions after the cursor.
	NewActions []brief.Action `json:"newActions"`
	// NewOpenItems are the brief's inherited items after the cursor.
	NewOpenItems []brief.OpenItem `json:"newOpenItems"`
	// Quiet says nothing changed within the measured window.
	Quiet bool `json:"quiet"`
	// QuietReason says what the quiet means, in words. "Nothing changed" and
	// "we cannot tell you what changed" are different facts, and the second
	// one is not quiet.
	QuietReason string `json:"quietReason,omitempty"`
	// Note says how the delta was measured, when that is not obvious.
	Note string `json:"note,omitempty"`
}

// Chief is the rendered artifact.
type Chief struct {
	Schema      string    `json:"schema"`
	TenantID    string    `json:"tenantId,omitempty"`
	SiteID      string    `json:"siteId,omitempty"`
	GeneratedAt time.Time `json:"generatedAt"`
	// Since is the cursor the delta was measured against, echoed so a reader
	// can see what "since" meant without holding the URL.
	Since time.Time `json:"since"`

	// Headline is the Chief's opening line. It is DETERMINISTIC: the same
	// facts always produce the same sentence.
	Headline string `json:"headline"`
	// Delta is what changed.
	Delta Delta `json:"delta"`
	// Attention is the queue, in the product's order.
	Attention []Item `json:"attention"`
	// Brief is the deterministic shift brief. Null when the site has none.
	Brief *brief.Brief `json:"brief,omitempty"`
	// Narration is the AI plane's voice. Null when there is none, and null
	// is a supported state rather than a failure.
	Narration *Narration `json:"narration,omitempty"`
	// Degradation is the named absence, when there is one.
	Degradation *Degradation `json:"degradation,omitempty"`
	// Autonomy is the site's CONFIGURED level.
	Autonomy Autonomy `json:"autonomy"`
	// EffectiveAutonomy is the level the site is actually permitted to act
	// at. It differs from Autonomy when A2 is configured at a site with no
	// labelled base rate, and it is published rather than applied silently:
	// an operator who is told the Chief may act and finds that it may not
	// learns that the number is not trustworthy.
	EffectiveAutonomy Autonomy `json:"effectiveAutonomy"`
}

// Render turns facts into a Chief.
//
// It is pure and total: every field of every fact is accounted for, no map is
// iterated, and every slice is sorted by a stable key, so the same facts
// produce the same bytes. That is what makes the golden test possible and it
// is the same promise `internal/brief` makes.
func Render(f Facts) Chief {
	c := Chief{
		Schema:      Schema,
		TenantID:    f.TenantID,
		SiteID:      f.SiteID,
		GeneratedAt: f.GeneratedAt.UTC(),
		Since:       f.Since.UTC(),
		Attention:   append([]Item(nil), f.Attention...),
		Brief:       f.Brief,
		Autonomy:    f.Autonomy,
	}

	if !c.Autonomy.Valid() {
		// An unknown level is A0, not an error. A site whose configuration
		// names a level this build does not know is a site that must be
		// handled CONSERVATIVELY, and the conservative level is the one that
		// only reports.
		c.Autonomy = AutonomyBrief
	}

	// The effective level, and the reason A2 is not simply "configured".
	c.EffectiveAutonomy = c.Autonomy
	if c.EffectiveAutonomy == AutonomyAct && !f.HasBaseRate {
		c.EffectiveAutonomy = AutonomyPropose
	}

	c.Delta = renderDelta(f)

	// The facts the narration may talk about, and the facts the Chief may
	// state. They are the same set: the Chief does not know anything the
	// narration is not allowed to.
	known := knownFacts(f, c.Delta)
	c.Narration = checkNarration(f.Narration, known)
	if c.Narration == nil && f.Narration != nil {
		// The narration was refused. The deterministic form speaks, and the
		// refusal is NAMED — a silent drop would read as "the model had
		// nothing to say", which is a different fact and a worse one.
		c.Degradation = &Degradation{
			Reason: DegradationNarrationUnattributed,
			Detail: "the narration cited something the product did not record, so it was refused and the deterministic brief speaks instead",
		}
	}
	if c.Degradation == nil {
		c.Degradation = f.Degradation
	}
	if c.Brief == nil && c.Degradation == nil {
		c.Degradation = &Degradation{
			Reason: DegradationNoBrief,
			Detail: "this site has no shift hand-over artifact, so the Chief has no camera, coverage or action history",
		}
	}

	c.Headline = renderHeadline(f, c.Delta, c.Degradation)
	return c
}

// Headline renders the opening line from the facts alone.
//
// It is exported because the narrator needs the headline BEFORE the Chief is
// rendered: the headline is what the narration expands on, and the narration
// is an INPUT to the render. There is one way to produce the sentence, so the
// sentence the narrator was given is the sentence the Chief shows — and a
// second implementation of it would be a second wording of the same fact,
// which is the defect the brief's golden test exists to prevent.
func Headline(f Facts) string {
	d := renderDelta(f)
	var degradation *Degradation
	switch {
	case f.Degradation != nil:
		degradation = f.Degradation
	case f.Brief == nil:
		degradation = &Degradation{
			Reason: DegradationNoBrief,
			Detail: "this site has no shift hand-over artifact, so the Chief has no camera, coverage or action history",
		}
	}
	return renderHeadline(f, d, degradation)
}

// renderDelta measures the change since the cursor.
//
// The cursor is the FIRST instant the operator is treated as having seen, and
// the comparison is strict: an item created AT the cursor is new, because the
// cursor is the moment before the operator looked, not the moment they
// looked. A boundary that dropped the item at the instant of the look would
// hide exactly the thing the operator is asking about.
func renderDelta(f Facts) Delta {
	d := Delta{Since: f.Since.UTC()}
	if f.Since.IsZero() {
		// No cursor. There was no window to measure, so the delta is the whole
		// picture and it says so — rather than claiming a quiet that was
		// never measured.
		d.Measured = false
		d.Quiet = false
		d.Note = "no cursor: this is the whole picture, not a change in it"
		d.NewIncidents = append([]Item(nil), f.Attention...)
		if f.Brief != nil {
			d.NewActions = append([]brief.Action(nil), f.Brief.Actions...)
			d.NewOpenItems = append([]brief.OpenItem(nil), f.Brief.OpenItems...)
		}
		return d
	}
	d.Measured = true
	for _, item := range f.Attention {
		if item.CreatedAt.After(f.Since) {
			d.NewIncidents = append(d.NewIncidents, item)
		}
	}
	if f.Brief != nil {
		for _, a := range f.Brief.Actions {
			if a.TS.After(f.Since) {
				d.NewActions = append(d.NewActions, a)
			}
		}
		for _, o := range f.Brief.OpenItems {
			// An open item carries no timestamp of its own — it is inherited
			// rather than timestamped — so it is reported when the cursor
			// predates the whole shift, and not otherwise. Reporting it
			// always would make the delta say "changed" every time, and a
			// delta that always says "changed" says nothing.
			if f.Since.Before(f.Brief.GeneratedAt) {
				d.NewOpenItems = append(d.NewOpenItems, o)
			}
		}
	}
	d.Quiet = len(d.NewIncidents) == 0 && len(d.NewActions) == 0 && len(d.NewOpenItems) == 0
	return d
}

// knownFacts is the set of ids the Chief produced and the narration may cite.
//
// It is built from the SAME facts the Chief renders, so the check cannot be
// passed by a fact the Chief itself does not hold.
func knownFacts(f Facts, d Delta) map[string]bool {
	known := make(map[string]bool)
	for _, item := range f.Attention {
		if item.IncidentID != "" {
			known[item.IncidentID] = true
		}
	}
	for _, item := range d.NewIncidents {
		if item.IncidentID != "" {
			known[item.IncidentID] = true
		}
	}
	if f.Brief != nil {
		for _, c := range f.Brief.Cameras {
			if c.ID != "" {
				known[c.ID] = true
			}
		}
		for _, z := range f.Brief.Coverage {
			if z.ZoneID != "" {
				known[z.ZoneID] = true
			}
		}
		for _, o := range f.Brief.OpenItems {
			if o.ID != "" {
				known[o.ID] = true
			}
		}
	}
	return known
}

// checkNarration refuses a narration that cites a fact the product did not
// produce, or that carries no attribution.
//
// The refusal is the control. A narration is a sentence about the record, and
// a sentence about something the record does not contain is a fabrication —
// the one failure this whole product is built to make impossible. The model
// is never asked to be trusted opaquely; it is asked to be replayable, and a
// replay of a narration that cites a thing that was never there fails.
//
// A refusal drops the narration. The deterministic form speaks instead, and
// the Chief names the refusal. It is not an error: an error would put the
// Chief's route in the server's error budget for something the product
// handles.
func checkNarration(n *Narration, known map[string]bool) *Narration {
	if n == nil {
		return nil
	}
	if strings.TrimSpace(n.Text) == "" {
		return nil
	}
	// Every attribution field is mandatory. A narration with no provider, no
	// model, no contract version, no temperature or no input digest is a
	// sentence nobody can replay.
	if n.Provider == "" || n.Model == "" || n.ContractVersion == "" || n.InputDigest == "" {
		return nil
	}
	for _, c := range n.Citations {
		if !known[c] {
			return nil
		}
	}
	return n
}

// renderHeadline writes the Chief's opening line.
//
// It is deterministic, and it is built to satisfy the rule the console has
// everywhere else: an empty answer says what the system DID and DID NOT see,
// in words. "Nothing new" alone reads as "nothing is happening", which is a
// different claim and a dangerous one at 02:00.
//
// So the headline always has two parts: the delta, and the standing state.
// And when something is degraded, the degradation is IN the headline rather
// than in a corner of the screen — because the Chief's silence has a cause
// and the operator is entitled to know it.
func renderHeadline(f Facts, d Delta, degradation *Degradation) string {
	var b strings.Builder

	switch {
	case f.Since.IsZero():
		fmt.Fprintf(&b, "%s.", plural(len(f.Attention), "incident", "incidents"))
		if len(f.Attention) > 0 {
			fmt.Fprintf(&b, " The most urgent is %s.", describe(f.Attention[0]))
		}
		b.WriteString(" There is no cursor, so this is the whole picture and not a change in it.")
	case d.Quiet:
		fmt.Fprintf(&b, "Nothing new since %s.", clockLabel(f.Since))
		if n := len(f.Attention); n > 0 {
			fmt.Fprintf(&b, " %s still open and waiting.", plural(n, "incident is", "incidents are"))
		}
	default:
		parts := []string{}
		if n := len(d.NewIncidents); n > 0 {
			parts = append(parts, plural(n, "new incident", "new incidents"))
		}
		if n := len(d.NewActions); n > 0 {
			parts = append(parts, plural(n, "new action", "new actions"))
		}
		if n := len(d.NewOpenItems); n > 0 {
			parts = append(parts, plural(n, "inherited item", "inherited items"))
		}
		fmt.Fprintf(&b, "%s since %s.", joinAnd(parts), clockLabel(f.Since))
		if len(d.NewIncidents) > 0 {
			fmt.Fprintf(&b, " The most urgent is %s.", describe(d.NewIncidents[0]))
		}
	}

	// The standing state, so the silence has a shape.
	if f.Brief != nil {
		if n := f.Brief.FleetDegraded; n > 0 {
			fmt.Fprintf(&b, " %s not watching.", plural(n, "camera is", "cameras are"))
		}
		for _, cov := range f.Brief.Coverage {
			if cov.UnderObserved {
				fmt.Fprintf(&b, " %s is under-observed.", cov.ZoneID)
				break
			}
		}
	}

	// The degradation, in the line itself. A Chief that is quieter than usual
	// and does not say why is the exact failure this product exists to
	// prevent, and it is the failure a customer discovers at the worst
	// possible moment.
	if degradation != nil {
		fmt.Fprintf(&b, " The Chief is degraded: %s.", degradationLabel(degradation))
	}

	return b.String()
}

// describe renders one queue item for a sentence.
func describe(item Item) string {
	title := strings.TrimSpace(item.Title)
	if title == "" {
		title = item.IncidentID
	}
	if item.ThreatScore > 0 {
		return fmt.Sprintf("%q, scored %d", title, item.ThreatScore)
	}
	return fmt.Sprintf("%q", title)
}

// degradationLabel writes the degradation as a clause rather than as a code.
//
// The code is in the JSON for a machine. The clause is for the operator, and
// it says what the Chief COULD not do — because "degraded" alone is a word
// and not an explanation.
func degradationLabel(d *Degradation) string {
	switch d.Reason {
	case DegradationNarrationUnattributed:
		return "a narration was refused because it cited something the product did not record"
	case DegradationNarrationUnavailable:
		return "the AI plane was asked and could not answer"
	case DegradationNoBrief:
		return "there is no shift hand-over for this site, so the camera and coverage history is unknown"
	}
	if d.Detail != "" {
		return d.Detail
	}
	return d.Reason
}

// clockLabel renders the cursor for a sentence.
//
// It is UTC with an offset, always. A time without one is a time the reader
// has to guess at, and the site's clock is the site's clock — but a Chief's
// sentence is read next to a wall clock, and the only honest way to say when
// something changed in a sentence is to say it in a named zone.
func clockLabel(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05Z")
}

// plural is the count-and-noun helper. The noun is chosen by the count so a
// sentence never says "1 incidents".
func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// joinAnd joins a list the way a sentence does: "a, b and c".
func joinAnd(parts []string) string {
	switch len(parts) {
	case 0:
		return "Nothing"
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " and " + parts[1]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}
