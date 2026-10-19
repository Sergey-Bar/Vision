// Package aistate is the public face of the answer vocabulary.
//
// The implementation is `github.com/Sergey-Bar/vision/internal/aistate`. It is
// internal because it is a vocabulary, and a vocabulary that half the tree can
// see and half cannot is a vocabulary that gets declared twice -- and a
// vocabulary declared twice is a vocabulary that drifts, which here is not a
// compile error but an invented detection rate.
//
// The POC module counts these states, so it comes here, over the module
// boundary, as aliases of the same words.
package aistate

import "github.com/Sergey-Bar/vision/internal/aistate"

// The five states.
const (
	Asked      = aistate.Asked
	NoObjects  = aistate.NoObjects
	Degraded   = aistate.Degraded
	NotAsked   = aistate.NotAsked
	CannotTell = aistate.CannotTell
)

// The lists.
var (
	// All is every state.
	All = aistate.All
	// BlindnessStates are the states that are never a negative answer.
	BlindnessStates = aistate.BlindnessStates
)

// The functions.
var (
	// IsAnswered reports whether a state is a real answer.
	IsAnswered = aistate.IsAnswered
	// IsBlindness reports whether a state is an absence.
	IsBlindness = aistate.IsBlindness
	// Valid reports whether a state is one of the five.
	Valid = aistate.Valid
)
