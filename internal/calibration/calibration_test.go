package calibration_test

// The gate.
//
// The engine's score had never been measured against an outcome. This file
// measures it: the corpus holds labelled cases and the golden decisions both
// engines made for them, and this test checks that the engines still make
// those decisions and reports what the decisions cost.
//
// It is the gate for the whole calibrated engine. Without it the engine is a
// plausible-looking rearrangement of a number nobody had checked, and the
// plan's D-14 would be a description rather than a decision.
//
// # The golden file
//
// The expectations live in the corpus file, and `go test ./internal/calibration
// -update` rewrites them. A deliberate change to the engine is therefore a
// diff in a file a human reads, rather than a surprise in a test log. The
// discipline is the one `internal/evaluation` already uses.

import (
	"flag"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/Sergey-Bar/vision/internal/calibration"
	"github.com/Sergey-Bar/vision/internal/threat"
)

// update asks for the golden file to be rewritten.
//
// It is a flag rather than an environment variable because the plan names it
// as `go test ./internal/calibration -update`, and a command that does not
// work is a command nobody runs.
var update = flag.Bool("update", false, "rewrite the calibration corpus's golden decisions")

// corpusPath is the committed corpus.
func corpusPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(".", calibration.CorpusFileName)
}

// TestMain rewrites the golden file when `-update` asks for it.
//
// A test main rather than an init so a run without `-update` never writes:
// the corpus is an artefact, and a test that rewrote its own fixture on every
// run would be a test whose input moved under it.
func TestMain(m *testing.M) {
	// The flags are parsed here rather than left to `m.Run`, because the
	// update happens BEFORE the run and `m.Run` is what parses them.
	flag.Parse()
	if *update {
		path, err := filepath.Abs(calibration.CorpusFileName)
		if err == nil {
			if c, err := calibration.LoadCorpus(path); err == nil {
				c = calibration.ComputePriors(c)
				c = calibration.BuildExpectations(c)
				if err := calibration.WriteCorpus(path, c); err == nil {
					_, _ = os.Stderr.WriteString("calibration: rewrote " + path + "\n")
				}
			}
		}
	}
	os.Exit(m.Run())
}

// TestThePriorIsSmoothed
//
// The prior is Laplace-smoothed, and an empty history is an absence rather
// than a negative observation.
//
// Without the smoothing a rule with one true positive and no false positives
// has a prior of 1, and the next incident it raises scores as high as the
// first one did and the one after that drops to zero. With it, a rule with
// one true positive has a prior of two thirds and a rule with none has one
// over n plus one -- neither of which is a claim about the world that the
// engine should act on.
//
// And a site with no history has NO prior. The engine then leaves the score
// alone, because an absence is not a negative observation.
func TestThePriorIsSmoothed(t *testing.T) {
	// No history at all: no prior, and the engine must not treat that as
	// "this rule is never right here".
	p := calibration.SmoothedPrior(0, 0)
	if p.Known {
		t.Error("a site with no labelled history has a KNOWN prior: an absence is not a " +
			"negative observation")
	}
	if p.Rate != 0 {
		t.Errorf("the prior with no history is %v, want 0", p.Rate)
	}

	// One true positive and no false positives: two thirds, not one.
	p = calibration.SmoothedPrior(1, 0)
	if !p.Known {
		t.Error("a site with one labelled incident has no prior")
	}
	if math.Abs(p.Rate-2.0/3.0) > 1e-9 {
		t.Errorf("the prior for one true positive and no false positives is %v, want %v: "+
			"the smoothing is what stops a single verdict from being a certainty", p.Rate, 2.0/3.0)
	}

	// A rule that is never right here keeps a small prior and no more.
	p = calibration.SmoothedPrior(0, 9)
	if !p.Known {
		t.Error("a site with nine labelled incidents has no prior")
	}
	if p.Rate >= 0.2 {
		t.Errorf("the prior for nine false positives is %v, and a rule that is never right "+
			"here is below the noise threshold of 0.2", p.Rate)
	}

	// The smoothing is symmetric in the counts and monotone in the truth.
	if calibration.SmoothedPrior(5, 5).Rate != 0.5 {
		t.Errorf("the prior for five of each is %v, want 0.5", calibration.SmoothedPrior(5, 5).Rate)
	}
	if calibration.SmoothedPrior(9, 1).Rate <= calibration.SmoothedPrior(5, 5).Rate {
		t.Error("a rule that is right more often does not have a higher prior")
	}
}

// TestTheCorpusIsLoaded is the corpus's own gate: a corpus that cannot be
// loaded, or that holds no cases, measures nothing.
func TestTheCorpusIsLoaded(t *testing.T) {
	c, err := calibration.LoadCorpus(corpusPath(t))
	if err != nil {
		t.Fatalf("load the corpus: %v", err)
	}
	if len(c.Cases) == 0 {
		t.Fatal("the corpus holds no cases")
	}
	if c.Provenance == "" {
		t.Fatal("the corpus states no provenance")
	}
}

// TestEveryCaseHasAGoldenDecision
//
// Every case in the corpus carries the decision both engines made for it.
//
// A case with no expectation is a case the gate measures nothing about, and
// a gate that measures nothing about a case is a gate that reads as a pass.
func TestEveryCaseHasAGoldenDecision(t *testing.T) {
	c, err := calibration.LoadCorpus(corpusPath(t))
	if err != nil {
		t.Fatalf("load the corpus: %v", err)
	}
	for _, cs := range c.Cases {
		want, ok := c.Expectations[cs.CaseID]
		if !ok {
			t.Errorf("the case %s carries no golden decision. Run "+
				"`go test ./internal/calibration -update`", cs.CaseID)
			continue
		}
		if want.Legacy.Action == "" || want.Calibrated.Action == "" {
			t.Errorf("the case %s carries an empty golden decision", cs.CaseID)
		}
	}
}

// TestTheEngineStillDecidesTheSameWay
//
// The gate.
//
// Both engines are run over every case and compared with the golden file. A
// difference is a failure, and the failure names the case, what the engine
// decided and what the file says.
func TestTheEngineStillDecidesTheSameWay(t *testing.T) {
	c, err := calibration.LoadCorpus(corpusPath(t))
	if err != nil {
		t.Fatalf("load the corpus: %v", err)
	}
	report := calibration.Evaluate(c)

	if len(report.Legacy.Mismatches) > 0 {
		for _, m := range report.Legacy.Mismatches {
			t.Errorf("the legacy engine changed its mind: %s", m)
		}
	}
	if len(report.Calibrated.Mismatches) > 0 {
		for _, m := range report.Calibrated.Mismatches {
			t.Errorf("the calibrated engine changed its mind: %s", m)
		}
	}

	t.Logf("the corpus holds %d cases", report.Cases)
	t.Logf("legacy:      escalated %d, attended %d, attention precision %.2f, "+
		"attention recall %.2f, calibration error %.3f, HAM/1000 %.1f",
		report.Legacy.Escalated, report.Legacy.Attention, report.Legacy.AttentionPrecision,
		report.Legacy.AttentionRecall, report.Legacy.CalibrationError, report.Legacy.HAMPer1000)
	t.Logf("calibrated:  escalated %d, attended %d, attention precision %.2f, "+
		"attention recall %.2f, calibration error %.3f, HAM/1000 %.1f",
		report.Calibrated.Escalated, report.Calibrated.Attention, report.Calibrated.AttentionPrecision,
		report.Calibrated.AttentionRecall, report.Calibrated.CalibrationError, report.Calibrated.HAMPer1000)
}

// TestTheCalibratedEngineBeatsTheLegacyOneOnTheCorpus
//
// The corpus gate.
//
// The calibrated engine has to be BETTER than the one it replaces, on the
// corpus, on the numbers that decide whether a control room keeps looking at
// the screen. "Better" is checked three ways -- the attention precision, the
// attention recall and the calibration error -- and the fourth assertion is
// the one the calibration factor exists for: at a site whose own labelled
// history says the rule is noise, the calibrated engine does not escalate.
//
// The legacy engine's numbers are the baseline and are reported in the log
// above, so a reader can see what the comparison was against.
func TestTheCalibratedEngineBeatsTheLegacyOneOnTheCorpus(t *testing.T) {
	c, err := calibration.LoadCorpus(corpusPath(t))
	if err != nil {
		t.Fatalf("load the corpus: %v", err)
	}
	report := calibration.Evaluate(c)

	if report.Calibrated.AttentionPrecision <= report.Legacy.AttentionPrecision {
		t.Errorf("the calibrated engine's attention precision is %.3f and the legacy engine's is %.3f: "+
			"the new engine sends the operator to no fewer false alarms than the one it replaces",
			report.Calibrated.AttentionPrecision, report.Legacy.AttentionPrecision)
	}
	if report.Calibrated.AttentionRecall <= report.Legacy.AttentionRecall {
		t.Errorf("the calibrated engine's attention recall is %.3f and the legacy engine's is %.3f: "+
			"the new engine finds no more of the real incidents than the one it replaces",
			report.Calibrated.AttentionRecall, report.Legacy.AttentionRecall)
	}
	if report.Calibrated.CalibrationError >= report.Legacy.CalibrationError {
		t.Errorf("the calibrated engine's calibration error is %.3f and the legacy engine's is %.3f: "+
			"the new engine's score is no closer to the observed rate of realness",
			report.Calibrated.CalibrationError, report.Legacy.CalibrationError)
	}

	// The calibration factor, and the assertion it exists for.
	//
	// A rule that is noise AT THIS SITE is held rather than escalated, and
	// the base rate is what says so. With the factor disabled the engine
	// reads the evidence and ignores the site's own record of the rule, and
	// it escalates the very cases the history says are noise -- which is the
	// whole reason the engine reads the history.
	noise := 0
	for _, cs := range c.Cases {
		if !cs.Prior.Known || !threat.IsNoisePrior(cs.Prior.Rate) {
			continue
		}
		noise++
		d := calibration.ScoreCaseCalibrated(cs)
		if d.Action == threat.ActionEscalate {
			t.Errorf("the case %s is at a site whose history says the rule is right %.0f%% of the "+
				"time, and the calibrated engine ESCALATED it. The base rate is the whole point: "+
				"the evidence is about the rule and not about this incident",
				cs.CaseID, cs.Prior.Rate*100)
		}
	}
	if noise == 0 {
		t.Error("the corpus holds no case at a site whose history says the rule is noise, " +
			"so the base rate is asserted and demonstrated for nothing")
	}
}

// TestTheCorpusCoversEveryCellOfTheMatrix
//
// Every one of the sixteen cells of the decision matrix is reached by at
// least one case.
//
// This is the gate for the plan's claim that the two-dimensional decision
// makes the matrix's cells reachable. The legacy engine derived both axes
// from one score with the same thresholds, which made twelve of the sixteen
// unreachable, and the corpus is the evidence that they are reachable now.
func TestTheCorpusCoversEveryCellOfTheMatrix(t *testing.T) {
	c, err := calibration.LoadCorpus(corpusPath(t))
	if err != nil {
		t.Fatalf("load the corpus: %v", err)
	}

	severities := []threat.SeverityLevel{
		threat.SeverityCritical, threat.SeverityHigh,
		threat.SeverityMedium, threat.SeverityLow,
	}
	likelihoods := []threat.LikelihoodLevel{
		threat.LikelihoodVeryHigh, threat.LikelihoodHigh,
		threat.LikelihoodMedium, threat.LikelihoodLow,
	}

	covered := map[string]bool{}
	for _, cs := range c.Cases {
		d := calibration.ScoreCaseCalibrated(cs)
		covered[string(d.Severity)+"×"+string(d.Likelihood)] = true
	}
	for _, s := range severities {
		for _, l := range likelihoods {
			if !covered[string(s)+"×"+string(l)] {
				t.Errorf("the corpus reaches no case in the cell %s×%s, so the matrix's "+
					"reachability is asserted for sixteen cells and demonstrated for fifteen",
					s, l)
			}
		}
	}
}

// TestTheCorpusIsRoundStable
//
// Writing what was read produces the same bytes.
//
// A corpus that drifts under regeneration is a corpus whose score is not
// reproducible, and a golden file nobody can regenerate is a golden file
// nobody can trust.
func TestTheCorpusIsRoundStable(t *testing.T) {
	if *update {
		return // a regeneration run, not a reproducibility check
	}
	path := corpusPath(t)
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	c, err := calibration.LoadCorpus(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	regenerated := filepath.Join(t.TempDir(), calibration.CorpusFileName)
	if err := calibration.WriteCorpus(regenerated, c); err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	second, err := os.ReadFile(regenerated)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(first) != string(second) {
		t.Error("the corpus is not round-stable: writing what was read produced different bytes")
	}
}
