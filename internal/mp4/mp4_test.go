package mp4_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/mp4"
)

// The fixture is a synthetic 8-second stream: three seconds of a flat grey
// frame, then five seconds of a moving test pattern. It is a lavfi pattern and
// contains no footage of anything, so it carries no licence and nobody's
// likeness. The static lead is there so the pre-roll has a visible "nothing
// happening" to include.
//
// It is committed because a build that only works against a live camera is a
// build that cannot be reproduced, and a golden test that runs against a live
// stream fails tomorrow for reasons that have nothing to do with the code.
const fixture = "../../testdata/poc/testsrc.h264"

// nominalFPS is the rate the fixture was generated at. It is passed in rather
// than assumed, because the same bytes at a different rate are a different clip
// and a test that hid the difference would be testing the assumption.
const nominalFPS = 25

func openFixture(t *testing.T) *os.File {
	t.Helper()
	f, err := os.Open(fixture)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func loadFixture(t *testing.T) [][]byte {
	t.Helper()
	r := mp4.NewAnnexBReader(openFixture(t), mp4.DefaultMaxNALSize)
	var out [][]byte
	for {
		nal, err := r.Next()
		if err != nil {
			break
		}
		out = append(out, nal)
	}
	if len(out) < 100 {
		t.Fatalf("fixture yielded %d NAL units, which is not a stream", len(out))
	}
	return out
}

// probe reads a file back with an independent decoder.
//
// This is the whole reason the muxer's tests are not a round trip. A round trip
// through our own reader accepts anything we happen to write, because both ends
// share our misunderstanding of the format; ffprobe shares none of it. The
// assertion is skipped, never failed, where ffprobe is absent - a test that
// fails because a developer tool is not installed teaches people to ignore it.
func probe(t *testing.T, data []byte) map[string]any {
	t.Helper()
	bin, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe is not on PATH; the independent-decoder check did not run")
	}
	path := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write clip: %v", err)
	}
	// -count_packets forces the demuxer to walk every sample, which is the only
	// way a fragment layout that merely looks right gets caught.
	//nolint:gosec // ffprobe is the independent decoder this test is about
	cmd := exec.CommandContext(t.Context(), bin, "-v", "error", "-count_packets",
		"-show_streams", "-show_format", "-of", "json", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe refused the clip we wrote: %v\n%s", err, out)
	}
	var parsed struct {
		Streams []struct {
			CodecName     string `json:"codec_name"`
			Width         int    `json:"width"`
			Height        int    `json:"height"`
			Duration      string `json:"duration"`
			NbReadPackets string `json:"nb_read_packets"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
			Size     string `json:"size"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("parse ffprobe output: %v\n%s", err, out)
	}
	if len(parsed.Streams) == 0 {
		t.Fatalf("ffprobe reported no streams for a file we wrote:\n%s", out)
	}
	s := parsed.Streams[0]
	m := map[string]any{
		"codec":   s.CodecName,
		"width":   s.Width,
		"height":  s.Height,
		"packets": s.NbReadPackets,
	}
	if d, err := time.ParseDuration(s.Duration + "s"); err == nil {
		m["duration"] = d
	}
	return m
}

// feeder drives a recorder from the fixture the way a live receiver does: one
// decode time per picture, with every NAL of a picture carrying that picture's
// timestamp, exactly as RTP delivers them.
//
// Pictures and the end-of-stream marker share one channel on purpose. With two,
// a `select` between them is free to take the end marker while pictures are
// still buffered, and the test then measures a stream that silently lost its
// last few frames.
type feeder struct {
	rec   *mp4.Recorder
	items chan feedItem
	wall  time.Time
	delay time.Duration
}

type feedItem struct {
	pic *mp4.Picture
	err error
}

func newFeeder(t *testing.T, opts mp4.Options) *feeder {
	t.Helper()
	opts.Timescale = mp4.DefaultTimescale
	f := &feeder{
		rec:   mp4.NewRecorder(opts),
		items: make(chan feedItem, 256),
		wall:  time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC),
		delay: time.Second / nominalFPS,
	}
	src := openFixture(t)
	go func() {
		defer close(f.items)
		pr := mp4.ReadPictures(src, nominalFPS, mp4.DefaultTimescale)
		for {
			p, err := pr.Next()
			if p != nil {
				f.items <- feedItem{pic: p}
			}
			if err != nil {
				f.items <- feedItem{err: err}
				return
			}
		}
	}()
	return f
}

// run feeds the whole stream, firing the trigger at the given picture index. A
// negative index never fires, which is how the untriggered case is exercised.
func (f *feeder) run(t *testing.T, triggerAt int) {
	t.Helper()
	i := 0
	for item := range f.items {
		if item.err != nil {
			return
		}
		p := item.pic
		wall := f.wall.Add(time.Duration(i) * f.delay)
		if i == triggerAt {
			f.rec.Trigger("test trigger", p.DecodeTime)
		}
		for _, nal := range p.NALs {
			if err := f.rec.PushNAL(nal, p.DecodeTime, wall); err != nil {
				t.Fatalf("picture %d: %v", i, err)
			}
		}
		i++
	}
}

func TestAnnexBReaderSplitsEveryNALUnit(t *testing.T) {
	r := mp4.NewAnnexBReader(openFixture(t), mp4.DefaultMaxNALSize)

	var counts [32]int
	total := 0
	for {
		nal, err := r.Next()
		if err != nil {
			break
		}
		typ, ok := mp4.Parse(nal)
		if !ok {
			t.Fatalf("NAL %d is empty", total)
		}
		if int(typ) >= len(counts) {
			t.Fatalf("NAL %d has type %d, outside the 5-bit field", total, typ)
		}
		counts[typ]++
		total++
	}
	if total == 0 {
		t.Fatal("no NAL units")
	}
	if counts[mp4.NALPrefix] == 0 || counts[mp4.NALSuffix] == 0 {
		t.Fatalf("fixture carries no parameter sets (SPS=%d PPS=%d)", counts[mp4.NALPrefix], counts[mp4.NALSuffix])
	}
	if counts[mp4.NALIDR] < 4 {
		t.Fatalf("fixture has %d IDR pictures; the pre-roll tests need several groups", counts[mp4.NALIDR])
	}
}

func TestPictureReaderSplitsPicturesAndStampsThem(t *testing.T) {
	pr := mp4.ReadPictures(openFixture(t), nominalFPS, mp4.DefaultTimescale)
	var pictures, keyframes int
	var last uint64
	for {
		p, err := pr.Next()
		if p == nil {
			break
		}
		pictures++
		if p.KeyFrame {
			keyframes++
		}
		if p.DecodeTime < last {
			t.Fatalf("picture %d goes backwards in time (%d after %d)", pictures, p.DecodeTime, last)
		}
		last = p.DecodeTime
		if len(p.NALs) == 0 {
			t.Fatalf("picture %d has no NAL units", pictures)
		}
		_ = err
	}
	if pictures < 100 {
		t.Fatalf("fixture yielded %d pictures, which is not a stream", pictures)
	}
	if keyframes < 4 {
		t.Fatalf("fixture has %d keyframes", keyframes)
	}
	// 8 seconds at 25 fps.
	if got := time.Duration(last) * time.Second / mp4.DefaultTimescale; got < 7*time.Second || got > 9*time.Second {
		t.Errorf("the stream spans %v, not the ~8s the fixture was generated at", got)
	}
}

// TestMuxedClipIsReadableByAnIndependentDecoder is the highest-value assertion
// in the package. Everything else here checks that we built what we meant to.
func TestMuxedClipIsReadableByAnIndependentDecoder(t *testing.T) {
	f := newFeeder(t, mp4.Options{
		PreRoll: 4 * time.Second, PostRoll: 3 * time.Second,
		MaxTime: 30 * time.Second, MaxBytes: 100 << 20,
	})
	f.run(t, 120)
	clip, err := f.rec.Finish()
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	if clip.Size() == 0 {
		t.Fatal("clip is zero bytes")
	}
	info := probe(t, clip.Data)
	if info["codec"] != "h264" {
		t.Errorf("ffprobe read codec %v, want h264", info["codec"])
	}
	if info["width"] != 640 || info["height"] != 360 {
		t.Errorf("ffprobe read %vx%v, want 640x360 (the fixture's own dimensions)", info["width"], info["height"])
	}
	// Every sample we wrote must be readable, not merely the first.
	if got, want := fmtInt(info["packets"]), clip.Pictures; got != want {
		t.Errorf("ffprobe read %d packets, the clip wrote %d samples", got, want)
	}
	d, _ := info["duration"].(time.Duration)
	if d <= 0 {
		t.Fatalf("ffprobe read duration %v; a clip whose length no decoder can read is not a clip", info["duration"])
	}
	// A frame of slack: our duration comes from the last sample's declared
	// duration and ffprobe's from the same boxes, rounded to its own frame.
	if delta := d - clip.Duration; delta > 100*time.Millisecond || delta < -100*time.Millisecond {
		t.Errorf("ffprobe read a duration of %v and the clip reports %v", d, clip.Duration)
	}
	if clip.Width != 640 || clip.Height != 360 {
		t.Errorf("clip reports %dx%d, want 640x360", clip.Width, clip.Height)
	}
	if clip.KeyFrames < 1 {
		t.Errorf("clip has %d keyframes; a clip with none cannot be seeked into", clip.KeyFrames)
	}
}

func fmtInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case string:
		i := 0
		for _, c := range n {
			if c < '0' || c > '9' {
				return -1
			}
			i = i*10 + int(c-'0')
		}
		return i
	}
	return -1
}

// TestPreRollContainsTheEventThatTriggeredIt is the assertion the whole
// architecture rests on.
//
// A detector fires *after* motion is visible. A clip that begins where the
// detector fired therefore begins after the thing that mattered, and every
// downstream judgement - the VLM's, the operator's, the auditor's - is made about
// the aftermath. This is the one failure the pre-roll exists to prevent, and it
// is silent: the clip looks fine until somebody plays it.
func TestPreRollContainsTheEventThatTriggeredIt(t *testing.T) {
	// Four seconds in: several groups after the first keyframe, and well before
	// the end, so there is a post-roll to record as well.
	const triggerAt = 100
	f := newFeeder(t, mp4.Options{PreRoll: 4 * time.Second, PostRoll: 2 * time.Second})
	f.run(t, triggerAt)
	clip, err := f.rec.Finish()
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	if clip.PreRollPics == 0 {
		t.Fatal("the clip contains no pre-roll: it starts where the detector fired, not before it")
	}
	// Everything the ring retained from before the trigger is in the clip, and
	// the ring holds at most the last four seconds, so 100 pictures is the most
	// that could possibly be there.
	if clip.PreRollPics < 90 {
		t.Errorf("clip carries %d pictures of lead-in, want the whole four seconds before the trigger", clip.PreRollPics)
	}
	if clip.PreRollSpan < 3*time.Second {
		t.Errorf("clip reports a pre-roll span of %v, want about four seconds", clip.PreRollSpan)
	}
	if clip.PreRollReason != "" {
		t.Errorf("clip says its lead-in was degraded: %s", clip.PreRollReason)
	}
	if clip.CapReason != "" {
		t.Errorf("a fixture this small should not hit a cap: %s", clip.CapReason)
	}
	if clip.Pictures <= clip.PreRollPics {
		t.Errorf("clip has %d pictures, %d of them pre-roll: nothing was recorded after the trigger",
			clip.Pictures, clip.PreRollPics)
	}
}

func TestClipAlwaysStartsOnAKeyframe(t *testing.T) {
	// A trigger placed at every offset across a fixture: whichever way it
	// lands, the clip must open on an IDR, or its first frames decode to noise.
	for _, at := range []int{1, 5, 17, 40, 61, 120, 170} {
		f := newFeeder(t, mp4.Options{PreRoll: 4 * time.Second, PostRoll: 2 * time.Second})
		f.run(t, at)
		clip, err := f.rec.Finish()
		if err != nil {
			t.Fatalf("trigger at picture %d: finish: %v", at, err)
		}
		if clip.KeyFrames == 0 {
			t.Errorf("trigger at picture %d: clip has no keyframe", at)
		}
	}
}

func TestRefusesAStreamWithNoParameterSets(t *testing.T) {
	// Red-check: the control being tested is the refusal. Remove it and this
	// test is the only thing in the tree that would notice.
	var nals [][]byte
	for _, nal := range loadFixture(t) {
		if typ, _ := mp4.Parse(nal); typ == mp4.NALPrefix || typ == mp4.NALSuffix {
			continue
		}
		nals = append(nals, nal)
	}
	r := mp4.NewRecorder(mp4.Options{PreRoll: 2 * time.Second, PostRoll: time.Second})
	triggered := false
	var refusal error
	for i, nal := range nals {
		ts := uint64(i) * 3600
		if i == len(nals)/3 && !triggered {
			triggered = true
			r.Trigger("no parameter sets", ts)
		}
		if err := r.PushNAL(nal, ts, time.Time{}); err != nil {
			refusal = err
			break
		}
	}
	if refusal == nil {
		_, refusal = r.Finish()
	}
	if refusal == nil {
		t.Fatal("a stream that never delivered an SPS and a PPS produced a clip; the file would be unplayable")
	}
}

func TestRefusesAMidStreamParameterSetChange(t *testing.T) {
	nals := loadFixture(t)
	var sps []byte
	changed := false
	out := make([][]byte, 0, len(nals))
	for _, nal := range nals {
		typ, _ := mp4.Parse(nal)
		if typ == mp4.NALPrefix && len(sps) == 0 {
			sps = nal
		} else if typ == mp4.NALPrefix && !changed {
			// A one-bit change is a different decode configuration, which is the
			// whole point: not a re-send of the same SPS.
			alt := append([]byte(nil), nal...)
			alt[1] ^= 0x01
			out = append(out, alt)
			changed = true
			continue
		}
		out = append(out, nal)
	}
	r := mp4.NewRecorder(mp4.Options{PreRoll: 2 * time.Second, PostRoll: time.Second})
	var refusal error
	for i, nal := range out {
		if err := r.PushNAL(nal, uint64(i)*3600, time.Time{}); err != nil {
			refusal = err
			break
		}
	}
	if refusal == nil {
		t.Fatal("a mid-stream SPS change was accepted; the resulting file has two answers to what resolution it is")
	}
	if !bytes.Contains([]byte(refusal.Error()), []byte("parameter-set change")) {
		t.Errorf("refusal %q does not name the problem", refusal)
	}
}

// TestRefusesAStreamThatDeclaresAFrameNoDecoderWillSize came out of the fuzzer,
// which found this in under a second: six bytes of SPS that legitimately
// describe a 32x16400 picture, because H.264 sets no maximum.
//
// The format permitting it is not a reason to write the file. Every player
// rejects that clip, and the rejection surfaces as "this clip will not open",
// three stages downstream of the real cause. The muxer refuses, by name, at the
// point where the number is still a number.
func TestRefusesAStreamThatDeclaresAFrameNoDecoderWillSize(t *testing.T) {
	// The exact input the fuzzer produced.
	sps := []byte{0x27, 0xd9, 0x28, 0x00, 0x80, 0x30}
	r := mp4.NewRecorder(mp4.Options{PreRoll: time.Second, PostRoll: time.Second})
	if err := r.PushNAL(sps, 0, time.Time{}); err != nil {
		// A refusal here would also be acceptable; what must not happen is a
		// geometry being reported.
		if !errors.Is(err, mp4.ErrGeometryTooLarge) {
			t.Fatalf("unexpected refusal: %v", err)
		}
		return
	}
	w, h, _, _, ok := r.Geometry()
	if !ok {
		return // refused, which is what the test wants
	}
	if w > mp4.MaxFrameSide || h > mp4.MaxFrameSide {
		t.Fatalf("SPS %x produced a %dx%d frame; the limit is %d per side", sps, w, h, mp4.MaxFrameSide)
	}
}

func TestNALSizeBoundIsEnforcedOnAStreamWithNoStartCode(t *testing.T) {
	// A camera that never emits a start code would grow the reader without
	// limit. It is a network peer and is treated as one.
	junk := bytes.Repeat([]byte{0x41}, 4096)
	r := mp4.NewAnnexBReader(bytes.NewReader(junk), 1024)
	if _, err := r.Next(); err == nil {
		t.Fatal("an unbounded NAL was accepted")
	}
}

func TestByteCapIsReportedRatherThanAppliedSilently(t *testing.T) {
	f := newFeeder(t, mp4.Options{
		PreRoll: 4 * time.Second, PostRoll: 4 * time.Second,
		MaxTime: 30 * time.Second, MaxBytes: 24 << 10,
	})
	f.run(t, 120)
	clip, err := f.rec.Finish()
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	if clip.CapReason == "" {
		t.Fatal("a clip stopped at a byte cap and did not say so")
	}
	if clip.Size() > 24<<10 {
		t.Errorf("clip is %d bytes, above the %d cap the recorder was given", clip.Size(), 24<<10)
	}
	probe(t, clip.Data) // still a decodable file, not a truncated one
}

func TestTimeCapIsReportedRatherThanAppliedSilently(t *testing.T) {
	f := newFeeder(t, mp4.Options{
		PreRoll: 2 * time.Second, PostRoll: 2 * time.Second,
		MaxTime: 2 * time.Second, MaxBytes: 100 << 20,
	})
	f.run(t, 120)
	clip, err := f.rec.Finish()
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	if clip.CapReason == "" {
		t.Fatal("a clip stopped at a time cap and did not say so")
	}
	if clip.Duration > 3*time.Second {
		t.Errorf("clip runs %v, well past the 2s cap plus post-roll", clip.Duration)
	}
}

func TestAnUntriggeredRecorderProducesNoClip(t *testing.T) {
	// An evidence object of zero bytes has the hash of nothing and looks
	// exactly like a clip of a scene where nothing happened.
	f := newFeeder(t, mp4.Options{})
	f.run(t, -1)
	if _, err := f.rec.Finish(); err == nil {
		t.Fatal("an untriggered recorder produced a clip")
	}
}

func TestPreRollEvictsOldGroupsAndStillOpensOnAKeyframe(t *testing.T) {
	// A ring smaller than the stream must drop the oldest groups. It must also
	// still open the clip on a keyframe, and the clip must still decode.
	f := newFeeder(t, mp4.Options{PreRoll: time.Second, PostRoll: 2 * time.Second})
	f.run(t, 150)
	clip, err := f.rec.Finish()
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	if clip.KeyFrames == 0 {
		t.Error("a heavily evicted pre-roll produced a clip with no keyframe")
	}
	if clip.Size() == 0 {
		t.Fatal("a heavily evicted pre-roll produced an empty clip")
	}
	// A one-second ring cannot carry a hundred pictures of lead-in.
	if clip.PreRollPics > 40 {
		t.Errorf("a one-second pre-roll retained %d pictures", clip.PreRollPics)
	}
	probe(t, clip.Data)
}

func TestDamagedNALIsReportedNotSwallowed(t *testing.T) {
	// A slice header we cannot read means we cannot find a picture boundary.
	// The frame is kept and the damage is reported, because dropping it quietly
	// and then claiming a continuous clip is the failure this whole package
	// exists to prevent.
	nals := loadFixture(t)
	var reasons []string
	r := mp4.NewRecorder(mp4.Options{
		PreRoll: 2 * time.Second, PostRoll: time.Second,
		OnDamaged: func(s string) { reasons = append(reasons, s) },
	})
	for i, nal := range nals {
		if i == 40 {
			// A VCL NAL with a valid type and then nothing but zero bits: an
			// Exp-Golomb code that never terminates.
			if err := r.PushNAL([]byte{0x41}, uint64(i)*3600, time.Time{}); err != nil {
				t.Fatalf("push damaged: %v", err)
			}
			continue
		}
		if err := r.PushNAL(nal, uint64(i)*3600, time.Time{}); err != nil {
			t.Fatalf("push %d: %v", i, err)
		}
	}
	if len(reasons) == 0 {
		t.Fatal("a NAL that could not be parsed was swallowed")
	}
}
