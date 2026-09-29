package rules

import (
	"fmt"
	"strings"
)

// semantic runs the checks a JSON Schema cannot express.
//
// Every problem here is a bundle that loads cleanly and cannot work: a rule
// referencing a zone that does not exist, a window that can never close, two
// rules that contradict each other at the same priority. A schema validates
// shape; this validates meaning.
func (c *Compiler) semantic(b *Bundle) []Problem {
	var problems []Problem

	if b.APIVersion != SchemaVersion {
		problems = append(problems, Problem{
			Severity: SeverityError,
			Code:     "api_version",
			Message: fmt.Sprintf("apiVersion is %q, this build compiles %q. A bundle written for "+
				"another version may use a construct this compiler would misread",
				b.APIVersion, SchemaVersion),
		})
	}
	if b.Kind != "RuleBundle" {
		problems = append(problems, Problem{
			Severity: SeverityError,
			Code:     "kind",
			Message:  fmt.Sprintf("kind is %q, want RuleBundle", b.Kind),
		})
	}
	if b.Version == "" {
		problems = append(problems, Problem{
			Severity: SeverityError, Code: "version", Message: "bundle version is required",
		})
	}
	if b.Site.SiteID == "" {
		problems = append(problems, Problem{
			Severity: SeverityError, Code: "site", Message: "site.siteId is required",
		})
	}
	if len(b.Site.Timezones) == 0 {
		// Rendering and schedule evaluation both need it, and a host's timezone
		// is never the right answer for a site.
		problems = append(problems, Problem{
			Severity: SeverityError,
			Code:     "site_timezones",
			Message:  "site.timezones must name at least one IANA zone; the host's timezone is never the site's",
		})
	}

	problems = append(problems, c.checkSettings(b)...)
	problems = append(problems, c.checkRules(b)...)

	compiled := compileBundle(b)
	for _, conflict := range compiled.Conflicts {
		problems = append(problems, Problem{
			Severity: SeverityError,
			Code:     "conflict",
			RuleID:   strings.Join(conflict.Rules, " + "),
			Message: fmt.Sprintf("these rules share priority %d and %s. A same-priority "+
				"incompatible pair is a configuration error; raise one priority or make the "+
				"outcomes agree",
				conflict.Priority, conflict.Reason),
		})
	}
	return problems
}

func (c *Compiler) checkSettings(b *Bundle) []Problem {
	var problems []Problem
	s := b.Settings.Clock

	if s.ReferenceSource != "ntp" && s.ReferenceSource != "vms" && s.ReferenceSource != "host" {
		problems = append(problems, Problem{
			Severity: SeverityError,
			Code:     "clock_reference",
			Message: fmt.Sprintf("settings.clock.referenceSource is %q, want ntp, vms or host",
				s.ReferenceSource),
		})
	}
	if s.DriftSevereMS <= s.DriftWarnMS {
		problems = append(problems, Problem{
			Severity: SeverityError,
			Code:     "clock_bands",
			Message: fmt.Sprintf("settings.clock.driftSevere (%d) must exceed driftWarn (%d); "+
				"otherwise a device in the severe band is indistinguishable from one in warn",
				s.DriftSevereMS, s.DriftWarnMS),
		})
	}
	if s.SkewToleranceMS <= 0 {
		problems = append(problems, Problem{
			Severity: SeverityError,
			Code:     "clock_skew",
			Message:  "settings.clock.skewTolerance must be positive; zero would make no two events ever simultaneous",
		})
	}
	if b.Settings.BurstMerge.WindowSeconds > 0 && b.Settings.BurstMerge.MaxEvents <= 0 {
		problems = append(problems, Problem{
			Severity: SeverityError,
			Code:     "burst_merge",
			Message:  "settings.burstMerge.maxEvents must be positive when a window is set",
		})
	}
	return problems
}

func (c *Compiler) checkRules(b *Bundle) []Problem {
	var problems []Problem
	seen := map[string]int{}

	for i, r := range b.Rules {
		add := func(code, msg string) {
			problems = append(problems, Problem{
				Severity: SeverityError, Code: code, RuleID: r.ID, Message: msg,
			})
		}
		if r.ID == "" {
			add("rule_id", fmt.Sprintf("rule %d has no id", i))
		} else if prior, dup := seen[r.ID]; dup {
			// Two rules with one id make the simulate diff ambiguous and the
			// explanation unattributable.
			add("duplicate_rule_id", fmt.Sprintf("rule id is also used at index %d; every rule id must be unique", prior))
		} else {
			seen[r.ID] = i
		}

		if r.Priority < 0 {
			add("priority", "priority cannot be negative")
		}

		problems = append(problems, c.checkAction(r)...)

		// hasCorroboration is a fact about the whole rule, not about one
		// condition: a timeWindow bounds which signals a corroboration clause
		// counts, so a rule with no corroboration clause anywhere has a window
		// that bounds nothing.
		hasCorroboration := ruleHasCorroboration(r.When)
		problems = append(problems, c.checkCondition(r.ID, r.When, "when", hasCorroboration)...)
	}
	return problems
}

// ruleHasCorroboration reports whether the rule requires corroboration anywhere.
func ruleHasCorroboration(cond Condition) bool {
	if cond.Corroborate != nil {
		return true
	}
	for _, child := range cond.All {
		if ruleHasCorroboration(child) {
			return true
		}
	}
	for _, child := range cond.Any {
		if ruleHasCorroboration(child) {
			return true
		}
	}
	if cond.Not != nil && ruleHasCorroboration(*cond.Not) {
		return true
	}
	return false
}

func (c *Compiler) checkAction(r Rule) []Problem {
	var problems []Problem
	t := r.Then

	if !t.CreateIncident && !t.Suppress && !t.MarkDuplicate && !t.Escalate && len(t.Dispatch) == 0 {
		// A rule that does nothing is a rule an operator believes is running.
		problems = append(problems, Problem{
			Severity: SeverityError, Code: "empty_action", RuleID: r.ID,
			Message: "the rule's action does nothing: it neither creates, suppresses, " +
				"escalates, marks a duplicate, nor dispatches anything",
		})
	}
	if t.CreateIncident {
		if t.Severity != "" && !Severities[t.Severity] {
			problems = append(problems, Problem{
				Severity: SeverityError, Code: "severity", RuleID: r.ID,
				Message: fmt.Sprintf("severity %q is not one of info|low|medium|high|critical", t.Severity),
			})
		}
		if t.Suppress {
			// The engine resolves this by treating suppression as dominant, but a
			// rule that says both is a mistake worth naming.
			problems = append(problems, Problem{
				Severity: SeverityError, Code: "contradictory_action", RuleID: r.ID,
				Message: "the rule both creates an incident and suppresses it",
			})
		}
	}
	if t.Suppress && t.MarkDuplicate {
		problems = append(problems, Problem{
			Severity: SeverityError, Code: "contradictory_action", RuleID: r.ID,
			Message: "the rule both suppresses the event and marks it a duplicate",
		})
	}
	if t.Escalate && t.Suppress {
		problems = append(problems, Problem{
			Severity: SeverityError, Code: "contradictory_action", RuleID: r.ID,
			Message: "the rule both escalates and suppresses",
		})
	}
	seen := map[string]bool{}
	for _, d := range t.Dispatch {
		if !DispatchTypes[d.Type] {
			problems = append(problems, Problem{
				Severity: SeverityError, Code: "dispatch_type", RuleID: r.ID,
				Message: fmt.Sprintf("dispatch type %q is not recognised; the closed set is %s",
					d.Type, strings.Join(sortedKeys(DispatchTypes), "|")),
			})
		}
		if d.Target == "" {
			problems = append(problems, Problem{
				Severity: SeverityError, Code: "dispatch_target", RuleID: r.ID,
				Message: fmt.Sprintf("dispatch %q has no target", d.Type),
			})
		}
		key := d.Type + "->" + d.Target
		if seen[key] {
			problems = append(problems, Problem{
				Severity: SeverityWarning, Code: "duplicate_dispatch", RuleID: r.ID,
				Message: fmt.Sprintf("dispatch %s appears more than once", key),
			})
		}
		seen[key] = true
	}
	return problems
}

func (c *Compiler) checkCondition(ruleID string, cond Condition, path string, hasCorroboration bool) []Problem {
	var problems []Problem

	switch {
	case cond.All == nil && cond.Any == nil && cond.Not == nil && cond.EventType == nil &&
		cond.Device == nil && cond.Zone == nil && cond.Schedule == nil &&
		cond.TimeWindow == nil && cond.Corroborate == nil && cond.Field == nil &&
		cond.ClockDrift == nil && cond.AIObservation == nil:
		problems = append(problems, Problem{
			Severity: SeverityError, Code: "empty_condition", RuleID: ruleID,
			Message: fmt.Sprintf("%s is empty, so the rule can never match", path),
		})
		return problems
	case cond.All != nil && cond.Any != nil:
		problems = append(problems, Problem{
			Severity: SeverityError, Code: "ambiguous_condition", RuleID: ruleID,
			Message: fmt.Sprintf("%s has both all and any; combine them explicitly", path),
		})
	case cond.Not != nil && (cond.All != nil || cond.Any != nil):
		problems = append(problems, Problem{
			Severity: SeverityError, Code: "ambiguous_condition", RuleID: ruleID,
			Message: fmt.Sprintf("%s wraps all/any in not; express the negation inside", path),
		})
	}

	if cond.All != nil {
		for i, child := range cond.All {
			problems = append(problems, c.checkCondition(ruleID, child, fmt.Sprintf("%s.all[%d]", path, i), hasCorroboration)...)
		}
	}
	if cond.Any != nil {
		for i, child := range cond.Any {
			problems = append(problems, c.checkCondition(ruleID, child, fmt.Sprintf("%s.any[%d]", path, i), hasCorroboration)...)
		}
	}
	if cond.Not != nil {
		problems = append(problems, c.checkCondition(ruleID, *cond.Not, path+".not", hasCorroboration)...)
	}

	for _, t := range cond.EventType {
		if !knownEventType(t) {
			problems = append(problems, Problem{
				Severity: SeverityError, Code: "unknown_event_type", RuleID: ruleID,
				Message: fmt.Sprintf("%s.eventType names %q, which this build does not produce; "+
					"a rule that can never match is a rule an operator believes is running", path, t),
			})
		}
	}

	if cond.Device != nil && cond.Device.Kind != "" && !DeviceKinds[cond.Device.Kind] {
		problems = append(problems, Problem{
			Severity: SeverityError, Code: "unknown_device_kind", RuleID: ruleID,
			Message: fmt.Sprintf("%s.device.kind is %q, which is not a known device kind", path, cond.Device.Kind),
		})
	}

	if cond.Zone != nil {
		switch {
		case cond.Zone.ID == "" && cond.Zone.AncestorOf == "" && cond.Zone.PathPrefix == "":
			problems = append(problems, Problem{
				Severity: SeverityError, Code: "empty_zone_match", RuleID: ruleID,
				Message: fmt.Sprintf("%s.zone matches nothing; set id, ancestorOf or pathPrefix", path),
			})
		case cond.Zone.ID != "" && cond.Zone.AncestorOf != "":
			problems = append(problems, Problem{
				Severity: SeverityError, Code: "ambiguous_zone_match", RuleID: ruleID,
				Message: fmt.Sprintf("%s.zone sets both id and ancestorOf", path),
			})
		case cond.Zone.ID != "" && len(c.KnownZones) > 0 && !c.KnownZones[cond.Zone.ID]:
			// Only checked when the compiler was given the site's zones. Without
			// that knowledge the check would be a guess.
			problems = append(problems, Problem{
				Severity: SeverityError, Code: "unknown_zone", RuleID: ruleID,
				Message: fmt.Sprintf("%s.zone.id names zone %q, which does not exist on this site", path, cond.Zone.ID),
			})
		}
	}

	if cond.Schedule != nil {
		if cond.Schedule.ID == "" {
			problems = append(problems, Problem{
				Severity: SeverityError, Code: "schedule_id", RuleID: ruleID,
				Message: fmt.Sprintf("%s.schedule.id is required", path),
			})
		} else if len(c.KnownSchedules) > 0 && !c.KnownSchedules[cond.Schedule.ID] {
			problems = append(problems, Problem{
				Severity: SeverityError, Code: "unknown_schedule", RuleID: ruleID,
				Message: fmt.Sprintf("%s.schedule.id names schedule %q, which does not exist on this site",
					path, cond.Schedule.ID),
			})
		}
	}

	if cond.TimeWindow != nil {
		if cond.TimeWindow.Field != "normalized_at" {
			// The plan is explicit that a time-window predicate may read
			// normalized_at and nothing else. observed_at is the source's claim
			// and received_at is ours; neither is a stable basis for correlation.
			problems = append(problems, Problem{
				Severity: SeverityError, Code: "time_window_field", RuleID: ruleID,
				Message: fmt.Sprintf("%s.timeWindow.field is %q, and only normalized_at may be read by a "+
					"time-window predicate; observed_at is the source's claim and received_at is ours",
					path, cond.TimeWindow.Field),
			})
		}
		if cond.TimeWindow.DurationSeconds < 0 {
			problems = append(problems, Problem{
				Severity: SeverityError, Code: "time_window_duration", RuleID: ruleID,
				Message: fmt.Sprintf("%s.timeWindow.durationSeconds cannot be negative", path),
			})
		}
		if d := cond.TimeWindow.Direction; d != "" && d != "before" && d != "after" && d != "either" {
			problems = append(problems, Problem{
				Severity: SeverityError, Code: "time_window_direction", RuleID: ruleID,
				Message: fmt.Sprintf("%s.timeWindow.direction is %q, want before, after or either", path, d),
			})
		}
		if !hasCorroboration {
			// A window on a rule with no corroboration requirement is dead
			// configuration: nothing reads it, because the only thing a window
			// bounds is which signals a corroboration clause will count. The check
			// is whether the rule requires corroboration at all, which is what
			// the message says; it is deliberately not whether the rule is
			// enabled, because a disabled rule's window is equally unread and
			// equally worth telling the author about.
			problems = append(problems, Problem{
				Severity: SeverityWarning, Code: "unused_time_window", RuleID: ruleID,
				Message: fmt.Sprintf("%s.timeWindow has no corroborate clause to bound", path),
			})
		}
	}

	if cond.Field != nil {
		if !validFieldPath(cond.Field.Path) {
			problems = append(problems, Problem{
				Severity: SeverityError, Code: "field_path", RuleID: ruleID,
				Message: fmt.Sprintf("%s.field.path %q is not a dotted identifier of at most %d segments",
					path, cond.Field.Path, maxFieldPathDepth),
			})
		}
		if !FieldOperators[cond.Field.Op] {
			problems = append(problems, Problem{
				Severity: SeverityError, Code: "field_operator", RuleID: ruleID,
				Message: fmt.Sprintf("%s.field.op is %q, which is not one of %s. There is deliberately "+
					"no regular expression: an unbounded pattern in an uploaded bundle is a denial of "+
					"service delivered as a YAML file",
					path, cond.Field.Op, strings.Join(sortedKeys(FieldOperators), "|")),
			})
		}
		if cond.Field.Op == "contains_prefix" {
			// HasPrefix(s, "") is true for every s, so an empty prefix makes the
			// condition match every present string: a rule the author believes
			// narrows to one prefix and in fact filters on nothing but presence.
			// Refused here as well as at evaluation, and the two are not
			// redundant — a CompiledBundle can be built without the compiler.
			prefix, ok := cond.Field.Value.(string)
			switch {
			case !ok:
				problems = append(problems, Problem{
					Severity: SeverityError, Code: "field_value", RuleID: ruleID,
					Message: fmt.Sprintf("%s.field.op %q needs a string value to match a prefix of",
						path, cond.Field.Op),
				})
			case prefix == "":
				problems = append(problems, Problem{
					Severity: SeverityError, Code: "field_value_empty_prefix", RuleID: ruleID,
					Message: fmt.Sprintf("%s.field.op %q with an empty value matches every string, "+
						"so the condition filters on presence only", path, cond.Field.Op),
				})
			}
		}
		if cond.Field.Op == "in" || cond.Field.Op == "not_in" {
			values, ok := cond.Field.Value.([]any)
			if !ok || len(values) == 0 {
				problems = append(problems, Problem{
					Severity: SeverityError, Code: "field_value", RuleID: ruleID,
					Message: fmt.Sprintf("%s.field.op %q needs a non-empty list of values", path, cond.Field.Op),
				})
			} else if len(values) > 256 {
				// A bounded list. An unbounded one is the same denial of service
				// as an unbounded pattern, wearing a different hat.
				problems = append(problems, Problem{
					Severity: SeverityError, Code: "field_value_too_large", RuleID: ruleID,
					Message: fmt.Sprintf("%s.field has %d values, at most 256 are allowed", path, len(values)),
				})
			}
		}
	}

	if cond.ClockDrift != nil && cond.ClockDrift.MaxMS < 0 {
		problems = append(problems, Problem{
			Severity: SeverityError, Code: "clock_drift", RuleID: ruleID,
			Message: fmt.Sprintf("%s.clockDrift.maxMs cannot be negative", path),
		})
	}

	problems = append(problems, c.checkCorroboration(ruleID, cond.Corroborate, path)...)
	problems = append(problems, c.checkAIObservation(ruleID, cond.AIObservation, path)...)
	return problems
}

func (c *Compiler) checkCorroboration(ruleID string, corr *Corroboration, path string) []Problem {
	if corr == nil {
		return nil
	}
	var problems []Problem

	if len(corr.EventTypes) == 0 {
		problems = append(problems, Problem{
			Severity: SeverityError, Code: "corroborate_types", RuleID: ruleID,
			Message: fmt.Sprintf("%s.corroborate.eventTypes is empty, so nothing can corroborate anything", path),
		})
	}
	for _, t := range corr.EventTypes {
		if !knownEventType(t) {
			problems = append(problems, Problem{
				Severity: SeverityError, Code: "corroborate_type", RuleID: ruleID,
				Message: fmt.Sprintf("%s.corroborate.eventTypes names %q, which this build does not produce",
					path, t),
			})
		}
	}
	if corr.MinSignals <= 0 {
		problems = append(problems, Problem{
			Severity: SeverityError, Code: "corroborate_min_signals", RuleID: ruleID,
			Message: fmt.Sprintf("%s.corroborate.minSignals must be at least 1", path),
		})
	}
	if corr.MinEvidenceTier != "" && corr.MinEvidenceTier != TierA &&
		corr.MinEvidenceTier != TierB && corr.MinEvidenceTier != TierC {
		problems = append(problems, Problem{
			Severity: SeverityError, Code: "corroborate_tier", RuleID: ruleID,
			Message: fmt.Sprintf("%s.corroborate.minEvidenceTier is %q, want A, B or C",
				path, corr.MinEvidenceTier),
		})
	}

	consumesAI := corroborationConsumesAI(corr)
	if consumesAI && corr.IfAIAvailable == nil {
		problems = append(problems, Problem{
			Severity: SeverityError, Code: "if_ai_unavailable_missing", RuleID: ruleID,
			Message: fmt.Sprintf("%s.corroborate consumes an AI observation but declares no "+
				"ifAIAvailable. A missing AI observation is never a negative observation, so the "+
				"rule must say whether it degrades to UNVERIFIED or proceeds without",
				path),
		})
	}
	// The invariant the plan states most forcefully: a rule that consumes a Tier C
	// signal must declare what happens without one. There is no default here,
	// deliberately — defaulting would make the decision on the operator's behalf
	// at evaluation time, on a site that has never thought about it. The test for
	// "consumes a Tier C signal" lives in corroborationConsumesAI so the engine
	// gates degradation on exactly the same clause this check gates the fallback
	// on.
	if !consumesAI && corr.IfAIAvailable != nil {
		problems = append(problems, Problem{
			Severity: SeverityWarning, Code: "if_ai_unavailable_unused", RuleID: ruleID,
			Message: fmt.Sprintf("%s.corroborate declares ifAIAvailable but does not consume an AI "+
				"observation; the declaration has no effect", path),
		})
	}
	if corr.IfAIAvailable != nil {
		d := corr.IfAIAvailable.DegradeTo
		if d != DegradeToUnverified && d != DegradeToProceedWithout {
			problems = append(problems, Problem{
				Severity: SeverityError, Code: "degrade_to", RuleID: ruleID,
				Message: fmt.Sprintf("%s.corroborate.ifAIAvailable.degradeTo is %q, want UNVERIFIED or "+
					"proceed_without", path, d),
			})
		}
		if d == DegradeToProceedWithout {
			// Not forbidden — an operator may genuinely want it — but it means an
			// incident is created on less evidence whenever the model is down, and
			// that should be a conscious choice.
			problems = append(problems, Problem{
				Severity: SeverityWarning, Code: "degrade_proceeds", RuleID: ruleID,
				Message: fmt.Sprintf("%s.corroborate degrades to proceed_without, so this rule creates "+
					"incidents on less evidence whenever the AI plane is unavailable; incidents raised "+
					"that way are flagged in the console and the audit chain", path),
			})
		}
	}
	return problems
}

func (c *Compiler) checkAIObservation(ruleID string, cond *AIObservationCondition, path string) []Problem {
	var problems []Problem
	if cond == nil {
		return nil
	}
	if cond.ProfileID == "" {
		problems = append(problems, Problem{
			Severity: SeverityError, Code: "ai_profile", RuleID: ruleID,
			Message: fmt.Sprintf("%s.aiObservation.profileId is required", path),
		})
		return problems
	}
	if cond.Field == "" {
		problems = append(problems, Problem{
			Severity: SeverityError, Code: "ai_field", RuleID: ruleID,
			Message: fmt.Sprintf("%s.aiObservation.field is required. Rules read validated structured "+
				"fields, never model prose", path),
		})
		return problems
	}

	fields, known := c.KnownAIFields[cond.ProfileID]
	if !known {
		// Only checked when the compiler was given the profile set. Without it the
		// check would be a guess about a bundle that may be perfectly valid on a
		// site that has the profile installed.
		problems = append(problems, Problem{
			Severity: SeverityWarning, Code: "ai_profile_unknown", RuleID: ruleID,
			Message: fmt.Sprintf("%s.aiObservation names profile %q, which this build has no schema for; "+
				"the field was not checked", path, cond.ProfileID),
		})
		return problems
	}
	if !fields[cond.Field] {
		problems = append(problems, Problem{
			Severity: SeverityError, Code: "ai_field_unknown", RuleID: ruleID,
			Message: fmt.Sprintf("%s.aiObservation.field %q is not a field of profile %q; the profile "+
				"emits %s", path, cond.Field, cond.ProfileID, strings.Join(sortedKeys(fields), "|")),
		})
	}
	return problems
}
