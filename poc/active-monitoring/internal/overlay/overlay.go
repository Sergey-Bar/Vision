// Package overlay draws the pipeline's picture: the approved boxes, the crossing
// lines, the tracked-object markers and the note line, in Go, on the pixels that
// already exist in this process.
//
// It is a LEAF. It imports the standard library and `internal/classify`, and
// nothing else in this module. That is arithmetic, not a style preference:
//
//	internal/ui  imports internal/soak and internal/store
//	internal/soak must draw a snapshot of a crossing, which is what T6.3 asks for
//
// so the runner cannot import the screen to reach the drawing code — that is a
// cycle and Go will not compile it. The drawing therefore lives here, below both,
// and the screen imports it upward. `scripts/verify.ps1` checks the import graph
// for this package and fails on any of soak, store, ai, detect or ui (T1.4),
// because a leaf that has quietly started depending on the thing that depends on
// it is exactly the cycle waiting to happen.
//
// The 7x13 face in font.go carries NO build tag. It used to live in
// `internal/ui/font_windows.go` under `//go:build windows`, which meant
// `internal/ui` did not compile on Linux at all: `labelBar` and `labelFace`
// existed on one platform only, and every caller of them was a compile error
// everywhere else. That is D7, and it is fixed by the face being here with no
// build constraint on it.
package overlay

import (
	"image"
	"image/color"
	"image/draw"
	"sort"
	"strings"

	"github.com/Sergey-Bar/ai-captain-poc/internal/classify"
)

// Box is one detection in NORMALISED coordinates: 0..1 against the frame, not
// against the model's input. A model at 640x640 and a camera at 1920x1080 produce
// the same numbers, so the picture matches the pixels at any resolution.
type Box struct {
	Class string
	Score float64
	X1    float64
	Y1    float64
	X2    float64
	Y2    float64
}

// Family is the class's family, resolved once at construction so drawing does no
// lookup per pixel and cannot disagree with the report about what an object is.
func (b Box) Family() classify.Family { return classify.FamilyOf(b.Class) }

// Centre is the box's centre in normalised units.
func (b Box) Centre() (float64, float64) { return (b.X1 + b.X2) / 2, (b.Y1 + b.Y2) / 2 }

// TrackID ties a box to its tracker. Zero means "untracked", which is what the
// screen draws for a box the line-cross machine has not adopted yet.
type TrackID int

// Box is a box that also knows which track it belongs to.
type TrackedBox struct {
	Box
	// Track is the tracker id, shown in the label plate as T12. It is drawn
	// because a track id that changes every frame makes a viewer distrust the
	// tracker, and a track id that is visible and stable is evidence for it.
	Track TrackID
	// Seen is how many frames the track has been seen. Below
	// SP_POC_TRACK_MIN_FRAMES a track cannot emit a crossing, and a box that
	// cannot yet be an event is drawn as a GHOST rather than as an approved
	// object, so the picture and the decision agree.
	Seen int
	// PrevX and PrevY are the previous frame's centre. They are what the velocity
	// arrow is drawn from, and (0,0) means "no previous position", which draws no
	// arrow rather than an arrow from the corner of the frame.
	PrevX, PrevY float64
	HasPrev      bool
}

// Line is a crossing line in normalised coordinates.
type Line struct {
	Name string
	A    [2]float64
	B    [2]float64
}

// Spec is everything one frame's overlay needs. It is a struct rather than eleven
// parameters so that adding a thing to the picture is one field and not a
// signature change at every call site, and so that "what was drawn" is a value a
// test can construct and assert on.
type Spec struct {
	// Lines are the crossing lines, drawn under the boxes.
	Lines []Line
	// Boxes are the APPROVED boxes: the ones the system decided about. The screen
	// used to draw the raw model output, below threshold and outside the approved
	// class list, so the operator saw what the detector produced and never what
	// the pipeline decided. That is D6, and the separation is the whole fix:
	// solid for approved, dim for ghosted, and a printed count for what was
	// suppressed.
	Boxes []TrackedBox
	// Ghosts are boxes that were below the score threshold but above
	// SP_POC_GHOST_MIN_SCORE, or of a class outside the approved list. They are
	// drawn 1 px and dim so "what was rejected" is visible without flooding the
	// frame. They are never counted as detections anywhere else.
	Ghosts []Box
	// MaxBoxes caps BOTH lists. A thirty-vehicle frame is thirty boxes, and thirty
	// unfilled outlines over a road is a picture nobody can read. What was capped
	// is printed in the note, so the cap is a statement and not a silence.
	MaxBoxes int
	// Suppressed maps a family to how many of ITS boxes were not drawn. A single
	// total would hide which family the cap actually ate, and "we drew 12 of 40"
	// without saying which 28 is a number nobody can act on.
	Suppressed map[classify.Family]int
	// Note is a line of text for the bottom-left, for anything the picture is
	// choosing not to show. A viewer who can see that 12 boxes were left off is
	// being told; one who cannot is being misled by an omission.
	Note string
	// Provenance, AIState and At are printed on the picture itself, so a
	// screenshot of the frame is still honest on its own.
	Provenance string
	AIState    string
	At         string
	// AIStateKnown distinguishes "the pipeline told us" from "no state has been
	// published yet". A blank AIState printed as an empty cell reads as "fine".
	AIStateKnown bool

	// Verdict is the MODEL's answer, burned into the picture. W2-8.
	//
	// It is on the server-drawn overlay rather than only in the HTML because a
	// screenshot is the artefact: a judge, a customer, a ticket — every one of
	// them gets a PNG, not a URL. And the earlier claim of "we show the verdict"
	// was carried by a row of text that the crop tool could remove and the
	// snapshot path did not draw at all.
	Verdict string
	// VerdictKnown distinguishes "the model answered" from "there is no answer".
	// An empty Verdict printed as an empty cell reads as `false_positive`, which
	// is the one impression this module must never give by omission.
	VerdictKnown bool
	// VerdictSummary is the model's own sentence, truncated for the banner.
	VerdictSummary string
	// PushResult is what the VMS said, and it is on the picture for the same
	// reason: "the event was raised" and "the event reached the VMS" are
	// different claims and a screenshot cannot make the difference.
	PushResult string
	// Counters are the run's totals, printed in the same bar.
	Counters string
}

// lineColour is the crossing line. It is deliberately NOT a family colour: a line
// is a piece of configuration the operator drew, and colouring it like an object
// would make it look like a detection.
var lineColour = color.RGBA{90, 200, 255, 255}

// headerColour is the provenance bar. Near-black and mostly opaque, because the
// point is to carry a label and not to hide the picture.
var headerColour = color.RGBA{20, 20, 20, 200}

// noteColour is the note line's foreground.
var noteColour = color.RGBA{200, 210, 225, 255}

// Draw renders the spec onto img. It mutates img and returns nothing: a drawing
// function that returned a new image would double the allocation on a 640x384
// frame drawn six times a second, for no benefit.
//
// Everything here is bounded. A box smaller than two pixels is skipped, a line is
// clipped rather than drawn out of bounds, and every write is clamped. An overlay
// that could panic takes the pipeline down, and the pipeline is the thing the
// overlay is describing.
func Draw(img *image.RGBA, s Spec) {
	if img == nil {
		return
	}
	b := img.Bounds()
	drawHeader(img, s)

	// The lines first, so the boxes sit on top of them.
	for _, ln := range s.Lines {
		drawLine(img, ln, lineColour)
	}

	// Ghosts before the approved boxes, so a ghost can never cover an object the
	// pipeline actually approved.
	for _, g := range s.Ghosts {
		r := rectOf(g, b)
		if r.Dx() < 2 || r.Dy() < 2 {
			continue
		}
		drawOutline(img, r, dim(classify.FamilyOf(g.Class)))
	}

	chosen, _ := Select(s)
	for _, tb := range chosen {
		r := rectOf(tb.Box, b)
		if r.Dx() < 2 || r.Dy() < 2 {
			continue
		}
		fam := tb.Family()
		c := familyColour(fam)
		fillRect(img, r, c)
		drawOutline(img, r, c)
		drawMarker(img, r, c)
		drawVelocity(img, tb, b, c)
		labelBar(img, r, labelFor(tb), c, b)
	}
	if s.Note != "" {
		drawNote(img, s.Note)
	}
}

// DefaultMaxBoxes is what the screen uses when nothing declared a cap. It is the
// number the previous hard-coded constant used, kept so the picture does not
// change shape for an operator who has not set anything.
const DefaultMaxBoxes = 12

// Select is the CAP as a decision, separated from the drawing of it.
//
// It returns the boxes that will be drawn, strongest first, and the per-family
// count of what was left off. `Draw` calls it and the tests assert on it directly,
// which matters: proving a cap by counting drawn pixels is a test of the LABEL
// PLATES as much as of the cap, because a neighbour's plate lands on the very
// pixel you are measuring. The decision and the rasterising are different
// questions and they get different tests.
func Select(s Spec) (drawn []TrackedBox, suppressed map[classify.Family]int) {
	suppressed = map[classify.Family]int{}
	boxes := append([]TrackedBox(nil), s.Boxes...)
	sort.SliceStable(boxes, func(i, j int) bool { return boxes[i].Score > boxes[j].Score })
	limit := s.MaxBoxes
	if limit <= 0 {
		limit = DefaultMaxBoxes
	}
	// Boxes too small to draw are NOT suppression: a two-pixel box was never going
	// to be visible, and counting it as "not drawn" would make the note claim that
	// a family was suppressed when what happened is that one box was a speck. They
	// are skipped silently, and the decker's `dropped_degenerate` counter is where
	// that fact belongs.
	for _, tb := range boxes {
		if len(drawn) >= limit {
			suppressed[tb.Family()]++
			continue
		}
		drawn = append(drawn, tb)
	}
	return drawn, suppressed
}

// labelFor is the plate text: `VEHICLE · car · 0.87 · T12` (T8.2).
//
// Family, class, score, track. The family is first because it is read at a glance,
// the class is the precise word, the score is what a reader judges the detector
// with, and the track id is what a reader judges the TRACKER with — and a score
// with no track id beside it cannot be compared frame to frame.
func labelFor(tb TrackedBox) string {
	s := classify.Label(tb.Class)
	s += " · " + fixed2(tb.Score)
	if tb.Track > 0 {
		s += " · T" + itoa(int(tb.Track))
	}
	return s
}

func drawHeader(img *image.RGBA, s Spec) {
	b := img.Bounds()
	const barHeight = 22
	if b.Dy() <= barHeight*2 {
		return
	}
	draw.Draw(img, image.Rect(b.Min.X, b.Min.Y, b.Max.X, b.Min.Y+barHeight),
		&image.Uniform{headerColour}, image.Point{}, draw.Src)

	// The state is printed in words and only when it is known. An empty cell next
	// to a live picture reads as "fine", and "fine" is a claim nobody made. This
	// is D5: the screen used to hardcode `answered` and print it even for a frame
	// that was never asked.
	text := s.Provenance
	if s.AIStateKnown && s.AIState != "" {
		if text != "" {
			text += "  ·  "
		}
		text += "AI " + s.AIState
	}
	if s.At != "" {
		if text != "" {
			text += "  ·  "
		}
		text += s.At
	}
	if text == "" {
		return
	}
	labelBarAt(img, b.Min.X+4, b.Min.Y+barHeight-4, text, noteColour)
	drawVerdict(img, s)
}

// clip truncates a string to n characters with an ellipsis, which is what a
// banner needs: the model's summary is one sentence and the bar is 640 pixels
// wide, and a summary that overflows the bar is a summary that pushed the verdict
// off it.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

// verdictColour maps a verdict to a hue. It is a hue and never a colour alone,
// because the word is always printed beside it — the same rule as the family
// plates, and for the same reason.
func verdictColour(v string) color.RGBA {
	switch v {
	case "confirmed":
		return color.RGBA{90, 220, 130, 255}
	case "false_positive":
		return color.RGBA{200, 200, 200, 255}
	case "needs_review":
		return color.RGBA{255, 176, 32, 255}
	default:
		return noteColour
	}
}

// drawVerdict burns the model's answer into the picture. W2-8.
//
// The bar is at the BOTTOM and drawn with a plate, because the verdict is the
// single most-screenshotted part of this module and it has to survive being
// cropped to the road.
//
// Two rules it obeys without exception:
//
//   - An answer that does not exist is printed as an absence, never as an empty
//     cell. `no answer (cap_reached)` and a blank both read as "fine" otherwise,
//     and "fine" on a camera that was not asked is the worst impression this
//     module can leave.
//   - The push result is printed next to it. "The model said confirmed" and "the
//     VMS accepted the event" are different claims, and a screenshot that carries
//     only the first has been cropped to hide a failure.
func drawVerdict(img *image.RGBA, s Spec) {
	b := img.Bounds()
	const barHeight = 22
	if b.Dy() <= barHeight*2 {
		return
	}
	parts := make([]string, 0, 3)
	switch {
	case s.VerdictKnown && s.Verdict != "":
		parts = append(parts, "VERDICT "+s.Verdict)
	case s.VerdictKnown:
		parts = append(parts, "VERDICT none")
	default:
		parts = append(parts, "VERDICT not asked")
	}
	if s.PushResult != "" {
		parts = append(parts, s.PushResult)
	}
	if s.Counters != "" {
		parts = append(parts, s.Counters)
	}
	if summary := clip(s.VerdictSummary, 46); summary != "" {
		parts = append(parts, summary)
	}
	y := b.Max.Y - barHeight
	draw.Draw(img, image.Rect(b.Min.X, y, b.Max.X, b.Max.Y),
		&image.Uniform{headerColour}, image.Point{}, draw.Src)
	labelBarAt(img, b.Min.X+4, y+barHeight-4, strings.Join(parts, "  ·  "), verdictColour(s.Verdict))
}

// familyColour is D-6: the colour is the FAMILY, and the plate text always names
// the family in words beside it.
//
// Vehicle red, person cyan, animal amber, everything else grey. Four hues is what a
// viewer can hold in their head while watching a road; thirty shades of orange is
// noise. The words are not redundant with the colour: a colour-blind viewer, a
// screenshot printed in greyscale and a reader who has not learned the code all get
// the same answer from the text.
//
// To make every family one literal colour, change the four return values. One line
// each, and the tests below read the same function, so forcing a literal all-red
// does not need a second edit.
func familyColour(f classify.Family) color.RGBA {
	switch f {
	case classify.FamilyVehicle:
		return color.RGBA{255, 70, 70, 255}
	case classify.FamilyPerson:
		return color.RGBA{90, 220, 255, 255}
	case classify.FamilyAnimal:
		return color.RGBA{255, 176, 32, 255}
	default:
		return color.RGBA{170, 170, 180, 255}
	}
}

// FamilyColour is the exported form, for a caller that needs to colour a legend or
// a table cell with the same value the picture uses. It returns the same colour
// `Draw` would use, so a legend cannot drift from the boxes.
func FamilyColour(f classify.Family) color.RGBA { return familyColour(f) }

// dim is a family colour at a third of its strength, for a ghost outline. A ghost
// that is as loud as an approved box is not a ghost.
func dim(f classify.Family) color.RGBA {
	c := familyColour(f)
	const keep = 110
	return color.RGBA{
		R: uint8(int(c.R) * keep / 255),
		G: uint8(int(c.G) * keep / 255),
		B: uint8(int(c.B) * keep / 255),
		A: 255,
	}
}

func rectOf(bx Box, bounds image.Rectangle) image.Rectangle {
	return image.Rect(
		scale(bx.X1, bounds.Dx()),
		scale(bx.Y1, bounds.Dy()),
		scale(bx.X2, bounds.Dx()),
		scale(bx.Y2, bounds.Dy()),
	).Intersect(bounds)
}

func scale(norm float64, extent int) int {
	if norm < 0 {
		norm = 0
	}
	if norm > 1.5 {
		norm = 1.5
	}
	return int(norm * float64(extent))
}
