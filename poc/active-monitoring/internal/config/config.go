// Package config reads the POC's entire runtime configuration in exactly one
// place, from environment variables only, and refuses to invent a value that
// would change behaviour.
//
// The rule the package exists to enforce: every value the pipeline depends on
// comes from one variable, and a missing variable fails by name and by
// consequence. There is no default that changes behaviour. Structural defaults
// (units, separators) are permitted and are listed in structuralDefaults, which
// the test below pins.
package config

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/classify"
)

// NoAnchorsSentinel is the value SP_POC_AI_ANCHORS takes when the model is a
// FUSED export and needs no anchor boxes.
//
// It is a word rather than an empty string because of this module's own rule: a
// variable set to whitespace is NOT set. That rule exists so a half-typed line is
// caught, and it applies here too — so "this model needs no anchors" has to be
// something an operator typed, not something they failed to. `pocctl model-probe`
// prints which kind of export the model is, so the operator is answering a
// question the tool has already answered for them.
const NoAnchorsSentinel = "none"

// NeedsAnchors reports whether the run declared anchor boxes. A FUSED export
// declares `none`; an ANCHORED one declares a file path. The sidecar is given
// the path either way, and reads this to decide whether to pass it.
func (c Config) NeedsAnchors() bool {
	return !strings.EqualFold(strings.TrimSpace(c.AIAnchors), NoAnchorsSentinel)
}

// EnvPrefix is prepended to every variable this package reads. The root
// platform uses SP_; the POC must not shadow a platform variable by accident,
// so it uses its own.
const EnvPrefix = "SP_POC_"

// MissingError is returned when a required variable is absent or unparseable.
// It carries the variable name, what breaks without it, and how to set it, so
// an operator reading a log line does not have to guess.
type MissingError struct {
	Var         string
	Consequence string
	Remedy      string
	Raw         string
	ParseErr    error
}

func (e *MissingError) Error() string {
	if e.ParseErr != nil {
		return fmt.Sprintf("%s: value %q is not usable (%v); without it %s; set it with %s",
			e.Var, e.Raw, e.ParseErr, e.Consequence, e.Remedy)
	}
	return fmt.Sprintf("%s: not set; without it %s; set it with %s", e.Var, e.Consequence, e.Remedy)
}

type kind int

const (
	kindString kind = iota
	kindInt
	kindFloat
	kindDuration
	kindBool
)

type field struct {
	// Name is the full variable name, including EnvPrefix.
	Name string
	kind kind
	// Consequence is what breaks when the variable is absent. It is written to
	// be read by a person, not by a linter.
	Consequence string
	// Remedy is the example assignment, e.g. "set SP_POC_AI_FPS=5".
	Remedy string
	// structural marks a default that is a unit or a spelling and cannot change
	// a verdict. Non-structural fields have no default at all.
	// structural is a BOOL and not a non-empty string, because a legitimate
	// structural default IS the empty string: the two camera credentials.
	structural    bool
	structuralVal string
	// when is the CONDITION under which this variable IS required.
	//
	// It exists because W1-4 retired the sidecar from the trigger path and every
	// sidecar variable was still listed as unconditionally required. An operator
	// running the shipped pipeline had to declare an ONNX model and a Python
	// script it would never execute, and the remedy text described a run that
	// "dies in minute one" — a run that has no sidecar in it and therefore cannot
	// die that way. A gate that is wrong in the safe direction is still wrong,
	// because the operator learns to type things in to get past it.
	//
	// The value is `<VAR>=<value>`. A condition naming a structural variable
	// sees that variable's structural default, because `conditionHolds` resolves
	// it the same way `get` does — a condition that read the raw environment
	// while the run read the default would gate on a different value from the one
	// the run uses, which is the worst way for a gate to be wrong.
	when string
}

// schema is the single declaration of every variable the POC reads. A variable
// that is not here is not read anywhere in the module; config_test.go asserts
// that by construction (see TestSchemaCoversEveryAccessor).
var schema = []field{
	{Name: "SP_POC_DATA_ROOT", kind: kindString,
		Consequence: "there is nowhere to put the SQLite WAL, the rolling segments or the evidence store, and the run cannot record anything",
		Remedy:      "set SP_POC_DATA_ROOT to a path on a LOCAL disk, e.g. C:\\poc-data"},
	{Name: "SP_POC_MODE", kind: kindString,
		Consequence: "the screen header cannot say whether this is a live run or a demo replay, and a demo could be presented as a measurement",
		Remedy:      "set SP_POC_MODE=live or SP_POC_MODE=demo-replay"},

	// --- source ---
	{Name: "SP_POC_SOURCE_KIND", kind: kindString,
		Consequence: "there is no video source, so nothing can be recorded and the AI has no frames",
		Remedy:      "set SP_POC_SOURCE_KIND to one of loop, rtsp, url"},
	{Name: "SP_POC_SOURCE_ARG", kind: kindString,
		Consequence: "ffmpeg has no input to read, so the pipeline never produces a first frame and preflight cannot pass",
		Remedy:      "set SP_POC_SOURCE_ARG to a file path (loop) or a URL (rtsp, url), with NO credentials in it"},
	{Name: "SP_POC_SOURCE_USER", kind: kindString,
		Consequence: "a camera that needs a login cannot be read, and there is nowhere else its credentials may safely be put: SP_POC_SOURCE_ARG is stored in the run row and printed by `watch --config`",
		Remedy:      "set SP_POC_SOURCE_USER to the camera's user name, e.g. admin"},
	{Name: "SP_POC_SOURCE_PASSWORD", kind: kindString,
		Consequence: "a camera that needs a password cannot be read, and a camera password that reaches a log, a run row or a process table is a public camera anybody on the network can watch",
		Remedy:      "set SP_POC_SOURCE_PASSWORD from the environment, never from a file in the repository"},
	// D-5. `source.Label()` said "live rtsp camera" for every RTSP source, and a
	// relay of RECORDED material is not a camera. This variable is how the run
	// says so, on the screen, in the log and in the report, in one derived
	// sentence that no caller composes by hand.
	//
	// It is REQUIRED rather than defaulted to false because the default is the
	// dangerous one: an operator who relays a recording and forgets this line
	// gets "live rtsp camera" printed beside real numbers, and the whole point of
	// this module is not making a claim it cannot back.
	{Name: "SP_POC_SOURCE_RELAY", kind: kindBool,
		Consequence: "the source line says `live rtsp camera` for a feed that is a recording being relayed, so a viewer is told a camera is connected when nothing is. The relay is what proves the rtsp PATH; it is not evidence of a camera",
		Remedy:      "set SP_POC_SOURCE_RELAY=true for a recording re-published over rtsp, or SP_POC_SOURCE_RELAY=false for an actual camera"},

	// --- AI sidecar ---
	//
	// Every one of these is conditional on `SP_POC_TRIGGER_MODE != sidecar`. W1-4
	// retired the sidecar from the trigger path: the shipped pipeline measures a
	// region with pure-Go arithmetic and asks a hosted model once per event, so it
	// needs no ONNX model, no Python worker and no anchors. They are kept, because
	// `trigger-mode=sidecar` is still a real mode and a useful one — it is how a
	// threshold gets chosen — and a tool with one path is a tool with one opinion.
	{Name: "SP_POC_TRIGGER_MODE", kind: kindString, structural: true, structuralVal: "novelty",
		Consequence: "the run would not say where its boxes come from, and the cost argument (one call per event against one per frame) has no subject",
		Remedy:      "set SP_POC_TRIGGER_MODE=novelty (shipped) or SP_POC_TRIGGER_MODE=sidecar (the older per-frame ONNX classifier)"},
	{Name: "SP_POC_AI_ADDR", kind: kindString, when: "SP_POC_TRIGGER_MODE=sidecar",
		Consequence: "there is no sidecar to send frames to, and every detection would be not_asked",
		Remedy:      "set SP_POC_AI_ADDR=127.0.0.1:8099"},
	{Name: "SP_POC_AI_MODEL", kind: kindString, when: "SP_POC_TRIGGER_MODE=sidecar",
		Consequence: "preflight cannot load the model, and a run that starts without it dies in minute one",
		Remedy:      "set SP_POC_AI_MODEL to the path of an ONNX file, e.g. models\\yolov8n.onnx"},
	{Name: "SP_POC_SIDECAR_SCRIPT", kind: kindString, when: "SP_POC_TRIGGER_MODE=sidecar",
		Consequence: "there is no Python worker to send frames to, and every detection would be not_asked",
		Remedy:      "set SP_POC_SIDECAR_SCRIPT=pyworker/server.py"},
	{Name: "SP_POC_AI_FPS", kind: kindInt,
		Consequence: "the decode pipe rate is unknown, so the disk budget computed by preflight is unknown and AC5 has nothing to check against",
		Remedy:      "set SP_POC_AI_FPS=5"},
	{Name: "SP_POC_AI_DEADLINE_MS", kind: kindDuration,
		Consequence: "a slow answer is indistinguishable from a negative answer, which breaks R4 and AC6",
		Remedy:      "set SP_POC_AI_DEADLINE_MS=800ms"},
	{Name: "SP_POC_AI_BUDGET_MS", kind: kindDuration,
		Consequence: "AC6's p99 clause has no declared ceiling, so the criterion cannot be scored as anything but NOT RUN",
		Remedy:      "set SP_POC_AI_BUDGET_MS=800ms"},
	{Name: "SP_POC_AI_BACKPRESSURE_MS", kind: kindDuration,
		Consequence: "a blocked pipe has no deadline, so ffmpeg and the recording stall silently instead of raising ai_pipe_backpressure",
		Remedy:      "set SP_POC_AI_BACKPRESSURE_MS=5s"},
	{Name: "SP_POC_AI_INPUT_WIDTH", kind: kindInt,
		Consequence: "the sidecar is fed frames of unknown size, so it would have to guess and a wrong guess is a corrupted letterbox",
		Remedy:      "set SP_POC_AI_INPUT_WIDTH=640"},

	// --- the verifier: which model, which region ---
	//
	// The provider defaults to `none`, so a run that never asks a model says so
	// rather than printing an empty provider name on a slide. `none` is not a
	// failure: the novelty trigger measures a region and proposes it, and an
	// operator who wants an unverified proposal has exactly that.
	{Name: "SP_POC_VLM_PROVIDER", kind: kindString, structural: true, structuralVal: "none",
		Consequence: "the report cannot say who verified the events, and an unverified proposal reads like a verified incident",
		Remedy:      "set SP_POC_VLM_PROVIDER=hafnia to verify each event against a hosted model, or none to run the local trigger alone"},
	{Name: "SP_POC_VLM_REGION", kind: kindString, when: "SP_POC_VLM_PROVIDER=hafnia",
		Consequence: "footage has no declared destination, and a default here is a site whose video went somewhere nobody chose",
		Remedy:      "set SP_POC_VLM_REGION=eu or SP_POC_VLM_REGION=us. There is NO failover between regions: during an outage a silent failover is a sovereignty breach"},
	{Name: "SP_POC_VLM_API_KEY", kind: kindString, when: "SP_POC_VLM_PROVIDER=hafnia",
		Consequence: "the call would upload footage unauthenticated, or silently not happen at all",
		Remedy:      "set SP_POC_VLM_API_KEY from the secret store. It is never a flag, a URL or a file in the repository"},
	{Name: "SP_POC_VLM_MODEL", kind: kindString, when: "SP_POC_VLM_PROVIDER=hafnia",
		Consequence: "the request would name no model, and the attribution record would have nothing to attribute to",
		Remedy:      "set SP_POC_VLM_MODEL to the provider's model identifier, e.g. cosmos-3-nano-16b"},
	{Name: "SP_POC_VLM_DAILY_CAP", kind: kindInt, when: "SP_POC_VLM_PROVIDER=hafnia",
		Consequence: "the site's spend against the contract is unbounded by anything local, and the cost argument depends on a local cap existing",
		Remedy:      "set SP_POC_VLM_DAILY_CAP=500 — the most inference calls this site may make per UTC day"},

	// --- the trigger: novelty threshold, sustain, grid ---
	//
	// These are structural, because a detector with no stated reference is a word
	// and not a measurement, and the threshold is the number a judge will
	// challenge. Both defaults are the measured ones and both are stored beside
	// every decision they produced, so a report can print the number rather than
	// name it.
	{Name: "SP_POC_NOVELTY_THRESHOLD", kind: kindFloat, structural: true, structuralVal: "0.35",
		Consequence: "the trigger would have no bar, and a bar of zero is a detector that fires on every frame",
		Remedy:      "set SP_POC_NOVELTY_THRESHOLD=0.35 — the fraction of the frame that must change"},
	{Name: "SP_POC_NOVELTY_RELEASE", kind: kindFloat, structural: true, structuralVal: "0.20",
		Consequence: "there is no hysteresis band, so a detector firing on one frame and resetting on the next stutters on a noisy scene",
		Remedy:      "set SP_POC_NOVELTY_RELEASE=0.20 — below this the sustain streak resets; it must be at or under the threshold"},
	{Name: "SP_POC_NOVELTY_SUSTAIN", kind: kindInt, structural: true, structuralVal: "90",
		Consequence: "one frame of difference would be an incident, and one frame is a car, a shadow or a compression artefact",
		Remedy:      "set SP_POC_NOVELTY_SUSTAIN=90 — consecutive frames a whole-view change must persist before it fires"},
	{Name: "SP_POC_NOVELTY_GRID", kind: kindInt, structural: true, structuralVal: "64",
		Consequence: "the mask would be computed at whatever resolution the frame happened to be, so the score would not be comparable between runs",
		Remedy:      "set SP_POC_NOVELTY_GRID=64 — the width of the luma grid the region is measured on"},

	// --- detection ---
	{Name: "SP_POC_SCORE_THRESHOLD", kind: kindFloat,
		Consequence: "there is no approved class threshold, so every box would be both kept and dropped depending on who asked",
		Remedy:      "set SP_POC_SCORE_THRESHOLD=0.45"},
	{Name: "SP_POC_CLASSES", kind: kindString,
		Consequence: "an unapproved class could reach the line-cross machine, and the class list in the report would be whatever the model happened to emit",
		Remedy:      "set SP_POC_CLASSES=vehicle,person — a family name or a raw class name; both are accepted, and an entry that is neither is refused by name"},
	{Name: "SP_POC_AI_ANCHORS", kind: kindString, when: "SP_POC_TRIGGER_MODE=sidecar",
		Consequence: "an ANCHORED export — one that publishes an output per stride — cannot be decoded without its anchor boxes, and they are in the model's .cfg rather than in the ONNX file. Without them the sidecar refuses the model by name rather than answering 'no objects' for every frame of the run",
		Remedy:      "set SP_POC_AI_ANCHORS=models\\yolov8n.anchors.txt for an anchored export, or SP_POC_AI_ANCHORS=none to STATE that this model is a fused export and needs none. 'none' rather than an empty value, because a variable set to nothing has said nothing and this module treats it as unset"},
	{Name: "SP_POC_MIN_BOX_AREA", kind: kindFloat,
		Consequence: "a box smaller than this fraction of the frame is a speck, a shadow edge or a letterbox band, and a line-crossing machine that counts specks produces crossings that never happened",
		Remedy:      "set SP_POC_MIN_BOX_AREA=0.0005"},
	{Name: "SP_POC_GHOST_MIN_SCORE", kind: kindFloat,
		Consequence: "below this nothing is drawn at all, and between it and SP_POC_SCORE_THRESHOLD a box is drawn dimmed. Without the band a picture showing only approved boxes cannot answer 'why is the road empty?', and a screen that cannot answer that is a screen nobody trusts",
		Remedy:      "set SP_POC_GHOST_MIN_SCORE=0.10"},
	{Name: "SP_POC_TRACK_MIN_FRAMES", kind: kindInt,
		Consequence: "one frame of a vehicle at a line is not a crossing, and a shadow, a reflection or a compression artefact is; only time tells them apart, so without this every one of them becomes an event",
		Remedy:      "set SP_POC_TRACK_MIN_FRAMES=3"},
	{Name: "SP_POC_MAX_BOXES_DRAWN", kind: kindInt,
		Consequence: "a thirty-vehicle frame is thirty boxes, and thirty unfilled outlines over a road is a picture nobody can read. What the cap left off is counted per family and printed, because a cap that silently drops is a cap that lies",
		Remedy:      "set SP_POC_MAX_BOXES_DRAWN=12"},
	{Name: "SP_POC_REPORT_METRICS", kind: kindString,
		Consequence: "the classifier's detection quality and the pipeline's event quality are different measurements, and a report written against both at once is comparable with neither",
		Remedy:      "set SP_POC_REPORT_METRICS=detectors or SP_POC_REPORT_METRICS=events — exactly one, and no default, because a report whose metric set was inferred would be comparable with a report that chose"},
	{Name: "SP_POC_ZONES", kind: kindString,
		Consequence: "there is no line to cross, so the line-cross machine can never reach done and every detection is lost",
		Remedy:      "set SP_POC_ZONES=testdata\\zones.json"},
	{Name: "SP_POC_COOLDOWN_MS", kind: kindDuration,
		Consequence: "one bird crossing twice would create two events, and the event rate in the report would be a function of jitter",
		Remedy:      "set SP_POC_COOLDOWN_MS=10s"},
	{Name: "SP_POC_TRACK_MAX_AGE_MS", kind: kindDuration,
		Consequence: "tracks would live forever, so a re-identification after a long occlusion would be treated as a new crossing",
		Remedy:      "set SP_POC_TRACK_MAX_AGE_MS=2s"},
	{Name: "SP_POC_TRACK_IOU", kind: kindFloat,
		Consequence: "track association has no threshold, so the tracker would pick one arbitrarily",
		Remedy:      "set SP_POC_TRACK_IOU=0.3"},
	{Name: "SP_POC_LINE_TOLERANCE", kind: kindFloat,
		Consequence: "a track touching the line would flip state on floating-point noise, and the crossing count would not be reproducible",
		Remedy:      "set SP_POC_LINE_TOLERANCE=0.01"},

	// --- evidence ---
	{Name: "SP_POC_CLIP_BEFORE_MS", kind: kindDuration,
		Consequence: "the clip window is undefined, so a clip could start after the event it is supposed to show",
		Remedy:      "set SP_POC_CLIP_BEFORE_MS=5s"},
	{Name: "SP_POC_CLIP_AFTER_MS", kind: kindDuration,
		Consequence: "the clip window is undefined, so a clip could end before the event is visible",
		Remedy:      "set SP_POC_CLIP_AFTER_MS=5s"},
	{Name: "SP_POC_MAX_DISK_BYTES", kind: kindInt,
		Consequence: "AC5's disk clause has no declared ceiling, and a 7-day run at 4 Mbps would fill the disk on day three",
		Remedy:      "set SP_POC_MAX_DISK_BYTES=214748364800"},
	{Name: "SP_POC_RSS_LIMIT_BYTES", kind: kindInt,
		Consequence: "AC5's memory clause has no declared ceiling, so a leak is only visible when the machine dies",
		Remedy:      "set SP_POC_RSS_LIMIT_BYTES=1610612736"},
	{Name: "SP_POC_DB_LIMIT_BYTES", kind: kindInt,
		Consequence: "AC5's database clause has no declared ceiling",
		Remedy:      "set SP_POC_DB_LIMIT_BYTES=536870912"},
	{Name: "SP_POC_CPU_LIMIT_PERCENT", kind: kindFloat,
		Consequence: "the ffmpeg+python CPU clause of AC5 is NOT RUN instead of PASS, and a real regression would hide behind it",
		Remedy:      "set SP_POC_CPU_LIMIT_PERCENT=180"},
	{Name: "SP_POC_DISK_HEADROOM", kind: kindFloat,
		Consequence: "preflight has no 1.5x headroom rule to enforce, so the run starts and fails on day three",
		Remedy:      "set SP_POC_DISK_HEADROOM=1.5"},
	{Name: "SP_POC_DISK_BUDGET_DAYS", kind: kindInt,
		Consequence: "preflight has no horizon to compute the required free space against, so the 1.5x rule is meaningless",
		Remedy:      "set SP_POC_DISK_BUDGET_DAYS=7"},

	// --- soak ---
	{Name: "SP_POC_HEARTBEAT_INTERVAL", kind: kindDuration,
		Consequence: "AC1's dropout definition is undefined, so a 10 minute silence is indistinguishable from a hang",
		Remedy:      "set SP_POC_HEARTBEAT_INTERVAL=10s"},
	{Name: "SP_POC_GAP_THRESHOLD", kind: kindDuration,
		Consequence: "the 30 second AC1 gap boundary does not exist, so AC1 cannot be scored",
		Remedy:      "set SP_POC_GAP_THRESHOLD=30s"},
	{Name: "SP_POC_SOAK_TARGET", kind: kindDuration,
		Consequence: "the report cannot distinguish PASS from PROVISIONAL, and 40 minutes would be reported as a pass",
		Remedy:      "set SP_POC_SOAK_TARGET=168h"},
	{Name: "SP_POC_SEGMENT_SECONDS", kind: kindInt,
		Consequence: "the rolling recorder has no segment length, so there is nothing to map an instant onto",
		Remedy:      "set SP_POC_SEGMENT_SECONDS=60"},
	{Name: "SP_POC_CLOCK_SOURCE", kind: kindString,
		Consequence: "the report cannot say whether the timestamps are synchronised, and a detection pushed at the wrong wall clock is the wrong detection",
		Remedy:      "set SP_POC_CLOCK_SOURCE=ntp or SP_POC_CLOCK_SOURCE=host"},
	{Name: "SP_POC_SIM_URL", kind: kindString,
		Consequence: "preflight cannot ask the push target who it is, so the run cannot prove it reached a simulator rather than nothing",
		Remedy:      "set SP_POC_SIM_URL=http://127.0.0.1:8098"},

	// --- push target ---
	{Name: "SP_POC_XPROTECT_BASE_URL", kind: kindString,
		Consequence: "there is no push target, so no event is ever attempted and AC4 is NOT RUN",
		Remedy:      "set SP_POC_XPROTECT_BASE_URL=http://127.0.0.1:8098 (xpsim) or https://<vms>"},
	{Name: "SP_POC_XPROTECT_CLIENT_ID", kind: kindString,
		Consequence: "there is no OAuth client, so the token call cannot be made and auth_failed is terminal for the whole run",
		Remedy:      "set SP_POC_XPROTECT_CLIENT_ID"},
	{Name: "SP_POC_XPROTECT_CLIENT_SECRET", kind: kindString,
		Consequence: "the token call has no secret, so auth_failed is terminal for the whole run",
		Remedy:      "set SP_POC_XPROTECT_CLIENT_SECRET (read from the environment, never from a repo file)"},
	{Name: "SP_POC_XPROTECT_USERNAME", kind: kindString,
		Consequence: "the password grant has no user, so it cannot be used as the fallback when the client credential is refused",
		Remedy:      "set SP_POC_XPROTECT_USERNAME"},
	{Name: "SP_POC_XPROTECT_PASSWORD", kind: kindString,
		Consequence: "the password grant has no password, so it cannot be used as the fallback",
		Remedy:      "set SP_POC_XPROTECT_PASSWORD (read from the environment, never from a repo file)"},
	{Name: "SP_POC_CAMERA_GUID", kind: kindString,
		Consequence: "there is no event source, so the pushed event would name nothing and an Alarm Definition could never match it",
		Remedy:      "set SP_POC_CAMERA_GUID=<camera guid>"},
	{Name: "SP_POC_DEVICE_PATH", kind: kindString,
		Consequence: "the bookmark has no devicePath, which the contract marks required, so every bookmark is a 400",
		Remedy:      "set SP_POC_DEVICE_PATH=/Bookmarks/Camera, e.g. /Local/00000000-0000-0000-0000-000000000000"},
	{Name: "SP_POC_EVENT_TYPE_GUID", kind: kindString,
		Consequence: "the event has no type, which the contract marks required, so every push is a 400",
		Remedy:      "set SP_POC_EVENT_TYPE_GUID=<event type guid>"},
	{Name: "SP_POC_PUSH_MAX_ATTEMPTS", kind: kindInt,
		Consequence: "the outbox backoff budget has no bound, so a dead provider would be retried forever and AC4 could never conclude",
		Remedy:      "set SP_POC_PUSH_MAX_ATTEMPTS=8"},
	{Name: "SP_POC_PUSH_TIMEOUT", kind: kindDuration,
		Consequence: "a hung provider has no deadline, so the worker is stuck and AC1 records a silent hang rather than a timeout",
		Remedy:      "set SP_POC_PUSH_TIMEOUT=15s"},

	// --- ui ---
	{Name: "SP_POC_UI_ADDR", kind: kindString,
		Consequence: "the live screen has no address and every number on it would have to be read from the log",
		Remedy:      "set SP_POC_UI_ADDR=127.0.0.1:8097 (loopback only; anything else is refused)"},
	{Name: "SP_POC_LABEL", kind: kindString,
		Consequence: "the run has no human label, so the report cannot say what it was measuring and two runs are indistinguishable",
		Remedy:      "set SP_POC_LABEL=baseline"},
}

// structuralDefaults are the only variables with a built-in value, and every one
// of them is a spelling, a unit, or an ABSENCE that cannot change a verdict.
//
// The two camera credentials are here for a specific reason: a FULLY PUBLIC
// camera is open, and requiring a password to describe one would refuse the
// exact case this project is about. An empty credential does not change a
// verdict — it changes whether the camera is readable, and the report prints
// which, in words, on the source line.
//
// The two CROSS-FIELD rules that make an empty credential safe are in Validate:
// a password with no user is refused, and credentials on a loop are refused.
var structuralDefaults = map[string]string{
	"SP_POC_PUSH_GRACE":  "30s",
	"SP_POC_SOURCE_USER": "",
	// The licence basis is NOT here: a live source REQUIRES it, and a missing one has
	// to be a refusal with a reason, not a default.
	"SP_POC_SOURCE_PASSWORD": "",
	// The licence basis is NOT a default: a LIVE source must supply one and a
	// missing one is a refusal with a reason. A loop over material the operator
	// already has is not the tool's business.
	"SP_POC_SOURCE_LICENCE": "",
}

var schemaByName = func() map[string]field {
	m := make(map[string]field, len(schema)+len(structuralDefaults))
	for _, f := range schema {
		m[f.Name] = f
	}
	for name, def := range structuralDefaults {
		// structural is a BOOL and not the value, because a legitimate
		// structural default is the EMPTY STRING: the two camera credentials.
		// Comparing the value against "" cannot tell "structural and empty"
		// from "not structural", and the loader then refuses every fully public
		// camera — the exact case this project is about.
		kind := kindString
		if name == "SP_POC_PUSH_GRACE" {
			kind = kindDuration
		}
		m[name] = field{Name: name, kind: kind, structural: true, structuralVal: def,
			Consequence: "this variable is a spelling, a unit, or an absence, and it cannot change a verdict. A fully public camera needs neither camera credential set, and requiring one would refuse the case this project is about.",
			Remedy:      "set " + name + " explicitly if the camera needs it"}
	}
	return m
}()

// Config is the whole runtime configuration. Nothing else in the module reads
// the environment.
type Config struct {
	DataRoot string
	Mode     string

	SourceKind string
	SourceArg  string
	// SourceUser and SourcePassword authenticate a LIVE camera. They are separate
	// from SourceArg because a URL with credentials inline is stored in the run
	// row, printed by --config, and visible in the process table.
	// SourceLicence is the PERMISSION BASIS for the material, not a licence file
	// name. It is recorded in the run and printed in the report, so a reader of the
	// report can see on what basis the footage was used.
	SourceLicence  string
	SourceUser     string
	SourcePassword string
	// SourceRelay says the "live" source is a RECORDING re-published over a
	// network protocol, and not a camera. D-5.
	//
	// The distinction is the whole of the field. `kind=rtsp` alone cannot tell a
	// Milestone site camera from an ffmpeg publisher on loopback, and the source
	// line the screen and the report print is derived from this: a relay is
	// labelled "live rtsp (relay of recorded material, NOT a camera)" and a camera
	// is labelled "live rtsp camera".
	SourceRelay bool

	AIAddr         string
	AIModel        string
	SidecarScript  string
	AIFPS          int
	AIDeadline     time.Duration
	AIBudget       time.Duration
	AIBackpressure time.Duration
	AIInputWidth   int

	// TriggerMode is where the boxes come from: `novelty` (the shipped path —
	// pure-Go measurement, one model call per event) or `sidecar` (the older
	// per-frame ONNX classifier, kept as a threshold-selection tool).
	TriggerMode string

	// The verifier. W1-4 through W1-7: the hosted model that confirms what the
	// local measurement proposed.
	//
	// VLMProvider is `none` by default and `none` is not a failure. The novelty
	// trigger measures a region and proposes it; without a verifier an operator
	// gets an UNVERIFIED proposal, which is a different thing from a verified
	// incident and is labelled as one.
	VLMProvider string
	// VLMRegion is `eu` or `us`, and there is no failover between them.
	VLMRegion string
	// VLMAPIKey comes from the secret store. It is never logged, never a flag and
	// never in the repository.
	VLMAPIKey string
	// VLMModel is the provider's model identifier. It goes into the attribution
	// record as a CLAIM by the provider; there is no model hash because a hosted
	// model has no weights to hash.
	VLMModel string
	// VLMDailyCap is the site's local daily call cap, on a UTC day.
	VLMDailyCap int

	// The trigger's measurement settings. Both are stored beside every decision
	// they produced, so a report prints the number rather than naming it.
	NoveltyThreshold float64
	NoveltyRelease   float64
	NoveltySustain   int
	NoveltyGrid      int

	ScoreThreshold float64
	// Classes is SP_POC_CLASSES as written: a list of family names and class
	// names, both accepted. `ApprovedClasses` is the resolved set and is what the
	// run filters on.
	Classes []string
	// AIAnchors is the darknet anchor file for an ANCHORED export: empty for a
	// fused export, required for an anchored one, and the rule is the model's own
	// rather than a configuration choice. An export that publishes one output per
	// stride needs anchors and one that publishes a single fused output does not.
	// The anchors live in the model's `.cfg` and are NOT in the ONNX file, so
	// there is nothing to infer them from and the sidecar refuses the model
	// without this. See `models/yolov8n.anchors.txt` and `pocctl model-probe`.
	AIAnchors string
	// MinBoxArea is SP_POC_MIN_BOX_AREA: the smallest box, as a fraction of the
	// frame area, treated as an object at all. Below it a box is a speck, a
	// shadow edge or a letterbox band, and a line-crossing machine that counts
	// specks produces crossings that never happened.
	MinBoxArea float64
	// GhostMinScore is SP_POC_GHOST_MIN_SCORE: the floor below which nothing is
	// drawn at all. Between it and ScoreThreshold a box is drawn as a GHOST —
	// visible, counted nowhere, never an event. The band exists because a picture
	// showing only approved boxes cannot answer "why is the road empty?", and a
	// screen that cannot answer that is a screen nobody trusts.
	GhostMinScore float64
	// TrackMinFrames is SP_POC_TRACK_MIN_FRAMES: how many consecutive frames a
	// track must be seen before it may emit a crossing at all. One frame of a
	// vehicle at a line is not a crossing; a shadow, a reflection or a
	// compression artefact is, and only time tells them apart.
	TrackMinFrames int
	// MaxBoxesDrawn is SP_POC_MAX_BOXES_DRAWN, the cap on boxes on the picture.
	// What the cap left off is COUNTED per family and printed, because a cap that
	// silently drops is a cap that lies.
	MaxBoxesDrawn int
	// ReportMetrics is SP_POC_REPORT_METRICS: `detectors` or `events`, exactly
	// one, with no default. A report whose metric set was inferred would be
	// comparable with a report that chose, and the whole point of the split is
	// that they are different measurements.
	ReportMetrics string
	ZonesPath     string
	Cooldown      time.Duration

	// approved guards the resolved class set. See ApprovedClasses.
	//
	// It is a POINTER to the cache and the cache is behind it, because Config is
	// passed by value to every reader in this module — `watch` alone hands it to
	// seven of them — and a value holding a sync.Once makes each of those a
	// `copylocks` finding under `go vet`. The gate runs vet, so the choice was
	// between the two: a cache that survives being copied, or a Config nobody can
	// pass. The cache is behind a pointer precisely so the copy carries the cache
	// with it instead of copying the lock.
	approvedOnce  *sync.Once
	approvedSet   map[string]bool
	approvedErr   error
	TrackMaxAge   time.Duration
	TrackIoU      float64
	LineTolerance float64

	ClipBefore     time.Duration
	ClipAfter      time.Duration
	MaxDisk        int64
	RSSLimit       int64
	DBLimit        int64
	CPULimit       float64
	DiskHeadroom   float64
	DiskBudgetDays int

	HeartbeatInterval time.Duration
	GapThreshold      time.Duration
	SoakTarget        time.Duration
	SegmentSeconds    int
	ClockSource       string
	SimURL            string

	XProtectBaseURL      string
	XProtectClientID     string
	XProtectClientSecret string
	XProtectUsername     string
	XProtectPassword     string
	CameraGUID           string
	DevicePath           string
	EventTypeGUID        string
	PushMaxAttempts      int
	PushTimeout          time.Duration
	PushGrace            time.Duration

	UIAddr string
	Label  string
}

// SourceKind constants. They are spelling, not behaviour.
const (
	SourceLoop = "loop"
	SourceRTSP = "rtsp"
	SourceURL  = "url"
)

// Mode constants.
const (
	ModeLive       = "live"
	ModeDemoReplay = "demo-replay"
)

// Lookup is the environment accessor, injected so the tests can describe a
// world in which one variable is missing.
type Lookup func(string) (string, bool)

// Load reads every variable exactly once. All missing variables are reported
// together, so an operator fixes the environment in one pass instead of
// one-per-run.
func Load(lookup Lookup) (Config, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	var (
		cfg  Config
		errs []error
		seen = make(map[string]bool, len(schemaByName))
	)

	get := func(name string) (string, bool) {
		f, ok := schemaByName[name]
		if !ok {
			panic("config: " + name + " is not in the schema")
		}
		seen[name] = true
		raw, present := lookup(name)
		if !present || strings.TrimSpace(raw) == "" {
			if f.structural {
				raw = f.structuralVal
			} else if f.when != "" && !conditionHolds(lookup, f.when) {
				// W1-4: the variable is genuinely not required by this run, and
				// the absence is recorded as satisfied rather than as an error. It
				// is marked `seen` above, so the dead-variable check still fires
				// on one nobody reads.
				return "", false
			} else {
				errs = append(errs, &MissingError{Var: name, Consequence: f.Consequence, Remedy: f.Remedy})
				return "", false
			}
		}
		return raw, true
	}

	str := func(dst *string, name string) {
		raw, ok := get(name)
		if !ok {
			return
		}
		*dst = strings.TrimSpace(raw)
	}
	num := func(dst *int, name string) {
		raw, ok := get(name)
		if !ok {
			return
		}
		v, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			f := schemaByName[name]
			errs = append(errs, &MissingError{Var: name, Raw: raw, ParseErr: err, Consequence: f.Consequence, Remedy: f.Remedy})
			return
		}
		*dst = v
	}
	flt := func(dst *float64, name string) {
		raw, ok := get(name)
		if !ok {
			return
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			f := schemaByName[name]
			errs = append(errs, &MissingError{Var: name, Raw: raw, ParseErr: err, Consequence: f.Consequence, Remedy: f.Remedy})
			return
		}
		*dst = v
	}
	// boolean is a plain bool parse with the same refusal as every other reader.
	// Only SP_POC_SOURCE_RELAY uses it, and it uses it because a relay declared as
	// a free-text string would have three spellings and two of them would mean
	// nothing to a reader of `watch --config`.
	boolean := func(dst *bool, name string) {
		raw, ok := get(name)
		if !ok {
			return
		}
		v, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			f := schemaByName[name]
			errs = append(errs, &MissingError{Var: name, Raw: raw, ParseErr: err, Consequence: f.Consequence, Remedy: f.Remedy})
			return
		}
		*dst = v
	}
	// integer is a plain decimal parse. It is a named helper rather than a call to
	// `strconv.Atoi` at the call site because there are now several of these and
	// they all have to produce the same refusal — one that names the variable and
	// prints the schema's consequence and remedy, which is what a reader of a
	// failed run actually reads.
	integer := func(dst *int, name string) {
		raw, ok := get(name)
		if !ok {
			return
		}
		v, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			f := schemaByName[name]
			errs = append(errs, &MissingError{Var: name, Raw: raw, ParseErr: err, Consequence: f.Consequence, Remedy: f.Remedy})
			return
		}
		*dst = v
	}
	i64 := func(dst *int64, name string) {
		raw, ok := get(name)
		if !ok {
			return
		}
		v, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			f := schemaByName[name]
			errs = append(errs, &MissingError{Var: name, Raw: raw, ParseErr: err, Consequence: f.Consequence, Remedy: f.Remedy})
			return
		}
		*dst = v
	}
	dur := func(dst *time.Duration, name string) {
		raw, ok := get(name)
		if !ok {
			return
		}
		v, err := time.ParseDuration(strings.TrimSpace(raw))
		if err != nil {
			f := schemaByName[name]
			errs = append(errs, &MissingError{Var: name, Raw: raw, ParseErr: err, Consequence: f.Consequence, Remedy: f.Remedy})
			return
		}
		if v <= 0 {
			f := schemaByName[name]
			errs = append(errs, &MissingError{Var: name, Raw: raw, ParseErr: fmt.Errorf("must be positive, got %s", v), Consequence: f.Consequence, Remedy: f.Remedy})
			return
		}
		*dst = v
	}

	str(&cfg.DataRoot, "SP_POC_DATA_ROOT")
	str(&cfg.Mode, "SP_POC_MODE")
	str(&cfg.SourceKind, "SP_POC_SOURCE_KIND")
	str(&cfg.SourceArg, "SP_POC_SOURCE_ARG")
	str(&cfg.SourceLicence, "SP_POC_SOURCE_LICENCE")
	str(&cfg.SourceUser, "SP_POC_SOURCE_USER")
	str(&cfg.SourcePassword, "SP_POC_SOURCE_PASSWORD")
	boolean(&cfg.SourceRelay, "SP_POC_SOURCE_RELAY")
	str(&cfg.AIAddr, "SP_POC_AI_ADDR")
	str(&cfg.AIModel, "SP_POC_AI_MODEL")
	str(&cfg.SidecarScript, "SP_POC_SIDECAR_SCRIPT")
	str(&cfg.TriggerMode, "SP_POC_TRIGGER_MODE")
	str(&cfg.VLMProvider, "SP_POC_VLM_PROVIDER")
	str(&cfg.VLMRegion, "SP_POC_VLM_REGION")
	str(&cfg.VLMAPIKey, "SP_POC_VLM_API_KEY")
	str(&cfg.VLMModel, "SP_POC_VLM_MODEL")
	num(&cfg.VLMDailyCap, "SP_POC_VLM_DAILY_CAP")
	flt(&cfg.NoveltyThreshold, "SP_POC_NOVELTY_THRESHOLD")
	flt(&cfg.NoveltyRelease, "SP_POC_NOVELTY_RELEASE")
	num(&cfg.NoveltySustain, "SP_POC_NOVELTY_SUSTAIN")
	num(&cfg.NoveltyGrid, "SP_POC_NOVELTY_GRID")
	num(&cfg.AIFPS, "SP_POC_AI_FPS")
	dur(&cfg.AIDeadline, "SP_POC_AI_DEADLINE_MS")
	dur(&cfg.AIBudget, "SP_POC_AI_BUDGET_MS")
	dur(&cfg.AIBackpressure, "SP_POC_AI_BACKPRESSURE_MS")
	num(&cfg.AIInputWidth, "SP_POC_AI_INPUT_WIDTH")
	flt(&cfg.ScoreThreshold, "SP_POC_SCORE_THRESHOLD")
	flt(&cfg.MinBoxArea, "SP_POC_MIN_BOX_AREA")
	flt(&cfg.GhostMinScore, "SP_POC_GHOST_MIN_SCORE")
	integer(&cfg.TrackMinFrames, "SP_POC_TRACK_MIN_FRAMES")
	integer(&cfg.MaxBoxesDrawn, "SP_POC_MAX_BOXES_DRAWN")
	str(&cfg.AIAnchors, "SP_POC_AI_ANCHORS")
	str(&cfg.ReportMetrics, "SP_POC_REPORT_METRICS")
	str(&cfg.ZonesPath, "SP_POC_ZONES")
	dur(&cfg.Cooldown, "SP_POC_COOLDOWN_MS")
	dur(&cfg.TrackMaxAge, "SP_POC_TRACK_MAX_AGE_MS")
	flt(&cfg.TrackIoU, "SP_POC_TRACK_IOU")
	flt(&cfg.LineTolerance, "SP_POC_LINE_TOLERANCE")
	dur(&cfg.ClipBefore, "SP_POC_CLIP_BEFORE_MS")
	dur(&cfg.ClipAfter, "SP_POC_CLIP_AFTER_MS")
	i64(&cfg.MaxDisk, "SP_POC_MAX_DISK_BYTES")
	i64(&cfg.RSSLimit, "SP_POC_RSS_LIMIT_BYTES")
	i64(&cfg.DBLimit, "SP_POC_DB_LIMIT_BYTES")
	flt(&cfg.CPULimit, "SP_POC_CPU_LIMIT_PERCENT")
	flt(&cfg.DiskHeadroom, "SP_POC_DISK_HEADROOM")
	num(&cfg.DiskBudgetDays, "SP_POC_DISK_BUDGET_DAYS")
	dur(&cfg.HeartbeatInterval, "SP_POC_HEARTBEAT_INTERVAL")
	dur(&cfg.GapThreshold, "SP_POC_GAP_THRESHOLD")
	dur(&cfg.SoakTarget, "SP_POC_SOAK_TARGET")
	num(&cfg.SegmentSeconds, "SP_POC_SEGMENT_SECONDS")
	str(&cfg.ClockSource, "SP_POC_CLOCK_SOURCE")
	str(&cfg.SimURL, "SP_POC_SIM_URL")
	str(&cfg.XProtectBaseURL, "SP_POC_XPROTECT_BASE_URL")
	str(&cfg.XProtectClientID, "SP_POC_XPROTECT_CLIENT_ID")
	str(&cfg.XProtectClientSecret, "SP_POC_XPROTECT_CLIENT_SECRET")
	str(&cfg.XProtectUsername, "SP_POC_XPROTECT_USERNAME")
	str(&cfg.XProtectPassword, "SP_POC_XPROTECT_PASSWORD")
	str(&cfg.CameraGUID, "SP_POC_CAMERA_GUID")
	str(&cfg.DevicePath, "SP_POC_DEVICE_PATH")
	str(&cfg.EventTypeGUID, "SP_POC_EVENT_TYPE_GUID")
	num(&cfg.PushMaxAttempts, "SP_POC_PUSH_MAX_ATTEMPTS")
	dur(&cfg.PushTimeout, "SP_POC_PUSH_TIMEOUT")
	dur(&cfg.PushGrace, "SP_POC_PUSH_GRACE")
	str(&cfg.UIAddr, "SP_POC_UI_ADDR")
	str(&cfg.Label, "SP_POC_LABEL")

	if raw, ok := get("SP_POC_CLASSES"); ok {
		for _, c := range strings.Split(raw, ",") {
			if c = strings.TrimSpace(c); c != "" {
				cfg.Classes = append(cfg.Classes, c)
			}
		}
	}

	// A variable in the schema that nobody read is a dead variable: it is
	// documented as required and would then never be required.
	for name := range schemaByName {
		if !seen[name] {
			panic("config: " + name + " is in the schema but never read")
		}
	}

	if len(errs) > 0 {
		return cfg, &AggregateError{Errors: errs}
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// conditionHolds reports whether a `<VAR>=<value>` condition is satisfied by the
// environment.
//
// It panics rather than returning false for an unparseable condition, for the same
// reason `get` panics for an unknown variable: a typo in a condition must be a
// startup failure, not a requirement that quietly stopped applying. A condition
// that silently never holds would make every field behind it unconditionally
// required again — which is the exact defect W1-4 closed.
func conditionHolds(lookup Lookup, cond string) bool {
	name, want, ok := strings.Cut(cond, "=")
	if !ok {
		panic("config: condition " + cond + " is not VAR=value")
	}
	name = strings.TrimSpace(name)
	f, known := schemaByName[name]
	if !known {
		panic("config: condition " + cond + " names a variable that is not in the schema")
	}
	raw, present := lookup(name)
	if !present || strings.TrimSpace(raw) == "" {
		if !f.structural {
			return false
		}
		raw = f.structuralVal
	}
	return strings.TrimSpace(raw) == strings.TrimSpace(want)
}

// LoadEnv is Load against the process environment.
func LoadEnv() (Config, error) { return Load(os.LookupEnv) }

// AggregateError carries every problem found in one Load, so an operator does
// not discover them one run at a time.
type AggregateError struct {
	Errors []error
}

func (a *AggregateError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d configuration value(s) are unusable:", len(a.Errors))
	for _, e := range a.Errors {
		b.WriteString("\n  - ")
		b.WriteString(e.Error())
	}
	return b.String()
}

func (a *AggregateError) Unwrap() []error { return a.Errors }

// Names returns the variables named by the error, sorted, for tests and for the
// preflight table.
func (a *AggregateError) Names() []string {
	var out []string
	for _, e := range a.Errors {
		var me *MissingError
		if asMissing(e, &me) {
			out = append(out, me.Var)
		}
	}
	sort.Strings(out)
	return out
}

func asMissing(err error, dst **MissingError) bool {
	me, ok := err.(*MissingError)
	if ok {
		*dst = me
	}
	return ok
}

// MissingNames returns the variable names Load refused to start without.
func MissingNames(err error) []string {
	var agg *AggregateError
	if e, ok := err.(*AggregateError); ok {
		agg = e
	}
	if agg == nil {
		if me, ok := err.(*MissingError); ok {
			return []string{me.Var}
		}
		return nil
	}
	return agg.Names()
}

// Validate checks the combinations that are wrong as a set rather than as
// individual values. These are refusals by name and consequence too, because
// they are the ones that are hardest to guess.
func (c Config) Validate() error {
	var errs []error
	add := func(f string, format string, args ...any) {
		errs = append(errs, fmt.Errorf(f+": "+format, args...))
	}

	switch c.SourceKind {
	case SourceLoop, SourceRTSP, SourceURL:
	default:
		add("SP_POC_SOURCE_KIND", "%q is not one of loop, rtsp, url; ffmpeg would be given an input we do not understand, and the failure would surface as a hang in preflight", c.SourceKind)
	}

	// --- the trigger and the verifier (W1-4, W1-7) ---
	//
	// Validated before anything else about the run, because a misconfiguration here
	// is the one that produces a report claiming a model verified something it did
	// not.
	switch strings.ToLower(strings.TrimSpace(c.TriggerMode)) {
	case "novelty", "sidecar":
	default:
		add("SP_POC_TRIGGER_MODE", "%q is not one of novelty, sidecar; the run would not be able to say where its boxes came from, "+
			"and the cost argument has no subject", c.TriggerMode)
	}
	switch strings.ToLower(strings.TrimSpace(c.VLMProvider)) {
	case "none":
		// Not a failure. The novelty trigger measures and proposes on its own, and
		// an unverified proposal is a real thing an operator can want.
	case "hafnia":
		switch strings.ToLower(strings.TrimSpace(c.VLMRegion)) {
		case "eu", "us":
		default:
			add("SP_POC_VLM_REGION", "%q is not eu or us; footage has no declared destination, and there is deliberately NO failover "+
				"between regions — during an outage a silent failover is a sovereignty breach", c.VLMRegion)
		}
	default:
		add("SP_POC_VLM_PROVIDER", "%q is not one of none, hafnia; the report cannot say who verified the events", c.VLMProvider)
	}
	if c.NoveltyThreshold <= 0 || c.NoveltyThreshold > 1 {
		add("SP_POC_NOVELTY_THRESHOLD", "%g is not in (0,1]; it is the FRACTION of the frame that must change, and a bar of zero "+
			"is a detector that fires on every frame", c.NoveltyThreshold)
	}
	if c.NoveltyRelease < 0 || c.NoveltyRelease > c.NoveltyThreshold {
		add("SP_POC_NOVELTY_RELEASE", "%g is not in [0, threshold]; a release above the trigger is an upside-down hysteresis band "+
			"and the trigger can never reset", c.NoveltyRelease)
	}
	if c.NoveltySustain < 1 {
		add("SP_POC_NOVELTY_SUSTAIN", "%d is not positive; one frame of difference would be an incident, and one frame is a car, "+
			"a shadow or a compression artefact", c.NoveltySustain)
	}
	if c.NoveltyGrid < 8 {
		add("SP_POC_NOVELTY_GRID", "%d is too small to locate anything, and the score would not be comparable between runs", c.NoveltyGrid)
	}
	if c.VLMDailyCap < 0 {
		add("SP_POC_VLM_DAILY_CAP", "%d is negative; the site's spend against the contract is unbounded by anything local, and the "+
			"cost argument depends on a local cap existing", c.VLMDailyCap)
	}
	if c.SourceKind == SourceLoop && (strings.HasPrefix(c.SourceArg, "rtsp://") || strings.HasPrefix(c.SourceArg, "http://") || strings.HasPrefix(c.SourceArg, "https://")) {
		add("SP_POC_SOURCE_ARG", "kind=loop was given a URL; a loop run is a measured run over recorded material and a live URL would be presented as one")
	}
	if c.SourceKind == SourceRTSP && !strings.HasPrefix(c.SourceArg, "rtsp://") && !strings.HasPrefix(c.SourceArg, "rtsps://") {
		add("SP_POC_SOURCE_ARG", "kind=rtsp was given %q, which is not an rtsp URL; the report would claim the rtsp path was exercised when it was not", c.SourceArg)
	}
	if c.SourceKind == SourceURL && !strings.HasPrefix(c.SourceArg, "http://") && !strings.HasPrefix(c.SourceArg, "https://") {
		add("SP_POC_SOURCE_ARG", "kind=url was given %q, which is not an http(s) URL", c.SourceArg)
	}

	// A URL with credentials inline is refused HERE as well as in
	// source.Parse.
	//
	// The check is duplicated on purpose. This is the layer the operator meets:
	// it is the error `watch` and `pocctl preflight` print, and it is the first
	// thing to run. A security check that exists in only one of two layers is
	// one refactor away from existing in none, and the string it protects is
	// stored in the run row and printed by `watch --config`.
	if strings.HasPrefix(strings.ToLower(c.SourceArg), "rtsp://") || strings.HasPrefix(strings.ToLower(c.SourceArg), "http") {
		rest := c.SourceArg[strings.Index(c.SourceArg, "://")+3:]
		if i := strings.IndexAny(rest, "/?#"); i >= 0 {
			rest = rest[:i]
		}
		if strings.Contains(rest, "@") {
			add("SP_POC_SOURCE_ARG",
				"the source URL carries credentials inline. That string is stored in the run row, printed by `watch --config`, echoed into logs, and visible in the process table.\n"+
					"  Put the camera's address in SP_POC_SOURCE_ARG and its credentials in SP_POC_SOURCE_USER and SP_POC_SOURCE_PASSWORD. ffmpeg is given them as an Authorization header, never as part of the URL.")
		}
	}

	// The camera-credential rules. These two are what make it safe for both
	// credentials to be OPTIONAL, which they have to be: a fully public camera
	// is open, and requiring a password would refuse the exact case this project
	// is about.
	if c.SourceKind == SourceLoop && (c.SourceUser != "" || c.SourcePassword != "") {
		add("SP_POC_SOURCE_USER",
			"kind=loop was given camera credentials. A loop plays recorded material and there is nothing to authenticate, so the password is carried for no reason and the run reads as though a camera were connected when none is")
	}
	if c.SourcePassword != "" && c.SourceUser == "" {
		add("SP_POC_SOURCE_USER",
			"a camera password was given with no user. ffmpeg would be sent a `Basic OnBhc3N3b3Jk:` header and the camera would answer 401 forever, with the reason in a line of ffmpeg's log that nobody reading the report will ever see")
	}

	// A relay is a live source by construction, so the two settings cannot be
	// combined the other way round. This is a refusal and not a normalisation
	// because the combination has no meaning to state: there is nothing to relay
	// in a file the run already has.
	if c.SourceRelay && !c.isLiveSource() {
		add("SP_POC_SOURCE_RELAY",
			"the run says it is relaying recorded material, and the source is %q, which reads a local file or nothing at all. A relay is a LIVE source by construction — a recording re-published over rtsp — so this pair of settings describes a thing that does not exist. Set SP_POC_SOURCE_KIND=rtsp, or set SP_POC_SOURCE_RELAY=false", c.SourceKind)
	}

	// A LIVE source needs a recorded permission basis, and nothing else can supply
	// one.
	//
	// This row exists because the tool would otherwise take a camera URL with no
	// question asked, and because a public feed being PUBLIC is not the same as
	// being free to record. The site that aggregates 155,000 cameras is not the
	// operator of any of them, and one of them refused automated access outright.
	//
	// It is required for live sources and optional for a loop, because the
	// committed material is synthetic and there is nobody to ask.
	if c.isLiveSource() && strings.TrimSpace(c.SourceLicence) == "" {
		add("SP_POC_SOURCE_LICENCE",
			"this is a LIVE source, so the run would record real footage of people in public space with no recorded basis for using it.\n"+
				"  Watching a feed live and storing clips of it are different acts, and the second is the camera OPERATOR's to permit — not the directory that lists the camera, and not the fact that the feed is public.\n"+
				"  Set SP_POC_SOURCE_LICENCE to the basis: a camera you own, the operator's published open-data terms, or a written permission naming the operator and how long the footage may be kept.\n"+
				"  If you are deliberately deciding to proceed without one, that decision belongs in the run's own record, which is what this variable is for.")
	}

	switch c.Mode {
	case ModeLive, ModeDemoReplay:
	default:
		add("SP_POC_MODE", "%q is not live or demo-replay; the screen header is mandatory and there is no third answer", c.Mode)
	}
	if c.Mode == ModeDemoReplay && c.SourceKind != SourceLoop {
		add("SP_POC_SOURCE_KIND", "mode=demo-replay with a non-loop source is a contradiction: a demo replay must play labelled recorded material")
	}

	if c.AIFPS <= 0 {
		add("SP_POC_AI_FPS", "%d frames per second is not a rate", c.AIFPS)
	}
	if c.AIDeadline <= 0 {
		add("SP_POC_AI_DEADLINE_MS", "a non-positive deadline makes every answer late")
	}
	if len(c.Classes) == 0 {
		add("SP_POC_CLASSES", "an empty class list means no box is approved, and the run would be a measurement of nothing")
	}
	// The class list is RESOLVED, not merely present. An entry that is neither a
	// family nor a class is a refusal here rather than at the first frame,
	// because a run that starts and then approves nothing produces zero events
	// and no error at all.
	if _, err := c.ApprovedClasses(); err != nil {
		add("SP_POC_CLASSES", err.Error())
	}
	if c.MinBoxArea <= 0 || c.MinBoxArea > 0.5 {
		add("SP_POC_MIN_BOX_AREA", "%v is not a fraction of the frame between 0 and 0.5; a larger one would reject every object a camera can see", c.MinBoxArea)
	}
	if c.GhostMinScore < 0 || c.GhostMinScore > 1 {
		add("SP_POC_GHOST_MIN_SCORE", "%v is not a probability", c.GhostMinScore)
	}
	// The ghost band is an ORDERING, and a band with the wrong order is not a
	// band: a ghost floor ABOVE the approval threshold means everything between
	// them is drawn dimmed AND approved, which is a contradiction the picture
	// cannot express and the reader cannot see.
	if c.GhostMinScore > c.ScoreThreshold {
		add("SP_POC_GHOST_MIN_SCORE", "%v is above SP_POC_SCORE_THRESHOLD (%v), so the rejected band is upside down: everything between them would be drawn dimmed and approved at once. The ghost floor must be at or below the approval threshold", c.GhostMinScore, c.ScoreThreshold)
	}
	if c.TrackMinFrames < 1 {
		add("SP_POC_TRACK_MIN_FRAMES", "%v frames; at least one is required, because a track that has been seen zero times is not an object", c.TrackMinFrames)
	}
	if c.MaxBoxesDrawn < 1 {
		add("SP_POC_MAX_BOXES_DRAWN", "%v; a cap of zero draws nothing and a picture with nothing on it is indistinguishable from a camera pointing at a wall", c.MaxBoxesDrawn)
	}
	// EXACTLY ONE, and no default. The split between classifier-detection metrics
	// and pipeline-event metrics is the point of the report, and a run that
	// quietly picked one would produce a number that reads like the other.
	switch c.ReportMetrics {
	case "detectors", "events":
	default:
		add("SP_POC_REPORT_METRICS", "%q is neither `detectors` nor `events`. Choose one and say which: the classifier's detection quality and the pipeline's event quality are different measurements, and a report written against both at once is comparable with neither", c.ReportMetrics)
	}
	if c.AIAnchors != "" {
		if strings.EqualFold(strings.TrimSpace(c.AIAnchors), NoAnchorsSentinel) {
			// An EXPLICIT declaration, not an absence. This module's convention is
			// that a variable set to whitespace is not set, and that convention is
			// right: an operator who typed nothing has said nothing. So a fused
			// export — which genuinely needs no anchors — has to SAY so, and the
			// word it says it with is a checked value rather than an empty string
			// that a reader cannot tell from a forgotten line.
		} else if _, err := os.Stat(c.AIAnchors); err != nil {
			add("SP_POC_AI_ANCHORS", "the anchors file %s could not be read: %v. The anchors are part of the model's .cfg and are not in the ONNX file, so there is nothing to infer them from and the sidecar will not guess them. Set it to %q if this model is a FUSED export, which needs no anchors", c.AIAnchors, err, NoAnchorsSentinel)
		}
	}
	if c.ScoreThreshold < 0 || c.ScoreThreshold > 1 {
		add("SP_POC_SCORE_THRESHOLD", "%v is not a probability", c.ScoreThreshold)
	}
	if c.TrackIoU <= 0 || c.TrackIoU > 1 {
		add("SP_POC_TRACK_IOU", "%v is not an IoU", c.TrackIoU)
	}
	if c.LineTolerance < 0 || c.LineTolerance > 1 {
		add("SP_POC_LINE_TOLERANCE", "%v is not a normalised tolerance", c.LineTolerance)
	}
	if c.MaxDisk <= 0 {
		add("SP_POC_MAX_DISK_BYTES", "%d is not a disk budget", c.MaxDisk)
	}
	if c.DiskHeadroom < 1 {
		add("SP_POC_DISK_HEADROOM", "%v leaves no headroom over the measured budget; 1.0 means the run starts already over budget", c.DiskHeadroom)
	}
	if c.SegmentSeconds <= 0 {
		add("SP_POC_SEGMENT_SECONDS", "%d is not a segment length", c.SegmentSeconds)
	}
	if c.GapThreshold <= c.HeartbeatInterval {
		add("SP_POC_GAP_THRESHOLD", "the gap threshold (%s) is not longer than the heartbeat interval (%s), so every heartbeat looks like a dropout", c.GapThreshold, c.HeartbeatInterval)
	}
	switch c.ClockSource {
	case "ntp", "host":
	default:
		add("SP_POC_CLOCK_SOURCE", "%q is not ntp or host; the report must state a clock source and there is no third answer", c.ClockSource)
	}
	if err := validateLoopback(c.UIAddr); err != nil {
		errs = append(errs, err)
	}
	if err := validateBaseURL(c.XProtectBaseURL); err != nil {
		errs = append(errs, err)
	}
	if err := validateBaseURL(c.SimURL); err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return &AggregateError{Errors: errs}
	}
	return nil
}

// validateLoopback refuses a non-loopback UI address. The screen carries
// detections and evidence hashes; it is not an authenticated service and must
// not be reachable from the network.
func validateLoopback(addr string) error {
	host, _, err := splitHostPort(addr)
	if err != nil {
		return fmt.Errorf("SP_POC_UI_ADDR: %q is not host:port; the screen has no address and every number on it would have to be read from the log", addr)
	}
	switch host {
	case "127.0.0.1", "::1", "localhost":
		return nil
	}
	return fmt.Errorf("SP_POC_UI_ADDR: %q is not loopback; the screen is read-only but unauthenticated, and binding it to a routable address would expose detections and evidence hashes to the network", addr)
}

// validateBaseURL refuses plain http except for loopback, so a token and a
// bearer-authenticated push cannot leave the machine in clear text.
func validateBaseURL(raw string) error {
	switch {
	case strings.HasPrefix(raw, "https://"):
		return nil
	case strings.HasPrefix(raw, "http://127.0.0.1"), strings.HasPrefix(raw, "http://localhost"), strings.HasPrefix(raw, "http://[::1]"):
		return nil
	case strings.HasPrefix(raw, "http://"):
		return fmt.Errorf("SP_POC_XPROTECT_BASE_URL: %q is plain http to a non-loopback host; the OAuth token and the bearer-authenticated push would cross the network in clear text — use https", raw)
	default:
		return fmt.Errorf("SP_POC_XPROTECT_BASE_URL: %q must be an http or https URL; a base URL we do not understand would fail as an opaque connection error", raw)
	}
}

func splitHostPort(addr string) (string, string, error) {
	i := strings.LastIndex(addr, ":")
	if i <= 0 || i == len(addr)-1 {
		return "", "", fmt.Errorf("missing port")
	}
	return strings.Trim(addr[:i], "[]"), addr[i+1:], nil
}

// Secrets returns the variable names whose values must never reach a log line,
// a database row or an outbox payload. The tests use it, and so does the
// redacting logger.
func Secrets() []string {
	// Three, and the third was added when a live camera arrived: the camera's
	// password. A camera password left in a process table, a run row, a log line
	// or a screenshot is a public camera anybody on the network can watch.
	out := []string{
		"SP_POC_XPROTECT_CLIENT_SECRET",
		"SP_POC_XPROTECT_PASSWORD",
		"SP_POC_SOURCE_PASSWORD",
	}
	sort.Strings(out)
	return out
}

// IsLiveVMS reports whether the push target is a real XProtect rather than the
// simulator. It is the only thing that may license a report to say the event
// reached a VMS, and it requires an explicit non-simulator base URL.
func (c Config) IsLiveVMS() bool {
	return !IsSimulatorURL(c.XProtectBaseURL)
}

// ApprovedClasses is the resolved, lower-cased set of classes this run will turn
// into a crossing event, with families expanded to their members.
//
// It is resolved ONCE and cached on the Config, because the answer has to be the
// same on every frame: a run whose approved set could change mid-run would emit
// an event for a class it had already decided against. `sync.Once` rather than a
// field set in `Load`, because `Config` is a value and is copied around, and a
// cache field set in a copy is a cache that is set in a copy of the copy.
//
// The resolution is a REFUSAL for an entry that is neither a family nor a class,
// by name, and the reason is spelled out in `Resolve`'s error. A typo in the
// approved list that silently approved nothing would produce a run with zero
// events and no error, and that is the most expensive silence in this module.
func (c Config) ApprovedClasses() (map[string]bool, error) {
	// A Config built by hand rather than by Load has no cache. Building one here
	// gives it the shared cache and costs a copy of a pointer; NOT building one
	// would make the set resolve on every call, which is the thing the cache
	// exists to prevent.
	once := c.approvedOnce
	if once == nil {
		once = &sync.Once{}
	}
	once.Do(func() {
		resolved, err := classify.Resolve(c.Classes)
		if err != nil {
			c.approvedErr = err
			return
		}
		set := make(map[string]bool, len(resolved))
		for _, k := range resolved {
			set[k] = true
		}
		c.approvedSet, c.approvedErr = set, nil
	})
	if c.approvedErr != nil {
		return nil, c.approvedErr
	}
	out := make(map[string]bool, len(c.approvedSet))
	for k, v := range c.approvedSet {
		out[k] = v
	}
	return out, nil
}

// IsApproved reports whether a class may produce a crossing event.
func (c Config) IsApproved(class string) bool {
	set, err := c.ApprovedClasses()
	if err != nil {
		// An unresolvable class list approves NOTHING, and Load refuses such a
		// configuration before a run starts. Reaching here means something built
		// a Config without loading one; approving everything would be the
		// dangerous answer, so this refuses.
		return false
	}
	return set[strings.ToLower(strings.TrimSpace(class))]
}

// isLiveSource reports whether the source is real-time. It is the boundary at
// which a permission basis becomes required: recorded material is the loop's own,
// a live camera belongs to somebody.
func (c Config) isLiveSource() bool {
	return c.SourceKind == SourceRTSP || c.SourceKind == SourceURL
}

// IsLiveSource is the exported form, for callers outside this package.
func (c Config) IsLiveSource() bool { return c.isLiveSource() }

// IsSimulatorURL reports whether a base URL is loopback, i.e. the simulator.
func IsSimulatorURL(base string) bool {
	return strings.HasPrefix(base, "http://127.0.0.1") ||
		strings.HasPrefix(base, "http://localhost") ||
		strings.HasPrefix(base, "http://[::1]")
}
