package detect

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Crossing is one decision: a tracked object went from one side of a line to
// the other, once, and that is the event. Exactly one Crossing is produced per
// pass, per track, per line, no matter how many frames the pass takes.
type Crossing struct {
	TrackID   int
	Class     string
	Line      string
	Direction string
	At        time.Time
	Box       Box
	// Frames is how many frames the track existed before the crossing. It is in
	// the event because "a box that appeared on the far side of the line" and
	// "a tracked object crossed" are different facts, and only the second is an
	// event anyone wants.
	Frames int
}

// Track is a live tracked object, for the screen's overlay and pulse rows.
type Track struct {
	ID    int
	Class string
	Box   Box
	Seen  int
	First time.Time
	Last  time.Time
	// Side is the current side of each line, and Latched records that a crossing
	// has already been emitted for the current pass so the same crossing is not
	// emitted on every frame it stays on the far side.
	Side    map[string]int
	Latched map[string]bool
}

// Stats are the per-frame counters, printed in the report as the detection
// rate. They are counters of what the engine saw, not of what happened in the
// world.
type Stats struct {
	Offered     int
	Approved    int
	Rejected    int
	Tracks      int
	TracksAlive int
}

// Config are the decision parameters. Every one of them is set from the
// environment by internal/config; none has a default that changes a verdict.
type Config struct {
	ScoreThreshold float64
	Classes        []string
	IoU            float64
	MaxAge         time.Duration
	Cooldown       time.Duration
	// Tolerance is the band around a line, in normalised perpendicular
	// distance, inside which a track's side does not change. Without it a track
	// sitting on the line would flip on floating-point noise and the crossing
	// count would not be reproducible.
	Tolerance float64
}

// Engine holds the tracker, the line-crossing state machines and the cooldowns.
// It is not safe for concurrent use: it is called from the single pipeline
// goroutine, next to the clock, and giving it a lock would only hide a second
// reader of time.
type Engine struct {
	zone Zone
	cfg  Config
	ok   map[string]bool
	mu   sync.Mutex // guards nextID only, so tests can run in parallel
	next int
	live map[int]*Track
	// cooldown is per (line, class), not per track: a new track for the same
	// object after a brief occlusion should not get a second event inside the
	// window, and a per-track key would let it.
	cooldown map[string]time.Time
}

// New builds an engine. It refuses an empty approved class list, because an
// engine that approves nothing produces a clean run that measured nothing.
func New(zone Zone, cfg Config) (*Engine, error) {
	if err := zone.Validate(); err != nil {
		return nil, fmt.Errorf("detect: %w", err)
	}
	if len(cfg.Classes) == 0 {
		return nil, fmt.Errorf("detect: the approved class list is empty, so no box can reach the line and a run would report zero events as if the model had found nothing")
	}
	if cfg.IoU <= 0 || cfg.IoU > 1 {
		return nil, fmt.Errorf("detect: the association threshold %v is not an IoU", cfg.IoU)
	}
	if cfg.Tolerance < 0 {
		return nil, fmt.Errorf("detect: the line tolerance %v is negative", cfg.Tolerance)
	}
	ok := make(map[string]bool, len(cfg.Classes))
	for _, c := range cfg.Classes {
		ok[strings.ToLower(strings.TrimSpace(c))] = true
	}
	return &Engine{
		zone:     zone,
		cfg:      cfg,
		ok:       ok,
		live:     map[int]*Track{},
		cooldown: map[string]time.Time{},
	}, nil
}

// Feed is one frame's worth of boxes at one instant. It returns the crossings
// decided by this frame, the live tracks after the update, and the counters.
//
// The instant is supplied by the caller because the caller owns the clock. This
// package never calls time.Now: a second clock is a second opinion about when
// something happened, and two opinions is how a clip ends up pointing at the
// wrong second.
func (e *Engine) Feed(at time.Time, boxes []Box) ([]Crossing, []Track, Stats) {
	var st Stats
	st.Offered = len(boxes)

	approved := make([]Box, 0, len(boxes))
	for _, b := range boxes {
		switch {
		case !b.Valid():
			st.Rejected++
		case b.Score < e.cfg.ScoreThreshold:
			st.Rejected++
		case !e.ok[strings.ToLower(b.Class)]:
			st.Rejected++
		case !e.inZone(b):
			st.Rejected++
		default:
			approved = append(approved, b)
		}
	}
	st.Approved = len(approved)

	// Retire tracks that have not been seen for MaxAge, BEFORE associating.
	// Associating first would let a track that vanished for a minute be matched
	// to whatever appears now, and a re-appearing object after a long occlusion
	// is a new track, not a revival of the old one.
	for id, t := range e.live {
		if at.Sub(t.Last) > e.cfg.MaxAge {
			delete(e.live, id)
		}
	}

	// Associate with existing tracks, greedily by best IoU. A greedy pass is the
	// right size for this: the alternative is an assignment problem whose
	// optimum nobody here can verify, and a slightly worse association costs
	// nothing that the report claims to measure.
	used := make(map[int]bool, len(approved))
	for _, t := range e.live {
		used[t.ID] = true
	}
	type pair struct {
		track int
		box   int
		score float64
	}
	var pairs []pair
	for ti, t := range e.live {
		_ = ti
		for bi, b := range approved {
			if t.Class != b.Class {
				continue
			}
			if v := iou(t.Box, b); v >= e.cfg.IoU {
				pairs = append(pairs, pair{track: t.ID, box: bi, score: v})
			}
		}
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].score > pairs[j].score })
	matchedBoxes := map[int]int{}
	assigned := map[int]int{}
	for _, p := range pairs {
		if _, ok := assigned[p.track]; ok {
			continue
		}
		if _, ok := matchedBoxes[p.box]; ok {
			continue
		}
		matchedBoxes[p.box] = p.track
		assigned[p.track] = p.box
	}

	for id, bi := range assigned {
		t := e.live[id]
		t.Box = approved[bi]
		t.Seen++
		t.Last = at
	}

	for bi, b := range approved {
		if _, ok := matchedBoxes[bi]; ok {
			continue
		}
		e.next++
		e.live[e.next] = &Track{
			ID: e.next, Class: b.Class, Box: b, Seen: 1, First: at, Last: at,
			Side: map[string]int{}, Latched: map[string]bool{},
		}
	}

	// Retire anything that was never associated and has aged out, which can
	// only be tracks that already existed before this frame.
	for id, t := range e.live {
		if at.Sub(t.Last) > e.cfg.MaxAge {
			delete(e.live, id)
		}
	}

	var crossings []Crossing
	for _, t := range sortedTracks(e.live) {
		crossings = append(crossings, e.step(at, t)...)
	}
	for i, t := range e.live {
		if t.Seen == 0 {
			delete(e.live, i)
		}
	}

	alive := e.live
	out := make([]Track, 0, len(alive))
	for _, t := range sortedTracks(alive) {
		out = append(out, *t)
	}
	st.TracksAlive = len(alive)
	return crossings, out, st
}

// step runs the line-crossing state machine for one track against every line.
func (e *Engine) step(at time.Time, t *Track) []Crossing {
	cx, cy := t.Box.Centre()
	var out []Crossing
	for _, l := range e.zone.Lines {
		if !e.lineApplies(l, t.Class) {
			continue
		}
		sign, dist := l.side(cx, cy)

		prev, had := t.Side[l.Name]
		// Inside the band: the side is unknown, and an unknown side is not a
		// crossing. Holding the previous side is what stops a track parked on
		// the line from generating a stream of events.
		if dist < e.cfg.Tolerance {
			if had {
				t.Side[l.Name] = prev
			}
			continue
		}
		if !had {
			t.Side[l.Name] = sign
			continue
		}
		if sign == prev {
			t.Side[l.Name] = sign
			continue
		}
		// The side changed. That is a crossing, unless this pass already emitted
		// one for this line, or the cooldown for this line and class has not
		// elapsed.
		if t.Latched[l.Name] {
			// Re-arm only once the object is back on the original side, so a
			// second real crossing in the same direction is still counted.
			t.Side[l.Name] = sign
			continue
		}
		if last, ok := e.cooldown[cooldownKey(l.Name, t.Class)]; ok && at.Sub(last) < e.cfg.Cooldown {
			t.Side[l.Name] = sign
			continue
		}
		t.Latched[l.Name] = true
		e.cooldown[cooldownKey(l.Name, t.Class)] = at
		out = append(out, Crossing{
			TrackID: t.ID, Class: t.Class, Line: l.Name, Direction: l.Direction,
			At: at, Box: t.Box, Frames: t.Seen,
		})
		t.Side[l.Name] = sign
	}
	// Re-arm a latched line when the object returns to the far side of it.
	for _, l := range e.zone.Lines {
		if !t.Latched[l.Name] {
			continue
		}
		other := 0
		if v, ok := t.Side[l.Name]; ok {
			other = -v
		}
		sign, dist := l.side(cx, cy)
		if other != 0 && sign == other && dist >= e.cfg.Tolerance {
			t.Latched[l.Name] = false
		}
	}
	return out
}

func cooldownKey(line, class string) string { return line + "|" + class }

func (e *Engine) lineApplies(l Line, class string) bool {
	if len(l.Classes) == 0 {
		return true
	}
	class = strings.ToLower(class)
	for _, c := range l.Classes {
		if strings.ToLower(c) == class {
			return true
		}
	}
	return false
}

// inZone applies the polygon restrictions, if the zone has any.
func (e *Engine) inZone(b Box) bool {
	if len(e.zone.Polygons) == 0 {
		return true
	}
	cx, cy := b.Centre()
	for _, p := range e.zone.Polygons {
		if !p.contains(cx, cy) {
			continue
		}
		if len(p.Classes) == 0 {
			return true
		}
		for _, c := range p.Classes {
			if strings.EqualFold(c, b.Class) {
				return true
			}
		}
	}
	return false
}

func sortedTracks(m map[int]*Track) []*Track {
	out := make([]*Track, 0, len(m))
	for _, t := range m {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func iou(a, b Box) float64 {
	ix1, iy1 := maxF(a.X1, b.X1), maxF(a.Y1, b.Y1)
	ix2, iy2 := minF(a.X2, b.X2), minF(a.Y2, b.Y2)
	iw, ih := ix2-ix1, iy2-iy1
	if iw <= 0 || ih <= 0 {
		return 0
	}
	inter := iw * ih
	ua := a.Width()*a.Height() + b.Width()*b.Height() - inter
	if ua <= 0 {
		return 0
	}
	return inter / ua
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// LiveTracks returns the current tracks, for the screen.
func (e *Engine) LiveTracks() []Track {
	return e.snapshot()
}

func (e *Engine) snapshot() []Track {
	out := make([]Track, 0, len(e.live))
	for _, t := range sortedTracks(e.live) {
		out = append(out, *t)
	}
	return out
}
