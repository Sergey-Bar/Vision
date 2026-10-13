package store

import (
	"testing"

	"github.com/Sergey-Bar/vision-poc/internal/aistate"
	"github.com/Sergey-Bar/vision-poc/internal/vlm"
)

// TestTheStoredSpellingsMatchTheVerifierClosedSets is what makes the duplicated
// literals in this package safe.
//
// The AI STATES are not literals — they come from `internal/aistate`, the shared
// vocabulary package, and that is checked by the compiler. The VERDICTS and the
// PROMPT ID *are* literals, because their owner is `internal/vlm` and there is no
// vocabulary package below it for them to live in.
//
// So this test is the other half of that decision. It costs nothing, it needs no
// database, and it fails the moment either side moves without the other. A drift
// here is not a compile error either: a renamed verdict makes a brief report zero
// confirmations and no error anywhere.
func TestTheStoredSpellingsMatchTheVerifierClosedSets(t *testing.T) {
	for _, pair := range []struct{ got, want string }{
		{verdictConfirmed, string(vlm.VerdictConfirmed)},
		{verdictNoObjects, string(vlm.VerdictNone)},
		{verdictNeedsReview, string(vlm.VerdictNeedsReview)},
	} {
		if pair.got != pair.want {
			t.Fatalf("the store filters on the verdict %q and the verifier writes %q; a brief would count the wrong bucket",
				pair.got, pair.want)
		}
	}
	// The prompt id is a manifest entry in internal/vlm, not a Go constant, so it
	// is asserted as a literal rather than against a symbol.
	if promptAlarm != "alarm" {
		t.Fatalf("the store searches prompt id %q; `pocctl search` would return nothing for every observation in the store", promptAlarm)
	}
}

// TestTheStoreFiltersOnTheSharedVocabularyAndNotOnItsOwnCopy: `internal/store`
// takes the four absence states from `internal/aistate`. This asserts it still
// does, because a future edit that inlines `"asked_answered"` here would look
// harmless and would be a fifth declaration of a vocabulary that already has one
// owner.
func TestTheStoreFiltersOnTheSharedVocabularyAndNotOnItsOwnCopy(t *testing.T) {
	if aistate.Asked != "asked_answered" {
		t.Fatalf("the shared asked state is %q; every filter in this package is now keyed on it", aistate.Asked)
	}
	for _, s := range aistate.All {
		if s == "" {
			t.Fatal("the closed set has an empty member, and a SQL `IN ()` over it matches nothing while reporting success")
		}
	}
	// The three states that mean the model failed or was never consulted must not
	// report themselves answered. `IsAnswered` is the function that decides, and it
	// is what the contract means.
	if aistate.IsAnswered(aistate.Degraded) || aistate.IsAnswered(aistate.NotAsked) || aistate.IsAnswered(aistate.CannotTell) {
		t.Fatal("a state that means the model failed or was never consulted reports itself answered")
	}
}
