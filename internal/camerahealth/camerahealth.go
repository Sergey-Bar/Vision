// Package camerahealth names the camera states and owns the one state machine
// the whole platform reads.
//
// The July 2026 control digitisation survey's number — 30% of organisations
// still checking camera health by hand — is what these states answer. A camera
// that is silent (no frames) or blind (no usable image) is a camera nobody is
// watching, and the product's job is to say so by name rather than to look
// fine.
//
// Why this package exists rather than four local vocabularies: the state
// names, the state machine and the "is this camera degraded" predicate were
// each spelled in several places, and the places did not agree — the brief
// counted `silent | blind` while coverage counted `stale | silent | blind`, so
// one shift could render "every camera reporting" in its headline and
// UNDER-OBSERVED in its coverage section. One vocabulary, one predicate, one
// state machine, and every consumer reads them here.
//
// `frozen` is deliberately NOT a state. A static scene is legitimately static,
// and calling a quiet parking lot a broken camera would be a guess; a camera
// whose frames keep arriving but never change reads fresh, because that is all
// the bytes say.
package camerahealth

import "time"

// The four states.
const (
	Fresh  = "fresh"  // frames arriving, and they look like a scene
	Stale  = "stale"  // frames are late, but the camera is not dead
	Silent = "silent" // no frame for the silent window
	Blind  = "blind"  // the frame that arrived says the camera cannot see
)

// Degraded reports whether a state means "not watching". This is THE set:
// every consumer uses it, so a stale camera is degraded for the brief, for
// coverage, for telemetry and for the duty log alike.
func Degraded(state string) bool {
	switch state {
	case Stale, Silent, Blind:
		return true
	}
	return false
}

// Live is the opposite reading, for callers that ask the positive question.
func Live(state string) bool {
	return !Degraded(state)
}

// Windows are the two durations the state machine compares a frame's age
// against. They are derived from the capture rate rather than configured, so a
// camera sampling at 4 fps is not called silent for the two seconds a 1 fps
// camera is routinely quiet.
type Windows struct {
	Fresh  time.Duration
	Silent time.Duration
}

// WindowsFor derives the fresh/silent windows from a capture rate.
func WindowsFor(fps float64) Windows {
	interval := time.Second
	if fps > 0 {
		interval = time.Duration(float64(time.Second) / fps)
	}
	fresh := time.Duration(2.5 * float64(interval))
	if fresh < 2*time.Second {
		fresh = 2 * time.Second
	}
	silent := 10 * interval
	if silent < 10*time.Second {
		silent = 10 * time.Second
	}
	return Windows{Fresh: fresh, Silent: silent}
}

// State is the state machine: how old the last frame is, and whether the frame
// that arrived said the camera could see.
//
// Silence outranks blindness — a camera sending nothing tells you nothing about
// its lens — and a blind camera that then goes silent reads silent, because
// that is the stronger statement the counters support. A camera with no frame
// yet reads silent, which is what an unstarted or just-failed camera should
// say.
func State(lastFrame time.Time, blind bool, now time.Time, w Windows) string {
	if lastFrame.IsZero() || now.Sub(lastFrame) > w.Silent {
		return Silent
	}
	if blind {
		return Blind
	}
	if now.Sub(lastFrame) > w.Fresh {
		return Stale
	}
	return Fresh
}
