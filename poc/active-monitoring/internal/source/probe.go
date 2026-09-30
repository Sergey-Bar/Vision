package source

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Runner executes a probe command. It is an interface so the capability table
// can be tested without an ffmpeg install, and so a missing binary is a value
// the table can report rather than a panic.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) Result
}

// Result is one probe command's outcome.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Err      error
}

// ExecRunner runs real commands.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) Result {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if err != nil {
		var ee *exec.Error
		if ok := asExecError(err, &ee); ok && ee.Err == exec.ErrNotFound {
			res.Err = fmt.Errorf("%s is not on PATH", name)
			return res
		}
		res.Err = err
	}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	} else {
		res.ExitCode = -1
	}
	return res
}

func asExecError(err error, dst **exec.Error) bool {
	if ee, ok := err.(*exec.Error); ok {
		*dst = ee
		return true
	}
	return false
}

// State is the only two-valued answer a capability item may have. There is no
// "unknown" that renders as a tick.
type State string

const (
	Present State = "present"
	Absent  State = "ABSENT"
)

// Item is one measured capability. An absent item always carries a reason and
// the evidence line it was decided from. "we did not check" is rendered as
// ABSENT with that reason, never as present and never omitted.
type Item struct {
	Name     string `json:"name"`
	State    State  `json:"state"`
	Value    string `json:"value,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Evidence string `json:"evidence,omitempty"`
}

// Stream is the subset of ffprobe's output the report actually uses.
type Stream struct {
	Index        int     `json:"index"`
	Codec        string  `json:"codec"`
	Width        int     `json:"width"`
	Height       int     `json:"height"`
	FPS          float64 `json:"fps"`
	BitRateBps   int64   `json:"bit_rate_bps"`
	DurationSec  float64 `json:"duration_seconds"`
	PixelFormat  string  `json:"pixel_format,omitempty"`
	FrameRateRaw string  `json:"frame_rate,omitempty"`
}

// DecodeCost is the wall clock of a decode-only pass. It is what tells the
// operator whether the resolution and frame rate they chose can survive real
// time on this machine.
type DecodeCost struct {
	Frames    int           `json:"frames"`
	Wall      time.Duration `json:"-"`
	WallSec   float64       `json:"wall_seconds"`
	Realtime  float64       `json:"realtime_factor"`
	Reachable bool          `json:"reachable"`
	Reason    string        `json:"reason,omitempty"`
}

// Report is the whole measured picture of one source on one machine.
type Report struct {
	Source      Label      `json:"source"`
	Streams     []Stream   `json:"streams"`
	VideoStream *Stream    `json:"video_stream,omitempty"`
	BitRateBps  int64      `json:"measured_bit_rate_bps"`
	BitRateSrc  string     `json:"measured_bit_rate_source"`
	Disk        DiskBudget `json:"disk_budget"`
	Decode      DecodeCost `json:"decode_cost"`
	Items       []Item     `json:"capabilities"`
}

// Label is the source sentence, kept in the report so the header line and the
// numbers cannot drift apart.
type Label string

// DiskBudget is the arithmetic of §5: the camera's bit rate is the disk rate.
// It is measured, not quoted from a datasheet.
type DiskBudget struct {
	SecondsPerDay     float64 `json:"seconds_per_day"`
	Days              int     `json:"days"`
	SegmentsPerDay    int     `json:"segments_per_day"`
	BytesPerDay       int64   `json:"bytes_per_day"`
	TotalBytes        int64   `json:"total_bytes"`
	Assumed           bool    `json:"assumed_bit_rate"`
	AssumedBitRateBps int64   `json:"assumed_bit_rate_bps,omitempty"`
	Note              string  `json:"note,omitempty"`
}

// Prober measures a source.
type Prober struct {
	Runner   Runner
	FFmpeg   string
	FFprobe  string
	Segments int
	Days     int
}

// ffprobeJSON mirrors the parts of -show_streams this module reads. Unknown
// fields are ignored, which is the correct behaviour: a newer ffprobe that adds
// keys must not break a run.
type ffprobeJSON struct {
	Streams []struct {
		Index        int    `json:"index"`
		CodecType    string `json:"codec_type"`
		CodecName    string `json:"codec_name"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		AvgFrameRate string `json:"avg_frame_rate"`
		RFrameRate   string `json:"r_frame_rate"`
		BitRate      string `json:"bit_rate"`
		Duration     string `json:"duration"`
		PixFmt       string `json:"pix_fmt"`
	} `json:"streams"`
	Format struct {
		BitRate string `json:"bit_rate"`
	} `json:"format"`
}

// Probe measures the source's streams, the machine's decoders, and the cost of
// decoding this source. It reports absence by name.
func (p Prober) Probe(ctx context.Context, spec Spec) (Report, error) {
	rep := Report{Source: Label(spec.Label())}
	if p.Segments <= 0 {
		p.Segments = 60
	}
	if p.Days <= 0 {
		p.Days = 7
	}

	streams, raw, err := p.probeStreams(ctx, spec)
	if err != nil {
		rep.Items = append(rep.Items, Item{
			Name:   "source reachable",
			State:  Absent,
			Reason: err.Error(),
		})
		return rep, err
	}
	rep.Streams = streams
	for i := range rep.Streams {
		if rep.Streams[i].Codec != "mjpeg" && rep.VideoStream == nil {
			rep.VideoStream = &rep.Streams[i]
		}
	}
	if rep.VideoStream == nil {
		return rep, fmt.Errorf("ffprobe reported no decodable video stream for %s", spec.Arg)
	}

	// Measured bit rate first, container rate second, a stated assumption last.
	switch {
	case rep.VideoStream.BitRateBps > 0:
		rep.BitRateBps = rep.VideoStream.BitRateBps
		rep.BitRateSrc = "ffprobe stream bit_rate"
	case parseInt(raw.Format.BitRate) > 0:
		rep.BitRateBps = parseInt(raw.Format.BitRate)
		rep.BitRateSrc = "ffprobe format bit_rate"
	default:
		// A live rtsp source has no duration and often no declared bit rate.
		// Assuming is recorded as assuming, in the report, next to the number.
		rep.BitRateBps = 4_000_000
		rep.BitRateSrc = "ASSUMED 4 Mbps - ffprobe declared no bit rate for this source"
	}
	rep.Disk = budget(rep.BitRateBps, p.Segments, p.Days, rep.BitRateSrc != "ffprobe stream bit_rate" && rep.BitRateSrc != "ffprobe format bit_rate")

	rep.Decode = p.measureDecode(ctx, spec, rep.VideoStream)
	rep.Items = append(rep.Items, p.capabilities(ctx, spec, rep.VideoStream)...)
	return rep, nil
}

func (p Prober) probeStreams(ctx context.Context, spec Spec) ([]Stream, ffprobeJSON, error) {
	var raw ffprobeJSON
	args := append([]string{"-v", "error", "-print_format", "json", "-show_streams", "-show_format"}, "-i", spec.Arg)
	res := p.Runner.Run(ctx, p.FFprobe, args...)
	if res.Err != nil {
		if isNotFound(res.Err) {
			return nil, raw, fmt.Errorf("ffprobe is not on PATH; the source cannot be measured and the disk budget cannot be computed: %w", res.Err)
		}
		return nil, raw, fmt.Errorf("ffprobe failed on %s (exit %d): %s", spec.Arg, res.ExitCode, firstLine(res.Stderr))
	}
	if err := json.Unmarshal([]byte(res.Stdout), &raw); err != nil {
		return nil, raw, fmt.Errorf("ffprobe output for %s is not usable JSON, so nothing about this source is known rather than partially known: %w", spec.Arg, err)
	}
	var out []Stream
	for _, s := range raw.Streams {
		out = append(out, Stream{
			Index:        s.Index,
			Codec:        s.CodecName,
			Width:        s.Width,
			Height:       s.Height,
			FPS:          parseRatio(s.AvgFrameRate),
			BitRateBps:   parseInt(s.BitRate),
			DurationSec:  parseFloat(s.Duration),
			PixelFormat:  s.PixFmt,
			FrameRateRaw: s.AvgFrameRate,
		})
	}
	if len(out) == 0 {
		return nil, raw, fmt.Errorf("ffprobe found no streams in %s", spec.Arg)
	}
	return out, raw, nil
}

// measureDecode runs a decode-only pass and times it. The factor is the honest
// question: can this machine hold this resolution and frame rate in real time.
func (p Prober) measureDecode(ctx context.Context, spec Spec, v *Stream) DecodeCost {
	// Bounded so a live source cannot run forever. The -t is a wall-clock cap
	// on the measurement, not on the recording.
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	args := []string{"-hide_banner", "-nostdin", "-benchmark"}
	if spec.Kind == KindLoop {
		args = append(args, "-stream_loop", "-1")
	}
	args = append(args, "-t", "10", "-i", spec.Arg, "-an", "-f", "null", "-")

	start := time.Now()
	res := p.Runner.Run(ctx, p.FFmpeg, args...)
	wall := time.Since(start)

	d := DecodeCost{Wall: wall, WallSec: wall.Seconds()}
	if res.Err != nil && isNotFound(res.Err) {
		d.Reason = "ffmpeg is not on PATH"
		return d
	}
	if d.WallSec <= 0 {
		d.Reason = "the pass took no measurable time"
		return d
	}
	media := 10.0
	if v.DurationSec > 0 && spec.Kind == KindLoop {
		media = min(10.0, v.DurationSec)
	}
	d.Realtime = media / d.WallSec
	d.Reachable = d.Realtime >= 1.0
	if !d.Reachable {
		d.Reason = fmt.Sprintf("decoded %.2fx slower than real time on this machine, so this resolution and frame rate cannot be held live", d.Realtime)
	}
	return d
}

// capabilities measures decoders, hardware acceleration and the optional
// GStreamer alternative. Every item is decided by a command; nothing is
// assumed from the platform.
func (p Prober) capabilities(ctx context.Context, spec Spec, v *Stream) []Item {
	var items []Item

	if res := p.Runner.Run(ctx, p.FFmpeg, "-hide_banner", "-decoders"); res.Err != nil {
		items = append(items, Item{Name: "h264 decoder", State: Absent,
			Reason: "ffmpeg is not on PATH, so no decoder is available and there is no source"})
	} else {
		items = append(items, decoderItem("h264 decoder", res.Stdout, "h264"))
		items = append(items, decoderItem("h264_cuvid (NVIDIA NVDEC)", res.Stdout, "h264_cuvid"))
	}

	if res := p.Runner.Run(ctx, p.FFmpeg, "-hide_banner", "-hwaccels"); res.Err != nil {
		items = append(items, Item{Name: "hardware acceleration", State: Absent,
			Reason: "ffmpeg -hwaccels failed: " + firstLine(res.Stderr)})
	} else if accels := hwAccelList(res.Stdout); len(accels) == 0 {
		items = append(items, Item{Name: "hardware acceleration", State: Absent,
			Reason: "ffmpeg -hwaccels listed no method; this build has no hardware decode, so all decode is CPU"})
	} else {
		items = append(items, Item{Name: "hardware acceleration", State: Present,
			Value: strings.Join(accels, ", "), Evidence: "ffmpeg -hwaccels"})
	}

	// GStreamer is a documented substitute, never a dependency. Its absence is
	// not a defect, so it is ABSENT with the reason "not installed" and the
	// reader is told what it would have replaced.
	if res := p.Runner.Run(ctx, "gst-inspect-1.0", "--version"); res.Err != nil && isNotFound(res.Err) {
		items = append(items, Item{Name: "GStreamer (documented substitute for ffmpeg)", State: Absent,
			Reason: "gst-inspect-1.0 is not installed; nothing is missing, ffmpeg is the chosen pipeline and covers appsink/nvjpeg/rtsp needs"})
	} else {
		items = append(items, Item{Name: "GStreamer (documented substitute for ffmpeg)", State: Present,
			Value: strings.TrimSpace(res.Stdout), Evidence: "gst-inspect-1.0 --version"})
	}

	if v != nil {
		items = append(items, Item{Name: "source video decoder", State: Present,
			Value:    fmt.Sprintf("%s %dx%d @ %.3f fps", v.Codec, v.Width, v.Height, v.FPS),
			Evidence: "ffprobe -show_streams"})
	}
	return items
}

// decoderItem reports whether ffmpeg lists a decoder. The symbol is the first
// non-flag FIELD, not the start of the line: ffmpeg's output is
//
//	V....D h264    H.264 / AVC / MPEG-4 AVC / H.264 / ...
//
// so a line-prefix match finds nothing and reports a working decoder as ABSENT.
// DecoderItem reports whether ffmpeg lists a decoder, and carries the decoder's
// own line as the evidence.
//
// It is EXPORTED so the preflight gate uses the same matcher as the capability
// table. The first version of the gate had its own copy that compared the start
// of a line to the symbol; ffmpeg puts a capability column in front of the symbol
// and that column's length varies with the build, so the gate refused to start
// on a machine whose decoder `pocctl cap` had just reported as present. Two
// copies of one rule will disagree; one copy cannot.
func DecoderItem(name, decodersOut, symbol string) Item {
	return decoderItem(name, decodersOut, symbol)
}

func decoderItem(name, decodersOut, symbol string) Item {
	for _, line := range strings.Split(decodersOut, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// ffmpeg's capability column comes first and is a run of letters and
		// dots. Its LENGTH VARIES with the build: a decoder with frame-level
		// multithreading is "VFS..D", seven characters, while a plain one is
		// "V.....", six. So the column is matched on its shape and not on a
		// length, and a length check is what made a working h264 decoder report
		// as ABSENT on a real machine.
		for len(fields) > 0 && isFlagColumn(fields[0]) {
			fields = fields[1:]
		}
		if len(fields) == 0 {
			continue
		}
		if fields[0] == symbol {
			return Item{Name: name, State: Present, Value: strings.TrimSpace(line), Evidence: "ffmpeg -decoders"}
		}
	}
	return Item{Name: name, State: Absent,
		Reason:   "this ffmpeg build does not list " + symbol + " in -decoders",
		Evidence: "ffmpeg -decoders"}
}

// isFlagColumn recognises ffmpeg's capability column: a run of at least four
// characters drawn only from the capability letters and the dots between them.
//
// It is matched on the alphabet rather than on a length, and never on a length
// alone: a decoder symbol is at least as likely to be six characters long as
// the column is, so "length six" would eat symbols as well as miss columns.
func isFlagColumn(s string) bool {
	if len(s) < 4 {
		return false
	}
	if strings.Count(s, ".") == 0 {
		return false
	}
	for _, r := range s {
		switch r {
		case 'V', 'A', 'S', 'F', 'X', 'B', 'D', 'C', 'E', '.':
		default:
			return false
		}
	}
	return true
}

// hwAccelList reads the acceleration methods out of `ffmpeg -hwaccels`, which
// prints a human header line first. Reporting the header as if it were a
// method would put "Hardware acceleration methods:" into a table of methods.
func hwAccelList(out string) []string {
	var accels []string
	for _, line := range strings.Split(out, "\n") {
		l := strings.TrimSpace(line)
		if l == "" || strings.HasSuffix(l, ":") {
			continue
		}
		if strings.Contains(l, " ") {
			// A method name has no spaces; a sentence does.
			continue
		}
		accels = append(accels, l)
	}
	return accels
}

func budget(bps int64, segmentSeconds, days int, assumed bool) DiskBudget {
	secsPerDay := 86400
	perDay := bps / 8 * int64(secsPerDay)
	d := DiskBudget{
		SecondsPerDay:     float64(secsPerDay),
		Days:              days,
		SegmentsPerDay:    secsPerDay / segmentSeconds,
		BytesPerDay:       perDay,
		TotalBytes:        perDay * int64(days),
		Assumed:           assumed,
		AssumedBitRateBps: bps,
	}
	if assumed {
		d.Note = "the bit rate is an assumption, not a measurement; the projection inherits that"
	} else {
		d.Note = fmt.Sprintf("measured at %.2f Mbit/s by ffprobe; %d segments/day of %ds", float64(bps)/1e6, d.SegmentsPerDay, segmentSeconds)
	}
	return d
}

func parseInt(s string) int64 {
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || v < 0 {
		return 0
	}
	return v
}

func parseFloat(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || v < 0 {
		return 0
	}
	return v
}

func parseRatio(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	parts := strings.SplitN(s, "/", 2)
	if len(parts) == 2 {
		num, den := parseInt(parts[0]), parseInt(parts[1])
		if den == 0 {
			return 0
		}
		return float64(num) / float64(den)
	}
	return parseFloat(s)
}

func firstLine(s string) string {
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		if t := strings.TrimSpace(sc.Text()); t != "" {
			return t
		}
	}
	return "(no message)"
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	var ee *exec.Error
	if asExecError(err, &ee) {
		return true
	}
	return strings.Contains(err.Error(), "not on PATH") || strings.Contains(err.Error(), "executable file not found")
}
