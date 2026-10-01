package detect

import (
	"testing"
	"time"
)

func TestTwoAbuttingStripsJoinAndTwoSeparatedOnesDoNot(t *testing.T) {
	zone := Zone{Lines: []Line{{Name: "l", A: [2]float64{0.5, 0}, B: [2]float64{0.5, 1}, Direction: "left-to-right"}}}
	// MaxAge is set, and that is the second half of the lesson. It defaults to ZERO,
	// which `associate` reads as "forget every track at the start of the next frame"
	// — so a zero MaxAge deletes the track before it can ever be matched, and no
	// association rule in the world can help. Two callers (this file's first draft
	// and `cmd/fixtureprobe`) set it and measured "the tracker never joins them",
	// which was a true statement about a configuration nothing would ever use.
	eng, err := New(zone, Config{ScoreThreshold: 0.01, Classes: []string{"m"}, IoU: 0.3, Tolerance: 0.01, MinArea: 0, TrackMinFrames: 2, MaxBoxesDrawn: 8, MaxAge: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(0, 0).UTC()

	// A 4-cell strip moving 4 cells per frame: consecutive strips share only an edge.
	strip := func(x0 float64) Box { return Box{Class: "m", Score: 0.05, X1: x0, Y1: 0, X2: x0 + 0.0625, Y2: 1} }

	if !touching(strip(0), strip(0.0625)) {
		acx, _ := strip(0).Centre()
		bcx, _ := strip(0.0625).Centre()
		t.Fatalf("touching says two abutting 0.0625-wide strips are not touching: centres %.5f and %.5f", acx, bcx)
	}
	if touching(strip(0), strip(0.2)) {
		t.Fatal("touching says two boxes with a 0.14 gap are touching")
	}
	if iou(strip(0), strip(0.0625)) >= 0.3 {
		t.Fatalf("the premise is wrong: abutting strips have IoU %.4f, so this test is not testing what it claims", iou(strip(0), strip(0.0625)))
	}

	for i, x := range []float64{0, 0.0625, 0.125} {
		_, tracks, st := eng.Feed(at.Add(time.Duration(i)*time.Second), []Box{strip(x)})
		for _, tr := range tracks {
			t.Logf("frame %d: track %d seen=%d", i, tr.ID, tr.Seen)
		}
		t.Logf("frame %d: offered %d approved %d alive %d tooNew %d tracks %d",
			i, st.Offered, st.Approved, st.TracksAlive, st.DroppedTooNew, len(tracks))
	}
	_, tracks, _ := eng.Feed(at.Add(4*time.Second), []Box{strip(0.1875)})
	if len(tracks) != 1 || tracks[0].Seen != 4 {
		t.Fatalf("abutting strips did not become one track: %+v", tracks)
	}

	// Two strips with a real gap between them must NOT join. Two frames, two
	// tracks: this engine has no notion of "an object that vanished", so an
	// unmatched box becomes a new track and the old one ages out.
	eng2, _ := New(zone, Config{ScoreThreshold: 0.01, Classes: []string{"m"}, IoU: 0.3, Tolerance: 0.01, MinArea: 0, TrackMinFrames: 2, MaxBoxesDrawn: 8, MaxAge: 2 * time.Second})
	eng2.Feed(at, []Box{strip(0)})
	_, tr2, _ := eng2.Feed(at.Add(time.Second), []Box{strip(0.2)})
	if len(tr2) != 2 {
		t.Fatalf("two separated boxes produced %d tracks; they are two objects and must not merge", len(tr2))
	}
}
