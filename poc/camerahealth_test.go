package main

import (
	"testing"
	"time"
)

// frame builds a synthetic rgb24 frame of the POC's geometry, filled by fill.
func testFrame(fill func(x, y int) [3]byte) []byte {
	px := make([]byte, frameW*frameH*3)
	for y := 0; y < frameH; y++ {
		for x := 0; x < frameW; x++ {
			c := fill(x, y)
			i := (y*frameW + x) * 3
			px[i], px[i+1], px[i+2] = c[0], c[1], c[2]
		}
	}
	return px
}

// sceneFrame is a plausible scene: a gradient sky over dark ground, so the
// blocks differ from each other the way any real view does.
func sceneFrame() []byte {
	return testFrame(func(x, y int) [3]byte {
		if y < frameH/2 {
			return [3]byte{byte(120 + x/32), byte(140 + x/64), byte(200)}
		}
		return [3]byte{byte(20 + x/128), byte(20 + y/64), byte(20)}
	})
}

func TestDigestFrameDistinguishesStructureFromUniformity(t *testing.T) {
	scene := digestFrame(sceneFrame(), frameW, frameH)
	if scene.blockStd() < nearUniformStd {
		t.Fatalf("structured scene reads blockStd %v, want >= %v", scene.blockStd(), nearUniformStd)
	}

	// A covered lens: every block the same near-black value.
	black := digestFrame(testFrame(func(_, _ int) [3]byte { return [3]byte{4, 4, 4} }), frameW, frameH)
	if black.blockStd() >= nearUniformStd {
		t.Errorf("uniform black frame reads blockStd %v, want < %v", black.blockStd(), nearUniformStd)
	}

	// A white-out is just as blind as a blackout.
	white := digestFrame(testFrame(func(_, _ int) [3]byte { return [3]byte{250, 250, 250} }), frameW, frameH)
	if white.blockStd() >= nearUniformStd {
		t.Errorf("uniform white frame reads blockStd %v, want < %v", white.blockStd(), nearUniformStd)
	}
}

func TestDigestFrameOfWrongGeometryIsUniform(t *testing.T) {
	// Bytes that are not a frame digest to zero, which reads as near-uniform
	// rather than as a scene — the honest answer for input we cannot read. The
	// geometry does not match the bytes either, which is the other half of the
	// same answer.
	d := digestFrame([]byte{1, 2, 3}, frameW+8, frameH+4)
	if d.blockStd() >= nearUniformStd {
		t.Errorf("short buffer reads blockStd %v, want < %v", d.blockStd(), nearUniformStd)
	}
}

func TestBlindReasonNearUniformAndGlobalChange(t *testing.T) {
	scene := sceneFrame()

	// First frame of a capture is never judged: there is nothing to compare.
	if blind, _ := blindReason(frameDigest{}, digestFrame(scene, frameW, frameH), false); blind {
		t.Error("first frame of a capture was judged blind; there is no previous frame to compare")
	}

	// Lens covered: the previous frame was a scene, this one is not.
	black := digestFrame(testFrame(func(_, _ int) [3]byte { return [3]byte{3, 3, 3} }), frameW, frameH)
	blind, why := blindReason(digestFrame(scene, frameW, frameH), black, true)
	if !blind {
		t.Error("a frame that lost all structure was not called blind")
	}
	if why == "" {
		t.Error("blind verdict carries no reason")
	}

	// Camera moved: the whole frame changes at once, every block.
	shifted := testFrame(func(x, y int) [3]byte {
		if y < frameH/2 {
			return [3]byte{20, 20, 20}
		}
		return [3]byte{200, 200, 210}
	})
	blind, why = blindReason(digestFrame(scene, frameW, frameH), digestFrame(shifted, frameW, frameH), true)
	if !blind {
		t.Error("a whole-frame change was not called blind")
	}
	if why == "" {
		t.Error("global-change verdict carries no reason")
	}
}

func TestBlindReasonIgnoresOrdinarySceneChange(t *testing.T) {
	// A frame where a band of blocks changed (an object, a cloud) must not
	// read as a moved camera: only one or two blocks moved, not all of them.
	prev := testFrame(func(x, y int) [3]byte { return [3]byte{60, 60, 60} })
	cur := testFrame(func(x, y int) [3]byte {
		if y > frameH*3/4 {
			return [3]byte{200, 200, 200}
		}
		return [3]byte{60, 60, 60}
	})
	blind, _ := blindReason(digestFrame(prev, frameW, frameH), digestFrame(cur, frameW, frameH), true)
	if blind {
		t.Error("a change confined to part of the frame was called blind")
	}
}

func TestCameraHealthStateMachine(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	w := healthWindows{Fresh: 3 * time.Second, Silent: 10 * time.Second}

	if got := cameraHealth(time.Time{}, false, now, w); got != camSilent {
		t.Errorf("no frame yet: state = %q, want %q", got, camSilent)
	}
	if got := cameraHealth(now.Add(-time.Second), false, now, w); got != camFresh {
		t.Errorf("a second-old frame: state = %q, want %q", got, camFresh)
	}
	if got := cameraHealth(now.Add(-5*time.Second), false, now, w); got != camStale {
		t.Errorf("a 5s-old frame with a 3s fresh window: state = %q, want %q", got, camStale)
	}
	if got := cameraHealth(now.Add(-time.Second), true, now, w); got != camBlind {
		t.Errorf("a fresh blind frame: state = %q, want %q", got, camBlind)
	}
	// Silence outranks blindness: a camera sending nothing tells you nothing
	// about its lens.
	if got := cameraHealth(now.Add(-30*time.Second), true, now, w); got != camSilent {
		t.Errorf("a blind frame gone silent: state = %q, want %q", got, camSilent)
	}
}

func TestHealthWindowsScaleWithCaptureRate(t *testing.T) {
	slow := healthWindowsFor(0.25) // one frame every 4s
	if slow.Fresh <= 4*time.Second {
		t.Errorf("0.25 fps fresh window = %v, want > 4s (a 4s gap must not read stale)", slow.Fresh)
	}
	if slow.Silent < 16*time.Second {
		t.Errorf("0.25 fps silent window = %v, want >= 16s", slow.Silent)
	}
	fast := healthWindowsFor(4)
	if fast.Fresh >= slow.Fresh || fast.Silent >= slow.Silent {
		t.Errorf("4 fps windows (%v/%v) should be tighter than 0.25 fps (%v/%v)",
			fast.Fresh, fast.Silent, slow.Fresh, slow.Silent)
	}
	if w := healthWindowsFor(0); w.Fresh <= 0 || w.Silent <= 0 {
		t.Errorf("zero fps must still yield positive windows, got %+v", w)
	}
}

// TestCameraHealthNeverClaimsFrozen is the plan's v5 correction as a test: a
// static scene is legitimately static, and the health read must not invent a
// state for it. A camera whose frames keep arriving and never change reads
// fresh, because that is all the bytes say.
func TestCameraHealthNeverClaimsFrozen(t *testing.T) {
	now := time.Now()
	w := healthWindowsFor(1)
	state := cameraHealth(now.Add(-100*time.Millisecond), false, now, w)
	if state != camFresh {
		t.Fatalf("an unchanging but live camera reads %q, want %q", state, camFresh)
	}
	if state == "frozen" {
		t.Error("frozen is deliberately not a state this package may produce")
	}
}

func TestDigestBlockStdIsZeroForEmptyDigest(t *testing.T) {
	var d frameDigest
	if got := d.blockStd(); got != 0 {
		t.Errorf("zero digest blockStd = %v, want 0", got)
	}
}
