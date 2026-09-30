package source

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestParseRejectsLoopGivenAURL(t *testing.T) {
	for _, arg := range []string{
		"https://example.com/a.m3u8",
		"rtsp://camera/stream",
		"HTTP://example.com/a.mp4",
	} {
		if _, err := Parse("loop", arg); err == nil {
			t.Fatalf("loop accepted %q: a live feed must never be labelled a recorded loop", arg)
		} else if !strings.Contains(err.Error(), "kind=loop") {
			t.Fatalf("error for %q does not name the mismatch: %v", arg, err)
		}
	}
}

func TestParseRejectsRTSPWithoutAnRTSPURL(t *testing.T) {
	_, err := Parse("rtsp", "https://example.com/a.m3u8")
	if err == nil || !strings.Contains(err.Error(), "kind=rtsp") {
		t.Fatalf("rtsp accepted an http URL: %v", err)
	}
}

func TestParseAcceptsTheThreeKinds(t *testing.T) {
	cases := map[string]Spec{
		`testdata\car.mp4`:    {Kind: KindLoop, Arg: `testdata\car.mp4`},
		"rtsp://c/live":       {Kind: KindRTSP, Arg: "rtsp://c/live"},
		"rtsps://c/live":      {Kind: KindRTSP, Arg: "rtsps://c/live"},
		"https://h/clip.m3u8": {Kind: KindURL, Arg: "https://h/clip.m3u8"},
	}
	for arg, want := range cases {
		got, err := Parse(string(want.Kind), arg)
		if err != nil {
			t.Fatalf("Parse(%s, %s): %v", want.Kind, arg, err)
		}
		if got != want {
			t.Fatalf("Parse(%s, %s) = %+v, want %+v", want.Kind, arg, got, want)
		}
	}
}

func TestParseRejectsUnknownKindAndEmptyArg(t *testing.T) {
	if _, err := Parse("webcam", "x"); err == nil {
		t.Fatal("unknown kind accepted")
	}
	if _, err := Parse("loop", "  "); err == nil {
		t.Fatal("empty argument accepted")
	}
}

// TestLoopArgsProduceAnActualLoop: a "loop" that plays once is not a loop, and
// a 7 day run over a 20 second clip would be a run over nothing.
func TestLoopArgsProduceAnActualLoop(t *testing.T) {
	s, _ := Parse("loop", `testdata\car.mp4`)
	args := strings.Join(s.InputArgs(), " ")
	if !strings.Contains(args, "-stream_loop -1") {
		t.Fatalf("loop args do not loop: %s", args)
	}
	if !strings.Contains(args, "-re") {
		t.Fatalf("loop args do not pace to real time: %s", args)
	}
}

// TestSingleInputOnly: exactly one reader means exactly one -i. Two inputs
// would be a second reader and would reintroduce the timestamp drift the
// architecture exists to prevent.
func TestSingleInputOnly(t *testing.T) {
	for _, k := range []string{"loop", "rtsp", "url"} {
		arg := `f.mp4`
		switch k {
		case "rtsp":
			arg = "rtsp://c/s"
		case "url":
			arg = "https://h/x.m3u8"
		}
		s, err := Parse(k, arg)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, a := range s.InputArgs() {
			if a == "-i" {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("%s produced %d -i arguments, want 1: %v", k, n, s.InputArgs())
		}
	}
}

// TestTheDecoderSymbolIsFoundInTheFlagColumnFormat: ffmpeg's -decoders output
// is a capability column followed by the symbol, and the column's LENGTH varies
// with the build — "VFS..D" is seven characters on a build with frame-level
// multithreading and "V....." is six without it. A length check finds h264 on
// one machine and not on another, which is a capability table that lies about
// the machine it ran on. The shape is checked instead.
func TestTheDecoderSymbolIsFoundInTheFlagColumnFormat(t *testing.T) {
	// The first two lines are the real output of ffmpeg 8.1.1 on Windows.
	const real = `Decoders:
 VFS..D h264                 H.264 / AVC / MPEG-4 AVC / MPEG-4 part 10
 V....D h264_qsv             H264 video (Intel Quick Sync Video acceleration) (codec h264)
 V..... h264_cuvid           Nvidia CUVID H264 decoder (codec h264)
 VS... libx264              libx264 H.264 / AVC / MPEG-4 AVC / H.264
 V..... a64multi             Multicolor charset for Commodore 64
 A..... aac                  AAC (Advanced Audio Coding)
`
	for _, sym := range []string{"h264", "h264_qsv", "h264_cuvid", "libx264", "aac"} {
		if got := decoderItem(sym, real, sym); got.State != Present {
			t.Errorf("%s reported %s from real -decoders output: %s", sym, got.State, got.Reason)
		}
	}
	got := decoderItem("h264 decoder", real, "h264")
	if !strings.Contains(got.Value, "H.264 / AVC") {
		t.Fatalf("the evidence line is not the decoder's own line: %q", got.Value)
	}
	// A decoder this build does not have is still ABSENT.
	if missing := decoderItem("h264_videotoolbox", real, "h264_videotoolbox"); missing.State != Absent {
		t.Fatalf("an absent decoder reported %s", missing.State)
	}
	// The legend rows are not decoders.
	if dup := decoderItem("D", real, "D"); dup.State == Present {
		t.Fatal("a character from the capability column was read as a decoder symbol")
	}
}

func TestTheFlagColumnShapeIsCheckedNotTheLength(t *testing.T) {
	for _, col := range []string{"V.....", "VFS..D", "VS...", "A.....", "S.....", ".F....", "..C..."} {
		if !isFlagColumn(col) {
			t.Errorf("%q is a real capability column and was not recognised", col)
		}
	}
	for _, notCol := range []string{"h264", "libx264", "h264_cuvid", "dec", "VSX", "video", "VSX..d", "libvpx"} {
		if isFlagColumn(notCol) {
			t.Errorf("%q is a decoder name and was eaten as a capability column", notCol)
		}
	}
}

func TestHwAccelListDropsTheHeaderLine(t *testing.T) {
	// ffmpeg -hwaccels prints a human sentence first. Reporting it as a method
	// puts "Hardware acceleration methods:" into a table of methods.
	const real = "Hardware acceleration methods:\ncuda\ndxva2\nqsv\nvaapi\nd3d11va\n"
	got := hwAccelList(real)
	want := []string{"cuda", "dxva2", "qsv", "vaapi", "d3d11va"}
	if len(got) != len(want) {
		t.Fatalf("hwAccelList = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("hwAccelList[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if len(hwAccelList("")) != 0 {
		t.Fatal("empty output produced methods")
	}
}

func TestRTSPPinsTCPTransport(t *testing.T) {
	// The default may pick UDP, which a corporate network drops silently. A
	// silently dropped source is a hang, not a gap.
	s, _ := Parse("rtsp", "rtsp://c/s")
	args := strings.Join(s.InputArgs(), " ")
	if !strings.Contains(args, "-rtsp_transport tcp") {
		t.Fatalf("rtsp transport is not pinned to tcp: %s", args)
	}
}

func TestLabelIsDerivedNotComposed(t *testing.T) {
	loop, _ := Parse("loop", "f.mp4")
	if !strings.Contains(loop.Label(), "no live camera") {
		t.Fatalf("loop label does not disclaim a live camera: %q", loop.Label())
	}
	rtsp, _ := Parse("rtsp", "rtsp://c/s")
	if strings.Contains(rtsp.Label(), "no live camera") {
		t.Fatalf("rtsp label claims no live camera: %q", rtsp.Label())
	}
}

// --- probe ------------------------------------------------------------------

type fakeRunner struct {
	responses map[string]Result
	calls     [][]string
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) Result {
	f.calls = append(f.calls, append([]string{name}, args...))
	key := name + " " + strings.Join(args, " ")
	if r, ok := f.responses[key]; ok {
		return r
	}
	// Longest matching prefix, so the tests do not have to spell every flag.
	best := ""
	for k := range f.responses {
		if strings.HasPrefix(key, k) && len(k) > len(best) {
			best = k
		}
	}
	if best != "" {
		return f.responses[best]
	}
	return Result{ExitCode: 1, Stderr: "unmocked: " + key}
}

const ffprobeOK = `{
  "streams": [
    {"index":0,"codec_type":"video","codec_name":"h264","width":1280,"height":720,
     "avg_frame_rate":"30000/1001","r_frame_rate":"30/1","bit_rate":"1048576",
     "duration":"8.0","pix_fmt":"yuv420p"},
    {"index":1,"codec_type":"audio","codec_name":"aac"}
  ],
  "format": {"bit_rate":"1100000"}
}`

const decodersOK = `Decoders:
 V..... h264                 H.264 / AVC / MPEG-4 AVC
 V..... h264_cuvid            Nvidia CUVID H.264 decoder
 A..... aac                   AAC`

func TestProbeMeasuresBitRateFromTheStream(t *testing.T) {
	fr := &fakeRunner{responses: map[string]Result{
		"ffprobe -v error":                        {Stdout: ffprobeOK},
		"ffmpeg -hide_banner -decoders":           {Stdout: decodersOK},
		"ffmpeg -hide_banner -hwaccels":           {Stdout: "cuda\ndxva2\nqsv\n"},
		"ffmpeg -hide_banner -nostdin -benchmark": {Stdout: "bench\n"},
	}}
	p := Prober{Runner: fr, FFmpeg: "ffmpeg", FFprobe: "ffprobe", Segments: 60, Days: 7}
	spec, _ := Parse("loop", `testdata\car.mp4`)

	rep, err := p.Probe(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if rep.BitRateBps != 1048576 {
		t.Fatalf("bit rate = %d, want the measured stream value 1048576", rep.BitRateBps)
	}
	if rep.Disk.Assumed {
		t.Fatal("a declared bit rate must not be recorded as an assumption")
	}
	// 1048576/8 bytes/s * 86400 s/day * 7 days
	want := int64(1048576/8) * 86400 * 7
	if rep.Disk.TotalBytes != want {
		t.Fatalf("total bytes = %d, want %d", rep.Disk.TotalBytes, want)
	}
	if rep.VideoStream == nil || rep.VideoStream.Codec != "h264" {
		t.Fatalf("video stream = %+v", rep.VideoStream)
	}
	if len(rep.Streams) != 2 {
		t.Fatalf("expected the audio stream to be reported too, got %d streams", len(rep.Streams))
	}
}

func TestUndeclaredBitRateIsRecordedAsAnAssumption(t *testing.T) {
	// A live rtsp source commonly declares no bit rate. Projecting a disk
	// budget from a guess is fine; presenting the guess as a measurement is not.
	ff := `{"streams":[{"index":0,"codec_type":"video","codec_name":"h264","width":1920,"height":1080,"avg_frame_rate":"25/1"}],"format":{}}`
	fr := &fakeRunner{responses: map[string]Result{
		"ffprobe -v error":              {Stdout: ff},
		"ffmpeg -hide_banner -decoders": {Stdout: decodersOK},
		"ffmpeg -hide_banner -hwaccels": {Stdout: "cuda\n"},
		"ffmpeg -hide_banner -nostdin":  {Stdout: "bench\n"},
	}}
	p := Prober{Runner: fr, FFmpeg: "ffmpeg", FFprobe: "ffprobe", Segments: 60, Days: 7}
	spec, _ := Parse("rtsp", "rtsp://c/s")

	rep, err := p.Probe(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Disk.Assumed {
		t.Fatal("an undeclared bit rate was projected as a measurement")
	}
	if !strings.Contains(rep.Disk.Note, "assumption") {
		t.Fatalf("note does not say it is an assumption: %q", rep.Disk.Note)
	}
	if !strings.Contains(rep.BitRateSrc, "ASSUMED") {
		t.Fatalf("bit rate source does not say ASSUMED: %q", rep.BitRateSrc)
	}
}

func TestMissingCuvidIsReportedAbsentWithAReason(t *testing.T) {
	fr := &fakeRunner{responses: map[string]Result{
		"ffprobe -v error":              {Stdout: ffprobeOK},
		"ffmpeg -hide_banner -decoders": {Stdout: "Decoders:\n V..... h264   H.264 / AVC"},
		"ffmpeg -hide_banner -hwaccels": {Stdout: ""},
		"ffmpeg -hide_banner -nostdin":  {Stdout: ""},
	}}
	p := Prober{Runner: fr, FFmpeg: "ffmpeg", FFprobe: "ffprobe"}
	rep, err := p.Probe(context.Background(), mustParseLoop(t))
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Item{}
	for _, it := range rep.Items {
		byName[it.Name] = it
	}
	cuvid, ok := byName["h264_cuvid (NVIDIA NVDEC)"]
	if !ok {
		t.Fatal("no cuvid item in the capability table")
	}
	if cuvid.State != Absent {
		t.Fatalf("a build without cuvid reported it as %q", cuvid.State)
	}
	if cuvid.Reason == "" {
		t.Fatal("an ABSENT item with no reason: the plan requires the reason be visible")
	}
	if _, ok := byName["h264 decoder"]; !ok {
		t.Fatal("no h264 decoder item")
	}
	hw := byName["hardware acceleration"]
	if hw.State != Absent || hw.Reason == "" {
		t.Fatalf("empty -hwaccels output was not reported as absent: %+v", hw)
	}
}

func TestAbsentGStreamerIsNotADefect(t *testing.T) {
	fr := &fakeRunner{responses: map[string]Result{
		"ffprobe -v error":              {Stdout: ffprobeOK},
		"ffmpeg -hide_banner -decoders": {Stdout: decodersOK},
		"ffmpeg -hide_banner -hwaccels": {Stdout: "cuda\n"},
		"ffmpeg -hide_banner -nostdin":  {Stdout: ""},
		"gst-inspect-1.0":               {Err: errNotFound{}},
	}}
	p := Prober{Runner: fr, FFmpeg: "ffmpeg", FFprobe: "ffprobe"}
	rep, err := p.Probe(context.Background(), mustParseLoop(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range rep.Items {
		if it.Name == "GStreamer (documented substitute for ffmpeg)" {
			if it.State != Absent {
				t.Fatalf("gstreamer state = %q", it.State)
			}
			if !strings.Contains(it.Reason, "nothing is missing") {
				t.Fatalf("reason should say nothing is missing: %q", it.Reason)
			}
			return
		}
	}
	t.Fatal("no GStreamer item")
}

type errNotFound struct{}

func (errNotFound) Error() string {
	return `exec: "gst-inspect-1.0": executable file not found in %PATH%`
}

func TestMissingFFprobeIsAnErrorNotAPartialReport(t *testing.T) {
	fr := &fakeRunner{responses: map[string]Result{
		"ffprobe -v error": {Err: errNotFound{}},
	}}
	p := Prober{Runner: fr, FFmpeg: "ffmpeg", FFprobe: "ffprobe"}
	rep, err := p.Probe(context.Background(), mustParseLoop(t))
	if err == nil {
		t.Fatal("a missing ffprobe produced a report")
	}
	if !strings.Contains(err.Error(), "not on PATH") {
		t.Fatalf("error does not say what is missing: %v", err)
	}
	if len(rep.Items) == 0 || rep.Items[0].State != Absent {
		t.Fatalf("the failure was not recorded as an ABSENT item: %+v", rep.Items)
	}
}

func TestUnusableFFprobeOutputIsRejectedNotGuessed(t *testing.T) {
	fr := &fakeRunner{responses: map[string]Result{
		"ffprobe -v error": {Stdout: "not json at all"},
	}}
	p := Prober{Runner: fr, FFmpeg: "ffmpeg", FFprobe: "ffprobe"}
	_, err := p.Probe(context.Background(), mustParseLoop(t))
	if err == nil || !strings.Contains(err.Error(), "not usable JSON") {
		t.Fatalf("unparseable probe output was accepted: %v", err)
	}
}

func TestDecodeSlowerThanRealTimeIsNotReachable(t *testing.T) {
	slow := &slowRunner{delay: 6 * time.Second}
	p := Prober{Runner: slow, FFmpeg: "ffmpeg", FFprobe: "ffprobe"}
	rep, err := p.Probe(context.Background(), mustParseLoop(t))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Decode.Reachable {
		t.Fatalf("a 10 second decode that took %.1fs was called reachable", rep.Decode.WallSec)
	}
	if rep.Decode.Reason == "" {
		t.Fatal("an unreachable decode with no reason")
	}
}

type slowRunner struct {
	delay time.Duration
	Slow  time.Duration
}

func (s *slowRunner) Run(_ context.Context, name string, _ ...string) Result {
	switch {
	case strings.HasPrefix(name, "ffprobe"):
		return Result{Stdout: ffprobeOK}
	case strings.HasPrefix(name, "ffmpeg") && len(name) == 6:
		// the -decoders / -hwaccels forms
		return Result{Stdout: decodersOK}
	case strings.HasPrefix(name, "gst"):
		return Result{Err: errNotFound{}}
	}
	time.Sleep(s.Slow)
	return Result{Stdout: ""}
}

func mustParseLoop(t *testing.T) Spec {
	t.Helper()
	s, err := Parse("loop", `testdata\car.mp4`)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
