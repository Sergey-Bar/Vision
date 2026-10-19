package soak_test

// The per-site, per-class verification slice.
//
// The claim this section carries is the platform's third invariant, restated for
// the module that is actually demonstrated: **A missing AI observation is never
// a negative observation.** Every rule that consumes a Tier C signal declares
// what it does when the signal is absent, and this slice is where the run-level
// arithmetic says so.
//
// So the tests below are not about a table rendering. They are about which of
// the five states counts as an answer, what happens to a state that is not one
// of the five, and what a reader is told when there was nothing to measure. Each
// of those is a place where folding the wrong thing into a share produces a dead
// sidecar that reads as a quiet road.

import (
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision-poc/internal/soak"
	"github.com/Sergey-Bar/vision/shared/aistate"
)

// sliceFixture is a small run with a shape that exercises every branch: one site,
// two classes, two answered states and two blind ones, plus a total that is
// larger than what was retained because two detections were abandoned
// mid-pipeline.
func sliceFixture() soak.VerificationSlice {
	cells := []soak.VerificationCell{
		{Site: "camera-east.mp4", Class: "car", State: aistate.Asked, Count: 20},
		{Site: "camera-east.mp4", Class: "car", State: aistate.NoObjects, Count: 6},
		{Site: "camera-east.mp4", Class: "car", State: aistate.Degraded, Count: 4},
		{Site: "camera-east.mp4", Class: "person", State: aistate.Asked, Count: 10},
		{Site: "camera-east.mp4", Class: "person", State: aistate.NotAsked, Count: 2},
		{Site: "camera-west.mp4", Class: "car", State: aistate.Asked, Count: 14},
		{Site: "camera-west.mp4", Class: "car", State: aistate.NotAsked, Count: 8},
	}
	byState := map[string]int{
		aistate.Asked: 44, aistate.NoObjects: 6, aistate.Degraded: 4, aistate.NotAsked: 10,
	}
	return soak.VerificationSliceOf(cells, byState, 68, 64)
}

// renderSlice reaches the unexported section the way the report does — through
// the type the report holds — so the test cannot pass by calling a helper the
// real report does not use.
func renderSlice(v soak.VerificationSlice) string {
	return soak.MarkdownForTest(soak.Report{Counters: soak.Counters{Verification: v}})
}

// TestABlindStateIsNeverCountedAsAnAnswer is the load-bearing one.
//
// `no_objects` IS an answer -- the model ran, parsed, and the array was empty,
// which is the only state in which "the model found nothing" is a statement
// about the scene. The other three are not, and folding any of them into the
// answered half is how a run with a dead sidecar reports a detection rate about
// frames nobody answered. This is the platform's invariant 3 and the reason the
// whole section exists.
func TestABlindStateIsNeverCountedAsAnAnswer(t *testing.T) {
	v := sliceFixture()
	if v.Answered != 50 {
		t.Fatalf("answered = %d, want 50 (20+6+10+14 across the four answered cells)", v.Answered)
	}
	if v.Unanswered != 14 {
		t.Fatalf("unanswered = %d, want 14 (4 degraded + 2 not_asked + 8 not_asked)", v.Unanswered)
	}
	// The two halves must be the retained total and not one more than it.
	if v.Answered+v.Unanswered != v.Retained {
		t.Fatalf("answered (%d) + unanswered (%d) = %d, want the retained total %d; the two halves are not a partition of the denominator the share is printed over",
			v.Answered, v.Unanswered, v.Answered+v.Unanswered, v.Retained)
	}
}

// TestTheShareIsPrintedOverItsOwnDenominator guards the share.
//
// A share over a denominator nobody printed is a number about the denominator,
// and the one here is the count the section states in its own sentence. It is
// also the count that makes an unmeasurable run unmeasurable rather than 0%.
func TestTheShareIsPrintedOverItsOwnDenominator(t *testing.T) {
	v := sliceFixture()
	if !v.UnansweredMeasured {
		t.Fatal("the fixture has 64 retained detections, so the share is a measurement")
	}
	if v.UnansweredShareDenom != 64 {
		t.Fatalf("the share's denominator is %d, want the retained 64", v.UnansweredShareDenom)
	}
	want := 14.0 / 64.0
	if v.UnansweredShareFrac < want-0.0001 || v.UnansweredShareFrac > want+0.0001 {
		t.Fatalf("the unanswered share is %.4f, want %.4f (14 of 64)", v.UnansweredShareFrac, want)
	}
	// The section must PRINT the denominator beside the share. A percentage
	// alone is the number the runaway denominator produces.
	out := renderSlice(v)
	if !strings.Contains(out, "of the 64 retained detection(s)") {
		t.Fatalf("the share is printed without its denominator:\n%s", out)
	}
}

// sectionOf slices one section out of a rendered report, so an assertion about
// this section is not accidentally an assertion about the verdict table next to
// it. The whole-page string is what a reader sees; the section is what a test
// claims about.
func sectionOf(out, heading string) string {
	lines := strings.Split(out, "\n")
	start := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == heading {
			start = i
			break
		}
	}
	if start < 0 {
		return ""
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "## ") {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}

// TestAnUnmeasuredRunSaysNOTRUNAndNotZero is the other half of the same rule.
//
// An empty slice means no detection ever reached the query. That is a fact about
// the run and not a measurement of the plane, and printing 0% for it says the
// model was served perfectly on a run where nothing happened at all.
func TestAnUnmeasuredRunSaysNOTRUNAndNotZero(t *testing.T) {
	v := soak.VerificationSliceOf(nil, nil, 12, 0)
	if v.Measured {
		t.Fatal("a run with nothing retained is not a measured slice")
	}
	if v.UnansweredMeasured {
		t.Fatal("an unmeasured slice must not carry a share")
	}
	out := sectionOf(renderSlice(v), "## Where this run stayed quiet, per site and class")
	if !strings.Contains(out, "NOT RUN") {
		t.Fatalf("an unmeasured slice does not say NOT RUN:\n%s", out)
	}
	if strings.Contains(out, "%") {
		t.Fatalf("an unmeasured slice printed a percentage, which is a measurement nobody made:\n%s", out)
	}
	if !strings.Contains(out, "`detections | 0`") {
		t.Fatalf("the unmeasured sentence does not name the confusion it exists to refuse, which is a run that produced nothing and a run whose detector saw nothing printing the same two characters:\n%s", out)
	}
}

// TestAStateOutsideTheFiveIsRefusedNotFolded covers the sixth state.
//
// The vocabulary is closed. A state that is not one of the five is a protocol
// error from the wire, and the choice that matters is whether it lands in the
// answered half (a fabricated detection), the unanswered half (an invented
// blindness), or neither. Neither is the only honest answer: it is printed
// under its own name and it is excluded from both halves, so the reader sees a
// partition that does not add up and the reason why.
func TestAStateOutsideTheFiveIsRefusedNotFolded(t *testing.T) {
	cells := []soak.VerificationCell{
		{Site: "s", Class: "car", State: aistate.Asked, Count: 5},
		{Site: "s", Class: "car", State: "answered_hard", Count: 3},
	}
	v := soak.VerificationSliceOf(cells, map[string]int{aistate.Asked: 5, "answered_hard": 3}, 8, 8)
	if v.Answered != 5 {
		t.Fatalf("answered = %d, want 5; a state outside the five was counted as an answer", v.Answered)
	}
	if v.Unanswered != 0 {
		t.Fatalf("unanswered = %d, want 0; a state outside the five was counted as a blindness", v.Unanswered)
	}
	out := renderSlice(v)
	if !strings.Contains(out, "answered_hard") {
		t.Fatalf("the unrecognised state is not shown by name:\n%s", out)
	}
	if !strings.Contains(out, "not one of the five") {
		t.Fatalf("the unrecognised state is shown without being marked as one:\n%s", out)
	}
	// The share stays a measurement over the RETAINED total, so it still adds
	// up -- 0 of 8 -- and the discrepancy the reader sees is between the halves
	// and the denominator, which is exactly the signal.
	if v.UnansweredShareFrac != 0 || !v.UnansweredMeasured {
		t.Fatalf("share = %.2f (measured=%v), want 0.00 measured", v.UnansweredShareFrac, v.UnansweredMeasured)
	}
}

// TestTheAxesAreSets checks the table's rows and columns.
//
// A site that produced detections of two classes must be one row and not two,
// and the classes must be one column and not one per cell. The ordering is
// sorted, so the printed table is byte-stable between runs.
func TestTheAxesAreSets(t *testing.T) {
	v := sliceFixture()
	if len(v.Sites) != 2 || v.Sites[0] != "camera-east.mp4" || v.Sites[1] != "camera-west.mp4" {
		t.Fatalf("sites = %v, want the two, sorted", v.Sites)
	}
	if len(v.Classes) != 2 || v.Classes[0] != "car" || v.Classes[1] != "person" {
		t.Fatalf("classes = %v, want car and person, sorted", v.Classes)
	}
}

// TestTheSectionNamesTheSitesItIsTalkingAbout is the section's own premise.
//
// This is the autonomous-camera claim made measurable: the question is not "how
// quiet was the run" but "WHERE was it quiet", and a table that cannot be read
// back by site answers a different question. The row labels come from the run's
// own source, so a reader checking the claim can find the camera.
func TestTheSectionNamesTheSitesItIsTalkingAbout(t *testing.T) {
	out := renderSlice(sliceFixture())
	for _, want := range []string{"camera-east.mp4", "camera-west.mp4", "car", "person"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the section does not name %q:\n%s", want, out)
		}
	}
	// And the five states are all columns, so a sighting with no cell for a
	// state is readable as a zero rather than as a missing word.
	for _, k := range aistate.All {
		if !strings.Contains(out, k) {
			t.Fatalf("the section has no column for state %q:\n%s", k, out)
		}
	}
	if !strings.Contains(out, "NOT a detection rate") {
		t.Fatalf("the section does not refuse to let its share read as a detection rate:\n%s", out)
	}
}

// TestTheSectionSaysSoWhenEveryDetectionGotAnAnswer is the clean-run sentence.
//
// A run where the plane answered everything is a run that says nothing about
// behaviour under an absent plane. Saying so is not modesty: it is the reason
// the rehearsed faults exist, and a reader who is told "everything was answered"
// without also being told that this proves nothing about a failing sidecar has
// been handed a green badge.
func TestTheSectionSaysSoWhenEveryDetectionGotAnAnswer(t *testing.T) {
	cells := []soak.VerificationCell{
		{Site: "s", Class: "car", State: aistate.Asked, Count: 9},
		{Site: "s", Class: "car", State: aistate.NoObjects, Count: 4},
	}
	v := soak.VerificationSliceOf(cells, map[string]int{aistate.Asked: 9, aistate.NoObjects: 4}, 13, 13)
	out := renderSlice(v)
	if !strings.Contains(out, "Every retained detection had an answer") {
		t.Fatalf("the clean run is not stated as one:\n%s", out)
	}
}

// TestTheDenominatorIsVisibleBesideItsExclusion closes the last gap.
//
// The slice's denominator is the RETAINED detections, which is not the run's
// detection count: abandoned ones never reached the plane. A reader must be able
// to see the difference on the page, or the difference silently becomes a
// pipeline problem hidden behind the plane.
func TestTheDenominatorIsVisibleBesideItsExclusion(t *testing.T) {
	v := sliceFixture()
	if v.TotalDetections != 68 || v.Retained != 64 {
		t.Fatalf("total=%d retained=%d, want 68 and 64", v.TotalDetections, v.Retained)
	}
	out := renderSlice(v)
	if !strings.Contains(out, "**64**") || !strings.Contains(out, "**68**") {
		t.Fatalf("the section does not print both numbers, so the exclusion is invisible:\n%s", out)
	}
}
