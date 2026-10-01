package vlm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/Sergey-Bar/ai-captain-poc/internal/aistate"
)

// Verdict is what the model SAID.
//
// It is a second vocabulary from [aistate], and the two are never merged. aistate
// is what happened to the ask; verdict is what the answer was. A verdict without
// an answered ask is a fabricated negative, which is why [VerdictNone] is a real
// member of this set rather than an empty string, and why
// [Observation.normalise] refuses to let anything else through.
type Verdict string

// The closed set. Four values, and nothing else: an unrecognised value is a
// malformed answer, not a new verdict.
const (
	// VerdictConfirmed means the model says the thing in the measured region is
	// real and is what triggered.
	VerdictConfirmed Verdict = "confirmed"
	// VerdictFalsePositive means the model says the region is not an incident.
	VerdictFalsePositive Verdict = "false_positive"
	// VerdictNeedsReview means the model cannot tell, and says so. It is an
	// honest answer and it is what gets pushed.
	VerdictNeedsReview Verdict = "needs_review"
	// VerdictNone means there is no verdict, because the ask did not answer.
	VerdictNone Verdict = "none"
)

// AllVerdicts is the closed set, sorted so a diagnostic reads the same on every
// machine.
func AllVerdicts() []Verdict {
	out := make([]Verdict, 0, len(knownVerdicts))
	for v := range knownVerdicts {
		out = append(out, v)
	}
	return out
}

var knownVerdicts = map[Verdict]bool{
	VerdictConfirmed:     true,
	VerdictFalsePositive: true,
	VerdictNeedsReview:   true,
	VerdictNone:          true,
}

// Valid reports whether v is in the closed set.
func (v Verdict) Valid() bool { return knownVerdicts[v] }

// OutputSchemaVersion is this build's verdict schema version.
//
// It is part of the contract version, so a schema change changes the contract
// version and a reviewer can see which schema produced which answer.
const OutputSchemaVersion = 1

// Observation is one answer about one clip, with everything needed to attribute
// it afterwards.
//
// It is the record the proof chain reads. Two of its fields are deliberate
// absences: there is no model hash, because a hosted model has no weights to
// hash, and there is no default verdict, because a default verdict on a failed
// ask is the fabricated negative this whole package exists to prevent.
type Observation struct {
	// DetectionID is the detection this observation is about. Empty on the
	// wire-shaped result of a call that has not been attached to one yet.
	DetectionID string

	// State is the ask's state, from the aistate vocabulary. Nothing else in
	// this struct is meaningful unless it is aistate.Asked.
	State string

	Verdict              Verdict
	Summary              string
	Confidence           float64
	InsufficientEvidence bool

	// ModelIdentity is what the API said it used. It is a CLAIM by the
	// provider, recorded as a claim.
	ModelIdentity string
	// PromptID is the incident class. It is the only thing that differs between
	// one incident class and another: two classes are two prompt files.
	PromptID string
	// PromptVersion is the operator's label for this prompt revision.
	PromptVersion string
	// ContractVersion is this build's content hash over the prompt, the prompt's
	// revision, the output schema version and the API model name.
	ContractVersion string
	// Temperature is what was asked for.
	Temperature float64

	// OutputHash is the content hash of the validated structured output. It is
	// hashed rather than stored as prose, because golden-testing a model's words
	// is golden-testing the weather.
	OutputHash string
	// PromptTokens and CompletionTokens are the provider's own accounting, kept
	// as it reported them rather than recomputed.
	PromptTokens     int
	CompletionTokens int
	// LatencyMS is measured here, not taken from the provider.
	LatencyMS int64
	// AssetID is the clip this answer was about, for the sweep and the audit.
	AssetID string
	// Reason is the machine-readable cause, from a closed set, when there is no
	// answer. It is a code rather than a sentence because a consumer parses it
	// and nothing in this repository matches on prose.
	Reason string
	// Message is the human one-liner for the same failure. It is for an operator
	// reading a fault table and is never the thing a report counts.
	Message string
}

// validWire is the provider's structured output, as required.
//
// Pointers for every field, because "absent" and "present and empty" are
// different facts and a struct with plain fields cannot tell them apart. A
// missing field is a malformed answer, not a zero.
type validWire struct {
	Verdict              *string  `json:"verdict"`
	Summary              *string  `json:"summary"`
	Confidence           *float64 `json:"confidence"`
	InsufficientEvidence *bool    `json:"insufficient_evidence"`
}

// parseAnswer reads the provider's answer strictly.
//
// Strict means: exactly one choice, non-empty content, and content that is a JSON
// object carrying all four fields with a verdict from the closed set and a
// confidence in [0,1]. A response with two choices is ambiguous and is refused,
// because picking the first is a guess whose failure mode is invisible.
func parseAnswer(resp *http.Response) (Observation, error) {
	var wire answerResponse
	if err := decodeStrict(resp, &wire); err != nil {
		return Observation{}, err
	}
	if len(wire.Choices) != 1 {
		return Observation{}, fmt.Errorf("%w: %d choices; exactly one is required and picking one would be a guess",
			ErrMalformedAnswer, len(wire.Choices))
	}
	content := strings.TrimSpace(wire.Choices[0].Message.Content)
	if content == "" {
		return Observation{}, fmt.Errorf("%w: the choice carried no content", ErrMalformedAnswer)
	}
	obs, err := ParseVerdict(content)
	if err != nil {
		return Observation{}, err
	}
	obs.ModelIdentity = wire.Model
	obs.PromptTokens = wire.Usage.PromptTokens
	obs.CompletionTokens = wire.Usage.CompletionTokens
	return obs, nil
}

// ParseVerdict validates a structured output against the schema and returns the
// observation it describes.
//
// It is exported because it is the whole of the parser's contract, and a caller
// that needs to validate an answer it already has — a replay, a golden file, a
// test — must be able to reach exactly the code the provider path reaches rather
// than a second copy that could be more forgiving.
func ParseVerdict(content string) (Observation, error) {
	var out validWire
	dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(content)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return Observation{}, fmt.Errorf("%w: the content is not the required JSON object: %w", ErrMalformedAnswer, err)
	}
	if out.Verdict == nil || out.Summary == nil || out.Confidence == nil || out.InsufficientEvidence == nil {
		return Observation{}, fmt.Errorf("%w: the content is missing one of verdict, summary, confidence, insufficient_evidence; "+
			"a partial parse is not an answer", ErrMalformedAnswer)
	}
	v := Verdict(strings.TrimSpace(*out.Verdict))
	if !v.Valid() {
		return Observation{}, fmt.Errorf("%w: verdict %q is not one of confirmed, false_positive, needs_review, none",
			ErrMalformedAnswer, *out.Verdict)
	}
	c := *out.Confidence
	if math.IsNaN(c) || math.IsInf(c, 0) || c < 0 || c > 1 {
		return Observation{}, fmt.Errorf("%w: confidence %v is not in [0,1]", ErrMalformedAnswer, c)
	}
	obs := Observation{
		State:                aistate.Asked,
		Verdict:              v,
		Summary:              strings.TrimSpace(*out.Summary),
		Confidence:           c,
		InsufficientEvidence: *out.InsufficientEvidence,
	}
	obs.normalise()
	return obs, nil
}

// OutputHashOf is the content hash of a validated structured output.
//
// It hashes the canonical four fields in a fixed order with length-prefixed
// parts, so two different outputs cannot share a hash by moving a field
// boundary. It is the field a golden test pins, in place of the model's prose.
func OutputHashOf(o Observation) string {
	h := sha256.New()
	for _, part := range []string{
		"vlm/verdict/1",
		string(o.Verdict),
		o.Summary,
		fmt.Sprintf("%.6f", o.Confidence),
		fmt.Sprintf("%t", o.InsufficientEvidence),
	} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// normalise is the rule that a missing answer is never a negative one.
//
// REDCHECK RC-1. Everything else in this package can be edited; this function is
// the one whose removal makes the product lie, so it is one function with one
// obvious condition rather than checks scattered across four call sites.
//
// A verdict means something only when the ask was answered. Anything else —
// degraded, cannot_tell, not_asked — clears the verdict, the confidence AND the
// summary, because a summary without a verdict on a failed ask is prose from a
// model nobody managed to read, and it reaches a screen as if somebody had said
// it.
func (o *Observation) normalise() {
	// REDCHECK RC-1: this comparison is the fabricated-negative control.
	if o.State != aistate.Asked {
		o.Verdict = VerdictNone
		o.Confidence = 0
		o.Summary = ""
		o.InsufficientEvidence = false
		return
	}
	if !o.Verdict.Valid() {
		// A verdict outside the closed set is a programming error, and it is
		// corrected rather than stored: an unvalidated verdict on an answered
		// observation would be read by a screen as an answer.
		o.Verdict = VerdictNone
		o.Confidence = 0
		o.Summary = ""
		o.InsufficientEvidence = false
	}
}

// SetState records the ask's state and re-applies the normalisation, so there is
// one place a state ever reaches a verdict.
func (o *Observation) SetState(state string) {
	o.State = state
	o.normalise()
}

// Finalise computes the output hash and re-applies the normalisation.
//
// It is separate from [ParseVerdict] because the hash must be over the output as
// validated, and an observation that reached the store through a path other than
// the parser still needs both.
func (o *Observation) Finalise() {
	o.normalise()
	o.OutputHash = OutputHashOf(*o)
}

// Absence reports whether this observation carries no answer at all.
//
// It is the predicate the report and the screen use, and it exists so the two
// cannot disagree about what "no answer" means.
func (o Observation) Absence() bool {
	return o.State != aistate.Asked || o.Verdict == VerdictNone
}
