package main

// The committed machine-derivable review, and the four ways it must be refused.
//
// This is the file that lets criterion 07 print a number without anybody having
// watched footage, so the tests are about the boundary rather than about the
// arithmetic:
//
//   - the committed file loads and holds only clips whose answer is in the frame
//   - a clip that needs a human is NOT in it, and there is a test that fails if
//     somebody adds one
//   - a verdict outside the closed set is refused BY NAME, at the file
//   - a mark with no `why` is refused, because the whole claim of this file is that
//     the reason is derivable and an unexplained answer is just an assertion

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// syntheticReviewPath is the committed file, addressed from the PACKAGE rather than
// through `repoRoot()`.
//
// `repoRoot()` walks up from the working directory, which is the module root when
// an operator runs `pocctl` and is `cmd/pocctl` when `go test` runs it. The first
// version of these tests used the helper and every one of them failed on a path that
// does not exist — a failure that names a directory rather than the mistake.
func syntheticReviewPath() string {
	return filepath.Join("..", "..", "testdata", "groundtruth.synthetic.json")
}

// humanReviewPath is the human review, for the same reason.
func humanReviewPath() string { return filepath.Join("..", "..", "testdata", "groundtruth.json") }
func TestTheCommittedSyntheticReviewLoads(t *testing.T) {
	rv, err := loadSyntheticReview(syntheticReviewPath())
	if err != nil {
		t.Fatalf("the committed review does not load: %v", err)
	}
	if len(rv.Marks) == 0 {
		t.Fatal("the committed review holds no marks. An empty list is a valid file and it " +
			"means NOT RUN, which is a different thing from a review that works")
	}
	if len(rv.Comment) == 0 {
		t.Error("the file carries no $comment. The rule it exists to state -- that " +
			"groundtruth.json needs a person and this file does not -- is only enforceable " +
			"if it is written down where somebody adding a mark will read it")
	}
	if strings.TrimSpace(rv.Source) == "" {
		t.Error("the file declares no `source`, so a precision printed from it would not " +
			"be able to name its provenance")
	}
}

// The boundary, as a test.
//
// `car-crossing.mp4` is a real car on a real road. Whether a given frame is a
// "crossing" is a judgement, and nobody has watched it, so it has no mark in either
// file. If somebody adds one here, the file stops being machine-definable and
// starts being an unaudited assertion -- and the `$comment` alone is not a control.
func TestTheSyntheticReviewHoldsNoClipThatNeedsAHuman(t *testing.T) {
	rv, err := loadSyntheticReview(syntheticReviewPath())
	if err != nil {
		t.Fatalf("the committed review does not load: %v", err)
	}
	for _, m := range rv.Marks {
		if m.Clip == "car-crossing.mp4" {
			t.Fatal("car-crossing.mp4 has a mark in the machine-definable review. A real " +
				"vehicle on a real road is a judgement about what counts as a crossing, and " +
				"nobody has watched the footage. If a person does watch it, the mark belongs " +
				"in groundtruth.json with their name on it, not here")
		}
	}
	// And the inverse: the two clips that DO qualify must actually be in it, or the
	// file exists and measures nothing.
	for _, want := range []string{"bird-synthetic.mp4", "injection-hostile.mp4"} {
		found := false
		for _, m := range rv.Marks {
			if m.Clip == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s is not in the machine-definable review. Its answer is in the "+
				"frame -- a drawn bird on a flat background, and the injection string over "+
				"a frame with no vehicle -- so leaving it out makes the file measure less "+
				"than it can", want)
		}
	}
}

func TestAVerdictOutsideTheClosedSetIsRefusedByName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad-verdict.json")
	body := `{"schema":1,"source":"test","marks":[
	  {"clip":"bird-synthetic.mp4","verdict":"probably_a_vehicle","why":"because"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := loadSyntheticReview(path)
	if err == nil {
		t.Fatal("a third verdict loaded. It would be counted by a report nobody could read it with")
	}
	if !strings.Contains(err.Error(), "probably_a_vehicle") {
		t.Errorf("the refusal does not name the verdict it refused: %v", err)
	}
	if !strings.Contains(err.Error(), "false_positive") {
		t.Errorf("the refusal does not say what the two legal values are: %v", err)
	}
}

func TestAMarkWithNoReasonIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "no-why.json")
	body := `{"schema":1,"source":"test","marks":[
	  {"clip":"bird-synthetic.mp4","verdict":"false_positive"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := loadSyntheticReview(path)
	if err == nil {
		t.Fatal("a mark with no reason loaded. This file's entire claim to being different " +
			"from the human review is that each answer carries its derivation, and an " +
			"unexplained answer is just an assertion with a JSON key on it")
	}
	if !strings.Contains(err.Error(), "why") {
		t.Errorf("the refusal does not name what is missing: %v", err)
	}
}

// A misspelled key decodes to an empty mark list without `DisallowUnknownFields`,
// and the command then reports "no clip resolved" -- a sentence nobody could act
// on, because the file was there and full.
func TestAnUnknownFieldIsRefusedRatherThanIgnored(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "typo.json")
	body := `{"schema":1,"source":"test","markz":[
	  {"clip":"bird-synthetic.mp4","verdict":"false_positive","why":"because"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := loadSyntheticReview(path); err == nil {
		t.Fatal("a file with `markz` instead of `marks` loaded, and its marks would be " +
			"silently empty")
	}
}

func TestASchemaThisBuildDoesNotReadIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "future.json")
	body := `{"schema":2,"source":"test","marks":[]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := loadSyntheticReview(path)
	if err == nil {
		t.Fatal("a schema 2 file loaded against a schema 1 reader")
	}
	if !strings.Contains(err.Error(), "schema 2") {
		t.Errorf("the refusal does not name the schema it refused: %v", err)
	}
}

// The human file must stay empty.
//
// Its `$comment` forbids filling it without a person having watched the footage, and
// the correct state of that file in this repository is an empty `marks` array. This
// test exists so that a well-meaning edit is caught by a gate rather than by whoever
// reads it next.
func TestTheHumanReviewIsStillEmpty(t *testing.T) {
	path := humanReviewPath()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("the human review is not present here: %v", err)
	}
	var rv struct {
		Marks []json.RawMessage `json:"marks"`
	}
	if err := json.Unmarshal(raw, &rv); err != nil {
		t.Fatalf("testdata/groundtruth.json does not parse: %v", err)
	}
	if len(rv.Marks) > 0 {
		t.Fatalf("testdata/groundtruth.json now holds %d mark(s). Filling it is allowed and "+
			"correct -- but only when somebody has actually watched the footage, and the "+
			"author of that mark has to be the person who watched. If nobody has, this file "+
			"is the one place a number must not appear", len(rv.Marks))
	}
}
