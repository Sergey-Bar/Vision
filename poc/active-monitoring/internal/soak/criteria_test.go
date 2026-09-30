package soak

import (
	"strings"
	"testing"
	"time"
)

// goodRun is a run where everything held, so each test can break exactly one
// thing and assert that exactly one criterion notices.
func goodRun() Inputs {
	return Inputs{
		Elapsed:         7 * 24 * time.Hour,
		Target:          7 * 24 * time.Hour,
		RunEnded:        true,
		TotalDetections: 70,
		EvidenceStored:  70, EvidenceVerified: 70,
		PushAttempts: 70, PushesWithResult: 70,
		PeakRSSBytes: 400 << 20, DeclaredRSS: 1536 << 20,
		PeakDBBytes: 40 << 20, DeclaredDB: 512 << 20,
		PeakDiskBytes: 100 << 30, DeclaredMaxDisk: 200 << 30,
		PeakCPUPercent: 90, DeclaredCPU: 180, CPUMeasured: true,
		AIP99: 300 * time.Millisecond, AIP99Samples: 3000, DeclaredAIBudget: 800 * time.Millisecond,
		RehearsedFaults: []Rehearsal{
			{Fault: "401", Expected: "auth_failed", Observed: "auth_failed"},
			{Fault: "500", Expected: "retry", Observed: "retry"},
		},
		Provenance: "real", PushTargetIsSimulator: true,
		SourceKind: "loop", SourceLabel: "recorded loop",
		HeartbeatCount: 6000, ResourceSamples: 6000,
	}
}

func byID(cs []Criterion, id string) Criterion {
	for _, c := range cs {
		if c.ID == id {
			return c
		}
	}
	return Criterion{ID: id, Verdict: VerdictNotRun, Missing: "no such criterion was scored"}
}

func TestAGoodRunPassesEverything(t *testing.T) {
	cs := Score(goodRun())
	for _, c := range cs {
		if c.Verdict != VerdictPass {
			t.Fatalf("%s (%s) = %s, want PASS: measured=%q missing=%q", c.ID, c.Name, c.Verdict, c.Observed, c.Missing)
		}
	}
	s := Overall(goodRun(), cs)
	if s.Verdict != VerdictPass || s.Provisional {
		t.Fatalf("summary = %+v, want a settled PASS", s)
	}
}

func TestThereAreExactlyThreeVerdicts(t *testing.T) {
	for _, v := range []Verdict{VerdictPass, VerdictFail, VerdictNotRun} {
		if !Valid(v) {
			t.Fatalf("%q is not accepted as a verdict", v)
		}
	}
	for _, v := range []Verdict{"", "PASSED", "ok", "SKIP", "N/A", "PROVISIONAL", "partial"} {
		if Valid(v) {
			t.Fatalf("%q was accepted as a verdict; there are three and no more", v)
		}
	}
}

// TestAC1FailsOnAProcessExitAndOnAHeartbeatGap is the zero-tolerance clause. A
// run with zero dropouts is not "99.9% dropouts".
func TestAC1IsZeroTolerance(t *testing.T) {
	in := goodRun()
	in.ProcessExits = 1
	if c := byID(Score(in), "AC1"); c.Verdict != VerdictFail {
		t.Fatalf("one process exit scored %s; AC1 is absolute zero", c.Verdict)
	}
	in = goodRun()
	in.HeartbeatGaps = []GapMeasurement{{Seconds: 31}}
	if c := byID(Score(in), "AC1"); c.Verdict != VerdictFail {
		t.Fatalf("a 31 second heartbeat gap scored %s; the threshold is 30", c.Verdict)
	}
	// Exactly at the threshold is not over it.
	in = goodRun()
	in.HeartbeatGaps = []GapMeasurement{{Seconds: 30}}
	if c := byID(Score(in), "AC1"); c.Verdict != VerdictPass {
		t.Fatalf("a 30 second gap scored %s; the threshold is strictly greater than 30", c.Verdict)
	}
}

// TestAC1IsNotRunWithoutHeartbeatEvidence: a run that never reported liveness
// cannot be said to have been alive, and that is NOT RUN rather than PASS.
func TestAC1IsNotRunWithoutHeartbeats(t *testing.T) {
	in := goodRun()
	in.HeartbeatCount = 0
	c := byID(Score(in), "AC1")
	if c.Verdict != VerdictNotRun {
		t.Fatalf("a run with no heartbeats scored %s for AC1", c.Verdict)
	}
	if c.Missing == "" {
		t.Fatal("NOT RUN without a stated reason")
	}
}

func TestAC2FailsOnAStalledDetection(t *testing.T) {
	in := goodRun()
	in.StalledDetections = []StalledDetection{{ID: "det-1", Status: "accepted", Age: 7 * time.Minute}}
	c := byID(Score(in), "AC2")
	if c.Verdict != VerdictFail {
		t.Fatalf("a detection stuck for 7 minutes scored %s", c.Verdict)
	}
	if !strings.Contains(c.Observed, "det-1") {
		t.Fatalf("the failing detection is not named: %s", c.Observed)
	}
}

// TestAC2AndAC3AreNotRunOnARunThatFoundNothing: zero detections is a run that
// measured nothing, and it must not be reported as having passed the criteria
// that depend on detections.
func TestCriteriaAreNotRunOnARunThatFoundNothing(t *testing.T) {
	in := goodRun()
	in.TotalDetections = 0
	in.EvidenceStored, in.EvidenceVerified = 0, 0
	in.PushAttempts, in.PushesWithResult = 0, 0
	cs := Score(in)
	for _, id := range []string{"AC3", "AC4"} {
		c := byID(cs, id)
		if c.Verdict != VerdictNotRun {
			t.Fatalf("%s scored %s on a run with no detections; a criterion that was never exercised is NOT RUN", id, c.Verdict)
		}
		if c.Missing == "" {
			t.Fatalf("%s is NOT RUN with no stated reason", id)
		}
	}
	s := Overall(in, cs)
	if s.Verdict == VerdictPass {
		t.Fatal("a run that found nothing was reported as a pass")
	}
}

func TestAC3FailsOnAReadBackMismatch(t *testing.T) {
	in := goodRun()
	in.EvidenceMismatched = 1
	in.EvidenceVerified = 69
	c := byID(Score(in), "AC3")
	if c.Verdict != VerdictFail {
		t.Fatalf("a clip that reads back as different bytes scored %s", c.Verdict)
	}
}

func TestAC4FailsOnAnUnattributedPushOrADuplicate(t *testing.T) {
	in := goodRun()
	in.UnattributedPushes = 1
	if c := byID(Score(in), "AC4"); c.Verdict != VerdictFail {
		t.Fatal("a detection with no outbox row did not fail AC4")
	}
	in = goodRun()
	in.DuplicateTags = []string{"aicap-bird-abc"}
	c := byID(Score(in), "AC4")
	if c.Verdict != VerdictFail {
		t.Fatal("a duplicate tag did not fail AC4")
	}
	if !strings.Contains(c.Observed, "aicap-bird-abc") {
		t.Fatalf("the duplicate tag is not named: %s", c.Observed)
	}
}

func TestAC5FailsWhenAPeakExceedsItsCeiling(t *testing.T) {
	in := goodRun()
	in.PeakRSSBytes = (1536 << 20) + 1
	if c := byID(Score(in), "AC5"); c.Verdict != VerdictFail {
		t.Fatal("an RSS peak one byte over the ceiling did not fail AC5")
	}
}

func TestAC5CPUIsNotRunWithoutACeiling(t *testing.T) {
	in := goodRun()
	in.DeclaredCPU = 0
	c := byID(Score(in), "AC5")
	// The other clauses pass, so the criterion is NOT RUN because of the CPU
	// clause. It must not be PASS, and it must not be FAIL either: nothing was
	// exceeded, it was simply not measurable against a declared ceiling.
	if c.Verdict != VerdictNotRun {
		t.Fatalf("AC5 with no CPU ceiling scored %s; it must be NOT RUN", c.Verdict)
	}
	sawNotRun := false
	for _, cl := range c.Clauses {
		if cl.Verdict == VerdictNotRun && strings.Contains(cl.Name, "cpu") {
			sawNotRun = true
		}
	}
	if !sawNotRun {
		t.Fatalf("the CPU clause was not marked NOT RUN: %+v", c.Clauses)
	}
}

func TestAC6FailsOnAnInventedAnswer(t *testing.T) {
	in := goodRun()
	in.UnparsedAIResponses = 1
	if c := byID(Score(in), "AC6"); c.Verdict != VerdictFail {
		t.Fatal("an unparsed sidecar answer did not fail AC6")
	}
	in = goodRun()
	in.HealthyFromFailed = 1
	if c := byID(Score(in), "AC6"); c.Verdict != VerdictFail {
		t.Fatal("a healthy answer derived from a failed read did not fail AC6")
	}
	in = goodRun()
	in.AIP99 = time.Second
	in.DeclaredAIBudget = 800 * time.Millisecond
	if c := byID(Score(in), "AC6"); c.Verdict != VerdictFail {
		t.Fatal("an AI p99 over the declared budget did not fail AC6")
	}
}

func TestAC7IsNotRunWithoutARehearsal(t *testing.T) {
	// This is the criterion a fresh run most often gets wrong by accident.
	in := goodRun()
	in.RehearsedFaults = nil
	c := byID(Score(in), "AC7")
	if c.Verdict != VerdictNotRun {
		t.Fatalf("a run with no rehearsal scored %s for AC7", c.Verdict)
	}
	if c.Missing == "" {
		t.Fatal("NOT RUN with no stated reason")
	}
	if !strings.Contains(c.Missing, "rehearsal") {
		t.Fatalf("the reason does not say what is missing: %q", c.Missing)
	}
	s := Overall(in, Score(in))
	if s.Verdict == VerdictPass {
		t.Fatal("a run with no rehearsal was reported as a pass")
	}
}

func TestAC7FailsWhenARehearsedFaultBehavedUnexpectedly(t *testing.T) {
	in := goodRun()
	in.RehearsedFaults = []Rehearsal{{Fault: "401", Expected: "auth_failed", Observed: "retry"}}
	c := byID(Score(in), "AC7")
	if c.Verdict != VerdictFail {
		t.Fatal("a fault that behaved unexpectedly did not fail AC7")
	}
	if !strings.Contains(c.Observed, "unexpectedly") {
		t.Fatalf("the summary does not say a fault misbehaved: %s", c.Observed)
	}
}

// TestProvisionalWhenTheTargetIsNotReached is the difference between a 40-minute
// demonstration and a seven-day measurement.
func TestProvisionalWhenTheTargetIsNotReached(t *testing.T) {
	in := goodRun()
	in.Elapsed = 47 * time.Minute
	in.Target = 7 * 24 * time.Hour
	cs := Score(in)
	s := Overall(in, cs)
	if !s.Provisional {
		t.Fatal("a 47 minute run on a 7 day target was not marked provisional")
	}
	if s.Verdict != VerdictPass {
		t.Fatalf("verdict = %s; a provisional run still reports what held", s.Verdict)
	}
	np := NotProven(in, s)
	found := false
	for _, n := range np {
		if strings.Contains(n, "PROVISIONAL") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the report does not list the provisional status as something it does not prove: %v", np)
	}
}

func TestANotRunIsNeverPromotedToPass(t *testing.T) {
	// Every criterion NOT RUN, nothing failed: the run is NOT RUN, not PASS.
	in := goodRun()
	in.RehearsedFaults = nil
	in.HeartbeatCount = 0
	in.ResourceSamples = 0
	s := Overall(in, Score(in))
	if s.Verdict == VerdictPass {
		t.Fatal("a run with nothing measured was reported as a pass")
	}
	if s.Verdict != VerdictNotRun {
		t.Fatalf("verdict = %s, want NOT RUN", s.Verdict)
	}
}

func TestTheTruthLineSaysTheSimulatorAndNeverClaimsAVMS(t *testing.T) {
	sim := BuildTruth("recorded loop (no live camera)", "http://127.0.0.1:8098", "xpsim", false, "synthetic")
	if !strings.Contains(sim.PushTarget, "No XProtect server was reachable") {
		t.Fatalf("the push-target line does not disclaim XProtect: %q", sim.PushTarget)
	}
	if !strings.Contains(sim.Source, "no live camera") {
		t.Fatalf("the source line does not disclaim a live camera: %q", sim.Source)
	}
	// A live VMS is the only thing that changes the line, and it needs the flag.
	live := BuildTruth("live rtsp camera", "https://vms.example.com", "XProtect", true, "real")
	if strings.Contains(live.PushTarget, "No XProtect server") {
		t.Fatalf("a run against a declared live VMS still disclaimed it: %q", live.PushTarget)
	}
	// Forgetting the flag must not silently produce a live claim.
	notFlagged := BuildTruth("x", "https://vms.example.com", "XProtect", false, "real")
	if !strings.Contains(notFlagged.PushTarget, "No XProtect server") {
		t.Fatalf("without the explicit flag the line claimed a VMS: %q", notFlagged.PushTarget)
	}
}

func TestNotProvenNamesTheSimulatorTheLoopAndTheProvenance(t *testing.T) {
	in := goodRun()
	np := NotProven(in, Summary{})
	joined := strings.Join(np, "\n")
	for _, want := range []string{
		"simulator",
		"No XProtect server was reachable",
		"recording played in a loop",
		"no ground truth",
		"false-positive rate",
		"Smart Client",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the 'not proven' list does not mention %q:\n%s", want, joined)
		}
	}
}

func TestNotProvenOmitsWhatDoesNotApply(t *testing.T) {
	in := goodRun()
	in.SourceKind = "rtsp"
	in.SourceLabel = "live rtsp camera"
	in.Provenance = "real"
	in.PushTargetIsSimulator = false
	in.RehearsedFaults = []Rehearsal{{Fault: "401", Expected: "auth_failed", Observed: "auth_failed"}}
	np := strings.Join(NotProven(in, Summary{}), "\n")
	if strings.Contains(np, "recording played in a loop") {
		t.Fatal("the list claims the source was a loop when it was rtsp")
	}
	if strings.Contains(np, "labelled synthetic") {
		t.Fatal("the list claims the material was synthetic when it was real")
	}
	if strings.Contains(np, "Nothing reached Milestone") {
		t.Fatal("the list claims nothing reached Milestone for a run marked as hitting a VMS")
	}
	// The claims that always hold must still be there.
	if !strings.Contains(np, "100% detection") {
		t.Fatal("the always-true disclaimer about detection accuracy was dropped")
	}
}

func TestBytesRendersWithoutInventingPrecision(t *testing.T) {
	cases := map[int64]string{
		0:         "0 B",
		1023:      "1023 B",
		1024:      "1.0 KiB",
		1536:      "1.5 KiB",
		1 << 20:   "1.0 MiB",
		1 << 30:   "1.0 GiB",
		200 << 30: "200.0 GiB",
	}
	for in, want := range cases {
		if got := Bytes(in); got != want {
			t.Fatalf("Bytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestEveryCriterionCarriesItsArithmetic(t *testing.T) {
	// A criterion without its arithmetic is a number an agent could re-derive
	// differently and still call the same criterion.
	for _, c := range Score(goodRun()) {
		if c.Arithmetic == "" {
			t.Fatalf("%s has no arithmetic", c.ID)
		}
		if c.How == "" {
			t.Fatalf("%s has no statement of how it is measured", c.ID)
		}
	}
}

func TestTheMarkdownReportRendersEverySection(t *testing.T) {
	in := goodRun()
	cs := Score(in)
	r := Report{
		GeneratedAt: time.Now(),
		Run:         RunInfo{ID: "r1", Label: "baseline", Mode: "live", StartedAt: time.Now().Add(-in.Elapsed), TargetSecs: in.Target.Seconds(), ElapsedSecs: in.Elapsed.Seconds(), ClockSource: "ntp", Database: "poc.db"},
		Verdict:     string(Overall(in, cs).Verdict),
		Criteria:    cs,
		Summary:     Overall(in, cs),
		Faults:      []FaultRow{{At: time.Now(), Reason: "stream_gap", Detail: "ffmpeg exited", Verdict: "FAIL"}},
		Rehearsals:  in.RehearsedFaults,
		Counters:    Counters{Detections: 70, ByStatus: map[string]int{"done": 70}, Heartbeats: 6000, Faults: 1},
		Truth:       BuildTruth("recorded loop (no live camera)", "http://127.0.0.1:8098", "xpsim", false, "real"),
		NotProven:   NotProven(in, Overall(in, cs)),
	}
	md := r.Markdown()
	for _, section := range []string{
		"# POC run report", "## Acceptance criteria", "## Counters", "## Faults",
		"## Rehearsed faults", "## Resource peaks", "## What this report does not prove",
		"AC1", "AC7", "push target: local simulator (xpsim). No XProtect server was reachable during this run.",
	} {
		if !strings.Contains(md, section) {
			t.Fatalf("the Markdown report is missing %q", section)
		}
	}
	if !strings.Contains(md, "400.0 MiB") || !strings.Contains(md, "200.0 GiB") {
		t.Fatal("the resource table is not rendering the measured peaks and the declared ceilings")
	}
	if _, err := r.JSON(); err != nil {
		t.Fatalf("the JSON form does not render: %v", err)
	}
}

func TestAReportWithNoRehearsalSaysSoInTheRehearsalTable(t *testing.T) {
	in := goodRun()
	in.RehearsedFaults = nil
	cs := Score(in)
	r := Report{Run: RunInfo{ID: "r1", TargetSecs: in.Target.Seconds(), ElapsedSecs: in.Elapsed.Seconds()},
		Verdict: string(Overall(in, cs).Verdict), Criteria: cs, Summary: Overall(in, cs),
		Counters:  Counters{ByStatus: map[string]int{}},
		Truth:     BuildTruth("x", "http://127.0.0.1:1", "xpsim", false, "real"),
		NotProven: NotProven(in, Overall(in, cs))}
	md := r.Markdown()
	if !strings.Contains(md, "**NOT RUN.** No fault was injected") {
		t.Fatal("the rehearsal table does not say NOT RUN in words")
	}
}
