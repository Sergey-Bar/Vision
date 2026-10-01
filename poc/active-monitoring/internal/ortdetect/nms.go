package ortdetect

import (
	"math"
	"sort"
)

// Two suppression passes, mirroring pyworker/server.py's `nms` and `nms_global`
// pair exactly. The parity test asserts the two implementations agree, so every
// threshold, comparison and tie-break here is the one the Python does — including
// the parts that look arbitrary.
//
// # Why one pass is not enough
//
// A single class-aware NMS at 0.45 leaves BOTH a `car` box and a `truck` box on
// ONE object whenever the two class scores are close. The run then reports two
// vehicles where there is one.
//
// The second, class-agnostic pass is what removes it: the two boxes overlap each
// other completely, and a pass that does not care which class won collapses them
// to the higher score. A pass that cares about the class CANNOT — it never
// compares across classes, so a `car`/`truck` pair is invisible to it by
// construction, not by accident.
//
// That is why the global threshold is the LOOSER of the two (0.75 against 0.45)
// and why the configuration validator refuses an inverted pair. Inverted, the
// global pass is redundant with the first and the cross-class pair survives.

// defaultNMSClass and defaultNMSGlobal are the values the Python sidecar ships
// with. They are defaults here for PROBE and TEST; a run reads them from
// SP_POC_DET_NMS_CLASS and SP_POC_DET_NMS_GLOBAL, which are required by name.
const (
	defaultNMSClass  = 0.45
	defaultNMSGlobal = 0.75
)

// iou is the intersection over union of two normalised boxes.
//
// Zero on no overlap, and zero on a degenerate box rather than a division by
// zero. A degenerate box has no area, so it has no overlap with anything — which
// is the correct answer, and returning NaN would make every comparison against it
// false in a way that depends on which side of the comparison you were on.
//
// This mirrors the Python line for line, including the max(0, ...) on the
// intersection: two boxes that only touch along an edge have zero intersection
// area, not a negative one.
func iou(a, b [4]float64) float64 {
	ix1, iy1 := math.Max(a[0], b[0]), math.Max(a[1], b[1])
	ix2, iy2 := math.Min(a[2], b[2]), math.Min(a[3], b[3])
	iw, ih := math.Max(0, ix2-ix1), math.Max(0, iy2-iy1)
	inter := iw * ih
	if inter <= 0 {
		return 0
	}
	areaA := math.Max(0, a[2]-a[0]) * math.Max(0, a[3]-a[1])
	areaB := math.Max(0, b[2]-b[0]) * math.Max(0, b[3]-b[1])
	union := areaA + areaB - inter
	if union <= 0 {
		return 0
	}
	return inter / union
}

// suppressed is one candidate at one pass: a box, a score, a class index and the
// position it came from, so the second pass can find the class again.
type suppressed struct {
	box      [4]float64
	score    float64
	class    int
	original int
}

// nmsClassAware is plain greedy NMS, per class.
//
// Greedy and not Hungarian, and the reason is scale rather than elegance: at
// 100 boxes per frame with a 0.45 threshold, an optimal assignment buys a
// percentage point and costs an algorithm nobody in this repository could read.
// The tracker downstream is greedy for the same reason.
//
// Ties in score are broken by INDEX, not by map order. The Python sorts by
// -score and numpy's argsort is stable, so equal scores keep their original
// order; the parity test depends on this and an unstable sort would make it pass
// or fail at random.
func nmsClassAware(cands []suppressed, iouThr float64) []suppressed {
	keep := make([]suppressed, 0, len(cands))
	taken := make([]bool, len(cands))
	order := sortedByScore(cands)

	for _, i := range order {
		if taken[i] {
			continue
		}
		keep = append(keep, cands[i])
		for _, j := range order {
			if taken[j] || j == i {
				continue
			}
			if cands[j].class != cands[i].class {
				continue
			}
			if iou(cands[i].box, cands[j].box) > iouThr {
				taken[j] = true
			}
		}
	}
	return keep
}

// nmsGlobal is class-agnostic greedy NMS, and it is the pass that catches the
// `car`/`truck` pair the class-aware pass structurally cannot.
//
// The surviving box keeps its ORIGINAL class, recovered by position. The Python
// recovers it by rounding the box coordinates to nine decimals and looking the
// geometry up in a dict; this recovers it by index, which is exact. The two agree
// because the index map is a bijection of the same list — and the index is
// strictly better, because a geometry key cannot tell two identical boxes apart
// and two identical boxes on one object is precisely the case this pass exists
// to resolve.
func nmsGlobal(cands []suppressed, iouThr float64) []suppressed {
	keep := make([]suppressed, 0, len(cands))
	taken := make([]bool, len(cands))
	order := sortedByScore(cands)

	for _, i := range order {
		if taken[i] {
			continue
		}
		keep = append(keep, cands[i])
		for _, j := range order {
			if taken[j] || j == i {
				continue
			}
			if iou(cands[i].box, cands[j].box) > iouThr {
				taken[j] = true
			}
		}
	}
	return keep
}

// sortedByScore returns the indices of cands in descending score order, with
// EQUAL SCORES IN ORIGINAL ORDER.
//
// The stability is load-bearing rather than tidy. Two decoders that disagree only
// on which of two equal-scoring boxes survives will disagree on a different box
// each frame, and the cross-decoder parity test would fail intermittently on a
// run that is in fact correct. A sort that is merely unspecified produces a
// non-deterministic answer, and a non-deterministic answer cannot be tested.
func sortedByScore(cands []suppressed) []int {
	order := make([]int, len(cands))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return cands[order[a]].score > cands[order[b]].score
	})
	return order
}
