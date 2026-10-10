package siteconfig

import (
	"fmt"
	"sort"
)

// learnZones turns a camera's observed occupancy into zone proposals.
//
// The survey's complaint is the days of video an operator combs through after
// an event; the other side of that is the configuration nobody derived from
// the watching that already happened. This is that derivation, and it is
// deliberately the simplest thing that can produce a polygon: grid binning.
// Not k-means, not a clustering library — a grid, a threshold and a bounding
// box, because a site operator has to be able to look at a proposal and see
// what it came from.
//
// The rules that keep a learned zone safe:
//
//   - It is `learned`, with its evidence attached (the cell counts), never
//     `declared`. A human promotes it or it stays a proposal.
//   - It is typed `monitor` unless the caller says otherwise; a learned zone
//     is never silently critical, because the cost of a wrong alarm in a
//     restricted zone is a control room that stops trusting the screen.
//   - Declared wins on overlap: the caller drops a proposal that overlaps a
//     declared zone, because the declaration is the record of what a human
//     already decided.

// Cell is one grid cell of one camera.
type Cell struct {
	Row int `json:"row"`
	Col int `json:"col"`
}

// Observation is one captured frame's occupancy: the grid cells the frame's
// detections covered. Empty cells — a frame with no detections — contribute
// nothing, which is what makes the occupancy count a dwell measure rather
// than a motion one.
type Observation struct {
	CameraID string `json:"camera_id"`
	Cells    []Cell `json:"cells"`
}

// LearnConfig configures the grid binning.
type LearnConfig struct {
	GridW  int // grid columns
	GridH  int // grid rows
	FrameW float64
	FrameH float64
	// MinOccupancyFrames is how many of the window's frames a cell must have
	// been occupied in to count as dwelled-in.
	MinOccupancyFrames int
	// MinCells is how many cells a cluster needs before it is a zone rather
	// than a blip.
	MinCells int
	// WindowFrames is how many frames the window covered, which is what the
	// confidence is a fraction of.
	WindowFrames int
	// ProposedType is what a learned zone is typed as. Empty means monitor,
	// which is the ceiling: a learned zone is never silently critical.
	ProposedType string
}

// DefaultLearnConfig is the binning the demo runs with: a 16x9 grid over a
// 1280x720 frame, a fifth of the window occupied before a cell counts, four
// cells before a cluster is a zone.
func DefaultLearnConfig() LearnConfig {
	return LearnConfig{
		GridW: 16, GridH: 9,
		FrameW: 1280, FrameH: 720,
		MinOccupancyFrames: 5,
		MinCells:           4,
		WindowFrames:       60,
	}
}

// learnZonesFromObservations proposes zones from one camera's observations. Deterministic:
// the same observations and config produce the same proposals in the same
// order, whatever order the observations arrived in.
//
// It is the per-frame form; LearnZonesFromCube is the aggregate form. This
// delegates to it, because the two carry the same per-cell counts: a frame that
// covered four cells contributes +1 to each, exactly as four frames that
// covered one cell each do. That equivalence is what lets a capture loop keep a
// cube (bounded by the grid) and a test write observations, with one
// derivation underneath both.
func learnZonesFromObservations(cameraID string, obs []Observation, cfg LearnConfig) []Zone {
	cube := map[Cell]int{}
	frames := 0
	for _, o := range obs {
		if o.CameraID != cameraID {
			continue
		}
		frames++
		seen := map[Cell]bool{}
		for _, c := range o.Cells {
			if c.Row < 0 || c.Col < 0 || c.Row >= cfg.GridH || c.Col >= cfg.GridW {
				continue
			}
			seen[c] = true
		}
		for c := range seen {
			cube[c]++
		}
	}
	return learnZones(cameraID, cube, frames, cfg)
}

// Cube is one camera's aggregated occupancy: how many frames each cell held a
// detection in, and how many frames the window covered.
type Cube struct {
	Counts map[Cell]int `json:"counts" yaml:"counts"`
	Frames int          `json:"frames" yaml:"frames"`
}

// learnZonesFromCube proposes zones from an aggregated occupancy cube. It is
// the form a capture loop can afford: the loop marks cells per frame and never
// keeps the frames, so the derivation's cost is the grid's size rather than the
// shift's length. Materialising one Observation per occupied cell per frame on
// every read is O(frames x occupied cells) allocations to answer a question the
// counts already answer.
func learnZonesFromCube(cameraID string, cube Cube, cfg LearnConfig) []Zone {
	clean := make(map[Cell]int, len(cube.Counts))
	for cell, count := range cube.Counts {
		if cell.Row >= 0 && cell.Col >= 0 && cell.Row < cfg.GridH && cell.Col < cfg.GridW {
			clean[cell] = count
		}
	}
	return learnZones(cameraID, clean, cube.Frames, cfg)
}

// learnZones is the derivation both forms share.
func learnZones(cameraID string, occupied map[Cell]int, frames int, cfg LearnConfig) []Zone {
	if frames == 0 {
		return nil
	}
	if cfg.WindowFrames == 0 {
		cfg.WindowFrames = frames
	}
	proposedType := cfg.ProposedType
	if proposedType == "" {
		proposedType = ZoneTypeMonitor
	}

	// The cells that were dwelled in, then the connected components of that
	// set. Four-neighbour, so a diagonal touch is not a connection.
	hot := make(map[Cell]bool, len(occupied))
	for cell, count := range occupied {
		if count >= cfg.MinOccupancyFrames {
			hot[cell] = true
		}
	}
	var clusters []map[Cell]bool
	visited := make(map[Cell]bool, len(hot))
	// Sorted iteration so the cluster order — and the ids derived from it —
	// are deterministic.
	cells := make([]Cell, 0, len(hot))
	for cell := range hot {
		cells = append(cells, cell)
	}
	sort.Slice(cells, func(i, j int) bool { return cellLess(cells[i], cells[j]) })
	for _, start := range cells {
		if visited[start] {
			continue
		}
		cluster := map[Cell]bool{}
		queue := []Cell{start}
		visited[start] = true
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			cluster[cur] = true
			for _, nb := range neighbours(cur) {
				if hot[nb] && !visited[nb] {
					visited[nb] = true
					queue = append(queue, nb)
				}
			}
		}
		clusters = append(clusters, cluster)
	}

	zones := make([]Zone, 0, len(clusters))
	for idx, cluster := range clusters {
		if len(cluster) < cfg.MinCells {
			continue
		}
		minRow, minCol, maxRow, maxCol := cfg.GridH, cfg.GridW, 0, 0
		best := 0
		for cell := range cluster {
			if cell.Row < minRow {
				minRow = cell.Row
			}
			if cell.Row > maxRow {
				maxRow = cell.Row
			}
			if cell.Col < minCol {
				minCol = cell.Col
			}
			if cell.Col > maxCol {
				maxCol = cell.Col
			}
			if occupied[cell] > best {
				best = occupied[cell]
			}
		}
		confidence := float64(best) / float64(cfg.WindowFrames)
		if confidence > 1 {
			confidence = 1
		}
		zones = append(zones, Zone{
			ID:         fmt.Sprintf("learned-%s-%02d", cameraID, idx),
			Type:       proposedType,
			Polygon:    cellPolygon(minRow, minCol, maxRow, maxCol, cfg),
			Confidence: confidence,
			Cameras:    []string{cameraID},
			Prov: Provenance{
				Origin: OriginLearned,
				Evidence: fmt.Sprintf("%d of %d frames' occupancy in %d cells; best cell occupied %d frames",
					frames, cfg.WindowFrames, len(cluster), best),
			},
		})
	}
	// Sort by id so the proposal order is the artifact order.
	sort.Slice(zones, func(i, j int) bool { return zones[i].ID < zones[j].ID })
	return zones
}

func cellLess(a, b Cell) bool {
	if a.Row != b.Row {
		return a.Row < b.Row
	}
	return a.Col < b.Col
}

func neighbours(c Cell) []Cell {
	return []Cell{
		{Row: c.Row - 1, Col: c.Col},
		{Row: c.Row + 1, Col: c.Col},
		{Row: c.Row, Col: c.Col - 1},
		{Row: c.Row, Col: c.Col + 1},
	}
}

// cellPolygon is the bounding box of a cluster in frame coordinates, closed.
func cellPolygon(minRow, minCol, maxRow, maxCol int, cfg LearnConfig) []Point {
	x0 := float64(minCol) * cfg.FrameW / float64(cfg.GridW)
	x1 := float64(maxCol+1) * cfg.FrameW / float64(cfg.GridW)
	y0 := float64(minRow) * cfg.FrameH / float64(cfg.GridH)
	y1 := float64(maxRow+1) * cfg.FrameH / float64(cfg.GridH)
	return []Point{
		{X: x0, Y: y0},
		{X: x1, Y: y0},
		{X: x1, Y: y1},
		{X: x0, Y: y1},
	}
}

// overlaps reports whether two polygons' bounding boxes intersect. Bounding
// boxes, not exact polygon intersection: the question this answers is "does a
// learned proposal sit on top of something a human declared", and a
// conservative yes is the safe answer for that question.
func overlaps(a, b []Point) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	ax0, ay0, ax1, ay1 := bounds(a)
	bx0, by0, bx1, by1 := bounds(b)
	return ax0 < bx1 && bx0 < ax1 && ay0 < by1 && by0 < ay1
}

func bounds(p []Point) (x0, y0, x1, y1 float64) {
	x0, y0, x1, y1 = p[0].X, p[0].Y, p[0].X, p[0].Y
	for _, pt := range p[1:] {
		if pt.X < x0 {
			x0 = pt.X
		}
		if pt.Y < y0 {
			y0 = pt.Y
		}
		if pt.X > x1 {
			x1 = pt.X
		}
		if pt.Y > y1 {
			y1 = pt.Y
		}
	}
	return x0, y0, x1, y1
}
