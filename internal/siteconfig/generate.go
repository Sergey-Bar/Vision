package siteconfig

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Sergey-Bar/Ai-Captain/internal/safename"
)

// GenerateInput is everything the artifact is generated from: the site's
// identity, a camera inventory, the declared zones, the observed occupancy and
// the measured headroom. Generate takes it and returns the artifact.
//
// The order of operations is the reason this function exists: discovery
// first — a failure here is a named error, never an empty artifact that looks
// like a site with no cameras — then the declared zones as given, then the
// learned zones that survive the overlap check, then the plans computed from
// what survived.
type GenerateInput struct {
	SiteID      string
	GeneratedAt time.Time
	Inventory   CameraInventory
	// DeclaredZones comes from the site model; its cameras are adopted from
	// the inventory's own declarations.
	DeclaredZones []Zone
	// Observations is the occupancy the pipeline measured, per camera, in the
	// per-frame form.
	Observations []Observation
	// Cubes is the same measurement in the aggregated form: camera id → the
	// cells it held detections in and the frames the window covered. A camera
	// with a cube does not need its observations, and a caller with a
	// long-running capture loop has the cube rather than the frames — which
	// is why the two forms exist and one derivation reads both.
	Cubes    map[string]Cube
	LearnCfg LearnConfig
	// Headroom is what the fleet measured per camera.
	Headroom []CameraHeadroom
}

// Generate builds the artifact.
func Generate(in GenerateInput) (*Artifact, error) {
	if in.Inventory == nil {
		return nil, fmt.Errorf("siteconfig: no camera inventory was provided")
	}
	discovered, err := in.Inventory.Discover()
	if err != nil {
		return nil, err
	}
	if len(discovered) == 0 {
		return nil, fmt.Errorf("siteconfig: the inventory discovered no cameras")
	}
	sort.Slice(discovered, func(i, j int) bool { return discovered[i].ID < discovered[j].ID })

	art := &Artifact{
		SiteID:      in.SiteID,
		GeneratedAt: in.GeneratedAt.UTC().Format(time.RFC3339),
		Cameras:     discovered,
		Zones:       append([]Zone(nil), in.DeclaredZones...),
	}

	// Learned zones, minus the ones that sit on a declared zone: the
	// declaration is the record of what a human already decided. Both input
	// forms feed the same derivation, so a site that learns from cubes and a
	// site that learns from observations produce the same proposals for the
	// same per-cell counts.
	for _, cam := range discovered {
		for _, z := range learnZonesFor(cam.ID, in.Observations, in.Cubes, in.LearnCfg) {
			if overlapsAny(z.Polygon, art.Zones) {
				continue
			}
			art.Zones = append(art.Zones, z)
		}
	}

	art.Video = videoPlans(discovered, art.Zones, in.Headroom)
	art.Sort()
	if err := art.Validate(); err != nil {
		return nil, err
	}
	return art, nil
}

// AnalyticsRulesFor renders the analytics derivation of a generated artifact:
// the rules its zones imply, in the shape anomaly.DefaultRuleSet already
// uses, so a human reads the derivation before it becomes a bundle candidate.
func (a *Artifact) AnalyticsRulesFor(routes []string) []AnalyticsRule {
	return AnalyticsRules(a.Zones, routes)
}

func overlapsAny(poly []Point, zones []Zone) bool {
	for _, z := range zones {
		if overlaps(poly, z.Polygon) {
			return true
		}
	}
	return false
}

// learnZonesFor picks the input form a caller supplied: the cube when there is
// one, the observations otherwise. A caller with both is answered from the
// cube, which is the form a long-running loop can actually keep.
func learnZonesFor(cameraID string, obs []Observation, cubes map[string]Cube, cfg LearnConfig) []Zone {
	if cube, ok := cubes[cameraID]; ok && cube.Frames > 0 {
		return learnZonesFromCube(cameraID, cube, cfg)
	}
	return learnZonesFromObservations(cameraID, obs, cfg)
}

// FileTarget is the POC's reconcile target: it regenerates its own artifact
// directory, one YAML file per object. It is a real target rather than a test
// double — the POC's durable state for the configurator is exactly this — and
// it is deliberately the simplest target, because the interface is the seam
// and the VMS target is the product's to write.
//
// The read and write funcs are injected so a test can drive the engine with no
// filesystem at all; NewFileTarget wires the stdlib.
type FileTarget struct {
	Dir   string
	Read  func(path string) ([]byte, error)
	Write func(path string, data []byte) error
}

// NewFileTarget is the FileTarget over the real filesystem.
func NewFileTarget(dir string) *FileTarget {
	return &FileTarget{Dir: dir, Read: os.ReadFile, Write: writeFileOwnerOnly}
}

// writeFileOwnerOnly writes an object file the way the artifact directory
// expects: one file per object, readable only by the owner that wrote it.
func writeFileOwnerOnly(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}

func (t *FileTarget) path(name string) string { return filepath.Join(t.Dir, name) }

// List reads the objects the target holds from its directory.
func (t *FileTarget) List(_ context.Context) ([]Object, error) {
	entries, err := os.ReadDir(t.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // an empty directory is an empty target, not an error
		}
		return nil, fmt.Errorf("siteconfig: read the artifact dir: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yaml") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	objects := make([]Object, 0, len(names))
	for _, name := range names {
		kind, id, ok := parseObjectName(name)
		if !ok {
			continue
		}
		data, err := t.Read(t.path(name))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("siteconfig: read %s: %w", name, err)
		}
		obj, err := decodeObject(kind, id, data)
		if err != nil {
			return nil, err
		}
		objects = append(objects, obj)
	}
	return objects, nil
}

// Create writes an object the target does not hold.
func (t *FileTarget) Create(_ context.Context, obj Object) error {
	return t.writeObject(obj)
}

// Update rewrites an object's content.
func (t *FileTarget) Update(_ context.Context, obj Object) error {
	return t.writeObject(obj)
}

func (t *FileTarget) writeObject(obj Object) error {
	if err := obj.Validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(obj)
	if err != nil {
		return fmt.Errorf("siteconfig: marshal %s %s: %w", obj.Kind, obj.ID, err)
	}
	if t.Write == nil {
		return fmt.Errorf("siteconfig: file target has no writer")
	}
	return t.Write(t.path(objectFileName(obj)), data)
}

// objectFileName is the safe mapping from an object to a file name. The mapping
// is a bijection over safe ids and refuses an unsafe one rather than writing
// to a path the id could have escaped.
func objectFileName(obj Object) string {
	return safename.Safe(obj.Kind) + "-" + safename.Safe(obj.ID) + ".yaml"
}

// parseObjectName is the inverse, and it is deliberately strict about the
// prefix: only files named after a known kind are objects. An artifact's own
// `artifact.yaml`, a snapshot, an operator's README or a stray editor backup
// sitting in the directory must not be read back as an object — a snapshot
// parsed as one would refuse the next apply for having an unknown shape, which
// would make an idempotent command fail the second time it ran.
func parseObjectName(name string) (kind, id string, ok bool) {
	if !strings.HasSuffix(name, ".yaml") {
		return "", "", false
	}
	stem := strings.TrimSuffix(name, ".yaml")
	for _, k := range knownShapes {
		if strings.HasPrefix(stem, k+"-") {
			return k, strings.TrimPrefix(stem, k+"-"), true
		}
	}
	return "", "", false
}

// decodeObject reads an object file. The struct's own yaml tags are the
// contract; the file name is the fallback when the content omits a field, which
// is a hand-edited or partially written file rather than a normal one.
func decodeObject(kind, id string, data []byte) (Object, error) {
	var obj Object
	if err := yaml.Unmarshal(data, &obj); err != nil {
		return Object{}, fmt.Errorf("siteconfig: %s %s is not an object file: %w", kind, id, err)
	}
	if obj.Kind == "" {
		obj.Kind = kind
	}
	if obj.ID == "" {
		obj.ID = id
	}
	if obj.Shape == "" {
		obj.Shape = obj.Kind
	}
	return obj, nil
}
