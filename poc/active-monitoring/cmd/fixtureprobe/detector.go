package main

// The detector half of `fixtureprobe`, and the contract record it writes.
//
// WHY IT IS A SEPARATE MODE AND NOT A FLAG ON THE NOVELTY PATH. The novelty probe
// answers "can this clip move a changed-fraction bar". The detector probe answers
// "can this model, with this export, produce a box on this material". Those are
// different questions about different components, and a single tool answering both
// would have one mode for each — and the run-time ones conflict: the novelty probe
// deliberately uses `motion` as a class name because that is all a change-fraction
// has, and a detector run against a model that emits `car` would report every frame
// as a class mismatch.
//
// WHAT IT PRINTS, AND WHY IT PRINTS ALL OF IT. This repository has shipped a silent
// zero three times, and each time the diagnosis needed exactly one fact the run did
// not print:
//
//   - the sidecar's unknown-field wire mismatch: the answer arrived and could not be
//     parsed;
//   - the sidecar's `input_w` key: the answer arrived and could not be parsed;
//   - this decoder's single-channel probability check, found while writing it: the
//     answer arrived, parsed, and came out empty because no sigmoid was applied.
//
// In all three the output was "0 detections" and exit status 0. So the first run of
// any new model prints the five facts that distinguish them:
//
//	DECLARED OUTPUT SHAPE   [1,84,8400] or [1,8400,84] — fields-first or fields-last
//	RECORD WIDTH           84, and whether column 4 is an objectness
//	RAW CANDIDATE COUNT     how many records the tensor held before any filter
//	PRE / POST NMS          how many cleared the floor, and what each pass removed
//	PER-CLASS COUNTS       which class the boxes actually carry
//
// and the layout is read from the DECLARED SHAPE and nothing else, because the
// bytes cannot tell [1,84,8400] from [1,8400,84] — they are the same numbers in a
// different order.
//
// The tool REFUSES a model it cannot read rather than reporting zero, and the
// refusal names the axis and the field count, which is what sends a reader to the
// exporter instead of to the camera.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/classify"
	"github.com/Sergey-Bar/ai-captain-poc/internal/ortdetect"
)

// contractFile is where the probe writes the machine-readable record.
//
// It is the Go port of `pyworker/probe_model.py`'s `*.contract.json`, and it lives
// at the same path so a reader who has seen one has seen the other. The file
// records what the model IS, not what it scored — a contract that recorded a score
// would be a second thing to keep in sync with the first.
const contractFile = "models/detector.contract.json"

// detectorFlags are the probe's own flags, kept apart from the package-level
// `flag` set the novelty path uses so the two modes cannot see each other's flags.
type detectorFlags struct {
	library   string
	model     string
	threads   int
	input     int
	emit      float64
	nmsClass  float64
	nmsGlobal float64
	maxBoxes  int
	minArea   float64
	contract  string
}

// ContractRecord is the machine-readable answer to "what model is this".
//
// Every field is either read from the model, read from the session, or recorded as
// absent. There are no defaults standing in for a measurement, and `Understood` is
// false with a reason when the export could not be read at all — because a contract
// file that says `understood: true` for a model nothing could decode is a file that
// certifies the wrong thing.
type ContractRecord struct {
	// Model is the file's basename, not its full path: a path is machine-specific
	// and a contract travels between machines.
	Model string `json:"model"`
	// SHA256 is computed from the bytes on disk. There is no hash in an ONNX graph
	// and no runtime API that reports one, so this is the only honest value.
	SHA256  string `json:"sha256"`
	Bytes   int64  `json:"bytes"`
	Licence string `json:"licence"`
	// LicenceRecorded is false when the licence is empty. It is separate from the
	// string so a reader can tell "recorded as MIT" from "recorded as nothing", and
	// the second is the one that matters.
	LicenceRecorded bool   `json:"licence_recorded"`
	Source          string `json:"source"`

	InputName   string   `json:"input_name"`
	InputW      int      `json:"input_w"`
	InputH      int      `json:"input_h"`
	InputLayout string   `json:"input_layout"`
	InputRank   int      `json:"input_rank"`
	Outputs     []Output `json:"outputs"`

	// DecoderFamily is `fused` — the only family this decoder reads. It is stated
	// rather than inferred, because a reader comparing this file against
	// pyworker's must not have to know that the anchored family was refused.
	DecoderFamily string `json:"decoder_family"`
	// Fields, HasObjectness and CoordSpace are DECIDED, not assumed, and the
	// decision is what makes them worth recording: a wrong one shifts every class
	// index by one and reports a truck as a bench.
	Fields        int    `json:"fields"`
	HasObjectness string `json:"has_objectness"`
	CoordSpace    string `json:"coord_space"`
	// ClassCount is how many names the model itself declared, and zero means the
	// COCO-80 shapes were assumed. A zero here is reported rather than hidden.
	ClassCount int `json:"class_count"`
	// ClassesUsed names the classes a probe run actually saw boxes of.
	ClassesUsed []string `json:"classes_used"`

	EmitScore  float64 `json:"emit_score"`
	NMSClass   float64 `json:"nms_class_iou"`
	NMSGlobal  float64 `json:"nms_global_iou"`
	MaxBoxes   int     `json:"max_boxes"`
	MinBoxArea float64 `json:"min_box_area"`

	// Latency is the measurement over the frames the probe ran, and it is here
	// because the throughput gate refuses on it. A contract file without a p99
	// cannot be used to decide whether a rate is achievable.
	Frames     int     `json:"frames"`
	Measured   int     `json:"frames_measured"`
	Refused    int     `json:"frames_refused"`
	TotalBoxes int     `json:"total_boxes"`
	P50MS      float64 `json:"p50_ms"`
	P95MS      float64 `json:"p95_ms"`
	P99MS      float64 `json:"p99_ms"`
	Achievable float64 `json:"achievable_fps"`

	Understood bool   `json:"understood"`
	Reason     string `json:"reason"`
	// ProbedAt is when this record was written. A contract with no date cannot be
	// distinguished from one that has been true for a year.
	ProbedAt string `json:"probed_at"`
	Platform string `json:"platform"`
}

// Output is one declared model output, with its shape verbatim.
type Output struct {
	Name string  `json:"name"`
	Dims []int64 `json:"dims"`
	// Records and Fields are the two readings of that shape, and they are what a
	// reader needs to see the axis decision made for them rather than deduce it.
	Records  int    `json:"records"`
	Fields   int    `json:"fields"`
	Layout   string `json:"layout"`
	ReadNote string `json:"read_note"`
}

// runDetector is the detector mode.
func runDetector(paths []string, f detectorFlags) bool {
	allOK := true
	record := &ContractRecord{
		Model:         filepath.Base(f.model),
		Licence:       licenceOf(f.model),
		Source:        "named by SP_POC_DET_MODEL",
		DecoderFamily: "fused",
		EmitScore:     f.emit,
		NMSClass:      f.nmsClass,
		NMSGlobal:     f.nmsGlobal,
		MaxBoxes:      f.maxBoxes,
		MinBoxArea:    f.minArea,
		ProbedAt:      time.Now().UTC().Format(time.RFC3339),
		Platform:      runtime.GOOS + "/" + runtime.GOARCH,
	}
	if record.Licence != "" {
		record.LicenceRecorded = true
	}

	if sum, size, err := ortdetect.HashModel(f.model); err == nil {
		record.SHA256, record.Bytes = sum, size
	} else {
		// Not fatal. The probe still measures; it just cannot say which weights it
		// measured, and the contract records that it could not.
		record.Reason = "the model's SHA-256 could not be read: " + err.Error()
		fmt.Printf("WARNING: %v\n", record.Reason)
	}

	sess, err := ortdetect.Open(ortdetect.SessionConfig{
		LibraryPath: f.library, ModelPath: f.model, Threads: f.threads,
	})
	if err != nil {
		record.Understood = false
		if record.Reason == "" {
			record.Reason = err.Error()
		}
		fmt.Printf("REFUSED: %v\n", err)
		fmt.Println("This is not a zero. A detector that cannot be opened produces no boxes, and a")
		fmt.Println("probe that reported that as '0 boxes' would be the silent-zero defect this tool")
		fmt.Println("exists to catch.")
		writeContract(f.contract, record)
		return false
	}
	defer func() { _ = sess.Close() }()

	in := sess.Input()
	record.InputName, record.InputW, record.InputH = in.Name, in.W, in.H
	record.InputLayout, record.InputRank = in.Layout, in.Rank
	record.Understood = true

	fmt.Printf("model %s\n", record.Model)
	fmt.Printf("  sha256        %s (%d bytes)\n", record.SHA256, record.Bytes)
	fmt.Printf("  licence       %s\n", licenceOrUnrecorded(record.Licence))
	fmt.Printf("  input         %q %s %dx%d rank=%d\n", in.Name, in.Layout, in.W, in.H, in.Rank)

	// The DECLARED SHAPES, printed verbatim and before any frame is decoded. This is
	// the first line a reader looks at when the run reports nothing.
	for _, o := range sess.Outputs() {
		out := Output{Name: o.Name, Dims: o.Dims}
		// The reading is computed HERE, from the shape, and not left to the decode —
		// so a shape the decoder will refuse is visible before the refusal.
		if rec, fields, layout, note := readShape(o.Dims); note != "" {
			out.ReadNote = note
			record.Understood = false
			record.Reason = fmt.Sprintf("output %q declares %v, which cannot be read: %s", o.Name, o.Dims, note)
		} else {
			out.Records, out.Fields, out.Layout = rec, fields, layout
			record.Fields = fields
		}
		record.Outputs = append(record.Outputs, out)
		fmt.Printf("  output        %q declares %v", o.Name, o.Dims)
		if out.ReadNote != "" {
			fmt.Printf("   UNREADABLE: %s\n", out.ReadNote)
		} else {
			fmt.Printf("   -> %d records of %d fields (%s)\n", out.Records, out.Fields, out.Layout)
		}
	}

	if !record.Understood {
		fmt.Printf("\nREFUSED: %s\n", record.Reason)
		writeContract(f.contract, record)
		return false
	}

	det, err := ortdetect.NewDetector(ortdetect.Config{
		Session: sess,
		Decode: ortdetect.DecodeConfig{
			EmitScore: f.emit, MaxBoxes: f.maxBoxes,
			NMSClass: f.nmsClass, NMSGlobal: f.nmsGlobal, MinBoxArea: f.minArea,
		},
		InputSize: in.W,
	})
	if err != nil {
		fmt.Printf("\nREFUSED: %v\n", err)
		record.Understood = false
		record.Reason = err.Error()
		writeContract(f.contract, record)
		return false
	}

	for _, path := range paths {
		if !probeClip(path, det, record, in) {
			allOK = false
		}
	}

	// The percentiles are the throughput gate's input, so they are printed AND
	// written. A contract record without a p99 cannot be used to decide whether a
	// rate is achievable.
	if v, ok := det.Latencies().P50(); ok {
		record.P50MS = v
	}
	if v, ok := det.Latencies().P95(); ok {
		record.P95MS = v
	}
	if v, ok := det.Latencies().P99(); ok {
		record.P99MS = v
	}
	if v, ok := det.Latencies().AchievableFPS(); ok {
		record.Achievable = v
	}

	fmt.Printf("\nframes %d  measured %d  refused %d  boxes %d\n",
		record.Frames, record.Measured, record.Refused, record.TotalBoxes)
	fmt.Printf("latency p50 %.1fms  p95 %.1fms  p99 %.1fms  ->  %.1f fps achievable\n",
		record.P50MS, record.P95MS, record.P99MS, record.Achievable)
	if ok, arithmetic := detectorFits(record.P99MS, f.input); ok {
		fmt.Printf("throughput   %s\n", arithmetic)
	} else {
		fmt.Printf("throughput   REFUSED: %s\n", arithmetic)
	}
	if record.TotalBoxes == 0 {
		fmt.Println()
		fmt.Println("ZERO BOXES. That is a fact about the material and the export, not a pass, and")
		fmt.Println("these are the four things to check, in this order:")
		fmt.Println("  1. the output shape above — a fields-first tensor read as fields-last gives")
		fmt.Println("     records of the wrong width, which is noise rather than detections")
		fmt.Println("  2. has_objectness — reading an 84-field record as 85 shifts every class by one")
		fmt.Println("  3. coord_space — a pixel-space model read as fractions draws every box 640x too small")
		fmt.Println("  4. emit_score — the floor is deliberately low, so a model emitting nothing means")
		fmt.Println("     the weights are wrong rather than the threshold")
	}

	writeContract(f.contract, record)
	return allOK
}

// readShape reports how a declared output shape will be read.
//
// It is the same rule the decoder applies, called here so the decision is VISIBLE
// before the decode rather than inferred from its output. A shape the decoder will
// refuse is refused here, with the reason naming the axis.
func readShape(dims []int64) (records, fields int, layout string, note string) {
	d := dims
	for len(d) > 2 && d[0] == 1 {
		d = d[1:]
	}
	if len(d) < 2 {
		return 0, 0, "", "expected at least rank 2 after dropping a batch axis"
	}
	chosen := -1
	for axis, v := range d {
		if int(v) == 4+ortdetect.NumClasses || int(v) == 5+ortdetect.NumClasses {
			chosen = axis
		}
	}
	if chosen < 0 {
		return 0, 0, "", fmt.Sprintf("no axis is a record width for a model with %d classes; a record "+
			"must have %d or %d fields and neither is present",
			ortdetect.NumClasses, 4+ortdetect.NumClasses, 5+ortdetect.NumClasses)
	}
	fields = int(d[chosen])
	records = 1
	for axis, v := range d {
		if axis != chosen {
			records *= int(v)
		}
	}
	if chosen == len(d)-1 {
		return records, fields, string(ortdetect.LayoutFieldsLast), ""
	}
	return records, fields, string(ortdetect.LayoutFieldsFirst), ""
}

// probeClip measures one clip and prints the per-class histogram.
//
// The histogram is the last of the five facts and the most diagnostic: a detector
// finding only `person` on traffic footage is a detector whose weights are a model
// of people, and the report would say "detections" with a count that looks fine.
func probeClip(path string, det *ortdetect.Detector, record *ContractRecord, in ortdetect.InputSpec) bool {
	w, h, frames := decode(path)
	if len(frames) == 0 {
		fmt.Printf("%-20s decodes zero frames\n", base(path))
		return false
	}
	fmt.Printf("\n%-20s %4dx%-4d %4d frames -> letterboxed into %dx%d %s (content %dx%d, pad %d,%d)\n",
		base(path), w, h, len(frames), in.W, in.H, in.Layout, gContentW(w, h, in.W, in.H), gContentH(w, h, in.W, in.H),
		gPadX(w, h, in.W, in.H), gPadY(w, h, in.W, in.H))

	counts := map[string]int{}
	families := map[string]int{}
	var perFrame []string
	var frameMeasured, frameRefused int
	// The FIRST measured frame's decoder arithmetic, printed verbatim. It is the
	// frame where `candidates`, `pre_nms` and `post_nms` say which of the four
	// possible failures this is: the tensor was not a detector's output, the model
	// emitted nothing, the emit floor swallowed everything, or suppression collapsed
	// everything.
	var firstTelemetry ortdetect.Telemetry
	var haveTelemetry bool

	for i, fr := range frames {
		ans := det.Detect(uint64(i), fr, w, h)
		record.Frames++
		if ans.Refusal != nil {
			record.Refused++
			frameRefused++
			if len(perFrame) < 4 {
				perFrame = append(perFrame, ans.Refusal.What)
			}
			continue
		}
		record.Measured++
		frameMeasured++
		record.TotalBoxes += len(ans.Boxes)
		if !haveTelemetry {
			firstTelemetry, haveTelemetry = ans.Telemetry, true
		}
		for _, b := range ans.Boxes {
			counts[b.Class]++
			families[string(classify.FamilyOf(b.Class))]++
		}
	}

	if haveTelemetry {
		t := firstTelemetry
		fmt.Printf("  %-24s candidates %d  pre_nms %d  post_nms_class %d  post_nms %d  dropped_degenerate %d  dropped_tiny %d\n",
			"first measured frame:", t.Candidates, t.PreNMS, t.PostNMSClass, t.PostNMS,
			t.DroppedDegenerate, t.DroppedTiny)
		fmt.Printf("  %-24s fields %d  has_objectness %v  coord_space %s  layout %s  classes %d\n",
			"", t.Fields, t.HasObjectness, t.CoordSpace, t.Layout, t.Classes)
	}

	if len(frames) > 0 {
		fmt.Printf("  %-24s %d frames decoded, %d measured, %d refused\n",
			base(path), len(frames), frameMeasured, frameRefused)
	}

	if len(perFrame) > 0 {
		fmt.Printf("  REFUSED on %d frames, first reason: %s\n", record.Refused, perFrame[0])
	}

	if len(counts) == 0 {
		fmt.Printf("  %-20s NO BOXES on %d measured frames\n", base(path), record.Measured)
		return false
	}
	fmt.Printf("  per-class  %s\n", histogram(counts))
	fmt.Printf("  per-family %s\n", histogram(families))
	return record.Measured > 0 && record.TotalBoxes > 0
}

// gScale and the geometry helpers are the letterbox geometry, recomputed here only
// to PRINT it beside the boxes.
//
// They are not used for anything else — the detector computes its own and the
// decoder divides by those. Printing them is the point: a reader who sees every box
// sitting in the top 60% of the frame needs to know that 128 of the 640 rows are
// letterbox padding before they decide the detector is broken.
func gScale(w, h, iw, ih int) float64 {
	sx := float64(iw) / float64(w)
	sy := float64(ih) / float64(h)
	if sy < sx {
		return sy
	}
	return sx
}

func gContentW(w, h, iw, ih int) int {
	return int(float64(w)*gScale(w, h, iw, ih) + 0.5)
}

func gContentH(w, h, iw, ih int) int {
	return int(float64(h)*gScale(w, h, iw, ih) + 0.5)
}

func gPadX(w, h, iw, ih int) int { return (iw - gContentW(w, h, iw, ih)) / 2 }
func gPadY(w, h, iw, ih int) int { return (ih - gContentH(w, h, iw, ih)) / 2 }

// histogram renders class counts highest-first, and it prints the WHOLE
// distribution rather than a top three.
//
// A truncated histogram hides the failure that matters: a model emitting fifty
// classes at score 0.05 and nothing above, which looks like "many classes" and is
// actually no detections.
func histogram(m map[string]int) string {
	if len(m) == 0 {
		return "(none)"
	}
	type kv struct {
		k string
		v int
	}
	pairs := make([]kv, 0, len(m))
	for k, v := range m {
		pairs = append(pairs, kv{k, v})
	}
	sort.Slice(pairs, func(a, b int) bool {
		if pairs[a].v != pairs[b].v {
			return pairs[a].v > pairs[b].v
		}
		return pairs[a].k < pairs[b].k
	})
	var b strings.Builder
	for i, p := range pairs {
		if i > 0 {
			b.WriteString("  ")
		}
		fmt.Fprintf(&b, "%s=%d", p.k, p.v)
	}
	return b.String()
}

// detectorFits is the same arithmetic the preflight gate refuses on, so the probe's
// verdict and the gate's cannot disagree.
func detectorFits(p99 float64, _ int) (bool, string) {
	if p99 <= 0 {
		return false, "nothing was measured"
	}
	fps := 1000 / p99
	return true, sprintf("det_p99=%.1fms supports %.1f fps; at 5 fps that is %.0fms of a 1000ms second",
		p99, fps, p99*5)
}

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

func licenceOrUnrecorded(l string) string {
	if strings.TrimSpace(l) == "" {
		return "NOT RECORDED"
	}
	return l
}

// licenceOf reads a model's licence from the provenance record beside the weights.
//
// It reads `testdata/labels.json`, which is where every other material's licence
// lives, and it reports a MISSING licence rather than a default. A model whose terms
// nobody wrote down is a fact about the answer — a demo whose output somebody else
// sees inherits them.
func licenceOf(modelPath string) string {
	raw, err := os.ReadFile(filepath.Join("testdata", "labels.json"))
	if err != nil {
		return ""
	}
	var doc struct {
		Entries []struct {
			Path    string `json:"path"`
			Licence string `json:"licence"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ""
	}
	base := filepath.ToSlash(filepath.Clean(modelPath))
	for _, e := range doc.Entries {
		if filepath.ToSlash(filepath.Clean(e.Path)) == base {
			return e.Licence
		}
	}
	return ""
}

// writeContract records what the model IS, and it refuses to overwrite a record
// that said the model could not be read with one that says it could.
//
// That direction is safe — a re-probe after fixing something should succeed. The
// other direction is the dangerous one: a successful probe overwriting an `understood:
// false` record would erase the evidence that the export was wrong, and the next
// reader would have no way to know the file had ever been refused.
func writeContract(path string, r *ContractRecord) {
	if path == "" {
		return
	}
	if existing, err := readContract(path); err == nil && !existing.Understood && r.Understood {
		fmt.Printf("\nrefusing to overwrite %s, which records that this model could NOT be read:\n"+
			"  %s\n  Move it aside deliberately if you have fixed the model and want the new\n"+
			"  record. A successful probe must not erase the evidence that the export was wrong.\n",
			path, existing.Reason)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Printf("could not create %s: %v\n", filepath.Dir(path), err)
		return
	}
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		fmt.Printf("could not encode the contract record: %v\n", err)
		return
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		fmt.Printf("could not write %s: %v\n", path, err)
		return
	}
	fmt.Printf("\nwrote %s\n", path)
	if !r.LicenceRecorded {
		fmt.Println("  the model's licence is NOT RECORDED. Add it to testdata/labels.json: a run whose")
		fmt.Println("  output is shown to somebody else depends on a term nobody has written down.")
	}
}

func readContract(path string) (*ContractRecord, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r ContractRecord
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// unusedGuards keep the imports honest for the flags the two modes share.
var _ = os.Stdout
