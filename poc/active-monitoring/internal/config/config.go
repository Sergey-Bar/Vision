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

	"github.com/Sergey-Bar/vision-poc/internal/classify"
	"github.com/Sergey-Bar/vision-poc/internal/novelty"
	"github.com/Sergey-Bar/vision-poc/internal/ortdetect"
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
	// The public relay: a real camera, streamed by somebody else. This is the
	// YouTube live cam case, and it is a THIRD state rather than a refinement of
	// the second because both refinements are lies: `Relay`'s sentence ("relay
	// of recorded material, NOT a camera") denies a camera that is demonstrably
	// there, and the plain live sentence ("live http stream") claims nothing
	// false but says nothing about whose camera it is. The derived sentence is
	// "public relay of a real camera, NOT ours", which is the only one of the
	// three that is true.
	//
	// Required rather than defaulted, for SP_POC_SOURCE_RELAY's reason: the
	// default is the dangerous one. An operator reading a public cam who leaves
	// this unset gets a source line that does not say the camera is somebody
	// else's, and the whole point of this module is not making a claim it cannot
	// back - in either direction.
	{Name: "SP_POC_SOURCE_PUBLIC", kind: kindBool,
		Consequence: "the source line does not say the camera belongs to somebody else, so a run over a public relay reads as though this installation owns the hardware it is measuring",
		Remedy:      "set SP_POC_SOURCE_PUBLIC=true for a public relay of a real camera (e.g. a YouTube live cam resolved by `pocctl live`), or false for a camera this site operates"},

	// --- AI sidecar ---
	//
	// Every one of these is conditional on `SP_POC_TRIGGER_MODE != sidecar`. W1-4
	// retired the sidecar from the trigger path: the shipped pipeline measures a
	// region with pure-Go arithmetic and asks a hosted model once per event, so it
	// needs no ONNX model, no Python worker and no anchors. They are kept, because
	// `trigger-mode=sidecar` is still a real mode and a useful one — it is how a
	// threshold gets chosen — and a tool with one path is a tool with one opinion.
	{Name: "SP_POC_TRIGGER_MODE", kind: kindString, structural: true, structuralVal: "detector",
		Consequence: "the run would not say where its boxes come from, and the cost argument (one call per event against one per frame) has no subject",
		Remedy:      "set SP_POC_TRIGGER_MODE=detector (the shipped in-process ONNX detector, which needs weights and an ONNX Runtime library), SP_POC_TRIGGER_MODE=novelty (pure-Go whole-frame change measurement: licence-free, no weights, no class vocabulary) or SP_POC_TRIGGER_MODE=sidecar (the older per-frame Python classifier, kept as a threshold-selection and parity tool)"},
	{Name: "SP_POC_AI_ADDR", kind: kindString, when: "SP_POC_TRIGGER_MODE=sidecar",
		Consequence: "there is no sidecar to send frames to, and every detection would be not_asked",
		Remedy:      "set SP_POC_AI_ADDR=127.0.0.1:8099"},
	{Name: "SP_POC_AI_MODEL", kind: kindString, when: "SP_POC_TRIGGER_MODE=sidecar",
		Consequence: "preflight cannot load the model, and a run that starts without it dies in minute one",
		Remedy:      "set SP_POC_AI_MODEL to the path of an ONNX file, e.g. models\\yolov4n.onnx"},
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

	// --- the second trigger: which detector is beside the alarm one ---
	//
	// It was read by `os.Getenv` in cmd/watch and validated there, which is the
	// defect this table exists to prevent: a variable that changes behaviour and
	// is in no schema is not required by name, is not printed by `watch --config`,
	// and is covered by no test. It is REQUIRED and not defaulted, on the same
	// reasoning as SP_POC_SOURCE_RELAY: the default here is the dangerous one, and
	// a whole-view trigger pins a reference view on disk for every later run, so
	// "did the operator mean to carry it" has to be a thing they said.
	{Name: "SP_POC_CAMERA_INTEGRITY", kind: kindBool,
		Consequence: "the run does not say whether it carries the camera_integrity trigger beside the alarm one, so a reference-mode run that pins a view on disk and one that does not are indistinguishable afterwards",
		Remedy:      "set SP_POC_CAMERA_INTEGRITY=true to carry the camera_integrity trigger, which pins a reference view on disk; or false for the alarm trigger alone"},

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
		Remedy:      "set SP_POC_AI_ANCHORS=models\\yolov4n.anchors.txt for an anchored export, or SP_POC_AI_ANCHORS=none to STATE that this model is a fused export and needs none. 'none' rather than an empty value, because a variable set to nothing has said nothing and this module treats it as unset"},
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
	// STRUCTURAL, and it is the one place a variable is optional because the primary
	// configuration is its ABSENCE. An empty zones value means PRESENCE MODE: every
	// tracked object that holds still long enough is reported, with no line drawn and
	// nothing to configure.
	//
	// The refusal that used to sit here said "there is no line to cross, so the
	// line-cross machine can never reach done and every detection is lost". That was
	// written when crossing was the only mode, and it has the shape of every other
	// refusal in this table: a stated consequence and a stated remedy. What changed is
	// that the consequence is no longer true. `detect.Engine` grew a presence path, and
	// a line-less zone is now a real zone with a real answer.
	//
	// This is NOT a silent default. "The operator configured nothing and got a different
	// detector than last week" is the failure this whole table exists to prevent. The
	// difference is that the value is NAMED, the absence is the primary intended
	// configuration rather than a fallback, and the mode the run is actually in is
	// printed on the report, the screen and the run row. `watch` logs which one it is
	// on at startup, in a line, by name.
	{Name: "SP_POC_ZONES", kind: kindString, structural: true, structuralVal: "",
		Consequence: "the run is in PRESENCE MODE: there is no line, so every tracked object that is seen often enough is reported once, with its class and its measured box, and nothing has to be drawn or configured first. Set SP_POC_ZONES to a zone file to switch to line-crossing mode, where only objects that cross a named line are reported",
		Remedy:      "leave SP_POC_ZONES unset for presence mode, or set SP_POC_ZONES=testdata\\zones.json for line-crossing mode. Setting it back is the whole of the rollback: one variable, no state to unwind"},
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

	// --- in-process detector (SP_POC_TRIGGER_MODE=detector) ---
	//
	// Every entry below is gated on `when: SP_POC_TRIGGER_MODE=detector`, so a
	// motion run is not asked about an ONNX file it will never open. They are all
	// required rather than defaulted WITHIN the schema: the defaults below are
	// what `pocctl fetch-runtime` writes into the manifest and what the demo sets
	// explicitly, and a detector run that silently picked a model would be a run
	// whose precision number names a file nobody chose.
	{Name: "SP_POC_DET_MODEL", kind: kindString, when: "SP_POC_TRIGGER_MODE=detector",
		Consequence: "the run has no detector. `detector` mode without a model is a run that decodes frames, draws nothing, and reports a zero detection rate with no error — which is what the previous build did for two real runs",
		Remedy:      "set SP_POC_DET_MODEL=models\\yolov4n.onnx, and record its licence and SHA-256 with `pocctl fetch-runtime`"},
	{Name: "SP_POC_DET_INPUT", kind: kindInt, when: "SP_POC_TRIGGER_MODE=detector",
		Consequence: "the model has no input size to be letterboxed into. This is a model property and not a preference: a wrong value produces a session error or a silently wrong scale, and the second is the one that ships",
		Remedy:      "set SP_POC_DET_INPUT=640 — read it from the session, not from a filename; `pocctl fixtureprobe` prints the declared shape"},
	{Name: "SP_POC_DET_NMS_CLASS", kind: kindFloat, when: "SP_POC_TRIGGER_MODE=detector",
		Consequence: "there is no class-aware suppression pass, so one object emitted as both a car and a bus becomes two boxes, two tracks and two crossings",
		Remedy:      "set SP_POC_DET_NMS_CLASS=0.45 — the class-aware pass; the global pass below is separate and is not a substitute for it"},
	{Name: "SP_POC_DET_NMS_GLOBAL", kind: kindFloat, when: "SP_POC_TRIGGER_MODE=detector",
		Consequence: "there is no class-agnostic pass. Class-aware NMS structurally CANNOT collapse a car box and a truck box on the same object, because it never compares them; one object then becomes two boxes and two crossings",
		Remedy:      "set SP_POC_DET_NMS_GLOBAL=0.75 — higher than the class-aware threshold on purpose; it is the pass that catches the cross-class pair"},
	{Name: "SP_POC_DET_MAX_BOXES", kind: kindInt, when: "SP_POC_TRIGGER_MODE=detector",
		Consequence: "there is no ceiling on boxes per frame, so a frame of noise can produce thousands of tracks and the tracker does association work on all of them. The count of what the cap removed is reported, because a cap that silently drops is a cap that lies",
		Remedy:      "set SP_POC_DET_MAX_BOXES=100"},
	{Name: "SP_POC_DET_SCORE", kind: kindFloat, when: "SP_POC_TRIGGER_MODE=detector",
		Consequence: "there is no model emit floor, so every raw candidate above zero becomes a box and the ghost band below has nothing to sit in. The floor must be at or below SP_POC_GHOST_MIN_SCORE, and the validator refuses an inverted chain by name",
		Remedy:      "set SP_POC_DET_SCORE=0.05 — this is the model's emit floor, not the approval bar; that is SP_POC_SCORE_THRESHOLD"},
	{Name: "SP_POC_ORT_LIB", kind: kindString, when: "SP_POC_TRIGGER_MODE=detector",
		Consequence: "no path to the ONNX Runtime shared library, so the session cannot be created. It is named by path rather than discovered from PATH because two versions can be installed and only one of them is the one the SHA-256 was recorded against",
		Remedy:      "run `pocctl fetch-runtime`, which writes the path and the SHA-256 it verified into the manifest"},
	{Name: "SP_POC_ORT_THREADS", kind: kindInt, when: "SP_POC_TRIGGER_MODE=detector",
		Consequence: "the intra-op thread count is whatever ORT picks from the machine's core count, so the same run on a 4-core laptop and a 16-core server has different latency and the measured p99 describes neither of them",
		Remedy:      "set SP_POC_ORT_THREADS=4 — it is recorded with every measurement, because a latency number without its thread count does not describe a build"},
	{Name: "SP_POC_BOX_LOG_FRAMES", kind: kindInt,
		Consequence: "the per-frame box log is unbounded. SQLite here runs synchronous=FULL on one connection, so at 30 fps an unbounded log is the thing that makes AC1 fail on storage latency rather than on anything about detection",
		Remedy:      "set SP_POC_BOX_LOG_FRAMES=20000 — at the cap every crossing frame is kept and every Nth of the rest, and the run records box_log_sampled with both counts"},

	// --- the crop pass (SP_POC_TRIGGER_MODE=detector) ---
	//
	// Cheap perception proposes where to look; the expensive detector looks there at
	// full resolution. Without it a whole-frame pass shrinks 1920x1080 to 416x416, a
	// 4.6x downscale, and a bird 40 px across at 30 m arrives as EIGHT pixels, which is
	// below the floor at which any detector is reliable. No threshold fixes that and no
	// replacement model does either: it is what one downscaled pass over a wide frame IS.
	// The same bird in a 200x150 crop arrives as roughly 100 px.
	//
	// The on/off switch is REQUIRED and not defaulted, on the same reasoning as
	// SP_POC_CAMERA_INTEGRITY: the default here is the dangerous one. A run that cannot
	// see a bird at 30 m, and a report claiming to report "everything that passes", are
	// the same run, and nothing in its output says otherwise. "Did the operator mean to
	// carry the crop pass" has to be something they said.
	{Name: "SP_POC_CROP_PASS", kind: kindBool, when: "SP_POC_TRIGGER_MODE=detector",
		Consequence: "the run does not say whether it carries the crop pass, so a run that can see a bird at 30 m and a run that cannot are indistinguishable afterwards, and neither is named in the output",
		Remedy:      "set SP_POC_CROP_PASS=true to carry the crop pass; set it to false for one downscaled pass over the whole frame, which finds vehicles and people and is silent about everything smaller. False restores the previous behaviour exactly: no state to unwind"},
	// The four knobs below are STRUCTURAL for the reason the novelty ones are: they are
	// measurement settings whose values are stored beside every decision they produced,
	// so a report prints the number rather than naming it. Each is gated on the crop pass
	// being enabled, so a run that carries none is not asked about the cost of carrying
	// one.
	{Name: "SP_POC_CROP_MIN_AREA", kind: kindFloat, structural: true, structuralVal: "0.0004", when: "SP_POC_TRIGGER_MODE=detector",
		Consequence: "a region smaller than this fraction of the frame is two cells of a 64-wide luma grid, which is the noise floor of a brightness difference rather than an object, and a crop pass that magnifies noise spends a full inference on it",
		Remedy:      "set SP_POC_CROP_MIN_AREA=0.0004 — a fraction of the FRAME, not a pixel count, so it survives a change of resolution"},
	{Name: "SP_POC_CROP_PAD", kind: kindFloat, structural: true, structuralVal: "0.35", when: "SP_POC_TRIGGER_MODE=detector",
		Consequence: "a region is cropped at exactly its own edge, so an object that a connected component clipped to its brightest half is magnified with the rest of itself cut off, and the detector is asked about a partial object",
		Remedy:      "set SP_POC_CROP_PAD=0.35 — a fraction of the region's own size, added on every side and clamped to the frame"},
	{Name: "SP_POC_CROP_MAX_REGIONS", kind: kindInt, structural: true, structuralVal: "4", when: "SP_POC_TRIGGER_MODE=detector",
		Consequence: "there is no ceiling on crops per frame, so a busy scene at 25 fps can issue an unbounded number of inferences and the detector becomes the frame-rate bottleneck rather than the novelty gate. What the ceiling left off is COUNTED and printed, because a cap that silently drops is a cap that lies",
		Remedy:      "set SP_POC_CROP_MAX_REGIONS=4 — each crop is one more inference at roughly the whole-frame cost, so this is the run's dominant cost knob"},
	{Name: "SP_POC_CROP_MIN_FRAMES", kind: kindInt, structural: true, structuralVal: "2", when: "SP_POC_TRIGGER_MODE=detector",
		Consequence: "a crop-sourced track is held to the same minimum as a whole-frame one, and that minimum is calibrated for whole-frame evidence. A bird at 30 m is on screen about a second and a half, which at the detection rate is two or three samples, so a bird is erased by the very rule written to reject compression artefacts, and it is the smallest object the system exists to report",
		Remedy:      "set SP_POC_CROP_MIN_FRAMES=2 — it must be at or below SP_POC_TRACK_MIN_FRAMES, and the validator refuses it above, because a lower bar that is higher is not a lower bar"},
	// The novelty RATE is the other load-bearing parameter, and it is required rather
	// than defaulted for the same reason SP_POC_CROP_PASS is: the default here is the
	// one that quietly does not work.
	//
	// A bird at 30 m is on screen for about 1.5 s. It has to be PROPOSED at least three
	// times in that window or it is never reliably cropped, and three proposals in 1.5 s
	// is 2 per second. Two per second is not a margin, it is the floor, and it is
	// reached only at the optimistic end of the dwell estimate. So the rate is named,
	// the shortfall arithmetic is a REFUSAL rather than a note, and the gate reports
	// what it actually achieved when the source cannot deliver what was asked for.
	{Name: "SP_POC_NOVELTY_FPS", kind: kindInt, when: "SP_POC_TRIGGER_MODE=detector",
		Consequence: "the run does not say how often it proposes regions, so a novelty rate that cannot cover the smallest object's dwell looks exactly like a detector that cannot see small objects. Those are different faults with different owners and the same symptom",
		Remedy:      "set SP_POC_NOVELTY_FPS=0 to run the novelty pass on every decoded frame (the honest default for a one-camera POC, measured at 3.5 ms per 1080p frame), or set it to a rate and the validator refuses anything that cannot propose a region three times in the shortest dwell the system claims to report"},

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
	// The blindness map, and it is STRUCTURAL rather than required.
	//
	// Not required because its absence is a real, statable world: an account without
	// the rights to register Analytics Event Types cannot create them, and the run
	// still has to work — it pushes detection events, records every absence, and
	// refuses to push an absence as if it were a crossing. Requiring the map would
	// make that account unable to run at all, which is a worse answer than running
	// with the limitation printed.
	//
	// Structural rather than a plain default because the empty string IS the
	// meaningful value here: it says "no blindness types are registered", and that
	// has to be something the operator typed rather than something they failed to.
	{Name: "SP_POC_EVENT_TYPE_GUIDS", kind: kindString, structural: true, structuralVal: "",
		Consequence: "there is no Analytics Event Type for \"this camera was not assessed\", so a blindness cannot become a filterable XProtect alarm. It stays in this module's store, `pocctl brief` reports the window as unsearchable, and nothing is pushed as if it were a crossing",
		Remedy: "set SP_POC_EVENT_TYPE_GUIDS to 'class@absence_state=guid' entries separated by ';', e.g. " +
			"SP_POC_EVENT_TYPE_GUIDS='*@not_asked=<guid1>;*@degraded=<guid2>;*@cannot_tell=<guid3>'. Only " +
			"not_asked, degraded and cannot_tell are accepted; no_objects is a negative answer and does not get an alarm type"},
	// The execution provider, and it is STRUCTURAL rather than required for the same
	// reason SP_POC_EVENT_TYPE_GUIDS is: the CPU is a real, measured, honest
	// configuration and refusing to run without a GPU stated would make the majority
	// of machines unable to run at all.
	//
	// It is structural rather than defaulted because the WHOLE POINT of the variable
	// is that the recorded device has to be one somebody chose. A defaulted value
	// would be `cpu` and a run that silently fell back to the CPU while recording
	// `cuda` is the failure this module exists to end.
	{Name: "SP_POC_ORT_EP", kind: kindString, structural: true, structuralVal: "cpu",
		Consequence: "every latency, detection rate and memory figure is measured on a device nobody named. The numbers would be real and the record would be incomplete, which is the shape of a claim nobody can reproduce",
		Remedy: "set SP_POC_ORT_EP=cpu or SP_POC_ORT_EP=cuda. `cuda` is REFUSED before the session opens unless " +
			"nvidia-smi reports the configured device, because ONNX Runtime falls back to the CPU and logs it " +
			"rather than returning an error"},
	{Name: "SP_POC_ORT_CUDA_DEVICE", kind: kindInt, structural: true, structuralVal: "0",
		Consequence: "only read when SP_POC_ORT_EP=cuda, and it would silently name GPU 0 on a multi-GPU machine",
		Remedy:      "set SP_POC_ORT_CUDA_DEVICE to one of the indices `nvidia-smi --query-gpu=index --format=csv` prints; CUDA numbers devices from zero"},
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

// The three trigger modes, as the one canonical declaration.
//
// `config` owns this vocabulary because it owns SP_POC_TRIGGER_MODE, the variable
// whose value selects one. It is a leaf package — it imports `classify` and
// nothing else internal — so `soak` and `store` both alias these constants rather
// than repeating the literals.
//
// They were literals in three places before. A fourth mode added to one of them
// would have produced a build that validated a mode it could not run, or ran one
// it could not validate. A closed set is worth exactly one declaration.
const (
	// TriggerModeDetector is the in-process ONNX detector: real classes and real
	// scores per frame, which is the only thing that makes a precision number
	// possible at all. It is the shipped path.
	TriggerModeDetector = "detector"
	// TriggerModeNovelty is the whole-frame change trigger: licence-free, no
	// weights, and no class vocabulary at all.
	TriggerModeNovelty = "novelty"
	// TriggerModeSidecar is the per-frame Python classifier, kept as the
	// cross-decoder parity reference.
	TriggerModeSidecar = "sidecar"
)

// ValidTriggerMode reports whether m names a mode this build operates.
func ValidTriggerMode(m string) bool {
	switch m {
	case TriggerModeDetector, TriggerModeNovelty, TriggerModeSidecar:
		return true
	}
	return false
}

// usesDetector reports whether this run's boxes come from the in-process
// detector. Every detector precondition is gated on this one predicate, for the
// reason the sidecar predicate gives: separate conditionals are separate places
// to leave a gate hard-on and demand an ONNX file from a run that never opens
// one.
func (c Config) usesDetector() bool { return c.TriggerMode == TriggerModeDetector }

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
	// SourcePublic says the live source is a REAL CAMERA relayed by somebody
	// else: a YouTube live cam, a municipal open feed, anything whose camera
	// exists and is not ours. The label it derives is "public relay of a real
	// camera, NOT ours", and it is separate from SourceRelay because a bool
	// cannot say both "this is a recording" and "this is a camera".
	SourcePublic bool

	AIAddr         string
	AIModel        string
	SidecarScript  string
	AIFPS          int
	AIDeadline     time.Duration
	AIBudget       time.Duration
	AIBackpressure time.Duration
	AIInputWidth   int

	// TriggerMode is where the boxes come from. One of [TriggerModeDetector] (the
	// shipped in-process ONNX detector), [TriggerModeNovelty] (pure-Go
	// whole-frame change measurement, licence-free, no class vocabulary) or
	// [TriggerModeSidecar] (the per-frame Python classifier, kept as a
	// threshold-selection and parity tool).
	TriggerMode string

	// CameraIntegrity says whether this run carries the REFERENCE trigger beside
	// the alarm one. It is required and not defaulted because the default would
	// be the dangerous one: the reference trigger pins a view on disk, and a view
	// that was pinned by a run which did not mean to is a view every later
	// measurement was made against.
	CameraIntegrity bool

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
	// without this. See `models/yolov4n.anchors.txt` and `pocctl model-probe`.
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
	// ZonesPath is SP_POC_ZONES, and it is EMPTY in the primary configuration.
	//
	// Empty means PRESENCE MODE: no line is drawn, nothing is configured, and every
	// tracked object that is seen often enough is reported once with its class and its
	// measured box. Non-empty means line-crossing mode, where only objects that cross a
	// named line are reported.
	//
	// The rollback from presence to crossing is this one variable. That is what makes
	// presence safe to ship as the default: there is no state to unwind and no second
	// code path to keep in step.
	ZonesPath string
	Cooldown  time.Duration

	// CropPass is SP_POC_CROP_PASS. See the schema entry for why it is required rather
	// than defaulted.
	CropPass bool
	// CropMinArea is SP_POC_CROP_MIN_AREA: the smallest novelty region worth a crop, as
	// a fraction of the frame.
	CropMinArea float64
	// CropPad is SP_POC_CROP_PAD: the context added around a region on every side, as a
	// fraction of the region's own size.
	CropPad float64
	// CropMaxRegions is SP_POC_CROP_MAX_REGIONS, the ceiling on crops per frame. What it
	// left off is counted, because a cap that silently drops is a cap that lies.
	CropMaxRegions int
	// CropMinFrames is SP_POC_CROP_MIN_FRAMES: the minimum number of sightings for a
	// track whose box came from a magnified crop.
	//
	// It is the source-aware half of TrackMinFrames and it is why the minimum exists
	// twice rather than being lowered globally. Lowering the global minimum re-admits
	// exactly the single-frame shadow and artefact noise the rule was added to stop, and
	// that would be visible in the false-positive rate.
	CropMinFrames int
	// NoveltyFPS is SP_POC_NOVELTY_FPS: how often the novelty pass runs, in passes per
	// second. Zero means EVERY DECODED FRAME.
	//
	// It is load-bearing rather than a cost knob. Novelty is what PROPOSES the region a
	// crop is taken from, so a region that is not proposed is not cropped and an object
	// that is not cropped is never measured at the resolution that finds it. A bird at
	// 30 m is on screen for about 1.5 s and needs three proposals in that window.
	NoveltyFPS int

	// --- the in-process ONNX detector ---
	//
	// Only read when TriggerMode is `detector`. See the `--- in-process
	// detector ---` block in the schema for why each one is required by name
	// rather than defaulted here.
	DetModel string
	// DetInput is the model's square input size. It is a MODEL property and it is
	// cross-checked against the shape the session declares: a mismatch is a
	// refusal, because letterboxing a frame into the wrong square is the single
	// most productive way to produce confident nonsense.
	DetInput int
	// DetNMSClass and DetNMSGlobal are the TWO suppression passes and they are
	// separate settings rather than one, because they do different jobs. The
	// class-aware pass at a low IoU collapses duplicates of one class; the
	// class-agnostic pass at a high IoU collapses a `car` and a `truck` box on the
	// same object, which the first pass structurally cannot do — it never compares
	// across classes.
	DetNMSClass  float64
	DetNMSGlobal float64
	// DetMaxBoxes is the ceiling on boxes per frame, before association. What it
	// removed is counted and reported.
	DetMaxBoxes int
	// DetScore is the MODEL'S EMIT FLOOR: a raw candidate below it is never
	// turned into a box at all. It is not the approval bar — SP_POC_SCORE_THRESHOLD
	// is that. The three form an ordered chain and the validator refuses it
	// inverted:
	//
	//	DET_SCORE <= GHOST_MIN_SCORE <= SCORE_THRESHOLD
	//	 emit floor      draw floor        approval bar
	DetScore float64
	// ORTLib is the path to the ONNX Runtime shared library, named by path rather
	// than resolved from PATH so the recorded SHA-256 names one file.
	ORTLib string
	// ORTThreads is the intra-op thread count. Recorded with every latency
	// measurement, because a p99 without its thread count describes no build.
	ORTThreads int
	// BoxLogFrames bounds the per-frame box log. At the cap every frame that
	// produced a crossing is kept and every Nth of the rest.
	BoxLogFrames int

	// approved guards the resolved class set. See ApprovedClasses.
	//
	// It is a POINTER to the cache and the cache is behind it, because Config is
	// passed by value to every reader in this module — `watch` alone hands it to
	// seven of them — and a value holding a sync.Once makes each of those a
	// `copylocks` finding under `go vet`. The gate runs vet, so the choice was
	// between the two: a cache that survives being copied, or a Config nobody can
	// pass. The cache is behind a pointer precisely so the copy carries the cache
	// with it instead of copying the lock.
	// approved is the resolved SP_POC_CLASSES cache.
	//
	// It is behind a POINTER, and the reason is specific and was learned the hard
	// way. `Config` is passed by value to every reader in this module — `watch`
	// alone hands it to seven of them — so a cache written into `Config`'s own
	// fields is written into whichever copy happened to resolve first, and every
	// later copy reads it empty. `TestTheResolutionIsStableAcrossCopies` is that
	// failure: the first reader approved `car`, the second did not.
	//
	// A `*approvedCache` behind a pointer makes every copy carry the same cache
	// rather than a copy of the cache, which is also why the sync.Once inside it is
	// safe to copy around: the pointer is copied, the struct behind it is not.
	approved      *approvedCache
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
	// EventTypeGUIDs is SP_POC_EVENT_TYPE_GUIDS, resolved. See eventtypes.go for
	// why the map is the only source of truth for a blindness event's type.
	EventTypeGUIDs EventTypeMap
	// ORTEP is SP_POC_ORT_EP, already normalised to `cpu` or `cuda`. See
	// ortdetect/execution_provider.go for why a default would be a defect.
	ORTEP string
	// ORTCUDADevice is SP_POC_ORT_CUDA_DEVICE, the zero-based CUDA device index. It
	// means nothing when ORTEP is `cpu` and it is NOT defaulted to "the first GPU"
	// there, because a number beside a field that does not apply is a number somebody
	// will later trust.
	ORTCUDADevice   int
	PushMaxAttempts int
	PushTimeout     time.Duration
	PushGrace       time.Duration

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
	boolean(&cfg.SourcePublic, "SP_POC_SOURCE_PUBLIC")
	str(&cfg.AIAddr, "SP_POC_AI_ADDR")
	str(&cfg.AIModel, "SP_POC_AI_MODEL")
	str(&cfg.SidecarScript, "SP_POC_SIDECAR_SCRIPT")
	str(&cfg.TriggerMode, "SP_POC_TRIGGER_MODE")
	boolean(&cfg.CameraIntegrity, "SP_POC_CAMERA_INTEGRITY")
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
	// An empty zones value is PRESENCE MODE, not a missing one, so `str` is given what
	// to do with the empty string rather than refusing it. It is read through the
	// ordinary `get` so it is still declared as seen: the dead-variable check at the end
	// of Load panics on a schema entry nobody reads, and a variable that became optional
	// by being special-cased out of `get` would be exactly that.
	if raw, ok := get("SP_POC_ZONES"); ok {
		cfg.ZonesPath = strings.TrimSpace(raw)
	}
	// The crop pass's five settings. They are read unconditionally rather than behind the
	// detector-mode gate so the dead-variable check covers them too: the schema's own
	// `when` has already refused to demand them outside detector mode, and a structural
	// default fills them in.
	boolean(&cfg.CropPass, "SP_POC_CROP_PASS")
	flt(&cfg.CropMinArea, "SP_POC_CROP_MIN_AREA")
	flt(&cfg.CropPad, "SP_POC_CROP_PAD")
	integer(&cfg.CropMaxRegions, "SP_POC_CROP_MAX_REGIONS")
	integer(&cfg.CropMinFrames, "SP_POC_CROP_MIN_FRAMES")
	num(&cfg.NoveltyFPS, "SP_POC_NOVELTY_FPS")
	dur(&cfg.Cooldown, "SP_POC_COOLDOWN_MS")
	str(&cfg.DetModel, "SP_POC_DET_MODEL")
	num(&cfg.DetInput, "SP_POC_DET_INPUT")
	flt(&cfg.DetNMSClass, "SP_POC_DET_NMS_CLASS")
	flt(&cfg.DetNMSGlobal, "SP_POC_DET_NMS_GLOBAL")
	num(&cfg.DetMaxBoxes, "SP_POC_DET_MAX_BOXES")
	flt(&cfg.DetScore, "SP_POC_DET_SCORE")
	str(&cfg.ORTLib, "SP_POC_ORT_LIB")
	num(&cfg.ORTThreads, "SP_POC_ORT_THREADS")
	integer(&cfg.BoxLogFrames, "SP_POC_BOX_LOG_FRAMES")
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
	num(&cfg.ORTCUDADevice, "SP_POC_ORT_CUDA_DEVICE")
	if raw, ok := get("SP_POC_ORT_EP"); ok {
		// Normalised through ortdetect's own predicate so the vocabulary has one home,
		// and the refusal the operator reads is the same sentence the session would
		// give. Config does not re-implement it: two normalisers drift, and the drift
		// is a provider string nobody can match against a machine.
		ep, err := ortdetect.ValidateEP(raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("SP_POC_ORT_EP: %w", err))
		} else {
			cfg.ORTEP = ep
		}
	} else {
		cfg.ORTEP = "cpu"
	}
	if raw, ok := get("SP_POC_EVENT_TYPE_GUIDS"); ok {
		m, err := ParseEventTypeMap(raw)
		if err != nil {
			// Collected rather than returned, so an operator with three bad entries
			// fixes them in one pass instead of re-running once per entry. `get` has
			// already marked the variable seen, so the dead-variable check is not
			// disturbed by the failure.
			errs = append(errs, &MissingError{Var: "SP_POC_EVENT_TYPE_GUIDS", Raw: raw, ParseErr: err,
				Consequence: "an Analytics Event Type map that cannot be read would resolve nothing, and every blindness " +
					"state would be refused at the push site instead of at startup",
				Remedy: "set SP_POC_EVENT_TYPE_GUIDS to 'class@absence_state=guid' entries separated by ';'"})
		} else {
			cfg.EventTypeGUIDs = m
		}
	}
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
	// The class cache is created HERE, by the loader, and not lazily by the reader.
	//
	// `Config` is passed by value, so a cache a reader creates for itself belongs to
	// that reader's copy and is invisible to the next one: `watch` hands this Config
	// to seven functions and only the first of them would have approved anything.
	// The loader is the one place that hands out copies, so it is the one place that
	// can hand out a shared cache with them.
	cfg.approved = &approvedCache{}
	return cfg, nil
}

// RequiredUnder returns, in schema order, every variable that is REQUIRED BY
// NAME for the environment `lookup` describes and does not supply.
//
// It is the same enumeration `Load` performs, exposed rather than reimplemented,
// and it exists for the callers that assemble their OWN environment. `pocctl
// demo` builds one as a slice of `NAME=value` strings and hands it to a child
// process; until this existed, the only thing that could tell whether that slice
// was complete was running `pocctl demo` and reading where it stopped.
//
// That is not a small gap. A required variable added to the schema does not reach
// a hand-built slice, and the schema's own test suite stays green throughout,
// because the schema does not know the slice exists. Two variables were made
// required under `SP_POC_TRIGGER_MODE=detector` on one commit and `pocctl demo`
// — the module's headline command — refused to start at step 6 of 7, after
// building both binaries, after the fault rehearsal and after the push target was
// listening. Nothing but running the command found it.
//
// Structural variables are NOT listed, and neither is a variable whose `when:`
// condition does not hold in this environment. Both fill themselves in or do not
// apply, and listing them would make a caller add values whose absence changes
// nothing — which is how a caller ends up copying a secret-looking line into a
// repository to satisfy a check that never needed it.
//
// A nil lookup is an empty environment, not the process environment: a caller
// asking what is required should be told what is required unconditionally rather
// than what happens to be set on the machine running the test.
func RequiredUnder(lookup Lookup) []string {
	if lookup == nil {
		lookup = func(string) (string, bool) { return "", false }
	}
	names := make([]string, 0, len(schema))
	for _, f := range schema {
		// The MERGED entry, not the one from `schema`. The two camera credentials
		// appear in both tables: `schema` describes what they mean and
		// `structuralDefaults` declares that a fully public camera needs neither.
		// `schemaByName` is what `get` reads, so it is what decides whether a
		// variable is required — and reading `schema` alone reported
		// SP_POC_SOURCE_USER and SP_POC_SOURCE_PASSWORD as missing from every
		// environment that correctly leaves them unset, which is the refusal this
		// project exists to avoid making.
		merged, known := schemaByName[f.Name]
		if !known {
			merged = f
		}
		if merged.structural {
			continue
		}
		if merged.when != "" && !conditionHolds(lookup, merged.when) {
			continue
		}
		if raw, present := lookup(merged.Name); present && strings.TrimSpace(raw) != "" {
			continue
		}
		names = append(names, merged.Name)
	}
	return names
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
// ceilDiv is a division that rounds UP, for the remedy half of a refusal.
//
// A refusal that names a remedy has to name a value that actually works. Rounding DOWN
// here would print SP_POC_AI_FPS=1 for a minimum of 3 and a dwell of 2 s, which is still
// one sample short, so an operator who followed the advice exactly would still be
// refused. A refusal whose remedy does not work is worse than no remedy at all, because
// it teaches people that the messages are approximate.
func ceilDiv(a, b int) int {
	if b <= 0 {
		return a
	}
	return (a + b - 1) / b
}

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
	case TriggerModeDetector, TriggerModeNovelty, TriggerModeSidecar:
	default:
		add("SP_POC_TRIGGER_MODE", "%q is not one of detector, novelty, sidecar; the run would not be able to say where its boxes came from, "+
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

	// A public relay is a live source by the same construction. A file on disk
	// is not a camera somebody else owns, so the pair has nothing to describe.
	if c.SourcePublic && !c.isLiveSource() {
		add("SP_POC_SOURCE_PUBLIC",
			"the run says the source is a public relay of a real camera, and the source is %q, which reads a local file. There is no camera in that sentence at all. Set SP_POC_SOURCE_KIND=rtsp or url, or set SP_POC_SOURCE_PUBLIC=false", c.SourceKind)
	}

	// The two relay flags cannot both be true, and the refusal is by name
	// because the combination is a specific lie rather than a nonsense pair:
	// SourceRelay's sentence denies a camera ("relay of recorded material, NOT
	// a camera") and SourcePublic's asserts one ("public relay of a real
	// camera"), so a run carrying both would print whichever won the branch
	// order while having verbally claimed the opposite on the previous line.
	// A source is one or the other, and which one it is has to be said.
	if c.SourcePublic && c.SourceRelay {
		add("SP_POC_SOURCE_PUBLIC",
			"both SP_POC_SOURCE_PUBLIC and SP_POC_SOURCE_RELAY are true. They are different sentences about the same source: a relay of RECORDED material is not a camera, and a public relay of a real camera is. Both cannot be true of one stream, and a run that carries both prints whichever the label derived first. Set one to false")
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

	if c.AIFPS < 0 {
		add("SP_POC_AI_FPS", "%d frames per second is not a rate", c.AIFPS)
	}
	// ZERO IS A RATE, and it means "every decoded frame". It used to be refused
	// here as "not a rate", which left the most demanding mode — full-rate
	// detection — the one mode the configuration could not express. The demand is
	// real and it is enforced where the arithmetic exists: preflight refuses by
	// name with `det_p99 x ai_fps < 1000` when the detector cannot hold the rate.
	// Refusing the spelling here would have made the check unreachable rather
	// than strict.
	if c.AIDeadline <= 0 {
		add("SP_POC_AI_DEADLINE_MS", "a non-positive deadline makes every answer late")
	}
	if len(c.Classes) == 0 {
		add("SP_POC_CLASSES", "an empty class list means no box is approved, and the run would be a measurement of nothing")
	}
	// The execution provider is re-checked here as well as in Load, because
	// [Config.ORTEP] is a plain field and a Config assembled by a caller rather than
	// by Load can carry anything in it. The session is the last line of defence, but a
	// run that reaches the session with an unknown provider has already printed its
	// header.
	if ep := ortdetect.NormalizeEP(c.ORTEP); ep == "" {
		add("SP_POC_ORT_EP", fmt.Sprintf("%q is not an execution provider this build performs; it is `cpu` or `cuda`, "+
			"and an unknown value is refused rather than treated as the default because ONNX Runtime falls back to "+
			"the CPU and says so in a log line rather than in an error", c.ORTEP))
	}
	// A negative device is refused rather than clamped. CUDA numbers devices from zero,
	// so -1 names no device, and clamping it to 0 would quietly put the run on a GPU
	// nobody asked for.
	if c.ORTCUDADevice < 0 {
		add("SP_POC_ORT_CUDA_DEVICE", fmt.Sprintf("%d is not a CUDA device id; CUDA numbers devices from zero, so this "+
			"names no device and clamping it to 0 would put the run on a GPU nobody asked for", c.ORTCUDADevice))
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
	// The LEFT-hand link of the same chain, and it is checked only in detector
	// mode because the other two floors belong to a trigger that has no model
	// emit stage.
	//
	// RC-19. An inverted chain lies in BOTH directions at once, which is why it
	// cannot be caught downstream: with DET_SCORE above GHOST_MIN_SCORE, a box
	// the model never emitted is drawn as a ghost, and a box the model did emit
	// is refused. The picture shows objects that are not there and omits objects
	// that are, and every downstream number is computed over that. Neither half
	// is individually detectable — only the ordering is.
	if c.usesDetector() {
		if c.DetScore < 0 || c.DetScore > 1 {
			add("SP_POC_DET_SCORE", "%v is not a probability", c.DetScore)
		}
		if c.DetScore > c.GhostMinScore {
			add("SP_POC_DET_SCORE", "%v is above SP_POC_GHOST_MIN_SCORE (%v), so the score chain is inverted: "+
				"candidates the model scored below its own emit floor would still be drawn as ghosts, and candidates above it "+
				"would be refused. The chain must read DET_SCORE (%v) <= GHOST_MIN_SCORE (%v) <= SCORE_THRESHOLD (%v)",
				c.DetScore, c.GhostMinScore, c.DetScore, c.GhostMinScore, c.ScoreThreshold)
		}
		if strings.TrimSpace(c.DetModel) == "" {
			add("SP_POC_DET_MODEL", "SP_POC_TRIGGER_MODE=detector and no model named; a detector run that silently opened no file is a run that reports zero detections with no error")
		}
		if c.DetInput < 32 || c.DetInput > 4096 {
			add("SP_POC_DET_INPUT", "%d is not a plausible square input size. Read it from the session: `pocctl fixtureprobe` prints the declared shape, and a wrong value here letterboxes the frame into a square the model was not exported for", c.DetInput)
		}
		if c.DetNMSClass <= 0 || c.DetNMSClass >= 1 {
			add("SP_POC_DET_NMS_CLASS", "%v is not an IoU in (0,1)", c.DetNMSClass)
		}
		if c.DetNMSGlobal <= 0 || c.DetNMSGlobal >= 1 {
			add("SP_POC_DET_NMS_GLOBAL", "%v is not an IoU in (0,1)", c.DetNMSGlobal)
		}
		// The global pass is the pass that collapses a `car` and a `truck` on one
		// object, and it only does that if it is the LOOSER of the two. Inverted,
		// it is redundant with the class-aware pass and a cross-class pair survives
		// as two boxes and two crossings.
		if c.DetNMSGlobal < c.DetNMSClass {
			add("SP_POC_DET_NMS_GLOBAL", "%v is tighter than SP_POC_DET_NMS_CLASS (%v). The class-agnostic pass exists to catch the pair a class-aware pass cannot compare, so it must be the looser of the two; inverted it is redundant and one object becomes two boxes", c.DetNMSGlobal, c.DetNMSClass)
		}
		if c.DetMaxBoxes < 1 {
			add("SP_POC_DET_MAX_BOXES", "%v; a cap of zero draws nothing and reports a zero detection rate that reads like an empty road", c.DetMaxBoxes)
		}
		if strings.TrimSpace(c.ORTLib) == "" {
			add("SP_POC_ORT_LIB", "SP_POC_TRIGGER_MODE=detector and no runtime library named; `pocctl fetch-runtime` writes the path it verified")
		}
		if c.ORTThreads < 1 {
			add("SP_POC_ORT_THREADS", "%v threads; at least one is required, and the number is recorded with every latency measurement because a p99 without it describes no build", c.ORTThreads)
		}
	}
	if c.BoxLogFrames < 1 {
		add("SP_POC_BOX_LOG_FRAMES", "%v; a cap of zero logs nothing and the replay that a Kalman crossing depends on has no per-frame boxes to read", c.BoxLogFrames)
	}
	if c.TrackMinFrames < 1 {
		add("SP_POC_TRACK_MIN_FRAMES", "%v frames; at least one is required, because a track that has been seen zero times is not an object", c.TrackMinFrames)
	}
	// The cross-check that was missing, and it is missing for a specific reason worth
	// writing down: SP_POC_TRACK_MIN_FRAMES and SP_POC_AI_FPS were validated
	// INDEPENDENTLY and behave as if no relationship exists, because no relationship had
	// ever been stated. Nothing in this file related them, and the combination in which
	// the minimum exceeds the samples a dwell time can produce was accepted.
	//
	// The consequence is not subtle and it is not a tuning matter. A vehicle crossing a
	// frame at 30 km/h from 30 m is on screen for one to two seconds; at 2 detection
	// samples a second that is 2 to 4 samples. A minimum of 3 is therefore already at the
	// edge, and a bird 40 px across at 30 m, on screen for about the same second and a
	// half, reaches 2 to 4 samples against a minimum of 3 and is erased HALF THE TIME.
	// Not by the detector and not by resolution, but by the rule that exists to reject
	// single-frame compression artefacts. The smallest object the system exists to
	// report is the one the anti-jitter rule removes.
	//
	// So the combination is refused BY NAME, with the consequence and the remedy stated,
	// in the same shape as every other refusal in this function. Two remedies are named
	// because both are correct and the operator knows which one they meant: sample more
	// often, or, and this is the one the crop pass exists for, accept that a magnified
	// sighting is worth more per sample and set SP_POC_CROP_MIN_FRAMES below the global
	// minimum.
	if c.AIFPS > 0 && c.TrackMinFrames > 0 {
		// The dwell window a crossing-sized object gets, in samples. Two seconds is the
		// 1 to 2 s from the object's own arithmetic, taken at its LOW end: refusing on
		// the pessimistic bound means a configuration is only accepted when it works for
		// the shortest dwell the system claims to report, which is the only defensible
		// way to phrase it.
		const shortestDwellSeconds = 2
		samples := c.AIFPS * shortestDwellSeconds
		if c.TrackMinFrames > samples {
			add("SP_POC_TRACK_MIN_FRAMES",
				"%d samples required, but at SP_POC_AI_FPS=%d a crossing-sized object is on screen for about %d seconds and yields %d sample(s). The minimum can never be reached, so every object this run exists to report is silently dropped and the run reports a clean zero detections with no error.\n"+
					"  Raise SP_POC_AI_FPS to at least %d, or lower SP_POC_TRACK_MIN_FRAMES to at most %d.\n"+
					"  If the object is SMALL rather than slow, a bird at 30 m and not a lorry, the second remedy is SP_POC_CROP_MIN_FRAMES: a magnified sighting is a structurally better observation, and the crop pass is what makes two of them enough. Lowering the global minimum instead re-admits the single-frame shadow and artefact noise this rule was added to stop, and that shows up in the false-positive rate.",
				c.TrackMinFrames, c.AIFPS, shortestDwellSeconds, samples,
				ceilDiv(c.TrackMinFrames, shortestDwellSeconds), samples)
		}
	}
	// The crop pass's own chain. Each is a single ordering or a single bound, and each of
	// them is the difference between a crop pass that finds small objects and one that
	// either cannot or costs more than it saves.
	if c.CropPass {
		if c.CropMinArea <= 0 || c.CropMinArea > 0.5 {
			add("SP_POC_CROP_MIN_AREA", "%v is not a fraction of the frame between 0 and 0.5; a larger one would reject every region a camera can produce", c.CropMinArea)
		}
		if c.CropPad < 0 {
			add("SP_POC_CROP_PAD", "%v is negative; a negative pad crops away part of the region it is meant to give context to", c.CropPad)
		}
		if c.CropMaxRegions < 1 {
			add("SP_POC_CROP_MAX_REGIONS", "%d; at least one is required, because a crop pass that may infer nothing is a novelty engine with an inference budget of zero. Set it to 0 and SP_POC_CROP_PASS=false to say the run carries no crop pass", c.CropMaxRegions)
		}
		// The source-aware minimum is only meaningful when it is LOWER. A value above the
		// global minimum applies the STRICTER bar to the better measurement, which is the
		// opposite of what a magnified sighting is worth, and a report calling it
		// "source-aware" would be describing a bar that never loosens.
		if c.CropMinFrames < 1 {
			add("SP_POC_CROP_MIN_FRAMES", "%d sightings; at least one is required, because a track that has been seen zero times is not an object", c.CropMinFrames)
		} else if c.CropMinFrames > c.TrackMinFrames {
			add("SP_POC_CROP_MIN_FRAMES",
				"%d is above SP_POC_TRACK_MIN_FRAMES (%d), so the source-aware minimum holds a MAGNIFIED sighting to the STRICTER bar and there is no lower bar at all. The two measurements are not the same kind of evidence: a box found in a 200x150 crop arrives as roughly 100 px where the whole-frame pass delivered 8, and two of the former are worth more than two of the latter. Set SP_POC_CROP_MIN_FRAMES at or below SP_POC_TRACK_MIN_FRAMES, or drop SP_POC_TRACK_MIN_FRAMES if the whole-frame pass is not meant to report small objects at all",
				c.CropMinFrames, c.TrackMinFrames)
		}
		// The novelty RATE against the smallest dwell the system claims to report.
		//
		// This is the refusal the crop pass's headline rests on. Novelty proposes the
		// region, the crop measures it at a resolution that can find a bird at all, and
		// so a rate that cannot propose a region often enough means the object is never
		// cropped and never measured. The symptom is identical to a detector that
		// cannot see small objects, which is why it has to be a refusal at startup and
		// not a note in a report: the two faults have different owners and the same
		// evidence.
		//
		// The arithmetic is the plan's own. A bird at 30 m is on screen about 1.5 s, and
		// it needs three proposals in that window, so three proposals in 1.5 s is 2 per
		// second, which is the floor rather than a margin.
		if _, nerr := novelty.NoveltyRateCoversDwell(c.NoveltyFPS, novelty.SmallestDwellSeconds,
			novelty.ProposalsNeededForACrop); nerr != nil {
			add("SP_POC_NOVELTY_FPS", "%s", strings.TrimPrefix(nerr.Error(), "novelty: "))
		}
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
	classes, _, err := c.approvedSets()
	return classes, err
}

// approvedSets resolves SP_POC_CLASSES ONCE and returns both halves: the expanded
// class names, and the family names that were NAMED rather than expanded.
//
// Both come out of ONE resolution and are returned together, because they share one
// cache and two functions each doing their own `Once.Do` is a cache that fills once
// for the first caller and never again for the second — which is a class filter that
// approves nothing depending on which function the caller happened to reach for.
//
// That failure is not hypothetical: it is what the first version of this did, and
// TestAClassListIsResolvedOnceAndNotPerFrame caught it immediately.
func (c Config) approvedSets() (classes, families map[string]bool, err error) {
	cache := c.approved
	if cache == nil {
		cache = &approvedCache{}
	}
	cache.once.Do(func() {
		resolved, rerr := classify.Resolve(c.Classes)
		if rerr != nil {
			cache.err = rerr
			return
		}
		set := make(map[string]bool, len(resolved))
		for _, k := range resolved {
			set[k] = true
		}
		// The families NAMED in the list, kept beside the classes they expand to.
		//
		// This is not a second, looser way to approve a class. It is the only way a
		// class can be approved at all when it is not in the taxonomy, and the
		// shipped trigger's class is exactly that case: the novelty engine emits
		// `motion`, `classify.FamilyOf("motion")` returns `other`, and `other` is
		// not a member of any family in the table because it IS the family.
		//
		// Before this, `SP_POC_CLASSES=other` expanded to the COCO classes whose
		// family is `other` and `IsApproved` did an exact lookup against that set —
		// so `motion` was refused, every candidate was dropped as `class not on the
		// approved list`, and the shipped path could not produce one box in a run
		// configured by its own demo. Naming `motion` did not help either:
		// `classify.Resolve` refuses a name that is neither a family nor a class,
		// by name, and it was right to.
		fams := make(map[string]bool, 4)
		for _, e := range c.Classes {
			if classify.IsFamily(e) {
				fams[strings.ToLower(strings.TrimSpace(e))] = true
			}
		}
		cache.classes, cache.families, cache.err = set, fams, nil
	})
	if cache.err != nil {
		return nil, nil, cache.err
	}
	out := make(map[string]bool, len(cache.classes))
	for k, v := range cache.classes {
		out[k] = v
	}
	fout := make(map[string]bool, len(cache.families))
	for k, v := range cache.families {
		fout[k] = v
	}
	return out, fout, nil
}

// approvedCache is what Config.approved points at.
type approvedCache struct {
	once     sync.Once
	classes  map[string]bool
	families map[string]bool
	err      error
}

// IsApproved reports whether a class may produce a crossing event.
//
// A class is approved when it is NAMED in SP_POC_CLASSES, or when its FAMILY is
// named there. The second half is what makes the shipped trigger usable at all: the
// novelty engine emits `motion`, which is not a class in the COCO taxonomy, so the
// only thing it can be matched on is the family `FamilyOf` assigns it — `other`.
//
// Both halves live in this one function rather than in a sibling the caller has to
// remember, because a class filter with two halves and one caller is a filter with
// two halves and a bug: the run would approve a class in one path and refuse it in
// another, and the refusal is the half that produces silence.
func (c Config) IsApproved(class string) bool {
	set, fams, err := c.approvedSets()
	if err != nil {
		// An unresolvable class list approves NOTHING, and Load refuses such a
		// configuration before a run starts. Reaching here means something built
		// a Config without loading one; approving everything would be the
		// dangerous answer, so this refuses.
		return false
	}
	if set[strings.ToLower(strings.TrimSpace(class))] {
		return true
	}
	return fams[strings.ToLower(string(classify.FamilyOf(class)))]
}

// IsApprovedFamily reports whether a family was NAMED in SP_POC_CLASSES.
//
// It answers the family question on its own, for a caller that has a family and no
// class — a report grouping by family, or a screen deciding what to draw per family.
func (c Config) IsApprovedFamily(class string) bool {
	_, fams, err := c.approvedSets()
	if err != nil {
		return false
	}
	return fams[strings.ToLower(string(classify.FamilyOf(class)))]
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
