// Package nlp converts free-text rule descriptions into deterministic
// rule-bundle rule documents. It does not call the rule engine; it only
// produces YAML-ready rule text that the existing rules.Compiler can compile.
//
// The conversion is keyword-based for demo purposes: entity words map to
// event types, time phrases map to windows, and location words map to zones.
package nlp

import (
	"fmt"
	"strings"
	"time"
)

// entityToEvent maps recognized entity keywords to event types the rule
// engine knows about (see rules.KnownEventTypes).
var entityToEvent = map[string]string{
	"person":  "person",
	"people":  "person",
	"אנשים":   "person",
	"אדם":     "person",
	"vehicle": "vehicle",
	"car":     "vehicle",
	"רכב":     "vehicle",
	"מכונית":  "vehicle",
	"animal":  "animal",
	"חיה":     "animal",
	"bag":     "bag",
	"תיק":     "bag",
}

// timePhrase maps time keywords to a duration and direction.
type timePhrase struct {
	from string
	to   string
	dur  time.Duration
}

var timePhrases = []struct {
	keywords []string
	phrase   timePhrase
}{
	{[]string{"last hour", "past hour", "שעה אחרונה"}, timePhrase{from: "", to: "", dur: time.Hour}},
	{[]string{"last 24 hours", "past day", "יום אחרון", "24 שעות אחרונות"}, timePhrase{from: "", to: "", dur: 24 * time.Hour}},
	{[]string{"today", "היום"}, timePhrase{from: "", to: "", dur: 0}},
}

// zoneKeywords maps location keywords to zone path prefixes.
var zoneKeywords = map[string]string{
	"entrance": "/entrance",
	"entry":    "/entrance",
	"exit":     "/exit",
	"door":     "/door",
	"gate":     "/gate",
	"lobby":    "/lobby",
}

// CompileRuleFromText turns a free-text description into a rule document
// string in YAML form, ready to be stored as a bundle and compiled by
// rules.Compiler. It returns an error when no recognizable entity is found.
func CompileRuleFromText(text string, _ string) (string, error) {
	lower := strings.ToLower(text)

	// Detect entities.
	var entities []string
	for keyword, event := range entityToEvent {
		if strings.Contains(lower, keyword) {
			found := false
			for _, e := range entities {
				if e == event {
					found = true
					break
				}
			}
			if !found {
				entities = append(entities, event)
			}
		}
	}
	if len(entities) == 0 {
		return "", fmt.Errorf("no recognizable entity in rule text (person, vehicle, animal, bag)")
	}

	// Detect zone.
	zone := ""
	for keyword, prefix := range zoneKeywords {
		if strings.Contains(lower, keyword) {
			zone = prefix
			break
		}
	}

	// Detect time window.
	dur := time.Duration(0)
	for _, tp := range timePhrases {
		for _, kw := range tp.keywords {
			if strings.Contains(lower, kw) {
				dur = tp.phrase.dur
				break
			}
		}
		if dur > 0 {
			break
		}
	}

	// Build the rule YAML.
	var sb strings.Builder
	sb.WriteString("id: nlp-")
	sb.WriteString(strings.Join(entities, "-"))
	sb.WriteString("\n")
	sb.WriteString("description: >-\n  ")
	sb.WriteString(text)
	sb.WriteString("\n")
	sb.WriteString("enabled: true\n")
	sb.WriteString("priority: 300\n")
	sb.WriteString("tags: [nlp, free-text]\n")
	sb.WriteString("when:\n")
	sb.WriteString("  all:\n")
	for _, ent := range entities {
		sb.WriteString("    - eventType: [")
		sb.WriteString(ent)
		sb.WriteString("]\n")
	}
	if zone != "" {
		sb.WriteString("    - zone:\n")
		sb.WriteString("        pathPrefix: ")
		sb.WriteString(zone)
		sb.WriteString("\n")
	}
	if dur > 0 {
		sb.WriteString("    - timeWindow:\n")
		sb.WriteString("        field: normalized_at\n")
		fmt.Fprintf(&sb, "        durationSeconds: %d\n", int(dur.Seconds()))
		sb.WriteString("        direction: past\n")
	}
	sb.WriteString("then:\n")
	sb.WriteString("  createIncident: true\n")
	sb.WriteString("  severity: medium\n")
	sb.WriteString("  title: \"NLP rule: ")
	sb.WriteString(text)
	sb.WriteString("\"\n")
	sb.WriteString("  dispatch:\n")
	sb.WriteString("    - type: notify_operator\n")
	sb.WriteString("      target: on-call\n")

	return sb.String(), nil
}
