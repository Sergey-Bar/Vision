package config

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// anchorsFile writes a real anchors file in a temp directory and returns its
// path. Three anchors, which is the smallest set the loader's shape check
// accepts; this is a configuration test, not a decoding test, and the decoding
// of a real anchor set is `pyworker/test_decode.py`'s job.
func anchorsFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "anchors.txt")
	if err := os.WriteFile(path, []byte("10,13\n16,30\n33,23\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// complete returns a world in which every variable is present and valid. Each
// test removes exactly one and asserts the refusal names it.
//
// SP_POC_AI_ANCHORS has to point at a file that EXISTS, because the loader checks
// the path rather than only parsing it. A world pointing at a path that is not
// there would be testing the missing-file refusal in every case at once, and
// every test that is supposed to be about some other variable would fail for one
// reason.
func complete(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"SP_POC_DATA_ROOT":          `C:\poc-data`,
		"SP_POC_MODE":               ModeLive,
		"SP_POC_SOURCE_KIND":        SourceLoop,
		"SP_POC_SOURCE_ARG":         `testdata\car-crossing.mp4`,
		"SP_POC_SOURCE_RELAY":       "false",
		"SP_POC_AI_ADDR":            "127.0.0.1:8099",
		"SP_POC_AI_MODEL":           `models\yolov8n.onnx`,
		"SP_POC_SIDECAR_SCRIPT":     `pyworker/server.py`,
		"SP_POC_AI_FPS":             "5",
		"SP_POC_AI_DEADLINE_MS":     "800ms",
		"SP_POC_AI_BUDGET_MS":       "800ms",
		"SP_POC_AI_BACKPRESSURE_MS": "5s",
		"SP_POC_AI_INPUT_WIDTH":     "640",
		"SP_POC_SCORE_THRESHOLD":    "0.45",
		// The six that D1-D11 introduced. All are REQUIRED and all have no
		// defaults, which is the point: a run that has not said what a speck is,
		// what a ghost is, how many frames make an object, how many boxes fit on
		// a picture, which metric set its report is written against, and where
		// this model's anchor boxes live has not made those decisions, and a
		// default would be this module making them silently.
		"SP_POC_MIN_BOX_AREA":           "0.0005",
		"SP_POC_GHOST_MIN_SCORE":        "0.10",
		"SP_POC_TRACK_MIN_FRAMES":       "3",
		"SP_POC_MAX_BOXES_DRAWN":        "12",
		"SP_POC_REPORT_METRICS":         "events",
		"SP_POC_AI_ANCHORS":             anchorsFile(t),
		"SP_POC_CLASSES":                "bird,car,person",
		"SP_POC_ZONES":                  `testdata\zones.json`,
		"SP_POC_COOLDOWN_MS":            "10s",
		"SP_POC_TRACK_MAX_AGE_MS":       "2s",
		"SP_POC_TRACK_IOU":              "0.3",
		"SP_POC_LINE_TOLERANCE":         "0.01",
		"SP_POC_CLIP_BEFORE_MS":         "5s",
		"SP_POC_CLIP_AFTER_MS":          "5s",
		"SP_POC_MAX_DISK_BYTES":         "214748364800",
		"SP_POC_RSS_LIMIT_BYTES":        "1610612736",
		"SP_POC_DB_LIMIT_BYTES":         "536870912",
		"SP_POC_CPU_LIMIT_PERCENT":      "180",
		"SP_POC_DISK_HEADROOM":          "1.5",
		"SP_POC_DISK_BUDGET_DAYS":       "7",
		"SP_POC_HEARTBEAT_INTERVAL":     "10s",
		"SP_POC_GAP_THRESHOLD":          "30s",
		"SP_POC_SOAK_TARGET":            "168h",
		"SP_POC_SEGMENT_SECONDS":        "60",
		"SP_POC_CLOCK_SOURCE":           "ntp",
		"SP_POC_SIM_URL":                "http://127.0.0.1:8098",
		"SP_POC_XPROTECT_BASE_URL":      "http://127.0.0.1:8098",
		"SP_POC_XPROTECT_CLIENT_ID":     "GrantValidatorClient",
		"SP_POC_XPROTECT_CLIENT_SECRET": "s3cret",
		"SP_POC_XPROTECT_USERNAME":      "poc",
		"SP_POC_XPROTECT_PASSWORD":      "hunter2",
		"SP_POC_CAMERA_GUID":            "0d9a1b2c-1111-2222-3333-444455556666",
		"SP_POC_DEVICE_PATH":            "/Local/00000000-0000-0000-0000-000000000000",
		"SP_POC_EVENT_TYPE_GUID":        "0d9a1b2c-9999-8888-7777-666655554444",
		"SP_POC_PUSH_MAX_ATTEMPTS":      "8",
		"SP_POC_PUSH_TIMEOUT":           "15s",
		"SP_POC_UI_ADDR":                "127.0.0.1:8097",
		"SP_POC_LABEL":                  "baseline",
	}
}

func lookupFrom(m map[string]string) Lookup {
	return func(name string) (string, bool) {
		v, ok := m[name]
		return v, ok
	}
}

func TestCompleteWorldLoads(t *testing.T) {
	cfg, err := Load(lookupFrom(complete(t)))
	if err != nil {
		t.Fatalf("complete world must load: %v", err)
	}
	if cfg.Mode != ModeLive {
		t.Fatalf("mode = %q", cfg.Mode)
	}
	if got, want := cfg.Classes, []string{"bird", "car", "person"}; len(got) != len(want) {
		t.Fatalf("classes = %v, want %v", got, want)
	}
	if cfg.AIDeadline != 800*time.Millisecond {
		t.Fatalf("deadline = %s", cfg.AIDeadline)
	}
	if cfg.PushGrace != 30*time.Second {
		t.Fatalf("push grace structural default = %s, want 30s", cfg.PushGrace)
	}
}

// TestTheGhostBandMustBeTheRightWayUp is the ordering rule and it is a REFUSAL,
// because a band with the wrong order is not a band. A ghost floor above the
// approval threshold means everything between them is drawn dimmed AND approved
// at once, which is a contradiction the picture cannot express and the reader
// cannot see.
func TestTheGhostBandMustBeTheRightWayUp(t *testing.T) {
	world := complete(t)
	world["SP_POC_SCORE_THRESHOLD"] = "0.45"
	world["SP_POC_GHOST_MIN_SCORE"] = "0.60"
	_, err := Load(lookupFrom(world))
	if err == nil {
		t.Fatal("a ghost floor above the approval threshold was accepted: the rejected band is upside down")
	}
	for _, want := range []string{"SP_POC_GHOST_MIN_SCORE", "SP_POC_SCORE_THRESHOLD", "upside down"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
	// And the equal case is allowed: a band of zero width is a deliberate choice
	// to draw no ghosts at all, not a contradiction.
	world["SP_POC_GHOST_MIN_SCORE"] = "0.45"
	if _, err := Load(lookupFrom(world)); err != nil {
		t.Fatalf("a zero-width ghost band was refused: an operator may choose to draw none: %v", err)
	}
}

// TestTheMetricSetIsChosenNotInferred: exactly one, and no default. The split
// between the classifier's detection quality and the pipeline's event quality is
// the point of the report, and a run that picked one silently would produce a
// number that reads like the other.
func TestTheMetricSetIsChosenNotInferred(t *testing.T) {
	for _, ok := range []string{"detectors", "events"} {
		world := complete(t)
		world["SP_POC_REPORT_METRICS"] = ok
		cfg, err := Load(lookupFrom(world))
		if err != nil {
			t.Fatalf("%s was refused: %v", ok, err)
		}
		if cfg.ReportMetrics != ok {
			t.Errorf("ReportMetrics = %q, want %q", cfg.ReportMetrics, ok)
		}
	}
	for _, bad := range []string{"", "both", "DETECTORS", "detector", "everything"} {
		world := complete(t)
		world["SP_POC_REPORT_METRICS"] = bad
		_, err := Load(lookupFrom(world))
		if err == nil {
			t.Errorf("%q was accepted as a metric set; a report written against both at once is comparable with neither", bad)
		}
	}
}

// TestTheClassListTakesFamiliesAndRawClasses is D-9, asserted through the loader
// rather than through the taxonomy alone.
func TestTheClassListTakesFamiliesAndRawClasses(t *testing.T) {
	// The pre-existing spelling keeps working with no change to the environment.
	world := complete(t)
	world["SP_POC_CLASSES"] = "bird,car,person"
	cfg, err := Load(lookupFrom(world))
	if err != nil {
		t.Fatalf("the pre-existing spelling was refused: %v", err)
	}
	for _, want := range []string{"bird", "car", "person"} {
		if !cfg.IsApproved(want) {
			t.Errorf("%q is in the class list and is not approved", want)
		}
	}
	if cfg.IsApproved("chair") {
		t.Error("a class that is not in the list was approved")
	}

	// A family expands to its members, and the union of both spellings works.
	world["SP_POC_CLASSES"] = "vehicle,person"
	cfg, err = Load(lookupFrom(world))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"car", "truck", "bus", "person"} {
		if !cfg.IsApproved(want) {
			t.Errorf("%q is in the vehicle family and is not approved", want)
		}
	}
	for _, notWant := range []string{"chair", "bird"} {
		if cfg.IsApproved(notWant) {
			t.Errorf("%q was approved by a list of vehicle and person", notWant)
		}
	}
}

// TestAMisspelledClassIsRefusedByName: the silence this prevents is a run with
// zero events and no error, which is the most expensive kind in this module.
func TestAMisspelledClassIsRefusedByName(t *testing.T) {
	world := complete(t)
	world["SP_POC_CLASSES"] = "car,cars"
	_, err := Load(lookupFrom(world))
	if err == nil {
		t.Fatal("a misspelled class was accepted; the run would approve nothing and report zero events as a measurement")
	}
	for _, want := range []string{"SP_POC_CLASSES", "cars", "vehicle", "person", "animal", "other"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
}

// TestAClassListIsResolvedOnceAndNotPerFrame: the set is cached on the Config,
// because a run whose approved set could change mid-run would emit an event for a
// class it had already decided against, and because resolving it per frame would
// make a set of 80 strings an allocation on every frame of a seven-day run.
func TestAClassListIsResolvedOnceAndNotPerFrame(t *testing.T) {
	world := complete(t)
	world["SP_POC_CLASSES"] = "car"
	cfg, err := Load(lookupFrom(world))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if !cfg.IsApproved("car") || cfg.IsApproved("bird") {
			t.Fatalf("the approved set changed on call %d", i)
		}
	}
	// The returned map is a COPY, so a caller that mutates it cannot change the
	// answer for the next caller.
	set, err := cfg.ApprovedClasses()
	if err != nil {
		t.Fatal(err)
	}
	set["bird"] = true
	if cfg.IsApproved("bird") {
		t.Fatal("a caller mutated the resolved class set through the returned map")
	}
}

// TestAnAnchorsFileThatIsNotThereIsRefused: the anchors are in the model's .cfg
// and not in the ONNX file, so a path that does not resolve is a run that cannot
// start, said plainly at load rather than discovered in minute one.
func TestAnAnchorsFileThatIsNotThereIsRefused(t *testing.T) {
	world := complete(t)
	world["SP_POC_AI_ANCHORS"] = `C:\does\not\exist\anchors.txt`
	_, err := Load(lookupFrom(world))
	if err == nil {
		t.Fatal("a missing anchors file was accepted")
	}
	for _, want := range []string{"SP_POC_AI_ANCHORS", "not in the ONNX file", "will not guess"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
	// And the FUSED case is a WORD, not an absence. This module's own rule is
	// that a variable set to whitespace is not set, and it applies here too: "this
	// model needs no anchors" has to be something an operator typed.
	world["SP_POC_AI_ANCHORS"] = ""
	if _, err := Load(lookupFrom(world)); err == nil {
		t.Error("an empty SP_POC_AI_ANCHORS was accepted; an operator who typed nothing has said nothing")
	}
	world["SP_POC_AI_ANCHORS"] = "none"
	cfg, err := Load(lookupFrom(world))
	if err != nil {
		t.Fatalf("SP_POC_AI_ANCHORS=none was refused: a fused export needs no anchors: %v", err)
	}
	if cfg.NeedsAnchors() {
		t.Error("SP_POC_AI_ANCHORS=none still asks for anchors")
	}
}

// TestTheNewNumbersHaveBoundsWorthHaving: each of them has a refusal that names
// what breaks, because "0" and "-1" are both "wrong" and only one of them is
// wrong in a way the reader would guess.
func TestTheNewNumbersHaveBoundsWorthHaving(t *testing.T) {
	cases := []struct {
		name, raw, want string
	}{
		{"SP_POC_MIN_BOX_AREA", "0", "fraction of the frame"},
		{"SP_POC_MIN_BOX_AREA", "0.9", "fraction of the frame"},
		{"SP_POC_GHOST_MIN_SCORE", "1.5", "not a probability"},
		{"SP_POC_TRACK_MIN_FRAMES", "0", "is not an object"},
		{"SP_POC_MAX_BOXES_DRAWN", "0", "indistinguishable from a camera pointing at a wall"},
	}
	for _, tc := range cases {
		world := complete(t)
		world[tc.name] = tc.raw
		_, err := Load(lookupFrom(world))
		if err == nil {
			t.Errorf("%s=%s was accepted", tc.name, tc.raw)
			continue
		}
		if !strings.Contains(err.Error(), tc.name) {
			t.Errorf("the refusal for %s=%s does not name the variable: %v", tc.name, tc.raw, err)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("the refusal for %s=%s does not say what breaks (%q): %v", tc.name, tc.raw, tc.want, err)
		}
	}
}

// TestEveryVariableIsRequiredByName is the T4 test: remove one variable, get a// refusal that names that variable and states the consequence. A variable that
// silently defaults is the defect this whole package exists to prevent.
func TestEveryVariableIsRequiredByName(t *testing.T) {
	for name := range schemaByName {
		if _, structural := structuralDefaults[name]; structural {
			continue
		}
		t.Run(name, func(t *testing.T) {
			m := complete(t)
			delete(m, name)

			_, err := Load(lookupFrom(m))
			if err == nil {
				t.Fatalf("%s is absent but Load succeeded: a missing value must fail by name", name)
			}
			names := MissingNames(err)
			if len(names) != 1 || names[0] != name {
				t.Fatalf("missing names = %v, want exactly [%s]", names, name)
			}
			f := schemaByName[name]
			if f.Consequence == "" {
				t.Fatalf("%s has no stated consequence", name)
			}
			msg := err.Error()
			if !strings.Contains(msg, name) {
				t.Fatalf("error does not name the variable: %s", msg)
			}
			if !strings.Contains(msg, f.Consequence) {
				t.Fatalf("error does not state the consequence %q: %s", f.Consequence, msg)
			}
		})
	}
}

// TestEmptyValueIsAlsoMissing: a variable set to whitespace is not set. An
// operator who exports an empty string has not set it.
func TestEmptyValueIsAlsoMissing(t *testing.T) {
	m := complete(t)
	m["SP_POC_AI_MODEL"] = "   "
	_, err := Load(lookupFrom(m))
	if names := MissingNames(err); len(names) != 1 || names[0] != "SP_POC_AI_MODEL" {
		t.Fatalf("names = %v, want [SP_POC_AI_MODEL]", names)
	}
}

// TestUnparseableValueNamesTheVariableAndTheParseError.
func TestUnparseableValueNamesTheVariableAndTheParseError(t *testing.T) {
	m := complete(t)
	m["SP_POC_AI_FPS"] = "five"
	_, err := Load(lookupFrom(m))
	me := findMissing(t, err)
	if me.Var != "SP_POC_AI_FPS" {
		t.Fatalf("var = %q", me.Var)
	}
	if me.ParseErr == nil {
		t.Fatalf("parse error was dropped: %v", me)
	}
	if me.Consequence == "" {
		t.Fatalf("consequence was dropped")
	}
}

func TestNonPositiveDurationIsRefused(t *testing.T) {
	m := complete(t)
	m["SP_POC_SOAK_TARGET"] = "0s"
	_, err := Load(lookupFrom(m))
	me := findMissing(t, err)
	if me.Var != "SP_POC_SOAK_TARGET" {
		t.Fatalf("var = %q", me.Var)
	}
	if !strings.Contains(me.Error(), "must be positive") {
		t.Fatalf("error does not say why: %s", me.Error())
	}
}

// TestAllMissingAreReportedAtOnce: an operator fixes the environment in one
// pass, not one variable per run.
func TestAllMissingAreReportedAtOnce(t *testing.T) {
	_, err := Load(lookupFrom(map[string]string{}))
	names := MissingNames(err)
	if len(names) != len(schemaByName)-len(structuralDefaults) {
		t.Fatalf("got %d names, want %d", len(names), len(schemaByName)-len(structuralDefaults))
	}
	if !sort.StringsAreSorted(names) {
		t.Fatalf("names are not sorted: %v", names)
	}
}

// TestStructuralDefaultsAreSpellingOnly pins the exception. Adding a
// behavioural default is a one-line change here and a failing test there.
func TestStructuralDefaultsAreSpellingOnly(t *testing.T) {
	want := map[string]string{
		"SP_POC_PUSH_GRACE":      "30s",
		"SP_POC_SOURCE_USER":     "",
		"SP_POC_SOURCE_PASSWORD": "",
		"SP_POC_SOURCE_LICENCE":  "",
	}
	if len(structuralDefaults) != len(want) {
		t.Fatalf("structuralDefaults = %v, want exactly %v", structuralDefaults, want)
	}
	for k, v := range want {
		if structuralDefaults[k] != v {
			t.Fatalf("structuralDefaults[%s] = %q, want %q", k, structuralDefaults[k], v)
		}
	}
	// The camera credentials are the case that needed a thought: a FULLY PUBLIC
	// camera is open, so requiring a password would refuse the exact case this
	// project is about. An absent credential must not be a load failure.
	for _, name := range []string{"SP_POC_SOURCE_USER", "SP_POC_SOURCE_PASSWORD"} {
		m := complete(t)
		delete(m, name)
		if _, err := LoadWithFlags(nil, lookupFrom(m)); err != nil {
			t.Fatalf("%s is unset and the load was refused; a fully public camera needs no credentials: %v", name, err)
		}
	}
}

// TestACameraPasswordWithNoUserIsRefused: one of the two cross-field rules that
// makes an empty credential safe. Without it, ffmpeg would be handed
// `Basic Onh1bnRlcjI=` and the camera would answer 401 forever with a reason
// nobody would find.
func TestACameraPasswordWithNoUserIsRefused(t *testing.T) {
	m := complete(t)
	m["SP_POC_SOURCE_KIND"] = "rtsp"
	m["SP_POC_SOURCE_ARG"] = "rtsp://cam/stream"
	m["SP_POC_SOURCE_PASSWORD"] = "hunter2"
	_, err := LoadWithFlags(nil, lookupFrom(m))
	if err == nil {
		t.Fatal("a camera password with no user was accepted")
	}
	if !strings.Contains(err.Error(), "SP_POC_SOURCE_USER") {
		t.Fatalf("the refusal does not name the missing half: %v", err)
	}
}

// TestCredentialsOnALoopAreRefused: the other cross-field rule. A loop plays
// recorded material and there is nothing to authenticate, so a password carried
// for it means the run reads as though a camera were connected when none is.
func TestCredentialsOnALoopAreRefused(t *testing.T) {
	m := complete(t)
	m["SP_POC_SOURCE_USER"] = "admin"
	m["SP_POC_SOURCE_PASSWORD"] = "hunter2"
	_, err := LoadWithFlags(nil, lookupFrom(m))
	if err == nil {
		t.Fatal("camera credentials were accepted for a loop source")
	}
	if !strings.Contains(err.Error(), "nothing to authenticate") {
		t.Fatalf("the refusal does not explain why: %v", err)
	}
}

// TestAnRtspSourceRefusesCredentialsInTheURL: the leak the two variables exist
// to prevent, refused at the configuration layer as well as the source layer.
func TestAnRtspSourceRefusesCredentialsInTheURL(t *testing.T) {
	m := complete(t)
	m["SP_POC_SOURCE_KIND"] = "rtsp"
	m["SP_POC_SOURCE_ARG"] = "rtsp://admin:hunter2@cam/stream"
	_, err := LoadWithFlags(nil, lookupFrom(m))
	if err == nil {
		t.Fatal("a source URL with credentials inline was accepted; that string is stored in the run row and printed by --config")
	}
}

// TestARelayIsALiveSourceAndNothingElse: D-5's whole mechanism. A relay of
// recorded material is a live source — a recording re-published over rtsp — so
// the pair loads, and it still needs a recorded permission basis exactly as a
// camera does.
func TestARelayIsALiveSourceAndNothingElse(t *testing.T) {
	m := complete(t)
	m["SP_POC_MODE"] = ModeLive
	m["SP_POC_SOURCE_KIND"] = SourceRTSP
	m["SP_POC_SOURCE_ARG"] = "rtsp://127.0.0.1:8554/street"
	m["SP_POC_SOURCE_RELAY"] = "true"
	m["SP_POC_SOURCE_LICENCE"] = "recorded for this project; relayed over loopback. No camera is connected."
	cfg, err := Load(lookupFrom(m))
	if err != nil {
		t.Fatalf("a declared relay over rtsp must load: %v", err)
	}
	if !cfg.SourceRelay {
		t.Fatal("SourceRelay is false; the source line would say `live rtsp camera` over a recording")
	}
	if !cfg.IsLiveSource() {
		t.Fatal("a relay is a live source: the rolling recording must be a ring and a licence must be required")
	}

	// The licence rule does not relax for a relay. Watching a recording being
	// replayed and storing clips of it are different acts, and the second one
	// belongs to whoever owns the material.
	delete(m, "SP_POC_SOURCE_LICENCE")
	err = mustFail(t, m)
	if !strings.Contains(err.Error(), "SP_POC_SOURCE_LICENCE") {
		t.Fatalf("a relay with no recorded permission basis was accepted: %v", err)
	}
}

// TestARelayOfSomethingThatIsNotLiveIsRefused: the pair has no meaning to state,
// so it is refused rather than normalised away. Silently ignoring the flag would
// print "relay" nowhere, which is the one outcome this exists to prevent.
func TestARelayOfSomethingThatIsNotLiveIsRefused(t *testing.T) {
	m := complete(t)
	m["SP_POC_SOURCE_RELAY"] = "true"
	err := mustFail(t, m)
	if !strings.Contains(err.Error(), "SP_POC_SOURCE_RELAY") {
		t.Fatalf("relay=true over kind=loop was accepted: %v", err)
	}
}

// TestTheRelayFlagMustBeABoolean: a free-text "yes" would leave three spellings
// on the wire and two of them meaning nothing to a reader of `watch --config`.
func TestTheRelayFlagMustBeABoolean(t *testing.T) {
	m := complete(t)
	m["SP_POC_SOURCE_RELAY"] = "yes"
	err := mustFail(t, m)
	if !strings.Contains(err.Error(), "SP_POC_SOURCE_RELAY") {
		t.Fatalf("a non-boolean relay flag was accepted: %v", err)
	}
}

func TestUIServerRefusesNonLoopback(t *testing.T) {
	m := complete(t)
	m["SP_POC_UI_ADDR"] = "0.0.0.0:8097"
	_, err := Load(lookupFrom(m))
	if !strings.Contains(err.Error(), "not loopback") {
		t.Fatalf("0.0.0.0 UI address was accepted: %v", err)
	}
	m["SP_POC_UI_ADDR"] = "192.168.1.10:8097"
	_, err = Load(lookupFrom(m))
	if !strings.Contains(err.Error(), "not loopback") {
		t.Fatalf("routable UI address was accepted: %v", err)
	}
}

// TestPlainHTTPRefusedForRemoteVMS: a bearer token and an authenticated push
// must not cross a network in clear text.
func TestPlainHTTPRefusedForRemoteVMS(t *testing.T) {
	m := complete(t)
	m["SP_POC_XPROTECT_BASE_URL"] = "http://vms.example.com"
	_, err := Load(lookupFrom(m))
	if !strings.Contains(err.Error(), "plain http") {
		t.Fatalf("plain http to a remote host was accepted: %v", err)
	}
	m["SP_POC_XPROTECT_BASE_URL"] = "https://vms.example.com"
	if _, err := Load(lookupFrom(m)); err != nil {
		t.Fatalf("https VMS refused: %v", err)
	}
}

func TestDemoReplayRefusesLiveSource(t *testing.T) {
	m := complete(t)
	m["SP_POC_MODE"] = ModeDemoReplay
	m["SP_POC_SOURCE_KIND"] = SourceRTSP
	m["SP_POC_SOURCE_ARG"] = "rtsp://camera/stream"
	err := mustFail(t, m)
	if !strings.Contains(err.Error(), "contradiction") {
		t.Fatalf("demo-replay with rtsp was accepted: %v", err)
	}
}

func TestLoopKindRefusesURL(t *testing.T) {
	m := complete(t)
	m["SP_POC_SOURCE_ARG"] = "https://example.com/clip.m3u8"
	err := mustFail(t, m)
	if !strings.Contains(err.Error(), "kind=loop was given a URL") {
		t.Fatalf("loop with a URL was accepted: %v", err)
	}
}

func TestGapThresholdMustExceedHeartbeat(t *testing.T) {
	m := complete(t)
	m["SP_POC_HEARTBEAT_INTERVAL"] = "30s"
	m["SP_POC_GAP_THRESHOLD"] = "10s"
	err := mustFail(t, m)
	if !strings.Contains(err.Error(), "longer than the heartbeat interval") {
		t.Fatalf("a gap threshold shorter than the heartbeat was accepted: %v", err)
	}
}

func TestClockSourceIsMandatoryVocabulary(t *testing.T) {
	m := complete(t)
	m["SP_POC_CLOCK_SOURCE"] = "maybe"
	err := mustFail(t, m)
	if !strings.Contains(err.Error(), "is not ntp or host") {
		t.Fatalf("an unknown clock source was accepted: %v", err)
	}
}

func TestIsLiveVMSIsFalseForTheSimulator(t *testing.T) {
	m := complete(t)
	m["SP_POC_XPROTECT_BASE_URL"] = "http://127.0.0.1:8098"
	cfg, err := Load(lookupFrom(m))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IsLiveVMS() {
		t.Fatal("loopback simulator reported as a live VMS: the report would claim a Milestone landing that never happened")
	}
	m["SP_POC_XPROTECT_BASE_URL"] = "https://vms.example.com"
	cfg, err = Load(lookupFrom(m))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.IsLiveVMS() {
		t.Fatal("an https remote base URL was not recognised as a live VMS")
	}
}

func TestSecretsAreEnumerated(t *testing.T) {
	got := Secrets()
	// THREE, and the third arrived with the live camera. A camera password that
	// reaches a log, a run row or a process table is a public camera anybody on
	// the network can watch.
	want := []string{"SP_POC_SOURCE_PASSWORD", "SP_POC_XPROTECT_CLIENT_SECRET", "SP_POC_XPROTECT_PASSWORD"}
	if len(got) != len(want) {
		t.Fatalf("Secrets() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Secrets()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// The camera's USER is not a secret. It is a name, it is printed on the
	// source line on purpose, and treating it as a secret would hide the fact
	// that a run authenticated as somebody.
	if slicesContains(Secrets(), "SP_POC_SOURCE_USER") {
		t.Fatal("the camera's user name is treated as a secret; the report must name who it authenticated as")
	}
}

func slicesContains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestVariableNamesArePrefixed(t *testing.T) {
	for name := range schemaByName {
		if !strings.HasPrefix(name, EnvPrefix) {
			t.Fatalf("%s does not carry the %s prefix; an unprefixed variable could shadow a platform variable", name, EnvPrefix)
		}
	}
}

func mustFail(t *testing.T, m map[string]string) error {
	t.Helper()
	_, err := Load(lookupFrom(m))
	if err == nil {
		t.Fatal("expected a refusal, got none")
	}
	return err
}

func findMissing(t *testing.T, err error) *MissingError {
	t.Helper()
	agg, ok := err.(*AggregateError)
	if !ok || len(agg.Errors) != 1 {
		t.Fatalf("want exactly one problem, got %v", err)
	}
	me, ok := agg.Errors[0].(*MissingError)
	if !ok {
		t.Fatalf("want a *MissingError, got %T", agg.Errors[0])
	}
	return me
}
