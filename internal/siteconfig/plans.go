package siteconfig

import (
	"fmt"
	"sort"
	"time"

	"github.com/Sergey-Bar/vision/internal/anomaly"
	"github.com/Sergey-Bar/vision/internal/camerahealth"
	"github.com/Sergey-Bar/vision/internal/sitemodel"
)

// Plans: what the artifact says the site should DO, not just what it has.
//
// Both plan kinds are closed loops over measurements the fleet already
// takes, and both are proposals: the artifact carries them with provenance,
// and a human or a guardrail decides whether they land. Nothing here tunes
// itself silently — that is the plan's ruling, and it is also the only way a
// tuning change is explainable six months later.

// VideoPlan tuning. The ceilings are the ponytail comment made executable:
// they exist because nobody has measured past them, and the code refuses to
// claim a camera captures faster than its own sidecar can answer.
const (
	// MaxCaptureFPS is the per-camera capture ceiling. Above this, the
	// shared sidecar serializes and latency reads as frame loss.
	MaxCaptureFPS = 2.0
	// MinCaptureFPS is the floor a degraded camera drops to. A camera that
	// cannot sustain this is silent, and the fleet's health says so.
	MinCaptureFPS = 0.25
	// DefaultCaptureFPS is what an unmeasured camera is planned at.
	DefaultCaptureFPS = 1.0
)

// CameraHeadroom is what the fleet measured for one camera: its named health
// state (fresh | stale | silent | blind) and how long the sidecar actually
// took to answer. A camera with no measurement is planned at the default, and
// the plan's provenance says so.
type CameraHeadroom struct {
	CameraID   string
	Health     string
	InferP95   time.Duration
	FramesSeen int64
}

// ZoneCriticality is the criticality of the most critical zone a camera is
// declared to cover, which is what the clip and retention numbers scale
// with. An unknown zone counts as monitor: the least critical thing we know.
func ZoneCriticality(cameraID string, zones []Zone) string {
	rank := map[string]int{
		ZoneTypeRestricted: 3,
		ZoneTypeAsset:      3,
		ZoneTypeChoke:      2,
		ZoneTypeRoute:      1,
		ZoneTypeMonitor:    1,
	}
	best, bestRank := ZoneTypeMonitor, 1
	for _, z := range zones {
		if !covers(z, cameraID) {
			continue
		}
		if r := rank[z.Type]; r > bestRank {
			best, bestRank = z.Type, r
		}
	}
	return best
}

func covers(z Zone, cameraID string) bool {
	for _, c := range z.Cameras {
		if c == cameraID {
			return true
		}
	}
	return false
}

// videoPlans derives the capture and recording desired state per camera from
// the measured headroom and the zones that camera covers. It is deterministic
// and additive: the same facts produce the same numbers, every run.
func videoPlans(cameras []Camera, zones []Zone, headroom []CameraHeadroom) []VideoPlan {
	byID := make(map[string]CameraHeadroom, len(headroom))
	for _, h := range headroom {
		byID[h.CameraID] = h
	}
	out := make([]VideoPlan, 0, len(cameras))
	for _, cam := range cameras {
		h, measured := byID[cam.ID]
		fps := DefaultCaptureFPS
		origin, evidence := OriginAssumed, "no measurement for this camera; planned at the default"
		if measured {
			// A camera the fleet says is degraded is planned at the floor, not
			// deleted from the plan: the desired state still says what this
			// camera should do, and the health state says why it is not doing
			// it. Health is the degenerate sensor's reading — an image arriving
			// with a covered lens tells you nothing about the scene, but it
			// still says the pipeline is alive.
			if camerahealth.Degraded(h.Health) {
				fps = MinCaptureFPS
				origin = OriginLearned
				evidence = fmt.Sprintf("the fleet reports this camera %s; planned at the floor until it is watching again", h.Health)
			} else if h.FramesSeen > 0 {
				fps = sustainableFPS(h.InferP95)
				origin, evidence = OriginLearned,
					fmt.Sprintf("p95 infer %s over %d frames", h.InferP95, h.FramesSeen)
			}
		}
		crit := ZoneCriticality(cam.ID, zones)
		plan := VideoPlan{
			CameraID:      cam.ID,
			CaptureFPS:    fps,
			PreRollSec:    preRollFor(crit),
			PostRollSec:   postRollFor(crit),
			MaxClipSec:    clipCapFor(crit),
			RetentionDays: retentionFor(crit),
			Prov: Provenance{
				Origin:   origin,
				Evidence: evidence + "; clips and retention from the most critical covered zone (" + crit + ")",
			},
		}
		out = append(out, plan)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CameraID < out[j].CameraID })
	return out
}

// sustainableFPS is the closed loop: the sidecar's own p95 latency says how
// fast a camera can be sampled, clamped between the floor and the ceiling.
// It is deliberately pessimistic (a 20% allowance for capture and tracking),
// because a plan that asks for the maximum is a plan the next measurement
// contradicts.
func sustainableFPS(p95 time.Duration) float64 {
	if p95 <= 0 {
		return DefaultCaptureFPS
	}
	perFrame := float64(p95) * 1.2
	fps := float64(time.Second) / perFrame
	if fps > MaxCaptureFPS {
		fps = MaxCaptureFPS
	}
	if fps < MinCaptureFPS {
		fps = MinCaptureFPS
	}
	return fps
}

func preRollFor(crit string) int {
	switch crit {
	case ZoneTypeRestricted, ZoneTypeAsset:
		return 10
	case ZoneTypeChoke:
		return 5
	default:
		return 3
	}
}

func postRollFor(crit string) int {
	switch crit {
	case ZoneTypeRestricted, ZoneTypeAsset:
		return 15
	case ZoneTypeChoke:
		return 10
	default:
		return 5
	}
}

func clipCapFor(crit string) int {
	switch crit {
	case ZoneTypeRestricted, ZoneTypeAsset:
		return 60
	case ZoneTypeChoke:
		return 30
	default:
		return 20
	}
}

func retentionFor(crit string) int {
	switch crit {
	case ZoneTypeRestricted, ZoneTypeAsset:
		return 90
	case ZoneTypeChoke:
		return 30
	default:
		return 7
	}
}

// AnalyticsRule is one derived anomaly rule: which zone it fires for, what it
// measures, and the declaration it came from.
//
// It is a description rather than a bundle object because the bundle is a
// contract surface and the rendering through rules.Compiler is the product's
// step; what this provides is the derivation a human reads before that.
type AnalyticsRule struct {
	ZoneID      string `yaml:"zone_id" json:"zone_id"`
	Type        string `yaml:"type" json:"type"` // dwell | loiter | flow | tailgate
	Threshold   string `yaml:"threshold" json:"threshold"`
	MinSeverity string `yaml:"min_severity" json:"min_severity"`
	Evidence    string `yaml:"evidence" json:"evidence"`
}

// AnalyticsRules derives the rule set an artifact's zones imply.
//
// It renders `anomaly`'s own description of the rules it will run, so the table
// an operator reads before a bundle is created describes the rules that
// actually exist. The previous version re-derived the table locally and had
// drifted: the artifact said loiter low / tailgate medium / flow low while the
// engine ran medium / high / high, which is worse than no table, because it
// reads as a description of behaviour it does not describe.
//
// The caller has the artifact's zones rather than the site model they were
// built from, so the zones are handed to `anomaly.DescribeSite` as a site —
// the conversion carries the fields the description reads (zone type, the
// camera declarations, the route ids) and nothing else, which is why it is not
// a general adapter but a narrow one that exists to answer this question.
func AnalyticsRules(zones []Zone, flows []string) []AnalyticsRule {
	site := &sitemodel.Site{ID: "artifact"}
	for _, z := range zones {
		site.Zones = append(site.Zones, sitemodel.Zone{
			ID:      z.ID,
			Type:    z.Type,
			Polygon: toModelPoints(z.Polygon),
		})
		for _, c := range z.Cameras {
			site.Cameras = append(site.Cameras, sitemodel.Camera{ID: c, ZoneIDs: []string{z.ID}})
		}
	}
	for _, f := range flows {
		site.Routes = append(site.Routes, sitemodel.Route{ID: f})
	}

	summaries := anomaly.DescribeSite(site)
	out := make([]AnalyticsRule, 0, len(summaries))
	for _, s := range summaries {
		out = append(out, AnalyticsRule{
			ZoneID:      s.ZoneID,
			Type:        s.Type,
			Threshold:   s.Threshold,
			MinSeverity: s.MinSeverity,
			Evidence:    "anomaly.DefaultRuleSet over the artifact's declared zones",
		})
	}
	return out
}

// toModelPoints converts artifact polygons into site-model ones.
func toModelPoints(in []Point) []sitemodel.Point {
	out := make([]sitemodel.Point, 0, len(in))
	for _, p := range in {
		out = append(out, sitemodel.Point{X: p.X, Y: p.Y})
	}
	return out
}
