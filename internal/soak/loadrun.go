package soak

import (
	"time"

	"github.com/Sergey-Bar/vision/internal/observability"
)

// The load-run ARTEFACT.
//
// # What it is
//
// The record a load run leaves behind: the rate it ran at, how long it ran,
// every objective with its target and its measurement, and every objective the
// run did NOT cover.
//
// # Why the last part is the important part
//
// A load run that measured four of the eight objectives and published four
// numbers is indistinguishable, to a reader, from one that measured all eight.
// The objectives it did not cover are the ones nobody is being told about, and
// a gate that only counted the covered ones would pass a run that proved
// almost nothing.
//
// The roadmap names this as the one thing that is not cuttable: publishing an
// SLO the run did not cover is worse than publishing none.

// LoadRun is what a load run leaves behind.
type LoadRun struct {
	// Version is the artefact's own contract.
	Version string `json:"version"`

	// StartedAt and FinishedAt are when the run happened.
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`

	// Rate is the rate the run pushed events at, in events per second.
	Rate float64 `json:"rate_events_per_second"`
	// Events is how many events the run pushed.
	Events int `json:"events"`
	// DurationSeconds is how long it took.
	DurationSeconds float64 `json:"duration_seconds"`

	// Criteria are the soak's own pass criteria, with the ones that were not
	// EXERCISED named apart from the ones that passed.
	Criteria []CriterionResult `json:"criteria"`

	// Objectives are the SLOs the run measured, each with its target and its
	// measurement.
	Objectives []ObjectiveReading `json:"objectives"`

	// Uncovered are the objectives the run did NOT measure.
	//
	// It is the field the whole artefact exists for: a reader who sees eight
	// objectives and three uncovered knows the run measured five, and a gate
	// that reads it knows the run is not a load run.
	Uncovered []string `json:"uncovered"`

	// Note carries what a reader needs to know and would otherwise have to
	// guess.
	Note string `json:"note"`
}

// LoadRunVersion is the artefact's contract.
const LoadRunVersion = "security.io/load-run/v1"

// ObjectiveReading is one objective's measurement.
type ObjectiveReading struct {
	// Name is the objective's name, as `observability` spells it.
	Name string `json:"name"`
	// Target is what the objective is measured against.
	Target string `json:"target"`
	// Measured is what the run saw, or the empty string when the run did not
	// measure it.
	Measured string `json:"measured"`
	// Breached says whether the measurement violated the target.
	//
	// It is a pointer because "not measured" and "not breached" are different
	// answers, and a boolean cannot tell them apart.
	Breached *bool `json:"breached"`
}

// Complete says whether the run covered EVERY objective.
//
// It is the gate's whole question, and the answer is computed against the
// objectives this build evaluates rather than against a list kept beside the
// artefact, so a new objective and an old artefact cannot both be right.
func (r LoadRun) Complete() bool {
	return len(r.Uncovered) == 0 && len(r.Objectives) == len(observability.SLONames())
}

// UncoveredObjectives is the objectives in the build that the run did not
// measure.
//
// It is computed from the BUILD and the ARTEFACT, not from the artefact alone:
// an artefact that lists three objectives and says it covered all eight is
// wrong, and the comparison is what catches it.
func UncoveredObjectives(r LoadRun) []string {
	measured := map[string]bool{}
	for _, o := range r.Objectives {
		measured[o.Name] = true
	}
	var out []string
	for _, name := range observability.SLONames() {
		if !measured[name] {
			out = append(out, name)
		}
	}
	return out
}
