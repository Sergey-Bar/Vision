// Package detect turns boxes into line-crossing decisions.
//
// It is deliberately the smallest package in the module and the one with the
// fewest dependencies: boxes and a line go in, a decision comes out. It knows
// nothing about Milestone, about SQLite, about HTTP, about evidence, or about
// the fact that a clip will eventually be cut. That isolation is what makes it
// testable in a unit test instead of only in a seven-day run.
package detect

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/Sergey-Bar/ai-captain-poc/internal/proposal"
)

// Box is a detection in normalised coordinates: 0..1 against the frame. The
// coordinates are the frame's, not the model's input's, so a model that runs at
// 640x640 and a camera at 1920x1080 produce the same numbers.
//
// It is an ALIAS of [proposal.Box]. The five methods that used to be declared
// here — Centre, Width, Height, Valid, and the type's own definition — live in
// `proposal` now and reach every call site in this package through the alias, so
// not one of the package's tests needed editing. An alias rather than a wrapper
// is the point: a wrapper would have put a conversion on each of them, and a
// conversion on a coordinate is a place to rescale one by accident.
type Box = proposal.Box

// Line is a crossing line in normalised coordinates.
type Line struct {
	Name string     `json:"name"`
	A    [2]float64 `json:"a"`
	B    [2]float64 `json:"b"`
	// Direction is what the report prints for a crossing: the operator's word
	// for which way is "across", not a computed normal.
	Direction string `json:"direction"`
	// Classes restricts the line to the classes that count as crossing it. An
	// empty list means every approved class.
	Classes []string `json:"classes,omitempty"`
}

// Polygon is a region of interest. A box must be inside it to be considered.
type Polygon struct {
	Name    string       `json:"name"`
	Points  [][2]float64 `json:"points"`
	Classes []string     `json:"classes,omitempty"`
}

// Zone is the whole configuration file: the geometry plus the source it was
// drawn for. The source matters, because coordinates drawn on one camera are
// meaningless on another and the report has to say which file was used.
type Zone struct {
	Source   string    `json:"source"`
	Frame    *Frame    `json:"frame,omitempty"`
	Lines    []Line    `json:"lines"`
	Polygons []Polygon `json:"polygons,omitempty"`
	Note     string    `json:"note,omitempty"`
}

// Frame records the resolution the coordinates were drawn against, so a
// mismatch with the live source can be refused instead of producing nonsense
// crossings.
type Frame struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// LoadZone reads and validates a zones.json. Every refusal names the file and
// the problem, because a mis-drawn line produces a run with zero events and no
// error, which is the most expensive kind of silence there is.
func LoadZone(path string) (Zone, error) {
	raw0, err := os.ReadFile(path)
	if err != nil {
		return Zone{}, fmt.Errorf("detect: cannot read the zone file %s; without a line there is no crossing and the run produces no events: %w", path, err)
	}
	var any0 any
	if err := json.Unmarshal(raw0, &any0); err != nil {
		return Zone{}, fmt.Errorf("detect: %s is not usable JSON: %w", path, err)
	}
	scrubbed, err := json.Marshal(stripAnnotations(any0))
	if err != nil {
		return Zone{}, fmt.Errorf("detect: %s could not be re-encoded after removing its annotations: %w", path, err)
	}
	var z Zone
	dec := json.NewDecoder(bytes.NewReader(scrubbed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&z); err != nil {
		return Zone{}, fmt.Errorf("detect: %s is not the documented zone shape (a key beginning with $ is an annotation at any depth and is ignored): %w", path, err)
	}
	if err := z.Validate(); err != nil {
		return Zone{}, fmt.Errorf("detect: %s is not usable: %w", path, err)
	}
	return z, nil
}

// stripAnnotations removes every object key that begins with `$`, at any depth.
//
// A `$` key is an annotation by the JSON-LD convention, and this file carries
// `$comment` blocks explaining what a wrong line costs. It is allowed
// deliberately: refusing a comment would make it impossible to keep that
// explanation next to the thing it explains, and a reader would then delete the
// comment and leave nobody knowing why.
//
// Everything else stays strict. A typo in a field that restricts a line must
// fail, because the operator would think the restriction is in force when it is
// not, and the run would count crossings the line never meant to allow.
func stripAnnotations(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, child := range t {
			if strings.HasPrefix(k, "$") {
				continue
			}
			out[k] = stripAnnotations(child)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, child := range t {
			out[i] = stripAnnotations(child)
		}
		return out
	default:
		return v
	}
}

// Validate checks the geometry.
//
// A zone with NO lines is legal, and it is the primary intended configuration rather
// than a degenerate one: presence mode reports that a tracked object was seen, with no
// line drawn and nothing to configure. The refusal that used to sit here said a
// line-less zone "can only ever produce zero events", which was true of the crossing
// machine and false of the engine, because the engine grew a presence path. A guard
// written against an earlier engine outliving the engine is how a default becomes a
// refusal.
//
// Every OTHER check is kept, and each of them is a real geometry error rather than a
// missing drawing: a duplicate name counts one crossing twice, identical endpoints have
// no direction, and a point outside the frame is not on the picture the operator drew
// it on. A line-less zone does not make any of those go away. It only means there are
// none to make.
func (z Zone) Validate() error {
	seen := map[string]bool{}
	for i, l := range z.Lines {
		if l.Name == "" {
			return fmt.Errorf("line %d has no name; an unnamed line's crossings cannot be told apart in the report", i)
		}
		if seen[l.Name] {
			return fmt.Errorf("line %q is declared twice; a crossing would be counted twice", l.Name)
		}
		seen[l.Name] = true
		if l.A[0] == l.B[0] && l.A[1] == l.B[1] {
			return fmt.Errorf("line %q has identical endpoints, so it has no direction and nothing can cross it", l.Name)
		}
		for _, p := range [][2]float64{l.A, l.B} {
			if p[0] < -0.05 || p[0] > 1.05 || p[1] < -0.05 || p[1] > 1.05 {
				return fmt.Errorf("line %q has endpoint [%v %v] outside the frame; coordinates are normalised to 0..1", l.Name, p[0], p[1])
			}
		}
	}
	for i, p := range z.Polygons {
		if len(p.Points) < 3 {
			return fmt.Errorf("polygon %d has %d points; a polygon needs at least three", i, len(p.Points))
		}
	}
	return nil
}

// side reports which side of the line a point is on, and how far from the line
// it is in normalised units perpendicular to the line. The sign is the side;
// the magnitude is the distance used for the tolerance band.
func (l Line) side(x, y float64) (sign int, dist float64) {
	dx, dy := l.B[0]-l.A[0], l.B[1]-l.A[1]
	length := math.Hypot(dx, dy)
	if length == 0 {
		return 0, 0
	}
	// Cross product of the line's direction with the vector to the point,
	// divided by the length, gives the signed perpendicular distance.
	cross := dx*(y-l.A[1]) - dy*(x-l.A[0])
	d := cross / length
	if d > 0 {
		return 1, d
	}
	if d < 0 {
		return -1, -d
	}
	return 0, 0
}

// contains reports whether a point is inside a polygon, by the even-odd rule.
func (p Polygon) contains(x, y float64) bool {
	inside := false
	n := len(p.Points)
	for i, j := 0, n-1; i < n; j, i = i, i+1 {
		xi, yi := p.Points[i][0], p.Points[i][1]
		xj, yj := p.Points[j][0], p.Points[j][1]
		if (yi > y) != (yj > y) {
			t := (y - yi) / (yj - yi)
			if x < xi+t*(xj-xi) {
				inside = !inside
			}
		}
	}
	return inside
}

// SortedClasses returns the zone's class restrictions, sorted, for the report.
func (z Zone) SortedClasses() []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range z.Lines {
		for _, c := range l.Classes {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	for _, p := range z.Polygons {
		for _, c := range p.Classes {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	sort.Strings(out)
	return out
}
