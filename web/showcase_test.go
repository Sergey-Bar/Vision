package main

// The hub's tests. They assert the one property the page exists for: every
// number comes from a file, an absent file is said to be absent, the two
// actions run one fixed command each against a data root the hub CHOSE BY
// ASKING the evidence command about every candidate, and nothing a request
// carries can change an argument.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func showcaseWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// showcaseRoot creates a data root that looks like a POC store: a directory
// with a poc.db in it, in one of the two places the POC writes them.
func showcaseRoot(t *testing.T, repoRoot, rel string) string {
	t.Helper()
	dir := filepath.Join(repoRoot, filepath.FromSlash(rel))
	showcaseWrite(t, filepath.Join(dir, "poc.db"), "a store, standing in for one")
	return dir
}

func showcaseGet(t *testing.T, h http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/showcase/x", nil)
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func showcasePost(t *testing.T, h http.HandlerFunc, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/showcase/x", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func showcaseDecodeStatus(t *testing.T, rec *httptest.ResponseRecorder) showcaseStatus {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. Body: %s", rec.Code, rec.Body.String())
	}
	var st showcaseStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("the status is not JSON: %v", err)
	}
	return st
}

// showcaseStubCall is one recorded invocation of the POC binary.
type showcaseStubCall struct {
	Bin  string
	Args []string
}

// stubShowcaseRun replaces the POC binary for one test. Every call is recorded
// and the responder answers it, so a test can hand the root scan one verdict
// per root and the command under test another.
func stubShowcaseRun(t *testing.T, respond func(call showcaseStubCall) (int, string)) *[]showcaseStubCall {
	t.Helper()
	var calls []showcaseStubCall
	prev := showcaseRun
	showcaseRun = func(_ context.Context, bin string, args ...string) (int, string) {
		call := showcaseStubCall{Bin: bin, Args: append([]string{}, args...)}
		calls = append(calls, call)
		return respond(call)
	}
	t.Cleanup(func() { showcaseRun = prev })
	return &calls
}

// findCall returns the first recorded call whose arguments start with prefix.
func findCall(calls []showcaseStubCall, prefix ...string) *showcaseStubCall {
	for i := range calls {
		if len(calls[i].Args) < len(prefix) {
			continue
		}
		match := true
		for j, p := range prefix {
			if calls[i].Args[j] != p {
				match = false
				break
			}
		}
		if match {
			return &calls[i]
		}
	}
	return nil
}

func ledgerPassOutput(runID string) string {
	return "# the newest run in this data root is " + runID + "; name another with --run\n\n" +
		"# ledger for run " + runID + "\n\n" +
		"  PASS — 85 row(s) from sequence 0 to 85 recomputed and chained\n"
}

func ledgerEmptyOutput(runID string) string {
	return "# the newest run in this data root is " + runID + "; name another with --run\n\n" +
		"# ledger for run " + runID + "\n\n" +
		"    the ledger is empty. An empty chain is not an intact one; it is the absence of evidence.\n\n" +
		"    FAIL — the ledger is empty; there is nothing to verify, and an empty chain is not an intact one\n"
}

func TestShowcaseStatusReadsTheNewestRecord(t *testing.T) {
	root := t.TempDir()
	showcaseWrite(t, filepath.Join(root, "dist", "verification", "verify-20260101-000000.json"),
		`{"outcome":"failed","exitReason":"one gate failed","gates":[{"name":"vet","outcome":"failed","seconds":1}]}`)
	showcaseWrite(t, filepath.Join(root, "dist", "verification", "verify-20260102-000000.json"), `{
		"schema": "security-platform/verification-record/v1",
		"outcome": "passed",
		"exitReason": "all gates passed",
		"finishedAtUtc": "2026-01-02T00:00:00Z",
		"provenance": {"kind":"local-run","automated":false,"ci":"none","commit":"0123456789abcdef0123456789abcdef01234567","worktreeDirty":true},
		"mode": {"raceDetector":true,"cgoEnabled":true},
		"database": {"urlSet":true,"passwordSet":true},
		"excluded": ["poc/ - a second Go module"],
		"gates": [{"name":"vet","outcome":"passed","seconds":1},{"name":"red-check","outcome":"passed","seconds":194}],
		"skipped": [],
		"failedGates": []
	}`)
	showcaseWrite(t, filepath.Join(root, "poc", "active-monitoring", "redcheck-transcripts.txt"),
		"# Red-check transcript\n\nProduced by scripts\\redcheck-transcript.ps1 on 2026-01-02 12:00.\nGo: go version go1.27.0 windows/amd64\n\nControls in the table: 93\n\n## Result\n\n    proved:  91\n    skipped: 2\n    total:   93\n")
	showcaseWrite(t, filepath.Join(root, "scripts", "verify.ps1"),
		"Invoke-Gate 'red-check' {\n    $controls = @(\n        @{\n            name = 'one'\n            test = 'TestOne'\n        }\n        @{\n            name = 'two'\n        }\n    )\n    if ($truthy) {\n        $script:skips.Add([ordered]@{ name = 'red-check'; reason = 'a skip record, not a control' })\n    }\n}\nInvoke-Gate 'migration-phases' {\n    name = 'not a control'\n}\n")
	showcaseWrite(t, filepath.Join(root, "dist", "live-demo", "report.html"), "<html></html>")

	st := showcaseDecodeStatus(t, showcaseGet(t, handleShowcaseStatus(root)))

	if st.Gate == nil {
		t.Fatal("no gate was read, so this test asserted nothing")
	}
	if st.Gate.Outcome != "passed" || st.Gate.Gates != 2 || st.Gate.Failed != 0 {
		t.Errorf("gate = %q gates=%d failed=%d, want the newest record: passed, 2, 0", st.Gate.Outcome, st.Gate.Gates, st.Gate.Failed)
	}
	if !strings.HasSuffix(st.Gate.Record, "verify-20260102-000000.json") {
		t.Errorf("record = %q, want the newest file named", st.Gate.Record)
	}
	if st.Gate.CommitShort != "01234567" {
		t.Errorf("commitShort = %q, want the first eight characters of the commit", st.Gate.CommitShort)
	}
	if !st.Gate.Dirty || !st.Gate.DBURLSet || !st.Gate.Race {
		t.Errorf("dirty=%v dbUrlSet=%v race=%v: the record's three honesty flags were not read", st.Gate.Dirty, st.Gate.DBURLSet, st.Gate.Race)
	}
	if len(st.Gate.GateList) != 2 || st.Gate.GateList[1].Name != "red-check" {
		t.Errorf("gateList = %+v, want the record's own gate rows", st.Gate.GateList)
	}
	if st.RedCheck.POCProved != 91 || st.RedCheck.POCSkipped != 2 || st.RedCheck.POCTotal != 93 {
		t.Errorf("poc red-check = %d/%d skipped %d, want 91/93 skipped 2", st.RedCheck.POCProved, st.RedCheck.POCTotal, st.RedCheck.POCSkipped)
	}
	if st.RedCheck.RootCount != 2 {
		t.Errorf("root controls = %d, want 2: the count stops at the next gate, does not count its entries, and does not count a skip record that carries the same text mid-line", st.RedCheck.RootCount)
	}
	if len(st.Artifacts) != 1 || st.Artifacts[0].Path != "dist/live-demo/report.html" {
		t.Errorf("artifacts = %+v, want exactly the file that exists", st.Artifacts)
	}
	if st.PocctlBuilt {
		t.Error("pocctlBuilt = true with no binary on disk; the page would offer an action that cannot run")
	}
	if len(st.Roots) != 0 {
		t.Errorf("roots = %+v with no data root on disk", st.Roots)
	}
	found := false
	for _, n := range st.Notes {
		if strings.Contains(n, "pocctl") {
			found = true
		}
	}
	if !found {
		t.Errorf("notes = %v, want a note saying the binary is not built", st.Notes)
	}
}

// TestTheRootControlCountLeavesOutRecordsThatAreNotControls: the gate writes a
// skip record carrying `name = 'red-check'` mid-line whenever it skips, and a
// counter that matched the text anywhere on a line read six of those as controls
// - thirty-four root controls in a table of twenty-eight, a number no table in
// this repository contains. The count must be the table's own.
func TestTheRootControlCountLeavesOutRecordsThatAreNotControls(t *testing.T) {
	script := "Invoke-Gate 'red-check' {\n" +
		"    $controls = @(\n" +
		"        @{\n" +
		"            name = 'one'\n" +
		"        }\n" +
		"        @{\n" +
		"            name = 'two'\n" +
		"        }\n" +
		"    )\n" +
		"    $script:skips.Add([ordered]@{ name = 'red-check'; reason = 'skipped on this box' })\n" +
		"}\n" +
		"Invoke-Gate 'migration-phases' {\n" +
		"    name = 'not a control'\n" +
		"}\n"
	if got := countRootRedCheckControls(script); got != 2 {
		t.Fatalf("counted %d root controls in a table of 2 with a skip record beside it; a skip record is not a control", got)
	}
}

func TestShowcaseStatusSaysNothingRanRatherThanPassed(t *testing.T) {
	st := showcaseDecodeStatus(t, showcaseGet(t, handleShowcaseStatus(t.TempDir())))
	if st.Gate != nil {
		t.Fatalf("gate = %+v with no record on disk; an absent run is not a green one", st.Gate)
	}
	if st.RedCheck.POCTotal != 0 || st.RedCheck.RootCount != 0 {
		t.Errorf("red-check = %+v with no transcript and no script", st.RedCheck)
	}
}

// TestShowcaseStatusAsksEveryRootAndPrefersTheSoundOne is the whole selection
// policy in one test: the newest root is scanned, its ledger is found empty, an
// older root verifies, and the older one becomes the root the commands act on -
// with a note, because a silent substitution would be the page lying by
// omission.
func TestShowcaseStatusAsksEveryRootAndPrefersTheSoundOne(t *testing.T) {
	root := t.TempDir()
	showcaseWrite(t, pocctlPath(root), "the stub stands in for this binary")
	older := showcaseRoot(t, root, "poc/active-monitoring/.live-demo/20260101-000000")
	newer := showcaseRoot(t, root, "poc/active-monitoring/.live-demo/20260102-000000")
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(older, old, old); err != nil {
		t.Fatal(err)
	}

	stubShowcaseRun(t, func(call showcaseStubCall) (int, string) {
		last := call.Args[len(call.Args)-1]
		switch last {
		case newer:
			return 1, ledgerEmptyOutput("run-neww")
		case older:
			return 0, ledgerPassOutput("run-oldd")
		}
		t.Errorf("unexpected call: %v", call.Args)
		return -1, ""
	})

	st := showcaseDecodeStatus(t, showcaseGet(t, handleShowcaseStatus(root)))

	if len(st.Roots) != 2 {
		t.Fatalf("roots = %+v, want both data roots listed", st.Roots)
	}
	byPath := map[string]showcaseRootCheck{}
	for _, r := range st.Roots {
		byPath[r.Path] = r
	}
	if got := byPath["poc/active-monitoring/.live-demo/20260102-000000"]; got.State != "empty" {
		t.Errorf("newest root state = %q, want empty", got.State)
	}
	if got := byPath["poc/active-monitoring/.live-demo/20260101-000000"]; got.State != "intact" || got.RunID != "run-oldd" {
		t.Errorf("older root = %+v, want intact with its run id", got)
	}
	if !strings.HasSuffix(st.DataRoot, "20260101-000000") {
		t.Errorf("dataRoot = %q, want the root whose ledger verifies", st.DataRoot)
	}
	noteFound := false
	for _, n := range st.Notes {
		if strings.Contains(n, "newest root overall") && strings.Contains(n, "never silent") {
			noteFound = true
		}
	}
	if !noteFound {
		t.Errorf("notes = %v, want the note that says which root was skipped and why", st.Notes)
	}
}

func TestShowcaseRootsAreUnreadWhenTheBinaryIsNotBuilt(t *testing.T) {
	root := t.TempDir()
	showcaseRoot(t, root, "poc/active-monitoring/.demo-data")
	calls := stubShowcaseRun(t, func(call showcaseStubCall) (int, string) {
		t.Errorf("the scan ran the binary even though it does not exist: %v", call.Args)
		return 0, ""
	})

	st := showcaseDecodeStatus(t, showcaseGet(t, handleShowcaseStatus(root)))

	if len(st.Roots) != 1 || st.Roots[0].State != "no-binary" {
		t.Fatalf("roots = %+v, want one root reported unread rather than guessed about", st.Roots)
	}
	if len(*calls) != 0 {
		t.Errorf("calls = %v, want none", *calls)
	}
}

func TestShowcaseStatusIsGetOnly(t *testing.T) {
	rec := showcasePost(t, handleShowcaseStatus(t.TempDir()), "")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func showcaseDecodeRun(t *testing.T, rec *httptest.ResponseRecorder) showcaseRunResult {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even when nothing ran. Body: %s", rec.Code, rec.Body.String())
	}
	var res showcaseRunResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("the run result is not JSON: %v", err)
	}
	return res
}

func TestShowcaseLedgerTamperRunsTheRootWhoseChainVerifies(t *testing.T) {
	root := t.TempDir()
	showcaseWrite(t, pocctlPath(root), "the stub stands in for this binary")
	older := showcaseRoot(t, root, "poc/active-monitoring/.live-demo/20260101-000000")
	newer := showcaseRoot(t, root, "poc/active-monitoring/.live-demo/20260102-000000")
	if err := os.Chtimes(older, time.Now().Add(-2*time.Hour), time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	calls := stubShowcaseRun(t, func(call showcaseStubCall) (int, string) {
		if call.Args[0] == "audit" && len(call.Args) > 2 && call.Args[2] == "--tamper" {
			return 1, "the chain broke at seq 4, as it must be; the row and the triggers were restored"
		}
		if call.Args[len(call.Args)-1] == newer {
			return 1, ledgerEmptyOutput("run-neww")
		}
		return 0, ledgerPassOutput("run-oldd")
	})

	res := showcaseDecodeRun(t, showcasePost(t, handleShowcaseLedger(root), `{"mode":"tamper"}`))

	if !res.Ran {
		t.Fatalf("ran = false with binary and an intact root present: %s", res.Reason)
	}
	tamper := findCall(*calls, "audit", "ledger", "--tamper")
	if tamper == nil {
		t.Fatalf("no tamper call was made; calls: %v", *calls)
	}
	if got := tamper.Args[len(tamper.Args)-1]; got != older {
		t.Errorf("tamper ran against %q, want the root whose ledger verifies (%q)", got, older)
	}
	if res.Exit != 1 || !strings.Contains(res.Output, "chain broke at seq 4") {
		t.Errorf("exit=%d output=%q, want the command's own exit code and output", res.Exit, res.Output)
	}
	if !strings.Contains(res.Command, "--data-root poc/active-monitoring/.live-demo/20260101-000000") {
		t.Errorf("command = %q, want the relative data root a reader can find in the tree", res.Command)
	}
}

func TestShowcaseLedgerRefusesAModeItDoesNotHave(t *testing.T) {
	root := t.TempDir()
	showcaseWrite(t, pocctlPath(root), "x")
	showcaseRoot(t, root, "poc/active-monitoring/.demo-data")
	calls := stubShowcaseRun(t, func(call showcaseStubCall) (int, string) {
		return 0, ledgerPassOutput("run-x")
	})
	rec := showcasePost(t, handleShowcaseLedger(root), `{"mode":"delete-row"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: the request chose a command, and only two exist", rec.Code)
	}
	if len(*calls) != 0 {
		t.Fatalf("argv = %v, want nothing run", *calls)
	}
}

func TestShowcaseLedgerWithoutADataRootSaysNotRun(t *testing.T) {
	root := t.TempDir()
	showcaseWrite(t, pocctlPath(root), "x")
	calls := stubShowcaseRun(t, func(call showcaseStubCall) (int, string) {
		t.Errorf("a command ran with no data root: %v", call.Args)
		return 0, ""
	})

	res := showcaseDecodeRun(t, showcasePost(t, handleShowcaseLedger(root), `{"mode":"verify"}`))

	if res.Ran {
		t.Fatal("ran = true with no demo data root; there is nothing to audit")
	}
	if !strings.Contains(res.Reason, ".demo-data") {
		t.Errorf("reason = %q, want it to name where a data root would be", res.Reason)
	}
	if len(*calls) != 0 {
		t.Errorf("argv = %v, want nothing run", *calls)
	}
}

func TestShowcaseLedgerWithoutTheBinarySaysNotRun(t *testing.T) {
	root := t.TempDir()
	showcaseRoot(t, root, "poc/active-monitoring/.live-demo/20260102-000000")
	res := showcaseDecodeRun(t, showcasePost(t, handleShowcaseLedger(root), `{"mode":"verify"}`))
	if res.Ran {
		t.Fatal("ran = true with no binary on disk")
	}
	if !strings.Contains(res.Reason, "pocctl") {
		t.Errorf("reason = %q, want it to name the missing binary", res.Reason)
	}
}

func TestShowcaseProveReplaysTheRunItsLedgerNames(t *testing.T) {
	root := t.TempDir()
	showcaseWrite(t, pocctlPath(root), "x")
	sound := showcaseRoot(t, root, "poc/active-monitoring/.demo-data")
	calls := stubShowcaseRun(t, func(call showcaseStubCall) (int, string) {
		if call.Args[0] == "prove" {
			return 0, "# the newest run in this data root is run-abcdef1234; name another with --run\n\nreplayed: 1 decision, 0 provider calls, the box and the score recomputed from the stored row\n"
		}
		return 0, ledgerPassOutput("run-abcdef1234")
	})

	if rec := showcaseGet(t, handleShowcaseProve(root)); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", rec.Code)
	}
	res := showcaseDecodeRun(t, showcasePost(t, handleShowcaseProve(root), ""))
	if !res.Ran {
		t.Fatalf("ran = false: %s", res.Reason)
	}
	prove := findCall(*calls, "prove")
	if prove == nil {
		t.Fatalf("no prove call was made; calls: %v", *calls)
	}
	want := []string{"prove", "--run", "run-abcdef1234", "--data-root", sound}
	if strings.Join(prove.Args, "|") != strings.Join(want, "|") {
		t.Errorf("argv = %v, want %v: the run replayed is the one the chain audit just named", prove.Args, want)
	}
	if res.Exit != 0 || !strings.Contains(res.Output, "0 provider calls") {
		t.Errorf("exit=%d output=%q, want the command's own replay line", res.Exit, res.Output)
	}
}

func TestShowcaseProveSaysNotRunWhenNoChainVerifies(t *testing.T) {
	root := t.TempDir()
	showcaseWrite(t, pocctlPath(root), "x")
	showcaseRoot(t, root, "poc/active-monitoring/.demo-data")
	calls := stubShowcaseRun(t, func(call showcaseStubCall) (int, string) {
		if call.Args[0] == "prove" {
			t.Errorf("prove ran against a root with no verifiable chain: %v", call.Args)
		}
		return 1, ledgerEmptyOutput("run-empty")
	})

	res := showcaseDecodeRun(t, showcasePost(t, handleShowcaseProve(root), ""))

	if res.Ran {
		t.Fatal("ran = true with no intact chain; there is no decision to replay and saying otherwise would be an invented proof")
	}
	if !strings.Contains(res.Reason, "empty") {
		t.Errorf("reason = %q, want the ledger's own state named", res.Reason)
	}
	if len(*calls) == 0 || findCall(*calls, "prove") != nil {
		t.Errorf("calls = %v, want the scan to have run and prove not to have", *calls)
	}
}

func TestShowcaseIncidentsReadsThePairsNewestFirst(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "dist", "demo")
	showcaseWrite(t, filepath.Join(dir, "inc_aaa.incident.json"),
		`{"incidentId":"inc_aaa","raisedAt":"2026-01-01T00:00:00Z","reasonCode":"ai_plane_absent","decidedWithoutAiObservation":true}`)
	showcaseWrite(t, filepath.Join(dir, "inc_bbb.incident.json"),
		`{"incidentId":"inc_bbb","raisedAt":"2026-01-02T00:00:00Z"}`)
	showcaseWrite(t, filepath.Join(dir, "inc_bbb.observation.json"),
		`{"observationId":"obs_bbb","degraded":true,"degradedReason":"no_runtime","insufficientEvidence":true}`)

	rec := showcaseGet(t, handleShowcaseIncidents(root))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Found bool                   `json:"found"`
		Pairs []showcaseIncidentPair `json:"pairs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if !body.Found || len(body.Pairs) != 2 {
		t.Fatalf("found=%v pairs=%d, want both documents", body.Found, len(body.Pairs))
	}
	if body.Pairs[0].Incident["incidentId"] != "inc_bbb" {
		t.Errorf("first pair = %v, want the newer incident first", body.Pairs[0].Incident["incidentId"])
	}
	if body.Pairs[0].Observation == nil || body.Pairs[0].Observation["degradedReason"] != "no_runtime" {
		t.Errorf("the observation document was not attached: %+v", body.Pairs[0])
	}
	if body.Pairs[1].Observation != nil || body.Pairs[1].Missing == "" {
		t.Errorf("incident with no observation = %+v, want a missing note rather than silence", body.Pairs[1])
	}
}

func TestShowcaseIncidentsWithoutTheDirectorySaysSo(t *testing.T) {
	rec := showcaseGet(t, handleShowcaseIncidents(t.TempDir()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Found  bool   `json:"found"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if body.Found || !strings.Contains(body.Reason, "demo.ps1") {
		t.Errorf("found=%v reason=%q, want a NOT RUN note naming the script that writes the directory", body.Found, body.Reason)
	}
}
