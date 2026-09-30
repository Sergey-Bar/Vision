package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"sort"
	"strings"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/soak"
	"github.com/Sergey-Bar/ai-captain-poc/internal/store"
)

// DrawOverlay draws the boxes and lines onto the last frame and encodes a JPEG.
// The drawing happens here, on the server, in Go: the pixels already exist in
// this process and a browser drawing them back would be a second reader of the
// video.
func DrawOverlay(l Live) ([]byte, error) {
	if len(l.Frame) == 0 {
		return nil, fmt.Errorf("ui: no frame to draw on")
	}
	src, err := jpeg.Decode(bytes.NewReader(l.Frame))
	if err != nil {
		return nil, fmt.Errorf("ui: the last frame is not decodable: %w", err)
	}
	bounds := src.Bounds()
	canvas := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(canvas, canvas.Bounds(), src, bounds.Min, draw.Src)

	// A header bar, so the provenance and the AI state are on the picture and
	// not only in the page's margin: a screenshot of the frame is still honest.
	barHeight := 22
	if canvas.Bounds().Dy() > barHeight*2 {
		draw.Draw(canvas, image.Rect(0, 0, canvas.Bounds().Dx(), barHeight), &image.Uniform{color.RGBA{20, 20, 20, 200}}, image.Point{}, draw.Src)
	}

	// The lines first, so the boxes sit on top of them.
	for _, ln := range l.Lines {
		drawLine(canvas, ln, color.RGBA{90, 200, 255, 255})
	}
	// The boxes.
	//
	// A thirty-vehicle frame is thirty boxes, and thirty unfilled outlines over a
	// road is a picture nobody can read: every line crosses every other and the
	// image reads as noise. Three things make it legible, and all three are
	// about restraint rather than about drawing more:
	//
	//   - a TRANSLUCENT FILL, so the pixels inside the box are still visible.
	//     An outline says "somewhere in here"; a fill says "this is the object".
	//   - ONE label per box, carrying the class and the score, on a solid bar.
	//     A bare label over a road is unreadable, and two labels is worse.
	//   - a CAP, highest score first. A viewer needs the strongest detections,
	//     not all of them, and the count of what was suppressed is printed rather
	//     than silently dropped.
	const maxBoxes = 8
	boxes := append([]OverlayBox(nil), l.Boxes...)
	sort.SliceStable(boxes, func(i, j int) bool { return boxes[i].Score > boxes[j].Score })
	drawn := boxes
	suppressed := 0
	if len(drawn) > maxBoxes {
		suppressed = len(drawn) - maxBoxes
		drawn = drawn[:maxBoxes]
	}
	seen := map[OverlayBox]bool{}
	for _, b := range drawn {
		rect := image.Rect(
			scale(b.X1, canvas.Bounds().Dx()),
			scale(b.Y1, canvas.Bounds().Dy()),
			scale(b.X2, canvas.Bounds().Dx()),
			scale(b.Y2, canvas.Bounds().Dy()),
		)
		if rect.Dx() < 2 || rect.Dy() < 2 || seen[b] {
			continue
		}
		seen[b] = true
		c := boxColour(b)
		fillRect(canvas, rect, c)
		drawOutline(canvas, rect, c)
		labelBar(canvas, rect.Min.X, rect.Min.Y, fmt.Sprintf("%s %.2f", b.Class, b.Score), c)
	}
	if suppressed > 0 {
		drawNote(canvas, fmt.Sprintf("+%d lower-scoring detections not drawn", suppressed))
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, canvas, &jpeg.Options{Quality: 80}); err != nil {
		return nil, fmt.Errorf("ui: the drawn frame could not be encoded: %w", err)
	}
	return buf.Bytes(), nil
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

// boxColour is a FAMILY colour, not a per-class one. Thirty shades of orange are
// noise; a few strong hues let a viewer see "these are people, these are
// vehicles" at a glance, which is the only thing the colour is for.
func boxColour(b OverlayBox) color.RGBA {
	switch strings.ToLower(b.Class) {
	case "person", "pedestrian":
		return color.RGBA{90, 200, 255, 255}
	case "bicycle", "motorcycle":
		return color.RGBA{180, 140, 255, 255}
	case "bus", "truck":
		return color.RGBA{255, 140, 90, 255}
	case "traffic light", "stop sign", "fire hydrant":
		return color.RGBA{255, 110, 200, 255}
	default:
		return color.RGBA{255, 215, 70, 255}
	}
}

// fillRect is a translucent wash. alphaBlend is not available on every path and
// is slower than it looks on a 640x360 frame at six frames a second, so the
// blend is done by hand over the box's own pixels.
func fillRect(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	b := img.Bounds()
	x0, y0 := clamp(r.Min.X, b.Min.X, b.Max.X), clamp(r.Min.Y, b.Min.Y, b.Max.Y)
	x1, y1 := clamp(r.Max.X, b.Min.X, b.Max.X), clamp(r.Max.Y, b.Min.Y, b.Max.Y)
	const a = 20
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			o := img.PixOffset(x, y)
			img.Pix[o+0] = uint8((int(img.Pix[o+0])*(255-a) + int(c.R)*a) / 255)
			img.Pix[o+1] = uint8((int(img.Pix[o+1])*(255-a) + int(c.G)*a) / 255)
			img.Pix[o+2] = uint8((int(img.Pix[o+2])*(255-a) + int(c.B)*a) / 255)
		}
	}
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// drawNote puts a line at the bottom left, for anything the picture is choosing
// not to show. A viewer who can see that 12 boxes were left off is being told;
// one who cannot is being misled by an omission.
func drawNote(img *image.RGBA, text string) {
	labelBar(img, 4, img.Bounds().Dy()-4, text, color.RGBA{200, 210, 225, 255})
}

func drawOutline(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	drawHLine(img, r.Min.X, r.Max.X, r.Min.Y, c)
	drawHLine(img, r.Min.X, r.Max.X, r.Max.Y, c)
	drawVLine(img, r.Min.Y, r.Max.Y, r.Min.X, c)
	drawVLine(img, r.Min.Y, r.Max.Y, r.Max.X, c)
}

func drawHLine(img *image.RGBA, x0, x1, y int, c color.RGBA) {
	if y < 0 || y >= img.Bounds().Dy() {
		return
	}
	for x := x0; x <= x1; x++ {
		if x >= 0 && x < img.Bounds().Dx() {
			img.SetRGBA(x, y, c)
		}
	}
}

func drawVLine(img *image.RGBA, y0, y1, x int, c color.RGBA) {
	if x < 0 || x >= img.Bounds().Dx() {
		return
	}
	for y := y0; y <= y1; y++ {
		if y >= 0 && y < img.Bounds().Dy() {
			img.SetRGBA(x, y, c)
		}
	}
}

func drawLine(img *image.RGBA, l OverlayLine, c color.RGBA) {
	b := img.Bounds()
	x0, y0 := scale(l.A[0], b.Dx()), scale(l.A[1], b.Dy())
	x1, y1 := scale(l.B[0], b.Dx()), scale(l.B[1], b.Dy())
	// Bresenham: the lines are a handful of pixels wide and there is no
	// dependency for a line.
	dx := abs(x1 - x0)
	dy := -abs(y1 - y0)
	sx, sy := -1, -1
	if x0 < x1 {
		sx = 1
	}
	if y0 < y1 {
		sy = 1
	}
	err := dx + dy
	for {
		img.SetRGBA(clampX(x0, b.Dx()), clampY(y0, b.Dy()), c)
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func clampX(x, w int) int {
	if x < 0 {
		return 0
	}
	if x >= w {
		return w - 1
	}
	return x
}

func clampY(y, h int) int {
	if y < 0 {
		return 0
	}
	if y >= h {
		return h - 1
	}
	return y
}

func inputsFrom(d Data) soak.Inputs {
	in := soak.Inputs{
		Provenance:            d.Live.Provenance,
		PushTargetIsSimulator: d.Live.Simulator,
		SourceKind:            d.Live.Source,
		EvidenceStored:        d.Evidence.Stored,
		EvidenceVerified:      d.Evidence.Verified,
		EvidenceMismatched:    d.Evidence.Mismatched,
		PushAttempts:          d.Push.Total,
		PushesWithResult:      d.Push.WithResult,
		UnattributedPushes:    d.Push.Unattributed,
		DeadLettered:          d.Push.DeadLetter,
		HeartbeatCount:        d.Heartbeats,
		ResourceSamples:       d.Peaks.Samples,
		PeakRSSBytes:          d.Peaks.RSSBytes,
		PeakDBBytes:           d.Peaks.DB,
		PeakDiskBytes:         d.Peaks.DiskBytes,
		ProcessExits:          d.Counts["process_exit"],
	}
	for _, g := range d.Gaps {
		in.HeartbeatGaps = append(in.HeartbeatGaps, soak.GapMeasurement{From: g.From, To: g.To, Seconds: g.Seconds})
	}
	for _, s := range d.Stalled {
		in.StalledDetections = append(in.StalledDetections, soak.StalledDetection{
			ID: s.ID, Status: s.Status, Age: time.Since(s.UpdatedAt),
		})
	}
	in.TotalDetections = 0
	for _, v := range d.Counts {
		in.TotalDetections += v
	}
	if d.Run != nil {
		in.Target = time.Duration(d.Run.TargetSeconds) * time.Second
		end := time.Now().UTC()
		if d.Run.EndedAt != nil {
			end = *d.Run.EndedAt
		}
		in.Elapsed = end.Sub(d.Run.StartedAt)
	}
	// The screen does not know the declared ceilings or the rehearsal outcomes;
	// those live in the run's configuration, and a screen that guessed them
	// would be a screen scoring against numbers nobody declared.
	return in
}

func verdictOf(in soak.Inputs) soak.Verdict {
	return soak.Overall(in, soak.Score(in)).Verdict
}

func criteriaFor(d Data) []soak.Criterion {
	in := inputsFrom(d)
	cs := soak.Score(in)
	out := make([]soak.Criterion, 0, len(cs))
	for _, c := range cs {
		// The screen labels the criteria it cannot fully compute as NOT RUN with
		// a reason, rather than showing a number it got from nowhere.
		if !hasDeclaredCeilings(d) && (c.ID == "AC5" || c.ID == "AC6" || c.ID == "AC7") {
			c.Verdict = soak.VerdictNotRun
			c.Missing = "the screen reads only the run's rows; the declared ceilings and the rehearsal outcomes live in the run configuration, and the screen will not invent them"
			c.Observed = "not computed on the screen"
			c.Clauses = nil
		}
		out = append(out, c)
	}
	return out
}

func hasDeclaredCeilings(d Data) bool {
	// The peaks table carries samples; the ceilings live in the process
	// configuration. A run with no samples at all cannot be scored at all.
	return d.Peaks.Samples > 0
}

var _ = store.Terminal
