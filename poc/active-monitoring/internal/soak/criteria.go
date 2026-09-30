// Package soak scores a run and refuses to start one that would not mean
// anything.
//
// Two halves, deliberately separated:
//
//   - Preflight answers "may this run start at all?", and every refusal names
//     the condition and the consequence. Most of the things that would break a
//     seven-day run in the middle are knowable in the first minute.
//   - Criteria answer "did it hold?", with the arithmetic written out rather
//     than implied, and exactly three verdicts: PASS, FAIL, NOT RUN.
package soak

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Verdict is one of three. There is no fourth. NOT RUN always carries a reason
// and is never written as PASS.
type Verdict string

const (
	VerdictPass   Verdict = "PASS"
	VerdictFail   Verdict = "FAIL"
	VerdictNotRun Verdict = "NOT RUN"
)

// Valid reports whether a verdict is one of the three.
func Valid(v Verdict) bool {
	switch v {
	case VerdictPass, VerdictFail, VerdictNotRun:
		return true
	}
	return false
}

// Criterion is one acceptance criterion's score.
type Criterion struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Verdict Verdict `json:"verdict"`
	// How is measured, in the operator's words. Printed in the report so nobody
	// has to trust the verdict without reading the method.
	How string `json:"how"`
	// Arithmetic is the exact expression, so an agent cannot re-derive a
	// different number and call it the same criterion.
	Arithmetic string `json:"arithmetic"`
	// Observed is the measured values, as text.
	Observed string `json:"observed"`
	// Missing is why NOT RUN was chosen, if it was.
	Missing string `json:"missing,omitempty"`
	// Clauses are the individual sub-checks, so a FAIL names the clause that
	// failed rather than just the criterion.
	Clauses []Clause `json:"clauses,omitempty"`
}

// Clause is one measurable part of a criterion.
type Clause struct {
	Name    string  `json:"name"`
	Verdict Verdict `json:"verdict"`
	Value   string  `json:"value"`
	Want    string  `json:"want"`
}

func pass(name, value, want string) Clause {
	return Clause{Name: name, Verdict: VerdictPass, Value: value, Want: want}
}

func fail(name, value, want string) Clause {
	return Clause{Name: name, Verdict: VerdictFail, Value: value, Want: want}
}

func notRun(name, why string) Clause {
	return Clause{Name: name, Verdict: VerdictNotRun, Value: "not measured", Want: why}
}

// rollUp turns clauses into a criterion verdict. Any FAIL makes the criterion
// FAIL; a NOT RUN with no FAIL makes it NOT RUN; otherwise PASS. A NOT RUN is
// never promoted to PASS, which is the one promotion this module forbids.
func rollUp(cs []Clause) Verdict {
	if len(cs) == 0 {
		return VerdictNotRun
	}
	sawNotRun := false
	for _, c := range cs {
		switch c.Verdict {
		case VerdictFail:
			return VerdictFail
		case VerdictNotRun:
			sawNotRun = true
		}
	}
	if sawNotRun {
		return VerdictNotRun
	}
	return VerdictPass
}

// Inputs is everything the criteria need, gathered by the caller. It is a plain
// struct so the scoring is a pure function of measurements and can be tested
// without a run.
type Inputs struct {
	Elapsed  time.Duration
	Target   time.Duration
	RunEnded bool

	// AC1
	ProcessExits   int
	HeartbeatGaps  []GapMeasurement
	HeartbeatCount int
	// GapThreshold is the boundary above which a heartbeat silence is a dropout.
	// It defaults to 30 seconds, which is the value in the acceptance table, and
	// it is applied HERE rather than trusted from the caller: a criterion that
	// filters in two places has a boundary that can differ between them.
	GapThreshold time.Duration

	// AC2
	TotalDetections   int
	StalledDetections []StalledDetection
	MaxStall          time.Duration

	// AC3
	EvidenceStored     int
	EvidenceVerified   int
	EvidenceMismatched int

	// AC4
	PushAttempts       int
	PushesWithResult   int
	UnattributedPushes int
	DuplicateTags      []string
	DeadLettered       int

	// AC5
	PeakRSSBytes   int64
	PeakCPUPercent float64
	// CPUMeasured says whether PeakCPUPercent is a measurement. A zero with no
	// flag is a ceiling that passes on any load at all.
	CPUMeasured     bool
	PeakDBBytes     int64
	PeakDiskBytes   int64
	DeclaredMaxDisk int64
	DeclaredRSS     int64
	DeclaredDB      int64
	DeclaredCPU     float64
	ResourceSamples int

	// AC6
	UnparsedAIResponses int
	HealthyFromFailed   int
	AIP99               time.Duration
	AIP99Samples        int
	DeclaredAIBudget    time.Duration

	// AC7
	RehearsedFaults []Rehearsal

	// FalsePositives is the human's marking. A false-positive rate exists only
	// when a person looked at the events and said which were false. This is the
	// entire mechanism: a count a human filled in, and NOT RUN when nobody did.
	FalsePositives *HumanMarking

	// Provenance of the material the numbers came from, printed next to them.
	Provenance string

	// Truth line
	PushTargetIsSimulator bool
	PushTargetName        string
	SourceKind            string
	SourceLabel           string
}

// GapMeasurement is one heartbeat silence.
type GapMeasurement struct {
	From, To time.Time
	Seconds  float64
}

// StalledDetection is a detection that never reached a terminal state.
type StalledDetection struct {
	ID     string
	Status string
	Age    time.Duration
}

// Rehearsal is one injected fault and what actually happened.
type Rehearsal struct {
	Fault    string
	Expected string
	Observed string
	At       time.Time
	Detail   string
}

// AC1Name and friends are the criterion names, kept here so the report and the
// one-pager cannot disagree with the arithmetic.
const (
	AC1Name = "zero dropouts"
	AC2Name = "zero lost events"
	AC3Name = "evidence intact"
	AC4Name = "push without loss or duplication"
	AC5Name = "resources bounded"
	AC6Name = "no invented answers"
	AC7Name = "modelled faults behaved"
)

// HumanMarking is a count a person filled in by looking at the events. It is not
// computed, inferred, estimated or modelled by anything in this repository.
//
// There is no default of zero here. Zero would be a measurement, and a person
// who has not looked at a single event has not made one. The distinction is the
// whole point: "nobody marked any" and "nobody was false" are different claims
// and only one of them is ever available.
type HumanMarking struct {
	// Examined is how many events a person actually looked at. Zero is the
	// normal case and is why the metric is NOT RUN.
	Examined int
	// False is how many of those were judged false.
	False int
	// By and When make the marking auditable: a rate with no author and no date
	// is a number somebody typed.
	By   string
	When time.Time
	// Note is the person's own words about what they saw.
	Note string
}

// Rate is the false-positive rate, or ok=false when nobody marked anything.
func (h *HumanMarking) Rate() (float64, bool) {
	if h == nil || h.Examined <= 0 {
		return 0, false
	}
	return float64(h.False) / float64(h.Examined), true
}

// Verdict is what the report prints for the false-positive row. It is NOT RUN
// until a human has looked, and it never becomes PASS because nothing went
// wrong.
func (h *HumanMarking) Verdict() (Verdict, string) {
	if h == nil || h.Examined <= 0 {
		return VerdictNotRun, "no human marked any event as false, so there is no false-positive rate to print. It is NOT RUN, not zero."
	}
	if h.By == "" {
		return VerdictNotRun, fmt.Sprintf("%d of %d examined were false, but nobody is recorded as having marked them, and a rate with no author is a number somebody typed", h.False, h.Examined)
	}
	r, _ := h.Rate()
	return VerdictPass, fmt.Sprintf("%d of %d examined events were marked false by %s on %s (%.1f%%); %d of %d detections were examined",
		h.False, h.Examined, h.By, h.When.Format("2006-01-02"), r*100, h.Examined, h.Examined)
}

// Score computes all seven criteria. It is a pure function: same inputs, same
// verdicts, no side effects, no clock reads.
//
// The false-positive rate is deliberately NOT a criterion. It is reported on its
// own row, as NOT RUN until a human has marked events, and it never contributes
// to PASS. A metric that can only ever be NOT RUN or PASS-with-a-human is not
// something to let roll up into an overall verdict.
func Score(in Inputs) []Criterion {
	return []Criterion{
		scoreAC1(in),
		scoreAC2(in),
		scoreAC3(in),
		scoreAC4(in),
		scoreAC5(in),
		scoreAC6(in),
		scoreAC7(in),
	}
}

func scoreAC1(in Inputs) Criterion {
	c := Criterion{
		ID: "AC1", Name: AC1Name, Verdict: VerdictFail,
		How:        "no process exited and no heartbeat interval exceeded the gap threshold",
		Arithmetic: "process_exits == 0 and count(heartbeat_gap > 30s) == 0",
	}
	// Zero tolerance, on both halves, counted independently: a run with no
	// exits and a 45 second silence still failed.
	if in.ProcessExits == 0 {
		c.Clauses = append(c.Clauses, pass("process exits", "0", "0"))
	} else {
		c.Clauses = append(c.Clauses, fail("process exits", fmt.Sprint(in.ProcessExits), "0; AC1 is absolute zero, not a rate"))
	}
	if len(overThreshold(in.HeartbeatGaps, gapThreshold(in))) == 0 {
		c.Clauses = append(c.Clauses, pass("heartbeat gaps over "+gapThreshold(in).String(), "0", "0"))
	} else {
		over := overThreshold(in.HeartbeatGaps, gapThreshold(in))
		worst := over[0]
		for _, g := range over {
			if g.Seconds > worst.Seconds {
				worst = g
			}
		}
		c.Clauses = append(c.Clauses, fail("heartbeat gaps over "+gapThreshold(in).String(), fmt.Sprintf("%d (longest %.1fs)", len(over), worst.Seconds), "0"))
	}
	if in.HeartbeatCount == 0 {
		c.Clauses = append(c.Clauses, notRun("heartbeat evidence", "no heartbeat was ever recorded, so liveness cannot be shown; a run that never reported cannot be said to have been alive"))
	} else {
		c.Clauses = append(c.Clauses, pass("heartbeats recorded", fmt.Sprint(in.HeartbeatCount), "at least 1"))
	}
	c.Observed = fmt.Sprintf("%d process exit(s), %d heartbeat gap(s) over %s, %d heartbeat(s) recorded",
		in.ProcessExits, len(overThreshold(in.HeartbeatGaps, gapThreshold(in))), gapThreshold(in), in.HeartbeatCount)
	c.Verdict = rollUp(c.Clauses)
	applyMissing(&c)
	return c
}

// DefaultGapThreshold is the AC1 boundary.
const DefaultGapThreshold = 30 * time.Second

func gapThreshold(in Inputs) time.Duration {
	if in.GapThreshold <= 0 {
		return DefaultGapThreshold
	}
	return in.GapThreshold
}

func overThreshold(gaps []GapMeasurement, threshold time.Duration) []GapMeasurement {
	var out []GapMeasurement
	for _, g := range gaps {
		if g.Seconds > threshold.Seconds() {
			out = append(out, g)
		}
	}
	return out
}

// applyMissing copies the reason out of a NOT RUN clause onto the criterion, so
// the report's "why NOT RUN" line is never empty. An empty reason is what turns
// a NOT RUN into something a reader has to guess about.
func applyMissing(c *Criterion) {
	if c.Verdict != VerdictNotRun || c.Missing != "" {
		return
	}
	var why []string
	for _, cl := range c.Clauses {
		if cl.Verdict == VerdictNotRun {
			why = append(why, fmt.Sprintf("%s: %s", cl.Name, cl.Want))
		}
	}
	if len(why) > 0 {
		c.Missing = strings.Join(why, "; ")
	}
}

func scoreAC2(in Inputs) Criterion {
	c := Criterion{
		ID: "AC2", Name: AC2Name, Verdict: VerdictFail,
		How:        "every detection reached a terminal state; nothing sat in a non-terminal state for longer than five minutes",
		Arithmetic: "count(detections where status != 'done' and age > 5m) == 0",
		Observed:   fmt.Sprintf("%d detection(s), %d not terminal past the limit", in.TotalDetections, len(in.StalledDetections)),
	}
	if in.TotalDetections == 0 {
		// Zero events makes the arithmetic trivially true, and trivially true is
		// not evidence. A run that produced no detections has not shown that it
		// can keep the ones it produces, so the criterion is NOT RUN.
		c.Verdict = VerdictNotRun
		c.Observed = "no detection was recorded, so nothing was at risk of being lost"
		c.Missing = "no detection was recorded, so the ability to keep one was never exercised. The arithmetic is trivially satisfied and a criterion nobody exercised is NOT RUN, not PASS."
		c.Clauses = []Clause{notRun("detections recorded", "the run produced no detection, so loss was never exercised")}
		return c
	}
	c.Clauses = []Clause{
		pass("detections recorded", fmt.Sprint(in.TotalDetections), "at least 1"),
	}
	if len(in.StalledDetections) > 0 {
		names := make([]string, 0, len(in.StalledDetections))
		for _, s := range in.StalledDetections {
			names = append(names, fmt.Sprintf("%s(%s, %s)", s.ID, s.Status, s.Age.Round(time.Second)))
		}
		sort.Strings(names)
		c.Observed = fmt.Sprintf("%d detection(s), %d not terminal past the limit: %s",
			in.TotalDetections, len(in.StalledDetections), strings.Join(names, " "))
		c.Clauses = append(c.Clauses, fail("detections not terminal past 5m", strings.Join(names, " "), "none"))
	} else {
		c.Clauses = append(c.Clauses, pass("detections not terminal past 5m", "0", "none"))
	}
	c.Verdict = rollUp(c.Clauses)
	applyMissing(&c)
	return c
}

func scoreAC3(in Inputs) Criterion {
	c := Criterion{
		ID: "AC3", Name: AC3Name, Verdict: VerdictFail,
		How:        "every stored clip is read back from the store and its SHA-256 compared with the recorded value",
		Arithmetic: "count(clip_stored where readback_sha256 != sha256) == 0",
		Observed:   fmt.Sprintf("%d stored, %d verified on read-back, %d mismatched", in.EvidenceStored, in.EvidenceVerified, in.EvidenceMismatched),
	}
	if in.EvidenceStored == 0 {
		c.Verdict = VerdictNotRun
		c.Missing = "no clip was stored, so integrity was never exercised; a run with no detections cannot pass this criterion and must not be counted as passing it"
		c.Clauses = []Clause{notRun("read-back verification", "no stored clip to read back")}
		return c
	}
	c.Clauses = []Clause{pass("clips stored", fmt.Sprint(in.EvidenceStored), "any")}
	if in.EvidenceMismatched == 0 {
		c.Clauses = append(c.Clauses, pass("read-back mismatches", "0", "0"))
	} else {
		c.Clauses = append(c.Clauses, fail("read-back mismatches", fmt.Sprint(in.EvidenceMismatched), "0"))
	}
	c.Verdict = rollUp(c.Clauses)
	applyMissing(&c)
	return c
}

func scoreAC4(in Inputs) Criterion {
	c := Criterion{
		ID: "AC4", Name: AC4Name, Verdict: VerdictFail,
		How:        "every push attempt has a recorded outcome, every detection has an outbox row, and no tag produced two events",
		Arithmetic: "count(detections with no outbox row) == 0 and count(attempts with no result) == 0 and count(duplicate ids per tag) == 0",
		Observed: fmt.Sprintf("%d attempt(s), %d with a result, %d detection(s) with no outbox row, %d duplicate tag(s), %d dead-lettered",
			in.PushAttempts, in.PushesWithResult, in.UnattributedPushes, len(in.DuplicateTags), in.DeadLettered),
	}
	if in.PushAttempts == 0 {
		c.Verdict = VerdictNotRun
		c.Missing = "nothing was ever pushed, so loss and duplication were never exercised"
		c.Clauses = []Clause{notRun("push attempts", "no attempt was made")}
		return c
	}
	c.Clauses = []Clause{pass("push attempts", fmt.Sprint(in.PushAttempts), "any")}
	if in.PushesWithResult == in.PushAttempts {
		c.Clauses = append(c.Clauses, pass("attempts with a result", fmt.Sprint(in.PushesWithResult), fmt.Sprint(in.PushAttempts)))
	} else {
		c.Clauses = append(c.Clauses, fail("attempts with a result", fmt.Sprint(in.PushesWithResult), fmt.Sprint(in.PushAttempts)))
	}
	if in.UnattributedPushes == 0 {
		c.Clauses = append(c.Clauses, pass("detections with no outbox row", "0", "0"))
	} else {
		c.Clauses = append(c.Clauses, fail("detections with no outbox row", fmt.Sprint(in.UnattributedPushes), "0"))
	}
	if len(in.DuplicateTags) == 0 {
		c.Clauses = append(c.Clauses, pass("duplicate tags", "0", "0"))
	} else {
		dups := append([]string(nil), in.DuplicateTags...)
		sort.Strings(dups)
		c.Observed = fmt.Sprintf("%d attempt(s), %d with a result, %d detection(s) with no outbox row, %d duplicate tag(s): %s, %d dead-lettered",
			in.PushAttempts, in.PushesWithResult, in.UnattributedPushes, len(dups), strings.Join(dups, " "), in.DeadLettered)
		c.Clauses = append(c.Clauses, fail("duplicate tags", strings.Join(dups, " "), "0"))
	}
	c.Verdict = rollUp(c.Clauses)
	applyMissing(&c)
	return c
}

func scoreAC5(in Inputs) Criterion {
	c := Criterion{
		ID: "AC5", Name: AC5Name, Verdict: VerdictFail,
		How:        "measured peaks against the ceilings the operator declared before the run",
		Arithmetic: "rss <= declared and db <= declared and disk <= declared and (cpu <= declared if declared)",
		Observed: fmt.Sprintf("rss %s, db %s, disk %s of %s, cpu %.1f%% of %.1f%%",
			Bytes(in.PeakRSSBytes), Bytes(in.PeakDBBytes), Bytes(in.PeakDiskBytes), Bytes(in.DeclaredMaxDisk),
			in.PeakCPUPercent, in.DeclaredCPU),
	}
	if in.ResourceSamples == 0 {
		c.Verdict = VerdictNotRun
		c.Missing = "no resource sample was recorded, so no peak was measured"
		c.Clauses = []Clause{notRun("resource peaks", "no samples")}
		return c
	}
	c.Clauses = []Clause{pass("samples", fmt.Sprint(in.ResourceSamples), "at least 1")}
	c.Clauses = append(c.Clauses, limit("rss", in.PeakRSSBytes, in.DeclaredRSS))
	c.Clauses = append(c.Clauses, limit("database", in.PeakDBBytes, in.DeclaredDB))
	c.Clauses = append(c.Clauses, limit("data root", in.PeakDiskBytes, in.DeclaredMaxDisk))
	if in.DeclaredCPU > 0 {
		if !in.CPUMeasured {
			// A zero here would be a ceiling that passes on any load at all, and
			// a seven-day run scored against one is a run whose CPU clause was
			// never checked.
			c.Clauses = append(c.Clauses, notRun("cpu (ffmpeg+python)",
				"no child-process CPU was measured, so the clause was not evaluated. A zero would be a number nobody took."))
		} else if in.PeakCPUPercent <= in.DeclaredCPU {
			c.Clauses = append(c.Clauses, pass("cpu (ffmpeg+python)", fmt.Sprintf("%.1f%%", in.PeakCPUPercent), fmt.Sprintf("<= %.1f%%", in.DeclaredCPU)))
		} else {
			c.Clauses = append(c.Clauses, fail("cpu (ffmpeg+python)", fmt.Sprintf("%.1f%%", in.PeakCPUPercent), fmt.Sprintf("<= %.1f%%", in.DeclaredCPU)))
		}
	} else {
		c.Clauses = append(c.Clauses, notRun("cpu (ffmpeg+python)", "no CPU ceiling was declared, so this clause is not measured rather than passed"))
	}
	c.Verdict = rollUp(c.Clauses)
	applyMissing(&c)
	return c
}

func limit(name string, value, declared int64) Clause {
	if declared <= 0 {
		return notRun(name, "no ceiling was declared")
	}
	if value <= declared {
		return pass(name, Bytes(value), "<= "+Bytes(declared))
	}
	return fail(name, Bytes(value), "<= "+Bytes(declared))
}

func scoreAC6(in Inputs) Criterion {
	c := Criterion{
		ID: "AC6", Name: AC6Name, Verdict: VerdictFail,
		How:        "no unparsed sidecar answer, no healthy reading derived from a failed one, and the AI p99 inside the declared budget",
		Arithmetic: "count(unparsed responses) == 0 and count(healthy derived from a failed read) == 0 and ai_p99 <= declared",
		Observed:   fmt.Sprintf("%d unparsed, %d healthy-from-failed, p99 %s over %d samples", in.UnparsedAIResponses, in.HealthyFromFailed, in.AIP99, in.AIP99Samples),
	}
	c.Clauses = []Clause{}
	if in.UnparsedAIResponses == 0 {
		c.Clauses = append(c.Clauses, pass("unparsed responses", "0", "0"))
	} else {
		c.Clauses = append(c.Clauses, fail("unparsed responses", fmt.Sprint(in.UnparsedAIResponses), "0"))
	}
	if in.HealthyFromFailed == 0 {
		c.Clauses = append(c.Clauses, pass("healthy answers derived from a failed read", "0", "0"))
	} else {
		c.Clauses = append(c.Clauses, fail("healthy answers derived from a failed read", fmt.Sprint(in.HealthyFromFailed), "0"))
	}
	if in.AIP99Samples == 0 {
		c.Clauses = append(c.Clauses, notRun("AI p99", "no AI round trip was measured"))
	} else if in.DeclaredAIBudget <= 0 {
		c.Clauses = append(c.Clauses, notRun("AI p99 against a budget", "no budget was declared, so the p99 was measured but not scored"))
	} else if in.AIP99 <= in.DeclaredAIBudget {
		c.Clauses = append(c.Clauses, pass("AI p99", in.AIP99.String(), "<= "+in.DeclaredAIBudget.String()))
	} else {
		c.Clauses = append(c.Clauses, fail("AI p99", in.AIP99.String(), "<= "+in.DeclaredAIBudget.String()))
	}
	c.Verdict = rollUp(c.Clauses)
	applyMissing(&c)
	return c
}

func scoreAC7(in Inputs) Criterion {
	c := Criterion{
		ID: "AC7", Name: AC7Name, Verdict: VerdictFail,
		How:        "every rehearsed fault has a recorded outcome matching the expected one",
		Arithmetic: "count(rehearsals where observed != expected) == 0 and count(rehearsals) > 0",
		Observed:   fmt.Sprintf("%d rehearsed fault(s)", len(in.RehearsedFaults)),
	}
	if len(in.RehearsedFaults) == 0 {
		c.Verdict = VerdictNotRun
		c.Missing = "no fault was rehearsed on this run, so nothing was proven about behaviour under failure. A run with no rehearsal cannot pass this criterion and must not be counted as passing it."
		c.Clauses = []Clause{notRun("rehearsed faults", "no rehearsal ran on this run")}
		return c
	}
	mismatched := 0
	for _, r := range in.RehearsedFaults {
		if r.Observed != r.Expected {
			mismatched++
			c.Clauses = append(c.Clauses, fail(r.Fault, r.Observed, r.Expected))
			continue
		}
		c.Clauses = append(c.Clauses, pass(r.Fault, r.Observed, r.Expected))
	}
	if mismatched > 0 {
		c.Observed = fmt.Sprintf("%d rehearsed fault(s), %d behaved unexpectedly", len(in.RehearsedFaults), mismatched)
	}
	c.Verdict = rollUp(c.Clauses)
	applyMissing(&c)
	return c
}

// Summary is the run's overall verdict.
type Summary struct {
	Verdict Verdict
	// Provisional is true when elapsed is below the target. A run that has not
	// reached its target has not concluded, and the verdict says so.
	Provisional bool
	Elapsed     time.Duration
	Target      time.Duration
	Pass        int
	Fail        int
	NotRun      int
	// Blocking is the names of the criteria that failed, for the header.
	Blocking []string
}

// Overall rolls the criteria up into one verdict, and distinguishes PASS from
// PROVISIONAL. A 40-minute run cannot report a pass on a seven-day target, and
// the distinction is the whole reason the target is declared up front.
func Overall(in Inputs, cs []Criterion) Summary {
	s := Summary{Elapsed: in.Elapsed, Target: in.Target}
	for _, c := range cs {
		switch c.Verdict {
		case VerdictPass:
			s.Pass++
		case VerdictFail:
			s.Fail++
			s.Blocking = append(s.Blocking, c.ID)
		case VerdictNotRun:
			s.NotRun++
		}
	}
	s.Verdict = VerdictPass
	if s.Fail > 0 {
		s.Verdict = VerdictFail
	} else if s.NotRun > 0 {
		// A criterion that was never measured does not become a pass. The run
		// did not hold, it was not fully shown to hold.
		s.Verdict = VerdictNotRun
	}
	if in.Elapsed < in.Target {
		s.Provisional = true
	}
	return s
}

// Bytes renders a byte count in the units an operator reads, without inventing a
// decimal point that changes a comparison.
func Bytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
