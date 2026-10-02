package injectionfixture

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixture's own tests, and they exist for a reason that is easy to miss.
//
// Without them the suite would assert an attack it is not running. The MP4 carries a
// recorded SHA-256 and a label describing it, and neither survives: a re-encode that
// dropped the text, a font that failed to load, a crop that cut the sign off. Every
// security test above would stay green, and the one claim in this repository that
// cannot be measured without a provider would be resting on a picture of a picture.
//
// So: the committed reference frame is compared, pixel for pixel, with what this
// package renders today, and the committed video's hash is compared with what
// labels.json says.

const (
	mp4    = "injection-hostile.mp4"
	frame0 = "injection-hostile.frame0.png"
)

func testdataPath(name string) string { return filepath.Join("..", "..", "testdata", name) }

// TestTheCommittedReferenceFrameIsWhatThisCodeRenders is the control on the fixture.
func TestTheCommittedReferenceFrameIsWhatThisCodeRenders(t *testing.T) {
	f, err := os.Open(testdataPath(frame0))
	if err != nil {
		t.Fatalf("%s is missing: %v. Rebuild the fixture with `go run ./cmd/injectionfixture`", frame0, err)
	}
	defer func() { _ = f.Close() }()
	committed, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	rendered := Frame(0, Seconds*FPS)

	if committed.Bounds() != rendered.Bounds() {
		t.Fatalf("the committed frame is %v and this build renders %v", committed.Bounds(), rendered.Bounds())
	}
	diff, first := 0, ""
	for y := rendered.Bounds().Min.Y; y < rendered.Bounds().Max.Y; y++ {
		for x := rendered.Bounds().Min.X; x < rendered.Bounds().Max.X; x++ {
			got, want := committed.At(x, y), rendered.At(x, y)
			if got != want {
				if diff == 0 {
					first = "at " + point(x, y)
				}
				diff++
			}
		}
	}
	if diff != 0 {
		t.Fatalf("%d pixel(s) differ, first %s. The committed fixture no longer contains the attacks this "+
			"package draws, so every security test above is passing against a file with nothing in it. "+
			"Rebuild it with `go run ./cmd/injectionfixture` and commit both files",
			diff, first)
	}
}

// TestTheCommittedVideoIsTheFileLabelsJsonDescribes: the provenance half, and it is
// checked against the FILE rather than against the generator. A hash nobody computed
// is a hash that will be copied forward through every future change.
func TestTheCommittedVideoIsTheFileLabelsJsonDescribes(t *testing.T) {
	body, err := os.ReadFile(testdataPath(mp4))
	if err != nil {
		t.Fatalf("%s is missing: %v", mp4, err)
	}
	sum := sha256.Sum256(body)

	var labels struct {
		Clips []struct {
			File                  string   `json:"file"`
			Provenance            string   `json:"provenance"`
			Licence               string   `json:"licence"`
			SHA256                string   `json:"sha256"`
			Bytes                 int64    `json:"bytes"`
			ReferenceFrame        string   `json:"reference_frame"`
			ReferenceFrameSHA256  string   `json:"reference_frame_sha256"`
			BuiltBy               string   `json:"built_by"`
			Reproducible          bool     `json:"reproducible"`
			ContainsPeople        string   `json:"contains_people"`
			ClearedForExternalUse *bool    `json:"cleared_for_external_use"`
			InjectedText          []string `json:"injected_text"`
		} `json:"clips"`
	}
	raw, err := os.ReadFile(testdataPath("labels.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &labels); err != nil {
		t.Fatal(err)
	}
	var entry *struct {
		File                  string   `json:"file"`
		Provenance            string   `json:"provenance"`
		Licence               string   `json:"licence"`
		SHA256                string   `json:"sha256"`
		Bytes                 int64    `json:"bytes"`
		ReferenceFrame        string   `json:"reference_frame"`
		ReferenceFrameSHA256  string   `json:"reference_frame_sha256"`
		BuiltBy               string   `json:"built_by"`
		Reproducible          bool     `json:"reproducible"`
		ContainsPeople        string   `json:"contains_people"`
		ClearedForExternalUse *bool    `json:"cleared_for_external_use"`
		InjectedText          []string `json:"injected_text"`
	}
	for i := range labels.Clips {
		if labels.Clips[i].File == mp4 {
			entry = &labels.Clips[i]
		}
	}
	if entry == nil {
		t.Fatalf("%s has no labels.json entry, so a number derived from it may not be quoted and every "+
			"test in this repository that leans on it is resting on an unlabelled file", mp4)
	}
	if !strings.EqualFold(entry.SHA256, hex.EncodeToString(sum[:])) {
		t.Fatalf("labels.json says the clip hashes to %s and the committed file hashes to %s. One of them is "+
			"wrong and a reader has no way to tell which", entry.SHA256, hex.EncodeToString(sum[:]))
	}
	if entry.Bytes != int64(len(body)) {
		t.Fatalf("labels.json says %d bytes and the file is %d", entry.Bytes, len(body))
	}
	if entry.Provenance != "synthetic" {
		t.Fatalf("provenance = %q; every pixel here is a Go computation", entry.Provenance)
	}
	if entry.ClearedForExternalUse == nil || !*entry.ClearedForExternalUse {
		t.Fatal("the fixture is not cleared for external use, which would make the one security claim in this " +
			"repository unquotable — and it is generated here with nobody's footage in it")
	}
	if entry.BuiltBy == "" || !strings.Contains(entry.BuiltBy, "injectionfixture") {
		t.Fatalf("built_by = %q; a label that names no generator describes a file nobody can check", entry.BuiltBy)
	}
	if !entry.Reproducible {
		t.Fatal("reproducible = false; the DRAWING is byte-reproducible and the reference frame is what a test " +
			"compares. H.264 is not bit-stable and labels.json says so in its notes")
	}
	if !strings.Contains(entry.Licence, "no rights holder") {
		t.Fatalf("the licence does not say why the question does not arise: %q", entry.Licence)
	}
	// The reference frame's own hash, for the same reason: it is a committed file too.
	fb, err := os.ReadFile(testdataPath(frame0))
	if err != nil {
		t.Fatal(err)
	}
	fsum := sha256.Sum256(fb)
	if !strings.EqualFold(entry.ReferenceFrameSHA256, hex.EncodeToString(fsum[:])) {
		t.Fatalf("labels.json says the reference frame hashes to %s and the file hashes to %s",
			entry.ReferenceFrameSHA256, hex.EncodeToString(fsum[:]))
	}
	if filepath.Base(entry.ReferenceFrame) != frame0 {
		t.Fatalf("reference_frame = %q, want the committed %q", entry.ReferenceFrame, frame0)
	}
}

// TestTheLabelNamesTheSameAttacksTheCodeDraws closes the last gap: the label, the
// source and the pixels are three descriptions of one thing, and this asserts the first
// two agree so that the third cannot quietly differ from both.
func TestTheLabelNamesTheSameAttacksTheCodeDraws(t *testing.T) {
	var labels struct {
		Clips []struct {
			File         string   `json:"file"`
			InjectedText []string `json:"injected_text"`
		} `json:"clips"`
	}
	raw, err := os.ReadFile(testdataPath("labels.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &labels); err != nil {
		t.Fatal(err)
	}
	var named []string
	for _, c := range labels.Clips {
		if c.File == mp4 {
			named = c.InjectedText
		}
	}
	got := Text()
	if len(named) != len(got) {
		t.Fatalf("labels.json names %d injected string(s) and the code draws %d:\nlabel: %v\ncode:  %v",
			len(named), len(got), named, got)
	}
	for i := range got {
		if named[i] != got[i] {
			t.Fatalf("injected string %d: labels.json says %q and the code draws %q", i, named[i], got[i])
		}
	}
}

// TestEveryAttackIsOneAnAdversaryWouldActuallyWrite: four attempts, and the number is
// the point. A banned-word list catches `ignore` and misses the rest, so a fixture
// carrying only the phrase everyone has already seen tests a filter rather than a
// model's judgement.
func TestEveryAttackIsOneAnAdversaryWouldActuallyWrite(t *testing.T) {
	texts := Text()
	if len(texts) < 4 {
		t.Fatalf("the fixture carries %d attempt(s); the guard it defends against needs more than the one "+
			"phrase that is already on every filter list", len(texts))
	}
	seen := map[string]bool{}
	for _, s := range texts {
		if seen[s] {
			t.Fatalf("the same sentence appears twice: %q", s)
		}
		seen[s] = true
		if s != strings.ToUpper(s) {
			t.Fatalf("%q is not upper-case; lower-case signage is a different case and would be a fifth "+
				"fixture rather than a variant of one of these", s)
		}
	}
}

// TestTheRenderedFrameActuallyHasPixelsWhereTheTextIs, rather than a background that
// happens to be the same colour as the band drawn over it. A band and its text in the
// same colour is a plausible bug — `image.Uniform` for both — and it would leave every
// other test in this file green.
func TestTheRenderedFrameActuallyHasPixelsWhereTheTextIs(t *testing.T) {
	img := Frame(0, Seconds*FPS)
	var light, dark int
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if r > 0x8000 && g > 0x8000 && b > 0x8000 {
				light++
			}
			if r < 0x2000 && g < 0x2000 && b < 0x2000 {
				dark++
			}
		}
	}
	if light < 200 {
		t.Fatalf("the frame has %d near-white pixel(s); glyphs are drawn in white on a near-black band, so "+
			"this is below what four lines of 7x13 text need and the text is not there", light)
	}
	if dark < 200 {
		t.Fatalf("the frame has %d near-black pixel(s); the bands are not there", dark)
	}
	// And the moving rectangle is a yellow block, so the clip has something that
	// crosses the line: a fixture that produced no detection would test nothing
	// downstream of the model either.
	//
	// Read from the MIDDLE frame, not frame 0. At frame 0 the rectangle is entirely
	// off the left edge — it starts at -150 — so asserting its presence there would
	// either fail or tempt somebody into loosening the assertion until it passed.
	mid := Seconds * FPS / 2
	img = Frame(mid, Seconds*FPS)
	var yellow int
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if r > 0xE000 && g > 0xE000 && b < 0x4000 {
				yellow++
			}
		}
	}
	if yellow < 1000 {
		t.Fatalf("frame %d has %d yellow pixel(s); the moving rectangle is %d by %d and is either not drawn "+
			"or not the colour it claims", mid, yellow, 150, 90)
	}
}

func point(x, y int) string {
	var b strings.Builder
	b.WriteString("(")
	b.WriteString(itoa(x))
	b.WriteString(",")
	b.WriteString(itoa(y))
	b.WriteString(")")
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
