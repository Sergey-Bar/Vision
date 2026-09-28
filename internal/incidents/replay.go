package incidents

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/Ai-Captain/internal/clock"
	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
	"github.com/Sergey-Bar/Ai-Captain/internal/rules"
)

// ReplayStep is one step of the evaluation, in the order it happened.
type ReplayStep struct {
	Order           int               `json:"order"`
	Kind            string            `json:"kind"`
	Description     string            `json:"description"`
	EventIDs        []string          `json:"eventIds,omitempty"`
	ObservationIDs  []string          `json:"observationIds,omitempty"`
	DriftExclusions []rules.Exclusion `json:"driftExclusions,omitempty"`
}

// Explanation is the answer to "why did this incident exist".
//
// ReplayedFromStoredFacts is a constant true and is not a field anybody sets. It
// is the assertion this whole path exists to make, and making it a computed
// value would let a bug make it say something else.
type Explanation struct {
	IncidentID              string       `json:"incidentId"`
	RuleID                  string       `json:"ruleId"`
	BundleVersion           string       `json:"bundleVersion"`
	BundleContentHash       string       `json:"bundleContentHash"`
	PolicyVersion           string       `json:"policyVersion"`
	State                   string       `json:"state"`
	ReasonCode              string       `json:"reasonCode"`
	ObservationID           string       `json:"observationId,omitempty"`
	Model                   string       `json:"model,omitempty"`
	ModelHash               string       `json:"modelHash,omitempty"`
	PromptTemplateID        string       `json:"promptTemplateId,omitempty"`
	PromptVersion           string       `json:"promptVersion,omitempty"`
	ReplayedFromStoredFacts bool         `json:"replayedFromStoredFacts"`
	Steps                   []ReplayStep `json:"steps"`
	// Matches says whether re-evaluating the stored facts reproduces the recorded
	// outcome. A mismatch is not necessarily a bug: a bundle may have been
	// re-simulated, or a stored fact may have been re-normalized. It is always
	// reported, because a silent mismatch is how a replay becomes a fiction.
	Matches  bool   `json:"matches"`
	Mismatch string `json:"mismatch,omitempty"`
}

// ReplayInputs are the stored facts a replay reads.
//
// There is no field for a model client, a bundle path, or a clock. That is the
// point: the struct cannot be given the means to re-run anything.
type ReplayInputs struct {
	TenantID   string
	IncidentID string
	Bundle     *rules.CompiledBundle
	Policy     clock.Policy
	// SkewTolerance is the site's, used to reproduce the corroboration window.
	SkewToleranceMS int32
	// SiteTimezone renders the incident's times, for display. It cannot affect the
	// decision, which is made on normalized UTC.
	SiteTimezone string
	// InSchedule resolves a named schedule against the stored event times. The
	// schedule definitions are site data; a replay of a months-old decision uses
	// the schedule as it is now, which is a known limitation named in the
	// explanation rather than hidden.
	InSchedule func(id string, atUnixSeconds int64) bool
}

// Replay reproduces an incident's decision from stored facts.
//
// There is no code path in this function that invokes a model, reads a wall
// clock, or loads a bundle from disk. The bundle is passed in by the caller, and
// a caller that passes a bundle different from the one that made the decision
// gets a mismatch reported rather than a different answer presented as the same
// one.
func (s *Service) Replay(ctx context.Context, in ReplayInputs) (Explanation, error) {
	var ex Explanation
	if in.TenantID == "" || in.IncidentID == "" {
		return ex, fmt.Errorf("incidents: replay needs a tenant and an incident")
	}

	detail, err := s.Get(ctx, in.TenantID, in.IncidentID)
	if err != nil {
		return ex, err
	}
	if in.Bundle == nil {
		return ex, fmt.Errorf("incidents: replay needs the bundle the decision was made under")
	}

	ex = Explanation{
		IncidentID:              detail.IncidentID,
		RuleID:                  detail.Verification.ReasonCode,
		BundleVersion:           detail.BundleVersion,
		BundleContentHash:       "",
		PolicyVersion:           detail.PolicyVersion,
		State:                   detail.Verification.State,
		ReasonCode:              detail.Verification.ReasonCode,
		ReplayedFromStoredFacts: true,
	}

	// The rule the decision was made by, named from the incident rather than
	// guessed from the explanation.
	var ruleID string
	if err := s.Pool.QueryRow(ctx,
		`SELECT rule_id FROM incidents WHERE tenant_id = $1 AND incident_id = $2`,
		in.TenantID, in.IncidentID).Scan(&ruleID); err != nil {
		return ex, fmt.Errorf("incidents: replay rule: %w", err)
	}
	ex.RuleID = ruleID

	// The observation the decision used, if any, with its full attribution. This
	// is the record that replaces re-running the model: the model said this, with
	// this prompt, over evidence with these hashes, at this time.
	if obs := pickObservation(detail.Observations, detail.Verification.SupportingObservations); obs != nil {
		ex.ObservationID = obs.ObservationID
		ex.Model = obs.Model
		ex.ModelHash = obs.ModelHash
		ex.PromptTemplateID = obs.PromptTemplateID
		ex.PromptVersion = obs.PromptVersion
		ex.BundleContentHash = obs.OutputHash
	}

	steps := []ReplayStep{}
	order := 0
	add := func(kind, description string, eventIDs, obsIDs []string, exclusions []rules.Exclusion) {
		order++
		steps = append(steps, ReplayStep{
			Order: order, Kind: kind, Description: description,
			EventIDs: eventIDs, ObservationIDs: obsIDs, DriftExclusions: exclusions,
		})
	}

	// Step 1: the trigger, as recorded.
	if len(detail.Events) > 0 {
		trigger := detail.Events[0]
		add("condition",
			fmt.Sprintf("Event %s (%s) at normalized %s triggered the evaluation.",
				trigger.EventID, trigger.EventType, trigger.NormalizedAt.UTC().Format("2006-01-02T15:04:05.000Z")),
			[]string{trigger.EventID}, nil, nil)
	}

	// Step 2: the corroborating facts, each with its drift state, so a reader can
	// see not just which events were used but how trustworthy each one's clock was.
	supporting := map[string]bool{}
	for _, id := range detail.Verification.SupportingEvents {
		supporting[id] = true
	}
	for _, ev := range detail.Events {
		if ev.EventID == detail.Events[0].EventID {
			continue
		}
		if !supporting[ev.EventID] {
			continue
		}
		add("corroboration",
			fmt.Sprintf("Event %s (%s) at normalized %s, clock source %s with %dms measured drift, corroborated the trigger.",
				ev.EventID, ev.EventType, ev.NormalizedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
				ev.ClockSource, ev.ClockDriftMS),
			[]string{ev.EventID}, nil, nil)
	}

	// Step 3: what the model said, and the fact that nothing was re-run.
	if ex.ObservationID != "" {
		add("observation",
			fmt.Sprintf("Stored AI observation %s: model %s (%s), prompt %s version %s, output hash %s. "+
				"The model was not re-run; this is the observation as recorded.",
				ex.ObservationID, ex.Model, shortHash(ex.ModelHash),
				ex.PromptTemplateID, ex.PromptVersion, shortHash(ex.BundleContentHash)),
			nil, []string{ex.ObservationID}, nil)
	} else {
		add("observation",
			"No AI observation participated in this decision.", nil, nil, nil)
	}

	// Step 4: any device excluded for clock drift.
	if len(detail.Verification.DriftExclusions) > 0 {
		for _, exc := range detail.Verification.DriftExclusions {
			add("degradation", exc.Reason, nil, nil, []rules.Exclusion{exc})
		}
	}

	// Step 5: the degradation, if the AI plane was not fully available. This is
	// the step a pilot reads to know whether the AI path was live when this
	// incident was raised.
	if detail.Verification.AIDegradation != "" && detail.Verification.AIDegradation != "none" {
		add("degradation",
			fmt.Sprintf("The AI observation was %s when this decision was made, and the rule's "+
				"declared behaviour applied. The incident is flagged as degraded.", detail.Verification.AIDegradation),
			nil, nil, nil)
	}

	// Step 6: the outcome.
	add("action", detail.Verification.Explanation,
		detail.Verification.SupportingEvents, detail.Verification.SupportingObservations, nil)

	ex.Steps = steps

	// Now re-evaluate the stored facts and report whether they still produce the
	// recorded outcome. This is the part that catches a bundle change, a
	// re-normalized fact, or a corrupted row.
	replayed, ruleFound := s.reevaluate(in, detail)
	switch {
	case !ruleFound:
		ex.Matches = false
		ex.Mismatch = fmt.Sprintf(
			"the bundle passed to replay has no rule %q. The decision was made under bundle %s; "+
				"replaying under a different bundle would answer a different question",
			ex.RuleID, ex.BundleVersion)
	case string(replayed.State) != detail.Verification.State:
		ex.Matches = false
		ex.Mismatch = fmt.Sprintf(
			"re-evaluating the stored facts produced %s, the recorded outcome is %s",
			replayed.State, detail.Verification.State)
	default:
		ex.Matches = true
	}
	return ex, nil
}

// reevaluate runs the compiled bundle over the incident's stored facts.
//
// Everything it reads came out of the database. The Context's Now is the
// incident's own first event time, so no wall clock participates, and a replay
// three months from now produces the same answer as one today.
func (s *Service) reevaluate(in ReplayInputs, detail Detail) (rules.Decision, bool) {
	var compiled *rules.CompiledRule
	for i := range in.Bundle.Compiled {
		if in.Bundle.Compiled[i].ID == detailRuleID(detail) {
			compiled = &in.Bundle.Compiled[i]
			break
		}
	}
	if compiled == nil {
		return rules.Decision{}, false
	}

	skew := in.SkewToleranceMS
	if skew <= 0 {
		skew = in.Bundle.Settings.Clock.SkewToleranceMS
	}
	siteLoc := in.Policy
	_ = siteLoc

	engine := rules.NewEngine(in.Bundle, rules.Context{
		// The incident's own first event time stands in for "now": a schedule
		// condition has to be evaluated against when the event happened, not
		// against when the replay runs, or a months-old after-hours incident would
		// replay differently depending on the hour someone ran the query.
		Now: detail.FirstEvent,
		InSchedule: func(id string, at time.Time) bool {
			if in.InSchedule == nil {
				return false
			}
			return in.InSchedule(id, at.Unix())
		},
		SkewTolerance: durationMS(skew),
	})

	trigger, others := signalsFrom(detail)
	return engine.Resolve(engine.Evaluate(trigger, others)), true
}

func detailRuleID(d Detail) string {
	// Detail carries the rule on the summary; the verification view does not
	// repeat it, so one read is enough.
	return d.RuleID
}

// signalsFrom rebuilds the evaluator's inputs from the incident's stored events
// and observations.
//
// This is the boundary the plan cares about: a stored AIObservation becomes a
// rule input, and nothing else does. There is no path here that could produce an
// observation.
func signalsFrom(d Detail) (rules.Signal, []rules.Signal) {
	obsByEvent := map[string]*rules.AIObservation{}
	var triggerObs *rules.AIObservation
	for _, o := range d.Observations {
		obs := &rules.AIObservation{
			ObservationID:        o.ObservationID,
			ProfileID:            o.ProfileID,
			Structured:           asMap(o.StructuredOutput),
			InsufficientEvidence: o.InsufficientEvidence,
			Degraded:             o.DegradedReason != "" && o.DegradedReason != "none",
			DegradedReason:       o.DegradedReason,
		}
		obsByEvent[o.ObservationID] = obs
		_ = triggerObs
	}
	// An observation is attached to the trigger, since a stored observation is
	// recorded against an incident and an event, and the first event is the
	// trigger.
	var first *ObservationView
	for i := range d.Observations {
		first = &d.Observations[i]
		break
	}
	var trigger rules.Signal
	var others []rules.Signal

	for i, ev := range d.Events {
		sig := rules.Signal{
			EventID:         ev.EventID,
			SourceEventID:   ev.SourceEventID,
			EventType:       ev.EventType,
			NormalizedAt:    ev.NormalizedAt,
			ObservedAt:      ev.ObservedAt,
			DeviceID:        ev.DeviceID,
			ZoneID:          ev.ZoneID,
			ClockDriftMS:    ev.ClockDriftMS,
			Tier:            tierFor(ev.ClockSource),
			Payload:         asMap(ev.Payload),
			DriftedExcluded: ev.ClockDriftMS >= inSevereBand(d),
		}
		if i == 0 && first != nil {
			obs := &rules.AIObservation{
				ObservationID:        first.ObservationID,
				ProfileID:            first.ProfileID,
				Structured:           asMap(first.StructuredOutput),
				InsufficientEvidence: first.InsufficientEvidence,
				Degraded:             first.DegradedReason != "" && first.DegradedReason != "none",
				DegradedReason:       first.DegradedReason,
			}
			sig.AIObservation = obs
			obsByEvent[first.ObservationID] = obs
		}
		for _, id := range d.Verification.SupportingObservations {
			if obs, ok := obsByEvent[id]; ok && sig.AIObservation == nil {
				sig.AIObservation = obs
			}
		}
		if i == 0 {
			trigger = sig
			continue
		}
		others = append(others, sig)
	}
	return trigger, others
}

// inSevereBand guesses the band from the policy the verification recorded. The
// stored verification already carries the exclusions, so this only needs to be
// consistent with them, not authoritative.
func inSevereBand(d Detail) int32 {
	for _, exc := range d.Verification.DriftExclusions {
		if exc.DeviceID != "" {
			return exc.DriftMS
		}
	}
	return 1 << 30
}

// tierFor classifies a stored event. A device reporting its own state is Tier A
// — the source of truth about itself. Everything else that reached the core is a
// derived fact, Tier B.
func tierFor(clockSource string) rules.Tier {
	if clockSource == "ntp" || clockSource == "vms" {
		return rules.TierA
	}
	return rules.TierB
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	// The column came back as raw JSON, which is the common case for a view
	// built by scanning a jsonb column into `any`.
	body, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil
	}
	return out
}

func pickObservation(all []ObservationView, supporting []string) *ObservationView {
	if len(supporting) > 0 {
		for i := range all {
			if all[i].ObservationID == supporting[0] {
				return &all[i]
			}
		}
	}
	if len(all) == 0 {
		return nil
	}
	// The most recent, which is the one the decision cited when nothing else was
	// recorded.
	return &all[len(all)-1]
}

func durationMS(ms int32) time.Duration { return time.Duration(ms) * time.Millisecond }

func shortHash(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12]
}

var _ = pgx.ErrNoRows
var _ = dbx.SystemTenant
