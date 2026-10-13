package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/config"
	"github.com/Sergey-Bar/vision-poc/internal/soak"
	"github.com/Sergey-Bar/vision-poc/internal/store"
)

// cmdSoak is T24's `poc soak start|status|report`.
//
// The gate into a run is preflight, which refuses by name. `start` therefore
// runs preflight first and refuses to launch on a single failing row, rather
// than launching a seven-day run and finding out at hour six.
func cmdSoak(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("soak: say what you want: start, status, or report. A bare `poc soak` is not one of them, and running the pipeline because someone mistyped is the most expensive way to spend a week")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "start":
		return soakStart(rest)
	case "status":
		return soakStatus(rest)
	case "report":
		// report is the same thing from the store; both spellings are accepted
		// because the plan names `poc soak report` and people type `poc report`.
		return cmdReport(append([]string{"--data-root", dataRootOrFlag(rest)}, rest...))
	case "faults":
		return soakFaults(rest)
	default:
		return fmt.Errorf("soak: %q is not one of start, status, report, faults", sub)
	}
}

func dataRootOrFlag(args []string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == "--data-root" && i+1 < len(args) {
			return args[i+1]
		}
	}
	if v := os.Getenv("SP_POC_DATA_ROOT"); v != "" {
		return v
	}
	return ""
}

func soakStart(args []string) error {
	flags, err := flagSet("soak start", map[string]string{
		"data-root": "", "sim-addr": "127.0.0.1:8098", "seconds": "150",
		"clock-state": "", "power-state": "",
	}, args)
	if err != nil {
		return err
	}
	root := flags["data-root"]
	if root == "" {
		root = os.Getenv("SP_POC_DATA_ROOT")
	}
	if root == "" {
		root = filepath.Join(repoRoot(), ".poc-data")
	}

	// Each step gets ONLY the flags IT accepts.
	//
	// The first version forwarded every flag to both preflight and the
	// rehearsal, so `pocctl soak start --data-root X` was refused by preflight
	// with "--data-root is not a flag of this subcommand" and the command could
	// never run at all. A gate that cannot be opened is not a gate.
	//
	// The rehearsal writes into the RUN'S OWN data root, not a scratch one.
	//
	// It used to write elsewhere, which meant AC7 was `NOT RUN` in normal use:
	// the run's own store had no rehearsal rows, so the criterion that is supposed
	// to say "this build was shown to survive failure" had nothing to score. A
	// rehearsal is evidence about a build, and evidence belongs beside the
	// measurement it justifies.
	reh := []string{"--data-root", root, "--sim-addr", flags["sim-addr"]}
	gateD := "PASS"
	if err := cmdRehearse(reh); err != nil {
		gateD = "FAIL"
	}
	if gateD != "PASS" {
		fmt.Println()
		fmt.Println("soak start: REFUSED at gate D. A week of uptime measured on code that has not")
		fmt.Println("been shown to survive a 500 is a week spent measuring the wrong thing.")
		return fmt.Errorf("soak start: gate D is not green, so the run clock has not started")
	}

	pre := []string{"--gate-d", gateD, "--gate-d-age", "0s"}
	if flags["clock-state"] != "" {
		pre = append(pre, "--clock-state", flags["clock-state"])
	}
	if flags["power-state"] != "" {
		pre = append(pre, "--power-state", flags["power-state"])
	}
	if err := cmdPreflight(pre); err != nil {
		fmt.Println()
		fmt.Println("soak start: REFUSED at preflight. The run clock has not started and no data was written.")
		return err
	}
	fmt.Println()
	fmt.Println("soak start: preflight and gate D are both green.")
	fmt.Println("  The run itself is `watch`, so the seven-day process is the one that holds the")
	fmt.Println("  recorder, the sidecar and the clock:")
	fmt.Println()
	fmt.Printf("    watch --data-root %q --soak-target 168h\n", root)
	fmt.Println()
	fmt.Println("  This is deliberate. `soak start` refusing IS the gate; `watch` is the thing that")
	fmt.Println("  runs for a week, and it is a plain long-lived process you can stop with ctrl-C.")
	return nil
}

func soakStatus(args []string) error {
	flags, err := flagSet("soak status", map[string]string{"data-root": "", "run": ""}, args)
	if err != nil {
		return err
	}
	root := flags["data-root"]
	if root == "" {
		root = os.Getenv("SP_POC_DATA_ROOT")
	}
	if root == "" {
		return fmt.Errorf("soak status: no data root. Pass --data-root or set SP_POC_DATA_ROOT")
	}
	// The root is checked BEFORE SQLite opens the file. SQLite reports a missing
	// directory as "out of memory (14)", which sends an operator looking for a
	// memory problem that does not exist.
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return fmt.Errorf("soak status: %s is not a directory, so there is no run to report. Check the path before reading anything else: SQLite would say `out of memory (14)` here, which is exactly the wrong thing to send an operator to", root)
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		// Check the ROOT first. SQLite reports a missing directory as
		// "out of memory (14)", which sends an operator looking for a memory
		// problem that does not exist.
		return fmt.Errorf("soak status: %s is not a directory, so there is no run to report. SQLite would say `out of memory (14)` here, which is what this check exists to prevent", root)
	}
	st, err := store.OpenPath(filepath.Join(root, "poc.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	c := ctx()
	id := flags["run"]
	if id == "" {
		run, err := st.LatestRun(c)
		if err != nil {
			return err
		}
		if run == nil {
			fmt.Printf("no run in %s. Nothing has been recorded, which is not the same as a run that recorded nothing.\n", st.Path())
			return nil
		}
		id = run.ID
	}
	run, err := st.RunByID(c, id)
	if err != nil {
		return err
	}
	counts, _ := st.CountByStatus(c, id)
	faults, _ := st.Faults(c, id)
	beats, _ := st.HeartbeatCount(c, id)
	gaps, _ := st.HeartbeatGaps(c, id, 30*time.Second)
	stalled, _ := st.Stalled(c, id, 5*time.Minute)
	push, _ := st.Stats(c, id)
	peaks, _ := st.SamplePeaks(c, id)

	end := time.Now().UTC()
	if run.EndedAt != nil {
		end = *run.EndedAt
	}
	fmt.Printf("run      %s (%s)\n", run.ID, run.Label)
	fmt.Printf("elapsed  %s of a %s target%s\n", end.Sub(run.StartedAt).Round(time.Second),
		(time.Duration(run.TargetSeconds) * time.Second), provisionalIf(end.Sub(run.StartedAt) < time.Duration(run.TargetSeconds)*time.Second))
	fmt.Printf("mode     %s\n", run.Mode)
	fmt.Printf("clock    %s\n", run.ClockSource)
	fmt.Printf("source   %s\n", run.SourceKind)
	fmt.Printf("push     %s\n", soak.BuildTruth(run.SourceKind, run.PushBaseURL, "xpsim", run.PushIsVMS, "").PushTarget)
	fmt.Println()
	fmt.Printf("  heartbeats            %d\n", beats)
	fmt.Printf("  heartbeat gaps > 30s  %d\n", len(gaps))
	fmt.Printf("  detections            %d\n", sumCounts(counts))
	for _, k := range sortedStatusKeys(counts) {
		fmt.Printf("    %-14s      %d\n", k, counts[k])
	}
	fmt.Printf("  not terminal > 5m     %d   (each one is a lost event and AC2 fails)\n", len(stalled))
	fmt.Printf("  push attempts         %d (%d with a result, %d detections with no outbox row)\n",
		push.Total, push.WithResult, push.Unattributed)
	fmt.Printf("  dead-lettered         %d\n", push.DeadLetter)
	fmt.Printf("  faults                %d\n", len(faults))
	for _, f := range faults {
		verdict := ""
		if f.Expected != "" {
			verdict = " [expected " + f.Expected + ", observed " + f.Observed + "]"
		}
		fmt.Printf("    %s  %-28s %s%s\n", f.At.Format("15:04:05"), f.Reason, oneLine(f.Detail), verdict)
	}
	fmt.Printf("  rss peak              %s\n", soak.Bytes(peaks.RSSBytes))
	fmt.Printf("  db size               %s\n", soak.Bytes(peaks.DB))
	fmt.Printf("  disk peak             %s\n", soak.Bytes(peaks.DiskBytes))
	return nil
}

func provisionalIf(v bool) string {
	if v {
		return "  — PROVISIONAL: the run has not reached its target, so nothing here concludes"
	}
	return ""
}

func sumCounts(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func sortedStatusKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// soakFaults prints the local rehearsal table: the six faults that attack the
// pipeline itself, as against the seven that attack the push target.
func soakFaults(args []string) error {
	fmt.Print(soak.LocalMarkdown())
	fmt.Println("The provider faults are a separate table: 'pocctl rehearse'.")
	fmt.Println()
	fmt.Println("Neither table is a substitute for the other. A pipeline rehearsed only against")
	fmt.Println("a 500 has been rehearsed in one direction.")
	return nil
}

// cmdProbeStability is T22's mechanism: the tool must be unable to promote a
// path on its own. The check is a test, and the test lives in the pocctl package
// so the table and the guarantee are in the same place.
func probeConfidence() string {
	// The only two words the table is allowed to use. Both are fixed here, in
	// one place, and neither is computed from anything at runtime.
	return "documented (spec) / untested-against-real-vms"
}

var _ = strings.TrimSpace
var _ = config.SourceLoop
