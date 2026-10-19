package aistate_test

import (
	"testing"

	"github.com/Sergey-Bar/vision/internal/aistate"
)

// The vocabulary is five words, and the whole product agrees on them.
//
// It is a leaf that imports nothing, and this test is why that is safe: three
// packages name these states and none of them can import the others, so the
// words have to be right in one place.
func TestTheVocabularyIsFiveWords(t *testing.T) {
	all := aistate.All
	if len(all) != 5 {
		t.Fatalf("the vocabulary holds %d words, want five: %v", len(all), all)
	}
	seen := map[string]bool{}
	for _, s := range all {
		if s == "" {
			t.Error("the vocabulary holds an empty word")
		}
		if seen[s] {
			t.Errorf("the vocabulary holds %q twice", s)
		}
		seen[s] = true
	}
}

// The three blindness states are the ones that are never a negative answer.
//
// This is the invariant the whole product rests on: a missing observation is
// not a negative observation, and a rule that reads an absence as "nothing
// there" invents a fact.
func TestTheBlindnessStatesAreTheAbsences(t *testing.T) {
	blind := aistate.BlindnessStates
	if len(blind) != 3 {
		t.Fatalf("there are %d blindness states, want three: %v", len(blind), blind)
	}
	for _, s := range blind {
		if aistate.IsAnswered(s) {
			t.Errorf("%q is an absence and an answer at once", s)
		}
		if !aistate.IsBlindness(s) {
			t.Errorf("%q is in the blindness list and is not one", s)
		}
	}
	if !aistate.IsBlindness(aistate.Degraded) {
		t.Error("a degraded answer is not a blindness state")
	}
	if aistate.IsBlindness(aistate.Asked) {
		t.Error("an asked state is a blindness state, and it is the one that is not")
	}
}

// An unknown word is not a state, and it is not an answer.
//
// The check exists because a caller that passed a typo would otherwise be
// told it had asked a question, and the whole point of the blindness
// vocabulary is that an unrecognised absence is a refusal.
func TestAnUnknownWordIsNeither(t *testing.T) {
	if aistate.Valid("not-a-state") {
		t.Error("an unknown word is a valid state")
	}
	if aistate.IsAnswered("not-a-state") {
		t.Error("an unknown word is an answer")
	}
	if aistate.IsBlindness("not-a-state") {
		t.Error("an unknown word is an absence")
	}
}
