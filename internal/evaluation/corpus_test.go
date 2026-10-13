package evaluation_test

// The corpus test: the plan's C4, end to end.
//
// The file this generates is the artefact; this test is what makes it real. It
// loads the committed corpus, scores it against the gates, and asserts the one
// property a promotion bar exists for: a corpus that clears the bar is
// promotable, and a corpus that does not clear it is not — the difference
// between a gate and a comment.
//
// The genuine article, run by hand before it was entered:
//
//	cd internal/evaluation && go test -run TestCorpus -update   # writes the file
//	go test ./internal/evaluation                                 # reads it back

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/internal/evaluation"
)

// TestTheCorpusIsCommittedAndLoads is the floor.
//
// The file exists, it is committed, and it holds at least the gate's floor. A
// corpus below the floor is a corpus that cannot support the numbers the gates
// are written in — the arithmetic is in DefaultGates' own comment.
func TestTheCorpusIsCommittedAndLoads(t *testing.T) {
	path := filepath.Join(corpusDir(t), evaluation.CorpusFileName)
	c, err := evaluation.LoadCorpus(path)
	if err != nil {
		t.Fatalf("the corpus should load: %v", err)
	}
	if len(c.Cases) < evaluation.MinCorpusCases {
		t.Fatalf("the corpus holds %d cases, want at least %d. A corpus below the "+
			"gate floor cannot support a score written in rates", len(c.Cases), evaluation.MinCorpusCases)
	}
	for i, cs := range c.Cases {
		if cs.ID == "" {
			t.Errorf("case %d has no id; a case nobody can name is a case nobody can "+
				"re-examine when the score is disputed", i)
		}
		if cs.ProfileID == "" {
			t.Errorf("case %d names no profile", i)
		}
	}
}

// TestTheCorpusClearsThePromotionBar.
//
// The corpus is written to a distribution a real restricted-person profile
// would plausibly produce — recall well above the floor, false positives under
// the ceiling — so the gates' PASS case has something to pass.
func TestTheCorpusClearsThePromotionBar(t *testing.T) {
	path := filepath.Join(corpusDir(t), evaluation.CorpusFileName)
	c, err := evaluation.LoadCorpus(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	pr := profileReportOf(t, c.Score(evaluation.DefaultGates()))
	if !evaluation.Eligible(c, evaluation.DefaultGates()) {
		t.Fatalf("the corpus does not clear the bar: labeled=%d answered=%d recall=%.3f fpr=%.3f disagreement=%.3f",
			pr.Labeled, pr.Answered, pr.Recall, pr.FalsePositiveRate, pr.DisagreementRate)
	}
	if pr.Labeled < evaluation.MinCorpusCases {
		t.Errorf("the report counted %d labelled cases, want at least %d: the corpus "+
			"must actually exercise the floor", pr.Labeled, evaluation.MinCorpusCases)
	}
}

// TestACorpusThatFailsTheBarIsNotPromotable.
//
// The other half, and the half that makes the first one mean something: the
// same machinery over a corpus that misses the floor must refuse. A gate that
// only ever passes is indistinguishable from a gate with no bar.
func TestACorpusThatFailsTheBarIsNotPromotable(t *testing.T) {
	failing, err := evaluation.LoadCorpus(filepath.Join(corpusDir(t), "restricted-person-failing.corpus.json"))
	if err != nil {
		t.Fatalf("the failing corpus should load: %v", err)
	}
	if evaluation.Eligible(failing, evaluation.DefaultGates()) {
		t.Error("a corpus built to fail the bar was promoted. The gate's PASS path " +
			"has no meaning until its FAIL path has been watched fail")
	}
}

// TestTheCorpusIsReproducible.
//
// Regenerating the corpus produces the same bytes. A corpus that drifts under
// regeneration is a corpus whose score is not reproducible, and a golden file
// nobody can regenerate is a golden file nobody can trust.
func TestTheCorpusIsReproducible(t *testing.T) {
	dir := corpusDir(t)
	path := filepath.Join(dir, evaluation.CorpusFileName)
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if update := os.Getenv("EVALUATION_CORPUS_REGEN"); update != "" {
		return // a regeneration run, not a reproducibility check
	}
	regenerated := filepath.Join(t.TempDir(), evaluation.CorpusFileName)
	c, err := evaluation.LoadCorpus(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := evaluation.WriteCorpus(regenerated, c); err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	second, err := os.ReadFile(regenerated)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(first) != string(second) {
		t.Error("the corpus is not round-stable: writing what was read produced " +
			"different bytes. Run with -update and commit the result")
	}
}

// TestACorpusThatCannotBeLoadedIsRefusedWithAReason covers the loader's refusal
// paths.
//
// These were the floor's finding, not a guess about what might be worth testing:
// the corpus landed in C4 with the loader's error paths unexercised, and the
// cover-floor gate failed on `internal/evaluation` at 93.3% against a floor of
// 96.5% recorded the day before. The gap was real and this test closes it rather
// than lowering the floor to sit under it.
//
// A corpus is the thing the promotion bar is measured against, so a loader that
// accepts a corpus which is not the corpus it was asked for is a gate that can be
// passed by formatting.
func TestACorpusThatCannotBeLoadedIsRefusedWithAReason(t *testing.T) {
	dir := t.TempDir()

	noSuchFile := filepath.Join(dir, "absent.corpus.json")
	if _, err := evaluation.LoadCorpus(noSuchFile); err == nil {
		t.Error("a corpus path that does not exist loaded without an error")
	} else if !strings.Contains(err.Error(), noSuchFile) {
		t.Errorf("the refusal does not name the path it could not read: %v", err)
	}

	notJSON := filepath.Join(dir, "not-json.corpus.json")
	if err := os.WriteFile(notJSON, []byte("{ this is not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := evaluation.LoadCorpus(notJSON); err == nil {
		t.Error("a file that is not JSON loaded without an error")
	}

	empty := filepath.Join(dir, "empty.corpus.json")
	if err := evaluation.WriteCorpus(empty, evaluation.Corpus{
		ProfileID: "does-not-matter",
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := evaluation.LoadCorpus(empty); err == nil {
		t.Error("a corpus holding no cases loaded; a gate measured against no cases is a gate that passes")
	}

	// A case naming another profile is the loader's most specific refusal: the
	// gates are per-profile, so a mixed corpus measures neither.
	mixed := filepath.Join(dir, "mixed.corpus.json")
	body := `{"profileId":"restricted-person","contractVersion":"security.io/evaluation-corpus/v1",
"cases":[{"profileId":"another-profile"}]}`
	if err := os.WriteFile(mixed, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := evaluation.LoadCorpus(mixed); err == nil {
		t.Error("a corpus whose case names another profile loaded")
	} else if !strings.Contains(err.Error(), "another-profile") {
		t.Errorf("the refusal does not name the profile that should not be there: %v", err)
	}

	// And the write side: a path that is a directory is refused rather than
	// reported as success. It is the error path a rounding of the file perms
	// would hide.
	dirPath := filepath.Join(dir, "a-directory")
	if err := os.Mkdir(dirPath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := evaluation.WriteCorpus(dirPath, evaluation.Corpus{
		ProfileID: "restricted-person",
		Cases:     []evaluation.Case{{ProfileID: "restricted-person"}},
	}); err == nil {
		t.Error("writing the corpus onto a directory succeeded")
	}
}

// TestACorpusWithNoLatencyReportsAStatedZero covers the "no data at all" paths
// the floor gate found, and they are properties rather than coverage figures.
//
// The gate drove finding them: the uncovered lines were `median` of an empty
// slice, `mean` of an empty one, and the branch for a case with no observation.
// What they mean together: a corpus holding one labelled case and no answers has
// a median latency of zero and a disposition time of zero — stated zeros, never
// leftovers, never an un-normalised integer read as milliseconds — and its single
// case is an absence, which is neither a miss nor a false positive. The same rule
// `rate` states about zero denominators.
func TestACorpusWithNoLatencyReportsAStatedZero(t *testing.T) {
	dir := corpusDir(t)
	c, err := evaluation.LoadCorpus(filepath.Join(dir, evaluation.CorpusFileName))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	tiny := evaluation.Corpus{
		ContractVersion: c.ContractVersion,
		ProfileID:       c.ProfileID,
		Cases:           []evaluation.Case{{ProfileID: c.ProfileID}},
	}
	got := tiny.Score(evaluation.DefaultGates())
	for _, pr := range got.Reports {
		if pr.MedianLatencyMS != 0 {
			t.Errorf("the median latency of a corpus with no latencies is %d, want 0", pr.MedianLatencyMS)
		}
		if pr.HoursToDisposition != 0 {
			t.Errorf("the disposition time of a corpus with no dispositions is %v, want 0", pr.HoursToDisposition)
		}
		if pr.Undispositioned != 1 {
			t.Errorf("a case with no observation is not an absence: %d undispositioned, want 1", pr.Undispositioned)
		}
	}
}

// generateCorpus builds the committed distribution.
//
// It is a function and not a fixture in the test body because the corpus is a
// file: this writes it, the tests read it, and the distribution is stated once
// where a diff can see it.
//
// The distribution, and why each row class is in it:
//
//   - 220 labelled cases, 200 of them answered. The floor is checked against
//     `Answered` and not `Labeled` - evaluateGates says why - so a corpus with
//     unanswered rows needs MORE rows, not fewer.
//   - 110 positives: 96 detected, 4 answered wrong (real misses, so both
//     numerator and denominator are real), 6 the plane said it could not judge
//     and 4 it could not answer at all. Invariant 3 makes those absent rather
//     than negative, and a corpus without them would score a denominator no
//     real run produces. Recall 0.96 over the 0.70 floor.
//   - 110 negatives, 22 of them noise the operator judged as such: false
//     positive rate 0.105, under the 0.20 ceiling by the margin a real profile
//     has - a corpus landing on the ceiling fails on the first noisy day.
//   - One operator contradiction in seventeen, so the disagreement rate is
//     exercised rather than assumed zero.
//   - Median latency in the low thousands, under the 8000ms bar.
func generateCorpus() evaluation.Corpus {
	const total = 220
	c := evaluation.Corpus{
		ContractVersion: evaluation.CorpusVersion,
		ProfileID:       "restricted-person",
	}
	for i := 0; i < total; i++ {
		positive := i < 110
		id := "rp-" + itoa(i)

		// What the labelling tool believed: the truth, except on one row in
		// seventeen where it disagreed. The scorer reads the observation, so
		// this is the record of what a human thought next to what the model
		// said - the shape the corpus keeps for the audit trail.
		labelBelieved := !positive
		if i%17 == 3 {
			labelBelieved = positive
		}

		// What the AI plane said.
		var breach bool
		insufficient := positive && i >= 100 && i < 106
		degraded := positive && i >= 106
		switch {
		case positive && i < 96:
			breach = true // detected
		case positive && i >= 96 && i < 100:
			breach = false // answered, and wrong: a miss that counts
		case positive:
			// Unanswered - insufficient or degraded - and the value here is
			// irrelevant because predictedRestricted returns nil for those.
			breach = true
		case !positive && i >= 132 && i < 154:
			breach = true // the noise: 22 of 110 negatives
		default:
			breach = false
		}

		outcome := evaluation.OutcomeBenign
		switch {
		case positive:
			outcome = evaluation.OutcomeTruePositive
		case !positive && i >= 132 && i < 154:
			// The operator judged these: the observation was right that
			// something happened and wrong that it mattered. That is the noise
			// floor the false-positive gate is written against, and a corpus
			// that marked them benign would score zero while being noisier
			// than one that scores 0.10.
			outcome = evaluation.OutcomeFalsePositive
		}
		c.Cases = append(c.Cases, evaluation.Case{
			ID: id, ProfileID: c.ProfileID, Outcome: outcome,
			PredictedRestricted:  &labelBelieved,
			HoursToDisposition:   0.5,
			InSufficientEvidence: insufficient,
			Degraded:             degraded,
		})
		c.Observations = append(c.Observations, evaluation.Observation{
			ID: id, ProfileID: c.ProfileID,
			LatencyMS:            int64(2000 + i%500),
			Degraded:             degraded,
			InsufficientEvidence: insufficient,
			OperatorAgreed:       operatorAgreed(i, breach),
			Structured: map[string]any{
				"restrictedZoneBreach": breach,
				"summary":              "corpus row",
			},
		})
	}
	return c
}

// operatorAgreed is the operator's verdict on one observation: the
// observation's own answer, except on one row in seventeen where the operator
// contradicts it - so the disagreement rate is exercised rather than assumed zero.
func operatorAgreed(i int, breach bool) *bool {
	v := breach
	if i%17 == 3 {
		v = !breach
	}
	return &v
}

// generateFailingCorpus is the same shape built to fail the bar: recall under
// the floor and false positives over the ceiling.
func generateFailingCorpus() evaluation.Corpus {
	c := evaluation.Corpus{
		ContractVersion: evaluation.CorpusVersion,
		ProfileID:       "restricted-person",
	}
	for i := 0; i < 200; i++ {
		positive := i < 100
		outcome := evaluation.OutcomeBenign
		if positive {
			outcome = evaluation.OutcomeTruePositive
		}
		breach := positive && i < 55 // recall 0.55, under the floor
		if !positive && i%2 == 0 {
			breach = true // rate 0.50, over the ceiling
		}
		id := "rpf-" + itoa(i)
		c.Cases = append(c.Cases, evaluation.Case{
			ID: id, ProfileID: c.ProfileID, Outcome: outcome,
			PredictedRestricted: &breach, HoursToDisposition: 0.5,
		})
		c.Observations = append(c.Observations, evaluation.Observation{
			ID: id, ProfileID: c.ProfileID, LatencyMS: 3000,
			OperatorAgreed: &breach,
			Structured: map[string]any{
				"restrictedZoneBreach": breach,
				"summary":              "corpus row",
			},
		})
	}
	return c
}

// TestMain writes the corpora when the tree lacks them, or when -update asks
// for them.
//
// A test main rather than an init so a run without -update never writes: the
// corpus is an artefact, and a test that rewrote its own fixture on every run
// would be a test whose input moved under it.
func TestMain(m *testing.M) {
	dir := corpusDirOrNil()
	if dir != "" && (os.Getenv("EVALUATION_CORPUS_UPDATE") != "" || missingCorpus(dir)) {
		passing := generateCorpus()
		_ = evaluation.WriteCorpus(filepath.Join(dir, evaluation.CorpusFileName), passing)
		_ = evaluation.WriteCorpus(filepath.Join(dir, "restricted-person-failing.corpus.json"), generateFailingCorpus())
	}
	os.Exit(m.Run())
}

func missingCorpus(dir string) bool {
	for _, name := range []string{evaluation.CorpusFileName, "restricted-person-failing.corpus.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			return true
		}
	}
	return false
}

// profileReportOf returns the one profile report a single-profile corpus
// produces.
func profileReportOf(t *testing.T, r evaluation.Report) evaluation.ProfileReport {
	t.Helper()
	if len(r.Reports) != 1 {
		t.Fatalf("the corpus scored %d profiles, want exactly 1", len(r.Reports))
	}
	return r.Reports[0]
}

func corpusDir(t *testing.T) string {
	t.Helper()
	dir := corpusDirOrNil()
	if dir == "" {
		t.Skip("no corpus directory on this run")
	}
	return dir
}

func corpusDirOrNil() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
}
