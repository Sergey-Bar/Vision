package novelty

import "testing"

// TestAComponentsBoxIsItsOwnExtentAndNotTheFrameOrigin is the test for the bug that
// made every region this engine proposed a rectangle anchored at (0,0).
//
// `components` grew a box with `minf(c.box.X1, x)` starting from a zero-value
// `Box{X1:0, Y1:0, X2:0, Y2:0}`. A zero has X1 == 0, and `minf(0, anything)` is 0,
// so X1 and Y1 were pinned to the top-left corner of the frame for every component
// in every frame, and only X2 and Y2 moved.
//
// It was found by building a fixture and watching it fail, and it presented as three
// unrelated things:
//
//   - on an operator's screen the box was the wrong shape;
//   - consecutive boxes all shared the left edge, their IoU collapsed to zero, and
//     the tracker started a NEW track every frame — 189 approved boxes, zero
//     crossings, no error;
//   - `Box.Area()` is what `SP_POC_MIN_BOX_AREA` is compared against, so a small
//     change in the corner of the frame was reported as covering a quarter of it.
func TestAComponentsBoxIsItsOwnExtentAndNotTheFrameOrigin(t *testing.T) {
	// A 4x3 grid with a single changed cell at row 1, column 2 — deliberately not
	// at the origin, because a component at the origin looks correct by accident.
	const gw, gh = 4, 3
	m := mask{W: gw, H: gh, cells: make([]bool, gw*gh)}
	m.cells[1*gw+2] = true

	got := components(m, 1)
	if len(got) != 1 {
		t.Fatalf("components = %d, want 1", len(got))
	}
	want := Box{X1: 2.0 / gw, Y1: 1.0 / gh, X2: 3.0 / gw, Y2: 2.0 / gh}
	if got[0].box != want {
		t.Fatalf("box = %+v, want %+v. A zero-value Box has X1 == 0, so `minf(box.X1, x)` pinned X1 to the "+
			"frame origin for every component", got[0].box, want)
	}
}

// TestTwoComponentsFarApartDoNotShareABox: the other half of the same property. A
// fix that pinned X1 but let X2 run away would merge two changes into one region.
func TestTwoComponentsFarApartDoNotShareABox(t *testing.T) {
	const gw, gh = 4, 3
	m := mask{W: gw, H: gh, cells: make([]bool, gw*gh)}
	m.cells[0] = true      // (0,0)
	m.cells[2*gw+3] = true // (3,2)
	got := components(m, 1)
	if len(got) != 2 {
		t.Fatalf("components = %d, want 2", len(got))
	}
	// Largest first, ties broken by the left edge, so the order is deterministic.
	if got[0].box.X1 != 0 || got[0].box.Y2 != 1.0/gh {
		t.Errorf("the first component's box is %+v, want the (0,0) cell", got[0].box)
	}
	if got[1].box.X1 != 3.0/gw || got[1].box.Y1 != 2.0/gh {
		t.Errorf("the second component's box is %+v, want the (3,2) cell", got[1].box)
	}
}

// TestAComponentAtTheOriginStillHasItsOwnBox: the case the bug hid behind. Before
// the fix, a component touching (0,0) was indistinguishable from a correct one.
func TestAComponentAtTheOriginStillHasItsOwnBox(t *testing.T) {
	const gw, gh = 4, 3
	m := mask{W: gw, H: gh, cells: make([]bool, gw*gh)}
	m.cells[0] = true
	m.cells[1] = true
	got := components(m, 1)
	if len(got) != 1 {
		t.Fatalf("components = %d, want 1", len(got))
	}
	want := Box{X1: 0, Y1: 0, X2: 2.0 / gw, Y2: 1.0 / gh}
	if got[0].box != want {
		t.Fatalf("box = %+v, want %+v", got[0].box, want)
	}
	if got[0].box.Y2 >= 1.0 {
		t.Errorf("a component in the top row has Y2 %v; it must not reach the bottom of the frame", got[0].box.Y2)
	}
}
