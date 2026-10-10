// Package siteconfig is the autonomous configurator: it turns what a site
// declares and what it has learned into a provenance-carrying artifact, then
// reconciles that artifact against the VMS behind one interface.
//
// This is the repo's DoD item 1 — the only open product item a person with a
// real installation can move today — and it answers the two pains the July
// 2026 surveys measured: operator time spent on routine VMS changes, and the
// per-camera cost creep that comes from siloed platforms.
//
// Three rules govern the whole package, and they are why it is pure:
//
//  1. Nothing is guessed. An entry is `declared` (a human wrote it), `learned`
//     (the pipeline's facts produced it, with its evidence attached), or
//     `assumed` (nobody knows and the artifact says so). An unknown camera
//     position is `assumed`, never invented.
//  2. Purity by construction. Plan and Apply take their arguments — a sink, a
//     target, a clock — rather than reaching for a database, a vendor client
//     or a logger. The product passes an audit.Writer and the Milestone
//     adapter; the POC passes the duty log and the YouTube registry. This
//     package imports no DB and no vendor.
//  3. Validate-then-write. If any target object's response shape is unknown,
//     the whole plan is refused. Never a partial application — a half-applied
//     VMS change is indistinguishable from corruption to the next operator.
package siteconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// Zone types — the same vocabulary the site model uses, because an artifact
// that invents a second vocabulary for the same concept is two vocabularies.
const (
	ZoneTypeRestricted = "restricted"
	ZoneTypeChoke      = "choke"
	ZoneTypeMonitor    = "monitor"
	ZoneTypeAsset      = "asset"
	ZoneTypeRoute      = "route"
)

// Origins. Every entry in an artifact carries one of these plus its evidence,
// because a configuration nobody can trace is the problem this artifact
// replaces.
const (
	OriginDeclared = "declared" // a human wrote it down
	OriginLearned  = "learned"  // the pipeline's facts produced it
	OriginAssumed  = "assumed"  // nobody knows; the artifact says so
)

// Provenance says where an entry came from and what it rests on. Evidence is
// the citation: the site-model line, the detection ids, the probe report.
type Provenance struct {
	Origin   string `yaml:"origin" json:"origin"`
	Evidence string `yaml:"evidence,omitempty" json:"evidence,omitempty"`
}

// Valid reports whether the origin is one this package writes.
func (p Provenance) Valid() bool {
	switch p.Origin {
	case OriginDeclared, OriginLearned, OriginAssumed:
		return true
	}
	return false
}

// Camera is one camera in the discovered inventory. Position is nil when the
// discovery source could not tell us — a nil position is `assumed`, never a
// point at the origin, because a guessed coordinate is worse than a missing
// one: it looks like knowledge.
type Camera struct {
	ID        string     `yaml:"id" json:"id"`
	Name      string     `yaml:"name,omitempty" json:"name,omitempty"`
	Position  *Point     `yaml:"position,omitempty" json:"position,omitempty"`
	ZoneHints []string   `yaml:"zone_hints,omitempty" json:"zone_hints,omitempty"`
	Prov      Provenance `yaml:"provenance" json:"provenance"`
}

// Point is a 2D coordinate in the site plan.
type Point struct {
	X float64 `yaml:"x" json:"x"`
	Y float64 `yaml:"y" json:"y"`
}

// Zone is one zone in the artifact: the polygon, its declared or proposed
// type, and where the type came from. Confidence is a learned zone's support
// over its window; a declared zone carries none, because a declaration does
// not have a confidence.
type Zone struct {
	ID         string     `yaml:"id" json:"id"`
	Type       string     `yaml:"type" json:"type"` // restricted | choke | monitor | asset | route
	Polygon    []Point    `yaml:"polygon" json:"polygon"`
	Confidence float64    `yaml:"confidence,omitempty" json:"confidence,omitempty"`
	Cameras    []string   `yaml:"cameras,omitempty" json:"cameras,omitempty"`
	Prov       Provenance `yaml:"provenance" json:"provenance"`
}

// VideoPlan is one camera's desired capture and recording state.
type VideoPlan struct {
	CameraID      string     `yaml:"camera_id" json:"camera_id"`
	CaptureFPS    float64    `yaml:"capture_fps" json:"capture_fps"`
	PreRollSec    int        `yaml:"pre_roll_sec" json:"pre_roll_sec"`
	PostRollSec   int        `yaml:"post_roll_sec" json:"post_roll_sec"`
	MaxClipSec    int        `yaml:"max_clip_sec" json:"max_clip_sec"`
	RetentionDays int        `yaml:"retention_days" json:"retention_days"`
	Prov          Provenance `yaml:"provenance" json:"provenance"`
}

// Artifact is the generated site configuration: what the site has, where each
// entry came from, and where it is going. It is the destinations-file pattern
// from the dispatch package — YAML a human reads, every line traceable.
type Artifact struct {
	SiteID      string      `yaml:"site_id" json:"site_id"`
	GeneratedAt string      `yaml:"generated_at" json:"generated_at"` // caller-stamped, so renders are deterministic
	Cameras     []Camera    `yaml:"cameras" json:"cameras"`
	Zones       []Zone      `yaml:"zones" json:"zones"`
	Video       []VideoPlan `yaml:"video_plans" json:"video_plans"`
}

// Sort puts the artifact into its canonical order, so two runs over the same
// facts produce the same bytes. The order is by id, the only stable key the
// entries have.
func (a *Artifact) Sort() {
	sort.Slice(a.Cameras, func(i, j int) bool { return a.Cameras[i].ID < a.Cameras[j].ID })
	sort.Slice(a.Zones, func(i, j int) bool { return a.Zones[i].ID < a.Zones[j].ID })
	sort.Slice(a.Video, func(i, j int) bool { return a.Video[i].CameraID < a.Video[j].CameraID })
}

// Digest is the artifact's content hash: what a plan file cites to say which
// artifact it was computed from. It covers identity and content, not the
// timestamp, so re-rendering the same facts keeps the same digest.
func (a *Artifact) Digest() string {
	clone := *a
	clone.GeneratedAt = ""
	clone.Sort()
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "site=%s\n", a.SiteID)
	for _, c := range clone.Cameras {
		_, _ = fmt.Fprintf(h, "camera=%s;%s;%v;%v;%s\n", c.ID, c.Name, c.Position, c.ZoneHints, c.Prov.Origin)
	}
	for _, z := range clone.Zones {
		_, _ = fmt.Fprintf(h, "zone=%s;%s;%v;%v;%s\n", z.ID, z.Type, z.Polygon, z.Cameras, z.Prov.Origin)
	}
	for _, v := range clone.Video {
		_, _ = fmt.Fprintf(h, "video=%s;%v;%d;%d;%d;%d;%s\n", v.CameraID, v.CaptureFPS, v.PreRollSec, v.PostRollSec, v.MaxClipSec, v.RetentionDays, v.Prov.Origin)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// CameraInventory is the seam between this package and whatever discovers
// cameras. It has one method, deliberately: id, name, position-when-known,
// zone hints. A fat interface is how a seam dies, and every consumer only
// ever needs this call.
//
// The Milestone adapter implements it in the product; the declared YouTube
// registry implements it in the POC; a test implements it with a slice.
type CameraInventory interface {
	Discover() ([]Camera, error)
}

// StaticInventory is a CameraInventory that answers from a fixed list, which
// is what a declared registry, a fixture or a test has. It is not a stub: the
// POC's camera registry is exactly this, and calling it what it is keeps the
// production path honest about where its facts came from.
type StaticInventory []Camera

// Discover returns a copy of the inventory, so a caller cannot retract an
// origin by editing what it was handed.
//
// The seam's discover method is free to fail, and a failing discovery returns
// an error naming its source: an empty list with no error is not a failure, it
// is a statement that there are no cameras, and those are different things to
// say about a site.
func (s StaticInventory) Discover() ([]Camera, error) {
	out := make([]Camera, len(s))
	copy(out, s)
	return out, nil
}

// Validate checks the artifact before it is written: every entry's origin is
// one we write, every zone has a polygon, every camera id is unique. An
// artifact that fails this must never reach a disk.
func (a *Artifact) Validate() error {
	seenCam := map[string]bool{}
	for _, c := range a.Cameras {
		if c.ID == "" {
			return fmt.Errorf("siteconfig: camera with no id")
		}
		if seenCam[c.ID] {
			return fmt.Errorf("siteconfig: camera %s appears twice", c.ID)
		}
		seenCam[c.ID] = true
		if !c.Prov.Valid() {
			return fmt.Errorf("siteconfig: camera %s has origin %q, want declared|learned|assumed", c.ID, c.Prov.Origin)
		}
	}
	seenZone := map[string]bool{}
	for _, z := range a.Zones {
		if z.ID == "" {
			return fmt.Errorf("siteconfig: zone with no id")
		}
		if seenZone[z.ID] {
			return fmt.Errorf("siteconfig: zone %s appears twice", z.ID)
		}
		seenZone[z.ID] = true
		if !z.Prov.Valid() {
			return fmt.Errorf("siteconfig: zone %s has origin %q, want declared|learned|assumed", z.ID, z.Prov.Origin)
		}
		if len(z.Polygon) < 3 {
			return fmt.Errorf("siteconfig: zone %s has %d polygon points, want at least 3", z.ID, len(z.Polygon))
		}
	}
	for _, v := range a.Video {
		if !v.Prov.Valid() {
			return fmt.Errorf("siteconfig: video plan for %s has origin %q, want declared|learned|assumed", v.CameraID, v.Prov.Origin)
		}
		if v.CaptureFPS <= 0 {
			return fmt.Errorf("siteconfig: video plan for %s has capture fps %v, want > 0", v.CameraID, v.CaptureFPS)
		}
	}
	return nil
}

// Summary renders the artifact as a human-readable provenance table: one
// line per entry, origin first. It is what `securityctl site-config show`
// prints before any plan is computed.
func (a *Artifact) Summary() string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "site %s · %d cameras · %d zones · %d video plans · digest %s\n",
		a.SiteID, len(a.Cameras), len(a.Zones), len(a.Video), a.Digest())
	for _, c := range a.Cameras {
		pos := "position: assumed"
		if c.Position != nil {
			pos = fmt.Sprintf("position: %.0f,%.0f", c.Position.X, c.Position.Y)
		}
		_, _ = fmt.Fprintf(&b, "  camera %-16s %-8s %-18s name=%s\n", c.ID, c.Prov.Origin, pos, orDash(c.Name))
	}
	for _, z := range a.Zones {
		_, _ = fmt.Fprintf(&b, "  zone   %-16s %-8s type=%-10s cameras=%s\n", z.ID, z.Prov.Origin, z.Type, strings.Join(z.Cameras, "+"))
	}
	for _, v := range a.Video {
		_, _ = fmt.Fprintf(&b, "  video  %-16s %-8s fps=%.2f pre=%ds post=%ds cap=%ds retention=%dd\n",
			v.CameraID, v.Prov.Origin, v.CaptureFPS, v.PreRollSec, v.PostRollSec, v.MaxClipSec, v.RetentionDays)
	}
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "–"
	}
	return s
}
