package ortdetect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The session's refusal paths, and the two decisions it makes before a session
// exists: the input LAYOUT and the model's IDENTITY.
//
// Neither needs an ONNX file or a runtime, and both are where a wrong answer costs
// the most. A wrong layout hands the model a tensor it reads as a different
// picture; a wrong identity means a detection rate that cannot name its weights.

// TestTheInputLayoutIsReadByAxisPositionAndRefusedOtherwise is the D1 decision.
//
// ONNX exports both layouts. Ultralytics writes NCHW and the ONNX model zoo writes
// NHWC, and the first model this repository measured wanted NHWC — so a code path
// that only knew NCHW would have produced confident nonsense from it. What it
// produces is BOXES, on whatever the convolution happens to align with, which is
// worse than an error because those boxes generate line crossings on objects that
// are not there.
func TestTheInputLayoutIsReadByAxisPositionAndRefusedOtherwise(t *testing.T) {
	for _, tc := range []struct {
		name  string
		shape []int64
		want  string
	}{
		{"ultralytics nchw", []int64{1, 3, 640, 640}, "nchw"},
		{"model zoo nhwc", []int64{1, 640, 640, 3}, "nhwc"},
		{"grayscale nchw", []int64{1, 1, 320, 320}, "nchw"},
		{"rgba nchw", []int64{1, 4, 512, 512}, "nchw"},
		{"dynamic batch nchw", []int64{-1, 3, 640, 640}, "nchw"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DetectLayout(tc.shape)
			if err != nil {
				t.Fatalf("a well-formed rank-4 input was refused: %v", err)
			}
			if got != tc.want {
				t.Fatalf("layout = %q, want %q for shape %v", got, tc.want, tc.shape)
			}
		})
	}

	// And the refusals. Every one of these would otherwise be handed to a model as
	// a tensor in a layout nobody chose.
	for _, tc := range []struct {
		name  string
		shape []int64
		want  string
	}{
		{"rank 3", []int64{3, 640, 640}, "not rank 4"},
		{"rank 2", []int64{640, 3}, "not rank 4"},
		{"rank 5", []int64{1, 1, 3, 640, 640}, "not rank 4"},
		{"no channel axis", []int64{1, 640, 640, 7}, "NCHW or NHWC"},
		{"channel count of 2", []int64{1, 2, 640, 640}, "NCHW or NHWC"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DetectLayout(tc.shape)
			if err == nil {
				t.Fatalf("shape %v was read as %q", tc.shape, got)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("the refusal does not mention %q: %v", tc.want, err)
			}
		})
	}
}

// TestTheDeclaredInputSizeFollowsTheLayout is the pair of axes, and reading them
// the wrong way round is a different picture rather than a wrong number.
func TestTheDeclaredInputSizeFollowsTheLayout(t *testing.T) {
	for _, tc := range []struct {
		shape  []int64
		layout string
		w, h   int
	}{
		{[]int64{1, 3, 640, 640}, "nchw", 640, 640},
		{[]int64{1, 3, 640, 384}, "nchw", 384, 640},
		{[]int64{1, 640, 384, 3}, "nhwc", 384, 640},
		{[]int64{1, 640, 640, 3}, "nhwc", 640, 640},
	} {
		w, h, err := InputSize(tc.shape, tc.layout)
		if err != nil {
			t.Fatalf("%v in %s: %v", tc.shape, tc.layout, err)
		}
		if w != tc.w || h != tc.h {
			t.Errorf("%v in %s = %dx%d, want %dx%d", tc.shape, tc.layout, w, h, tc.w, tc.h)
		}
	}

	// A batch axis other than 1 or -1 is refused rather than decoded: a batched
	// export would need every record decoded and one of them used, and the run
	// must not have to guess which.
	if _, _, err := InputSize([]int64{4, 3, 640, 640}, "nchw"); err == nil {
		t.Error("a batch axis of 4 was accepted; this detector runs one frame at a time")
	}
	if _, _, err := InputSize([]int64{1, 3, 0, 640}, "nchw"); err == nil {
		t.Error("a zero-height input was accepted")
	}
	if _, _, err := InputSize([]int64{1, 3, 640, 640}, "NCHW"); err == nil {
		t.Error("a layout with a capital letter in it was accepted")
	}
}

// TestTheModelHashIsComputedFromTheBytesOnDisk is RC-18's raw material.
//
// There is no hash field in an ONNX graph and no runtime API that reports one, so
// the only honest hash is the one computed by reading the file. It is reported
// whether or not it matches anything, and a model whose hash cannot be read is
// refused rather than run unattributed.
func TestTheModelHashIsComputedFromTheBytesOnDisk(t *testing.T) {
	dir := t.TempDir()

	// The empty file's hash is a well-known constant, and checking it against a
	// literal rather than against a second implementation of sha256 is the point:
	// a test that recomputed the hash with the same code would pass if the code
	// were wrong.
	empty := filepath.Join(dir, "empty.onnx")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	sum, n, err := HashModel(empty)
	if err != nil {
		t.Fatal(err)
	}
	const emptySHA = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if sum != emptySHA {
		t.Fatalf("the empty file hashed to %s, want the known SHA-256 of no bytes %s", sum, emptySHA)
	}
	if n != 0 {
		t.Fatalf("the empty file reported %d bytes", n)
	}

	// A file with known content: "abc" hashes to a published value.
	abc := filepath.Join(dir, "abc.onnx")
	if err := os.WriteFile(abc, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum, n, err = HashModel(abc)
	if err != nil {
		t.Fatal(err)
	}
	const abcSHA = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if sum != abcSHA {
		t.Fatalf("abc hashed to %s, want %s", sum, abcSHA)
	}
	if n != 3 {
		t.Fatalf("three bytes reported as %d", n)
	}

	// A file larger than the read buffer, so the streaming loop actually loops.
	// The 257 MB model in this repository is the reason it streams at all, and a
	// one-shot read would allocate the model's whole size to learn its hash.
	big := filepath.Join(dir, "big.onnx")
	const bigSize = 3*(1<<20) + 17
	buf := make([]byte, bigSize)
	for i := range buf {
		buf[i] = byte(i)
	}
	if err := os.WriteFile(big, buf, 0o600); err != nil {
		t.Fatal(err)
	}
	sum, n, err = HashModel(big)
	if err != nil {
		t.Fatal(err)
	}
	if n != bigSize {
		t.Fatalf("a %d-byte file reported %d bytes", bigSize, n)
	}
	if len(sum) != 64 {
		t.Fatalf("hash = %q, want 64 hex characters", sum)
	}
	// Reading it twice gives the same answer, which is the only property that
	// matters about a hash.
	again, _, err := HashModel(big)
	if err != nil {
		t.Fatal(err)
	}
	if again != sum {
		t.Fatalf("the same file hashed to %s and then %s", sum, again)
	}
	// And one changed byte changes it.
	if err := os.WriteFile(big, append(buf, 'x'), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, _, err := HashModel(big)
	if err != nil {
		t.Fatal(err)
	}
	if changed == sum {
		t.Fatal("appending a byte did not change the hash")
	}

	// A missing file is an error, not an empty hash. An empty hash is a claim that
	// something was hashed and it hashed to nothing.
	if _, _, err := HashModel(filepath.Join(dir, "not-there.onnx")); err == nil {
		t.Fatal("a missing file hashed successfully")
	}
}

// TestTheDetectorRefusesRatherThanRunning is the shape of the whole package.
//
// Every one of these is a configuration mistake or a missing dependency. None of
// them may produce a detector that opens and reports zero detections, because that
// is the defect this repository shipped twice and it is indistinguishable from an
// empty road.
func TestTheDetectorRefusesRatherThanRunning(t *testing.T) {
	// No session.
	if _, err := NewDetector(Config{InputSize: 640}); err == nil {
		t.Fatal("a detector was built with no session; it would report zero detections on every frame")
	} else if !strings.Contains(err.Error(), "no session") {
		t.Errorf("the refusal does not say what is missing: %v", err)
	}

	// A nil session must not panic, and must not be reported as anything else.
	if _, err := NewDetector(Config{Session: nil, InputSize: 640}); err == nil {
		t.Fatal("accepted a nil session")
	}
}

// TestTheStateOfAnEmptyFrameIsNotAskedAndNotNoObjects is the law, at the one place
// a frame first arrives.
//
// A frame with no pixels was not measured. That is `not_asked`, and it is a
// different fact from `no_objects`. The difference is the whole of the four-word
// vocabulary: a report that conflates them says the road was empty when the truth
// is that nothing looked at it.
func TestTheStateOfAnEmptyFrameIsNotAskedAndNotNoObjects(t *testing.T) {
	// This needs no session, so it is buildable and testable without cgo. The
	// detector is unusable but its FIRST check is the frame check, and that check
	// must not depend on having a model — a frame with no pixels is not worth
	// loading a model for.
	d := &Detector{}
	ans := d.Detect(7, nil, 0, 0, Rect{})
	if ans.State != StateNotAsked {
		t.Fatalf("state = %q for a frame with no pixels, want %q", ans.State, StateNotAsked)
	}
	if ans.State == StateNoObjects {
		t.Fatal("a frame that was never measured was reported as no_objects. That is a healthy " +
			"answer derived from a failed read, which is what this module's law forbids")
	}
	if ans.Refusal == nil {
		t.Fatal("no refusal: an unmeasurable frame produced a bare empty answer")
	}
	if ans.Seq != 7 {
		t.Fatalf("seq = %d, want 7 — the sequence number must survive so a refusal can be traced "+
			"to the frame it happened on", ans.Seq)
	}
	// A refusal that does not say what to do is a refusal that gets ignored.
	if !strings.Contains(ans.Refusal.Detail, "not_asked") {
		t.Errorf("the refusal does not explain the state it produced: %q", ans.Refusal.Detail)
	}
}

// TestTheAbsenceStatesAreTheContractsOwn is the vocabulary drift guard.
//
// The four words are fixed by the contract and `internal/ai` is where they live.
// This package aliases them rather than restating them, and this test fails if the
// alias stops matching — which is what would happen if someone "fixed" a typo in
// `internal/ai` and did not look here.
func TestTheAbsenceStatesAreTheContractsOwn(t *testing.T) {
	for _, tc := range []struct{ got, want string }{
		{StateAsked, "asked_answered"},
		{StateNoObjects, "no_objects"},
		{StateDegraded, "degraded"},
		{StateNotAsked, "not_asked"},
	} {
		if tc.got != tc.want {
			t.Fatalf("state = %q, want %q. The four words are the contract's and they are aliased "+
				"from internal/ai; a fifth word is not an addition here, it is a drift", tc.got, tc.want)
		}
	}
}

// TestARefusalCarriesANameAndADetail is what makes a zero diagnosable.
//
// "0 detections" sends a reader to look at the model. "the output tensor's shape
// [1,84,8400] cannot be read: no axis is a record width" tells them the exporter is
// wrong and quotes the shape, and that is the difference between an unactionable
// number and a fixable one.
func TestARefusalCarriesANameAndADetail(t *testing.T) {
	r := &Refusal{What: "the output tensor's shape cannot be read", Detail: "shape [1 84 8400]"}
	if r.Error() == "" {
		t.Fatal("a refusal rendered as an empty string")
	}
	if !strings.Contains(r.Error(), "shape [1 84 8400]") {
		t.Fatalf("the detail was lost: %s", r.Error())
	}
	// A refusal with no detail is still a refusal, and says only its name.
	bare := &Refusal{What: "no session"}
	if bare.Error() != "no session" {
		t.Fatalf("a bare refusal rendered as %q", bare.Error())
	}
}
