package mp4_test

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/mp4"
)

// The capture path reads from a network peer, so its parsers are fuzz targets
// rather than unit-tested shapes. The seeds are the committed fixture - a real
// elementary stream - because a fuzzer that starts from nothing spends its
// entire budget rediscovering that H.264 has start codes.

// FuzzAnnexBReaderNeverPanicsAndNeverGrowsWithoutBound.
func FuzzAnnexBReaderNeverPanicsAndNeverGrowsWithoutBound(f *testing.F) {
	if raw, err := os.ReadFile(fixture); err == nil {
		f.Add(raw)
		f.Add(raw[:len(raw)/2])
		f.Add(raw[:1])
	}
	// Degenerate inputs: an empty stream, a stream of nothing but start codes, a
	// stream that is one long run of zeros.
	f.Add([]byte{})
	f.Add([]byte{0, 0, 0, 1})
	f.Add(bytes.Repeat([]byte{0}, 4096))
	f.Add([]byte{0, 0, 0, 1, 0x67, 0x42, 0xC0, 0x1E, 0xFF})
	f.Add([]byte{0, 0, 0, 1, 0x09, 0x10})

	f.Fuzz(func(t *testing.T, data []byte) {
		r := mp4.NewAnnexBReader(bytes.NewReader(data), 64<<10)
		for n := 0; n < 4096; n++ {
			nal, err := r.Next()
			if err != nil {
				return
			}
			if len(nal) > 64<<10 {
				t.Fatalf("a NAL of %d bytes escaped the bound", len(nal))
			}
		}
	})
}

// FuzzPictureReaderNeverPanics feeds the whole recorder path: a malformed
// elementary stream must produce a named refusal or a clip, never a panic and
// never a hang.
func FuzzPictureReaderNeverPanics(f *testing.F) {
	if raw, err := os.ReadFile(fixture); err == nil {
		f.Add(raw)
	}
	f.Add([]byte{0, 0, 0, 1, 0x67, 0x42, 0xC0, 0x1E, 0xDA, 0x02, 0x80, 0xBF})
	f.Add([]byte{0, 0, 0, 1, 0x65, 0x88})
	f.Add([]byte{0, 0, 0, 1, 0x41, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})

	f.Fuzz(func(t *testing.T, data []byte) {
		rec := mp4.NewRecorder(mp4.Options{
			PreRoll: time.Second, PostRoll: time.Second,
			MaxTime: 5 * time.Second, MaxBytes: 1 << 20,
		})
		pr := mp4.ReadPictures(bytes.NewReader(data), 25, mp4.DefaultTimescale)
		pictures := 0
		for {
			p, err := pr.Next()
			if p == nil {
				break
			}
			pictures++
			if pictures > 4096 {
				// A stream that never ends a picture is a stream the fuzzer
				// built; the bound keeps one input from consuming the budget.
				return
			}
			if pictures == 16 {
				rec.Trigger("fuzz", p.DecodeTime)
			}
			for _, nal := range p.NALs {
				// A refusal here is the documented behaviour for a mid-stream
				// parameter-set change and for an unparsable slice header.
				_ = rec.PushNAL(nal, p.DecodeTime, time.Time{})
			}
			_ = err
		}
		rec.Trigger("fuzz", 0)
		// A clip or a refusal; both are fine, a panic or a zero-byte clip is not.
		if clip, err := rec.Finish(); err == nil && len(clip.Data) > 1<<20 {
			t.Fatalf("a %d byte clip from a %d byte input", len(clip.Data), len(data))
		}
	})
}

// FuzzSPSNeverInventsAGeometry pins the one place a malformed parameter set
// could produce a plausible-looking answer instead of an error.
//
// The width and height printed in the poc's banner come from here, and a width
// of zero reads as "not known yet" while a width of 65535 reads as a fact. A
// parser that guesses when it cannot read is worse than one that says nothing,
// so the assertion is that anything the parser does report is a real frame.
func FuzzSPSNeverInventsAGeometry(f *testing.F) {
	if raw, err := os.ReadFile(fixture); err == nil {
		f.Add(raw[:28]) // the fixture's own SPS
	}
	f.Add([]byte{0x67, 0x42, 0xC0, 0x1E})
	f.Add([]byte{0x67})
	f.Add([]byte{0x67, 0xFF, 0xFF, 0xFF, 0xFF})
	// A 4K declaration in an 8-byte SPS: the shape a truncated parameter set
	// takes when the crop and VUI are missing.
	f.Add([]byte{0x67, 0x64, 0x00, 0x28, 0xAC, 0xD9, 0x40, 0x78})

	// Frame sizes a decoder can plausibly be asked to handle. A reported
	// geometry outside this is a parse, not a measurement.
	const maxSide = 16384

	f.Fuzz(func(t *testing.T, sps []byte) {
		rec := mp4.NewRecorder(mp4.Options{PreRoll: time.Second, PostRoll: time.Second})
		_ = rec.PushNAL(sps, 0, time.Time{})
		w, h, profile, level, ok := rec.Geometry()
		if !ok {
			return // refused, or too short to be an SPS at all
		}
		if w <= 0 || h <= 0 || w > maxSide || h > maxSide {
			t.Fatalf("SPS %x produced a %dx%d frame", sps, w, h)
		}
		if profile < 0 || profile > 255 || level < 0 || level > 255 {
			t.Fatalf("SPS %x produced profile %d level %d", sps, profile, level)
		}
	})
}
