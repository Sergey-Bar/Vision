package config

import (
	"sort"
	"strings"
	"testing"
	"time"
)

// complete returns a world in which every variable is present and valid. Each
// test removes exactly one and asserts the refusal names it.
func complete() map[string]string {
	return map[string]string{
		"SP_POC_DATA_ROOT":              `C:\poc-data`,
		"SP_POC_MODE":                   ModeLive,
		"SP_POC_SOURCE_KIND":            SourceLoop,
		"SP_POC_SOURCE_ARG":             `testdata\car-crossing.mp4`,
		"SP_POC_AI_ADDR":                "127.0.0.1:8099",
		"SP_POC_AI_MODEL":               `models\yolov8n.onnx`,
		"SP_POC_SIDECAR_SCRIPT":         `pyworker/server.py`,
		"SP_POC_AI_FPS":                 "5",
		"SP_POC_AI_DEADLINE_MS":         "800ms",
		"SP_POC_AI_BUDGET_MS":           "800ms",
		"SP_POC_AI_BACKPRESSURE_MS":     "5s",
		"SP_POC_AI_INPUT_WIDTH":         "640",
		"SP_POC_SCORE_THRESHOLD":        "0.45",
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
	cfg, err := Load(lookupFrom(complete()))
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

// TestEveryVariableIsRequiredByName is the T4 test: remove one variable, get a
// refusal that names that variable and states the consequence. A variable that
// silently defaults is the defect this whole package exists to prevent.
func TestEveryVariableIsRequiredByName(t *testing.T) {
	for name := range schemaByName {
		if _, structural := structuralDefaults[name]; structural {
			continue
		}
		t.Run(name, func(t *testing.T) {
			m := complete()
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
	m := complete()
	m["SP_POC_AI_MODEL"] = "   "
	_, err := Load(lookupFrom(m))
	if names := MissingNames(err); len(names) != 1 || names[0] != "SP_POC_AI_MODEL" {
		t.Fatalf("names = %v, want [SP_POC_AI_MODEL]", names)
	}
}

// TestUnparseableValueNamesTheVariableAndTheParseError.
func TestUnparseableValueNamesTheVariableAndTheParseError(t *testing.T) {
	m := complete()
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
	m := complete()
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
		m := complete()
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
	m := complete()
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
	m := complete()
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
	m := complete()
	m["SP_POC_SOURCE_KIND"] = "rtsp"
	m["SP_POC_SOURCE_ARG"] = "rtsp://admin:hunter2@cam/stream"
	_, err := LoadWithFlags(nil, lookupFrom(m))
	if err == nil {
		t.Fatal("a source URL with credentials inline was accepted; that string is stored in the run row and printed by --config")
	}
}

func TestUIServerRefusesNonLoopback(t *testing.T) {
	m := complete()
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
	m := complete()
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
	m := complete()
	m["SP_POC_MODE"] = ModeDemoReplay
	m["SP_POC_SOURCE_KIND"] = SourceRTSP
	m["SP_POC_SOURCE_ARG"] = "rtsp://camera/stream"
	err := mustFail(t, m)
	if !strings.Contains(err.Error(), "contradiction") {
		t.Fatalf("demo-replay with rtsp was accepted: %v", err)
	}
}

func TestLoopKindRefusesURL(t *testing.T) {
	m := complete()
	m["SP_POC_SOURCE_ARG"] = "https://example.com/clip.m3u8"
	err := mustFail(t, m)
	if !strings.Contains(err.Error(), "kind=loop was given a URL") {
		t.Fatalf("loop with a URL was accepted: %v", err)
	}
}

func TestGapThresholdMustExceedHeartbeat(t *testing.T) {
	m := complete()
	m["SP_POC_HEARTBEAT_INTERVAL"] = "30s"
	m["SP_POC_GAP_THRESHOLD"] = "10s"
	err := mustFail(t, m)
	if !strings.Contains(err.Error(), "longer than the heartbeat interval") {
		t.Fatalf("a gap threshold shorter than the heartbeat was accepted: %v", err)
	}
}

func TestClockSourceIsMandatoryVocabulary(t *testing.T) {
	m := complete()
	m["SP_POC_CLOCK_SOURCE"] = "maybe"
	err := mustFail(t, m)
	if !strings.Contains(err.Error(), "is not ntp or host") {
		t.Fatalf("an unknown clock source was accepted: %v", err)
	}
}

func TestIsLiveVMSIsFalseForTheSimulator(t *testing.T) {
	m := complete()
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
