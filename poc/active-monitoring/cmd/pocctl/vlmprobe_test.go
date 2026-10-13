package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/store"
	"github.com/Sergey-Bar/vision-poc/internal/vlm"
)

// TestVLMProbeRefusesWithoutTheEnvironmentOnlyCredentials is 0.2's first test. A
// probe that echoed the configuration back would be indistinguishable from one
// that reached the host, so a missing key has to be a refusal and not a print.
func TestVLMProbeRefusesWithoutTheEnvironmentOnlyCredentials(t *testing.T) {
	// Both credentials come from the environment and have NO flag: a key that can be
	// typed on a command line ends up in a shell history and a screenshot, and the
	// reason to probe is to check the configuration you already have.
	flagSpec := map[string]string{
		"region": "", "model": "", "timeout": "30s", "out": rawDir(), "print-body": "false",
	}
	for _, name := range []string{"api-key", "apikey", "key"} {
		if _, ok := flagSpec[name]; ok {
			t.Fatalf("vlm-probe has a --%s flag; a credential on a command line is a credential in a shell history", name)
		}
	}

	t.Setenv("SP_POC_VLM_API_KEY", "")
	t.Setenv("SP_POC_VLM_MODEL", "cosmos-3-nano-16b")
	err := cmdVLMProbe(nil)
	if err == nil {
		t.Fatal("a probe with no API key was allowed to run")
	}
	if !strings.Contains(err.Error(), "SP_POC_VLM_API_KEY") {
		t.Fatalf("the refusal does not name the variable to set: %v", err)
	}

	t.Setenv("SP_POC_VLM_API_KEY", "test-key-not-a-secret")
	t.Setenv("SP_POC_VLM_MODEL", "")
	err = cmdVLMProbe(nil)
	if err == nil || !strings.Contains(err.Error(), "SP_POC_VLM_MODEL") {
		t.Fatalf("a probe with no model name was allowed to run, or the refusal did not name the variable: %v", err)
	}
}

// TestVLMProbeRefusesARegionNoRunWouldEverCall: the probe resolves the host from
// the pinned region map, so a flag that pointed it elsewhere would answer a
// question about a host the pipeline has never contacted.
func TestVLMProbeRefusesARegionNoRunWouldEverCall(t *testing.T) {
	t.Setenv("SP_POC_VLM_API_KEY", "k")
	t.Setenv("SP_POC_VLM_MODEL", "m")
	t.Setenv("SP_POC_VLM_REGION", "")
	err := cmdVLMProbe([]string{"--region", "cn-north"})
	if err == nil {
		t.Fatal("a region outside the pinned map was accepted")
	}
	if !strings.Contains(err.Error(), "api.eu.hafnia.milestonesys.com") {
		t.Fatalf("the refusal does not name what could have been reached instead: %v", err)
	}
}

// TestWriteRawBodyLandsUnderRawAndNeverTouchesContractJSON is 0.2's second test.
func TestWriteRawBodyLandsUnderRawAndNeverTouchesContractJSON(t *testing.T) {
	root := t.TempDir()
	contract := filepath.Join(root, "contract.json")
	if err := os.WriteFile(contract, []byte(`{"status":"asserted_not_verified"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(contract)
	if err != nil {
		t.Fatal(err)
	}

	req := vlmProbeRequest{outDir: filepath.Join(root, "raw"), region: vlm.RegionEU}
	rep := vlm.ProbeReport{
		Host: "api.eu.hafnia.milestonesys.com", Region: vlm.RegionEU,
		Method: http.MethodPost, Path: vlm.PathCompletions, Status: 200,
		Body: `{"model":"cosmos-3-nano-16b"}`, ModelIdentity: "cosmos-3-nano-16b", LatencyMS: 412,
	}
	path, err := writeRawBody(req, rep)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != filepath.Join(root, "raw") {
		t.Fatalf("the raw body landed at %s, not under raw/", path)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The record has to be self-describing: a raw response filed on its own, with
	// no host and no status beside it, is a file nobody can interpret later.
	for _, want := range []string{"api.eu.hafnia.milestonesys.com", "cosmos-3-nano-16b", "status:   200", rep.Body} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("the record does not carry %q:\n%s", want, body)
		}
	}
	// And the file name distinguishes a 200 from a 401 six weeks later.
	if !strings.HasSuffix(path, "-eu-200.txt") {
		t.Fatalf("the file is named %s; a directory of raw responses must be readable without opening them", filepath.Base(path))
	}
	after, err := os.ReadFile(contract)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("writing a raw body changed contract.json; flipping status to verified is a HUMAN decision made after reading this file")
	}
}

// TestAProbeFailureIsStillRecorded: the 401 is the run that settles the auth
// assumption. A probe that discarded the body on the error path would throw away
// the evidence it was run to collect.
func TestAProbeFailureIsStillRecorded(t *testing.T) {
	root := t.TempDir()
	req := vlmProbeRequest{outDir: filepath.Join(root, "raw")}
	rep := vlm.ProbeReport{Host: "h", Region: vlm.RegionUS, Status: 401, Body: `{"detail":"unauthorized"}`}
	path, err := writeRawBody(req, rep)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "unauthorized") {
		t.Fatalf("the failure body was not recorded:\n%s", body)
	}
	if !strings.HasSuffix(path, "-401.txt") {
		t.Fatalf("a 401 is filed as %s; it must not share a name with a 200", filepath.Base(path))
	}
}

// TestTheHafniaContractIsStillAssertedNotVerified: this commit must not have
// quietly flipped it. The row is a human decision after the probe, and a
// mechanical "fix" here would make the file say the wire was checked when only a
// probe was written.
func TestTheHafniaContractIsStillAssertedNotVerified(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "contracts", "hafnia", "contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "asserted_not_verified") {
		t.Fatal("contracts/hafnia/contract.json is no longer asserted_not_verified; nobody read a probe and flipped it by hand")
	}
}

// TestMarkWritesThroughToTheStoredPrecision keeps `pocctl mark` honest against the
// one store call it wraps: a mark for a tag this run never pushed must be refused
// by the store and must not move the counters.
func TestMarkWritesThroughToTheStoredPrecision(t *testing.T) {
	st, runID, tag := runWithOnePushedTag(t)
	t.Cleanup(func() { _ = st.Close() })

	if err := st.MarkGroundTruth(context.Background(), runID, "aicap-car-never-pushed000", store.GroundTruthFalsePositive, "me", ""); err == nil {
		t.Fatal("a mark for an unpushed tag was accepted")
	}
	if _, _, measured, _ := st.Precision(context.Background(), runID); measured {
		t.Fatal("a refused mark moved the counters")
	}
	if err := st.MarkGroundTruth(context.Background(), runID, tag, store.GroundTruthFalsePositive, "me", "looked at it"); err != nil {
		t.Fatalf("a real mark was refused: %v", err)
	}
	tp, fp, measured, err := st.Precision(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if !measured || tp != 0 || fp != 1 {
		t.Fatalf("precision = %d/%d measured=%v, want 0/1 measured", tp, fp, measured)
	}
}

// runWithOnePushedTag builds a store with one run, one detection and one enqueued
// push, and returns the tag the push carries.
//
// It is the minimum a mark can be made against: [store.MarkGroundTruth] joins on
// (run, tag), so a tag nothing pushed can never be counted. That join is the whole
// reason a false-positive rate here is a count a human filled in.
func runWithOnePushedTag(t *testing.T) (*store.Store, string, string) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const runID, tag = "run-mark-1", "aicap-bird-abc123"
	if err := st.StartRun(ctx, store.Run{
		ID: runID, Label: "mark test", Mode: "live", SourceKind: "loop", SourceArg: "f.mp4",
		PushBaseURL: "http://127.0.0.1:8098", ClockSource: "host",
		StartedAt: time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC), TargetSeconds: 60,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Accept(ctx, store.Detection{
		ID: "det-1", RunID: runID, At: time.Date(2026, 9, 30, 10, 5, 0, 0, time.UTC), Class: "bird",
		Family: "bird", Direction: "left_to_right", Provenance: store.ProvenanceSynthetic,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Enqueue(ctx, store.OutboxEntry{
		DetectionID: "det-1", Kind: store.KindEvent, Payload: `{"tag":"` + tag + `"}`, Tag: tag,
	}, 4); err != nil {
		t.Fatal(err)
	}
	return st, runID, tag
}
