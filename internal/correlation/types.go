// Package correlation tracks entities across cameras/time and groups detections
// into CorrelatedIncident.
package correlation

import (
	"time"
)

// BBox is a bounding box.
type BBox struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// Point is a 2D coordinate.
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// EntityRef represents a tracked entity across cameras.
type EntityRef struct {
	ID          string
	Class       string // person | vehicle | other
	CameraID    string
	FirstSeen   time.Time
	LastSeen    time.Time
	BBoxHistory []BBox
}

// Anomaly represents a detected anomaly.
type Anomaly struct {
	Type      string // dwell | loiter | flow | tailgate
	RuleID    string
	ZoneID    string
	CameraID  string
	EntityIDs []string
	FirstSeen time.Time
	LastSeen  time.Time
	Severity  string
	Details   map[string]any
}

// ReasonStep is one step in a reason chain.
type ReasonStep struct {
	Key      string // "dwell>15s"
	Rule     string // rule name
	Factor   int    // score contribution
	Evidence string // short evidence
	At       time.Time
}

// ReasonChain is a chain of reasoning steps.
type ReasonChain struct {
	Steps []ReasonStep
	Why   string // human-readable summary
}

// CorrelatedIncident is a group of correlated detections across cameras/time.
type CorrelatedIncident struct {
	ID            string
	SiteID        string
	State         string // ACTIVE | RESOLVED | OVERRIDDEN
	FirstSeen     time.Time
	LastSeen      time.Time
	CameraIDs     []string
	DetectionIDs  []string
	Entities      []EntityRef
	Anomalies     []Anomaly
	ReasonChain   ReasonChain
	ThreatScore   int    // 0-100
	Action        string // OBSERVE | VERIFY | ESCALATE | HOLD
	AutonomyState string // AUTO | VERIFYING | ESCALATING | HOLD
	Provenance    ProvenanceExt
}

// ProvenanceExt extends the base provenance with correlation context.
type ProvenanceExt struct {
	SiteID        string
	CameraID      string
	ZoneID        string
	CorrelationID string
	ReasonChain   ReasonChain
	ModelIdentity string
	ContractVer   string
	OutputHash    string
	EvidenceRef   string // storage_ref + sha256
}
