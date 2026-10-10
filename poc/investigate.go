package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/airuntime"
	"github.com/Sergey-Bar/Ai-Captain/internal/dutylog"
	"github.com/Sergey-Bar/Ai-Captain/internal/observations"
)

// The investigator: the GPU finale, and the only role the AI plane ships.
//
// The July 2026 surveys' complaint is the days of video an operator combs
// through after an event. This answers questions over the facts the pipeline
// already stored — incidents, their reason chains, the duty log's lines, a
// camera's health — and cites the ids it used. Three rules govern it:
//
//  1. **It investigates. It never decides.** An answer is a reading of stored
//     facts, not an action; the playbook's decision stands on the
//     deterministic core and nothing here changes it.
//  2. **Every answer is checkable.** The citations are ids of facts actually
//     sent to the plane. An answer citing something it was not shown is
//     refused below, exactly as runtime.py refuses it.
//  3. **No GPU is a named degradation, never an empty box.** With no engine
//     bound the caller gets the reason, which the dashboard renders.
//
// The plane is the existing airuntime seam: the same client, the same wire
// shape, one more profile. Nothing new runs in the product process.

// InvestigatorProfile is the profile id the runtime binds the investigate role
// to. It is one constant the observations registry keys on and this seam sends,
// so a rename is one edit rather than three.
const InvestigatorProfile = observations.ProfileInvestigate

// InvestigatorSchema is the field set an investigate answer may carry.
//
// It is derived from the registry's own declaration of the profile rather than
// written out a second time here: the profile's field set, its required subset
// and the id are one declaration, and a copy that could drift from it is how a
// plane-side refusal becomes opaque. TestTheInvestigatorContractAgreesWithThePlane
// asserts the plane declares the same three things.
var InvestigatorSchema = investigateSchema()

func investigateSchema() map[string]any {
	profile, ok := observations.M1Profiles[InvestigatorProfile]
	if !ok {
		// The registry is the source of truth. A missing profile means this
		// build cannot answer anything, and the caller's error would be an
		// empty schema — so fail here, in the shape it can name.
		panic("investigate: the observations registry has no " + InvestigatorProfile + " profile")
	}
	properties := make(map[string]any, len(profile.Fields))
	for field := range profile.Fields {
		properties[field] = map[string]any{"type": "string"}
	}
	required := profile.Required
	if len(required) == 0 {
		for field := range profile.Fields {
			required = append(required, field)
		}
		sort.Strings(required)
	}
	return map[string]any{
		"properties": properties,
		"required":   required,
	}
}

// Fact is one thing the investigator may be shown, with the id an answer must
// cite. Text is what the model reads; its hash is what the plane verifies, so
// an answer provably came from these bytes.
type Fact struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// Answer is one investigate outcome, in the shape the Q&A box renders.
type Answer struct {
	Question  string   `json:"question"`
	Answer    string   `json:"answer,omitempty"`
	Citations []string `json:"citations,omitempty"`
	Facts     []string `json:"facts_considered"`
	// Degraded is the plane's own statement that it could not answer, and
	// DegradedReason is why. The UI renders both; it never renders an empty box.
	Degraded       bool   `json:"degraded"`
	DegradedReason string `json:"degraded_reason,omitempty"`
	DegradedDetail string `json:"degraded_detail,omitempty"`
	Model          string `json:"model,omitempty"`
	ModelIdentity  string `json:"model_identity,omitempty"`
	LatencyMS      int64  `json:"latency_ms,omitempty"`
}

// investigatorFacts assembles the facts a question may be answered from: the
// shift's correlated incidents with their reason chains, the duty log's lines
// and the fleet's camera health. Only facts the pipeline already holds — a
// question about anything else is answered with what is here, or refused.
func (s *StreamIngest) investigatorFacts(now time.Time) []Fact {
	facts := make([]Fact, 0, 16)

	if s.siteModel != nil && s.linker != nil {
		// The linker's maps are written under passMu by the autonomy pass, so
		// this read takes it too — an unsynchronized read beside a concurrent
		// write is a fatal map error, not a rare race.
		//
		// Only incidents that are still live are facts worth sending: the
		// linker never prunes, so walking everything it ever built would cost
		// O(every incident of the process's life × their anomalies) per
		// question, the overwhelming majority of it discarded by the cap
		// below.
		s.passMu.Lock()
		for _, inc := range s.linker.AllIncidents() {
			if inc.State == "RESOLVED" || inc.State == "OVERRIDDEN" {
				continue
			}
			facts = append(facts, Fact{
				ID: inc.ID,
				Text: fmt.Sprintf("incident %s: state=%s action=%s autonomy=%s threat_score=%d cameras=%s anomalies=%s reason=%s",
					inc.ID, inc.State, inc.Action, inc.AutonomyState, inc.ThreatScore,
					strings.Join(inc.CameraIDs, "+"),
					strings.Join(anomalyTypes(inc), "+"),
					inc.ReasonChain.Why),
			})
		}
		s.passMu.Unlock()
	}
	if s.handover != nil && s.handover.duty != nil {
		// The events come from the writer's own ring, which mirrors the file,
		// and are capped BEFORE a Fact is built for each: re-parsing the whole
		// shift per question and then discarding all but the last 200 is work
		// the ring already did. The cap is the same bound every other surface
		// of this artifact uses.
		for _, ev := range tailEvents(s.handover.duty.Events(), dutylog.MaxDumpEvents) {
			facts = append(facts, Fact{
				ID: fmt.Sprintf("%s#%d", ev.Subject, ev.Seq),
				Text: fmt.Sprintf("duty-log %s by %s on %s: %s %s (%s)",
					ev.Kind, ev.Actor, ev.Subject, ev.Action, ev.Reason, ev.TS.UTC().Format(time.RFC3339)),
			})
		}
	}
	for _, c := range s.fleetStatus(now) {
		facts = append(facts, Fact{
			ID: c.ID,
			Text: fmt.Sprintf("camera %s (%s): state=%s frames=%d %s",
				c.ID, c.Name, c.State, c.Frames, c.Reason),
		})
	}

	// Facts cap: the question is answered from the most recent slice of the
	// shift, not from an unbounded log. The cap is stated here rather than
	// discovered as an OOM, and it bounds work as well as retention — the
	// duty-log events are capped above, before a Fact is built for each.
	const maxFacts = 200
	if len(facts) > maxFacts {
		facts = facts[len(facts)-maxFacts:]
	}
	return facts
}

// tailEvents caps a list to its most recent n events.
func tailEvents(events []dutylog.Event, n int) []dutylog.Event {
	if len(events) <= n {
		return events
	}
	return events[len(events)-n:]
}

// Investigate answers one question from the facts at hand. Every failure is a
// named degradation the caller can render; none of them is an empty box.
func (s *StreamIngest) Investigate(ctx context.Context, question string) Answer {
	out := Answer{Question: question}
	if strings.TrimSpace(question) == "" {
		out.Degraded = true
		out.DegradedReason = "absent"
		out.DegradedDetail = "a question is required; an empty question is not an empty answer"
		return out
	}
	if s.aiClient == nil {
		out.Degraded = true
		out.DegradedReason = "no_runtime"
		out.DegradedDetail = "no AI runtime is configured; set ai_runtime.endpoint to the plane's address"
		return out
	}

	facts := s.investigatorFacts(time.Now().UTC())
	if len(facts) == 0 {
		out.Degraded = true
		out.DegradedReason = "absent"
		out.DegradedDetail = "this shift has no stored facts yet; investigate works over what the pipeline recorded"
		return out
	}
	for _, f := range facts {
		out.Facts = append(out.Facts, f.ID)
	}

	evidence := make([]airuntime.Evidence, 0, len(facts))
	for _, f := range facts {
		data := []byte(f.Text)
		sum := sha256.Sum256(data)
		evidence = append(evidence, airuntime.Evidence{
			ID:          f.ID,
			SHA256:      hex.EncodeToString(sum[:]),
			ContentType: "text/plain",
			Data:        data,
		})
	}

	res, err := s.aiClient.Do(ctx, airuntime.Request{
		ObservationID: investigationID(facts),
		ProfileID:     InvestigatorProfile,
		Evidence:      evidence,
		ProfileSchema: InvestigatorSchema,
	})
	// A transport failure and a degraded answer are the same thing here: the
	// plane answered that it could not answer, and the reason is what the
	// operator reads. The Go client's Do returns both a result and an error,
	// so the result's reason is preferred and the error is the fallback.
	if res.Degraded {
		out.Degraded = true
		out.DegradedReason = string(res.DegradedReason)
		out.DegradedDetail = res.DegradedDetail
		if err != nil && out.DegradedReason == "" {
			out.DegradedReason = "error"
			out.DegradedDetail = err.Error()
		}
		return out
	}
	if err != nil {
		out.Degraded = true
		out.DegradedReason = "error"
		out.DegradedDetail = err.Error()
		return out
	}

	answer, _ := res.Structured["answer"].(string)
	citations := citationStrings(res.Structured["citations"])
	if answer == "" {
		out.Degraded = true
		out.DegradedReason = "error"
		out.DegradedDetail = "the plane answered with no answer field; the field set is the contract"
		return out
	}
	// The citation check, on this side too: an answer that names a fact it was
	// not shown is refused rather than displayed.
	supplied := map[string]bool{}
	for _, f := range facts {
		supplied[f.ID] = true
	}
	for _, c := range citations {
		if !supplied[c] {
			out.Degraded = true
			out.DegradedReason = "error"
			out.DegradedDetail = fmt.Sprintf("the plane cited %s, which was not among the facts it was shown", c)
			return out
		}
	}
	if len(citations) == 0 {
		out.Degraded = true
		out.DegradedReason = "absent"
		out.DegradedDetail = "the plane answered without citing any fact; an answer nobody can check is not kept"
		return out
	}

	sort.Strings(citations)
	out.Answer = answer
	out.Citations = citations
	out.Model = res.Model
	out.ModelIdentity = res.ModelIdentity
	out.LatencyMS = res.LatencyMS
	return out
}

// citationStrings reads the citations field, which a JSON array arrives as.
func citationStrings(v any) []string {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// investigationID names the observation after the facts it rests on, not after
// the question, so the same question over the same facts is the same
// observation and a re-ask is idempotent rather than a second observation.
func investigationID(facts []Fact) string {
	ids := make([]string, 0, len(facts))
	for _, f := range facts {
		ids = append(ids, f.ID)
	}
	sort.Strings(ids)
	sum := sha256.Sum256([]byte(strings.Join(ids, "\n")))
	return "investigate-" + hex.EncodeToString(sum[:])[:16]
}
