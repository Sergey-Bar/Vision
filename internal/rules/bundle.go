// Package rules compiles and evaluates rule bundles.
//
// A rule bundle is untrusted input. It arrives as YAML, written by whoever
// configured the site, and it decides which events become incidents that an
// operator is expected to act on. So the compilation path has no shell, no SQL,
// no network, no dynamic code and no regular expression - the strongest operator
// in the whole language is a fixed prefix match. Everything else about the
// design follows from that.
//
// The pipeline is four stages, and each is a gate:
//
//	YAML - JSON Schema - semantic validation - compiled rule
//
// The stages are separate because they fail differently. A schema violation is a
// syntax problem with a file location. A semantic violation is a rule that is
// well-formed but cannot work - it references a zone that does not exist, or two
// rules with the same priority demand opposite outcomes, which is a
// configuration error that must never be resolved by silently picking one.
//
// Evaluation is deterministic and side-effect free. Given the same stored facts
// and the same stored AI observation, it produces the same outcome - which is
// what makes a months-old decision replayable rather than merely stored.
package rules

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// SchemaVersion is the bundle schema this build compiles. A bundle declaring
// another version is refused rather than interpreted optimistically: a bundle
// written for a future version may use a construct this compiler would
// misread, and a rule that silently means something else is worse than one that
// refuses to load.
const SchemaVersion = "security.io/v1"

// Tier is an evidence tier. The tiers are ordered by how much they can be
// trusted, and a rule states the minimum it needs.
type Tier string

const (
	// TierA is authoritative system state reported by the source of truth.
	TierA Tier = "A"
	// TierB is a deterministic derived fact computed by the core.
	TierB Tier = "B"
	// TierC is an AI observation. May be absent, and a missing Tier C signal is
	// never a negative one.
	TierC Tier = "C"
)

// rank orders tiers so a rule's minimum can be compared against a signal's
// actual tier.
func (t Tier) rank() int {
	switch t {
	case TierA:
		return 3
	case TierB:
		return 2
	case TierC:
		return 1
	default:
		return 0
	}
}

// AtLeast reports whether t is at least as trustworthy as min.
func (t Tier) AtLeast(minimum Tier) bool { return t.rank() >= minimum.rank() }

// DegradeTo is what a rule does when an AI observation it needs is absent. It
// is mandatory, never defaulted at evaluation time, because "what happens when
// the model is down" is a decision an operator makes once and reads many times.
type DegradeTo string

const (
	// DegradeToUnverified records the incident as UNVERIFIED rather than
	// treating the missing observation as a negative one.
	DegradeToUnverified DegradeTo = "UNVERIFIED"
	// DegradeToProceedWithout evaluates as if the Tier C clause were absent.
	DegradeToProceedWithout DegradeTo = "proceed_without"
)

// Bundle is a parsed rule bundle.
type Bundle struct {
	APIVersion  string   `yaml:"apiVersion" json:"apiVersion"`
	Kind        string   `yaml:"kind" json:"kind"`
	Version     string   `yaml:"version" json:"version"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Site        SiteRef  `yaml:"site" json:"site"`
	Settings    Settings `yaml:"settings" json:"settings"`
	Rules       []Rule   `yaml:"rules" json:"rules"`

	// ContentHash is filled by the compiler and is the identity of the bytes.
	ContentHash string `yaml:"-" json:"contentHash"`
}

// SiteRef names the site a bundle targets.
type SiteRef struct {
	SiteID    string   `yaml:"siteId" json:"siteId"`
	Timezones []string `yaml:"timezones" json:"timezones"`
}

// Settings are the bundle-wide parameters. Clock settings live here rather than
// in a global because two sites have different reference clocks and different
// tolerances, and hard-coding one silently applies one site's assumptions to
// another.
type Settings struct {
	Clock      ClockSettings  `yaml:"clock" json:"clock"`
	Dedupe     DedupeSettings `yaml:"dedupe,omitempty" json:"dedupe,omitempty"`
	BurstMerge BurstSettings  `yaml:"burstMerge,omitempty" json:"burstMerge,omitempty"`
}

// ClockSettings bound drift and skew for this site.
type ClockSettings struct {
	SkewToleranceMS int32  `yaml:"skewTolerance" json:"skewTolerance"`
	DriftWarnMS     int32  `yaml:"driftWarnMs" json:"driftWarnMs"`
	DriftSevereMS   int32  `yaml:"driftSevereMs" json:"driftSevereMs"`
	ReferenceSource string `yaml:"referenceSource" json:"referenceSource"`
}

// DedupeSettings configure burst collapsing.
type DedupeSettings struct {
	WindowSeconds int      `yaml:"windowSeconds" json:"windowSeconds"`
	Keys          []string `yaml:"keys" json:"keys"`
}

// BurstSettings configure merging of a burst into one incident.
type BurstSettings struct {
	WindowSeconds int `yaml:"windowSeconds" json:"windowSeconds"`
	MaxEvents     int `yaml:"maxEvents" json:"maxEvents"`
}

// Rule is one condition and the action it produces.
type Rule struct {
	ID          string    `yaml:"id" json:"id"`
	Description string    `yaml:"description,omitempty" json:"description,omitempty"`
	Enabled     bool      `yaml:"enabled" json:"enabled"`
	Priority    int       `yaml:"priority" json:"priority"`
	Tags        []string  `yaml:"tags,omitempty" json:"tags,omitempty"`
	When        Condition `yaml:"when" json:"when"`
	Then        Action    `yaml:"then" json:"then"`
}

// Condition is a boolean tree over the event and its context.
type Condition struct {
	All []Condition `yaml:"all,omitempty" json:"all,omitempty"`
	Any []Condition `yaml:"any,omitempty" json:"any,omitempty"`
	Not *Condition  `yaml:"not,omitempty" json:"not,omitempty"`

	EventType []string       `yaml:"eventType,omitempty" json:"eventType,omitempty"`
	Device    *DeviceMatch   `yaml:"device,omitempty" json:"device,omitempty"`
	Zone      *ZoneMatch     `yaml:"zone,omitempty" json:"zone,omitempty"`
	Schedule  *ScheduleMatch `yaml:"schedule,omitempty" json:"schedule,omitempty"`

	TimeWindow    *TimeWindow             `yaml:"timeWindow,omitempty" json:"timeWindow,omitempty"`
	Corroborate   *Corroboration          `yaml:"corroborate,omitempty" json:"corroborate,omitempty"`
	Field         *FieldCondition         `yaml:"field,omitempty" json:"field,omitempty"`
	ClockDrift    *DriftCondition         `yaml:"clockDrift,omitempty" json:"clockDrift,omitempty"`
	AIObservation *AIObservationCondition `yaml:"aiObservation,omitempty" json:"aiObservation,omitempty"`
}

// DeviceMatch selects devices.
type DeviceMatch struct {
	ID   string   `yaml:"id,omitempty" json:"id,omitempty"`
	Kind string   `yaml:"kind,omitempty" json:"kind,omitempty"`
	Tags []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// ZoneMatch selects zones, including by ancestry.
type ZoneMatch struct {
	ID         string `yaml:"id,omitempty" json:"id,omitempty"`
	AncestorOf string `yaml:"ancestorOf,omitempty" json:"ancestorOf,omitempty"`
	PathPrefix string `yaml:"pathPrefix,omitempty" json:"pathPrefix,omitempty"`
}

// ScheduleMatch selects by a named schedule. Schedules are defined per site
// rather than in the bundle so a site's opening hours live in one place.
type ScheduleMatch struct {
	ID     string `yaml:"id" json:"id"`
	Invert bool   `yaml:"invert,omitempty" json:"invert,omitempty"`
}

// TimeWindow bounds how far apart two signals may be. It is evaluated against
// normalized_at only: a rule that read wall clock directly would be
// unreproducible on replay.
type TimeWindow struct {
	Field           string `yaml:"field" json:"field"`
	DurationSeconds int    `yaml:"durationSeconds,omitempty" json:"durationSeconds,omitempty"`
	Direction       string `yaml:"direction,omitempty" json:"direction,omitempty"`
}

// Corroboration requires a second independent signal inside a time window.
type Corroboration struct {
	EventTypes            []string `yaml:"eventTypes" json:"eventTypes"`
	MinSignals            int      `yaml:"minSignals" json:"minSignals"`
	MinEvidenceTier       Tier     `yaml:"minEvidenceTier,omitempty" json:"minEvidenceTier,omitempty"`
	RequiresAIObservation bool     `yaml:"requiresAIObservation,omitempty" json:"requiresAIObservation,omitempty"`
	// IfAIAvailable is mandatory whenever an AI observation participates. There
	// is no default: a rule that omitted it would be making the decision at
	// evaluation time, silently, on a site that has never thought about it.
	IfAIAvailable *IfAIObserved `yaml:"ifAIAvailable,omitempty" json:"ifAIAvailable,omitempty"`
}

// IfAIObserved is the declared behaviour when an AI observation is unavailable.
type IfAIObserved struct {
	DegradeTo DegradeTo `yaml:"degradeTo" json:"degradeTo"`
}

// corroborationConsumesAI is the single definition of "this corroboration
// clause consumes a Tier C signal".
//
// It exists because the validator and the engine had drifted apart, and the
// drift was invisible: the validator required `ifAIAvailable` on a clause whose
// `minEvidenceTier` was C, while the compiled rule's RequiresAI was set only
// from `requiresAIObservation`. A bundle could therefore declare a fallback for
// a clause the evaluator then never consulted, and the operator's declared
// behaviour without the model would be dead configuration. Both sides now call
// this, so a clause the validator calls AI-consuming is the clause the engine
// treats as AI-consuming.
func corroborationConsumesAI(c *Corroboration) bool {
	if c == nil {
		return false
	}
	return c.RequiresAIObservation || c.MinEvidenceTier == TierC
}

// FieldCondition compares a whitelisted payload path.
type FieldCondition struct {
	Path  string `yaml:"path" json:"path"`
	Op    string `yaml:"op" json:"op"`
	Value any    `yaml:"value,omitempty" json:"value,omitempty"`
}

// DriftCondition bounds the event's device clock drift.
type DriftCondition struct {
	MaxMS int32 `yaml:"maxMs" json:"maxMs"`
}

// AIObservationCondition reads a validated structured field of a stored
// observation. It never reads model prose, and the field name is checked
// against the profile's schema at compile time.
type AIObservationCondition struct {
	ProfileID string `yaml:"profileId" json:"profileId"`
	Field     string `yaml:"field" json:"field"`
	Equals    any    `yaml:"equals,omitempty" json:"equals,omitempty"`
}

// Action is what a rule produces.
type Action struct {
	CreateIncident       bool       `yaml:"createIncident" json:"createIncident"`
	Severity             string     `yaml:"severity,omitempty" json:"severity,omitempty"`
	Title                string     `yaml:"title,omitempty" json:"title,omitempty"`
	Suppress             bool       `yaml:"suppress,omitempty" json:"suppress,omitempty"`
	MarkDuplicate        bool       `yaml:"markDuplicate,omitempty" json:"markDuplicate,omitempty"`
	Escalate             bool       `yaml:"escalate,omitempty" json:"escalate,omitempty"`
	RequireAIObservation bool       `yaml:"requireAIObservation,omitempty" json:"requireAIObservation,omitempty"`
	Dispatch             []Dispatch `yaml:"dispatch,omitempty" json:"dispatch,omitempty"`
}

// Dispatch is a side effect a matching rule requests.
type Dispatch struct {
	Type   string `yaml:"type" json:"type"`
	Target string `yaml:"target" json:"target"`
}

// RuleIDs returns the bundle's rule identifiers, sorted. Used by the simulate
// diff, which must not depend on the order the rules happen to be written in.
func (b *Bundle) RuleIDs() []string {
	out := make([]string, 0, len(b.Rules))
	for _, r := range b.Rules {
		out = append(out, r.ID)
	}
	sort.Strings(out)
	return out
}

// Lookup returns a rule by id.
func (b *Bundle) Lookup(id string) (Rule, bool) {
	for _, r := range b.Rules {
		if r.ID == id {
			return r, true
		}
	}
	return Rule{}, false
}

// MarshalCanonical renders the bundle in the form its content hash is taken
// over. It is JSON rather than YAML because JSON has one canonical form and
// YAML has several; a hash over a re-serialised YAML file would depend on the
// emitter's line-wrapping.
func (b *Bundle) MarshalCanonical() ([]byte, error) {
	body, err := json.Marshal(b)
	if err != nil {
		return nil, fmt.Errorf("rules: canonical marshal: %w", err)
	}
	return body, nil
}

// KnownEventTypes is the vocabulary the DSL names. A bundle referring to a type
// outside it is a semantic error, not a forward-compatible extension: a rule
// that can never match is a rule an operator believes is running.
var KnownEventTypes = map[string]bool{
	"motion": true, "analytics": true,
	"analytics.restricted_person": true,
	"alarm.created":               true, "alarm.acknowledged": true, "alarm.closed": true,
	"device.camera_offline": true, "device.camera_online": true, "device_state": true,
	"recording.started": true, "recording.stopped": true,
	"bookmark": true, "metadata": true,
	"access.granted": true, "access.denied": true, "gate": true,
}

func knownEventType(t string) bool { return KnownEventTypes[t] }

// FieldOperators is the closed set of comparisons. There is deliberately no
// regular expression: an unbounded pattern in a bundle an operator uploads is a
// denial of service delivered as a YAML file, and the strongest useful operator
// here is a fixed prefix match.
var FieldOperators = map[string]bool{
	"eq": true, "ne": true, "gt": true, "gte": true, "lt": true, "lte": true,
	"in": true, "not_in": true, "contains_prefix": true, "exists": true,
}

// DispatchTypes is the closed set of side effects a rule may request.
var DispatchTypes = map[string]bool{
	"notify_operator": true, "notify_supervisor": true,
	"request_vlm": true, "fetch_evidence": true,
}

// Severities is the closed severity set.
var Severities = map[string]bool{
	"info": true, "low": true, "medium": true, "high": true, "critical": true,
}

// DeviceKinds is the closed device-kind set.
var DeviceKinds = map[string]bool{
	"camera": true, "door": true, "sensor": true, "gate": true,
	"alarm_panel": true, "access_reader": true, "other": true,
}

// maxFieldPathDepth bounds a payload path. A deeply nested path on a 4 KB
// payload is not expensive, but an unbounded one is an invitation to write a
// condition whose cost nobody can predict.
const maxFieldPathDepth = 8

func validFieldPath(p string) bool {
	if p == "" {
		return false
	}
	parts := strings.Split(p, ".")
	if len(parts) > maxFieldPathDepth {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for i, r := range part {
			switch {
			case r >= 'a' && r <= 'z':
			case r >= 'A' && r <= 'Z':
			case r >= '0' && r <= '9':
			case r == '_':
			default:
				return false
			}
			if i == 0 && r >= '0' && r <= '9' {
				return false
			}
		}
	}
	return true
}

// severityRank orders severities so a conflict between two rules is decidable
// and a conflict between two equal severities is not.
func severityRank(s string) int {
	switch s {
	case "info":
		return 1
	case "low":
		return 2
	case "medium":
		return 3
	case "high":
		return 4
	case "critical":
		return 5
	default:
		return 0
	}
}
