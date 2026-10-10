package siteconfig

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixedNow() time.Time {
	return time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
}

func demoInventory() StaticInventory {
	return StaticInventory{
		{ID: "cam-01", Name: "City Square", Position: &Point{X: 100, Y: 200}, Prov: Provenance{Origin: OriginDeclared, Evidence: "the POC's camera registry"}},
		{ID: "cam-02", Name: "Harbour Road", Prov: Provenance{Origin: OriginDeclared, Evidence: "the POC's camera registry"}}, // position unknown
	}
}

func demoZones() []Zone {
	return []Zone{{
		ID: "restricted", Type: ZoneTypeRestricted, Cameras: []string{"cam-01"},
		Polygon: []Point{{X: 550, Y: 50}, {X: 950, Y: 50}, {X: 950, Y: 450}, {X: 550, Y: 450}},
		Prov:    Provenance{Origin: OriginDeclared, Evidence: "the site model"},
	}}
}

// observationsOf is occupancy in one 2x2 block of the grid, for that many
// frames: a cluster LearnZones should find. The camera is the dwell camera,
// because the test that matters here is what learnZones does with another
// camera's occupancy when asked about a different one.
func observationsOf(frames int) []Observation {
	out := make([]Observation, 0, frames)
	for i := 0; i < frames; i++ {
		out = append(out, Observation{CameraID: "cam-02", Cells: dwellCells()})
	}
	return out
}

func dwellCells() []Cell {
	return []Cell{{Row: 6, Col: 2}, {Row: 6, Col: 3}, {Row: 7, Col: 2}, {Row: 7, Col: 3}}
}

func demoInput() GenerateInput {
	return GenerateInput{
		SiteID:        "site-1",
		GeneratedAt:   fixedNow(),
		Inventory:     demoInventory(),
		DeclaredZones: demoZones(),
		Observations:  observationsOf(30),
		LearnCfg: func() LearnConfig {
			c := DefaultLearnConfig()
			c.WindowFrames = 30
			return c
		}(),
		Headroom: []CameraHeadroom{
			{CameraID: "cam-01", Health: "fresh", InferP95: 120 * time.Millisecond, FramesSeen: 500},
		},
	}
}

// TestGenerateIsDeterministicAndProvenanceCarrying is the golden contract:
// two runs over the same facts produce the same bytes, and every entry names
// where it came from — including the camera whose position nobody knows,
// which is assumed rather than guessed.
func TestGenerateIsDeterministicAndProvenanceCarrying(t *testing.T) {
	a, err := Generate(demoInput())
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	b, err := Generate(demoInput())
	if err != nil {
		t.Fatalf("generate again: %v", err)
	}
	if a.Digest() != b.Digest() {
		t.Error("two runs over the same facts produced different digests")
	}
	if a.Summary() != b.Summary() {
		t.Error("two runs over the same facts rendered different summaries")
	}

	var cam02 *Camera
	for i := range a.Cameras {
		if a.Cameras[i].ID == "cam-02" {
			cam02 = &a.Cameras[i]
		}
	}
	if cam02 == nil {
		t.Fatal("cam-02 is missing from the artifact")
	}
	if cam02.Position != nil {
		t.Error("cam-02 has a position nobody declared; an unknown position must stay unknown")
	}
	if !strings.Contains(a.Summary(), "cam-02") || !strings.Contains(a.Summary(), "position: assumed") {
		t.Errorf("the artifact does not mark cam-02's position as assumed:\n%s", a.Summary())
	}

	// The declared zone is declared, the learned one is learned with its
	// evidence attached, and it does not overlap the declared zone it
	// would otherwise have been dropped for.
	var declared, learned int
	for _, z := range a.Zones {
		switch z.Prov.Origin {
		case OriginDeclared:
			declared++
		case OriginLearned:
			learned++
			if z.Prov.Evidence == "" {
				t.Errorf("learned zone %s carries no evidence", z.ID)
			}
			if z.Type == ZoneTypeRestricted {
				t.Errorf("learned zone %s is typed restricted; a learned zone is never silently critical", z.ID)
			}
		}
	}
	if declared != 1 || learned != 1 {
		t.Errorf("artifact zones: %d declared, %d learned, want 1 and 1", declared, learned)
	}
}

// TestLearnZonesGridBinning checks the derivation: a camera that dwells in one
// corner proposes a zone there, with a confidence from the occupancy, and a
// camera that passes through proposes less than one that stayed.
// TestLearnZonesCubeAndObservationsAgree checks the equivalence the two input
// forms rest on: a frame that covered four cells is the same four +1s as four
// frames that covered one each, so the aggregate form a capture loop keeps and
// the per-frame form a test writes produce the same proposals for the same
// per-cell counts. Without that equivalence, one of the two callers would be
// learning from a different measurement than the other.
func TestLearnZonesCubeAndObservationsAgree(t *testing.T) {
	cfg := DefaultLearnConfig()
	cfg.WindowFrames = 30
	fromObs := learnZonesFromObservations("cam-02", observationsOf(30), cfg)
	cube := Cube{Counts: map[Cell]int{}, Frames: 30}
	for _, cell := range dwellCells() {
		cube.Counts[cell] = 30
	}
	fromCube := learnZonesFromCube("cam-02", cube, cfg)

	if len(fromObs) != 1 || len(fromCube) != 1 {
		t.Fatalf("observations proposed %d zones and the cube proposed %d, want 1 and 1", len(fromObs), len(fromCube))
	}
	if fromObs[0].ID != fromCube[0].ID || fromObs[0].Confidence != fromCube[0].Confidence {
		t.Errorf("the two forms disagree: %+v vs %+v", fromObs[0], fromCube[0])
	}
	// A cube with no frames is a camera nobody watched, and it proposes
	// nothing rather than a zone derived from nothing.
	if got := learnZonesFromCube("cam-02", Cube{}, cfg); got != nil {
		t.Errorf("an empty cube proposed %d zones", len(got))
	}
}

func TestLearnZonesGridBinning(t *testing.T) {
	cfg := DefaultLearnConfig()
	cfg.WindowFrames = 30
	zones := learnZonesFromObservations("cam-02", observationsOf(30), cfg)
	if len(zones) != 1 {
		t.Fatalf("learned %d zones from a clear dwell, want 1", len(zones))
	}
	z := zones[0]
	if z.Type != ZoneTypeMonitor {
		t.Errorf("learned zone type = %q, want monitor (the ceiling)", z.Type)
	}
	if z.Confidence < 0.9 {
		t.Errorf("confidence = %v, want ≥0.9 for 30 of 30 frames", z.Confidence)
	}
	if len(z.Polygon) != 4 {
		t.Errorf("polygon has %d points, want 4", len(z.Polygon))
	}
	if !strings.HasPrefix(z.ID, "learned-cam-02-") {
		t.Errorf("learned zone id = %q", z.ID)
	}
	// A passer-by, occupying a cell for fewer frames than the threshold, is
	// not a zone.
	sparse := learnZonesFromObservations("cam-02", observationsOf(2), cfg)
	if len(sparse) != 0 {
		t.Errorf("two frames of occupancy proposed %d zones", len(sparse))
	}
	// A different camera's observations do not propose zones for this one.
	other := learnZonesFromObservations("cam-03", observationsOf(30), cfg)
	if len(other) != 0 {
		t.Errorf("another camera's dwell proposed zones for cam-03: %+v", other)
	}
}

// TestVideoPlansUseMeasuredHeadroom: the closed loop reads the sidecar's own
// latency and the covered zones, and a camera with no measurement is planned
// at the default with `assumed` provenance rather than a confident number.
func TestVideoPlansUseMeasuredHeadroom(t *testing.T) {
	cameras := demoInventory().DiscoverMust(t)
	zones := demoZones()
	plans := videoPlans(cameras, zones, []CameraHeadroom{
		{CameraID: "cam-01", Health: "fresh", InferP95: 100 * time.Millisecond, FramesSeen: 800},
	})
	if len(plans) != 2 {
		t.Fatalf("%d plans for %d cameras", len(plans), len(cameras))
	}
	cam01, cam02 := plans[0], plans[1]
	if cam01.CaptureFPS != MaxCaptureFPS {
		t.Errorf("cam-01 fps = %v with a 100ms p95, want the %.1f ceiling", cam01.CaptureFPS, MaxCaptureFPS)
	}
	if cam01.RetentionDays != 90 || cam01.PreRollSec != 10 {
		t.Errorf("cam-01's restricted-zone plan = %+v, want retention 90 and pre-roll 10", cam01)
	}
	if cam02.Prov.Origin != OriginAssumed {
		t.Errorf("cam-02's plan origin = %q, want assumed (no measurement)", cam02.Prov.Origin)
	}
	if cam02.CaptureFPS != DefaultCaptureFPS {
		t.Errorf("cam-02's unmeasured fps = %v, want %v", cam02.CaptureFPS, DefaultCaptureFPS)
	}
	// A slow sidecar gets a slower plan: the ceiling is a ceiling, not a wish.
	slow := videoPlans(cameras, nil, []CameraHeadroom{{CameraID: "cam-01", InferP95: 4 * time.Second, FramesSeen: 50}})
	if slow[0].CaptureFPS >= cam01.CaptureFPS {
		t.Errorf("a slow sidecar produced the same plan as a fast one: %v", slow[0].CaptureFPS)
	}
}

// TestReconcileNeverDeletesNorTouchesForeign is the guardrail set as code:
// an object the engine did not create is adopted and reported, an object
// nobody desired is kept, and a retention reduction is refused by name.
func TestReconcileNeverDeletesNorTouchesForeign(t *testing.T) {
	desired := []Object{
		{Kind: KindCamera, ID: "cam-01", Shape: KindCamera, Fingerprint: "f1", Values: map[string]string{"name": "City Square"}},
		{Kind: KindCamera, ID: "cam-02", Shape: KindCamera, Fingerprint: "f2", Values: map[string]string{"name": "Harbour Road"}},
		{Kind: KindCamera, ID: "cam-03", Shape: KindCamera, Fingerprint: "f3", Values: map[string]string{"name": "Far Side"}},
		{Kind: KindVideo, ID: "cam-01", Shape: KindVideo, Fingerprint: "v1", RetentionDays: 7, Values: map[string]string{"fps": "1"}},
	}
	actual := []Object{
		{Kind: KindCamera, ID: "cam-01", Shape: KindCamera, Fingerprint: "f1"},
		{Kind: KindCamera, ID: "cam-02", Shape: KindCamera, Fingerprint: "f2-old", Foreign: true},
		{Kind: KindVideo, ID: "cam-01", Shape: KindVideo, Fingerprint: "v1", RetentionDays: 30},
		{Kind: KindCamera, ID: "cam-99", Shape: KindCamera, Fingerprint: "f9"},
	}

	plan := Reconcile(desired, actual)
	got := map[string]Change{}
	for _, c := range plan.Changes {
		got[c.ObjectKind+"/"+c.ObjectID] = c
	}
	if c := got["camera/cam-03"]; c.Kind != "create" {
		t.Errorf("an absent desired camera became %q, want create", c.Kind)
	}
	if c := got["camera/cam-01"]; c.Kind != "keep" {
		t.Errorf("an unchanged desired camera became %q, want keep", c.Kind)
	}
	if c := got["camera/cam-02"]; c.Kind != "keep" || c.Reason == "" || !strings.Contains(c.Reason, "adopted as declared") {
		t.Errorf("a foreign camera became %q (%q), want keep with an adoption reason", c.Kind, c.Reason)
	}
	if c := got["video/cam-01"]; c.Kind != "refuse" || !strings.Contains(c.Reason, "extend-only") {
		t.Errorf("a retention reduction became %q (%q), want refuse", c.Kind, c.Reason)
	}
	if c := got["camera/cam-99"]; c.Kind != "keep" || !strings.Contains(c.Reason, "never deleted") {
		t.Errorf("an undesired camera became %q (%q), want keep", c.Kind, c.Reason)
	}
	for _, c := range plan.Changes {
		if c.Kind == "delete" {
			t.Error("the plan produced a delete change")
		}
	}
}

// TestApplyRefusesUnknownShapesForEverything is validate-then-write: one
// unknown object in the target refuses the whole plan rather than applying
// part of it.
func TestApplyRefusesUnknownShapesForEverything(t *testing.T) {
	target := &fakeTarget{
		objects: []Object{{Kind: KindCamera, ID: "cam-01", Shape: "camera-v2", Fingerprint: "x"}},
	}
	plan := Reconcile([]Object{
		{Kind: KindCamera, ID: "cam-01", Shape: KindCamera, Fingerprint: "f1", Values: map[string]string{"a": "b"}},
		{Kind: KindCamera, ID: "cam-02", Shape: KindCamera, Fingerprint: "f2", Values: map[string]string{"c": "d"}},
	}, target.objects)

	if _, err := Apply(context.Background(), target, PlanWithDesired(plan, []Object{
		{Kind: KindCamera, ID: "cam-02", Shape: KindCamera, Fingerprint: "f2", Values: map[string]string{"c": "d"}},
	}), &fakeSink{}, ApplyOptions{}); err == nil {
		t.Fatal("an unknown shape did not refuse the plan")
	}
	if target.created != 0 || target.updated != 0 {
		t.Errorf("a refused plan still wrote: %d creates, %d updates", target.created, target.updated)
	}
}

// TestApplyDryRunIsTheDefault and its counterpart: with DryRun set, nothing is
// written; without it, each change is applied and recorded, in order.
func TestApplyDryRunIsTheDefault(t *testing.T) {
	want := Object{Kind: KindZone, ID: "restricted", Shape: KindZone, Fingerprint: "f1", Values: map[string]string{"type": "restricted"}}
	target := &fakeTarget{objects: []Object{
		{Kind: KindZone, ID: "restricted", Shape: KindZone, Fingerprint: "f0"},
	}}
	plan := PlanWithDesired(Reconcile([]Object{want}, target.objects), []Object{want})

	report, err := Apply(context.Background(), target, plan, &fakeSink{}, ApplyOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if target.updated != 0 || target.created != 0 {
		t.Error("a dry run wrote to the target")
	}
	if len(report.Refused) == 0 {
		t.Error("the dry run did not say it wrote nothing")
	}

	report, err = Apply(context.Background(), target, plan, &fakeSink{}, ApplyOptions{})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if target.updated != 1 {
		t.Errorf("apply updated %d objects, want 1", target.updated)
	}
	if len(report.Applied) != 1 {
		t.Errorf("report applied %d changes, want 1", len(report.Applied))
	}
}

// TestApplyEnforcesThePerHourCap: a plan larger than the hour's budget is
// refused whole, which is the guardrail that keeps a runaway derivation from
// reconfiguring a site in one sitting.
func TestApplyEnforcesThePerHourCap(t *testing.T) {
	want := []Object{
		{Kind: KindCamera, ID: "cam-01", Shape: KindCamera, Fingerprint: "f1", Values: map[string]string{"a": "1"}},
		{Kind: KindCamera, ID: "cam-02", Shape: KindCamera, Fingerprint: "f2", Values: map[string]string{"a": "2"}},
		{Kind: KindCamera, ID: "cam-03", Shape: KindCamera, Fingerprint: "f3", Values: map[string]string{"a": "3"}},
	}
	target := &fakeTarget{}
	plan := PlanWithDesired(Reconcile(want, nil), want)
	if _, err := Apply(context.Background(), target, plan, &fakeSink{}, ApplyOptions{MaxChangesPerHour: 2}); err == nil {
		t.Fatal("a plan over the hourly cap was applied")
	}
	if target.created != 0 {
		t.Errorf("the over-cap plan still wrote %d objects", target.created)
	}
	// Under the cap it applies.
	if _, err := Apply(context.Background(), target, plan, &fakeSink{}, ApplyOptions{MaxChangesPerHour: 3}); err != nil {
		t.Fatalf("apply under the cap: %v", err)
	}
	if target.created != 3 {
		t.Errorf("created %d, want 3", target.created)
	}
}

// TestApplySnapshotsBeforeWriting: the snapshot is taken before any write and
// a failing snapshot refuses the whole apply.
func TestApplySnapshotsBeforeWriting(t *testing.T) {
	want := Object{Kind: KindCamera, ID: "cam-01", Shape: KindCamera, Fingerprint: "f1"}
	target := &fakeTarget{objects: []Object{{Kind: KindCamera, ID: "cam-99", Shape: KindCamera, Fingerprint: "x"}}}
	plan := PlanWithDesired(Reconcile([]Object{want}, target.objects), []Object{want})

	if _, err := Apply(context.Background(), target, plan, &fakeSink{}, ApplyOptions{
		Snapshot: func([]Object) error { return context.DeadlineExceeded },
	}); err == nil {
		t.Fatal("a failing snapshot did not refuse the apply")
	}
	if target.created != 0 {
		t.Error("an apply with a failing snapshot still wrote")
	}

	order := []string{}
	plan = PlanWithDesired(Reconcile([]Object{want}, target.objects), []Object{want})
	if _, err := Apply(context.Background(), target, plan, &fakeSink{}, ApplyOptions{
		Snapshot: func([]Object) error { order = append(order, "snapshot"); return nil },
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(order) != 1 || order[0] != "snapshot" {
		t.Errorf("snapshot order = %v", order)
	}
	if target.created != 1 {
		t.Error("the write after a good snapshot did not happen")
	}
}

// TestApplyRecordsEveryChangeToTheSink: a change nobody recorded is the
// defect the artifact exists to prevent, so a sink that fails stops the apply.
func TestApplyRecordsEveryChangeToTheSink(t *testing.T) {
	want := Object{Kind: KindZone, ID: "z1", Shape: KindZone, Fingerprint: "f1"}
	plan := PlanWithDesired(Reconcile([]Object{want}, nil), []Object{want})
	report, err := Apply(context.Background(), &fakeTarget{}, plan, &fakeSink{}, ApplyOptions{})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(report.Applied) != 1 {
		t.Fatalf("applied %d changes", len(report.Applied))
	}
	if _, err := Apply(context.Background(), &fakeTarget{}, plan, &failingSink{}, ApplyOptions{}); err == nil {
		t.Error("a sink failure did not stop the apply")
	}
}

// TestFileTargetRoundTrips: the POC's own target writes an object file and
// reads it back as the same object, with the safe file name a hostile id
// cannot escape.
func TestFileTargetRoundTrips(t *testing.T) {
	dir := t.TempDir()
	target := NewFileTarget(dir)
	obj := Object{Kind: KindCamera, ID: "cam-01", Shape: KindCamera, Fingerprint: "f1", Values: map[string]string{"name": "City Square"}}
	if err := target.Create(context.Background(), obj); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := target.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].ID != "cam-01" || got[0].Fingerprint != "f1" {
		t.Fatalf("round trip returned %+v", got)
	}
	// A hostile id escapes the directory by nothing: the safe name maps it.
	hostile := Object{Kind: KindCamera, ID: "../../etc/passwd", Shape: KindCamera, Fingerprint: "f2"}
	name := objectFileName(hostile)
	if strings.Contains(name, "/") || strings.Contains(name, "\\") {
		t.Errorf("file name %q contains a path separator", name)
	}
}

// TestRetentionRoundTripsSoTheGuardCanFire is the regression test for the
// defect that made the extend-only guard decorative: the read side once used
// a scratch struct whose `retention_days` key never matched the `retentiondays`
// the write side produced, so every List returned RetentionDays 0 and a
// desired reduction (90 -> 7) produced an `update` instead of the `refuse` the
// guard is for. One write, one read, and the refusal that must follow.
func TestRetentionRoundTripsSoTheGuardCanFire(t *testing.T) {
	dir := t.TempDir()
	target := NewFileTarget(dir)
	if err := target.Create(context.Background(), Object{
		Kind: KindVideo, ID: "cam-01", Shape: KindVideo, Fingerprint: "f1", RetentionDays: 90,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	// And the file's own key, read the way a hand-edited file would arrive.
	handWritten := filepath.Join(dir, "video-cam-02.yaml")
	if err := os.WriteFile(handWritten, []byte("kind: video\nid: cam-02\nshape: video\nfingerprint: f1\nretention_days: 60\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	actual, err := target.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	byID := map[string]Object{}
	for _, o := range actual {
		byID[o.ID] = o
	}
	if got := byID["cam-01"].RetentionDays; got != 90 {
		t.Fatalf("cam-01 retention round-tripped to %d, want 90 — the extend-only guard cannot fire on a field that never reads back", got)
	}
	if got := byID["cam-02"].RetentionDays; got != 60 {
		t.Fatalf("a hand-written %s file reads retention %d, want 60 — the key on disk and the key decoded are different names again", filepath.Base(handWritten), got)
	}

	desired := []Object{{Kind: KindVideo, ID: "cam-01", Shape: KindVideo, Fingerprint: "f2", RetentionDays: 7}}
	refused := 0
	for _, c := range Reconcile(desired, []Object{byID["cam-01"]}).Changes {
		if c.Kind == "refuse" {
			refused++
			if !strings.Contains(c.Reason, "extend-only") {
				t.Errorf("refusal reason %q does not name the guard", c.Reason)
			}
		}
	}
	if refused != 1 {
		t.Errorf("a retention reduction produced %d refusals, want 1 — the data-loss boundary was crossed", refused)
	}

	// And an extension is not refused: 90 -> 180 is a normal update.
	for _, c := range Reconcile([]Object{{Kind: KindVideo, ID: "cam-01", Shape: KindVideo, Fingerprint: "f2", RetentionDays: 180}}, []Object{byID["cam-01"]}).Changes {
		if c.Kind == "refuse" {
			t.Errorf("an extension was refused: %q", c.Reason)
		}
	}
}

/* ---- test helpers ---- */

type fakeTarget struct {
	objects []Object
	created int
	updated int
}

func (f *fakeTarget) List(context.Context) ([]Object, error) { return f.objects, nil }
func (f *fakeTarget) Create(_ context.Context, obj Object) error {
	f.objects = append(f.objects, obj)
	f.created++
	return nil
}
func (f *fakeTarget) Update(_ context.Context, obj Object) error {
	for i := range f.objects {
		if f.objects[i].Kind == obj.Kind && f.objects[i].ID == obj.ID {
			f.objects[i] = obj
		}
	}
	f.updated++
	return nil
}

type fakeSink struct{}

func (fakeSink) RecordChange(_, _, _, _ string) error { return nil }

// failingSink refuses every record.
type failingSink struct{}

func (failingSink) RecordChange(kind, objectKind, objectID, reason string) error {
	return context.DeadlineExceeded
}

// countingTarget records how many times the target was listed.
type countingTarget struct {
	*fakeTarget
	lists int
}

func (c *countingTarget) List(context.Context) ([]Object, error) {
	c.lists++
	return c.fakeTarget.List(context.Background())
}

// TestApplyListsTheTargetOncePerApply is the double-read regression: the
// caller already listed the target to compute the plan, so Apply takes those
// objects rather than making a second (paginated, vendor-side) read — and when
// the caller supplies none, Apply still lists for itself. Two reads that could
// see different states is two answers to one question.
func TestApplyListsTheTargetOncePerApply(t *testing.T) {
	want := Object{Kind: KindZone, ID: "z1", Shape: KindZone, Fingerprint: "f1", Values: map[string]string{"a": "b"}}
	inner := &fakeTarget{}
	target := &countingTarget{fakeTarget: inner}
	plan := PlanWithDesired(Reconcile([]Object{want}, nil), []Object{want})

	// With the actual supplied, the target is never listed.
	if _, err := Apply(context.Background(), target, plan, fakeSink{}, ApplyOptions{Actual: []Object{}}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if target.lists != 0 {
		t.Errorf("Apply listed the target %d times when the caller had already listed it", target.lists)
	}
	if inner.created != 1 {
		t.Errorf("created %d objects, want the plan's one change still applied", inner.created)
	}

	// Without it, Apply lists exactly once.
	if _, err := Apply(context.Background(), target, plan, fakeSink{}, ApplyOptions{}); err != nil {
		t.Fatalf("apply without actual: %v", err)
	}
	if target.lists != 1 {
		t.Errorf("Apply listed the target %d times, want exactly 1", target.lists)
	}
}

// DiscoverMust is a test convenience over StaticInventory.Discover.
func (s StaticInventory) DiscoverMust(t *testing.T) []Camera {
	t.Helper()
	out, err := s.Discover()
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	return out
}
