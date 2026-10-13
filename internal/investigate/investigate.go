// Package investigate runs the stored, versioned answer to "how this site looks
// into this class of incident".
//
// The plan's C6 draws a line this package is built on one side of. The retrieval
// is deterministic and stored: a template declares queries, the queries run over
// the canonical record, and the hits are facts the record already held before
// the template asked. A language model may narrate those facts. It may not add
// any, and the run is produced identically whether or not one was attached —
// which is what makes `nl-replays-without-model` a gate rather than a promise.
//
// # Every hit resolves
//
// A hit that does not resolve to an incident or an evidence row is a hit nobody
// can open, and the `nl-hits-resolve` gate refuses exactly that at build time.
// The rule is enforced here, at the point of production, rather than at the
// point of display: the run drops an unresolvable event and counts what it
// dropped, so the number is visible to the caller and the alternative — a hit
// that renders as text — is not reachable.
//
// # The narration boundary
//
// `Narrator` is the only model-shaped thing in this package, and it receives a
// finished run. There is no other path by which a model's output can become a
// hit, because the hits are assembled by the same code that ran without the
// narrator attached. That is the invariant the second gate asserts.
package investigate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision/internal/eventsearch"
)

// Template is one stored, versioned investigation.
//
// The version is carried on the run rather than remembered by it, so an
// investigation can be replayed against the version that produced it — the same
// contract the decision ledger holds for everything else here.
type Template struct {
	TemplateID    string
	IncidentClass string
	Version       string
	Question      string
	Steps         []Step
}

// Step is one bounded query with a stated reason.
//
// The reason is required content, not documentation: a step nobody can explain
// is a step nobody can remove.
type Step struct {
	StepID  string
	Name    string
	Reason  string
	Query   eventsearch.Query
	MaxHits int
}

// Hit is one event a step contributed.
//
// The reference pair is the whole shape of the message: at least one of the two
// is non-empty for every hit the service emits, and the pair is what makes a
// hit openable rather than merely true.
type Hit struct {
	StepID       string
	EventID      string
	IncidentID   string
	EvidenceID   string
	NormalizedAt time.Time
}

// Resolved reports whether a hit names an incident or an evidence row.
//
// Both are empty on an event that is in no incident and has no evidence, which
// is common and honest — and exactly the event the gate refuses, because a
// narrative line that cites nothing is a narrative line that cannot be checked.
func (h Hit) Resolved() bool { return h.IncidentID != "" || h.EvidenceID != "" }

// Run is one deterministic investigation.
type Run struct {
	RunID           string
	TemplateID      string
	TemplateVersion string
	Hits            []Hit
	Unresolved      int
	StartedAt       time.Time
	CompletedAt     time.Time
	Narrative       string
	Narrated        bool
}

// Narrator is the model-shaped boundary.
//
// It receives a finished run and returns prose. It returns an error like any
// other component, and a narrator that fails costs the narrative and not the
// investigation: the run's hits are already there, and the caller decides
// whether a missing narrative is a failed run or a degraded one.
type Narrator interface {
	Narrate(ctx context.Context, run Run) (string, error)
}

// EvidenceResolver resolves the evidence behind an event. Nil means no evidence
// surface is configured, which is a real deployment state — a site whose fetch
// worker has never run — and not an error.
type EvidenceResolver interface {
	EvidenceForEvent(ctx context.Context, tenant, eventID string) (string, error)
}

// Service runs templates.
type Service struct {
	Search   *eventsearch.Service
	Evidence EvidenceResolver
}

// Run executes one template deterministically and narrates the result if a
// narrator was given.
//
// The order of the two is the whole point. The retrieval completes before the
// narrator is consulted, and the narrator receives the finished run, so the set
// of hits is a property of the template and the record rather than of whatever
// the model was in the mood to say.
func (s *Service) Run(ctx context.Context, tenant string, t Template, n Narrator) (Run, error) {
	if s == nil || s.Search == nil {
		return Run{}, errors.New("investigate: no search service")
	}
	if t.TemplateID == "" || t.Version == "" {
		return Run{}, errors.New("investigate: a template carries an id and a version or it cannot be replayed")
	}
	run := Run{
		RunID:           runID(tenant, t),
		TemplateID:      t.TemplateID,
		TemplateVersion: t.Version,
		StartedAt:       time.Now().UTC(),
	}
	for _, step := range t.Steps {
		q := step.Query
		q.Limit = step.MaxHits
		if q.Limit <= 0 {
			q.Limit = eventsearch.DefaultLimit
		}
		res, err := s.Search.Search(ctx, tenant, q)
		if err != nil {
			return run, fmt.Errorf("investigate: step %s: %w", step.StepID, err)
		}
		for _, h := range res.Hits {
			hit := Hit{StepID: step.StepID, EventID: h.EventID, IncidentID: h.IncidentID, NormalizedAt: h.NormalizedAt}
			if hit.IncidentID == "" && s.Evidence != nil {
				// The evidence surface is consulted only for an event with no
				// incident, because evidence is addressed through an incident
				// everywhere else in this product and a second path would be a
				// second thing to keep honest.
				evID, err := s.Evidence.EvidenceForEvent(ctx, tenant, h.EventID)
				if err != nil {
					return run, fmt.Errorf("investigate: step %s: resolve evidence for %s: %w", step.StepID, h.EventID, err)
				}
				hit.EvidenceID = evID
			}
			if !hit.Resolved() {
				// Dropped, and counted. The alternative is a hit that renders
				// as a line of prose citing nothing, which is the shape
				// `nl-hits-resolve` exists to refuse.
				run.Unresolved++
				continue
			}
			run.Hits = append(run.Hits, hit)
		}
	}
	run.CompletedAt = time.Now().UTC()
	if n != nil {
		narrative, err := n.Narrate(ctx, run)
		if err != nil {
			// The narrative failed; the investigation did not. The caller
			// decides what that costs, which is why the error travels with the
			// run rather than replacing it.
			run.Narrative = ""
			run.Narrated = false
			return run, fmt.Errorf("investigate: narrate: %w", err)
		}
		run.Narrative = narrative
		run.Narrated = true
	}
	return run, nil
}

// Canonical is the template's serialisable form, which is what a bundle carries
// and what the schema validates. It exists so the in-memory shape and the
// stored shape are the same shape rather than two shapes that drift.
type Canonical struct {
	APIVersion    string          `json:"apiVersion" yaml:"apiVersion"`
	Kind          string          `json:"kind" yaml:"kind"`
	TemplateID    string          `json:"templateId" yaml:"templateId"`
	IncidentClass string          `json:"incidentClass" yaml:"incidentClass"`
	Version       string          `json:"version" yaml:"version"`
	Question      string          `json:"question" yaml:"question"`
	Steps         []CanonicalStep `json:"steps" yaml:"steps"`
}

// CanonicalStep is one step as stored.
type CanonicalStep struct {
	StepID  string         `json:"stepId" yaml:"stepId"`
	Name    string         `json:"name" yaml:"name"`
	Reason  string         `json:"reason" yaml:"reason"`
	Query   CanonicalQuery `json:"query" yaml:"query"`
	MaxHits int            `json:"maxHits" yaml:"maxHits"`
}

// CanonicalQuery is the stored bound on a step. Times are RFC3339 strings
// because a stored template is JSON and a stored timestamp that cannot be
// compared is a timestamp nobody can replay.
type CanonicalQuery struct {
	SiteID        string `json:"siteId,omitempty" yaml:"siteId,omitempty"`
	EventType     string `json:"eventType,omitempty" yaml:"eventType,omitempty"`
	DeviceID      string `json:"deviceId,omitempty" yaml:"deviceId,omitempty"`
	ZoneID        string `json:"zoneId,omitempty" yaml:"zoneId,omitempty"`
	CorrelationID string `json:"correlationId,omitempty" yaml:"correlationId,omitempty"`
	IncidentID    string `json:"incidentId,omitempty" yaml:"incidentId,omitempty"`
	From          string `json:"from,omitempty" yaml:"from,omitempty"`
	To            string `json:"to,omitempty" yaml:"to,omitempty"`
}

// FromCanonical turns the stored form into the runnable one, refusing a
// template whose times will not parse rather than widening a step to the
// beginning of the table.
func FromCanonical(c Canonical) (Template, error) {
	t := Template{
		TemplateID:    c.TemplateID,
		IncidentClass: c.IncidentClass,
		Version:       c.Version,
		Question:      c.Question,
	}
	for _, cs := range c.Steps {
		step := Step{StepID: cs.StepID, Name: cs.Name, Reason: cs.Reason, MaxHits: cs.MaxHits}
		q, err := canonicalQueryToQuery(cs.Query)
		if err != nil {
			return t, fmt.Errorf("investigate: step %s: %w", cs.StepID, err)
		}
		step.Query = q
		if strings.TrimSpace(step.StepID) == "" || strings.TrimSpace(step.Reason) == "" {
			return t, fmt.Errorf("investigate: step %q needs an id and a reason", cs.Name)
		}
		t.Steps = append(t.Steps, step)
	}
	if len(t.Steps) == 0 {
		return t, errors.New("investigate: a template with no steps investigates nothing")
	}
	return t, nil
}

func canonicalQueryToQuery(c CanonicalQuery) (eventsearch.Query, error) {
	q := eventsearch.Query{
		EventType: c.EventType, DeviceID: c.DeviceID, ZoneID: c.ZoneID,
		CorrelationID: c.CorrelationID, IncidentID: c.IncidentID,
	}
	if strings.TrimSpace(c.SiteID) != "" {
		q.SiteIDs = []string{c.SiteID}
	}
	var err error
	if strings.TrimSpace(c.From) != "" {
		if q.From, err = time.Parse(time.RFC3339, c.From); err != nil {
			return q, fmt.Errorf("from is not an RFC3339 instant: %q", c.From)
		}
	}
	if strings.TrimSpace(c.To) != "" {
		if q.To, err = time.Parse(time.RFC3339, c.To); err != nil {
			return q, fmt.Errorf("to is not an RFC3339 instant: %q", c.To)
		}
	}
	return q, nil
}

func runID(tenant string, t Template) string {
	return tenant + ":" + t.TemplateID + ":" + t.Version
}
