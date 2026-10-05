package dispatch

// The boot-time check.
//
// # Why it runs at startup and not at first fire
//
// A rule bundle is compiled and activated. If it names a dispatch target that resolves to
// no configured destination, the first notification for that rule has nowhere to go. The
// two places that can notice are:
//
//   - **First delivery.** The handler refuses, the relay retries, the message dead-letters.
//     Discovered by whoever was on call at 03:00, from a dead-letter row, with no way to
//     tell "the receiver is down" from "there is no receiver configured".
//
//   - **Startup.** The daemon refuses to come up and prints the rule id, the target and
//     the destinations it does know. Discovered by whoever installed it, while the terminal
//     is still in front of them.
//
// Startup is the right one and the second option above is the reason: a refusal at startup
// has a name and a fix attached, and a dead letter three hours later has neither. The cost
// is that a rule bundle cannot be activated with an unconfigured target even in an install
// that intends to configure it later - which is the correct trade, because activating the
// bundle is the moment the operator says they believe those notifications will happen.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Sergey-Bar/Ai-Captain/internal/rules"
)

// CheckDestinations refuses an active bundle that names a dispatch this install cannot
// perform.
//
// Two failure classes, both reported together so a reader sees the whole problem at once
// rather than fixing one and re-running:
//
//   - A dispatch TYPE this build has no executor for. That is a version skew - a bundle
//     activated against a newer core - and it is worth distinguishing from a
//     configuration gap, because the fix is an upgrade rather than a configuration file.
//
//   - A dispatch TARGET that resolves to no configured destination. That is a configuration
//     gap, and the message names the destinations the install does have, because the
//     overwhelmingly common cause is a typo or a bundle written for a different install.
//
// A bundle with no dispatch at all passes. A bundle whose dispatches are all covered passes.
// A nil destinations set with a bundle that dispatches anything fails, which is the whole
// point: an install with no destinations file and a bundle that notifies has a product
// whose one advertised side effect is dead.
func (d *Dispatcher) CheckDestinations(b *rules.CompiledBundle) error {
	if b == nil {
		return nil
	}
	dests := collectTargets(b)

	var unknownTypes, unknownTargets []string
	performs := map[string]bool{}
	for _, t := range d.ExecutorTypes() {
		performs[t] = true
	}

	for _, entry := range dests {
		if !performs[entry.dispatchType] {
			unknownTypes = append(unknownTypes,
				fmt.Sprintf("%s: %s (no executor; this build performs %s)",
					entry.ruleID, entry.dispatchType, strings.Join(d.ExecutorTypes(), ", ")))
			continue
		}
		topic, err := TopicFor(entry.dispatchType)
		if err != nil {
			unknownTypes = append(unknownTypes, fmt.Sprintf("%s: %s", entry.ruleID, err))
			continue
		}
		// Only a notify topic resolves a destination. A queued dispatch's target names
		// what to fetch, not where to send it, so checking it against the destinations
		// file would refuse every bundle that asks for evidence.
		if topic != TopicNotifyOperator && topic != TopicNotifySupervisor {
			continue
		}
		name, err := NotificationPayload{Target: entry.target}.destinationName(topic)
		if err != nil {
			unknownTargets = append(unknownTargets, fmt.Sprintf("%s: %s", entry.ruleID, err))
			continue
		}
		if _, ok := d.Dests.Lookup(name); !ok {
			unknownTargets = append(unknownTargets, fmt.Sprintf(
				"%s: dispatch %s names target %q, which is not a configured destination%s",
				entry.ruleID, entry.dispatchType, name, knownNames(d)))
		}
	}

	var problems []string
	if len(unknownTypes) > 0 {
		sort.Strings(unknownTypes)
		problems = append(problems, "the bundle asks for dispatch types this build cannot perform:\n    "+
			strings.Join(unknownTypes, "\n    ")+
			"\n  This is version skew, not a configuration gap: the bundle was activated "+
			"against a newer core than the one running.")
	}
	if len(unknownTargets) > 0 {
		sort.Strings(unknownTargets)
		problems = append(problems, "the bundle asks for notifications with no destination:\n    "+
			strings.Join(unknownTargets, "\n    ")+
			"\n  Add the destination to the destinations file and restart, or remove the "+
			"dispatch from the bundle. Refusing here is deliberate: the alternative is a "+
			"dead letter discovered at 03:00 by whoever is on call.")
	}
	if len(problems) == 0 {
		return nil
	}
	return &StartupRefusal{Problems: problems}
}

func knownNames(d *Dispatcher) string {
	if d.Dests.Empty() {
		return " (this install has no destinations at all; " +
			strings.TrimSpace(ErrNoDestinationsFile.Error()) + ")"
	}
	return " (configured: " + strings.Join(d.Dests.Names(), ", ") + ")"
}

// StartupRefusal is the daemon's reason for not starting.
//
// A type rather than a formatted error so `main.go` can print the problems as a list and
// so a test can assert on them individually - a refusal an operator cannot act on is a
// refusal they will work around by removing the rule.
type StartupRefusal struct{ Problems []string }

func (e *StartupRefusal) Error() string {
	return "dispatch: refusing to start.\n  " + strings.Join(e.Problems, "\n  ")
}

// targetRef is one `dispatch:` entry from a bundle, with the rule that declared it.
type targetRef struct {
	ruleID       string
	dispatchType string
	target       string
}

// collectTargets walks every enabled rule's dispatch list.
//
// Compiled rules rather than the raw bundle, because the compiler is what resolved `then`
// into the form the engine runs: a rule that is `enabled: false` is not going to notify
// anyone and refusing the daemon over it would make "disable a rule" impossible without
// also editing the destinations file.
func collectTargets(b *rules.CompiledBundle) []targetRef {
	var out []targetRef
	for _, r := range b.Compiled {
		if !r.Enabled {
			continue
		}
		for _, d := range r.Then.Dispatch {
			out = append(out, targetRef{ruleID: r.ID, dispatchType: d.Type, target: d.Target})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ruleID != out[j].ruleID {
			return out[i].ruleID < out[j].ruleID
		}
		return out[i].dispatchType < out[j].dispatchType
	})
	return out
}
