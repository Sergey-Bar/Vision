package detect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testZone(t *testing.T) Zone {
	t.Helper()
	path := filepath.Join(t.TempDir(), "zones.json")
	body := `{
	  "source": "testdata/car-crossing.mp4",
	  "frame": {"width": 1280, "height": 720},
	  "lines": [
	    {"name": "kerb", "a": [0.0, 0.5], "b": [1.0, 0.5], "direction": "north-to-south"},
	    {"name": "crosswalk", "a": [0.5, 0.0], "b": [0.5, 1.0], "direction": "west-to-east",
	     "classes": ["person"]}
	  ]
	}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	z, err := LoadZone(path)
	if err != nil {
		t.Fatal(err)
	}
	return z
}

// polygonZone adds a region of interest, used by the tests that need a box to be
// inside a shape before it can reach a line.
func polygonZone(t *testing.T) Zone {
	t.Helper()
	z := testZone(t)
	z.Polygons = []Polygon{{
		Name:    "roadway",
		Points:  [][2]float64{{0, 0.5}, {1, 0.5}, {1, 1}, {0, 1}},
		Classes: []string{"car", "bird", "person"},
	}}
	return z
}

func testConfig() Config {
	return Config{ScoreThreshold: 0.45, Classes: []string{"car", "bird", "person"}, IoU: 0.3,
		MaxAge: 2 * time.Second, Cooldown: 10 * time.Second, Tolerance: 0.01}
}

func newEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := New(testZone(t), testConfig())
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func car(x1, y1, x2, y2 float64) Box {
	return Box{Class: "car", Score: 0.9, X1: x1, Y1: y1, X2: x2, Y2: y2}
}

// carAt centres a car box at (cx, cy) with a fixed size, so a test can move it
// across a line in realistic steps instead of teleporting it. A teleport would
// produce a new track, and a new track that appears on the far side of a line
// is correctly NOT a crossing.
func carAt(cx, cy float64) Box { return car(cx-0.1, cy-0.075, cx+0.1, cy+0.075) }

// walk feeds one object moving from `from` to `to` over `frames` steps and
// returns how many crossings the engine decided.
func walk(e *Engine, at time.Time, frames int, from, to, x float64) int {
	total := 0
	for i := 0; i < frames; i++ {
		frac := float64(i) / float64(frames-1)
		cy := from + (to-from)*frac
		c, _, _ := e.Feed(at.Add(time.Duration(i)*200*time.Millisecond), []Box{carAt(x, cy)})
		total += len(c)
	}
	return total
}

var base = time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)

// TestOnePassProducesExactlyOneCrossing is the T11 test. An object that walks
// across the line and then stays on the far side for many frames must produce
// one event, not one per frame: an event rate that is a function of the frame
// rate measures the frame rate.
func TestOnePassProducesExactlyOneCrossing(t *testing.T) {
	e := newEngine(t)
	if got := walk(e, base, 10, 0.70, 0.30, 0.3); got != 1 {
		t.Fatalf("a single pass produced %d crossings, want exactly 1", got)
	}
	// Ten more frames on the far side, still no further event.
	if got := walk(e, base.Add(3*time.Second), 10, 0.30, 0.28, 0.3); got != 0 {
		t.Fatalf("sitting on the far side produced %d more crossings", got)
	}
}

// TestASecondGenuineCrossingIsCounted: the latch must not eat a real second
// crossing. The car goes back, then crosses again, and the cooldown is long
// over by then.
func TestASecondGenuineCrossingIsCounted(t *testing.T) {
	e := newEngine(t)
	if got := walk(e, base, 10, 0.70, 0.30, 0.3); got != 1 {
		t.Fatalf("first pass = %d crossings, want 1", got)
	}
	if got := walk(e, base.Add(20*time.Second), 10, 0.30, 0.70, 0.3); got != 1 {
		t.Fatalf("the return trip = %d crossings, want 1", got)
	}
	if got := walk(e, base.Add(60*time.Second), 10, 0.70, 0.30, 0.3); got != 1 {
		t.Fatalf("second outward trip = %d crossings, want 1", got)
	}
}

// TestATrackOnTheLineDoesNotChatter: a track whose centre sits inside the
// tolerance band must not produce a stream of events. This is the noise that
// makes a crossing count unreproducible.
func TestATrackOnTheLineDoesNotChatter(t *testing.T) {
	e := newEngine(t)
	total := 0
	for i := 0; i < 40; i++ {
		offset := 0.001
		if i%2 == 0 {
			offset = -0.001
		}
		c, _, _ := e.Feed(base.Add(time.Duration(i)*200*time.Millisecond), []Box{carAt(0.3, 0.5+offset)})
		total += len(c)
	}
	if total != 0 {
		t.Fatalf("a track jittering on the line produced %d crossings", total)
	}
}

func TestClassesAreFilteredByScoreAndApproval(t *testing.T) {
	cases := []struct {
		name string
		box  Box
	}{
		{"below the threshold", Box{Class: "car", Score: 0.10, X1: 0.2, Y1: 0.7, X2: 0.4, Y2: 0.85}},
		{"class not approved", Box{Class: "aeroplane", Score: 0.99, X1: 0.2, Y1: 0.7, X2: 0.4, Y2: 0.85}},
		{"inverted box", car(0.4, 0.7, 0.2, 0.85)},
		{"no class", Box{Score: 0.9, X1: 0.2, Y1: 0.7, X2: 0.4, Y2: 0.85}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A fresh engine per case: a box left in the previous case's tracker
			// would make this case pass or fail for the wrong reason.
			e := newEngine(t)
			_, tracks, st := e.Feed(base, []Box{tc.box})
			if st.Approved != 0 || st.Rejected != 1 {
				t.Fatalf("approved = %d rejected = %d", st.Approved, st.Rejected)
			}
			if len(tracks) != 0 {
				t.Fatalf("a rejected box created %d tracks", len(tracks))
			}
		})
	}
}

func TestALineWithClassRestrictionsIgnoresOtherClasses(t *testing.T) {
	e := newEngine(t)
	total := 0
	for i := 0; i < 10; i++ {
		x := 0.2
		if i >= 5 {
			x = 0.7
		}
		b := Box{Class: "car", Score: 0.9, X1: x, Y1: 0.6, X2: x + 0.1, Y2: 0.75}
		c, _, _ := e.Feed(base.Add(time.Duration(i)*200*time.Millisecond), []Box{b})
		for _, x := range c {
			if x.Line == "crosswalk" {
				total++
			}
		}
	}
	if total != 0 {
		t.Fatalf("a car triggered the person-only crosswalk %d times", total)
	}
}

func TestPersonTriggersTheCrosswalk(t *testing.T) {
	// The restriction must not disable the line entirely: the class it does
	// name has to work.
	e := newEngine(t)
	got := 0
	// Twenty frames across 0.5 of the frame width: fast enough to be a test,
	// slow enough that consecutive boxes still overlap by more than the
	// association threshold. A teleport would produce a new track, and a new
	// track that appears on the far side is correctly not a crossing.
	const frames = 20
	for i := 0; i < frames; i++ {
		frac := float64(i) / (frames - 1)
		cx := 0.2 + 0.5*frac
		b := Box{Class: "person", Score: 0.9, X1: cx - 0.05, Y1: 0.6, X2: cx + 0.05, Y2: 0.8}
		c, _, _ := e.Feed(base.Add(time.Duration(i)*200*time.Millisecond), []Box{b})
		for _, x := range c {
			if x.Line == "crosswalk" {
				got++
			}
		}
	}
	if got != 1 {
		t.Fatalf("a person crossing the crosswalk produced %d events, want 1", got)
	}
}

func TestTrackerKeepsIdentityAcrossFrames(t *testing.T) {
	e := newEngine(t)
	first := 0
	for i := 0; i < 6; i++ {
		x := 0.2 + float64(i)*0.02
		_, tracks, _ := e.Feed(base.Add(time.Duration(i)*200*time.Millisecond), []Box{car(x, 0.7, x+0.2, 0.85)})
		if i == 0 {
			first = tracks[0].ID
			continue
		}
		if len(tracks) != 1 {
			t.Fatalf("frame %d has %d tracks, want 1: the tracker lost identity across a slow move", i, len(tracks))
		}
		if tracks[0].ID != first {
			t.Fatalf("frame %d produced track %d, want the same track %d", i, tracks[0].ID, first)
		}
		if tracks[0].Seen != i+1 {
			t.Fatalf("track seen count = %d, want %d", tracks[0].Seen, i+1)
		}
	}
}

func TestTrackIsRetiredAfterMaxAge(t *testing.T) {
	e := newEngine(t)
	_, tracks, _ := e.Feed(base, []Box{car(0.2, 0.7, 0.4, 0.85)})
	if len(tracks) != 1 {
		t.Fatal("no track")
	}
	first := tracks[0].ID
	// A long occlusion, then the object reappears at the same place: a new
	// track, not a revival. Associating before retiring would match it and the
	// tracker would believe the object never left.
	_, tracks, _ = e.Feed(base.Add(10*time.Second), []Box{car(0.2, 0.7, 0.4, 0.85)})
	if len(tracks) != 1 {
		t.Fatalf("tracks = %d", len(tracks))
	}
	if tracks[0].ID == first || tracks[0].Seen != 1 {
		t.Fatalf("a re-appearing object was treated as the same track: %+v", tracks[0])
	}
}

func TestCooldownSuppressesASecondObjectOfTheSameClass(t *testing.T) {
	e := newEngine(t)
	if got := walk(e, base, 10, 0.70, 0.30, 0.3); got != 1 {
		t.Fatalf("first car = %d crossings, want 1", got)
	}
	// A different car, same class, same line, 1 second later. The cooldown is
	// 10s and is keyed by line and class, so this is suppressed.
	if got := walk(e, base.Add(1*time.Second), 10, 0.70, 0.30, 0.6); got != 0 {
		t.Fatalf("a second car inside the cooldown produced %d crossings", got)
	}
	// Well after the window, a new car crossing is a real event again.
	if got := walk(e, base.Add(60*time.Second), 10, 0.70, 0.30, 0.3); got != 1 {
		t.Fatalf("a crossing after the cooldown was suppressed: %d", got)
	}
}

func TestCrossingCarriesTheBoxAndDirection(t *testing.T) {
	e := newEngine(t)
	var got []Crossing
	for i := 0; i < 10; i++ {
		frac := float64(i) / 9
		c, _, _ := e.Feed(base.Add(time.Duration(i)*200*time.Millisecond), []Box{carAt(0.3, 0.70-0.40*frac)})
		got = append(got, c...)
	}
	if len(got) != 1 {
		t.Fatalf("crossings = %d", len(got))
	}
	c := got[0]
	if c.Direction != "north-to-south" {
		t.Fatalf("direction = %q, want the line's declared direction", c.Direction)
	}
	if c.Class != "car" || c.Line != "kerb" {
		t.Fatalf("crossing = %+v", c)
	}
	if c.Frames < 2 {
		t.Fatalf("the crossing claims the object existed for %d frames; a box that appeared on the far side is not a crossing", c.Frames)
	}
	if !c.At.Equal(base.Add(4*200*time.Millisecond)) && c.Frames < 2 {
		t.Fatalf("the crossing instant %s is not on the frame clock", c.At)
	}
}

func TestPolygonRestrictsWhichBoxesCount(t *testing.T) {
	e, err := New(polygonZone(t), testConfig())
	if err != nil {
		t.Fatal(err)
	}
	// centre (0.3, 0.775) is inside the roadway polygon.
	if _, _, st := e.Feed(base, []Box{car(0.2, 0.7, 0.4, 0.85)}); st.Approved != 1 {
		t.Fatalf("a box inside the polygon was rejected (approved %d)", st.Approved)
	}
	e2, _ := New(polygonZone(t), testConfig())
	// centre (0.3, 0.375) is above the polygon.
	if _, tracks, st := e2.Feed(base, []Box{car(0.2, 0.30, 0.4, 0.45)}); st.Approved != 0 || len(tracks) != 0 {
		t.Fatalf("a box outside the polygon was approved (approved %d, tracks %d)", st.Approved, len(tracks))
	}
}

func TestNewRefusesAnEmptyClassList(t *testing.T) {
	z := testZone(t)
	cfg := testConfig()
	cfg.Classes = nil
	if _, err := New(z, cfg); err == nil {
		t.Fatal("an engine with no approved classes was built: every run would report zero events as if the model had found nothing")
	}
}

func TestNewRefusesAnUnusableIoU(t *testing.T) {
	z := testZone(t)
	cfg := testConfig()
	cfg.IoU = 0
	if _, err := New(z, cfg); err == nil {
		t.Fatal("a zero association threshold was accepted")
	}
}

// --- zone file validation ---------------------------------------------------

func writeZone(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "zones.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestZoneWithNoLinesIsRefused(t *testing.T) {
	// A zone with no line can only ever produce zero events, and a run that
	// reports zero events as if the model had found nothing is the most
	// expensive silence there is.
	path := writeZone(t, `{"source":"x","lines":[]}`)
	_, err := LoadZone(path)
	if err == nil || !strings.Contains(err.Error(), "no lines") {
		t.Fatalf("a zone with no lines was accepted: %v", err)
	}
}

func TestZoneWithADegenerateLineIsRefused(t *testing.T) {
	path := writeZone(t, `{"lines":[{"name":"x","a":[0.5,0.5],"b":[0.5,0.5]}]}`)
	_, err := LoadZone(path)
	if err == nil || !strings.Contains(err.Error(), "identical endpoints") {
		t.Fatalf("a zero-length line was accepted: %v", err)
	}
}

func TestZoneWithADuplicateLineNameIsRefused(t *testing.T) {
	path := writeZone(t, `{"lines":[{"name":"x","a":[0,0.5],"b":[1,0.5]},{"name":"x","a":[0,0.4],"b":[1,0.4]}]}`)
	_, err := LoadZone(path)
	if err == nil || !strings.Contains(err.Error(), "declared twice") {
		t.Fatalf("a duplicate line name was accepted: %v", err)
	}
}

func TestZoneWithAnOffFrameEndpointIsRefused(t *testing.T) {
	path := writeZone(t, `{"lines":[{"name":"x","a":[0,0.5],"b":[1.4,0.5]}]}`)
	_, err := LoadZone(path)
	if err == nil || !strings.Contains(err.Error(), "outside the frame") {
		t.Fatalf("an off-frame endpoint was accepted: %v", err)
	}
}

func TestZoneWithAnUnknownFieldIsRefused(t *testing.T) {
	// A typo in a hand-written zones.json must not be silently ignored: the
	// operator would think a restriction is in force when it is not.
	path := writeZone(t, `{"lines":[{"name":"x","a":[0,0.5],"b":[1,0.5],"dirction":"oops"}]}`)
	_, err := LoadZone(path)
	if err == nil || !strings.Contains(err.Error(), "not the documented zone shape") {
		t.Fatalf("a typo in a zone field was ignored: %v", err)
	}
}

// TestADollarKeyIsAnAnnotationAndIsIgnored: the zone file carries `$comment`
// blocks explaining what a wrong line costs. Refusing a comment would make it
// impossible to keep that explanation next to the thing it explains.
func TestADollarKeyIsAnAnnotationAndIsIgnored(t *testing.T) {
	path := writeZone(t, `{
	  "$comment": "a wrong line produces zero events and no error",
	  "source": "x.mp4",
	  "lines": [{"name":"x","a":[0,0.5],"b":[1,0.5]}]
	}`)
	z, err := LoadZone(path)
	if err != nil {
		t.Fatalf("a $comment key was refused: %v", err)
	}
	if z.Source != "x.mp4" || len(z.Lines) != 1 {
		t.Fatalf("the zone was not read past the annotation: %+v", z)
	}
	// A `$` key deep inside a line is an annotation too, and a real typo is
	// still a refusal.
	ok := writeZone(t, `{"lines":[{"name":"x","a":[0,0.5],"b":[1,0.5],"$note":"why"}]}`)
	if _, err := LoadZone(ok); err != nil {
		t.Fatalf("a $ key inside a line was refused: %v", err)
	}
	bad := writeZone(t, `{"lines":[{"name":"x","a":[0,0.5],"b":[1,0.5],"direcion":"oops"}]}`)
	if _, err := LoadZone(bad); err == nil {
		t.Fatal("a misspelled field next to an annotation was accepted")
	}
}

func TestMissingZoneFileNamesTheConsequence(t *testing.T) {
	_, err := LoadZone(filepath.Join(t.TempDir(), "nope.json"))
	if err == nil || !strings.Contains(err.Error(), "no crossing") {
		t.Fatalf("a missing zone file did not explain the consequence: %v", err)
	}
}

func TestSideIsSignedAndConsistent(t *testing.T) {
	l := Line{Name: "x", A: [2]float64{0, 0.5}, B: [2]float64{1, 0.5}}
	above, d1 := l.side(0.5, 0.8)
	below, d2 := l.side(0.5, 0.2)
	if above == below {
		t.Fatalf("points on opposite sides got the same sign: %d and %d", above, below)
	}
	if d1 < 0.29 || d1 > 0.31 || d2 < 0.29 || d2 > 0.31 {
		t.Fatalf("distances = %v, %v; want about 0.3 for a normalised frame", d1, d2)
	}
	on, d0 := l.side(0.5, 0.5)
	if on != 0 || d0 != 0 {
		t.Fatalf("a point on the line got sign %d distance %v", on, d0)
	}
}

func TestBoxValidityRefusesDegenerateBoxes(t *testing.T) {
	if (Box{Class: "car", Score: 0.9, X1: 0.5, Y1: 0.5, X2: 0.5, Y2: 0.6}).Valid() {
		t.Fatal("a zero-width box was accepted")
	}
	if (Box{Class: "car", Score: 0.9, X1: 0.6, Y1: 0.5, X2: 0.5, Y2: 0.6}).Valid() {
		t.Fatal("an inverted box was accepted")
	}
	if (Box{Score: 0.9, X1: 0.1, Y1: 0.1, X2: 0.2, Y2: 0.2}).Valid() {
		t.Fatal("a classless box was accepted")
	}
}

func TestZoneRecordsWhichSourceItWasDrawnFor(t *testing.T) {
	// The coordinates are only meaningful for the source they were drawn on, and
	// the report has to name it.
	z := testZone(t)
	if z.Source == "" {
		t.Fatal("the zone does not record its source")
	}
	if z.Frame == nil || z.Frame.Width != 1280 {
		t.Fatalf("the zone does not record the frame it was drawn against: %+v", z.Frame)
	}
	pz := polygonZone(t)
	got := pz.SortedClasses()
	if len(got) != 3 || got[0] != "bird" || got[2] != "person" {
		t.Fatalf("SortedClasses = %v, want the union of the line and polygon restrictions", got)
	}
}

func TestLiveTracksIsASnapshot(t *testing.T) {
	// The screen reads this from another goroutine, so handing out the live map
	// would be a data race and a crash on the day of the demo.
	e := newEngine(t)
	e.Feed(base, []Box{car(0.2, 0.7, 0.4, 0.85)})
	first := e.LiveTracks()
	first[0].ID = 999
	second := e.LiveTracks()
	if second[0].ID == 999 {
		t.Fatal("LiveTracks handed out the live map, not a copy")
	}
}
