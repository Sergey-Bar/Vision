package operatormetrics

// The MTTI baseline, and the comparison the plan's C8 asks for.
//
// # Why a baseline is a separate artefact
//
// `MTTI` measures our decision's clustering time in three parts. The plan's C8
// is the thing that makes that number mean anything: the same camera stream,
// monitored by the same operator WITHOUT this product, measured the same way.
// A speedup computed against a different stream is not evidence — it is a
// comparison of two different jobs, and the plan says so in the acceptance
// rule: *"a speedup measured against a different camera stream is not
// evidence."*
//
// So the baseline lives outside this database, in files an operator writes from
// their own records of the VMS-only period, and the comparison refuses to
// produce a ratio when the stream identities do not match. That refusal is the
// whole feature: the harness that cannot say "not comparable" is a harness that
// produces a number about nothing.
//
// # A file and not a table
//
// A baseline is a fact about a period this product was not present for, so it
// is entered by a person from the record that period already has — the VMS's
// own logs, a stopwatch study, a shift report. A table would put a migration in
// front of a value a human types, and the value is not ours to compute.
// The store is a directory of YAML; `CapturedBy` says who entered it.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Baseline is one operator's measured time-to-investigate on one stream,
// recorded while this product was not watching it.
type Baseline struct {
	// BaselineVersion is the artefact's own contract.
	BaselineVersion string `yaml:"version"`
	// OperatorID is whose baseline this is. The comparison is per-operator
	// because the whole point is the same person doing the same job on the same
	// stream: an operator who triages quickly and an operator who does not are
	// different measurements, and averaging them measures neither.
	OperatorID string `yaml:"operator_id"`
	// StreamRef is the camera stream the baseline covers, in whatever
	// vocabulary the operator's own record uses. It is compared by exact
	// string: a fuzzy identity is not an identity.
	StreamRef string `yaml:"stream_ref"`
	// SiteID is the site, recorded so a comparison cannot cross sites.
	SiteID string `yaml:"site_id"`
	// From and To bound the period the baseline covers.
	From, To time.Time `yaml:"-"`
	FromRaw  string    `yaml:"from"`
	ToRaw    string    `yaml:"to"`
	// MedianMTTISeconds is the operator's own median time-to-investigate over
	// the period. The median, because the same rule that governs our own
	// headline governs theirs: a mean of a handful of incidents is a number
	// without confidence in it.
	MedianMTTISeconds float64 `yaml:"median_mtti_seconds"`
	// Samples is how many incidents the median is over.
	Samples int `yaml:"samples"`
	// CapturedBy names the person who entered this, and CapturedAt is when. A
	// baseline with no author is a number nobody is answerable for.
	CapturedBy  string    `yaml:"captured_by"`
	CapturedAt  time.Time `yaml:"-"`
	CapturedRaw string    `yaml:"captured_at"`
	// How is the method the number came from: "vms_operator_log" for a reading
	// off the VMS's own records, "shift_report" for an operator's tally, and
	// nothing else. A method outside the set is refused rather than accepted,
	// because the method is what tells a reader whether to trust the median.
	How string `yaml:"how"`
}

// BaselineVersion is the artefact's contract version.
const BaselineVersion = "security.io/mtti-baseline/v1"

// allowedMethods are the ways a baseline may have been measured.
var allowedMethods = map[string]bool{
	"vms_operator_log": true,
	"shift_report":     true,
}

// Validate refuses a baseline that cannot be compared.
//
// What it refuses: no operator, no stream, a non-positive median, no samples, no
// author, an unknown method. Each of those is a comparison that would produce a
// number about nothing, and the harness's job is to say so before the ratio is
// computed rather than after.
func (b *Baseline) Validate() error {
	var problems []string
	if strings.TrimSpace(b.OperatorID) == "" {
		problems = append(problems, "operator_id is required: a baseline with no operator is a number about nobody")
	}
	if strings.TrimSpace(b.StreamRef) == "" {
		problems = append(problems, "stream_ref is required: a baseline with no stream is the not-comparable case")
	}
	if b.MedianMTTISeconds <= 0 {
		problems = append(problems, "median_mtti_seconds must be positive: an operator who never investigated is not a baseline")
	}
	if b.Samples <= 0 {
		problems = append(problems, "samples must be positive: a median over nothing is not a median")
	}
	if strings.TrimSpace(b.CapturedBy) == "" {
		problems = append(problems, "captured_by is required: a baseline nobody is answerable for is not evidence")
	}
	if !allowedMethods[b.How] {
		problems = append(problems, "how must be one of vms_operator_log or shift_report: the method is what tells a reader whether to trust the median")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// ReadParse re-reads the artefact's times, and is called by the loader.
//
// The times are stored as strings because a baseline is hand-written YAML and a
// hand-written timestamp is a string. Parsing happens on load, and an
// unparseable one is refused: a baseline with no window is a baseline that
// cannot be said to cover the stream.
func (b *Baseline) ReadParse() error {
	for _, tc := range []struct {
		name, raw string
		dst       *time.Time
	}{
		{"from", b.FromRaw, &b.From},
		{"to", b.ToRaw, &b.To},
		{"captured_at", b.CapturedRaw, &b.CapturedAt},
	} {
		if strings.TrimSpace(tc.raw) == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339, tc.raw)
		if err != nil {
			return fmt.Errorf("operatormetrics: baseline %s is not an RFC3339 instant: %q", tc.name, tc.raw)
		}
		*tc.dst = at
	}
	if !b.To.IsZero() && b.To.Before(b.From) {
		return fmt.Errorf("operatormetrics: baseline window ends before it starts: %s to %s", b.FromRaw, b.ToRaw)
	}
	return nil
}

// BaselineStore reads the baselines from a directory.
type BaselineStore struct {
	Dir string
}

// Load reads every baseline in the directory, refusing the whole load when one
// of them is invalid.
//
// The whole load fails rather than skipping the bad file, because a directory
// with one bad baseline is a directory whose comparison would silently use the
// others — and a baseline that was skipped is a baseline nobody looks at again.
func (s BaselineStore) Load() ([]Baseline, error) {
	if s.Dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("operatormetrics: read the baseline directory: %w", err)
	}
	var out []Baseline
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(s.Dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("operatormetrics: read %s: %w", path, err)
		}
		var b Baseline
		if err := yaml.Unmarshal(raw, &b); err != nil {
			return nil, fmt.Errorf("operatormetrics: parse %s: %w", path, err)
		}
		if err := b.ReadParse(); err != nil {
			return nil, err
		}
		if err := b.Validate(); err != nil {
			return nil, fmt.Errorf("operatormetrics: %s: %w", path, err)
		}
		out = append(out, b)
	}
	// Sorted so a directory listing is stable and a comparison reads the same
	// file twice.
	sort.Slice(out, func(i, j int) bool {
		if out[i].OperatorID != out[j].OperatorID {
			return out[i].OperatorID < out[j].OperatorID
		}
		return out[i].StreamRef < out[j].StreamRef
	})
	return out, nil
}

// For returns the baseline for one operator on one stream, or nil.
//
// The match is exact on both: an operator's baseline on a different stream is
// not that operator's baseline for this one, and returning it would be the
// not-comparable case wearing a number.
func (s BaselineStore) For(operatorID, streamRef string) (*Baseline, error) {
	all, err := s.Load()
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].OperatorID == operatorID && all[i].StreamRef == streamRef {
			return &all[i], nil
		}
	}
	return nil, nil
}

// Comparison is one measured comparison, or the refusal to produce one.
//
// The refusal is a first-class result rather than an error, because "not
// comparable" is a legitimate answer an operator needs and an error would send
// them looking for a fault. `Comparable` is false exactly when the comparison
// would be about a different stream or a different operator.
type Comparison struct {
	Comparable bool   `json:"comparable"`
	Reason     string `json:"reason,omitempty"`
	// BaselineSeconds is the operator's own median, empty when not comparable.
	BaselineSeconds float64 `json:"baseline_seconds,omitempty"`
	// OursSeconds is our measured figure for the same operator and stream.
	OursSeconds float64 `json:"ours_seconds,omitempty"`
	// Speedup is OursSeconds divided into BaselineSeconds. Empty when not
	// comparable, and the emptiness is the point.
	Speedup float64 `json:"speedup,omitempty"`
	// BaselineSamples is how many incidents the baseline's median is over, so
	// a reader can judge the ratio with the same arithmetic the gates use.
	BaselineSamples int `json:"baseline_samples,omitempty"`
}

// CompareBaseline is the plan's C8 in one function: our measured MTTI for one
// operator on one stream, against that operator's own VMS-only baseline for the
// same stream.
//
// A function rather than a method for the reason `evaluation.Eligible` states:
// the deadexport gate reads a function's callers by name and a method's by
// qualified selector, and a caller that holds the store in a local would then
// have no reader to point at.
func CompareBaseline(s BaselineStore, operatorID, streamRef string, oursSeconds float64) (Comparison, error) {
	b, err := s.For(operatorID, streamRef)
	if err != nil {
		return Comparison{}, err
	}
	if b == nil {
		return Comparison{
			Comparable: false,
			Reason: "no baseline recorded for this operator on this stream. A speedup " +
				"measured against a different camera stream is not evidence",
			OursSeconds: oursSeconds,
		}, nil
	}
	if oursSeconds <= 0 {
		return Comparison{
			Comparable:      false,
			Reason:          "our own figure is not measurable yet, so there is nothing to compare",
			BaselineSeconds: b.MedianMTTISeconds, BaselineSamples: b.Samples,
		}, nil
	}
	return Comparison{
		Comparable:      true,
		BaselineSeconds: b.MedianMTTISeconds,
		OursSeconds:     oursSeconds,
		Speedup:         b.MedianMTTISeconds / oursSeconds,
		BaselineSamples: b.Samples,
	}, nil
}

// _ keeps the context import for the harness's own shape: every service in this
// package takes a context, and a caller holding one expects to pass it.
var _ = context.Background
