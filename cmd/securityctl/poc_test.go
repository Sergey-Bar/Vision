package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sergey-Bar/Ai-Captain/internal/command"
)

// pocFixture is the same synthetic stream the capture packages are tested
// against: a static scene, then motion. The poc's job is not to decide anything
// about it, so what the fixture has to contain is a change — and it does.
const pocFixture = "../../testdata/poc/testsrc.h264"

// runPocForCheck lives in acceptance.go, because the acceptance report makes
// claims about poc and has to measure the same code path these tests do. A
// second copy in a test file is a second implementation of the same claim.

func runPocCmd(t *testing.T, args ...string) (command.ExitCode, string) {
	t.Helper()
	return runPocForCheck(args...)
}

// decodeReport runs the poc with --json and parses its report.
func decodeReport(t *testing.T, args ...string) pocReport {
	t.Helper()
	code, out := runPocCmd(t, append(args, "--json")...)
	if code != command.ExitOK {
		t.Fatalf("poc %v returned %d, want ok\n%s", args, code, out)
	}
	var rep pocReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("parse the report: %v\n%s", err, out)
	}
	return rep
}

// TestPocTakesAStreamToAStoredHashedClipAndARaisedIncident is the Stage 1
// acceptance criterion, asserted through the command tree rather than through
// the packages, because a command that composes the packages wrongly and one
// that composes them wrongly in a test both produce a green suite otherwise.
func TestPocTakesAStreamToAStoredHashedClipAndARaisedIncident(t *testing.T) {
	out := t.TempDir()
	rep := decodeReport(t,
		"--stream", pocFixture,
		"--out", out,
		"--site", "poc-test",
		"--pre-roll", "4s",
		"--post-roll", "3s",
		"--max-time", "20s",
	)

	if !rep.Triggered {
		t.Fatal("the detector never fired on a stream that contains a change")
	}
	if rep.Clip == nil || rep.Clip.Bytes == 0 {
		t.Fatal("no clip was produced")
	}
	if rep.Clip.Width == 0 || rep.Clip.Height == 0 {
		t.Errorf("the clip has no resolution; a banner and a report that cannot name the geometry are not naming it")
	}
	if rep.Clip.KeyFrames < 1 {
		t.Errorf("the clip has %d keyframes, so it cannot be seeked or pre-rolled", rep.Clip.KeyFrames)
	}
	if rep.Clip.PreRollPics == 0 {
		t.Error("the clip has no lead-in: it starts where the detector fired rather than before it")
	}
	if rep.Clip.PreRollReason != "" {
		t.Errorf("the lead-in is degraded and the report does not say why: %s", rep.Clip.PreRollReason)
	}

	// The clip obeys the caps it was given, and says so when it does not.
	if rep.Clip.Duration > 25*oneFrameTolerance {
		t.Errorf("the clip runs %v, past the 20s cap plus post-roll", rep.Clip.Duration)
	}
	if rep.Clip.Bytes > maxBytesDefault {
		t.Errorf("the clip is %d bytes, past the %d cap", rep.Clip.Bytes, maxBytesDefault)
	}

	// The evidence is on disk, at the path the report names, and its bytes
	// hash to what the report claims.
	if rep.Evidence == nil || rep.Evidence.SHA256 == "" {
		t.Fatal("no evidence was recorded")
	}
	if !rep.Evidence.HashVerified {
		t.Error("the stored clip did not read back identical; the proof has not checked its own claim")
	}
	stored := filepath.Join(out, "evidence", filepath.FromSlash(rep.Evidence.StorageRef))
	raw, err := os.ReadFile(stored)
	if err != nil {
		t.Fatalf("the evidence the report names is not on disk: %v", err)
	}
	if got := sha256Hex(raw); got != rep.Evidence.SHA256 {
		t.Errorf("the stored bytes hash to %s, the report says %s", got, rep.Evidence.SHA256)
	}
	if int64(len(raw)) != rep.Evidence.SizeBytes {
		t.Errorf("the stored file is %d bytes, the report says %d", len(raw), rep.Evidence.SizeBytes)
	}

	if rep.Incident == nil {
		t.Fatal("no incident was raised")
	}
	if !rep.Incident.DecidedWithoutAIObservation {
		t.Error("the incident does not say it was decided without an AI observation")
	}
	if !rep.Incident.AwaitingDisposition {
		t.Error("the incident does not say it is waiting for an operator; a decided-looking incident is one nobody will review")
	}
	if rep.Incident.Severity != "query" {
		t.Errorf("severity %q; an unverified novelty score is a query, not a claim", rep.Incident.Severity)
	}
	if rep.Incident.ReasonCode == "" || rep.Incident.Reason == "" {
		t.Error("the incident has no named reason")
	}
	if rep.Incident.ObservationID == "" || strings.Contains(rep.Incident.ObservationID, "pending") {
		t.Errorf("the incident cites observation %q, which is not an identity", rep.Incident.ObservationID)
	}
}

// TestPocRecordsADegradedObservationAndKeepsTheThreeAbsencesApart is the
// invariant this whole Stage 1 exists to demonstrate.
func TestPocRecordsADegradedObservationAndKeepsTheThreeAbsencesApart(t *testing.T) {
	out := t.TempDir()
	rep := decodeReport(t, "--stream", pocFixture, "--out", out)

	if rep.Observation == nil {
		t.Fatal("no observation was recorded")
	}
	if !rep.Observation.Degraded {
		t.Error("the observation is not marked degraded")
	}
	if rep.Observation.DegradedReason != "no_runtime" {
		t.Errorf("degradation %q, want no_runtime: nothing ran, which is a different fact from something running and failing",
			rep.Observation.DegradedReason)
	}
	if !rep.Observation.InsufficientEvidence {
		t.Error("insufficient-evidence is false; a degraded observation must fail closed for a consumer that forgets to check")
	}
	// The placeholders have to be recognisable as placeholders. A hash of
	// zeroes and a model called "none" say "nothing ran" in a shape a reader's
	// tooling will not mistake for a real model identity - which a plausible
	// invented hash would.
	if rep.Observation.Model != "none" {
		t.Errorf("the observation claims model %q; nothing ran, so it must not name one", rep.Observation.Model)
	}
	if rep.Observation.ModelHash != zeroHash {
		t.Errorf("the observation carries model hash %q; the documented placeholder is the all-zero hash, and anything "+
			"else is either a real model or a fabricated one", rep.Observation.ModelHash)
	}
	if !rep.Observation.IDMintedLocally {
		t.Error("the observation id does not say it was minted locally rather than assigned by a database")
	}

	// The three absences are three named facts, not one nullable field.
	a := rep.Absences
	if !a.NoObservation || !a.DegradedObservation || !a.InsufficientEvidence {
		t.Errorf("the three absences collapsed into %+v; they are three different facts", a)
	}
	if a.NoObservationWhy == "" || a.DegradedWhy == "" || a.InsufficientWhy == "" {
		t.Errorf("an absence with no explanation: %+v", a)
	}
	if a.IsNegative {
		t.Error("a missing observation was treated as a negative answer")
	}
	// And the human-readable output says the same thing, in words a person reads,
	// because a judge reads the words and not the JSON.
	code, human := runPocCmd(t, "--stream", pocFixture, "--out", out, "--quiet")
	if code != command.ExitOK {
		t.Fatalf("the human rendering returned %d\n%s", code, human)
	}
	for _, want := range []string{
		"no observation        ", "degraded observation  ", "insufficient evidence ",
		"decided WITHOUT an AI observation: yes",
		"treated as a negative no",
		"never a negative answer",
	} {
		if !strings.Contains(human, want) {
			t.Errorf("the summary does not say %q; the three absences have to be checkable at a glance", want)
		}
	}
	// The line that matters most is the one nobody can misread.
	if !strings.Contains(human, "the question was never asked") {
		t.Error("the summary does not say that the question was never asked")
	}
}

// TestPocNeverPrintsAMissingObservationAsNothingHappened is the red-check for
// the failure mode the three-absences discipline exists to prevent: a clip was
// taken, the model was never asked, and the output says nothing happened.
//
// With that defect reintroduced, this is the only assertion in the tree that
// fails. Everything else would still pass - the clip would still be stored, the
// hash would still match, the incident would still be raised - because a
// confidently wrong claim is not a claim that fails loudly.
func TestPocNeverPrintsAMissingObservationAsNothingHappened(t *testing.T) {
	out := t.TempDir()
	code, human := runPocCmd(t, "--stream", pocFixture, "--out", out, "--quiet")
	if code != command.ExitOK {
		t.Fatalf("poc returned %d\n%s", code, human)
	}
	lower := strings.ToLower(human)
	for _, phrase := range []string{
		"nothing happened", "no activity", "all clear", "nothing detected",
		"no intrusion", "no events", "scene is clear", "no person",
	} {
		if strings.Contains(lower, phrase) {
			t.Errorf("the output says %q after a run in which no model was ever asked", phrase)
		}
	}
	if !strings.Contains(lower, "query") {
		t.Error("the summary never says what kind of thing the incident is")
	}
}

// TestPocSaysNoClipQuietlyWhenTheDetectorNeverFires keeps a working capture and
// a quiet scene on different lines. "The capture worked and nothing triggered"
// and "the capture is broken" must never share an outcome.
//
// The quiet scene is a short run over the fixture's static opening, not an
// unreachable threshold: the score is normalised to [0,1], so any threshold
// below 1 can be crossed by a saturated reading, and a test built on an
// "uncrossable" threshold would be asserting an arithmetic accident.
func TestPocSaysNoClipQuietlyWhenTheDetectorNeverFires(t *testing.T) {
	out := t.TempDir()
	quiet := []string{"--stream", pocFixture, "--out", out, "--pictures", "50", "--pre-roll", "2s", "--post-roll", "1s"}
	rep := decodeReport(t, quiet...)
	if rep.Outcome != "no_clip" {
		t.Errorf("outcome %q, want no_clip on two seconds of a static scene", rep.Outcome)
	}
	if rep.Triggered {
		t.Error("the detector fired on a scene with no change in it")
	}
	if rep.Incident != nil {
		t.Error("an incident was raised with nothing to point at")
	}
	if rep.Evidence != nil {
		t.Error("an empty clip was stored; an evidence object of zero bytes has the hash of nothing")
	}
	if rep.OutcomeWhy == "" {
		t.Error("an outcome with no explanation is not an outcome")
	}
	code, human := runPocCmd(t, append(quiet, "--quiet")...)
	if code != command.ExitOK {
		t.Fatalf("a quiet scene is not a failure, and it returned %d\n%s", code, human)
	}
	if !strings.Contains(human, "NO CLIP") {
		t.Errorf("the summary does not name the outcome:\n%s", human)
	}
	if !strings.Contains(human, "completed run, not a failure") {
		t.Errorf("the summary does not distinguish a quiet scene from a broken capture:\n%s", human)
	}
}

// TestPocRecordProducesAFixtureTheReplayPathAccepts is what makes the build stop
// depending on the camera: whatever the record path writes, the replay path must
// read, byte for byte.
func TestPocRecordProducesAFixtureTheReplayPathAccepts(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "capture.h264")
	out := filepath.Join(dir, "run")

	code, text := runPocCmd(t,
		"--stream", pocFixture, "--record", record, "--out", out, "--quiet",
		"--pre-roll", "3s", "--post-roll", "2s", "--max-time", "20s")
	if code != command.ExitOK {
		t.Fatalf("poc --record returned %d\n%s", code, text)
	}
	recorded, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("nothing was recorded: %v", err)
	}
	if len(recorded) == 0 {
		t.Fatal("the recorded fixture is empty")
	}
	original, err := os.ReadFile(pocFixture)
	if err != nil {
		t.Fatalf("read the source fixture: %v", err)
	}
	// The recorder writes its own Annex-B framing, so the bytes are not identical
	// to a stream that already had it. What has to hold is that the NAL units
	// are, in order, exactly the ones that went in.
	if got, want := countNALs(recorded), countNALs(original); got != want {
		t.Errorf("the recording has %d NAL units, the source had %d", got, want)
	}
	for i, n := range nalUnits(recorded) {
		if i >= len(nalUnits(original)) {
			break
		}
		if !bytes.Equal(n, nalUnits(original)[i]) {
			t.Fatalf("recorded NAL %d differs from the source", i)
		}
	}
	// And the replay path accepts it, which is the point of the fixture.
	replayOut := filepath.Join(dir, "replay")
	code, text = runPocCmd(t, "--stream", record, "--out", replayOut, "--quiet",
		"--pre-roll", "3s", "--post-roll", "2s", "--max-time", "20s")
	if code != command.ExitOK {
		t.Fatalf("replaying our own recording returned %d\n%s", code, text)
	}
}

func TestPocRefusesAnInvocationItCannotHonour(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no source", nil, "--camera or --stream"},
		{"both sources", []string{"--camera", "rtsp://x/y", "--stream", pocFixture}, "mutually exclusive"},
		{"positional", []string{"--stream", pocFixture, "extra"}, "not as a positional"},
		{"no tenant", []string{"--stream", pocFixture, "--tenant", ""}, "--tenant is required"},
		{"negative roll", []string{"--stream", pocFixture, "--pre-roll", "-1s"}, "cannot be negative"},
		{"impossible threshold", []string{"--stream", pocFixture, "--threshold", "2"}, "must be in (0,1]"},
		{"missing stream", []string{"--stream", filepath.Join(t.TempDir(), "nope.h264")}, "open --stream"},
	}
	for _, tc := range cases {
		code, out := runPocCmd(t, tc.args...)
		if code == command.ExitOK {
			t.Errorf("%s: the command accepted it", tc.name)
			continue
		}
		if !strings.Contains(out, tc.want) {
			t.Errorf("%s: got exit %d with %q, want a message containing %q", tc.name, code, out, tc.want)
		}
	}
}

func TestPocNeverPrintsCameraCredentials(t *testing.T) {
	// The URL is refused, but the refusal has to be safe to paste into a ticket.
	code, out := runPocCmd(t, "--camera", "rtsp://admin:hunter2@192.0.2.99:554/live", "--seconds", "1s") //nolint:gosec // a documentation-range address, not a credential
	if code == command.ExitOK {
		t.Fatal("an unreachable camera was accepted")
	}
	if strings.Contains(out, "hunter2") {
		t.Errorf("the diagnostic carries the camera password:\n%s", out)
	}
	if !strings.Contains(out, "192.0.2.99:554") {
		t.Errorf("the diagnostic lost the endpoint it is supposed to identify:\n%s", out)
	}
}

// TestTheClipTheCommandProducesIsReadableByAnIndependentDecoder closes the loop
// the muxer opens. A poc that writes a file only its own reader accepts has
// proven nothing about interoperability, which is the property the whole
// hackathon submission rests on.
func TestTheClipTheCommandProducesIsReadableByAnIndependentDecoder(t *testing.T) {
	bin, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe is not on PATH; the independent-decoder check did not run")
	}
	out := t.TempDir()
	rep := decodeReport(t, "--stream", pocFixture, "--out", out,
		"--pre-roll", "4s", "--post-roll", "3s", "--max-time", "20s")
	if rep.Evidence == nil {
		t.Fatal("no evidence")
	}
	path := filepath.Join(out, "evidence", filepath.FromSlash(rep.Evidence.StorageRef))
	cmd := exec.CommandContext(t.Context(), bin, "-v", "error", "-count_packets", //nolint:gosec // ffprobe is the independent decoder this test is about
		"-show_entries", "stream=codec_name,width,height,nb_read_packets",
		"-show_entries", "format=duration", "-of", "default=nw=1", path)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe refused the clip the command produced: %v\n%s", err, raw)
	}
	text := string(raw)
	for _, want := range []string{"codec_name=h264", "width=640", "height=360"} {
		if !strings.Contains(text, want) {
			t.Errorf("ffprobe did not report %s:\n%s", want, text)
		}
	}
	if rep.Clip == nil || !strings.Contains(text, "nb_read_packets="+itoa(rep.Clip.Pictures)) {
		t.Errorf("ffprobe read a different number of samples than the clip wrote:\n%s\nreport says %d pictures",
			text, rep.Clip.Pictures)
	}
}
