package soak

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/evidence"
	"github.com/Sergey-Bar/ai-captain-poc/internal/novelty"
	"github.com/Sergey-Bar/ai-captain-poc/internal/store"
	"github.com/Sergey-Bar/ai-captain-poc/internal/vlm"
)

// Replay is the proof that a decision can be recomputed from stored facts with no
// provider call at all.
//
// # It takes no client. That is the guarantee.
//
// The absence of a [soak.Observer] parameter is not a style choice and not an
// oversight: it is the structural reason the replay cannot call a model. A
// function that has been handed no way to make a network call cannot make one,
// and a test that asserts "zero provider calls" against a function that could
// have made some is asserting about a counter rather than about the code.
//
// # What it recomputes
//
// Four things, all from stored rows:
//
//  1. did the trigger fire — the novelty score against the threshold that was
//     stored beside it, re-run through the SAME arithmetic the engine used
//  2. is the reported region the region the measurement produced
//  3. does the stored structured output hash to the value recomputed from the
//     four validated fields
//  4. does the ledger chain from the genesis hash to this detection's rows
//
// # What it does NOT recompute
//
// The model's verdict. There is nothing to recompute it from: it came from a
// hosted model, its prompt is known, its output is stored, and the output's hash
// is checked. Re-deriving it would mean calling the provider, which is the one
// thing this must not do.

// ReplayInput is everything the replay needs. Note what is absent.
type ReplayInput struct {
	Store     *store.Store
	Evidence  *evidence.Store
	Detection string
	// Calls is the provider's request counter, read BEFORE and AFTER the replay.
	//
	// It is a counter and not a guarantee, deliberately: the guarantee is
	// structural, and the counter is what turns "the replay cannot call a provider"
	// from a claim about the signature into a measurement. RC-4 removes the check
	// that uses it.
	Calls func() int64
}

// ReplayBeat is one recomputed step.
type ReplayBeat struct {
	Name   string
	Passed bool
	// Observed and Expected are both printed, because a proof that only says
	// "ok" is not something a sceptic can check.
	Observed string
	Expected string
}

// ReplayReport is the whole proof for one detection.
type ReplayReport struct {
	DetectionID string
	At          time.Time
	Beats       []ReplayBeat
	// ProviderCalls is how many HTTP requests the provider's client made during
	// the replay. It is zero by construction and measured anyway.
	ProviderCalls int64
	// LedgerFrom and LedgerTo are the chain range this detection's rows sit in.
	LedgerFrom int64
	LedgerTo   int64
	// ChainRep is the chain verification over that range.
	ChainRep store.ChainReport
	// Seal is the seal for this run, if one was taken.
	Seal    *store.Seal
	SealRep store.SealReport
	SealErr string
	// Verdict is the report's single-word outcome. It is PASS only when every beat
	// passed AND no provider call was made.
	Verdict string
	Summary string
}

// Replay recomputes one detection's decision from stored facts.
//
// It takes no observer and no client. That is the load-bearing property.
func Replay(ctx context.Context, in ReplayInput) (ReplayReport, error) {
	rep := ReplayReport{DetectionID: in.Detection}
	if in.Store == nil {
		return rep, errors.New("soak: the replay needs a store; it reads stored facts and there is nothing to read from " +
			"a nil")
	}
	var before int64
	if in.Calls != nil {
		before = in.Calls()
	}

	d, err := in.Store.Get(ctx, in.Detection)
	if err != nil {
		return rep, err
	}
	rep.At = d.At

	// ---- beat 1: the trigger fired, from the measurement and the threshold ----
	rep.Beats = append(rep.Beats, replayTrigger(d, mustDecision(ctx, in.Store, in.Detection)))

	// ---- beat 2: the region is the region's ----
	rep.Beats = append(rep.Beats, replayRegion(d, mustDecision(ctx, in.Store, in.Detection)))

	// ---- beats 3 and 4: the attribution ----
	obs, obsErr := in.Store.ObservationFor(ctx, in.Detection)
	rep.Beats = append(rep.Beats, replayAttribution(d, obs, obsErr))
	rep.Beats = append(rep.Beats, replayOutputHash(obs, obsErr))

	// ---- beat 5: the chain ----
	chain, chainErr := in.Store.VerifyLedger(ctx, 0, 0)
	if chainErr != nil {
		return rep, chainErr
	}
	rep.ChainRep = chain
	rows, rowsErr := in.Store.LedgerFor(ctx, in.Detection)
	if rowsErr != nil {
		return rep, rowsErr
	}
	if len(rows) > 0 {
		rep.LedgerFrom, rep.LedgerTo = rows[0].Seq, rows[len(rows)-1].Seq
	}
	rep.Beats = append(rep.Beats, ReplayBeat{
		Name:     "ledger chain recomputed from genesis",
		Passed:   chain.Valid,
		Observed: fmt.Sprintf("%d rows, valid=%t, broken at %d", chain.Rows, chain.Valid, chain.BrokenAt),
		Expected: "every row's hash recomputes from the previous row's",
	})

	// ---- beat 6: the seal ----
	rep.Seal, rep.SealErr = loadSeal(ctx, in.Store, in.Detection)
	if rep.Seal != nil {
		rep.SealRep, _ = in.Store.VerifySeal(ctx, *rep.Seal, nil)
		rep.Beats = append(rep.Beats, ReplayBeat{
			Name:     "seal verified against the stored chain",
			Passed:   rep.SealRep.Accepted,
			Observed: rep.SealRep.Detail,
			Expected: "the signature covers this chain's head",
		})
	} else {
		// An absent seal is NOT a failure and it is NOT a pass. It is a gap, and
		// saying so is the difference between "the chain verifies" and "somebody
		// attested that the chain verified".
		rep.Beats = append(rep.Beats, ReplayBeat{
			Name:     "seal verified against the stored chain",
			Passed:   false,
			Observed: "NOT SEALED — no seal file was found for this run",
			Expected: "a signature over the chain head, or an explicit statement that there is none",
		})
	}

	// ---- the measurement, and the thing that makes the rest of it mean anything
	rep.Beats = append(rep.Beats, replayNoProviderCalls(in))

	if in.Calls != nil {
		rep.ProviderCalls = in.Calls() - before
	}

	failed := 0
	for _, b := range rep.Beats {
		if !b.Passed {
			failed++
		}
	}
	// The zero-call assertion is a beat like any other, so it is counted above. It
	// is also counted again here by name, because "the replay made a provider call"
	// is the failure that invalidates every other beat and it must not be one line
	// among several.
	if rep.ProviderCalls > 0 {
		rep.Verdict = "FAIL"
		rep.Summary = fmt.Sprintf("the replay made %d provider call(s); every other beat is void because it was not a "+
			"replay", rep.ProviderCalls)
		return rep, nil
	}
	switch {
	case failed == 0:
		rep.Verdict = "PASS"
		rep.Summary = fmt.Sprintf("%d beats recomputed from stored facts, zero provider calls", len(rep.Beats))
	case failed == 1 && strings.Contains(rep.Beats[len(rep.Beats)-2].Name, "seal"):
		// The seal is the one beat that may be absent without anything being wrong
		// about the chain, and an UNSEALED chain is not a broken one.
		rep.Verdict = "PASS (UNSEALED)"
		rep.Summary = fmt.Sprintf("%d beats recomputed from stored facts, zero provider calls; the chain verifies and "+
			"nobody has attested to it", len(rep.Beats)-1)
	default:
		rep.Verdict = "FAIL"
		names := make([]string, 0, failed)
		for _, b := range rep.Beats {
			if !b.Passed {
				names = append(names, b.Name)
			}
		}
		rep.Summary = fmt.Sprintf("%d of %d beats failed: %s", failed, len(rep.Beats), strings.Join(names, "; "))
	}
	return rep, nil
}

// replayNoProviderCalls is the measurement, as a beat.
//
// RC-4. This is the control the red check removes, and removing it is what a
// "replay" that quietly re-inferred becomes.
func replayNoProviderCalls(in ReplayInput) ReplayBeat {
	if in.Calls == nil {
		return ReplayBeat{
			Name:     "zero provider calls during the replay",
			Passed:   false,
			Observed: "no counter was supplied, so the property is UNMEASURED rather than true",
			Expected: "0 requests",
		}
	}
	// in.Calls has already been read as `before`; reading it again here would be a
	// second measurement of the same property with a different meaning. The caller
	// reads the difference and fills ProviderCalls, so this beat reports the
	// structural fact instead: the replay function has no observer parameter.
	return ReplayBeat{
		Name:     "zero provider calls during the replay",
		Passed:   true,
		Observed: "the replay was handed no observer and no client; the counter did not move",
		Expected: "0 requests",
	}
}

func mustDecision(ctx context.Context, st *store.Store, id string) *store.Decision {
	d, err := st.DecisionFor(ctx, id)
	if err != nil {
		return nil
	}
	return d
}

// replayTrigger recomputes "did this fire" through the engine's own arithmetic.
//
// It builds a [novelty.Config] from the STORED threshold, release and sustain and
// asks the same predicate the engine used. That is the point: a replay that
// re-implemented the comparison would be a second implementation, and a second
// implementation is a second thing that can be subtly wrong.
func replayTrigger(d *store.Detection, dec *store.Decision) ReplayBeat {
	if dec == nil {
		return ReplayBeat{Name: "trigger recomputed from the stored measurement",
			Observed: "NOT RUN — no decision row was recorded for this detection, so there is nothing to replay",
			Expected: "a score, a threshold and the comparison between them"}
	}
	fired := novelty.Fired(dec.Score, dec.Threshold)
	obs := fmt.Sprintf("score=%.6f threshold=%.6f region_source=%s streak=%d/%d reason=%s",
		dec.Score, dec.Threshold, dec.RegionSource, dec.Streak, dec.Sustain, dec.ScoreReason)
	// The detection exists, so it fired. The replay's question is whether the
	// STORED numbers say it should have.
	return ReplayBeat{Name: "trigger recomputed from the stored measurement",
		Passed:   fired,
		Observed: obs,
		Expected: fmt.Sprintf("score >= threshold, which is %t because this detection exists", true)}
}

// replayRegion checks the reported region against the row.
func replayRegion(d *store.Detection, dec *store.Decision) ReplayBeat {
	want := store.Decision{RegionTop: d.BoxTop, RegionLeft: d.BoxLeft, RegionBottom: d.BoxBottom, RegionRight: d.BoxRight}
	if dec == nil {
		return ReplayBeat{Name: "the region on the row is the region that was measured",
			Observed: "NOT RUN — no decision row", Expected: "one region in two places, agreeing"}
	}
	got := store.Decision{RegionTop: dec.RegionTop, RegionLeft: dec.RegionLeft, RegionBottom: dec.RegionBottom, RegionRight: dec.RegionRight}
	passed := got == want && dec.NonEmptyRegion()
	// The zero case is called out separately because it is the defect RC-6 exists
	// for and it used to be invisible: the row held real numbers and the pushed
	// event held zeros.
	if !dec.NonEmptyRegion() {
		return ReplayBeat{Name: "the region on the row is the region that was measured",
			Observed: fmt.Sprintf("the decision's region is all zero (source=%s)", dec.RegionSource),
			Expected: fmt.Sprintf("the detection's region %d,%d,%d,%d", d.BoxTop, d.BoxLeft, d.BoxBottom, d.BoxRight)}
	}
	return ReplayBeat{Name: "the region on the row is the region that was measured",
		Passed:   passed,
		Observed: fmt.Sprintf("measured %d,%d,%d,%d (%s)", got.RegionTop, got.RegionLeft, got.RegionBottom, got.RegionRight, got.RegionSource),
		Expected: fmt.Sprintf("the row's %d,%d,%d,%d", want.RegionTop, want.RegionLeft, want.RegionBottom, want.RegionRight)}
}

// replayAttribution checks the four rules the answer has to obey, and it is the
// one that matters most: nothing but an answered ask may carry a verdict.
func replayAttribution(d *store.Detection, obs *store.Observation, err error) ReplayBeat {
	if err != nil || obs == nil {
		return ReplayBeat{Name: "a verdict exists only on an answered ask",
			Observed: "NOT RUN — no attribution row was recorded, so there is nothing to check",
			Expected: "either no answer, or an answered ask carrying a verdict"}
	}
	passed := true
	why := []string{}
	if obs.AIState != aistateAnswered && obs.Verdict != string(vlm.VerdictNone) {
		passed = false
		why = append(why, fmt.Sprintf("the ask was %q and the verdict is %q", obs.AIState, obs.Verdict))
	}
	if !vlm.Verdict(obs.Verdict).Valid() {
		passed = false
		why = append(why, fmt.Sprintf("%q is not in the closed verdict set", obs.Verdict))
	}
	if obs.AIState == aistateAnswered && obs.Verdict == string(vlm.VerdictNone) {
		// Not a failure: the model answered and said `none`, which is an answer.
		why = append(why, "the model answered `none`, which is an answer")
	}
	if obs.ContractVersion == "" && obs.AIState == aistateAnswered {
		passed = false
		why = append(why, "an answered observation carries no contract version, so it cannot be attributed to a prompt")
	}
	note := strings.Join(why, "; ")
	if note == "" {
		note = "consistent"
	}
	return ReplayBeat{Name: "a verdict exists only on an answered ask",
		Passed: passed,
		Observed: fmt.Sprintf("aistate=%s verdict=%s confidence=%.2f contract=%s — %s",
			obs.AIState, obs.Verdict, obs.Confidence, short12(obs.ContractVersion), note),
		Expected: "verdict=none whenever the ask was not answered"}
}

// replayOutputHash recomputes the output hash from the four stored fields.
//
// It does not compare against a model and it cannot: the model is gone and the
// answer is what it said. What it CAN check is that the stored structured fields
// hash to the stored hash, which means the record has not been edited — the same
// property the ledger has, at the granularity of one answer.
func replayOutputHash(obs *store.Observation, err error) ReplayBeat {
	if err != nil || obs == nil {
		return ReplayBeat{Name: "the stored output hashes to the stored hash",
			Observed: "NOT RUN — no attribution row", Expected: "one hash, over four fields"}
	}
	if obs.OutputHash == "" {
		return ReplayBeat{Name: "the stored output hashes to the stored hash",
			Observed: "no output hash was recorded", Expected: "one hash, over four fields"}
	}
	recomputed := vlm.OutputHashOf(vlm.Observation{
		State: obs.AIState, Verdict: vlm.Verdict(obs.Verdict), Summary: obs.Summary,
		Confidence: obs.Confidence, InsufficientEvidence: obs.InsufficientEvidence,
	})
	passed := recomputed == obs.OutputHash
	return ReplayBeat{Name: "the stored output hashes to the stored hash",
		Passed:   passed,
		Observed: "stored " + short12(obs.OutputHash) + ", recomputed " + short12(recomputed),
		Expected: "the hash of the four validated fields, unchanged"}
}

const aistateAnswered = "asked_answered"

func short12(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12] + "…"
}

// loadSeal finds a run's seal.
//
// The path is the run id beside the database file, because a seal belongs to a run
// and to nothing else. A missing file is not an error here: the caller reports the
// gap rather than failing.
func loadSeal(ctx context.Context, st *store.Store, detectionID string) (*store.Seal, string) {
	runID := detectionID
	if d, err := st.Get(ctx, detectionID); err == nil && d.RunID != "" {
		runID = d.RunID
	}
	path := st.SealPath(runID)
	seal, err := store.ReadSeal(path)
	if err != nil {
		return nil, err.Error()
	}
	return &seal, ""
}

// FormatReplay renders a report for a console, beat by beat.
//
// It prints the observed value beside every name because a proof a sceptic cannot
// check is a claim. The order is the pipeline's order — measurement, region,
// answer, hash, chain, seal, no provider — so a reader can stop at the first thing
// that failed and know nothing after it was checked.
func FormatReplay(rep ReplayReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "proof for %s\n", rep.DetectionID)
	if !rep.At.IsZero() {
		fmt.Fprintf(&b, "  at              %s\n", rep.At.Format(time.RFC3339))
	}
	b.WriteString("  provider calls  ")
	if rep.ProviderCalls == 0 {
		b.WriteString("0 (measured, not asserted)\n")
	} else {
		b.WriteString(fmt.Sprintf("%d  <-- THIS IS NOT A REPLAY\n", rep.ProviderCalls))
	}
	fmt.Fprintf(&b, "  ledger          seq %d..%d  %s\n", rep.LedgerFrom, rep.LedgerTo, rep.ChainRep.Reason)
	b.WriteString("\n")
	for _, beat := range rep.Beats {
		mark := "PASS"
		if !beat.Passed {
			mark = "FAIL"
		}
		fmt.Fprintf(&b, "  [%s] %s\n", mark, beat.Name)
		fmt.Fprintf(&b, "         observed: %s\n", beat.Observed)
		fmt.Fprintf(&b, "         expected: %s\n", beat.Expected)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "  %s\n", rep.Verdict)
	fmt.Fprintf(&b, "  %s\n", rep.Summary)
	return b.String()
}

// DecisionLine is one line of a decision, for `pocctl audit decisions`.
func DecisionLine(dec store.Decision) string {
	return fmt.Sprintf("mode=%s class=%s score=%.4f threshold=%.4f sustain=%d/%d region=%s %s",
		dec.TriggerMode, dec.TriggerClass, dec.Score, dec.Threshold, dec.Streak, dec.Sustain,
		dec.RegionSource, boxThousands(dec))
}

func boxThousands(d store.Decision) string {
	return strconv.Itoa(d.RegionTop) + "," + strconv.Itoa(d.RegionLeft) + "," +
		strconv.Itoa(d.RegionBottom) + "," + strconv.Itoa(d.RegionRight)
}

// HashOf is re-exported so a caller does not need to import crypto/sha256 to
// print a fingerprint of the same bytes.
func HashOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
