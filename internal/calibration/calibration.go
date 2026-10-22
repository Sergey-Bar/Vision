// Package calibration measures the threat engine against labelled history.
//
// # Why this package exists
//
// The threat engine's score was a number nobody had ever checked against an
// outcome. It was plausible, it was monotone in the factors it read, and
// there was no measurement of whether the incidents it put at the top of the
// attention queue were the ones an operator would have called real.
//
// The labelled history already exists: an operator's disposition says whether
// an incident was a true positive, a false positive, or benign. That is the
// only source of ground truth this product has, and it is the source this
// package reads.
//
// # Three things live here
//
//   - The PRIOR: the local base rate for a rule at a site, smoothed, and
//     keyed by the site's own history rather than by a global constant.
//   - The CORPUS: the labelled cases, kept as data so a score is
//     reproducible and a change is a diff.
//   - The REPORT: precision, recall, calibration error, and the attention
//     cost, for the calibrated engine and for the legacy one beside it.
//
// # The corpus is a fixture, and it says so
//
// The committed corpus is a FIXTURE: cases with the shape the product's
// history has, carrying labels. It is not a claim that these are any
// particular site's real incidents, and the file says so in its own
// `provenance` field.
//
// `internal/calibration/generate.go` reads the product's real labelled
// history and writes a corpus in the same shape, and the database-backed
// test runs it. A site with real history regenerates its own corpus with
// `go test ./internal/calibration -update`, and the gate then measures the
// engine against that site's own labels rather than against a fixture.
//
// The golden-file discipline is the one `internal/evaluation` already uses:
// the expectations live in the file, `-update` rewrites them, and a
// deliberate change to the engine is a diff in the corpus rather than a
// surprise in a test log.
package calibration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// CorpusFileName is the committed corpus and golden file.
const CorpusFileName = "calibration.corpus.json"

// CorpusVersion is the corpus's own contract.
//
// It changes when the shape of a case changes, and the loader refuses a
// corpus it does not understand rather than scoring a file whose fields it
// silently ignored.
const CorpusVersion = "security.io/calibration-corpus/v1"

// Label is the operator's answer about an incident.
//
// Only two of the disposition outcomes are labels in the calibration sense.
// `benign` is a third thing — an incident that was not worth anything — and
// counting it as a false positive would teach the engine that a rule is
// noise when the operator actually said it was harmless. `escalated` and
// `acknowledged` say nothing about whether the incident was real, and
// `dismissed` is a workflow state rather than a judgement.
type Label string

// The two labels the calibration measures against.
const (
	// LabelTruePositive means the operator said the incident was real.
	LabelTruePositive Label = "true_positive"
	// LabelFalsePositive means the operator said the incident was not real.
	LabelFalsePositive Label = "false_positive"
)

// IsLabel reports whether a label is one the calibration measures against.
func (l Label) IsLabel() bool {
	return l == LabelTruePositive || l == LabelFalsePositive
}

// Features are the inputs the engine reads for one case.
//
// They are the anomaly's own facts: what was seen, where, for how long, and
// how many devices saw it. The scorer used to throw two of these away, and
// the corpus is how they were recovered.
type Features struct {
	// AnomalyType is the anomaly engine's kind: "presence".
	AnomalyType string `json:"anomalyType"`
	// ZoneType is the zone's kind: "restricted", "choke", "monitor".
	ZoneType string `json:"zoneType"`
	// Severity is the anomaly's own severity: "low", "medium", "high".
	Severity string `json:"severity"`
	// Devices is how many devices saw it.
	Devices int `json:"devices"`
	// PresentSeconds is how long the class was present.
	PresentSeconds float64 `json:"presentSeconds"`
	// ThresholdSeconds is the rule's window.
	ThresholdSeconds float64 `json:"thresholdSeconds"`
}

// Prior is the site's own base rate for the case's rule, at the moment the
// case was taken.
//
// It is stored with the case rather than recomputed, because the base rate
// moves as the site's history grows and a golden file whose expectations
// change every time a disposition is added is a golden file nobody can
// review.
//
// The prior is LEAVE-ONE-OUT: it is computed from the site's history
// without this case's own label. A prior that included the case it is
// scoring would give the engine the answer.
//
// The type itself is in prior.go, next to the store that produces it.

// Case is one labelled incident.
type Case struct {
	// CaseID names the case. It is stable: a case that moves is a diff.
	CaseID string `json:"caseId"`
	// Site is the site the incident happened at.
	Site string `json:"site"`
	// Rule is the rule bundle's rule that raised it.
	Rule string `json:"rule"`
	// Label is the operator's answer.
	Label Label `json:"label"`
	// Features are what the engine reads.
	Features Features `json:"features"`
	// Prior is the site's base rate for this rule, without this case.
	Prior Prior `json:"prior"`
}

// ComputePriors fills each case's prior from the corpus.
//
// The prior is LEAVE-ONE-OUT: a case's prior is the site's smoothed
// precision computed from the OTHER cases for the same (site, rule). A prior
// that included the case it is scoring would hand the engine the answer, and
// a measurement made that way is a measurement of the fixture rather than of
// the engine.
//
// The arithmetic therefore differs slightly between a case and its
// neighbours — a case whose own label was a false positive lowers the
// remaining true positives' prior — and that is the honest number, not a
// defect in the smoothing.
func ComputePriors(c Corpus) Corpus {
	for i, cs := range c.Cases {
		tp, fp := 0, 0
		for j, other := range c.Cases {
			if i == j {
				continue
			}
			if other.Site != cs.Site || other.Rule != cs.Rule {
				continue
			}
			switch other.Label {
			case LabelTruePositive:
				tp++
			case LabelFalsePositive:
				fp++
			}
		}
		c.Cases[i].Prior = SmoothedPrior(tp, fp)
	}
	return c
}

// Expected is what the two engines decided for a case.
//
// It is the golden part: the file holds the answer, the gate compares the
// engine against it, and `-update` rewrites it.
type Expected struct {
	// Legacy is the legacy engine's decision.
	Legacy Decision `json:"legacy"`
	// Calibrated is the calibrated engine's decision.
	Calibrated Decision `json:"calibrated"`
}

// Decision is one engine's answer for one case.
type Decision struct {
	// Score is the score, 0..100.
	Score int `json:"score"`
	// Action is what the engine decided to do.
	Action string `json:"action"`
	// Severity and Likelihood are the two axes. They are empty for the
	// legacy engine, which derives both from the score.
	Severity   string `json:"severity,omitempty"`
	Likelihood string `json:"likelihood,omitempty"`
}

// Corpus is the committed corpus: the cases, their labels, and the decisions
// both engines made for them.
type Corpus struct {
	// ContractVersion is the corpus's own version.
	ContractVersion string `json:"contractVersion"`
	// Provenance says what the cases are. It is a required field, because a
	// corpus with no stated provenance reads as somebody's real history.
	Provenance string `json:"provenance"`
	// Cases are the labelled cases.
	Cases []Case `json:"cases"`
	// Expectations are the golden decisions, keyed by case id.
	Expectations map[string]Expected `json:"expectations"`
}

// WriteCorpus writes the corpus as indented JSON.
//
// Indented because it is read by humans in a diff more often than by
// machines, and a corpus whose change is a thousand-line wall is a corpus
// nobody reviews.
func WriteCorpus(path string, c Corpus) error {
	body, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("calibration: encode the corpus: %w", err)
	}
	body = append(body, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("calibration: create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return fmt.Errorf("calibration: write %s: %w", path, err)
	}
	return nil
}

// LoadCorpus reads the corpus from disk.
//
// A corpus that does not declare its own version is refused rather than
// scored: a file whose fields this version does not know about would be
// scored with those fields silently ignored, and a measurement that ignores
// part of its input is a measurement of nothing.
func LoadCorpus(path string) (Corpus, error) {
	var c Corpus
	raw, err := os.ReadFile(path)
	if err != nil {
		return c, fmt.Errorf("calibration: read the corpus: %w", err)
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("calibration: parse the corpus: %w", err)
	}
	if c.ContractVersion != CorpusVersion {
		return c, fmt.Errorf("calibration: the corpus at %s declares version %q and this build "+
			"understands %q", path, c.ContractVersion, CorpusVersion)
	}
	if c.Provenance == "" {
		return c, fmt.Errorf("calibration: the corpus at %s states no provenance, and a corpus "+
			"with no stated provenance reads as somebody's real history", path)
	}
	if len(c.Cases) == 0 {
		return c, fmt.Errorf("calibration: the corpus at %s holds no cases", path)
	}
	for i, cs := range c.Cases {
		if !cs.Label.IsLabel() {
			return c, fmt.Errorf("calibration: case %d carries the label %q, which is not one "+
				"the calibration measures against", i, cs.Label)
		}
	}
	return c, nil
}
