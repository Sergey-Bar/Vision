// Package main is the YouTube live analytics POC: a single binary that serves
// the dashboard UI, the JSON API and the analytics WebSocket, and drives the
// frame pipeline (simulated in demo mode, HLS ingest in live mode) that feeds
// categorization, spatial analysis, rule violations and live search.
package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"math/rand"
	"time"

	"github.com/Sergey-Bar/vision/poc/search"
	"github.com/Sergey-Bar/vision/poc/spatial"
	"github.com/Sergey-Bar/vision/poc/ws"
)

// Frame geometry every coordinate in the POC is expressed in. The frontend
// scales this space onto the player's bounding box.
const (
	frameW   = 1280
	frameH   = 720
	demoTick = 500 * time.Millisecond
)

// wireBox is the JSON shape of a bounding box sent to the frontend.
type wireBox struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// wireObject is the JSON shape of one detection in a frame message.
type wireObject struct {
	ID          string         `json:"id"`
	Category    string         `json:"category"`
	SubCategory string         `json:"sub_category"`
	Label       string         `json:"label"`
	Confidence  float64        `json:"confidence"`
	BBox        wireBox        `json:"bbox"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

// simObject is one simulated actor moving through the frame.
type simObject struct {
	id       string
	category string
	sub      string
	label    string
	conf     float64
	attrs    map[string]any
	x, y     float64
	w, h     float64
	speed    float64
	tx, ty   float64
	born     time.Time
	life     time.Duration
	// loiterFrom is where a scripted loiterer shuttles back to, so the
	// back-and-forth is a pattern the loiter rule can count rather than a
	// random walk that happens to stay in the zone.
	loiterFrom *[2]float64
}

type vehicleKind struct {
	sub      string
	typeWord string
	w, h     float64
}

var vehicleKinds = []vehicleKind{
	{sub: "car", typeWord: "sedan", w: 210, h: 100},
	{sub: "suv", typeWord: "SUV", w: 235, h: 115},
	{sub: "truck", typeWord: "truck", w: 265, h: 125},
	{sub: "van", typeWord: "van", w: 230, h: 112},
	{sub: "motorcycle", typeWord: "motorcycle", w: 120, h: 80},
	{sub: "car", typeWord: "hatchback", w: 185, h: 95},
}

var vehicleColors = []string{"red", "blue", "white", "black", "silver", "green", "yellow", "gray"}

var animalKinds = []string{"dog", "cat", "bird"}

var otherKinds = []struct {
	label string
	w, h  float64
}{
	{"backpack", 70, 80},
	{"package", 80, 60},
	{"bicycle", 130, 90},
	{"bench", 160, 70},
	{"traffic cone", 55, 75},
	{"suitcase", 70, 95},
}

// zoneWatch is the shared, source-independent zone state: which track is in
// which zone since when, and when each rule last fired. The demo simulator and
// the live ONNX pipeline both fan their frames through ONE instance so an entry
// or a dwell is a transition of the scene, not of whichever source drew it.
type zoneWatch struct {
	engine     *spatial.Engine
	presence   map[string]time.Time
	dwellSet   map[string]bool
	violated   map[string]time.Time
	zoneToRule map[string]RuleConfig
	rules      []spatial.RuleViolation
}

func newZoneWatch(engine *spatial.Engine, rules []RuleConfig) *zoneWatch {
	zoneToRule := make(map[string]RuleConfig, len(rules))
	violations := make([]spatial.RuleViolation, 0, len(rules))
	for _, r := range rules {
		zoneToRule[r.ZoneID] = r
		violations = append(violations, spatial.RuleViolation{
			ZoneID:        r.ZoneID,
			Categories:    r.Categories,
			ViolationType: r.ViolationType,
			Severity:      r.Severity,
		})
	}
	return &zoneWatch{
		engine:     engine,
		presence:   make(map[string]time.Time),
		dwellSet:   make(map[string]bool),
		violated:   make(map[string]time.Time),
		zoneToRule: zoneToRule,
		rules:      violations,
	}
}

// simulator produces the per-frame detection stream when no real source is
// attached (demo mode). It is seeded so a given run is reproducible.
type simulator struct {
	engine  *spatial.Engine
	rng     *rand.Rand
	objects map[string]*simObject
	nextID  int
	ticks   int
	zones   []spatial.Zone
	watch   *zoneWatch
	// scenarioState records which scripted behaviours have already been
	// rehearsed, so the demo cycles through them rather than replaying one.
	scenarioState map[string]bool
}

func newSimulator(engine *spatial.Engine, rules []RuleConfig, seed int64) *simulator {
	return &simulator{
		engine: engine,
		//nolint:gosec // G404: POC scene randomness only; nothing security-
		// critical is derived from it and a seeded source keeps runs replayable.
		rng:           rand.New(rand.NewSource(seed)),
		objects:       make(map[string]*simObject),
		zones:         engine.GetZones(),
		watch:         newZoneWatch(engine, rules),
		scenarioState: make(map[string]bool),
	}
}

// StartDemoPipeline runs the simulated frame source until ctx is cancelled.
func (s *StreamIngest) StartDemoPipeline(ctx context.Context) {
	log.Printf("Demo pipeline: simulated frame source starting (mode=demo)")
	sim := newSimulator(s.spatialEngine, s.config.Spatial.Rules, time.Now().UnixNano())
	ticker := time.NewTicker(demoTick)
	defer ticker.Stop()

	// Seed immediately so the dashboard is alive before the first tick.
	s.demoTick(sim)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.demoTick(sim)
		}
	}
}

// frameObject is one detection in a frame, in FRAME pixel coordinates, from
// either source. Everything downstream — the broadcast, the zone analysis, the
// rule violations, the search index — consumes this type only.
type frameObject struct {
	ID       string
	Category string
	Sub      string
	Label    string
	Conf     float64
	BBox     wireBox
	Attrs    map[string]any
}

// demoTick advances the simulation one frame and publishes it through the
// shared fan-out. The demo simulator is the fleet's pseudo-camera: it
// publishes under its own camera id, keeps its own health counters and enters
// the autonomy pass like any other camera, so nothing downstream has a
// special case for "this one is simulated".
func (s *StreamIngest) demoTick(sim *simulator) {
	frame := s.frameCount.Add(1)
	now := time.Now().UTC()
	objs := sim.step(now)

	frameObjs := make([]frameObject, 0, len(objs))
	for _, o := range objs {
		frameObjs = append(frameObjs, frameObject{
			ID:       o.id,
			Category: o.category,
			Sub:      o.sub,
			Label:    o.label,
			Conf:     o.conf,
			BBox:     wireBox{X: o.x, Y: o.y, Width: o.w, Height: o.h},
			Attrs:    o.attrs,
		})
	}

	sim.ticks++
	if c := s.fleet.get(simCameraID); c != nil {
		c.frames.Add(1)
		c.lastFrame.Store(now.UnixNano())
		c.lastBlind.Store(false)
		c.health.Store(camFresh)
		c.reason.Store("")
	}
	s.publishFrame(simCameraID, "sim", now, frame, frameObjs, sim.ticks%10 == 0)
}

// publishFrame is the single fan-out every frame source shares: detection
// broadcast, spatial entry/dwell alerts, rule violations and (optionally) a
// search-index entry. The demo simulator and every camera go through it, so
// the dashboard cannot tell the sources apart and no source can skip a step.
// cameraID is whose frame this is — it is what the autonomy pass uses, so a
// detection always carries the camera that actually saw it.
func (s *StreamIngest) publishFrame(cameraID, source string, now time.Time, frame int64, objs []frameObject, index bool) {
	wire := make([]wireObject, 0, len(objs))
	spatialObjs := make([]spatial.DetectedObject, 0, len(objs))
	counts := map[string]int{"vehicle": 0, "biological": 0, "other": 0}

	for _, o := range objs {
		wire = append(wire, wireObject{
			ID:          o.ID,
			Category:    o.Category,
			SubCategory: o.Sub,
			Label:       o.Label,
			Confidence:  o.Conf,
			BBox:        o.BBox,
			Attributes:  o.Attrs,
		})
		// The spatial and zone paths key on the object id, and each camera's
		// local tracker numbers its own objects from t0001 — so the id is
		// camera-scoped here. Without that, a person on cam-01 and a car on
		// cam-02 would share a presence key and one zone entry.
		spatialObjs = append(spatialObjs, spatial.DetectedObject{
			ID:          cameraID + "/" + o.ID,
			Category:    o.Category,
			SubCategory: o.Sub,
			Label:       o.Label,
			Confidence:  o.Conf,
			BoundingBox: spatial.Box{X: o.BBox.X, Y: o.BBox.Y, Width: o.BBox.Width, Height: o.BBox.Height},
			Attributes:  o.Attrs,
		})
		counts[o.Category]++
	}

	s.objectCount.Add(int64(len(objs)))
	s.latestObjects.Store(int64(len(objs)))

	s.wsHub.Broadcast(ws.Message{
		Type: "detection",
		Data: map[string]any{
			"camera_id": cameraID,
			"frame":     frame,
			"source":    source,
			"width":     frameW,
			"height":    frameH,
			"counts":    counts,
			"objects":   wire,
			"wall_time": now.Format(time.RFC3339Nano),
		},
		Timestamp: now,
	})

	rawAlerts := s.spatialEngine.Analyze(spatialObjs)
	entryAlerts := s.zone.trackPresence(rawAlerts, now)
	violations := s.zone.checkViolations(spatialObjs, now)

	if len(entryAlerts) > 0 {
		s.alertCount.Add(int64(len(entryAlerts)))
		s.wsHub.Broadcast(ws.Message{
			Type: "spatial",
			Data: map[string]any{
				"camera_id": cameraID,
				"alerts":    entryAlerts,
				"frame":     frame,
			},
			Timestamp: now,
		})
	}
	for _, v := range violations {
		s.violationCount.Add(1)
		v["camera_id"] = cameraID
		s.wsHub.Broadcast(ws.Message{
			Type:      "violation",
			Data:      v,
			Timestamp: now,
		})
	}

	if index {
		cats := make([]search.CategoryInfo, 0, len(wire))
		for _, o := range wire {
			cats = append(cats, search.CategoryInfo{
				Category:    o.Category,
				SubCategory: o.SubCategory,
				Label:       o.Label,
				Confidence:  o.Confidence,
			})
		}
		entry := search.Entry{
			// The camera is part of the entry id: every camera numbers its
			// own frames from 1, so a frame id without a camera would let two
			// cameras' frames overwrite each other in the index.
			ID:            fmt.Sprintf("frame_%s_%d", cameraID, frame),
			Timestamp:     now,
			Categories:    cats,
			SpatialAlerts: searchAlerts(rawAlerts),
			Resolution:    fmt.Sprintf("%dx%d", frameW, frameH),
		}
		s.searchIndex.Add(entry)
		s.wsHub.Broadcast(ws.Message{
			Type:      "search_index",
			Data:      map[string]any{"added": entry, "total": s.searchIndex.Count()},
			Timestamp: now,
		})
	}

	// The autonomy layer runs on the same facts, in the same pass, after the
	// deterministic spine has produced its alerts and violations.
	s.autonomyPass(cameraID, now, frame, objs)
}

// trackPresence turns the per-frame zone hits into entry/dwell alerts with
// proper transitions: an entry fires once when a track crosses in, a dwell
// fires once after the zone's dwell threshold, and leaving resets both.
func (z *zoneWatch) trackPresence(hits []spatial.Alert, now time.Time) []spatial.Alert {
	present := make(map[string]bool, len(hits))
	var out []spatial.Alert

	for _, h := range hits {
		key := h.ZoneID + "|" + h.ObjectID
		present[key] = true
		zone := z.engine.GetZoneByID(h.ZoneID)
		if zone == nil {
			continue
		}
		if _, seen := z.presence[key]; !seen {
			z.presence[key] = now
			// Only zones that asked for entry alerts announce a crossing;
			// monitoring zones exist purely to time their dwell threshold.
			if zone.AlertOnEntry {
				h.AlertType = "entry"
				h.Timestamp = now.UnixMilli()
				out = append(out, h)
			}
		}
		if !zone.AlertOnDwell || zone.DwellThreshold <= 0 {
			continue
		}
		if z.dwellSet[key] {
			continue
		}
		if now.Sub(z.presence[key]) >= time.Duration(zone.DwellThreshold)*time.Millisecond {
			z.dwellSet[key] = true
			h.AlertType = "dwell"
			h.Timestamp = now.UnixMilli()
			h.DwellTimeMS = zone.DwellThreshold
			out = append(out, h)
		}
	}

	// Forget objects that left every zone so a re-entry alerts again.
	for key := range z.presence {
		if !present[key] {
			delete(z.presence, key)
			delete(z.dwellSet, key)
		}
	}
	return out
}

// checkViolations evaluates the configured no-zone rules, with a per-rule,
// per-object cooldown so a demo does not flood the alert feed.
func (z *zoneWatch) checkViolations(objs []spatial.DetectedObject, now time.Time) []map[string]any {
	var out []map[string]any
	for _, alert := range z.engine.CheckRuleViolation(objs, z.rules) {
		ruleID := ""
		cooldown := 10 * time.Second
		if rc, ok := z.zoneToRule[alert.ZoneID]; ok {
			ruleID = rc.ID
			if rc.Cooldown > 0 {
				cooldown = rc.Cooldown
			}
		}
		if ruleID == "" {
			ruleID = "rule_" + alert.ZoneID
		}
		key := ruleID + "|" + alert.ObjectID
		if last, ok := z.violated[key]; ok && now.Sub(last) < cooldown {
			continue
		}
		z.violated[key] = now
		out = append(out, map[string]any{
			"rule_id":         ruleID,
			"zone_id":         alert.ZoneID,
			"zone_name":       alert.ZoneName,
			"alert_type":      alert.AlertType,
			"object_label":    alert.ObjectLabel,
			"object_category": alert.ObjectCategory,
			"severity":        alert.Severity,
			"position":        alert.Position,
			"timestamp":       alert.Timestamp,
		})
	}
	return out
}

// step spawns, moves and retires one frame's worth of simulated objects.
// Actors have a lifetime: when it expires they leave the scene and the spawn
// mix runs again, so the scene keeps churning instead of freezing on its
// opening cast.
func (sim *simulator) step(now time.Time) []*simObject {
	const targetCount = 8
	for id, o := range sim.objects {
		if now.Sub(o.born) > o.life {
			delete(sim.objects, id)
		}
	}
	if len(sim.objects) < targetCount {
		for i := len(sim.objects); i < targetCount; i++ {
			sim.spawn(now)
		}
	}

	// Rehearse the scripted behaviours once each per run. They are added on
	// top of the ambient traffic so a demo shows both the ordinary scene and
	// the four patterns the autonomy layer is meant to catch.
	if len(sim.scenarioState) < 4 {
		sim.spawnScenario(now)
	}

	for id, o := range sim.objects {
		// A scripted loiterer shuttles between its two points rather than
		// picking a new target, which is what makes the back-and-forth a
		// countable pattern instead of a walk.
		if o.loiterFrom != nil {
			dist := math.Hypot(o.tx-o.x, o.ty-o.y)
			if dist <= o.speed {
				if o.x == o.loiterFrom[0] && o.y == o.loiterFrom[1] {
					o.tx, o.ty = o.loiterFrom[0]+80, o.loiterFrom[1]+60
				} else {
					o.tx, o.ty = o.loiterFrom[0], o.loiterFrom[1]
				}
			} else {
				o.x += (o.tx - o.x) / dist * o.speed
				o.y += (o.ty - o.y) / dist * o.speed
			}
			continue
		}
		dx, dy := o.tx-o.x, o.ty-o.y
		dist := math.Hypot(dx, dy)
		if dist <= o.speed {
			o.tx, o.ty = sim.pickTarget()
		} else {
			o.x += dx / dist * o.speed
			o.y += dy / dist * o.speed
		}
		if o.x < -400 || o.x > frameW+400 || o.y < -400 || o.y > frameH+400 {
			delete(sim.objects, id)
		}
	}

	out := make([]*simObject, 0, len(sim.objects))
	for _, o := range sim.objects {
		out = append(out, o)
	}
	return out
}

// spawnScenario makes one scripted actor so a demo reliably produces every
// behaviour the autonomy layer looks for: a person dwelling in a restricted
// zone, a loitering back-and-forth, a route traffic violation, and a pair that
// tailgates a choke point together.
//
// The scripted actors carry `scenario` on their attributes, which is what the
// synthetic labelling in labels.json names. A random actor that happens to
// wander into a zone is an ordinary detection; a scripted one is a rehearsal.
func (sim *simulator) spawnScenario(now time.Time) bool {
	if len(sim.zones) == 0 || !sim.nextScenarioNeeded() {
		return false
	}
	kinds := sim.scenarioKinds()
	for _, k := range kinds {
		// A behaviour already rehearsed is skipped, not replayed: the demo
		// shows all four rather than the first one four times.
		if sim.scenarioState[k.kind] {
			continue
		}
		z := sim.zoneByID(k.zoneID)
		if z == nil || len(z.Polygon) == 0 {
			continue
		}
		sim.nextID++
		id := fmt.Sprintf("obj_%03d", sim.nextID)
		o := &simObject{
			id:       id,
			category: "biological", sub: "human",
			label: "person",
			conf:  0.86 + sim.rng.Float64()*0.1,
			attrs: map[string]any{"role": "adult", "scenario": k.kind, "synthetic": true},
			w:     65, h: 165,
			born:  now,
			life:  20 * time.Second,
			speed: k.speed,
		}
		if k.kind == "tailgate" {
			// Two actors, close together, both entering the choke point.
			second := sim.nextID
			sim.nextID++
			o2 := *o
			o2.id = fmt.Sprintf("obj_%03d", sim.nextID)
			o2.attrs = map[string]any{"role": "adult", "scenario": "tailgate", "synthetic": true, "pair": second}
			cx, cy := zCentroid(z)
			o.x, o.y = cx-30, cy
			o.tx, o.ty = cx, cy
			o2.x, o2.y = cx+30, cy
			o2.tx, o2.ty = cx, cy
			sim.objects[o.id] = o
			sim.objects[o2.id] = &o2
			sim.markScenarioPlayed(k.kind)
			return true
		}
		cx, cy := zCentroid(z)
		o.x, o.y = cx, cy
		// A loiterer shuttles between two points inside the zone; a dweller is
		// left where it is; a flow violator is launched backwards down the
		// route.
		switch k.kind {
		case "loiter":
			o.tx, o.ty = cx+80, cy+60
			o.loiterFrom = &[2]float64{cx, cy}
		case "flow":
			pts := z.Polygon
			o.x, o.y = pts[len(pts)-1].X, pts[len(pts)-1].Y
			o.tx, o.ty = pts[0].X, pts[0].Y
		default:
			o.tx, o.ty = cx, cy
		}
		sim.objects[o.id] = o
		sim.markScenarioPlayed(k.kind)
		return true
	}
	return false
}

func zCentroid(z *spatial.Zone) (float64, float64) {
	var sx, sy float64
	for _, p := range z.Polygon {
		sx += p.X
		sy += p.Y
	}
	n := float64(len(z.Polygon))
	if n == 0 {
		return frameW / 2, frameH / 2
	}
	return sx / n, sy / n
}

type scenarioKind struct {
	kind   string
	zoneID string
	speed  float64
}

// nextScenarioNeeded reports whether a scripted behaviour that has not played
// recently is due, so the demo cycles through them instead of firing all four
// in the same second.
func (sim *simulator) nextScenarioNeeded() bool {
	return len(sim.scenarioState) < len(sim.scenarioKinds())
}

func (sim *simulator) markScenarioPlayed(kind string) {
	sim.scenarioState[kind] = true
}

func (sim *simulator) zoneByID(id string) *spatial.Zone {
	for i := range sim.zones {
		if sim.zones[i].ID == id {
			return &sim.zones[i]
		}
	}
	return nil
}

// scenarioKinds names the behaviour to rehearse and the zone to rehearse it
// in, preferring the site's restricted and choke zones when the config has
// them. A demo config with no such zones simply has no scripted actors.
func (sim *simulator) scenarioKinds() []scenarioKind {
	var out []scenarioKind
	seen := map[string]bool{}
	add := func(kind, want string) {
		for _, z := range sim.zones {
			if z.Type == want && !seen[kind] {
				out = append(out, scenarioKind{kind: kind, zoneID: z.ID, speed: 3})
				seen[kind] = true
				return
			}
		}
	}
	add("dwell", "no-entry")
	add("loiter", "monitoring")
	add("tailgate", "monitoring")
	add("flow", "monitoring")
	return out
}

// pickTarget chooses where an actor heads next. A quarter of the targets sit
// inside a configured zone so no-zone rules actually fire during a demo.
func (sim *simulator) pickTarget() (float64, float64) {
	if len(sim.zones) > 0 && sim.rng.Intn(3) == 0 {
		z := sim.zones[sim.rng.Intn(len(sim.zones))]
		if len(z.Polygon) > 0 {
			var sx, sy float64
			for _, p := range z.Polygon {
				sx += p.X
				sy += p.Y
			}
			return sx / float64(len(z.Polygon)), sy / float64(len(z.Polygon))
		}
	}
	return float64(sim.rng.Intn(frameW)), float64(sim.rng.Intn(frameH))
}

func (sim *simulator) spawn(now time.Time) {
	sim.nextID++
	id := fmt.Sprintf("obj_%03d", sim.nextID)

	// Weighted mix: vehicles 40%, humans 30%, animals 15%, other 15%.
	roll := sim.rng.Float64()
	var o *simObject
	switch {
	case roll < 0.40:
		k := vehicleKinds[sim.rng.Intn(len(vehicleKinds))]
		color := vehicleColors[sim.rng.Intn(len(vehicleColors))]
		o = &simObject{
			category: "vehicle", sub: k.sub,
			label: color + " " + k.typeWord,
			conf:  0.72 + sim.rng.Float64()*0.27,
			attrs: map[string]any{"color": color, "type": k.typeWord, "speed_kmh": 20 + sim.rng.Intn(60)},
			w:     k.w, h: k.h,
			x: -k.w, y: float64(430 + sim.rng.Intn(260)),
			speed: 10 + sim.rng.Float64()*10,
		}
		if sim.rng.Intn(2) == 0 {
			o.x = frameW + k.w
		}
	case roll < 0.70:
		role := "adult"
		if sim.rng.Intn(5) == 0 {
			role = "child"
		}
		o = &simObject{
			category: "biological", sub: "human",
			label: "person",
			conf:  0.80 + sim.rng.Float64()*0.19,
			attrs: map[string]any{"role": role, "carrying": sim.rng.Intn(2) == 0},
			w:     65, h: 165, x: float64(sim.rng.Intn(frameW-130) + 30), y: float64(frameH - 40),
			speed: 6 + sim.rng.Float64()*4,
		}
	case roll < 0.85:
		kind := animalKinds[sim.rng.Intn(len(animalKinds))]
		o = &simObject{
			category: "biological", sub: kind,
			label: kind,
			conf:  0.70 + sim.rng.Float64()*0.25,
			attrs: map[string]any{"leashed": sim.rng.Intn(2) == 0},
			w:     85, h: 65, x: float64(sim.rng.Intn(frameW-170) + 40), y: float64(frameH - 50),
			speed: 9 + sim.rng.Float64()*6,
		}
	default:
		k := otherKinds[sim.rng.Intn(len(otherKinds))]
		o = &simObject{
			category: "other", sub: k.label,
			label: k.label,
			conf:  0.65 + sim.rng.Float64()*0.30,
			attrs: map[string]any{"static": sim.rng.Intn(3) == 0},
			w:     k.w, h: k.h,
			x: float64(sim.rng.Intn(frameW)), y: float64(120 + sim.rng.Intn(500)),
			speed: 1 + sim.rng.Float64()*2,
		}
	}
	o.id = id
	o.born = now
	o.life = 20*time.Second + time.Duration(sim.rng.Intn(21))*time.Second
	// A quarter of actors are born inside a configured zone, so no-zone rules
	// fire promptly instead of waiting for a crossing that may outlive the
	// actor's lifetime.
	if sim.rng.Intn(4) == 0 && len(sim.zones) > 0 {
		z := sim.zones[sim.rng.Intn(len(sim.zones))]
		if len(z.Polygon) > 0 {
			var sx, sy float64
			for _, p := range z.Polygon {
				sx += p.X
				sy += p.Y
			}
			o.x = sx/float64(len(z.Polygon)) - o.w/2
			o.y = sy/float64(len(z.Polygon)) - o.h/2
		}
	}
	o.tx, o.ty = sim.pickTarget()
	sim.objects[id] = o
}

func searchAlerts(alerts []spatial.Alert) []search.SpatialAlert {
	out := make([]search.SpatialAlert, 0, len(alerts))
	for _, a := range alerts {
		out = append(out, search.SpatialAlert{
			ZoneID:         a.ZoneID,
			ZoneName:       a.ZoneName,
			AlertType:      a.AlertType,
			ObjectLabel:    a.ObjectLabel,
			ObjectCategory: a.ObjectCategory,
			Severity:       a.Severity,
			Timestamp:      a.Timestamp,
		})
	}
	return out
}
