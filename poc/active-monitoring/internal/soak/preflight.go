package soak

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/ai"
	"github.com/Sergey-Bar/vision-poc/internal/config"
	"github.com/Sergey-Bar/vision-poc/internal/recorder"
	"github.com/Sergey-Bar/vision-poc/internal/source"
)

// Check is one preflight condition.
type Check struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail"`
	// Why is what breaks if this is false. Printed even when the check passes,
	// so the reader can see what was being protected.
	Why string `json:"why"`
	// Measured is the value, when there is one.
	Measured string `json:"measured,omitempty"`
}

// PreflightResult is the whole table.
type PreflightResult struct {
	Pass     bool    `json:"pass"`
	Checks   []Check `json:"checks"`
	Failures int     `json:"failures"`
	// Summary is the one-line version the run logs.
	Summary string `json:"summary"`
}

// PreflightInput is everything preflight measures. The caller supplies the
// measured source report and the client it will use, so preflight has no
// dependency on the pipeline and can be tested with fakes.
type PreflightInput struct {
	FFmpeg       string
	FFprobe      string
	SourceReport StreamFacts
	FreeBytes    int64
	Headroom     float64
	BudgetDays   int
	DataRoot     string
	Client       *ai.Client
	SimURL       string
	// TriggerMode is where the boxes come from: `novelty` (the shipped path) or
	// `sidecar`. An empty value means the caller has not been updated, and the
	// sidecar row keeps its old meaning rather than the run silently changing
	// which component is required.
	TriggerMode string
	// VLMProvider is the declared verifier: `none` or `hafnia`.
	VLMProvider string
	// VLMRegion and VLMHost pin where footage may go. There is no failover, so
	// preflight prints the host an operator is about to send video to.
	VLMRegion string
	VLMHost   string
	// ClockChecked is the result of an NTP probe: "synchronised", "not
	// verified", or the error. "not verified" is allowed, but the report has to
	// say so.
	ClockState string
	// ConfiguredClock is the run's own SP_POC_CLOCK_SOURCE. The clock row
	// DEFAULTS to it, so an operator who declared a clock source is not then
	// refused by a second flag for the same fact.
	ConfiguredClock string
	// PowerState is the sleep/hibernate policy as found on this machine.
	PowerState string
	// GateDPrevious is the last rehearsal verdict. An empty string means no
	// rehearsal has ever run, which is not the same as "green".
	GateDPrevious string
	// RehearsalAge is how long ago that rehearsal ran.
	RehearsalAge time.Duration
	// DetectorModel and DetectorLibrary are what `detector` mode is about to load.
	// Both are NAMED rather than discovered: SP_POC_ORT_LIB is a path because two
	// ONNX Runtime versions can be installed on one machine and they are not
	// ABI-compatible with each other's Go bindings.
	DetectorModel   string
	DetectorLibrary string
	// DetectorThreads is SP_POC_ORT_THREADS. It is in the check's own text because a
	// p99 without its thread count describes no build.
	DetectorThreads int
	// DetectorP99MS is the MEASURED p99 from a calibration run, and AIFPS is the
	// declared detection rate.
	//
	// The throughput gate is one comparison: det_p99 x ai_fps < 1000. It exists
	// because `internal/recorder`'s bounded queue BLOCKS rather than dropping frames,
	// so a detector slower than the frame interval does not degrade — it produces
	// backpressure and fails AC1. Discovering that at minute three of a demo is the
	// worst possible time.
	//
	// A P99MS of zero means no calibration has been measured, and the gate says so
	// rather than passing on a multiplication by zero.
	DetectorP99MS float64
	// Calibration is the measured record the p99 came from, and CalibrationNote
	// is why it could not be used when it could not. Both are here so the row
	// can print WHICH build was timed against WHICH is about to run, rather
	// than a bare "no p99" that sends its reader to a profiler.
	Calibration     *DetectorCalibration
	CalibrationNote string
	// SidecarP99MS is the MEASURED loopback p99 from a sidecar calibration —
	// the same record shape as the detector's, measured over the sidecar's own
	// HTTP endpoint. It is zero when no sidecar calibration was recorded, and
	// the AI row says so rather than passing on an absence.
	//
	// The two records live in ONE file (the data root's
	// `detector-calibration.json`) because they are the same fact measured two
	// ways; the record's own trigger field says which way, and Applies refuses
	// a record from the other path.
	SidecarP99MS float64
	// SidecarCalibration and SidecarCalibrationNote are the record and its
	// refusal, so the AI row can print WHICH sidecar was timed when it cannot
	// use the number.
	SidecarCalibration     *DetectorCalibration
	SidecarCalibrationNote string
	// AIFPS is SP_POC_AI_FPS. Zero means EVERY DECODED FRAME, and the measured
	// source FPS stands in for it — refusing the spelling here would have made the
	// most demanding mode unexpressable, and the arithmetic is the same either way.
	AIFPS int
}

// StreamFacts is the measured source, kept as plain numbers so preflight does
// not have to import the source package.
type StreamFacts struct {
	Codec          string
	Width          int
	Height         int
	FPS            float64
	BitRateBps     int64
	Assumed        bool
	DecodeRealTime float64
}

// Preflight refuses to let a run start unless every condition holds. The
// refusals are the point: most of what would break a seven-day run in the middle
// is knowable in the first minute.
func Preflight(ctx context.Context, in PreflightInput) (PreflightResult, error) {
	var res PreflightResult

	add := func(c Check) {
		res.Checks = append(res.Checks, c)
		if !c.Pass {
			res.Failures++
		}
	}

	// 1. ffmpeg, with an h264 decoder.
	ffmpegOK, _ := commandExists(in.FFmpeg)
	add(Check{Name: "ffmpeg on PATH", Pass: ffmpegOK, Why: ffmpegWhy,
		Detail: describeCommand(in.FFmpeg, ffmpegOK)})

	// 2. The h264 decoder, which is a SEPARATE row from ffmpeg being present:
	// a build without it starts happily and then produces a stream of errors and
	// no frames, which looks like a source problem and is not.
	// The consequence is the CONSTANT, not whatever hasDecoder returned. A check
	// returns an empty reason precisely when it passes, and a row whose
	// consequence disappears when it is satisfied stops explaining itself exactly
	// when a reader is least alert for it.
	decodersOK, _ := hasDecoder(ctx, in, "h264")
	add(Check{Name: "h264 decoder listed by ffmpeg", Pass: decodersOK, Why: decoderWhy,
		Detail: decoderDetail(ctx, in, "h264")})

	// 3. The trigger path is ready.
	//
	// W1-4: this row was "the sidecar answers and its model is loaded", hard-on.
	// The shipped trigger is the local novelty engine, so the row asked a question
	// about a component the run does not have — and the table's own lesson is
	// "it failed is not an action", which applies to a row that cannot fail for a
	// reason that exists as much as to one that cannot pass.
	//
	// So the row is replaced rather than deleted. A `sidecar` run still needs the
	// sidecar; a `novelty` run instead checks the two things that can actually
	// stop it: that the verifier it declared is configured, and that the pinned
	// reference can be written, because a camera_integrity trigger with nowhere to
	// pin its reference measures against nothing.
	switch {
	case in.TriggerMode == config.TriggerModeDetector:
		add(Check{Name: "the in-process detector is named, hashed, and can hold the declared frame rate",
			Pass:     detectorPreflightOK(in),
			Why:      detectorWhy,
			Measured: detectorMeasured(in),
			Detail:   detectorDetail(in)})
	case in.TriggerMode == config.TriggerModeSidecar || (in.TriggerMode == "" && in.Client != nil):
		if in.Client == nil {
			add(Check{Name: "sidecar ready with the model loaded", Pass: false,
				Why:      sidecarWhy,
				Detail:   "no sidecar client was supplied and SP_POC_TRIGGER_MODE is sidecar",
				Measured: "none"})
		} else {
			p, err := in.Client.Probe(ctx)
			if err != nil {
				add(Check{Name: "sidecar ready with the model loaded", Pass: false,
					Why: sidecarWhy, Detail: err.Error()})
			} else {
				add(Check{Name: "sidecar ready with the model loaded", Pass: true,
					Why:      sidecarWhy,
					Measured: fmt.Sprintf("model=%s input=%dx%d providers=%s", p.Model, p.InputSize[0], p.InputSize[1], strings.Join(p.Providers, ",")),
					Detail:   "the sidecar answered /healthz with ready=true"})
			}
		}
	default:
		verifier := in.VLMProvider
		if verifier == "" {
			verifier = "none"
		}
		detail := "the trigger is the local novelty engine and the run declares no verifier, so every event is an " +
			"UNVERIFIED proposal. That is a legitimate configuration and the report labels it as such; it is not the same " +
			"product as a run that asks a model."
		if verifier != "none" {
			detail = fmt.Sprintf("the trigger is the local novelty engine and the verifier is %q in region %q, pinned to %s. "+
				"Region is not failed over: an outage degrades the run and the footage stays on site.", verifier, in.VLMRegion, in.VLMHost)
			if in.VLMRegion == "" || in.VLMHost == "" {
				add(Check{Name: "local trigger with a declared verifier", Pass: false,
					Why: triggerWhy, Detail: "a verifier was declared without a region, so footage has no pinned destination"})
				break
			}
		}
		add(Check{Name: "local trigger with a declared verifier", Pass: true,
			Why: triggerWhy, Measured: "trigger=novelty verifier=" + verifier, Detail: detail})
	}

	// 4. The source is visible: at least one frame within the deadline.
	srcOK, _ := sourceVisible(in)
	add(Check{Name: "source visible (at least one frame)", Pass: srcOK, Why: sourceWhy,
		Measured: fmt.Sprintf("%s %dx%d @ %.3f fps, %.2f Mbit/s, decode %.2fx real time",
			in.SourceReport.Codec, in.SourceReport.Width, in.SourceReport.Height, in.SourceReport.FPS,
			float64(in.SourceReport.BitRateBps)/1e6, in.SourceReport.DecodeRealTime),
		Detail: describeSource(in.SourceReport)})

	// 4. Free space for the measured bit rate, with the declared headroom.
	budget := int64(in.SourceReport.BitRateBps/8) * 86400 * int64(in.BudgetDays)
	diskOK := true
	diskDetail := ""
	if err := recorder.Check(in.FreeBytes, budget, in.Headroom, in.BudgetDays); err != nil {
		diskOK = false
		diskDetail = err.Error()
	}
	add(Check{Name: fmt.Sprintf("free space >= %.2fx the measured %d-day budget", in.Headroom, in.BudgetDays),
		Pass:     diskOK,
		Why:      "starting with too little room means the run fills the disk and stops with disk_budget_exceeded, which is a run that never concluded",
		Measured: fmt.Sprintf("free %s, budget %s, required %s", Bytes(in.FreeBytes), Bytes(budget), Bytes(int64(float64(budget)*in.Headroom))),
		Detail:   diskDetail})

	// 5. The data root is on local disk. WAL and fsync are not guaranteed on a
	// network share, and a WAL that does not fsync is a WAL that loses commits.
	locOK, _ := localDisk(in.DataRoot)
	add(Check{Name: "--data-root on a local disk", Pass: locOK, Why: localWhy,
		Measured: in.DataRoot, Detail: pathKind(in.DataRoot)})

	// 6. Sleep and hibernate off. A machine that sleeps at night produces a
	// seven-day "dropout" that is the operating system's, not the code's.
	powerOK := !powerAllowsSleep(in.PowerState)
	add(Check{Name: "sleep and hibernate disabled", Pass: powerOK,
		Why:      powerWhy,
		Measured: in.PowerState, Detail: in.PowerState})

	// 7. The clock.
	//
	// Three states, and the distinction between them is the point:
	//
	//   "ntp" / "synchronised" - declared and checked
	//   "host"                 - declared, and it is the machine's own clock
	//   "not verified"         - declared, and nobody checked it: PASSES, with a caveat
	//   absent                 - not declared: FAILS
	//
	// A declared-but-unverified clock passes because a run on a host's own
	// clock is a legitimate thing to do; the report prints which one it was, and
	// that is what the reader needs. The first version treated "not verified" as
	// absent and refused runs whose operator had done exactly the right thing.
	clock := in.ClockState
	if strings.TrimSpace(clock) == "" {
		clock = in.ConfiguredClock
	}
	clockDeclared := clock == "synchronised" || clock == "ntp" || clock == "host" || clock == "not verified"
	add(Check{Name: "clock stated (ntp, host, or explicitly not verified)", Pass: clockDeclared,
		Why:      "a detection pushed to a VMS at the wrong wall clock is the wrong detection, and a run whose clock is unstated cannot be compared with anything, including a VMS's",
		Measured: orUnset(clock), Detail: clockSourceNote(clock)})

	// 8. The push target identifies itself, and says whether it is a simulator.
	whoOK, whoDetail := pushTargetIdentified(ctx, in.SimURL)
	add(Check{Name: "push target answers who it is", Pass: whoOK,
		Why:      "a push that reached nothing and a push that reached a simulator look identical unless the target is asked, and this run reports against a simulator",
		Measured: in.SimURL, Detail: whoDetail})

	// 9. The AI p99 inside the declared budget, measured over 30 frames.
	//
	// The row applies to the run that HAS a per-frame model on the AI path — the
	// sidecar. On the shipped detector path the per-frame model is the in-process
	// detector, and its measured p99 is the row above, against the same declared
	// budget. Requiring a sidecar client from a run that never starts one is the
	// defect this row carried for a while: `pocctl preflight` in the shipped mode
	// failed a row about a component the run does not have, which is the exact
	// shape of bug W1-4 removed from the trigger row above it.
	//
	// So it is three states, and each says which one it is in: measured (a
	// sidecar that answers), impossible (a declared sidecar that does not answer),
	// and not-applicable (no sidecar in this run, with the detector row named as
	// the one that holds the number). A row that reports "not applicable, and
	// here is the row that does apply" is honest; a row that fails a
	// configuration it does not describe is a gate that trains everyone to
	// ignore it.
	p99OK, p99Detail, p99Measured := aiBudget(ctx, in)
	add(Check{Name: "AI p99 inside the declared budget", Pass: p99OK,
		Why:      "AC6 will fail automatically if it is not, but finding it here means lowering --ai-fps before a week is spent rather than after",
		Measured: p99Measured, Detail: p99Detail})

	// 10. Gate D: the fault rehearsal must be green on this build.
	//
	// `soak start` runs the rehearsal BEFORE preflight and passes the verdict in,
	// because a gate that requires a rehearsal to have already happened can
	// never be passed by the command that performs the rehearsal. Standing it up
	// on its own is a legitimate way to check a build, and that path is kept.
	gateDOK, gateDDetail := gateDGreen(in)
	add(Check{Name: "fault rehearsal green on this build", Pass: gateDOK,
		Why:      "otherwise a week is spent measuring code that has not been shown to survive anything, and the week does not come back",
		Measured: orUnset(in.GateDPrevious), Detail: gateDDetail})

	res.Pass = res.Failures == 0
	res.Summary = summarize(res)
	return res, nil
}

func orUnset(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(not stated)"
	}
	return s
}

func clockSourceNote(c string) string {
	switch c {
	case "ntp":
		return "the run's clock is declared as NTP-synchronised. The report prints this beside every timestamped number, and it is a DECLARATION, not a verification: nothing here queries a time server"
	case "host":
		return "the run's clock is declared as the host's own. A host that drifts, or that is resumed from sleep, will show it, and the report prints this beside every timestamped number"
	case "synchronised":
		return "the clock was checked and is synchronised"
	case "not verified":
		return "the clock is declared and nobody checked it. The run proceeds, and the report prints `not verified` beside every timestamped number so a reader knows the timestamps are the host's own and unproven"
	default:
		return "no clock source was declared. The run's timestamps cannot be compared with anything, including a VMS's"
	}
}

func summarize(res PreflightResult) string {
	if res.Pass {
		return fmt.Sprintf("preflight: all %d checks pass", len(res.Checks))
	}
	var names []string
	for _, c := range res.Checks {
		if !c.Pass {
			names = append(names, c.Name)
		}
	}
	return fmt.Sprintf("preflight refuses to start: %d of %d checks fail (%s)", len(names), len(res.Checks), strings.Join(names, "; "))
}

// The consequences, one constant per row.
//
// They are CONSTANTS and not the return value of the check, because the check
// returns an empty reason precisely when it PASSES — and a row whose
// consequence vanishes when it is satisfied is a rule that quietly stops
// explaining itself. The consequence has to be readable in the passing case
// too, because that is the case a reader is least alert for.
const (
	ffmpegWhy  = "without ffmpeg there is no source, no recording and no frames, so the run would produce nothing while looking alive"
	decoderWhy = "a source whose codec ffmpeg cannot decode produces a stream of errors and no frames, which looks like a source problem and is not"
	sidecarWhy = "a model that fails to load is a crash in minute one, and the report would call that a dropout of the run rather than of the setup"
	// triggerWhy is the novelty row's. The shipped trigger has no model to fail to
	// load, so the thing that can actually stop it is a verifier declared without a
	// pinned destination — and that is a refusal, not a warning.
	triggerWhy = "the trigger measures a region locally and cannot decide about it; the only thing that can stop the run " +
		"is a verifier that was declared without saying where the footage may go"
	sourceWhy = "no decodable video stream means preflight cannot see a first frame, and the run would hang silently instead of failing"
	diskWhy   = "starting with too little room means the run fills the disk and stops with disk_budget_exceeded, which is a run that never concluded"
	localWhy  = "SQLite WAL and fsync are not guaranteed on a network share, and a WAL that does not fsync loses commits"
	powerWhy  = "a machine that sleeps in the middle of the night makes a week of uptime look like a week of dropouts, and AC1 is zero-tolerance"
	whoamiWhy = "a push that reached nothing and a push that reached a simulator look identical unless the target is asked, and this run reports against a simulator"
)

func commandExists(name string) (bool, string) {
	if name == "" {
		name = "ffmpeg"
	}
	if _, err := exec.LookPath(name); err != nil {
		return false, ffmpegWhy
	}
	return true, ffmpegWhy
}

func describeCommand(name string, ok bool) string {
	if !ok {
		return fmt.Sprintf("%s is not on PATH", name)
	}
	return fmt.Sprintf("%s is on PATH", name)
}

func hasDecoder(ctx context.Context, in PreflightInput, sym string) (bool, string) {
	ff := in.FFmpeg
	if ff == "" {
		ff = "ffmpeg"
	}
	out, err := exec.CommandContext(ctx, ff, "-hide_banner", "-decoders").CombinedOutput()
	if err != nil {
		return false, "a source whose codec ffmpeg cannot decode produces a stream of errors and no frames"
	}
	// The SAME matcher the capability table uses. This used to be a
	// re-implementation that compared the start of the line to the symbol, and
	// ffmpeg's capability column ("VFS..D", seven characters on a build with
	// frame-level multithreading) is in front of it — so the gate refused to
	// start on a machine whose decoder `pocctl cap` had just reported as
	// present. Two copies of the same rule will disagree; one copy cannot.
	item := source.DecoderItem("decoder "+sym, string(out), sym)
	return item.State == source.Present, item.Reason
}

func decoderDetail(ctx context.Context, in PreflightInput, sym string) string {
	ff := in.FFmpeg
	if ff == "" {
		ff = "ffmpeg"
	}
	out, _ := exec.CommandContext(ctx, ff, "-hide_banner", "-decoders").CombinedOutput()
	return source.DecoderItem("decoder "+sym, string(out), sym).Value
}

func sourceVisible(in PreflightInput) (bool, string) {
	if in.SourceReport.Codec == "" {
		return false, "no decodable video stream was found, so preflight cannot see a first frame and the run would hang silently instead of failing"
	}
	if in.SourceReport.Width == 0 || in.SourceReport.Height == 0 {
		return false, "the stream reports no resolution, so nothing can be scaled or inferred on"
	}
	if in.SourceReport.DecodeRealTime > 0 && in.SourceReport.DecodeRealTime < 1 {
		return false, fmt.Sprintf("decoding runs at %.2fx real time on this machine, so the stream cannot be held live at this resolution and frame rate", in.SourceReport.DecodeRealTime)
	}
	return true, "no decodable video stream was found, so preflight cannot see a first frame and the run would hang silently instead of failing"
}

func describeSource(f StreamFacts) string {
	if f.Assumed {
		return "the bit rate is an ASSUMPTION, not a measurement; the disk projection inherits that and the report prints it next to every number"
	}
	return "measured by ffprobe on this machine"
}

func pathKind(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	vol := filepath.VolumeName(abs)
	if vol == "" {
		vol = string(filepath.Separator)
	}
	if isNetworkPath(abs) {
		return fmt.Sprintf("%s resolves to %s, which is a network path", path, abs)
	}
	return fmt.Sprintf("%s resolves to %s on volume %s, a local path", path, abs, vol)
}

func isNetworkPath(path string) bool {
	p := strings.ToLower(filepath.ToSlash(path))
	return strings.HasPrefix(p, "//") || strings.HasPrefix(p, "\\\\")
}

func localDisk(path string) (bool, string) {
	if path == "" {
		return false, "SQLite WAL and fsync are not guaranteed on a network share, and a WAL that does not fsync loses commits"
	}
	if isNetworkPath(path) {
		return false, "SQLite WAL and fsync are not guaranteed on a network share, and a WAL that does not fsync loses commits"
	}
	if runtime.GOOS == "windows" {
		// A substituted drive is a local path with a network's behaviour.
		if out, err := exec.Command("cmd", "/c", "subst").CombinedOutput(); err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				if strings.Contains(strings.ToLower(line), ":\\") && strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), strings.ToLower(filepath.VolumeName(path))) {
					return false, "this path is on a SUBST-ed drive, which points at another volume; SQLite WAL and fsync are only guaranteed on a real local volume"
				}
			}
		}
	}
	return true, "SQLite WAL and fsync are not guaranteed on a network share, and a WAL that does not fsync loses commits"
}

func powerAllowsSleep(state string) bool {
	s := strings.ToLower(state)
	if s == "" {
		return true
	}
	return !strings.Contains(s, "standby") && !strings.Contains(s, "hibernate") && !strings.Contains(s, "sleep") && s != "available"
}

func pushTargetIdentified(ctx context.Context, simURL string) (bool, string) {
	if simURL == "" {
		return false, "without a push target that answers who it is, a push that reached nothing and a push that reached a simulator are indistinguishable"
	}
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(c, http.MethodGet, strings.TrimRight(simURL, "/")+"/__control/whoami", nil)
	if err != nil {
		return false, err.Error()
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, fmt.Sprintf("the push target at %s did not answer: %v", simURL, err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	body := string(buf[:n])
	if !strings.Contains(body, "simulator") {
		return false, fmt.Sprintf("the push target did not identify itself as a simulator: %s", body)
	}
	return true, body
}

func aiBudget(ctx context.Context, in PreflightInput) (bool, string, string) {
	if in.TriggerMode != config.TriggerModeSidecar {
		return true, "this run's per-frame model is the " + triggerNoun(in.TriggerMode) +
				", and its measured p99 is the detector row above; there is no sidecar round trip to measure, " +
				"and a row about a component the run does not start is a row that cannot pass for the right reason",
			"not applicable (no sidecar on this path)"
	}
	if in.Client == nil {
		return false, "SP_POC_TRIGGER_MODE=sidecar and no sidecar client was supplied, so the AI latency budget was never measured", "not measured"
	}
	// A sidecar calibration recorded and applied: the REAL thing, measured over
	// the sidecar's own endpoint with its warm-up discarded, and checked with
	// the same arithmetic the detector row uses. The run still measures p99
	// continuously and AC6 still scores it — this row is the early warning,
	// which is the whole reason it exists.
	if in.SidecarP99MS > 0 {
		ok, arithmetic := sidecarArithmetic(in)
		// The record's own Describe sentence already names the sidecar and its
		// address, so it is printed once. A reader comparing a detector row's
		// line with a sidecar row's line should see the same sentence shape
		// with the path substituted, not the path twice.
		measured := "a sidecar calibration was applied"
		if in.SidecarCalibration != nil {
			measured = in.SidecarCalibration.Describe()
		}
		if !ok {
			return false, "the measured sidecar p99 does not fit inside the declared rate: " + arithmetic + ". " +
					"Lower SP_POC_AI_FPS, or a smaller SP_POC_AI_INPUT_WIDTH (the sidecar letterboxes to its " +
					"model's own input, so a smaller one is a model property, not a flag), or a fused export " +
					"decoded in-process. The budget is not the lever: raising SP_POC_AI_BUDGET_MS until the " +
					"row passes is the demo congratulating itself.",
				measured
		}
		return true, "the measured sidecar p99 fits inside the declared rate: " + arithmetic, measured
	}
	// A record exists and does not describe this run: the note names the part
	// that differs, so the operator knows whether to re-run the bench or fix
	// a variable. It is not a failure — a calibration nobody recorded is not
	// a calibration that failed, and the distinction is the row's.
	if in.SidecarCalibration != nil && in.SidecarCalibrationNote != "" {
		return true, "a sidecar calibration is recorded but does not describe this run: " + in.SidecarCalibrationNote +
				". The run measures p99 continuously and AC6 scores it; record one with " +
				"`pocctl bench --images <dir> --data-root <the run's data root> --trigger sidecar`",
			"recorded, but for another build"
	}
	return true, "no sidecar calibration is recorded; the run measures p99 continuously and AC6 scores it", "measured during the run"
}

// triggerNoun names the trigger in one word, for a sentence that has to read as
// English rather than as an enum.
func triggerNoun(mode string) string {
	switch mode {
	case config.TriggerModeDetector:
		return "in-process detector"
	case config.TriggerModeNovelty:
		return "novelty engine"
	case "":
		return "declared trigger"
	default:
		return mode + " trigger"
	}
}

func gateDGreen(in PreflightInput) (bool, string) {
	switch strings.ToUpper(strings.TrimSpace(in.GateDPrevious)) {
	case "PASS":
		if in.RehearsalAge > 14*24*time.Hour {
			return false, fmt.Sprintf("the last rehearsal was %s ago, which is longer than the two weeks this check is willing to accept: the code has moved since it was shown to survive anything", in.RehearsalAge.Round(time.Hour))
		}
		return true, fmt.Sprintf("the last rehearsal was %s ago and passed", in.RehearsalAge.Round(time.Hour))
	case "FAIL":
		return false, "the last rehearsal failed; starting a week on code that does not survive a 500 is measuring the wrong thing"
	case "NOT RUN", "":
		return false, "no fault rehearsal has ever run on this build, so nothing has been shown to survive anything; gate D comes before the run clock, not after"
	default:
		return false, fmt.Sprintf("the last rehearsal verdict %q is not one of PASS, FAIL or NOT RUN", in.GateDPrevious)
	}
}

var _ = os.Stat
