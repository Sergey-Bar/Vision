package observability

// The objectives this build evaluates, as data.
//
// # Why they are derived rather than written down
//
// A list of objective names kept beside the evaluator is a list that drifts:
// an objective added to the evaluator and not to the list is an objective
// nothing checks, and the check is the whole point.
//
// So the list is READ OFF the evaluator, and the only way it can be wrong is
// if the evaluator is wrong.

// SLONames is every objective this build evaluates, in the order they are
// evaluated.
//
// The order is the evaluator's, because a report that listed them in a
// different order each run could not be diffed.
func SLONames() []string {
	out := []string{}
	for _, s := range Breaches(nil, Readings{}) {
		out = append(out, s.Name)
	}
	return out
}

// SLOTargets maps every objective to the target it is measured against.
//
// The target is a SLO's own field, and it is read from the evaluator rather
// than kept here, for the same reason the names are.
func SLOTargets() map[string]string {
	out := map[string]string{}
	for _, s := range Breaches(nil, Readings{}) {
		out[s.Name] = s.Target
	}
	return out
}
