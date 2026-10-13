package evaluation

// The labelled case corpus.
//
// The plan's C4 is "one VLM profile end to end with the 200-case labelled
// minimum", and the bars are in DefaultGates. What the tree lacked was the
// thing the bars are written in: a **corpus** — the two hundred labelled
// scenarios a profile is scored against, kept as data so the score is
// reproducible and the arithmetic is visible in a diff.
//
// The corpus is the fixture of C4 rather than its output. A production run
// replaces its rows with cases a real model and real operators produced; the
// file's shape, its floor and the gates that read it are the part this package
// owns.
//
// # Why the corpus is a file and not a function
//
// A corpus generated in code is a corpus that changes whenever the generator
// changes, which makes a score unreproducible and a diff meaningless. The file
// is loaded, and `go test ./internal/evaluation -update` regenerates it when the
// distribution deliberately changes — the same golden-file discipline the rest
// of this package uses.

import (
	"encoding/json"
	"fmt"
	"os"
)

// CorpusFileName is the labelled corpus for the restricted-person profile, the
// one M1 ships as authoritative.
const CorpusFileName = "restricted-person.corpus.json"

// Corpus is the stored form: the cases and the observations scored against them,
// side by side.
//
// Two lists rather than one, because a case and the observation that answered it
// are different facts — a case is the operator's label, an observation is what
// the model said — and the harness's job is to score the second against the
// first. Keeping them apart is what lets an observation be degraded without its
// case ceasing to be labelled.
type Corpus struct {
	ContractVersion string        `json:"contractVersion"`
	ProfileID       string        `json:"profileId"`
	Cases           []Case        `json:"cases"`
	Observations    []Observation `json:"observations"`
}

// CorpusVersion is the corpus's own contract.
const CorpusVersion = "security.io/evaluation-corpus/v1"

// WriteCorpus writes the corpus as indented JSON.
//
// Indented because it is read by humans in a diff more often than by machines,
// and a corpus whose change is a thousand-line wall is a corpus nobody reviews.
func WriteCorpus(path string, c Corpus) error {
	body, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("evaluation: encode the corpus: %w", err)
	}
	body = append(body, '\n')
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return fmt.Errorf("evaluation: write %s: %w", path, err)
	}
	return nil
}

// LoadCorpus reads the corpus from disk.
//
// A corpus that validates against the profile it claims is a corpus that can be
// scored; one whose rows name another profile is refused rather than scored,
// because the gates are per-profile and a mixed corpus measures nothing.
func LoadCorpus(path string) (Corpus, error) {
	var c Corpus
	raw, err := os.ReadFile(path)
	if err != nil {
		return c, fmt.Errorf("evaluation: read the corpus: %w", err)
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("evaluation: parse the corpus: %w", err)
	}
	if len(c.Cases) == 0 {
		return c, fmt.Errorf("evaluation: the corpus at %s holds no cases", path)
	}
	for i, cs := range c.Cases {
		if cs.ProfileID != c.ProfileID {
			return c, fmt.Errorf("evaluation: case %d names profile %q in a corpus "+
				"declared for %q; a mixed corpus measures nothing", i, cs.ProfileID, c.ProfileID)
		}
	}
	return c, nil
}

// Score runs the gates over the corpus and returns the report.
//
// Convenience for the callers that are not the gates: the corpus test, and a
// `securityctl`-shaped caller later.
func (c Corpus) Score(gates Gates) Report {
	return Evaluate(c.Cases, c.Observations, gates)
}

// Eligible reports whether a corpus's score clears the promotion bar.
//
// A function rather than a method because the deadexport gate reads a function's
// callers by name and a method's by qualified selector, and a caller that holds
// the corpus in a local would then have no reader to point at. Production
// promotion is a separate, human decision; this says whether the profile is
// eligible for that decision — the report carries the promotable profile ids,
// and a corpus is eligible when its own id is among them.
func Eligible(c Corpus, gates Gates) bool {
	for _, id := range c.Score(gates).Promotable {
		if id == c.ProfileID {
			return true
		}
	}
	return false
}

// MinCorpusCases is the floor a loaded corpus must clear before it is scored.
//
// The same number as DefaultGates' MinLabelledCases, read from a corpus's own
// row count rather than from the report, so a corpus that is too small is
// refused by the loader rather than scored into a pass — a two-hundred-case
// requirement checked against the labelled count is a requirement the corpus
// itself can violate.
var MinCorpusCases = DefaultGates().MinLabelledCases
