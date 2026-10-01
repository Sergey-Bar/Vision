package novelty

import "testing"

// TestAConstructedMovingBlockMovesTheBar is the fixture-sizing question asked of
// the ENGINE rather than of a video file.
//
// A QA lab needs material the detector can actually fire on, and whether a fixture
// can clear the bar cannot be told from a report: a run over a clip that is too small
// to move the number prints zero detections, zero errors and exit status 0, which is
// indistinguishable from a pass. `cmd/fixtureprobe` asks the same question of a real
// file; this asks it of the arithmetic, with no codec, no container and no filter
// graph in between.
//
// The arithmetic, measured rather than assumed: a full-height object W px wide
// travelling D px per frame changes a FRACTION D/W of the frame between consecutive
// frames. It is D/W and not 2*D/W because `scoreMask` compares the residual
// SIGNED — only the LEADING edge, where the scene got brighter, counts. An object
// that only darkens a region is invisible to this engine, and that asymmetry is
// worth knowing before a detector is pointed at a dark car on a bright road.
func TestAConstructedMovingBlockMovesTheBar(t *testing.T) {
	const w, h = 1280, 720
	const objW, objD = 420, 60

	e, err := New(Defaults())
	if err != nil {
		t.Fatal(err)
	}
	// Semi-transparent: the whole object dims and brightens each frame, so the
	// changed component is the OBJECT rather than its leading edge.
	frameAt := func(x0 int) []byte {
		buf := make([]byte, w*h*3)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				i := (y*w + x) * 3
				v := byte(25)
				if x >= x0 && x < x0+objW {
					v = 93
				}
				buf[i], buf[i+1], buf[i+2] = v, v, v
			}
		}
		return buf
	}

	if _, sc := e.Candidates(frameAt(-objW), w, h); sc.Reason != ReasonFirstFrame {
		t.Fatalf("frame 0 reason = %q; the first frame has nothing to compare against", sc.Reason)
	}
	cands, sc := e.Candidates(frameAt(-objW+objD), w, h)
	t.Logf("moved %d px: score %.4f over %d cells, %d candidate(s), reason %q",
		objD, sc.Value, sc.Cells, len(cands), sc.Reason)

	if sc.Value <= 0 {
		t.Fatalf("a full-height block moving %d px changed nothing at all; either the frames are not what this "+
			"test thinks they are or the engine is not measuring them", objD)
	}
	// The geometry predicts D/W. Assert it, because the whole fixture-sizing
	// argument rests on this number and it had never been checked against the code.
	want := float64(objD) / float64(w)
	if sc.Value < want*0.5 || sc.Value > want*1.5 {
		t.Errorf("score %.4f but D/W predicts %.4f; the fixture geometry assumes the second number", sc.Value, want)
	}
	if len(cands) == 0 {
		t.Fatalf("the engine produced a score and no region; a score nobody can act on is not a detection")
	}
}
