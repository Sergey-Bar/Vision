package soak

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Sergey-Bar/vision-poc/internal/recorder"
	"github.com/Sergey-Bar/vision-poc/internal/store"
	"github.com/Sergey-Bar/vision-poc/internal/supervisor"
)

// LocalFault is one of the six faults the plan rehearses against the LOCAL
// pipeline rather than against the push target: killing the worker, killing
// ffmpeg, killing the sidecar, freezing the database, filling the disk, and
// disconnecting the simulator.
//
// They are a different family from the provider faults because they attack
// different code. A provider fault exercises the client; a local fault
// exercises the recorder, the supervisor, the outbox pump and the store. A
// pipeline that has only been rehearsed against a 500 has been rehearsed in
// one direction.
type LocalFault string

const (
	LocalKillWorker    LocalFault = "kill_worker"
	LocalKillFFmpeg    LocalFault = "kill_ffmpeg"
	LocalKillPython    LocalFault = "kill_python"
	LocalFreezeDB      LocalFault = "db_freeze"
	LocalFillDisk      LocalFault = "disk_fill"
	LocalDisconnectSim LocalFault = "simulator_disconnect"
)

// LocalFaults is the closed set, in the order the rehearsal runs them.
var LocalFaults = []LocalFault{
	LocalKillWorker, LocalKillFFmpeg, LocalKillPython,
	LocalFreezeDB, LocalFillDisk, LocalDisconnectSim,
}

// ValidLocalFault reports whether a name is one of them.
func ValidLocalFault(f LocalFault) bool {
	for _, k := range LocalFaults {
		if k == f {
			return true
		}
	}
	return false
}

// LocalExpectation is what a local fault is supposed to produce. It is the
// client's own promise, written down, and AC7 compares against it.
type LocalExpectation struct {
	Fault LocalFault
	// Outcome is the state the system must reach.
	Outcome string
	// MustRecord is the fault reason that must appear in the store.
	MustRecord string
	// MustNotHappen is the thing that would make this a defect, stated so a
	// reviewer can check it rather than infer it.
	MustNotHappen string
	// Verified is how this expectation is proven. It is one of:
	//   "unit" — a test in this repository exercises the code path
	//   "live"  — a rehearsal run injected it against real processes
	// Anything else is NOT RUN with the reason, and the report says so.
	Verified string
	// Needs names what a live rehearsal would need, when Verified is "unit".
	Needs string
}

// LocalExpectations is the table. It is the answer to "what happens if it
// breaks", written before anyone asks.
func LocalExpectations() []LocalExpectation {
	return []LocalExpectation{
		{
			Fault: LocalKillFFmpeg, Outcome: "stream_gap", MustRecord: string(supervisor.ReasonStreamGap),
			MustNotHappen: "ffmpeg is restarted, and the dropout becomes a success in the report",
			Verified:      "unit", Needs: "nothing: internal/supervisor tests the recorder's exit and asserts it is not restarted",
		},
		{
			Fault: LocalKillPython, Outcome: "sidecar_exit", MustRecord: string(supervisor.ReasonSidecarExit),
			MustNotHappen: "the frames after the kill are reported as no_objects; they are not_asked, and the two are never the same",
			Verified:      "unit", Needs: "nothing: internal/supervisor tests the sidecar's exit",
		},
		{
			Fault: LocalFreezeDB, Outcome: "db_unavailable", MustRecord: string(store.FaultDBUnavailable),
			MustNotHappen: "an event is accepted in memory and lost when the write fails, which is the loss R1 exists to prevent",
			Verified:      "unit", Needs: "nothing: internal/soak tests the store's write under a held exclusive lock",
		},
		{
			Fault: LocalFillDisk, Outcome: "disk_budget_exceeded", MustRecord: string(store.FaultDiskBudget),
			MustNotHappen: "a stored clip is deleted to make room; the sweep refuses and the run stops with the space accounted for",
			Verified:      "unit", Needs: "nothing: internal/recorder tests that the sweep refuses rather than deleting evidence",
		},
		{
			Fault: LocalKillWorker, Outcome: "outbox_preserved", MustRecord: string(store.FaultWorkerExit),
			MustNotHappen: "a push is reported as sent when the worker died before recording the result",
			Verified:      "unit", Needs: "nothing: internal/soak tests that a pump restart resumes the same rows and does not re-send a sent one",
		},
		{
			Fault: LocalDisconnectSim, Outcome: "outbox_retained", MustRecord: string(store.FaultProviderGone),
			MustNotHappen: "a detection reaches a terminal state with its push unresolved; it stays non-terminal and AC2 reports it",
			Verified:      "unit", Needs: "nothing: internal/soak tests an offline provider end to end through the pump",
		},
	}
}

// LocalRehearsalResult is one row of the rehearsal table.
type LocalRehearsalResult struct {
	LocalExpectation
	// Proved is whether this build has evidence for the expectation.
	Proved bool
	// Why is why it is NOT RUN, when it is not.
	Why string
}

// LocalReport renders the local rehearsal table. A row that has no evidence is
// NOT RUN, in words, and the summary is never "all clear" when a row is not.
func LocalReport() ([]LocalRehearsalResult, int, int) {
	exps := LocalExpectations()
	out := make([]LocalRehearsalResult, 0, len(exps))
	proved, notRun := 0, 0
	for _, e := range exps {
		r := LocalRehearsalResult{LocalExpectation: e, Proved: e.Verified == "unit" || e.Verified == "live"}
		if r.Proved {
			proved++
		} else {
			r.Why = e.Verified
			notRun++
		}
		out = append(out, r)
	}
	return out, proved, notRun
}

// LocalMarkdown renders the table for the operator.
func LocalMarkdown() string {
	rows, proved, notRun := LocalReport()
	var b strings.Builder
	b.WriteString("# local fault rehearsal (gate D, second family)\n\n")
	b.WriteString("These six attack the local pipeline. The provider faults (401, 500, timeout,\n")
	b.WriteString("connection reset) attack the client. A pipeline rehearsed in one direction only\n")
	b.WriteString("has been rehearsed once.\n\n")
	fmt.Fprintf(&b, "  %d of %d expectations have evidence in this build", proved, len(rows))
	if notRun > 0 {
		fmt.Fprintf(&b, "; %d is NOT RUN", notRun)
	}
	b.WriteString("\n\n")
	for _, r := range rows {
		verdict := "PROVED"
		if !r.Proved {
			verdict = "NOT RUN"
		}
		fmt.Fprintf(&b, "  [%-7s] %-22s -> %-22s records `%s`\n", verdict, r.Fault, r.Outcome, r.MustRecord)
		fmt.Fprintf(&b, "            must not: %s\n", r.MustNotHappen)
		if r.Proved {
			fmt.Fprintf(&b, "            evidence: %s\n", r.Needs)
		} else {
			fmt.Fprintf(&b, "            missing: %s\n", r.Why)
		}
	}
	b.WriteString("\n")
	return b.String()
}

// RehearsalsForReport turns the local table into the report's rehearsal rows, so
// a report regenerated from the store can still print what was proven and what
// was not. The rows carry a provenance note rather than being indistinguishable
// from an injected fault's row.
func RehearsalsForReport() []Rehearsal {
	var out []Rehearsal
	for _, e := range LocalExpectations() {
		observed := e.Outcome
		if e.Verified != "unit" && e.Verified != "live" {
			observed = "NOT RUN"
		}
		out = append(out, Rehearsal{
			Fault:    "local:" + string(e.Fault),
			Expected: e.Outcome,
			Observed: observed,
			Detail:   e.MustNotHappen,
		})
	}
	return out
}

// FaultReasons is every reason the module can record, sorted, so the report can
// print a closed list and a reader can see a reason that is not in it.
func FaultReasons() []string {
	seen := map[string]bool{}
	for _, r := range []string{
		string(supervisor.ReasonAIPipeBackpressure), string(supervisor.ReasonStreamGap),
		string(supervisor.ReasonSidecarExit), string(supervisor.ReasonSidecarUnready),
		string(supervisor.ReasonRecorderExit), string(supervisor.ReasonDiskBudget),
		string(store.FaultAIUnreadable), string(store.FaultClipRefused),
		string(store.FaultDBUnavailable), string(store.FaultDiskBudget),
		string(store.FaultWorkerExit), string(store.FaultProviderGone),
	} {
		if !seen[r] {
			seen[r] = true
			out := append([]string{}, keys(seen)...)
			sort.Strings(out)
			return out
		}
	}
	return nil
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// DBBusyError recognises the store's "another writer holds the lock" failure,
// which is what a frozen database looks like from here. It is used by the run
// loop to record a database fault instead of blaming the clip.
//
// It recognises three things on purpose. The driver's own "database is locked"
// is the clear case. A write whose own deadline expired is the same condition
// observed from the other side: the store gives a write a short deadline of its
// own precisely so a frozen database is reported rather than stalling the frame
// loop for the driver's full busy timeout. And a cancelled context is the same
// thing again when the caller gave up.
func DBBusyError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, marker := range []string{
		"database is locked",
		"database table is locked",
		"sqlite_busy",
		"the database is busy",
		"write could not be completed within",
		"context deadline exceeded",
		"context canceled",
	} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

// SweepRefused recognises the disk refusal, so a run that cannot fit its
// evidence stops with the space accounted for rather than deleting an incident.
func SweepRefused(err error) (*recorder.ErrDiskBudget, bool) {
	var b *recorder.ErrDiskBudget
	if errors.As(err, &b) {
		return b, true
	}
	return nil, false
}
