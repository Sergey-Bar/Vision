package main

// The showcase hub.
//
// This is a READING surface, and that is the whole design. Every number it
// prints is read from a file this repository already writes: the newest
// verification record in dist/verification, the red-check transcript beside the
// POC gate, the demo data roots, the demo's own incident documents. Nothing is
// typed in by hand, because a typed-in number is a claim, and the job of this
// page is to put the claims next to their evidence.
//
// Two of its sections ACT rather than read, and both run one fixed command of
// the POC binary against the newest demo data root:
//
//	pocctl audit ledger --tamper   edits one byte, shows the chain break by
//	                               name, and restores what it changed
//	pocctl prove                   replays the newest decision from stored
//	                               facts with zero provider calls
//
// Neither accepts a path, an argument or a command from the request. The only
// thing a request may choose is WHICH of the two commands runs, and when either
// the binary or the data root is absent the handler answers `ran: false` with
// the reason instead of an error wall: a NOT RUN printed as NOT RUN.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------- the shapes the hub's pages read --------------------------------

type showcaseGateRow struct {
	Name    string `json:"name"`
	Outcome string `json:"outcome"`
	Seconds int    `json:"seconds"`
}

type showcaseGate struct {
	Outcome       string            `json:"outcome"`
	ExitReason    string            `json:"exitReason"`
	FinishedAt    string            `json:"finishedAt"`
	Record        string            `json:"record"`
	Gates         int               `json:"gates"`
	Skipped       int               `json:"skipped"`
	Failed        int               `json:"failed"`
	Race          bool              `json:"race"`
	CGO           bool              `json:"cgo"`
	DBURLSet      bool              `json:"dbUrlSet"`
	DBPasswordSet bool              `json:"dbPasswordSet"`
	Commit        string            `json:"commit"`
	CommitShort   string            `json:"commitShort"`
	Dirty         bool              `json:"dirty"`
	LocalRun      bool              `json:"localRun"`
	CI            string            `json:"ci"`
	Excluded      []string          `json:"excluded"`
	GateList      []showcaseGateRow `json:"gateList"`
}

type showcaseRedCheck struct {
	POCProved  int    `json:"pocProved"`
	POCSkipped int    `json:"pocSkipped"`
	POCTotal   int    `json:"pocTotal"`
	POCAt      string `json:"pocAt"`
	POCFile    string `json:"pocFile"`
	RootCount  int    `json:"rootCount"`
	RootFile   string `json:"rootFile"`
}

type showcaseArtifact struct {
	Name string `json:"name"`
	Path string `json:"path"`
	At   string `json:"at"`
	Size int64  `json:"size"`
}

// showcaseRootCheck is one demo data root, with the state of its ledger as the
// POC binary itself reports it. The hub does not decide whether a chain is
// sound; it asks the command that exists to decide that, and prints the answer.
type showcaseRootCheck struct {
	Path   string `json:"path"`
	At     string `json:"at"`
	State  string `json:"state"` // intact | empty | broken | unread | no-binary
	RunID  string `json:"runId,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type showcaseStatus struct {
	GeneratedAt string              `json:"generatedAt"`
	Gate        *showcaseGate       `json:"gate"`
	RedCheck    showcaseRedCheck    `json:"redCheck"`
	Artifacts   []showcaseArtifact  `json:"artifacts"`
	Roots       []showcaseRootCheck `json:"roots"`
	DataRoot    string              `json:"dataRoot"`
	DataRootAt  string              `json:"dataRootAt"`
	PocctlBuilt bool                `json:"pocctlBuilt"`
	Notes       []string            `json:"notes"`
}

// showcaseRun is the one place a test can stand in for the POC binary.
var showcaseRun = runShowcasePocctl

func registerShowcaseRoutes(repoRoot string) {
	http.HandleFunc("/api/showcase/status", handleShowcaseStatus(repoRoot))
	http.HandleFunc("/api/showcase/ledger", handleShowcaseLedger(repoRoot))
	http.HandleFunc("/api/showcase/prove", handleShowcaseProve(repoRoot))
	http.HandleFunc("/api/showcase/incidents", handleShowcaseIncidents(repoRoot))
}

// ---------- reading: status ------------------------------------------------

// verificationRecord is the part of dist/verification/verify-*.json the hub
// reads. It is a struct rather than map[string]any so a shape change fails at
// the field that changed rather than printing "[object Object]".
type verificationRecord struct {
	Schema     string `json:"schema"`
	Outcome    string `json:"outcome"`
	ExitReason string `json:"exitReason"`
	FinishedAt string `json:"finishedAtUtc"`
	Verified   bool   `json:"verified"`
	Provenance struct {
		Kind          string `json:"kind"`
		Automated     bool   `json:"automated"`
		CI            string `json:"ci"`
		Commit        string `json:"commit"`
		WorktreeDirty bool   `json:"worktreeDirty"`
	} `json:"provenance"`
	Mode struct {
		RaceDetector bool `json:"raceDetector"`
		CGOEnabled   bool `json:"cgoEnabled"`
	} `json:"mode"`
	Database struct {
		URLSet      bool `json:"urlSet"`
		PasswordSet bool `json:"passwordSet"`
	} `json:"database"`
	Excluded []string `json:"excluded"`
	Gates    []struct {
		Name    string `json:"name"`
		Outcome string `json:"outcome"`
		Seconds int    `json:"seconds"`
	} `json:"gates"`
	Skipped     []any `json:"skipped"`
	FailedGates []any `json:"failedGates"`
}

func handleShowcaseStatus(repoRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("PANIC in handleShowcaseStatus: %v", rec)
				http.Error(w, fmt.Sprintf("Internal error: %v", rec), http.StatusInternalServerError)
			}
		}()
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		st := showcaseStatus{
			GeneratedAt: time.Now().UTC().Format(time.RFC3339),
			RedCheck:    readRedCheck(repoRoot),
			Artifacts:   readArtifacts(repoRoot),
		}
		st.Gate = readNewestVerificationRecord(repoRoot)
		st.PocctlBuilt = fileExists(pocctlPath(repoRoot))
		st.Roots = demoRoots(repoRoot)
		if chosen, reason := chooseRoot(st.Roots); chosen.Path != "" {
			st.DataRoot, st.DataRootAt = chosen.Path, chosen.At
			if reason != "" {
				st.Notes = append(st.Notes, reason)
			}
			if len(st.Roots) > 0 && chosen.Path != st.Roots[0].Path {
				st.Notes = append(st.Notes, fmt.Sprintf(
					"the newest root overall is %s and its ledger is %q; the /trust commands act on %s instead, the newest root whose ledger verifies — this line exists so that choice is never silent.",
					st.Roots[0].Path, st.Roots[0].State, chosen.Path))
			}
		}
		if !st.PocctlBuilt {
			st.Notes = append(st.Notes,
				"poc/active-monitoring/bin/pocctl.exe is not built on this machine, so the two actions on /trust will answer NOT RUN. scripts/live-demo.ps1 or the POC gate builds it.")
		}
		if st.Gate != nil && st.Gate.Dirty {
			st.Notes = append(st.Notes,
				"the newest gate record was taken over a dirty worktree: it describes the tree at run time, not the commit it names.")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(st)
	}
}

// readNewestVerificationRecord returns the newest record, or nil when the
// machine has not run the gate. nil is rendered as "no record" and never as
// "passed": absence is not success here either.
func readNewestVerificationRecord(repoRoot string) *showcaseGate {
	dir := filepath.Join(repoRoot, "dist", "verification")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var newest string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "verify-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		if name > newest {
			newest = name
		}
	}
	if newest == "" {
		return nil
	}
	path := filepath.Join(dir, newest)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var rec verificationRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil
	}
	g := &showcaseGate{
		Outcome:       rec.Outcome,
		ExitReason:    rec.ExitReason,
		FinishedAt:    rec.FinishedAt,
		Record:        filepath.ToSlash(filepath.Join("dist", "verification", newest)),
		Gates:         len(rec.Gates),
		Skipped:       len(rec.Skipped),
		Failed:        len(rec.FailedGates),
		Race:          rec.Mode.RaceDetector,
		CGO:           rec.Mode.CGOEnabled,
		DBURLSet:      rec.Database.URLSet,
		DBPasswordSet: rec.Database.PasswordSet,
		Commit:        rec.Provenance.Commit,
		Dirty:         rec.Provenance.WorktreeDirty,
		LocalRun:      rec.Provenance.Kind == "local-run",
		CI:            rec.Provenance.CI,
		Excluded:      rec.Excluded,
	}
	if len(g.Commit) > 8 {
		g.CommitShort = g.Commit[:8]
	} else {
		g.CommitShort = g.Commit
	}
	for _, row := range rec.Gates {
		g.GateList = append(g.GateList, showcaseGateRow{Name: row.Name, Outcome: row.Outcome, Seconds: row.Seconds})
	}
	return g
}

// readRedCheck reads both red-check records:
//
//   - the POC transcript, which is a machine-readable file and is parsed;
//   - the root table, which has no separate transcript (its controls quote
//     their transcripts in docs/status.md), so the count is derived from the
//     table in scripts/verify.ps1 the same way a reader would: the entries
//     between the red-check gate's opening line and the next gate's.
func readRedCheck(repoRoot string) showcaseRedCheck {
	out := showcaseRedCheck{
		RootFile: "scripts/verify.ps1",
	}
	pocPath := filepath.Join(repoRoot, "poc", "active-monitoring", "redcheck-transcripts.txt")
	out.POCFile = filepath.ToSlash(filepath.Join("poc", "active-monitoring", "redcheck-transcripts.txt"))
	if raw, err := os.ReadFile(pocPath); err == nil {
		text := string(raw)
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "Produced by"):
				out.POCAt = strings.TrimSpace(strings.TrimPrefix(line, "Produced by scripts\\redcheck-transcript.ps1 on"))
			case strings.HasPrefix(line, "proved:"):
				out.POCProved = atoiOr(line, 0)
			case strings.HasPrefix(line, "skipped:"):
				out.POCSkipped = atoiOr(line, 0)
			case strings.HasPrefix(line, "total:"):
				out.POCTotal = atoiOr(line, 0)
			}
		}
	}
	if raw, err := os.ReadFile(filepath.Join(repoRoot, "scripts", "verify.ps1")); err == nil {
		out.RootCount = countRootRedCheckControls(string(raw))
	}
	return out
}

func atoiOr(line string, fallback int) int {
	fields := strings.Fields(line)
	for _, f := range fields {
		if n, err := strconv.Atoi(f); err == nil {
			return n
		}
	}
	return fallback
}

// countRootRedCheckControls counts `name = '...'` lines inside the red-check
// gate block. The block ends at the next Invoke-Gate line, which is the same
// boundary a reader uses.
//
// The line must BEGIN with the assignment, after indentation. The gate also
// writes skip records - `$script:skips.Add([ordered]@{ name = 'red-check'; ... })` -
// and a count that matched `name = '` anywhere on the line reported those beside
// the controls: thirty-four where the table holds twenty-eight, a number no
// table in this repository contains. A skip record is not a control.
func countRootRedCheckControls(script string) int {
	lines := strings.Split(script, "\n")
	start := -1
	for i, line := range lines {
		if strings.Contains(line, "Invoke-Gate 'red-check'") {
			start = i
			break
		}
	}
	if start < 0 {
		return 0
	}
	n := 0
	for _, line := range lines[start+1:] {
		if strings.Contains(line, "Invoke-Gate '") {
			break
		}
		if strings.HasPrefix(strings.TrimSpace(line), "name = '") {
			n++
		}
	}
	return n
}

// readArtifacts lists the demo artefacts that exist, with their timestamps, so
// a page can say WHEN what it links was written.
func readArtifacts(repoRoot string) []showcaseArtifact {
	names := []struct{ label, rel string }{
		{"the live demo's report", "dist/live-demo/report.html"},
		{"the live demo's transcript", "dist/live-demo/transcript.txt"},
		{"the demo's report", "dist/demo/report.html"},
		{"the demo's dashboard", "dist/demo/dashboard.html"},
		{"the demo's transcript", "dist/demo/transcript.md"},
	}
	var out []showcaseArtifact
	for _, n := range names {
		path := filepath.Join(repoRoot, filepath.FromSlash(n.rel))
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		out = append(out, showcaseArtifact{
			Name: n.label,
			Path: n.rel,
			At:   info.ModTime().UTC().Format(time.RFC3339),
			Size: info.Size(),
		})
	}
	return out
}

// ---------- the demo data roots, each with the state of its ledger ---------
//
// A data root is a directory the POC writes a store into. Two exist in real
// use: `.demo-data`, which `pocctl demo` writes for a clip run against the
// simulator, and `.live-demo/<timestamp>`, one per live run. Both are scanned
// here, and each one is ASKED - by running the POC's own `audit ledger` against
// it - whether its chain verifies, because "does this evidence hold up" is a
// question with one implementation and this file is not it.
//
// The scan costs one process spawn per root, so its result is cached briefly.
// The cache is a performance decision and nothing else: a root that becomes
// sound between two reads shows up within the TTL, and a stale answer is never
// written anywhere.

var (
	showcaseRootsMu    sync.Mutex
	showcaseRootsCache = map[string]showcaseRootsEntry{}

	// showcaseRootsTTL is zero in tests, where caching would hide the thing
	// being tested.
	showcaseRootsTTL = 20 * time.Second
)

type showcaseRootsEntry struct {
	at    time.Time
	roots []showcaseRootCheck
}

// demoRoots returns every data root on this machine, newest first, each with
// the ledger state the POC binary reports for it.
func demoRoots(repoRoot string) []showcaseRootCheck {
	showcaseRootsMu.Lock()
	if entry, ok := showcaseRootsCache[repoRoot]; ok && showcaseRootsTTL > 0 && time.Since(entry.at) < showcaseRootsTTL {
		showcaseRootsMu.Unlock()
		return entry.roots
	}
	showcaseRootsMu.Unlock()

	roots := scanDemoRoots(repoRoot)

	showcaseRootsMu.Lock()
	showcaseRootsCache[repoRoot] = showcaseRootsEntry{at: time.Now(), roots: roots}
	showcaseRootsMu.Unlock()
	return roots
}

func scanDemoRoots(repoRoot string) []showcaseRootCheck {
	pocDir := filepath.Join(repoRoot, "poc", "active-monitoring")
	var dirs []string
	if fileExists(filepath.Join(pocDir, ".demo-data", "poc.db")) {
		dirs = append(dirs, filepath.Join(pocDir, ".demo-data"))
	}
	if entries, err := os.ReadDir(filepath.Join(pocDir, ".live-demo")); err == nil {
		for _, e := range entries {
			if e.IsDir() && fileExists(filepath.Join(pocDir, ".live-demo", e.Name(), "poc.db")) {
				dirs = append(dirs, filepath.Join(pocDir, ".live-demo", e.Name()))
			}
		}
	}
	sort.Slice(dirs, func(i, j int) bool {
		mi, _ := os.Stat(dirs[i])
		mj, _ := os.Stat(dirs[j])
		if mi == nil || mj == nil || mi.ModTime().Equal(mj.ModTime()) {
			return dirs[i] > dirs[j]
		}
		return mi.ModTime().After(mj.ModTime())
	})

	bin := pocctlPath(repoRoot)
	binBuilt := fileExists(bin)
	roots := make([]showcaseRootCheck, 0, len(dirs))
	for _, dir := range dirs {
		rel := relToRepo(repoRoot, dir)
		check := showcaseRootCheck{Path: rel, State: "unread"}
		if info, err := os.Stat(dir); err == nil {
			check.At = info.ModTime().UTC().Format(time.RFC3339)
		}
		if !binBuilt {
			check.State = "no-binary"
			check.Detail = "pocctl is not built on this machine, so the ledger state could not be read"
			roots = append(roots, check)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		exit, out := showcaseRun(ctx, bin, "audit", "ledger", "--data-root", dir)
		cancel()
		check.State, check.RunID, check.Detail = classifyLedger(exit, out)
		roots = append(roots, check)
	}
	return roots
}

// classifyLedger turns the evidence command's own exit code and words into a
// state. Nothing here decides whether a chain is sound; it reads the verdict
// the command wrote and keeps its sentence.
func classifyLedger(exit int, out string) (state, runID, detail string) {
	runID = ""
	if i := strings.Index(out, "the newest run in this data root is "); i >= 0 {
		rest := out[i+len("the newest run in this data root is "):]
		if j := strings.IndexAny(rest, "; \r\n"); j > 0 {
			runID = rest[:j]
		}
	}
	if exit == 0 {
		return "intact", runID, ""
	}
	state = "broken"
	if strings.Contains(out, "the ledger is empty") {
		state = "empty"
	}
	return state, runID, lastMeaningfulLine(out)
}

func lastMeaningfulLine(out string) string {
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			return line
		}
	}
	return ""
}

// chooseRoot picks the root the /trust commands act on: the newest one whose
// ledger verifies, or the newest one at all - with the reason said out loud -
// when nothing on this machine has an intact chain yet. The second case is not
// an error: an empty chain is the absence of evidence, and running the command
// against it and printing its refusal is more honest than refusing here.
func chooseRoot(roots []showcaseRootCheck) (showcaseRootCheck, string) {
	for _, r := range roots {
		if r.State == "intact" {
			return r, ""
		}
	}
	if len(roots) > 0 {
		return roots[0], fmt.Sprintf(
			"no demo data root on this machine holds an intact ledger yet (the newest is %s); the /trust ledger command still runs against that root and reports what it actually holds. `pocctl demo` writes a root with a ledger and detections in about three minutes.",
			roots[0].State)
	}
	return showcaseRootCheck{}, "no demo data root exists under poc/active-monitoring (.demo-data or .live-demo); `pocctl demo` writes one in about three minutes, and scripts/live-demo.ps1 writes one for the live path"
}

func relToRepo(repoRoot, path string) string {
	rel, err := filepath.Rel(repoRoot, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func pocctlPath(repoRoot string) string {
	name := "pocctl"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(repoRoot, "poc", "active-monitoring", "bin", name)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// ---------- acting: the two fixed commands --------------------------------

type showcaseRunResult struct {
	Ran      bool   `json:"ran"`
	Reason   string `json:"reason,omitempty"`
	Command  string `json:"command,omitempty"`
	DataRoot string `json:"dataRoot,omitempty"`
	Exit     int    `json:"exit"`
	Output   string `json:"output"`
}

func handleShowcaseLedger(repoRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("PANIC in handleShowcaseLedger: %v", rec)
				http.Error(w, fmt.Sprintf("Internal error: %v", rec), http.StatusInternalServerError)
			}
		}()
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Mode string `json:"mode"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var kind string
		switch req.Mode {
		case "", "verify":
			kind = "ledger-verify"
		case "tamper":
			kind = "ledger-tamper"
		default:
			http.Error(w, "mode is verify or tamper", http.StatusBadRequest)
			return
		}
		writeShowcaseRun(w, r, repoRoot, kind)
	}
}

func handleShowcaseProve(repoRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("PANIC in handleShowcaseProve: %v", rec)
				http.Error(w, fmt.Sprintf("Internal error: %v", rec), http.StatusInternalServerError)
			}
		}()
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		writeShowcaseRun(w, r, repoRoot, "prove")
	}
}

// writeShowcaseRun resolves the binary, chooses the data root, runs the one
// fixed command the kind names, and answers with the command line it ran.
//
// The chosen root is the newest one whose ledger verifies, because "replay this
// decision" and "break this chain" are claims about evidence, and running them
// against a root that is already known empty would demonstrate nothing while
// looking like a failure. `prove` additionally needs a run to replay: the POC's
// own `audit ledger` prints the newest run id in the root it verified, and that
// id is what `prove --run` is given - so the run replayed is the run the chain
// audit just named, not a guess.
//
// When anything needed is absent the answer is ran:false with the reason, at
// HTTP 200: the page must be able to render NOT RUN, and an error status would
// make an honest absence look like a broken server.
func writeShowcaseRun(w http.ResponseWriter, r *http.Request, repoRoot, kind string) {
	res := showcaseRunResult{}
	bin := pocctlPath(repoRoot)
	if !fileExists(bin) {
		res.Reason = fmt.Sprintf("pocctl is not built on this machine (expected %s); scripts/live-demo.ps1 builds it",
			filepath.ToSlash(filepath.Join("poc", "active-monitoring", "bin", filepath.Base(bin))))
		writeShowcaseJSON(w, res)
		return
	}
	chosen, _ := chooseRoot(demoRoots(repoRoot))
	if chosen.Path == "" {
		res.Reason = "no demo data root exists under poc/active-monitoring (.demo-data or .live-demo); `pocctl demo` writes one in about three minutes, and scripts/live-demo.ps1 writes one for the live path"
		writeShowcaseJSON(w, res)
		return
	}

	var args []string
	switch kind {
	case "ledger-verify":
		args = []string{"audit", "ledger"}
	case "ledger-tamper":
		args = []string{"audit", "ledger", "--tamper"}
	case "prove":
		if chosen.State != "intact" || chosen.RunID == "" {
			res.Reason = fmt.Sprintf(
				"the data root the commands act on (%s) has a %s ledger and no run to replay. `pocctl demo` writes a root whose chain verifies and whose run holds detections; the newest roots on this machine and their states are listed above the buttons",
				chosen.Path, chosen.State)
			writeShowcaseJSON(w, res)
			return
		}
		args = []string{"prove", "--run", chosen.RunID}
	default:
		log.Printf("writeShowcaseRun: unknown kind %q", kind)
		res.Reason = "the hub asked for a command it does not have; this is a bug in the hub, not in the repository"
		writeShowcaseJSON(w, res)
		return
	}

	// The command runs with the POC module as its working directory, so the
	// data root it is handed is absolute while the one printed is the relative
	// path a reader can find in the tree.
	absRoot := filepath.Join(repoRoot, filepath.FromSlash(chosen.Path))
	full := append(append([]string{}, args...), "--data-root", absRoot)
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	exit, out := showcaseRun(ctx, bin, full...)
	res.Ran = true
	res.Command = "pocctl " + strings.Join(append(append([]string{}, args...), "--data-root", chosen.Path), " ")
	res.DataRoot = chosen.Path
	res.Exit = exit
	res.Output = out
	writeShowcaseJSON(w, res)
}

func writeShowcaseJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// runShowcasePocctl runs the binary and returns its exit code and its combined
// output. A process that cannot be started at all is -1 and the error's text,
// which is a different thing from a command that ran and failed, and the page
// shows it as such.
func runShowcasePocctl(ctx context.Context, bin string, args ...string) (int, string) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = filepath.Dir(bin)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), string(out)
	}
	return -1, fmt.Sprintf("could not start %s: %v", filepath.Base(bin), err)
}

// ---------- reading: the demo's own incident documents ---------------------

type showcaseIncidentPair struct {
	Incident    map[string]any `json:"incident"`
	Observation map[string]any `json:"observation,omitempty"`
	Missing     string         `json:"observationMissing,omitempty"`
}

// handleShowcaseIncidents reads the incident documents the conference demo
// wrote, with their observations, newest first. The page renders the journey a
// decision actually took rather than a diagram of one.
func handleShowcaseIncidents(repoRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("PANIC in handleShowcaseIncidents: %v", rec)
				http.Error(w, fmt.Sprintf("Internal error: %v", rec), http.StatusInternalServerError)
			}
		}()
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		dir := filepath.Join(repoRoot, "dist", "demo")
		entries, err := os.ReadDir(dir)
		if err != nil {
			writeShowcaseJSON(w, map[string]any{
				"found":  false,
				"reason": "dist/demo has not been written on this machine; scripts/demo.ps1 writes it",
				"pairs":  []any{},
			})
			return
		}
		var pairs []showcaseIncidentPair
		for _, e := range entries {
			name := e.Name()
			if !strings.HasPrefix(name, "inc_") || !strings.HasSuffix(name, ".incident.json") {
				continue
			}
			inc, err := readJSONObject(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			pair := showcaseIncidentPair{Incident: inc}
			obsName := strings.TrimSuffix(name, ".incident.json") + ".observation.json"
			if obs, err := readJSONObject(filepath.Join(dir, obsName)); err == nil {
				pair.Observation = obs
			} else if !errors.Is(err, fs.ErrNotExist) {
				pair.Missing = "the observation document exists but could not be read: " + err.Error()
			} else {
				pair.Missing = "no observation document was written for this incident"
			}
			pairs = append(pairs, pair)
		}
		sort.Slice(pairs, func(i, j int) bool {
			ti, _ := pairs[i].Incident["raisedAt"].(string)
			tj, _ := pairs[j].Incident["raisedAt"].(string)
			return ti > tj
		})
		if len(pairs) > 20 {
			pairs = pairs[:20]
		}
		writeShowcaseJSON(w, map[string]any{
			"found": len(pairs) > 0,
			"reason": func() string {
				if len(pairs) == 0 {
					return "dist/demo exists but holds no incident documents; scripts/demo.ps1 writes them"
				}
				return ""
			}(),
			"pairs": pairs,
		})
	}
}

func readJSONObject(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	return obj, nil
}
