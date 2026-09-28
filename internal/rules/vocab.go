package rules

import "sort"

// sortedKeys renders a closed vocabulary set for a diagnostic, in a stable
// order. A diagnostic that lists the options in map order is a diagnostic that
// changes every run and cannot be diffed or grepped.
func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
