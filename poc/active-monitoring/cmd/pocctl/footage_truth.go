package main

// footage_truth.go - the OPTIONAL ground-truth boxes, and what happens without
// them.
//
// # There are none, and that is the finding
//
// `testdata/labels.json` records provenance, a line and an expected time window
// per clip. It records no object boxes, and this module may not invent any: a box
// drawn by looking at the picture and then used as the answer is not ground truth,
// it is a guess with a file name on it, and a per-class precision computed against
// a guess is a measurement of the guess.
//
// So `pocctl footage` takes a ground-truth file as an OPTIONAL input and, without
// one, prints `NOT RUN` for precision and recall with the reason attached. It
// does not print 0.000 and it does not fall back to the whole frame as a single
// ground-truth box, which is the tempting thing to do and would report a recall
// of 1.000 over a frame nobody annotated.
//
// # The file
//
//	{
//	  "material": "injection-hostile.mp4",
//	  "note": "free text, printed verbatim",
//	  "boxes": [
//	    {"at": 3.0, "class": "car", "x1": 0.21, "y1": 0.30, "x2": 0.33, "y2": 0.52}
//	  ]
//	}
//
// `at` is a second from the start of the window, and a box is attached to the
// sampled frame nearest it - but only within HALF the sampling interval. A box
// 2.5 seconds from a sampled frame is not a box on that frame; attaching it
// anyway would manufacture a miss on a frame the annotator never described, and a
// recall that penalises the annotator's timing is a recall about the interval
// rather than about the detector.
//
// The tolerance is declared and PRINTED, and a box that matched no frame is
// counted and reported rather than discarded.

import (
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/bench"
	"github.com/Sergey-Bar/ai-captain-poc/internal/ortdetect"
)

// truthFile is a parsed ground-truth box file.
type truthFile struct {
	Material string     `json:"material"`
	Note     string     `json:"note"`
	Boxes    []truthBox `json:"boxes"`

	// path is where it was read from, printed so the number can be tied to a file.
	path string
	// classes counts each class once, for the header.
	classes map[string]int
	// unmatched counts boxes that fell outside half an interval of every sampled
	// frame. Counted and reported, because a box that matched nothing is a fact
	// about the interval the operator chose and not about the detector.
	unmatched int
}

type truthBox struct {
	// At is seconds from the start of the WINDOW, not from the start of the file.
	// The window is what was declared and what was sampled, so a box is dated in
	// the same clock the reader used.
	At    float64 `json:"at"`
	Class string  `json:"class"`
	X1    float64 `json:"x1"`
	Y1    float64 `json:"y1"`
	X2    float64 `json:"x2"`
	Y2    float64 `json:"y2"`
}

// readTruthFile reads the ground-truth boxes, or explains why there are none.
//
// An absent `--truth` is NOT an error. It is the normal state of this repository,
// and the command's job is then to measure everything else and say plainly that
// the rates are not measurable. Refusing to run at all would throw away the
// arithmetic, which IS measurable, over the absence of something that is not.
//
// A `--truth` that was NAMED and cannot be read IS an error. The operator asked
// for ground truth and did not get it, and continuing would print NOT RUN for the
// rates as though they had not asked.
func readTruthFile(path string) (*truthFile, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ground truth was named with --truth and could not be read: %w. This is an error "+
			"rather than an absence: the operator asked for ground truth and did not get it, and continuing "+
			"would print NOT RUN for the rates as though nobody had asked", err)
	}
	var t truthFile
	if err := jsonUnmarshal(raw, &t); err != nil {
		return nil, fmt.Errorf("%s is not readable as a ground-truth box file: %w", path, err)
	}
	t.path = path
	if len(t.Boxes) == 0 {
		return nil, fmt.Errorf("%s holds no boxes. A ground-truth file with an empty `boxes` array is a "+
			"measurement whose recall denominator is zero, and it is refused here rather than printed as a "+
			"recall of nothing", path)
	}
	t.classes = map[string]int{}
	for i, b := range t.Boxes {
		if b.Class == "" {
			return nil, fmt.Errorf("%s: box %d has no class. A ground-truth box with no class cannot go in a "+
				"per-CLASS table, and guessing one from the picture is the guess this file exists to refuse", path, i)
		}
		if b.X2 <= b.X1 || b.Y2 <= b.Y1 {
			return nil, fmt.Errorf("%s: box %d has no extent (%.3f,%.3f)-(%.3f,%.3f). A box with no area matches "+
				"nothing and would count as a miss on every frame", path, i, b.X1, b.Y1, b.X2, b.Y2)
		}
		t.classes[b.Class]++
	}
	return &t, nil
}

// classesNamed returns the classes in the file, sorted, for the header.
func (t *truthFile) classesNamed() []string {
	out := make([]string, 0, len(t.classes))
	for c := range t.classes {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// expressible reports whether this build has a word for a ground-truth class.
//
// An unexpressible class is counted as DROPPED and kept out of the recall
// denominator, which is [bench.GT.Expressible]'s rule and not a new one. The
// vocabulary is COCO-80 because that is what `internal/ortdetect` names when a
// model declares none, and this build's model declares none.
func expressible(class string) bool {
	for i := 0; i < ortdetect.NumClasses; i++ {
		if ortdetect.ClassName(i) == class {
			return true
		}
	}
	return false
}

// at returns the ground-truth boxes for the sampled frame AT, within half an
// interval.
func (t *truthFile) at(at time.Duration, every time.Duration) []bench.GT {
	tolerance := every / 2
	var out []bench.GT
	for _, b := range t.Boxes {
		d := time.Duration(b.At*float64(time.Second)) - at
		if d < 0 {
			d = -d
		}
		if d > tolerance {
			// Outside the tolerance. Counted, not silently dropped: a box that
			// matched no sampled frame is a fact about the interval the operator
			// chose, and it is reported beside the rates.
			t.unmatched++
			continue
		}
		out = append(out, bench.GT{
			Box:         bench.Box{X1: b.X1, Y1: b.Y1, X2: b.X2, Y2: b.Y2},
			Class:       b.Class,
			Expressible: expressible(b.Class),
		})
	}
	return out
}

// describeTruth is the one-sentence header, and it states the absence.
func describeTruth(t *truthFile, err error, win footageWindow) string {
	if err != nil {
		return fmt.Sprintf("%s: %v", notRun, err)
	}
	if t == nil {
		return notRun + " - no --truth was given, and this repository has no ground-truth object boxes for " +
			"arbitrary footage. testdata/labels.json records provenance, a line and an expected time window, " +
			"and no boxes; a box drawn by looking at the picture is not ground truth."
	}
	return fmt.Sprintf("%d box(es) from %s across %d class(es) (%s), each attached to the sampled frame "+
		"nearest it within half the %v interval. Classes this build cannot name are counted as DROPPED, "+
		"never as a miss.",
		len(t.Boxes), t.path, len(t.classes), joinNames(t.classesNamed()), win.Every)
}

func joinNames(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}
