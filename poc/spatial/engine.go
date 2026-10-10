// Package spatial provides zone-based spatial analysis for object detection alerts.
package spatial

import (
	"time"
)

// Engine performs spatial analysis on detected objects against configured zones.
type Engine struct {
	zones []Zone
}

// Zone represents a configured spatial zone with polygon boundaries.
type Zone struct {
	ID             string
	Name           string
	Polygon        []Point
	Type           string
	AlertOnEntry   bool
	AlertOnDwell   bool
	DwellThreshold int64 // milliseconds
}

// ZoneConfig is the input configuration for a zone.
type ZoneConfig struct {
	ID             string
	Name           string
	Polygon        [][]float64
	Type           string
	AlertOnEntry   bool
	AlertOnDwell   bool
	DwellThreshold time.Duration
}

// Point represents a 2D coordinate.
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Alert represents a spatial alert triggered by an object entering or dwelling in a zone.
type Alert struct {
	ZoneID         string `json:"zone_id"`
	ZoneName       string `json:"zone_name"`
	ZoneType       string `json:"zone_type"`
	AlertType      string `json:"alert_type"` // "entry", "dwell", "exit"
	ObjectID       string `json:"object_id"`
	ObjectLabel    string `json:"object_label"`
	ObjectCategory string `json:"object_category"`
	Timestamp      int64  `json:"timestamp"`
	Position       Point  `json:"position"`
	DwellTimeMS    int64  `json:"dwell_time_ms,omitempty"`
	Severity       string `json:"severity"` // "info", "warning", "critical"
}

// TrackedObject represents an object being tracked across frames.
type TrackedObject struct {
	ID          string
	Label       string
	Category    string
	Position    Point
	ZoneEntries map[string]int64 // zoneID -> entry time
	LastAlert   map[string]int64 // zoneID -> last alert time
}

// NewEngine creates a new spatial analysis engine from zone configurations.
func NewEngine(zoneConfigs []ZoneConfig) *Engine {
	zones := make([]Zone, len(zoneConfigs))
	for i, zc := range zoneConfigs {
		polygon := make([]Point, len(zc.Polygon))
		for j, p := range zc.Polygon {
			if len(p) >= 2 {
				polygon[j] = Point{X: float64(p[0]), Y: float64(p[1])}
			}
		}
		zones[i] = Zone{
			ID:             zc.ID,
			Name:           zc.Name,
			Polygon:        polygon,
			Type:           zc.Type,
			AlertOnEntry:   zc.AlertOnEntry,
			AlertOnDwell:   zc.AlertOnDwell,
			DwellThreshold: zc.DwellThreshold.Milliseconds(),
		}
	}
	return &Engine{zones: zones}
}

// Analyze checks each object against all zones and returns any triggered alerts.
func (e *Engine) Analyze(objects []DetectedObject) []Alert {
	var alerts []Alert

	for _, obj := range objects {
		if obj.BoundingBox.Width == 0 && obj.BoundingBox.Height == 0 {
			continue
		}

		centerX := obj.BoundingBox.X + obj.BoundingBox.Width/2
		centerY := obj.BoundingBox.Y + obj.BoundingBox.Height/2
		pos := Point{X: centerX, Y: centerY}

		for _, zone := range e.zones {
			if e.pointInPolygon(pos, zone.Polygon) {
				alert := e.checkZone(zone, obj, pos)
				if alert != nil {
					alerts = append(alerts, *alert)
				}
			}
		}
	}

	return alerts
}

func (e *Engine) checkZone(zone Zone, obj DetectedObject, pos Point) *Alert {
	now := time.Now().UnixMilli()

	if zone.AlertOnEntry {
		alert := &Alert{
			ZoneID:         zone.ID,
			ZoneName:       zone.Name,
			ZoneType:       zone.Type,
			AlertType:      "entry",
			ObjectID:       obj.stableID(),
			ObjectLabel:    obj.Label,
			ObjectCategory: obj.Category,
			Timestamp:      now,
			Position:       pos,
			Severity:       e.severityForZoneType(zone.Type),
		}
		return alert
	}

	if zone.AlertOnDwell && zone.DwellThreshold > 0 {
		alert := &Alert{
			ZoneID:         zone.ID,
			ZoneName:       zone.Name,
			ZoneType:       zone.Type,
			AlertType:      "dwell",
			ObjectID:       obj.stableID(),
			ObjectLabel:    obj.Label,
			ObjectCategory: obj.Category,
			Timestamp:      now,
			Position:       pos,
			DwellTimeMS:    zone.DwellThreshold,
			Severity:       e.severityForZoneType(zone.Type),
		}
		return alert
	}

	return nil
}

func (e *Engine) severityForZoneType(zoneType string) string {
	switch zoneType {
	case "no-entry":
		return "critical"
	case "restricted":
		return "critical"
	case "monitoring":
		return "warning"
	default:
		return "info"
	}
}

// pointInPolygon uses the ray-casting algorithm to test if a point is inside a polygon.
func (e *Engine) pointInPolygon(pt Point, polygon []Point) bool {
	if len(polygon) < 3 {
		return false
	}

	inside := false
	j := len(polygon) - 1
	for i := 0; i < len(polygon); i++ {
		pi := polygon[i]
		pj := polygon[j]

		if ((pi.Y > pt.Y) != (pj.Y > pt.Y)) &&
			(pt.X < (pj.X-pi.X)*(pt.Y-pi.Y)/(pj.Y-pi.Y)+pi.X) {
			inside = !inside
		}
		j = i
	}
	return inside
}

// GetZones returns all configured zones.
func (e *Engine) GetZones() []Zone {
	return e.zones
}

// GetZoneByID returns a zone by its ID, or nil if not found.
func (e *Engine) GetZoneByID(id string) *Zone {
	for i := range e.zones {
		if e.zones[i].ID == id {
			return &e.zones[i]
		}
	}
	return nil
}

// RuleViolation defines a spatial rule to check against detected objects.
type RuleViolation struct {
	ZoneID        string
	Categories    []string
	ViolationType string // "entry", "dwell", "direction"
	Severity      string
	Direction     Point // for direction-based violations
	MinDwellTime  int64
}

// CheckRuleViolation checks objects against spatial rules and returns alerts for violations.
func (e *Engine) CheckRuleViolation(objects []DetectedObject, rules []RuleViolation) []Alert {
	var alerts []Alert

	for _, rule := range rules {
		for _, obj := range objects {
			if !matchesCategory(obj, rule.Categories) {
				continue
			}

			if obj.BoundingBox.Width == 0 && obj.BoundingBox.Height == 0 {
				continue
			}

			centerX := obj.BoundingBox.X + obj.BoundingBox.Width/2
			centerY := obj.BoundingBox.Y + obj.BoundingBox.Height/2
			pos := Point{X: centerX, Y: centerY}

			if rule.ZoneID != "" {
				zone := e.GetZoneByID(rule.ZoneID)
				if zone == nil {
					continue
				}

				if rule.ViolationType == "entry" && e.pointInPolygon(pos, zone.Polygon) {
					alerts = append(alerts, Alert{
						ZoneID:         zone.ID,
						ZoneName:       zone.Name,
						ZoneType:       zone.Type,
						AlertType:      "rule_violation",
						ObjectID:       obj.stableID(),
						ObjectLabel:    obj.Label,
						ObjectCategory: obj.Category,
						Timestamp:      time.Now().UnixMilli(),
						Position:       pos,
						Severity:       rule.Severity,
					})
				}
			}
		}
	}

	return alerts
}

func matchesCategory(obj DetectedObject, categories []string) bool {
	if len(categories) == 0 {
		return true
	}
	for _, cat := range categories {
		if obj.Category == cat || obj.SubCategory == cat {
			return true
		}
	}
	return false
}

// DetectedObject represents a detected object with spatial properties.
type DetectedObject struct {
	// ID is the caller's stable track identifier, when it has one. Zone
	// presence, dwell timing and per-rule cooldowns key on it, because two
	// objects of the same class in the same zone are two presences — and a
	// fallback to Label would merge them into one.
	ID          string
	Category    string
	SubCategory string
	Label       string
	Confidence  float64
	BoundingBox Box
	Attributes  map[string]any
}

// stableID is what a zone transition is keyed by: the track id where the
// caller supplied one, the label where it did not.
func (o DetectedObject) stableID() string {
	if o.ID != "" {
		return o.ID
	}
	if o.Label != "" {
		return o.Label
	}
	return o.Category
}

// Box represents a bounding box.
type Box struct {
	X      float64
	Y      float64
	Width  float64
	Height float64
}
