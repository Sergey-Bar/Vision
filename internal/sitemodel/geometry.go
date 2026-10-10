// Package sitemodel loads and provides site topology for the autonomy layer.
package sitemodel

// Point is a 2D coordinate in the site's reference frame.
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// PointInPolygon reports whether point p is inside polygon poly using the
// ray-casting algorithm. The polygon is closed implicitly (last point connects
// to first). Points on the boundary are considered inside.
//
// This is the only geometry the autonomy layer needs. A camera reports a
// bounding box, its centre is a point, and a zone is a polygon: every behaviour
// rule is a question about a point in a polygon. Anything more elaborate would
// be a question about geometry rather than about the site.
func PointInPolygon(p Point, poly []Point) bool {
	if len(poly) < 3 {
		return false
	}
	inside := false
	j := len(poly) - 1
	for i := 0; i < len(poly); i++ {
		pi := poly[i]
		pj := poly[j]
		intersect := (pi.Y > p.Y) != (pj.Y > p.Y) &&
			p.X < (pj.X-pi.X)*(p.Y-pi.Y)/(pj.Y-pi.Y)+pi.X
		if intersect {
			inside = !inside
		}
		j = i
	}
	return inside
}
