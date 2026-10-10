package anomaly

import (
	"fmt"
	"sort"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/sitemodel"
)

// RuleSummary is one rule a rule set applies, described for an operator.
//
// It exists so the configurator's artifact can render the rule table the
// autonomy layer will actually run, rather than a second derivation that says
// something similar. The two had drifted — the artifact said "loiter low,
// tailgate medium, flow low" while the engine ran medium/high/high, so an
// operator reading the artifact was reading rules that did not exist.
type RuleSummary struct {
	ZoneID      string `json:"zone_id"`
	Type        string `json:"type"`
	Threshold   string `json:"threshold"`
	MinSeverity string `json:"min_severity"`
}

// Describe summarises the rules a rule set applies. It reads each rule's own
// Describe(), which is the rule's own account of itself: a threshold the rule
// holds and a severity the rule emits, not a table someone maintains beside
// it.
func Describe(rs *RuleSet) []RuleSummary {
	if rs == nil {
		return nil
	}
	out := make([]RuleSummary, 0, len(rs.rules))
	for _, r := range rs.rules {
		out = append(out, r.Describe())
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ZoneID != out[j].ZoneID {
			return out[i].ZoneID < out[j].ZoneID
		}
		return out[i].Type < out[j].Type
	})
	return out
}

// DescribeSite builds the default rule set for a site and describes it.
func DescribeSite(site *sitemodel.Site) []RuleSummary {
	if site == nil {
		return nil
	}
	return Describe(DefaultRuleSet(site))
}

// formatThreshold renders a duration the way an operator reads it.
func formatThreshold(d time.Duration) string {
	if d <= 0 {
		return "–"
	}
	if d < time.Second {
		return fmt.Sprintf("%dpx / %s", int(d/time.Microsecond), d.String())
	}
	return d.String()
}
