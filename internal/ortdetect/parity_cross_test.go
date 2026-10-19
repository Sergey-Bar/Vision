//go:build parity

// TestCrossDecoderParity — the comparison the POC gate has always claimed.
//
// `scripts/verify.ps1` step "decoder parity" runs, in order:
//
//     go test -count=1 -run 'TestCrossDecoderParity' -tags parity ./internal/ortdetect/
//
// It has run that command since the step was written, and the named test did not
// exist in this package, so `go test` printed "no tests to run" and exited 0 —
// the gate then announced that the Go decoder and pyworker agree on the same
// tensor, over a comparison nobody had ever run. That is the worst shape a green
// check can take: not a test that fails to catch a defect, but a sentence about
// evidence that does not exist.
//
// This file is that test. It is behind the `parity` tag because the step that
// runs it is conditional on the Python sidecar and the model being present, and a
// test that silently skips when they are absent would be the same defect again
// in a different coat. The gate's SKIP message is explicit: the sidecar's own
// tests are run separately, and the comparison here is skipped with a reason
// rather than passed.
//
// WHAT MAKES IT A PARITY TEST AND NOT TWO TESTS ABOUT ONE DECODER
//
// The tensor is built once, on the Go side, from `parityFixture` — the fixture
// `TestTheFixtureExercisesBothNMSPasses` has already checked has the properties a
// parity fixture needs. It is handed to Python as the exact float32 values that
// Go decoded, and neither side rebuilds it. The only thing Python contributes is
// its decoder; the only thing Go contributes is its own.
//
// Both sides then must agree on ALL of:
//
//   * the record width and which axis it was on (layout), because reading the
//     wrong axis is a silent zero and not an error;
//   * how many candidates there were, because a difference here is a suppression
//     difference and not a geometry one;
//   * how many boxes survived, one to one, in the same place, at the same score,
//     with the same class.
//
// The tolerances are the ones `parity_test.go` uses and for its own documented
// reason: geometry at 1/1000, scores looser, because the last bit of a float32
// sigmoid differs between numpy and Go and a parity check that failed on that
// would be a flaky check — which is worse than no check, because a flaky check
// is one a team learns to re-run.

package ortdetect

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// parityFixtureFile is the on-the-wire shape handed to the Python side.
//
// `data` is the tensor's own []float32 widened to float64, which is exact for
// every finite float32, so Python decodes the same values and not a rounding of
// them.
type parityFixtureFile struct {
	Dims []int64   `json:"dims"`
	Data []float64 `json:"data"`
}

// pythonForParity finds an interpreter that can run the sidecar.
//
// The gate requires `py`; this accepts `python` as well, because a test that
// only runs under a Windows Python launcher is a test that cannot be compared on
// the machine the comparison matters on.
func pythonForParity() (string, bool) {
	for _, name := range []string{"py", "python", "python3"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, true
		}
	}
	return "", false
}

// pyworkerDir is the sidecar, which is the only place the decode path lives.
func pyworkerDir(t *testing.T) string {
	t.Helper()
	// Go runs a test with its working directory at the package, so the module
	// root is two levels up from internal/ortdetect. The Python sidecar lives
	// in the POC module, which is a separate module in this repository: the Go
	// half of the wire moved into the product and the Python half stayed.
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("cannot find the module root: %v", err)
	}
	dir := filepath.Join(root, "poc", "active-monitoring", "pyworker")
	if _, err := os.Stat(filepath.Join(dir, "server.py")); err != nil {
		t.Skipf("the sidecar is not at %s, so there is nothing to compare against: %v", dir, err)
	}
	return dir
}

// pyBox is one decoded box as the sidecar reports it.
//
// `cls` is the class INDEX the sidecar decoded and `name` is the word it would
// put on the wire. Both are carried because they are different claims: the
// index is arithmetic and must match Go's, the name is a vocabulary claim and
// TestTheClassVocabularyIsTheSameOnBothSides owns it.
type pyBox struct {
	Class int     `json:"cls"`
	Name  string  `json:"name"`
	Score float64 `json:"score"`
	X1    float64 `json:"x1"`
	Y1    float64 `json:"y1"`
	X2    float64 `json:"x2"`
	Y2    float64 `json:"y2"`
}

// pyAnswer is the whole sidecar reply.
type pyAnswer struct {
	Boxes     []pyBox `json:"boxes"`
	Telemetry struct {
		Layout     string `json:"layout"`
		Candidates int    `json:"candidates"`
		Fields     int    `json:"fields"`
		PreNMS     int    `json:"pre_nms"`
		PostNMS    int    `json:"post_nms"`
		PostClass  int    `json:"post_nms_class"`
	} `json:"telemetry"`
}

func TestCrossDecoderParity(t *testing.T) {
	py, ok := pythonForParity()
	if !ok {
		t.Skip("no Python interpreter is on PATH, so the sidecar's decoder cannot be run; the Go decoder was not compared against anything")
	}
	worker := pyworkerDir(t)

	// The tensor is built ONCE, from the fixture the package's own parity
	// self-checks have already validated, and handed to both decoders.
	//
	// It is the fields-FIRST layout — [1, F, N], what a YOLO11n export publishes
	// — because that is the harder of the two axis orders to get right: the data
	// cannot distinguish it from fields-last, only the declared shape can, and a
	// decoder that reads the wrong one produces noise rather than an error.
	tensor := fieldsFirstOf(parityFixture())

	path := filepath.Join(t.TempDir(), "parity-fixture.json")
	blob, err := json.Marshal(parityFixtureFile{
		Dims: tensor.Dims,
		Data: widen(tensor.Data),
	})
	if err != nil {
		t.Fatalf("cannot write the fixture: %v", err)
	}
	if err := os.WriteFile(path, blob, 0o600); err != nil {
		t.Fatalf("cannot write the fixture: %v", err)
	}

	out, err := exec.Command(py, "-3", filepath.Join(worker, "parity_decode.py"), path).Output()
	if err != nil {
		if ee, isExit := err.(*exec.ExitError); isExit && len(ee.Stderr) > 0 {
			t.Fatalf("the sidecar's decode of the same tensor failed: %s", string(ee.Stderr))
		}
		t.Fatalf("cannot run the sidecar's decode: %v", err)
	}
	var got pyAnswer
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("the sidecar's answer is not the shape this test expects: %v\n%s", err, out)
	}

	// The class count is passed, not zero. With no names a shape like
	// [1,84,168] is genuinely ambiguous — both axes are plausible record widths
	// — and the structural rule picks the last one that fits, which is 168 and
	// is wrong. The model's own vocabulary is the only thing that settles it,
	// and it settles it on both sides: the sidecar carries 80 names in
	// `fused_engine`, and this passes 80 here. A decoder that reaches for
	// structure when it has the vocabulary is the bug this whole package is
	// built around, so the fixture is arranged so that only the vocabulary gets
	// it right.
	want := Decode(tensor, square640(), NumClasses, defaultCfg())
	if want.Refusal != nil {
		t.Fatalf("the Go decoder refused the fixture it built: %v", want.Refusal)
	}

	// The record axis, first. Everything below is meaningless if the two sides
	// read the tensor as different shapes.
	if got.Telemetry.Layout != string(want.Telemetry.Layout) {
		t.Fatalf("the sidecar read the tensor as %q and Go read it as %q. One of the two is reading the record axis wrongly, which is a silent zero and not an error: the shape %v is the same numbers in a different order",
			got.Telemetry.Layout, want.Telemetry.Layout, tensor.Dims)
	}
	if got.Telemetry.Fields != want.Telemetry.Fields {
		t.Fatalf("the sidecar measured %d fields per record and Go measured %d; a record width disagreement shifts every class by one",
			got.Telemetry.Fields, want.Telemetry.Fields)
	}
	if got.Telemetry.Candidates != want.Telemetry.Candidates {
		t.Fatalf("the sidecar saw %d candidates and Go saw %d. A candidate difference is a suppression difference and it makes the geometry comparison meaningless",
			got.Telemetry.Candidates, want.Telemetry.Candidates)
	}

	// Then the boxes, one to one, at the tolerance the package's own parity test
	// uses. A set comparison would pass when the two decoders produce the same
	// NUMBER of boxes in different places, which is exactly the disagreement
	// worth catching.
	compareBoxSets(t, "pyworker", pyBoxes(got.Boxes), "go", want.Boxes, parityTolerance)

	// The scores are compared separately and more loosely, for the documented
	// reason: the last bit of a float32 sigmoid differs between numpy and Go.
	scoreParity(t, "pyworker", got.Boxes, "go", want.Boxes, scoreParityTolerance)
}

// pyBoxes narrows the sidecar's boxes to the fields the geometry comparison
// uses. The class is compared by INDEX here and by NAME below, because one is
// arithmetic and the other is a vocabulary.
func pyBoxes(in []pyBox) []Box {
	out := make([]Box, 0, len(in))
	for _, b := range in {
		out = append(out, Box{Class: b.Name, Score: b.Score, X1: b.X1, Y1: b.Y1, X2: b.X2, Y2: b.Y2})
	}
	return out
}

// scoreParity compares the scores one to one, in the order `compareBoxSets`
// established, and refuses to compare a score against a box that is not there.
func scoreParity(t *testing.T, aName string, a []pyBox, bName string, b []Box, tol float64) {
	t.Helper()
	if len(a) != len(b) {
		t.Fatalf("%s produced %d boxes and %s produced %d, so their scores cannot be compared one to one", aName, len(a), bName, len(b))
	}
	for i := range a {
		if d := absf(a[i].Score - b[i].Score); d > tol {
			t.Errorf("%s box %d scored %.6f and %s box %d scored %.6f. The last bit of a sigmoid differs between numpy and Go and that is what the %v tolerance is for; a difference larger than it is arithmetic",
				aName, i, a[i].Score, bName, i, b[i].Score, tol)
		}
	}
}

// TheClassVocabularyIsTheSameOnBothSides is the second half of parity, and it is
// here because a decoder can agree on every coordinate and still label the
// object wrongly — which is a defect neither geometry comparison can see, and
// one that reaches the wire as the wrong Alarm Definition.
func TestTheClassVocabularyIsTheSameOnBothSides(t *testing.T) {
	if _, ok := pythonForParity(); !ok {
		t.Skip("no Python interpreter is on PATH, so the sidecar's class names cannot be read")
	}
	worker := pyworkerDir(t)

	// Read the sidecar's OWN vocabulary through its own helper, rather than
	// parsing `server.py` for a list literal: a parsed list is a list the
	// import would have caught anyway.
	cmd := exec.Command(pythonForParityName(t), "-3", "-c", `
import json, os, sys
sys.path.insert(0, os.environ["PYWORKER_DIR"])
import test_decode
json.dump(test_decode.coco_names(), sys.stdout)
`)
	cmd.Env = append(os.Environ(), "PYWORKER_DIR="+worker)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("cannot read the sidecar's class vocabulary: %v", err)
	}
	native := map[int]string{}
	if err := json.Unmarshal(out, &native); err != nil {
		t.Fatalf("the sidecar's vocabulary is not an index-to-name map: %v\n%s", err, out)
	}
	if len(native) != NumClasses {
		t.Fatalf("the sidecar carries %d class names and the Go vocabulary has %d; the two decoders would label the same box with different words",
			len(native), NumClasses)
	}
	for i := 0; i < NumClasses; i++ {
		if native[i] != ClassName(i) {
			t.Fatalf("class %d is %q to the sidecar and %q to Go. The geometry can agree and the object still be named wrongly on the wire",
				i, native[i], ClassName(i))
		}
	}
}

// pythonForParityName is pythonForParity without its boolean, for the places that
// have already established an interpreter exists.
func pythonForParityName(t *testing.T) string {
	t.Helper()
	name, ok := pythonForParity()
	if !ok {
		t.Fatal("no Python interpreter is on PATH")
	}
	return name
}

// scoreParityTolerance is the score tolerance, and it is the same number
// TestTheScoresAreComparedSeparatelyFromTheGeometry names inside itself. That
// one is local because it is documentation of a single test's consequence; this
// one has to be reachable from the score comparison helper. They are the same
// number for the same reason: the last bit of a float32 sigmoid differs between
// numpy and Go, and a parity check that fails on it is a flaky check.
const scoreParityTolerance = 0.005

// fieldsFirstOf transposes a fields-LAST tensor into a fields-FIRST one.
//
// It exists so this test can hand both decoders the SAME bytes in the harder of
// the two orders, without re-declaring the fixture's records: re-declaring them
// is how a parity test ends up comparing two fixtures. The operation is the
// inverse of `records()`'s own transpose, so a bug in one shows up as a
// disagreement between this tensor and the Go decoder — which is the thing being
// compared, and is either a real finding or the decoder is wrong.
func fieldsFirstOf(t Tensor) Tensor {
	if len(t.Dims) != 3 || t.Dims[0] != 1 {
		return t // Not a batched rank-2 tensor; hand it over unchanged and let the decoders refuse it.
	}
	n, fields := int(t.Dims[1]), int(t.Dims[2])
	if n <= 0 || fields <= 0 || n*fields != len(t.Data) {
		return t
	}
	out := make([]float32, len(t.Data))
	for r := 0; r < n; r++ {
		for f := 0; f < fields; f++ {
			out[f*n+r] = t.Data[r*fields+f]
		}
	}
	return Tensor{Name: t.Name, Dims: []int64{1, int64(fields), int64(n)}, Data: out}
}

// widen widens a []float32 to []float64 without changing any value, which is
// exact for every finite float32 and is what makes the handed-over tensor the
// same tensor.
func widen(in []float32) []float64 {
	out := make([]float64, len(in))
	for i, v := range in {
		out[i] = float64(v)
	}
	return out
}

func absf(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
