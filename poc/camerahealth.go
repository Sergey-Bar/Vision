package main

import (
	"math"
	"time"

	"github.com/Sergey-Bar/vision/internal/camerahealth"
)

// Camera health, from counters and from the frame bytes the capture loop
// already holds. This is the ~30% of organisations that still check device
// health by hand (HiveWatch 2026): a camera that is silent or blind is a
// camera nobody is watching, and today that is found after the incident.
//
// The four states, the state machine and the degraded predicate are
// camerahealth's — one vocabulary for the whole platform, so the brief's
// headline, the coverage section, telemetry and the duty log cannot disagree
// about whether a camera is watching.
//
// `frozen` is deliberately NOT claimed. A static scene is legitimately
// static, and calling a quiet parking lot a broken camera would be a guess —
// this repo refuses guesses. A camera whose frames keep arriving but never
// change reads fresh, because that is all the bytes in hand actually say.
const (
	camFresh  = camerahealth.Fresh
	camStale  = camerahealth.Stale
	camSilent = camerahealth.Silent
	camBlind  = camerahealth.Blind
)

// The digest grid: a 4x4 grid of block mean luma is coarse enough to be
// nearly free over a 1280x720 frame with a strided sample, and fine enough
// that "every block went dark at once" and "one block has a car in it" are
// different readings.
const blockGrid = 4

// frameDigest is the cheap per-frame statistic the health read is built
// from. It is computed in the capture loop, which already decodes pixels
// for colour estimation, so it adds no second read of the stream.
//
// The coarse grid of block means is the whole statistic — the spread of the
// blocks says whether there is a scene, and the per-block differences say
// whether the whole frame changed at once. A whole-frame mean was computed
// here too and read by nothing; it is gone rather than carried.
type frameDigest struct {
	blockMeans [blockGrid * blockGrid]float64
}

// digestFrame samples px (rgb24, w by h) into a mean luma and a coarse grid
// of block means. A frame that is too small or short to be the geometry it
// claims digests to zero, which reads as near-uniform and therefore blind —
// the honest answer for bytes that are not a frame.
func digestFrame(px []byte, w, h int) frameDigest {
	var d frameDigest
	if w <= 0 || h <= 0 || len(px) < w*h*3 {
		return d
	}
	// Stride to roughly 4k samples whatever the geometry, so the cost of
	// the digest is the same on a 720p frame and a 4K one.
	rowStep := h / 64
	if rowStep < 1 {
		rowStep = 1
	}
	colStep := w / 64
	if colStep < 1 {
		colStep = 1
	}
	var blockSum [blockGrid * blockGrid]float64
	var blockN [blockGrid * blockGrid]float64
	for y := 0; y < h; y += rowStep {
		for x := 0; x < w; x += colStep {
			i := (y*w + x) * 3
			luma := 0.299*float64(px[i]) + 0.587*float64(px[i+1]) + 0.114*float64(px[i+2])
			bi := (y*blockGrid/h)*blockGrid + (x * blockGrid / w)
			blockSum[bi] += luma
			blockN[bi]++
		}
	}
	for i := range d.blockMeans {
		if blockN[i] > 0 {
			d.blockMeans[i] = blockSum[i] / blockN[i]
		}
	}
	return d
}

// blockStd is the spread of the block means: how much structure the frame
// has across its quadrants. A covered lens, a blackout and a white-out all
// read near zero; any real scene reads well above it.
func (d frameDigest) blockStd() float64 {
	var mean float64
	for _, b := range d.blockMeans {
		mean += b
	}
	mean /= float64(len(d.blockMeans))
	var sq float64
	for _, b := range d.blockMeans {
		sq += (b - mean) * (b - mean)
	}
	return math.Sqrt(sq / float64(len(d.blockMeans)))
}

const (
	// nearUniformStd is the block spread under which a frame carries no
	// scene at all. On the 0..255 luma scale, 2.0 is far below any outdoor
	// scene (sky versus ground alone is tens of units) and above the
	// rounding of a strided mean.
	nearUniformStd = 2.0
	// globalChangeShift and globalChangeFraction together mean "the whole
	// frame changed at once", which is a camera that was moved, not an
	// object that crossed it. Requiring both is what keeps a truck passing
	// the lens from reading as a blind camera.
	globalChangeShift    = 25.0
	globalChangeFraction = 0.75
	// blockShiftFloor is the per-block difference that counts as changed
	// at all.
	blockShiftFloor = 8.0
)

// blindReason reports whether the current frame says the camera cannot see,
// comparing it against the previous frame's digest: a near-uniform image
// (lens covered, blackout, white-out) or a sudden global scene change (the
// camera was knocked or rotated). The first frame of a capture is never
// judged — there is nothing to compare it to, and guessing from one frame is
// exactly what this check refuses to do.
func blindReason(prev, cur frameDigest, hasPrev bool) (bool, string) {
	if !hasPrev {
		return false, ""
	}
	if cur.blockStd() < nearUniformStd {
		return true, "near-uniform frame (lens covered, blackout or white-out)"
	}
	var shift float64
	changed := 0
	for i := range cur.blockMeans {
		d := math.Abs(cur.blockMeans[i] - prev.blockMeans[i])
		shift += d
		if d > blockShiftFloor {
			changed++
		}
	}
	shift /= float64(len(cur.blockMeans))
	if shift > globalChangeShift && float64(changed) > globalChangeFraction*float64(len(cur.blockMeans)) {
		return true, "sudden global scene change (camera moved)"
	}
	return false, ""
}

// healthWindows derives the fresh/silent windows from a capture rate.
type healthWindows = camerahealth.Windows

// healthWindowsFor derives the windows from a capture rate.
func healthWindowsFor(fps float64) healthWindows {
	return camerahealth.WindowsFor(fps)
}

// cameraHealth is the state machine: how old the last frame is, and whether
// the frame that arrived said the camera could see. Silence outranks
// blindness — a camera sending nothing tells you nothing about its lens —
// and a blind camera that then goes silent reads silent, because that is the
// stronger statement the counters support.
func cameraHealth(lastFrame time.Time, blind bool, now time.Time, w healthWindows) string {
	return camerahealth.State(lastFrame, blind, now, w)
}
