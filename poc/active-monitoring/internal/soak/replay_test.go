package soak

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/store"
	"github.com/Sergey-Bar/vision-poc/internal/vlm"
)

// counter is a fake provider client's request counter. It is a real
// implementation of `func() int64` and not a mock, because the point of the
// assertion is that the number is measured.
//
// `leak` makes it move by one on every read, which is what a provider client that
// the replay was quietly reaching for would look like from the outside. That is how
// RC-4's test produces the condition the check exists to catch.
type counter struct {
	n    atomic.Int64
	leak atomic.Int64
}

func (c *counter) Calls() int64 {
	if extra := c.leak.Load(); extra > 0 {
		c.n.Add(extra)
	}
	return c.n.Load()
}

// recorderFixture builds a detection, a decision, an attribution and a chain, all
// of them internally consistent, so a replay has something real to replay.
func recorderFixture(t *testing.T) (*store.Store, string, *counter) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	runID := "run-replay"
	if err := st.StartRun(ctx, store.Run{ID: runID, Mode: "live", StartedAt: time.Now(), TargetSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	id := "det-replay-1"
	if err := st.Accept(ctx, store.Detection{
		ID: id, RunID: runID, At: now, Class: "motion", Family: "vehicle", TrackID: 1, Score: 0.81,
		BoxTop: 120, BoxLeft: 200, BoxBottom: 460, BoxRight: 520,
		Direction: "left_to_right", AIState: "asked_answered", Provenance: store.ProvenanceReal,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutDecision(ctx, store.Decision{
		DetectionID: id, RunID: runID, TriggerMode: "temporal", TriggerClass: "motion",
		Score: 0.41, Threshold: 0.35, Release: 0.20, Sustain: 1, Streak: 1,
		ScoreReason: "changed", RegionSource: "measured",
		RegionTop: 120, RegionLeft: 200, RegionBottom: 460, RegionRight: 520,
		FrameSeq: 991,
	}); err != nil {
		t.Fatal(err)
	}
	obs := vlm.Observation{
		DetectionID: id, State: "asked_answered", Verdict: vlm.VerdictConfirmed,
		Summary: "a vehicle crossed the line", Confidence: 0.78,
		PromptID: "alarm", PromptVersion: "v1", ContractVersion: strings.Repeat("c", 64),
		ModelIdentity: "cosmos-3-nano-16b", Temperature: 0.01,
	}
	obs.Finalise()
	if err := st.PutObservation(ctx, store.Observation{
		DetectionID: id, RunID: runID, PromptID: obs.PromptID, PromptVersion: obs.PromptVersion,
		AIState: obs.State, Verdict: string(obs.Verdict), Summary: obs.Summary,
		Confidence: obs.Confidence, InsufficientEvidence: obs.InsufficientEvidence,
		ModelIdentity: obs.ModelIdentity, ContractVersion: obs.ContractVersion,
		Temperature: obs.Temperature, ReasonCode: "none", OutputHash: obs.OutputHash,
		InputHashes: store.InputHashesOf(map[string]string{"clip": strings.Repeat("a", 64)}),
	}); err != nil {
		t.Fatal(err)
	}
	for _, e := range []store.LedgerEntry{
		{Kind: store.LedgerDetectionCommitted, Subject: id, Payload: `{"class":"motion"}`},
		{Kind: store.LedgerVLMAsked, Subject: id, Payload: `{"aistate":"asked_answered"}`},
		{Kind: store.LedgerVLMVerdict, Subject: id, Payload: `{"output_hash":"x"}`},
	} {
		if _, err := st.AppendLedger(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	return st, id, &counter{}
}

// TestAReplayRecomputesTheDecisionFromStoredFactsAndCallsNobody is W3-4.
//
// Two claims, and both are asserted: the beats pass, and the provider counter did
// not move. The second is the one that matters — a replay that calls the model and
// prints the same number is not a replay, it is a second run with extra steps.
func TestAReplayRecomputesTheDecisionFromStoredFactsAndCallsNobody(t *testing.T) {
	st, id, calls := recorderFixture(t)
	rep, err := Replay(context.Background(), ReplayInput{Store: st, Detection: id, Calls: calls.Calls})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if rep.ProviderCalls != 0 {
		t.Fatalf("the replay made %d provider calls", rep.ProviderCalls)
	}
	if rep.Verdict == "FAIL" {
		t.Fatalf("the replay reports %s: %s", rep.Verdict, FormatReplay(rep))
	}
	// There is no seal file in this fixture, so the verdict is PASS (UNSEALED)
	// rather than PASS. A bare PASS would read as an attestation nobody made.
	if rep.Verdict != "PASS (UNSEALED)" {
		t.Fatalf("the replay reports %q rather than PASS (UNSEALED); a bare PASS reads as an attestation", rep.Verdict)
	}
	for _, name := range []string{
		"trigger recomputed from the stored measurement",
		"the region on the row is the region that was measured",
		"a verdict exists only on an answered ask",
		"the stored output hashes to the stored hash",
		"ledger chain recomputed from genesis",
		"zero provider calls during the replay",
	} {
		found := false
		for _, b := range rep.Beats {
			if b.Name == name {
				found = true
				if !b.Passed {
					t.Fatalf("the beat %q failed: observed %s — %s", name, b.Observed, b.Expected)
				}
			}
		}
		if !found {
			t.Fatalf("the replay has no beat named %q. Beats: %v", name, beatNames(rep))
		}
	}
}

// TestAReplayWithNoCounterSaysThePropertyIsUnmeasured is the honest degenerate
// case. `Calls` nil must NOT read as "zero calls, therefore a replay".
func TestAReplayWithNoCounterSaysThePropertyIsUnmeasured(t *testing.T) {
	st, id, _ := recorderFixture(t)
	rep, err := Replay(context.Background(), ReplayInput{Store: st, Detection: id})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, b := range rep.Beats {
		if b.Name == "zero provider calls during the replay" {
			found = true
			if b.Passed {
				t.Fatal("the zero-call beat passed with no counter supplied; an UNMEASURED property is not a true one")
			}
			if !strings.Contains(b.Observed, "UNMEASURED") {
				t.Fatalf("the beat does not say it is unmeasured: %s", b.Observed)
			}
		}
	}
	if !found {
		t.Fatalf("there is no zero-call beat at all: %v", beatNames(rep))
	}
	if rep.Verdict == "PASS" {
		t.Fatal("the replay PASSED with the zero-call property unmeasured")
	}
}

// TestAReplayFailsWhenTheProviderCounterMoves is RC-4's test.
//
// This is the check the red check removes. With `if calls > 0` gone, a replay
// that quietly made a provider call would print the same PASS — and this test is
// what notices.
func TestAReplayFailsWhenTheProviderCounterMoves(t *testing.T) {
	st, id, calls := recorderFixture(t)
	// A client that moves by one on every read is what a replay quietly reaching
	// for the provider looks like from the outside. This is the check the red check
	// removes; with `if calls > 0` gone, this test would report PASS.
	calls.leak.Store(1)
	rep, err := Replay(context.Background(), ReplayInput{Store: st, Detection: id, Calls: calls.Calls})
	if err != nil {
		t.Fatal(err)
	}
	if rep.ProviderCalls <= 0 {
		t.Fatalf("the delta is %d; the counter was made to move on every read and the replay saw none of it", rep.ProviderCalls)
	}
	// Any PASS is wrong here, including PASS (UNSEALED): an unsealed chain is a gap
	// in the ATTESTATION, and a replay that made provider calls is a lie about
	// every beat it printed. Asserting on the exact string would let a second pass
	// variant slip past.
	if strings.HasPrefix(rep.Verdict, "PASS") {
		t.Fatalf("the replay reports %q while the provider counter moved; this is RC-4", rep.Verdict)
	}
	if !strings.Contains(rep.Summary, "provider call") {
		t.Fatalf("the summary does not say the replay is void: %s", rep.Summary)
	}
	if !strings.Contains(FormatReplay(rep), "THIS IS NOT A REPLAY") {
		t.Fatalf("the printed report does not say it is not a replay: %s", FormatReplay(rep))
	}
}

// TestAReplayRefusesAnObservationCarryingAVerdictOnAnUnansweredAsk is the
// fabricated negative caught from the other end: even if a row somehow holds one,
// the replay says so.
func TestAReplayRefusesAnObservationCarryingAVerdictOnAnUnansweredAsk(t *testing.T) {
	ctx := context.Background()
	st, id, calls := recorderFixture(t)
	// Write the fabricated negative straight past the store's own guard.
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE vlm_observations SET aistate='degraded', verdict='false_positive' WHERE detection_id=?`, id); err != nil {
		t.Fatal(err)
	}
	rep, err := Replay(ctx, ReplayInput{Store: st, Detection: id, Calls: calls.Calls})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, b := range rep.Beats {
		if b.Name == "a verdict exists only on an answered ask" {
			found = true
			if b.Passed {
				t.Fatal("a false_positive on a degraded ask replayed as PASS")
			}
			if !strings.Contains(b.Observed, "false_positive") {
				t.Fatalf("the beat does not quote the verdict it found: %s", b.Observed)
			}
		}
	}
	if !found {
		t.Fatalf("there is no verdict beat: %v", beatNames(rep))
	}
	if rep.Verdict == "PASS" {
		t.Fatal("the replay PASSED over a fabricated negative")
	}
}

// TestAReplayRefusesAnAllZeroRegion is RC-6 seen from the proof side.
func TestAReplayRefusesAnAllZeroRegion(t *testing.T) {
	ctx := context.Background()
	st, id, calls := recorderFixture(t)
	// The pushed event carries zeros while the row beside it holds real numbers.
	// That is the defect the earlier plan called the worst thing in a demo, and the
	// replay has to be the thing that names it.
	if err := st.PutDecision(ctx, store.Decision{
		DetectionID: id, RunID: "run-replay", TriggerMode: "temporal", TriggerClass: "motion",
		Score: 0.41, Threshold: 0.35, RegionSource: "measured",
		RegionTop: 0, RegionLeft: 0, RegionBottom: 0, RegionRight: 0,
	}); err == nil {
		t.Fatal("the store accepted an all-zero region; the durable half of RC-6 is gone")
	}
	// So force it past the guard the way a second writer would.
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE decisions SET region_top=0, region_left=0, region_bottom=0, region_right=0 WHERE detection_id=?`, id); err != nil {
		t.Fatal(err)
	}
	rep, err := Replay(ctx, ReplayInput{Store: st, Detection: id, Calls: calls.Calls})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, b := range rep.Beats {
		if b.Name == "the region on the row is the region that was measured" {
			found = true
			if b.Passed {
				t.Fatal("an all-zero region replayed as PASS")
			}
			if !strings.Contains(b.Observed, "all zero") {
				t.Fatalf("the beat does not say the region is all zero: %s", b.Observed)
			}
		}
	}
	if !found {
		t.Fatalf("there is no region beat: %v", beatNames(rep))
	}
}

// TestAnUnsealedChainIsReportedAsUnsealedAndNotAsBroken. A chain that verifies is
// not the same claim as a chain somebody attested to.
func TestAnUnsealedChainIsReportedAsUnsealedAndNotAsBroken(t *testing.T) {
	st, id, calls := recorderFixture(t)
	rep, err := Replay(context.Background(), ReplayInput{Store: st, Detection: id, Calls: calls.Calls})
	if err != nil {
		t.Fatal(err)
	}
	if rep.ChainRep.Valid != true {
		t.Fatalf("the chain does not verify: %s", rep.ChainRep.Reason)
	}
	if rep.Verdict != "PASS (UNSEALED)" {
		t.Fatalf("an unsealed chain reports %s rather than PASS (UNSEALED); a bare PASS would read as an attestation", rep.Verdict)
	}
	if !strings.Contains(rep.Summary, "nobody has attested") {
		t.Fatalf("the summary does not say the gap: %s", rep.Summary)
	}
}

// TestAReplayNeedsAStore: a replay that reads stored facts and has no store has
// nothing to read, and saying so is better than a PASS over nothing.
func TestAReplayNeedsAStore(t *testing.T) {
	if _, err := Replay(context.Background(), ReplayInput{Detection: "det-1"}); err == nil {
		t.Fatal("a replay with no store succeeded")
	}
}

func beatNames(rep ReplayReport) []string {
	out := make([]string, 0, len(rep.Beats))
	for _, b := range rep.Beats {
		out = append(out, b.Name)
	}
	return out
}
