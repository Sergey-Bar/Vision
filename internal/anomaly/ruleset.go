// Package anomaly provides deterministic behavior rules for the autonomy layer.
package anomaly

import (
	"time"

	"github.com/Sergey-Bar/vision/internal/sitemodel"
)

// DefaultRuleSet builds the rules a site gets by default, from its own zones
// and routes.
//
// The severities are chosen so the score a rule feeds is honest about the zone:
// a person dwelling in a restricted area scores high enough for the playbook to
// verify it, and one in a monitoring zone does not. That is the difference
// between a demo whose autonomy badge moves and one that sits at AUTO forever.
//
// A nil site is a legitimate construction: the no-site and no-log paths in the
// POC build the hub before a site model is loaded, and those must yield an empty
// rule set rather than a nil dereference. A site with no zones and no routes
// gets the same empty set, for the same reason.
func DefaultRuleSet(site *sitemodel.Site) *RuleSet {
	rules := make([]Rule, 0)

	if site == nil {
		return newRuleSet(rules...)
	}

	for _, zone := range site.Zones {
		switch zone.Type {
		case "restricted":
			r := newDwellRule(zone.ID, 15*time.Second)
			r.MinSeverity = "high"
			rules = append(rules, r)
		case "choke":
			rules = append(rules, newDwellRule(zone.ID, 15*time.Second))
		}
	}

	for _, zone := range site.Zones {
		if zone.Type == "monitor" {
			rules = append(rules, newLoiterRule(zone.ID, 3, 60*time.Second))
		}
	}

	for _, route := range site.Routes {
		rules = append(rules, newFlowRule(route.ID))
	}

	// A choke point is where a tailgate happens, so that is where the proximity
	// rule applies.
	for _, zone := range site.Zones {
		if zone.Type == "choke" {
			rules = append(rules, newTailgateRule(zone.ID, 50.0, 5*time.Second))
		}
	}

	return newRuleSet(rules...)
}
