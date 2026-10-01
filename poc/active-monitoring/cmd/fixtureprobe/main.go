package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/detect"
	"github.com/Sergey-Bar/ai-captain-poc/internal/novelty"
)

// fixtureprobe reports whether a clip can produce a detection at all, BEFORE a run
// is started.
//
// WHY IT EXISTS. The shipped trigger's bar is `SP_POC_NOVELTY_THRESHOLD`, 0.35, and
// it is a CHANGED FRACTION OF THE WHOLE FRAME. The engine diffs consecutive frames,
// subtracts a uniform illumination offset, thresholds the residual and counts the
// cells that moved as a fraction of all cells. So a small object in a large frame
// can never fire, no matter how correct the line is — and a run over such a clip
// reports zero detections, zero errors and exit status 0.
//
// That silence is the most expensive one in this module: it looks exactly like a
// pass. Nothing in the report says "the material was too small to move the bar",
// because nothing measured that. This measures it.
//
// IT CALLS THE ENGINE. An earlier version reimplemented the diff, the illumination
// subtraction and the threshold, and it reported 0.0000 for a clip with a white
// block visibly crossing it — because `scoreMask` compares the residual against the
// threshold directly, and my reimplementation compared it against 0.35x255 instead.
// A probe that models the detector rather than calling it is a second opinion
// about a measurement, and a second opinion is how a QA tool ends up certifying a
// broken fixture.
//
//	go build -o bin/fixtureprobe.exe ./cmd/fixtureprobe
//	bin\fixtureprobe.exe qa\fixtures\crossing.mp4
func main() {
	mode := flag.String("detector", "", "probe the in-process ONNX detector over these clips instead of the novelty engine")
	fLibrary := flag.String("ort", "", "path to the ONNX Runtime shared library (SP_POC_ORT_LIB)")
	fModel := flag.String("model", "", "path to the detector's ONNX model (SP_POC_DET_MODEL)")
	fThreads := flag.Int("threads", 0, "ORT intra-op thread count (SP_POC_ORT_THREADS)")
	fContract := flag.String("contract", contractFile, "where to write the machine-readable contract record")
	fEmit := flag.Float64("emit", 0.05, "the model's emit floor, SP_POC_DET_SCORE")
	fNMSClass := flag.Float64("nms-class", 0.45, "class-aware suppression IoU, SP_POC_DET_NMS_CLASS")
	fNMSGlobal := flag.Float64("nms-global", 0.75, "class-agnostic suppression IoU, SP_POC_DET_NMS_GLOBAL")
	fMaxBoxes := flag.Int("max-boxes", 100, "ceiling on boxes per frame, SP_POC_DET_MAX_BOXES")
	flag.Parse()

	paths := flag.Args()
	if len(paths) == 0 {
		paths, _ = filepath.Glob(filepath.Join("qa", "fixtures", "*.mp4"))
	}

	// The detector mode is a different question about a different component, and it
	// is a flag rather than a second binary because a person diagnosing a zero wants
	// both answers about the same clip without running two tools.
	if *mode != "" {
		if len(paths) == 0 {
			fmt.Println("the detector mode needs at least one clip")
			os.Exit(1)
		}
		ok := runDetector(paths, detectorFlags{
			library:   envOr("SP_POC_ORT_LIB", *fLibrary),
			model:     envOr("SP_POC_DET_MODEL", *fModel),
			threads:   envInt("SP_POC_ORT_THREADS", *fThreads),
			contract:  *fContract,
			emit:      envFloat("SP_POC_DET_SCORE", *fEmit),
			nmsClass:  envFloat("SP_POC_DET_NMS_CLASS", *fNMSClass),
			nmsGlobal: envFloat("SP_POC_DET_NMS_GLOBAL", *fNMSGlobal),
			maxBoxes:  envInt("SP_POC_DET_MAX_BOXES", *fMaxBoxes),
			minArea:   envFloat("SP_POC_MIN_BOX_AREA", 0.0005),
		})
		if !ok {
			os.Exit(1)
		}
		return
	}

	if len(paths) == 0 {
		fmt.Println("usage: fixtureprobe [-detector -model ...] <clip.mp4>...   (or no arguments to scan qa/fixtures)")
		fmt.Println()
		fmt.Println("  says whether each clip can produce a candidate at all, by running the")
		fmt.Println("  novelty engine over its real frames and reporting the peak changed fraction")
		fmt.Println("  against the bar. A clip whose peak is below the bar will produce zero")
		fmt.Println("  detections with no error, and that is a fact about the MATERIAL, not a pass.")
		fmt.Println()
		fmt.Println("  -detector probes the in-process ONNX detector instead and prints the five")
		fmt.Println("  facts that make a zero diagnosable: the declared output shape, the record")
		fmt.Println("  layout, the field count, the raw candidate count, and the per-class histogram.")
		return
	}

	anyBelow := false
	for _, p := range paths {
		if report(p) {
			anyBelow = true
		}
	}
	if anyBelow {
		// A non-zero exit is the whole point: a lab that sweeps a fixture set and
		// cannot tell a usable one from an unusable one is a lab that passes.
		os.Exit(1)
	}
}

// envOr, envInt and envFloat read a fallback from the environment.
//
// This file is a diagnostic tool and not part of the runtime, which is why it reads
// the environment at all. The runtime reads nothing outside internal/config; a
// probe that went through the configuration layer could not be pointed at a model
// whose variables are not yet in the schema, which is exactly the situation it
// exists for.
func envOr(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

func envInt(name string, fallback int) int {
	v := envOr(name, "")
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func envFloat(name string, fallback float64) float64 {
	v := envOr(name, "")
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return f
}

// report prints one clip's measurement and reports whether it can produce a CROSSING.
//
// Two questions, because a run reports one number and the two failures it hides
// are unrelated. The first is whether the novelty engine proposes anything at all —
// a clip too small to move the bar. The second is whether the TRACKER can join those
// regions across frames — which is a different question with a different answer, and
// the one `pocctl demo` cannot show you.
//
// The second one is why this tool exists rather than a `pocctl` subcommand: a run
// over a clip that produces 189 approved boxes and zero crossings prints exactly
// what a run over an empty road prints, and only the tracker knows the difference.
func report(path string) bool {
	w, h, frames := decode(path)
	if len(frames) == 0 {
		fmt.Printf("%-20s decodes zero frames\n", base(path))
		return true
	}

	zone := fixtureZone()

	nv, err := novelty.New(novelty.Defaults())
	if err != nil {
		panic(err)
	}
	// The class list names `motion`, which is what the shipped novelty trigger emits.
	eng, err := detect.New(zone, detect.Config{
		ScoreThreshold: 0.02, Classes: []string{"motion"},
		IoU: 0.3, Tolerance: 0.01, MinArea: 0.0001, TrackMinFrames: 3, MaxBoxesDrawn: 12,
		// MaxAge is REQUIRED and zero means "forget every track at the start of the
		// next frame". It defaulted here, which made this tool report "the tracker
		// never joins them" - a true statement about a configuration nothing would
		// ever run.
		MaxAge: 2 * time.Second,
	})
	if err != nil {
		panic(err)
	}

	var candidates, crossings int
	var peak float64
	at := time.Unix(0, 0).UTC()
	for _, fr := range frames {
		cands, sc := nv.Candidates(fr, w, h)
		candidates += len(cands)
		if sc.Value > peak {
			peak = sc.Value
		}
		boxes := make([]detect.Box, 0, len(cands))
		for _, c := range cands {
			boxes = append(boxes, detect.Box{
				Class: "motion", Score: c.Score,
				X1: c.Box.X1, Y1: c.Box.Y1, X2: c.Box.X2, Y2: c.Box.Y2,
			})
		}
		cx, _, _ := eng.Feed(at, boxes)
		crossings += len(cx)
		at = at.Add(66 * time.Millisecond)
	}

	status := "CROSSES"
	if candidates == 0 {
		status = "no region proposed: too small to move the novelty bar"
	} else if crossings == 0 {
		status = "regions proposed but NO CROSSING: the tracker never joins them"
	}
	fmt.Printf("%-20s %4dx%-4d %4d frames  peak %.3f  regions %-5d crossings %-4d  %s\n",
		base(path), w, h, len(frames), peak, candidates, crossings, status)
	return crossings == 0
}

// fixtureZone is the line every fixture crosses, and it is written out here rather
// than read from testdata because the tool must be usable on a clip alone.
func fixtureZone() detect.Zone {
	return detect.Zone{
		Lines: []detect.Line{{
			Name: "carriageway", A: [2]float64{0.5, 0}, B: [2]float64{0.5, 1},
			Direction: "left-to-right", Classes: []string{"motion"},
		}},
		Polygons: []detect.Polygon{{
			Name:    "frame",
			Points:  [][2]float64{{0, 0}, {1, 0}, {1, 1}, {0, 1}},
			Classes: []string{"motion"},
		}},
	}
}

// rawMin/rawMax report the luma range of a raw RGB frame.
//
// They are here because "the engine scored it 0.0000" and "the engine was fed two
// identical frames" are the same symptom with opposite causes, and the difference is
// thirty characters of pixel data.
func base(path string) string {
	if i := strings.LastIndexAny(path, `\/`); i >= 0 {
		return path[i+1:]
	}
	return path
}

// maxFrames bounds the decode.
//
// It exists because the tool decodes to RAW RGB and holds it all in memory: a 90
// second 1280x720 clip is about 2.5 GB of bytes, and the first run against real
// footage died on the clock rather than on an answer. A sample is enough to say
// whether the novelty engine proposes anything and whether the tracker joins those
// proposals into a crossing; a sample that needs more than this is a question about
// a long take, and the honest answer for a long take is to run the demo.
const maxFrames = 400

// decode turns a clip into the engine's input: raw RGB frames, in order.
//
// One decode, whole file, no seeking. An earlier version used `-ss` per second and
// compared stills, and it reported 0.0000 for every fixture including one with a
// visible moving block, because consecutive `-ss` seeks landed on the same
// keyframe. The engine diffs consecutive frames, so the probe must too.
func decode(path string) (int, int, [][]byte) {
	w, h := dims(path)
	out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-i", path,
		"-frames:v", strconv.Itoa(maxFrames),
		"-f", "rawvideo", "-pix_fmt", "rgb24", "-").Output()
	if err != nil {
		fmt.Printf("%-20s ffmpeg could not decode it: %v\n", base(path), err)
		return w, h, nil
	}
	frameBytes := w * h * 3
	n := len(out) / frameBytes
	if n > maxFrames {
		n = maxFrames
	}
	frames := make([][]byte, n)
	for i := 0; i < n; i++ {
		frames[i] = out[i*frameBytes : (i+1)*frameBytes]
	}
	return w, h, frames
}

func dims(path string) (int, int) {
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height", "-of", "csv=p=0:s=x", path).Output()
	if err != nil {
		panic(err)
	}
	// The FIRST line only. ffprobe emits one `WIDTHxHEIGHT` per stream it reports,
	// and a real clip has an audio stream beside the video one, so `s=x` gives
	// `1280x720x\n\n1280x720x` — and splitting that on `x` yields "1280x720x", which
	// is not a number. It panicked the first time this tool was pointed at real
	// footage, after working perfectly on the generated fixtures.
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	line = strings.TrimSpace(line)
	// csv prints the separator AFTER each field, so the line ends with one and
	// splits into three parts, not two: `1280x720x`. The first two are the numbers.
	p := strings.Split(line, "x")
	if len(p) < 2 {
		panic("ffprobe said " + line + ", which is not WIDTHxHEIGHT")
	}
	var w, h int
	fmt.Sscan(p[0], &w)
	fmt.Sscan(p[1], &h)
	if w <= 0 || h <= 0 {
		panic("ffprobe said " + line + ", which is not a usable frame size")
	}
	return w, h
}
