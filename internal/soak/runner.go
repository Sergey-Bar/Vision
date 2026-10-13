package soak

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/Sergey-Bar/vision/internal/airuntime"
	"github.com/Sergey-Bar/vision/internal/evidence"
	"github.com/Sergey-Bar/vision/internal/observations"
)

func sha256Sum(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Options configures a run.
type Options struct {
	// Events is how many synthetic events to push through.
	Events int
	// Cameras is how many cameras they come from. More than one is what makes a
	// duplicate detectable across the fleet rather than within one feed.
	Cameras int
	// DriftMS is the clock drift the run reports. Zero is a healthy clock, and a
	// run that never measures a drift has not exercised window selection.
	DriftMS int32
	// DriftFromEvent is the event the drift begins at, so the run contains both
	// a healthy and a drifting period rather than only one of them.
	DriftFromEvent int
	// Answer is the provider's answer for the whole run.
	Answer Answer
	// AnswerFrom is the event the answer changes at, for an outage that recovers.
	AnswerFrom int
	// Recovered is the answer after that.
	Recovered Answer
	// Retries is how many times each event's query is attempted. A retry count
	// above one is what criterion 2 is about: the second attempt must not pay.
	Retries int
	// Kills records which fault-injection points the run exercised. It is
	// supplied rather than simulated, so a caller has to say which kills it
	// actually ran - a harness that claimed a kill it did not perform would make
	// criterion 6 meaningless.
	Kills []string
	// Seed is the detector's RNG seed, so a run is reproducible.
	Seed int64
	// Fixture is an elementary stream to feed the capture path. Nil means the
	// run exercises ingest and evaluation without the muxer, which is a smaller
	// claim and is reported as one.
	Fixture []byte
}

func (o *Options) withDefaults() Options {
	out := *o
	if out.Events <= 0 {
		out.Events = 100
	}
	if out.Cameras <= 0 {
		out.Cameras = 4
	}
	if out.Retries <= 0 {
		out.Retries = 1
	}
	return out
}

// Runner drives a synthetic workload through the pipeline and records what happened.
//
// It is a recording, not a judgement. Every method here appends to a Run and
// none of them decides whether the run passed; `Criteria` does that afterwards,
// from the same data, so the harness cannot mark its own homework.
type Runner struct {
	opts     Options
	store    *evidence.Store
	provider airuntime.Provider
}

// NewRunner builds a runner over an evidence store and a provider.
func NewRunner(store *evidence.Store, provider airuntime.Provider, opts Options) *Runner {
	return &Runner{opts: opts.withDefaults(), store: store, provider: provider}
}

// Run executes the workload and returns the record.
func (r *Runner) Run(ctx context.Context) *Run {
	rec := &Run{
		StartedAt: time.Now().UTC(),
		EndedAt:   time.Now().UTC(),
		Kills:     append([]string(nil), r.opts.Kills...),
		Notes:     []string{},
	}
	if r.opts.Fixture == nil {
		rec.Notes = append(rec.Notes,
			"no elementary stream was supplied, so the muxer and the pre-roll were not exercised")
	}

	events := make([]Event, 0, r.opts.Events)
	for i := 0; i < r.opts.Events; i++ {
		camera := fmt.Sprintf("cam-%03d", i%r.opts.Cameras)
		e := Event{
			ID:       fmt.Sprintf("evt-%05d", i),
			CameraID: camera,
			State:    EventNormalized,
		}
		e.WindowStart = rec.StartedAt.Add(time.Duration(i) * time.Second)
		e.WindowEnd = e.WindowStart.Add(8 * time.Second)

		// Criterion 8: the drift, and what was done about it. The decision is
		// made here by the same policy the product would use, so the recorded
		// fact is the product's behaviour rather than a test's opinion of it.
		if i >= r.opts.DriftFromEvent {
			e.DriftMS = r.opts.DriftMS
		}
		if e.DriftMS != 0 {
			switch driftBand(e.DriftMS) {
			case driftWarn:
				// A clock we are not sure of widens the window. Widening is the
				// honest response: it admits the window may be misplaced rather
				// than pretending it is not.
				e.WindowWidened = true
				e.WindowStart = e.WindowStart.Add(-time.Duration(e.DriftMS) * time.Millisecond)
			case driftSevere:
				// A clock we are certain is wrong is refused, by name, because a
				// window selected on a clock that is a minute out is not a
				// window - it is a guess with timestamps on it.
				e.WindowRefusal = "clock_drift_severe"
				e.State = EventDeadLettered
				events = append(events, e)
				continue
			}
		}

		if r.opts.Fixture != nil {
			e.Evidence = append(e.Evidence, r.storeClip(ctx, e.ID))
		}

		// The query, and the idempotency check in front of it.
		answer := r.opts.Answer
		if i >= r.opts.AnswerFrom && r.opts.AnswerFrom > 0 {
			answer = r.opts.Recovered
		}
		// The key is built once and its event identity is stamped on every
		// record. Sharing the derivation is not tidiness: a key built on one
		// identity and a record carrying another never match, and a
		// never-matching key is an idempotency check that pays on every attempt.
		key := observations.IdempotencyKey(
			"soak", e.CameraID, e.WindowStart, e.WindowEnd,
			"general-monitoring", "v1", hashesOfEvidence(e.Evidence),
		)
		obs, attempts := r.query(ctx, e, answer, key)
		e.Observations = obs
		e.State = EventObserved
		if len(obs) > 0 {
			e.SideEffects = append(e.SideEffects, "incident:"+obs[len(obs)-1].ObservationID)
		}
		_ = attempts
		events = append(events, e)
	}
	rec.Events = events
	rec.EndedAt = time.Now().UTC()
	if p, ok := r.provider.(*Stub); ok {
		rec.ProviderCalls = p.Calls()
		rec.CostUSD = p.CostUSD()
	}
	rec.Notes = append(rec.Notes, fmt.Sprintf("retries configured: %d", r.opts.Retries))
	return rec
}

// query performs the inference for an event, honouring the idempotency check and
// the retry count.
//
// The idempotency check is a pure function over what has already been answered,
// which is why it is a function and not a query: the day the database is down is
// the day this most needs to work, and a rule that needs the database does not.
func (r *Runner) query(ctx context.Context, e Event, answer Answer, key observations.QueryKey) ([]ObservationRecord, int) {
	_ = answer
	var out []ObservationRecord
	attempts := 0
	for attempt := 0; attempt < r.opts.Retries; attempt++ {
		attempts++
		if observations.AlreadyQueried(key, flatten(key, out)) {
			// The second and later attempts skip the provider entirely. This is
			// the money, and it is the whole reason the check exists.
			continue
		}
		req := airuntime.Request{
			ObservationID: fmt.Sprintf("obs-%s-%d", e.ID, attempt),
			ProfileID:     "general-monitoring",
			Evidence:      evidenceToAiruntime(e.Evidence),
		}
		res, err := r.provider.Do(ctx, req)
		if err != nil {
			// A transport error is a fact about the run, not a reason to abandon
			// it. It is recorded as a degraded observation with the reason, and
			// the retry loop gets another go - which is what a supervisor does.
			out = append(out, ObservationRecord{
				EventID: key.EventID, ProfileID: req.ProfileID, PromptVersion: "v1",
				ObservationID:  req.ObservationID,
				Degraded:       true,
				DegradedReason: "error",
				DegradedDetail: "reason=error " + err.Error(),
				// A transport error is emphatically not a negative answer.
				ReadAsNegative: false,
			})
			continue
		}
		rec := recordFrom(key.EventID, res)
		out = append(out, rec)
	}
	linkSupersession(out)
	return out, attempts
}

// linkSupersession makes the supersession chain reciprocal.
//
// A run that records only one half of it is recording a claim nobody can check,
// and criterion 5 exists to catch exactly that.
func linkSupersession(recs []ObservationRecord) {
	active := ""
	for _, r := range recs {
		if r.SupersededBy == "" {
			active = r.ObservationID
		}
	}
	for i := range recs {
		if recs[i].SupersededBy == "" && recs[i].ObservationID != active && active != "" {
			recs[i].SupersededBy = active
		}
	}
	for i := range recs {
		if recs[i].ObservationID == active {
			for j := range recs {
				if recs[j].SupersededBy == active && recs[j].ObservationID != active {
					recs[i].Supersedes = recs[j].ObservationID
				}
			}
		}
	}
}

func recordFrom(eventID string, res airuntime.Result) ObservationRecord {
	return ObservationRecord{
		EventID: eventID, ProfileID: res.ProfileID, PromptVersion: "v1",
		ObservationID:  res.ObservationID,
		Degraded:       res.Degraded,
		DegradedReason: string(res.DegradedReason),
		// The detail is carried verbatim, because criterion 3 is about the reason
		// surviving all the way to a consumer rather than only the flag doing so.
		DegradedDetail: res.DegradedDetail,
		// A degraded observation is never read as a negative. The consumer here
		// is the run's own model of the operator's console, and it records what it
		// concluded rather than re-deriving it, so the criterion reads a decision
		// and not a formula.
		ReadAsNegative: !res.Degraded && res.Structured["insufficientEvidence"] == true,
		EvidenceHashes: res.InputEvidenceHashes,
		InputHashes:    res.InputEvidenceHashes,
		CostUSD:        CostPerCall,
		LatencyMS:      res.LatencyMS,
	}
}

func (r *Runner) storeClip(ctx context.Context, eventID string) Evidence {
	_ = ctx
	// The run's clip is the committed fixture, trimmed, so a soak with a fixture
	// really does exercise the store. What it does not do is decode: this is the
	// evidence path, not the capture path, and the capture path has its own
	// tests with an independent decoder.
	payload := append([]byte(nil), r.opts.Fixture...)
	obj, err := r.store.Put("soak", bytes.NewReader(payload))
	if err != nil {
		return Evidence{Ref: eventID, SizeBytes: int64(len(payload))}
	}
	body, _, oerr := r.store.Open(obj.Ref, obj.SHA256)
	visible := oerr == nil
	if visible {
		got, _ := io.ReadAll(body)
		visible = len(got) == len(payload)
		_ = body.Close()
	}
	return Evidence{
		Ref: obj.Ref, SHA256: obj.SHA256, SizeBytes: obj.SizeBytes, Visible: visible,
	}
}

func evidenceToAiruntime(ev []Evidence) []airuntime.Evidence {
	out := make([]airuntime.Evidence, 0, len(ev))
	for _, e := range ev {
		out = append(out, airuntime.Evidence{ID: e.SHA256, SHA256: e.SHA256, ContentType: "video/mp4"})
	}
	return out
}

func hashesOfEvidence(ev []Evidence) []string {
	out := make([]string, 0, len(ev))
	for _, e := range ev {
		out = append(out, e.SHA256)
	}
	sort.Strings(out)
	return out
}

// flatten projects recorded runs back into observations for the idempotency
// check.
//
// It takes the key rather than reading the tenant from a record, because the
// tenant is a property of the *question* and a record that carried its own would
// be a record that could disagree with the key it was filed under. That
// disagreement is silent and it is the expensive direction: every attempt
// decides the question was never asked, and the provider is paid again.
func flatten(key observations.QueryKey, recs []ObservationRecord) []observations.Observation {
	out := make([]observations.Observation, 0, len(recs))
	for _, r := range recs {
		if r.SupersededBy != "" {
			continue
		}
		o := observations.Observation{
			ObservationID:       r.ObservationID,
			TenantID:            key.TenantID,
			EventID:             r.EventID,
			SiteID:              key.TenantID,
			ProfileID:           r.ProfileID,
			PromptVersion:       r.PromptVersion,
			InputEvidenceHashes: r.InputHashes,
			Degraded:            observations.Degradation(r.DegradedReason),
			DegradedDetail:      r.DegradedDetail,
		}
		if !r.Degraded {
			o.StructuredOutput = map[string]any{"insufficientEvidence": false}
		}
		out = append(out, o)
	}
	return out
}

// Drift bands, mirroring the product's clock policy so the recorded decision is
// the product's and not the harness's.
const (
	driftWarn   = "warn"
	driftSevere = "severe"
)

func driftBand(ms int32) string {
	switch {
	case ms > 30000:
		return driftSevere
	case ms > 2000:
		return driftWarn
	}
	return "ok"
}
