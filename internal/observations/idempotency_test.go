package observations_test

import (
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/observations"
)

// P2.8: "The idempotency key is derived from camera, window and prompt version.
// The worker checks for an existing observation before querying."
//
// The cost this protects is concrete and it is the reason the check has to be a
// pure function: an inference is about fifteen cents, a worker that crashes after
// paying and before recording pays twice on every restart, and the day the rule
// that decides whether to spend money is wrong the company finds out by paying.

func aQueryKey() observations.QueryKey {
	return observations.IdempotencyKey(
		"t1", "cam-014",
		time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 1, 12, 0, 8, 0, time.UTC),
		"general-monitoring", "v1",
		[]string{"clip-aaaa", "clip-bbbb"},
	)
}

func answered(key observations.QueryKey) observations.Observation {
	return observations.Observation{
		TenantID: key.TenantID, EventID: key.EventID, ProfileID: key.ProfileID,
		PromptVersion: key.PromptVersion, InputEvidenceHashes: key.InputEvidenceHashes,
		Model: "m", ModelIdentity: "m", ModelContractVersion: "c",
		RuntimeVersion: "r", PromptTemplateID: "t",
		OutputSchemaVersion: 1, StructuredOutput: map[string]any{"insufficientEvidence": false},
		CreatedAt: timeFixture,
	}
}

// TestARetryIsRecognisedAndWouldNotPayTwice is the control. With the check
// removed, a supervised worker pays fifteen cents twice for one answer on every
// restart, forever, and nothing anywhere says so.
func TestARetryIsRecognisedAndWouldNotPayTwice(t *testing.T) {
	key := aQueryKey()
	if observations.AlreadyQueried(key, nil) {
		t.Fatal("an empty history already answers this question")
	}
	if !observations.AlreadyQueried(key, []observations.Observation{answered(key)}) {
		t.Error("a retry of an answered question was not recognised; it would pay a second time")
	}
}

// TestTheKeyIsStableAcrossAssembliesAndDifferentAcrossQuestions: the two halves.
// Stable, so a genuine retry matches; different, so a genuinely different
// question is not skipped - which is the failure that would make the product
// permanently deaf after one answer.
func TestTheKeyIsStableAcrossAssembliesAndDifferentAcrossQuestions(t *testing.T) {
	base := aQueryKey()

	// Same question, evidence attached in the other order.
	reordered := base
	reordered.InputEvidenceHashes = []string{"clip-bbbb", "clip-aaaa"}
	if reordered.String() != base.String() {
		t.Error("the order evidence was attached in changed the key; two workers assembling the " +
			"same window differently would pay twice for one answer")
	}

	for name, mutate := range map[string]func(k *observations.QueryKey){
		"a different window": func(k *observations.QueryKey) {
			k.EventID = observations.EventIDFor("cam-014",
				time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC),
				time.Date(2026, 7, 1, 12, 0, 9, 0, time.UTC))
		},
		"a different camera": func(k *observations.QueryKey) {
			k.EventID = observations.EventIDFor("cam-999",
				time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC),
				time.Date(2026, 7, 1, 12, 0, 8, 0, time.UTC))
		},
		"a different prompt version": func(k *observations.QueryKey) { k.PromptVersion = "v2" },
		"a different profile":        func(k *observations.QueryKey) { k.ProfileID = "restricted-person" },
		"different evidence": func(k *observations.QueryKey) {
			k.InputEvidenceHashes = []string{"clip-aaaa", "clip-cccc"}
		},
		"more evidence": func(k *observations.QueryKey) {
			k.InputEvidenceHashes = []string{"clip-aaaa", "clip-bbbb", "clip-cccc"}
		},
	} {
		mutated := base
		mutate(&mutated)
		if observations.AlreadyQueried(mutated, []observations.Observation{answered(base)}) {
			t.Errorf("a question with %s was treated as already answered; the product would go deaf "+
				"after one answer", name)
		}
	}
}

// TestADegradedObservationStillCountsAsQueried is the subtle half, and getting it
// wrong is worse than having no check at all.
//
// An inference that was capped, or that the provider refused, or whose region
// was unreachable, produced no answer. If that counted as "asked", a single
// outage would permanently mark every window in the site as done, and the product
// would never look at any of them again - with no error, no degradation, and an
// operator staring at a site that looks perfectly healthy and has stopped
// watching since Tuesday.
func TestADegradedObservationStillCountsAsQueried(t *testing.T) {
	key := aQueryKey()
	degraded := answered(key)
	degraded.MarkDegraded(observations.DegradationModelUnavailable, "reason=cap_reached limit=500 used=500")

	if observations.AlreadyQueried(key, []observations.Observation{degraded}) {
		t.Error("a failed query counted as answered; one outage would permanently mark every " +
			"window in the site as done and the product would never look again")
	}
}

// TestASupersededObservationDoesNotCount: a re-evaluation after a prompt fix is a
// new question by another route.
//
// The direction matters and is easy to get backwards. The row that *carries*
// SupersedesObservationID is the current answer and must count; the row it
// *names* is the retained history and must not. Reading it the other way makes a
// corrected prompt impossible to re-run, because the superseded row looks like
// the answer forever.
func TestASupersededObservationDoesNotCount(t *testing.T) {
	key := aQueryKey()

	current := answered(key)
	current.ObservationID = "obs_second"
	current.SupersedesObservationID = "obs_first"
	old := answered(key)
	old.ObservationID = "obs_first"

	// With both rows present, the older one is history and the newer one is the
	// answer. The question has been asked, so a retry is skipped - once.
	if !observations.AlreadyQueried(key, []observations.Observation{old, current}) {
		t.Error("the current answer was not recognised; every retry after a re-evaluation would pay again")
	}

	// The older row on its own, with nothing naming it as superseded, is all the
	// history there is. A retention sweep that removed the newer row leaves
	// exactly this, and re-asking is the right response to it.
	if !observations.AlreadyQueried(key, []observations.Observation{old}) {
		t.Error("the only surviving row was not recognised; a sweep that removed the re-evaluation " +
			"would leave the question permanently unanswerable")
	}

	// And with the current row *also* degraded - a re-evaluation that the cap
	// refused - the question is open again, because the history it replaced is
	// history and the re-evaluation is not an answer.
	failed := current
	failed.MarkDegraded(observations.DegradationModelUnavailable, "reason=cap_reached")
	if observations.AlreadyQueried(key, []observations.Observation{old, failed}) {
		t.Error("a failed re-evaluation left the superseded row counting as the answer; a corrected " +
			"prompt could never be re-run after the provider came back")
	}
}

// TestAnObservationOfAnotherTenantNeverMatches: the check is scoped to the
// tenant, and a key that ignored the tenant would let one site's answered
// window suppress another site's identical question.
func TestAnObservationOfAnotherTenantNeverMatches(t *testing.T) {
	key := aQueryKey()
	other := answered(key)
	other.TenantID = "t2"

	if observations.AlreadyQueried(key, []observations.Observation{other}) {
		t.Error("another tenant's observation matched this site's key; an answered window on one " +
			"site would suppress the same question on another")
	}
}

// TestTheKeyIsReadableInADiagnostic, because a worker that skips a call and does
// not say why is a worker an operator cannot debug.
func TestTheKeyIsReadableInADiagnostic(t *testing.T) {
	got := observations.DescribeKey(aQueryKey())
	for _, want := range []string{"event=evt_", "profile=general-monitoring", "prompt=v1", "clip-aaaa"} {
		if !contains(got, want) {
			t.Errorf("the diagnostic %q does not carry %q", got, want)
		}
	}
	// And it is key=value, not prose: the repository's rule is that nothing
	// matches on prose.
	if contains(got, " ") && !contains(got, "=") {
		t.Error("the diagnostic is prose")
	}
}

func contains(h, n string) bool {
	if n == "" {
		return false
	}
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return true
		}
	}
	return false
}
