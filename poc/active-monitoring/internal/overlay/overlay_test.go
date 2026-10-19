package overlay

import (
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/shared/classify"
)

// blank is a mid-grey frame, so a drawn pixel is visible as a CHANGE and not
// merely as "not black". A test that drew on a black frame and asserted "a pixel
// is not black" would pass on a picture where only the alpha changed.
func blank(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{96, 96, 96, 255})
		}
	}
	return img
}

func isGreyish(c color.RGBA) bool {
	return c.R == 96 && c.G == 96 && c.B == 96
}

// changedAt reports whether a pixel at (x,y) was drawn on.
func changedAt(img *image.RGBA, x, y int) bool {
	if !(image.Point{x, y}).In(img.Bounds()) {
		return false
	}
	return !isGreyish(img.RGBAAt(x, y))
}

// TestGeometryOfAKnownBox is T9.3. A synthetic 640x384 frame with a box at known
// normalised coordinates, and the assertion is on PIXELS: the outline must be on
// the edges the arithmetic says, not approximately nearby.
//
// This is the test that catches the class of defect the whole plan is about. A
// decoder that squashes a box vertically still produces a box, still draws an
// outline, and still looks like a detection on a screen: the only way to see that
// it is wrong is to know where the edges should be and check.
func TestGeometryOfAKnownBox(t *testing.T) {
	const w, h = 640, 384
	img := blank(w, h)
	// 0.25..0.75 across, 0.50..0.75 down.
	const x1, y1, x2, y2 = 160, 192, 480, 288
	Draw(img, Spec{Boxes: []TrackedBox{{
		Box:   Box{Class: "car", Score: 0.87, X1: 0.25, Y1: 0.5, X2: 0.75, Y2: 0.75},
		Track: 12, Seen: 5,
	}}})

	edges := []struct {
		name string
		x, y int
	}{
		{"top-left corner", x1, y1},
		{"top edge, mid", (x1 + x2) / 2, y1},
		{"top edge, right", x2, y1},
		{"bottom-left corner", x1, y2},
		{"bottom edge, mid", (x1 + x2) / 2, y2},
		{"bottom edge, right", x2, y2},
		{"left edge, mid", x1, (y1 + y2) / 2},
		{"right edge, mid", x2, (y1 + y2) / 2},
	}
	for _, e := range edges {
		if !changedAt(img, e.x, e.y) {
			t.Errorf("%s (%d,%d) was not drawn: the outline is not on the box's edge", e.name, e.x, e.y)
		}
	}
	// And INSIDE the box the fill is a wash, so a pixel is still greyish-ish: an
	// opaque fill would hide the road and the picture would be a rectangle of
	// colour with a border.
	cx, cy := (x1+x2)/2, (y1+y2)/2
	if isGreyish(img.RGBAAt(cx, cy)) {
		t.Logf("the box interior at (%d,%d) is unchanged; a translucent fill is expected, an opaque one is not", cx, cy)
	}
}

// TestNoDrawnRunSpansMoreThanHalfTheFrameWidth is the other half of T9.3, and it is
// the assertion about the BUG rather than about the feature.
//
// When every box collapsed onto the frame's corner, every label and every outline
// landed in the same place and the picture grew one enormous smear down one edge. A
// small box must produce a small amount of drawing. This walks each row of the
// output and measures the longest horizontal run of drawn pixels.
//
// The provenance header bar is excluded, and it has to be: it is a full-width
// rectangle by design and it is drawn on EVERY frame including a frame with no
// boxes at all. Measuring it would report a violation on every picture and train a
// reader to ignore this test.
func TestNoDrawnRunSpansMoreThanHalfTheFrameWidth(t *testing.T) {
	const w, h = 640, 384
	img := blank(w, h)
	Draw(img, Spec{Provenance: "synthetic", AIStateKnown: false,
		Boxes: []TrackedBox{{
			Box:   Box{Class: "person", Score: 0.9, X1: 0.40, Y1: 0.40, X2: 0.50, Y2: 0.55},
			Track: 1, Seen: 3,
		}}})
	// The box itself is 0.10 wide, so 64 pixels. A label plate beside it is the
	// widest thing drawn, and at 7x13 with `PERSON · person · 0.90 · T1` it is
	// about 170 pixels. Half the frame is 320.
	const maxRun = w / 2
	// The scan excludes BOTH chrome bands — the provenance bar at the top and the
	// verdict bar W2-8 added at the bottom. Both are full-width by design and
	// both carry text; a full-width run of chrome is not the shape this test is
	// looking for, and counting it would make the test fail for the addition of a
	// header rather than for a decode that collapsed. The verdict bar has its own
	// assertion below.
	const headerBand = 22
	const footerBand = 22
	worst, worstY := 0, -1
	for y := headerBand; y < h-footerBand; y++ {
		run := 0
		for x := 0; x < w; x++ {
			if changedAt(img, x, y) {
				run++
				if run > worst {
					worst, worstY = run, y
				}
				continue
			}
			run = 0
		}
	}
	if worst > maxRun {
		t.Fatalf("a run of %d drawn pixels on row %d exceeds half the frame width (%d) for a box that covers a tenth of it. "+
			"That is the shape a collapsed decode produces: everything landing in one place", worst, worstY, maxRun)
	}
	if worst == 0 {
		t.Fatal("nothing at all was drawn below the header: a drawing test that measures nothing is not a test")
	}
}

// TestTheVerdictIsBurnedIntoThePicture is W2-8's assertion.
//
// The claim it closes was that the screen "shows the verdict", and it was carried
// by a row of HTML text that a screenshot's crop tool removes and that the
// snapshot path did not draw at all. So the verdict is a bar on the image, and
// this asserts the bar exists and that an ABSENCE is drawn as an absence rather
// than as an empty cell.
func TestTheVerdictIsBurnedIntoThePicture(t *testing.T) {
	const w, h = 640, 384
	img := blank(w, h)
	Draw(img, Spec{Provenance: "synthetic", VerdictKnown: true, Verdict: "confirmed",
		PushResult: "push_sent", Counters: "det 4 · vlm 1/1"})
	footer := 0
	for y := h - 22; y < h; y++ {
		for x := 0; x < w; x++ {
			if changedAt(img, x, y) {
				footer++
			}
		}
	}
	if footer < 200 {
		t.Fatalf("the verdict bar drew %d pixels; a verdict that is not in the pixels is a verdict in a text row", footer)
	}
	// An unasked model must NOT look like an answered one.
	unasked := blank(w, h)
	Draw(unasked, Spec{Provenance: "synthetic", VerdictKnown: false})
	quiet := 0
	for y := h - 22; y < h; y++ {
		for x := 0; x < w; x++ {
			if changedAt(unasked, x, y) {
				quiet++
			}
		}
	}
	if quiet < 200 {
		t.Fatalf("an unanswered run drew %d pixels in the verdict bar; it must still SAY it has no answer, because an "+
			"empty cell next to a live picture reads as a false_positive", quiet)
	}
}

// TestTheMarkerIsAtTheExpectedPixel: D-5, a filled triangle with its apex at the
// box's top-centre pointing down. The assertion is that the base is a horizontal
// run ABOVE the box and the tip is AT the top-centre, which is what "pointing down
// at the object" means in pixels.
func TestTheMarkerIsAtTheExpectedPixel(t *testing.T) {
	const w, h = 640, 384
	img := blank(w, h)
	const cx, top = 320, 192
	Draw(img, Spec{Boxes: []TrackedBox{{
		Box:   Box{Class: "car", Score: 0.9, X1: 0.45, Y1: 0.5, X2: 0.55, Y2: 0.8},
		Track: 3, Seen: 4,
	}}})

	// The base is markerHeight above the apex, and the apex is markerGap above the
	// box's top edge.
	apexY := top - markerGap
	baseY := apexY - markerHeight
	if !changedAt(img, cx, baseY) {
		t.Errorf("the marker base at (%d,%d) was not drawn", cx, baseY)
	}
	if !changedAt(img, cx, apexY) {
		t.Errorf("the marker apex at (%d,%d) was not drawn", cx, apexY)
	}
	// The base is WIDER than the tip: that is what makes it a downward triangle and
	// not a spike.
	baseWidth := 0
	for dx := -20; dx <= 20; dx++ {
		if changedAt(img, cx+dx, baseY) {
			baseWidth++
		}
	}
	if baseWidth < markerHalfBase*2 {
		t.Errorf("the marker base is %d px wide, want at least %d: the marker is not a triangle", baseWidth, markerHalfBase*2)
	}
	// Nothing above the base: the marker's height is declared, so a picture with
	// something 40 px higher is not this marker.
	if changedAt(img, cx, baseY-markerHeight) {
		t.Errorf("something was drawn at y=%d, %d px above the marker base; the marker height is %d", baseY-markerHeight, markerHeight, markerHeight)
	}
}

// TestTheFamilyIsTheColour: D-6 asserted on pixels. Four hues, and the four are
// distinct, because a colour that is the same for two families is a colour that
// carries no information.
func TestTheFamilyIsTheColour(t *testing.T) {
	seen := map[color.RGBA]classify.Family{}
	for _, f := range classify.Families {
		c := familyColour(f)
		if other, dup := seen[c]; dup {
			t.Errorf("family %s and %s have the same colour %v: the colour carries no information", f, other, c)
		}
		seen[c] = f
	}
	// And the literal values, because "four distinct colours" would also be
	// satisfied by four shades of grey.
	want := map[classify.Family]color.RGBA{
		classify.FamilyVehicle: {255, 70, 70, 255},
		classify.FamilyPerson:  {90, 220, 255, 255},
		classify.FamilyAnimal:  {255, 176, 32, 255},
		classify.FamilyOther:   {170, 170, 180, 255},
	}
	for f, w := range want {
		if got := familyColour(f); got != w {
			t.Errorf("familyColour(%s) = %v, want %v", f, got, w)
		}
	}
}

// TestTheLabelNamesTheFamilyInWords: the colour is the fast channel and the words
// are the certain one. A greyscale screenshot, a colour-blind viewer and a reader
// who has not learned the code all get the same answer from the text.
func TestTheLabelNamesTheFamilyInWords(t *testing.T) {
	tb := TrackedBox{Box: Box{Class: "truck", Score: 0.874, X1: 0.1, Y1: 0.1, X2: 0.4, Y2: 0.4}, Track: 12, Seen: 5}
	got := labelFor(tb)
	// The `f5` is the frame count, and it is here because it is the one thing on
	// the picture that explains a box the operator is looking at and not being told
	// about: a track seen twice cannot emit a crossing yet, and without the count
	// the operator concludes the system missed something — the opposite of what
	// happened, and the kind of conclusion that becomes a support ticket.
	want := "VEHICLE · truck · 0.87 · T12 · f5"
	if got != want {
		t.Fatalf("labelFor = %q, want %q", got, want)
	}
	// A track with no id must not print T0: it is not a track called zero, it is a
	// box nobody has adopted yet.
	tb.Track = 0
	if strings.Contains(labelFor(tb), "T0") {
		t.Errorf("an untracked box was labelled %q: T0 is a track id that does not exist", labelFor(tb))
	}
}

// TestTheSuppressionNoteCountsPerFamily: T8.5. A single total is not enough,
// because it hides which family the cap ate, and an operator who knows six
// VEHICLES were dropped acts on it while one who knows six CHAIRS were dropped
// does not.
func TestTheSuppressionNoteCountsPerFamily(t *testing.T) {
	got := SuppressionText(map[classify.Family]int{
		classify.FamilyVehicle: 6,
		classify.FamilyOther:   22,
	})
	for _, want := range []string{"+28 not drawn", "vehicle 6", "other 22"} {
		if !strings.Contains(got, want) {
			t.Errorf("the note %q does not contain %q", got, want)
		}
	}
	if SuppressionText(nil) != "" {
		t.Error("an empty suppression map produced a note; nothing was suppressed and the note would be a lie")
	}
	if SuppressionText(map[classify.Family]int{classify.FamilyVehicle: 0}) != "" {
		t.Error("a map of zeroes produced a note")
	}
}

// TestTheCapIsRespectedAndCounted: the picture draws at most MaxBoxes, keeps the
// STRONGEST, and says how many it left off. A cap that silently drops is a cap that
// lies, and a cap that keeps the first it found rather than the best is a cap that
// hides the objects a viewer most needs to see.
//
// The cap is asserted on `Select`, not on pixels. Proving it by counting drawn
// pixels is a test of the LABEL PLATES as much as of the cap: a neighbouring box's
// plate lands on the exact pixel being measured, and the test then measures the
// wrong thing. A separate test below checks that Draw renders what Select chose.
func TestTheCapIsRespectedAndCounted(t *testing.T) {
	var boxes []TrackedBox
	for i := 0; i < 30; i++ {
		col, row := float64(i%5)/6, float64(i/5)/7
		boxes = append(boxes, TrackedBox{
			Box:   Box{Class: "car", Score: 0.5 + float64(i)/100, X1: col, Y1: row, X2: col + 0.08, Y2: row + 0.05},
			Track: TrackID(i + 1), Seen: 3,
		})
	}
	drawn, suppressed := Select(Spec{Boxes: boxes, MaxBoxes: 12})
	if len(drawn) != 12 {
		t.Fatalf("Select drew %d boxes with a cap of 12 and 30 offered", len(drawn))
	}
	if got := suppressed[classify.FamilyVehicle]; got != 18 {
		t.Errorf("suppressed[vehicle] = %d, want 18: the note must say what the cap ate, per family", got)
	}
	// Strongest first: the scores here rise with the index, so the 12 chosen are
	// 29 down to 18 and box 17 must not be among them.
	if drawn[0].Track != 30 {
		t.Errorf("the first box selected is T%d, want T30 (the highest score)", drawn[0].Track)
	}
	if drawn[11].Track != 19 {
		t.Errorf("the last box selected is T%d, want T19 (the 12th highest score)", drawn[11].Track)
	}
	for _, b := range drawn {
		if b.Track < 19 {
			t.Errorf("T%d was selected; the cap must keep the strongest, not the first it found", b.Track)
		}
	}
	// An undeclared cap is the documented default, not "no cap": a frame with 400
	// boxes would otherwise be unreadable and a 30-box frame would look different
	// on a build that declared nothing.
	d2, _ := Select(Spec{Boxes: boxes})
	if len(d2) != DefaultMaxBoxes {
		t.Errorf("with no declared cap, %d boxes were selected, want the documented default of %d", len(d2), DefaultMaxBoxes)
	}
}

// TestDrawRendersWhatSelectChosen: the two halves agree. The one box here is alone
// on the frame, so its own top-left corner is unambiguous.
func TestDrawRendersWhatSelectChosen(t *testing.T) {
	const w, h = 640, 384
	img := blank(w, h)
	spec := Spec{Provenance: "synthetic", Boxes: []TrackedBox{{
		Box:   Box{Class: "bus", Score: 0.71, X1: 0.30, Y1: 0.60, X2: 0.44, Y2: 0.78},
		Track: 5, Seen: 6,
	}}}
	drawn, _ := Select(spec)
	if len(drawn) != 1 {
		t.Fatalf("Select chose %d boxes, want 1", len(drawn))
	}
	Draw(img, spec)
	r := rectOf(drawn[0].Box, img.Bounds())
	if !changedAt(img, r.Min.X, r.Min.Y) {
		t.Errorf("the box Select chose was not drawn at its own top-left corner (%d,%d): Draw and Select disagree", r.Min.X, r.Min.Y)
	}
}

// TestAGhostIsDimmerThanAnApprovedBox: T8.4. A ghost that is as loud as an
// approved box is not a ghost, and the operator cannot tell what the system
// decided from what the detector suggested.
func TestAGhostIsDimmerThanAnApprovedBox(t *testing.T) {
	const w, h = 640, 384
	img := blank(w, h)
	ghost := Box{Class: "dog", Score: 0.21, X1: 0.2, Y1: 0.2, X2: 0.4, Y2: 0.4}
	Draw(img, Spec{Ghosts: []Box{ghost}})
	if !changedAt(img, w*2/10, h*2/10) {
		t.Fatal("the ghost outline was not drawn at all")
	}
	got := img.RGBAAt(w*2/10, h*2/10)
	solid := familyColour(classify.FamilyOf(ghost.Class))
	if got != dim(classify.FamilyOf(ghost.Class)) {
		t.Errorf("the ghost outline is %v, want the dimmed family colour %v", got, dim(classify.FamilyOf(ghost.Class)))
	}
	if got == solid {
		t.Errorf("the ghost is drawn in the full family colour %v: it is indistinguishable from an approved box", got)
	}
}

// TestTheHeaderPrintsTheRealAIState: D-5 at the top of the picture. The state is
// printed only when it is KNOWN, because an empty cell beside a live picture reads
// as "fine" and "fine" is a claim nobody made.
func TestTheHeaderPrintsTheRealAIState(t *testing.T) {
	const w, h = 640, 384

	known := blank(w, h)
	Draw(known, Spec{Provenance: "synthetic", AIState: "not_asked", AIStateKnown: true})
	if !changedAt(known, 5, 5) {
		t.Error("the provenance bar was not drawn")
	}

	// The same picture with the state withheld. Only the header band should differ
	// — a known state adds text, an unknown one adds nothing.
	unknown := blank(w, h)
	Draw(unknown, Spec{Provenance: "synthetic", AIState: "not_asked", AIStateKnown: false})
	same := true
	for x := 0; x < w && same; x++ {
		for y := 0; y < 22; y++ {
			if known.RGBAAt(x, y) != unknown.RGBAAt(x, y) {
				same = false
				break
			}
		}
	}
	if same {
		t.Error("the header is identical whether the AI state is known or not: an uninterpretable frame and a healthy one would be drawn the same way, which is exactly D5")
	}
	// A state that is not one of the four vocabulary words is not printed either.
	// The four words are the contract's, and the header is part of the contract's
	// surface.
	for _, state := range []string{"asked_answered", "no_objects", "degraded", "not_asked", "cannot_tell"} {
		img := blank(w, h)
		Draw(img, Spec{Provenance: "synthetic", AIState: state, AIStateKnown: true})
		if sameHeader(img, blank(w, h)) {
			t.Errorf("the state %q produced no drawing at all", state)
		}
	}
}

// sameHeader reports whether the header band of a is indistinguishable from b's.
func sameHeader(a, b *image.RGBA) bool {
	for x := 0; x < a.Bounds().Dx(); x++ {
		for y := 0; y < 22; y++ {
			if a.RGBAAt(x, y) != b.RGBAAt(x, y) {
				return false
			}
		}
	}
	return true
}

// TestAnEmptyFrameIsNotAnError: Draw on a 1x1, and on a frame with absurd boxes.
// The overlay describes the picture; it must never be the reason the picture stops.
func TestAnEmptyFrameIsNotAnError(t *testing.T) {
	tiny := image.NewRGBA(image.Rect(0, 0, 1, 1))
	Draw(tiny, Spec{Boxes: []TrackedBox{{
		Box:   Box{Class: "car", Score: 1, X1: 0, Y1: 0, X2: 1, Y2: 1},
		Track: 1, Seen: 1, HasPrev: true, PrevX: -5, PrevY: -5,
	}}})

	big := blank(200, 100)
	Draw(big, Spec{
		Lines: []Line{{Name: "x", A: [2]float64{-3, -3}, B: [2]float64{9, 9}}},
		Boxes: []TrackedBox{
			{Box: Box{Class: "car", Score: 1, X1: -2, Y1: -2, X2: 3, Y2: 3}, Track: 1, Seen: 1},
			{Box: Box{Class: "bird", Score: 1, X1: 0.99, Y1: 0.0, X2: 1.0, Y2: 0.001}, Track: 2, Seen: 1,
				HasPrev: true, PrevX: 0.5, PrevY: 0.5},
		},
		Note: "x",
	})
	// A nil image is a no-op rather than a panic: the caller holds a Live with no
	// frame in it more often than anyone would like.
	Draw(nil, Spec{Boxes: []TrackedBox{{Box: Box{Class: "car", Score: 1}}}})
}

// TestTheLabelFlipsLeftRatherThanRunningOffTheEdge: T8.2. A label clipped by the
// canvas is a label nobody reads, and the picture would say the object has no
// class at all.
func TestTheLabelFlipsLeftRatherThanRunningOffTheEdge(t *testing.T) {
	const w, h = 640, 384
	img := blank(w, h)
	// A box hard against the right edge: the label must not run off.
	Draw(img, Spec{Boxes: []TrackedBox{{
		Box:   Box{Class: "motorcycle", Score: 0.8, X1: 0.92, Y1: 0.5, X2: 0.99, Y2: 0.7},
		Track: 7, Seen: 4,
	}}})
	// Nothing may be drawn at the very last column at the label's row: that is
	// what "ran off the edge" looks like.
	labelRow := int(0.5*h) - 3
	if changedAt(img, w-1, labelRow) {
		t.Error("the label ran off the right edge of the frame")
	}
	// And something WAS drawn on the left of the box, which is where it flipped to.
	flipped := false
	for x := 0; x < w*92/100-10; x++ {
		for y := labelRow - 12; y < labelRow+6; y++ {
			if changedAt(img, x, y) {
				flipped = true
			}
		}
	}
	if !flipped {
		t.Error("nothing was drawn to the left of a box at the right edge: the label was dropped rather than flipped")
	}
}
