package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/xprotect"
	"github.com/Sergey-Bar/ai-captain-poc/internal/xpsim"
)

// cmdDemo is T32: three minutes, a labelled clip, the real pipeline, the real
// screen, and an event that is shown to have landed by a read-back.
//
// The mode is demo-replay and it is stamped on the screen, in the log and in the
// report, because a viewer must not have to look for it.
//
// `--rtsp` changes ONE thing about that sentence and changes it in the honest
// direction. The relay publishes the same recording through an rtsp server, so
// the run really does read a live socket, and the source line says
// "live rtsp (relay of recorded material, NOT a camera)" on the screen, in the
// log and in the report. It is opt-in (D-4) because a machine with no mediamtx
// must still be able to run the loop, and it refuses by name when mediamtx is
// absent rather than falling back: a demo that quietly downgrades is a demo that
// reports a source it did not use.
func cmdDemo(args []string) error {
	flags, err := flagSet("demo", map[string]string{
		"data-root": "", "clip": "", "zones": "", "seconds": "180",
		"sim-addr": "127.0.0.1:8098", "ui-addr": "127.0.0.1:8097", "model": "",
		"skip-build": boolSpec, "rtsp": boolSpec,
	}, args)
	if err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		return fmt.Errorf("demo: this script is written for the Windows shell, because that is where the run was developed. The pipeline itself is cross-platform: run `go run ./cmd/watch` with the same SP_POC_* variables on any platform")
	}

	root := repoRoot()
	clip := flags["clip"]
	zones := flags["zones"]
	// The default material is the one the model actually detects things in.
	//
	// It used to be `car-crossing.mp4`, which is a coloured rectangle on a
	// `testsrc` background: a real run over it returns ZERO boxes, so the demo
	// ran the whole pipeline and produced no event to show. That is not a demo,
	// it is a demonstration of a detector that finds nothing, and `detections | 0`
	// reads exactly like a quiet road.
	//
	// This clip is a real night intersection and the model detects vehicles in it
	// at 0.75-0.93. Its licence is NOT recorded — see testdata/labels.json — so it
	// may be read on this machine and may NOT be shown outside it. That is a
	// licensing limit on the footage, not on the code, and it is printed in step 1
	// so nobody discovers it in a room.
	const defaultClip = `testdata\youtube\sTF-6_xinUU.mp4`
	const defaultZones = "testdata/night-intersection.zones.json"
	if clip == "" {
		clip = filepath.Join(root, defaultClip)
	}
	if zones == "" {
		zones = filepath.Join(root, filepath.FromSlash(defaultZones))
	}
	model := flags["model"]
	if model == "" {
		model = filepath.Join(root, "models", "yolov8n.onnx")
	}
	anchors := filepath.Join(root, "models", "yolov8n.anchors.txt")
	dataRoot := flags["data-root"]
	if dataRoot == "" {
		dataRoot = filepath.Join(root, ".demo-data")
	}
	seconds := 180
	fmt.Sscanf(flags["seconds"], "%d", &seconds)
	useRTSP := flags["rtsp"] == "true"

	step(1, 7, "check the material exists")
	for _, p := range []string{clip, zones, model, anchors, filepath.Join(root, "pyworker", "server.py")} {
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("demo: %s is missing: %w\n\n"+
				"  The demo needs the labelled clip, its zone file and the ONNX model. Build them with:\n"+
				"    scripts\\make-bird-clip.sh      (the synthetic bird case)\n"+
				"    pocctl fetch-model --url <url>  (the model is not in git)\n\n"+
				"  The anchors file is not in git either: the measured model is an ANCHORED export and\n"+
				"  its anchor boxes live in the model's .cfg, not in the ONNX file. See\n"+
				"  pyworker/fixtures/yolov8n.contract.json.", p, err)
		}
	}
	fmt.Printf("  clip   %s\n", clip)
	fmt.Printf("  zones  %s\n", zones)
	fmt.Printf("  model  %s\n", model)
	fmt.Printf("  anchors %s\n", anchors)
	fmt.Printf("  data   %s\n\n", dataRoot)
	fmt.Println("  The default clip is a real night intersection and the model detects vehicles in")
	fmt.Println("  it. Its licence is NOT recorded (testdata/labels.json): it may be read on THIS")
	fmt.Println("  machine and may NOT be shown to anyone outside it. A screenshot of this demo is")
	fmt.Println("  a screenshot of that footage.")
	fmt.Println()

	step(2, 7, "build the two binaries")
	if flags["skip-build"] != "true" {
		if err := run(root, "go", "build", "-o", filepath.Join(root, "bin", "pocctl.exe"), "./cmd/pocctl"); err != nil {
			return err
		}
		if err := run(root, "go", "build", "-o", filepath.Join(root, "bin", "watch.exe"), "./cmd/watch"); err != nil {
			return err
		}
	}
	pocctl := filepath.Join(root, "bin", "pocctl.exe")

	step(3, 7, "inject the faults first: the screen is only shown after the rehearsal passes")
	fmt.Println("  Gate D comes before the screen. A screen over a pipeline that has not been")
	fmt.Println("  shown to survive a 500 is a screen over nothing.")
	fmt.Println()
	fmt.Println("  The rehearsal arms its OWN simulator and takes it down again. It used to run")
	fmt.Println("  after this script's simulator was already listening, and `rehearse` refuses")
	fmt.Println("  a port somebody else opened — correctly, since a rehearsal against whatever")
	fmt.Println("  happens to be on the port is a rehearsal of nothing. So the rehearsal moved")
	fmt.Println("  here, ahead of the one the run needs.")
	fmt.Println()
	env := demoEnv(root, dataRoot, clip, zones, model, anchors, flags["sim-addr"], flags["ui-addr"], seconds, &relay{})
	if err := runEnv(root, env, pocctl, "rehearse", "all"); err != nil {
		return fmt.Errorf("demo: the fault rehearsal did not pass, so the screen is not shown. This is the order the plan requires and it is not negotiable")
	}
	fmt.Println()

	// Step 4, the relay. It is started AFTER the rehearsal so that a relay that
	// will not come up does not leave a rehearsal's worth of time already spent,
	// and BEFORE the run so the run shares the source with nothing else.
	step(4, 7, "publish the clip over rtsp")
	relay, err := startRelay(dataRoot, clip, useRTSP)
	if err != nil {
		return err
	}
	// Publisher first, server second. Killing the server first leaves ffmpeg
	// writing into a dead socket and its stderr becomes noise instead of a
	// diagnosis.
	defer relay.stop()

	// The RUN's environment, which is the rehearsal's with the source replaced.
	// The rehearsal never read a source — it exercises the push client against a
	// simulator — so it was rehearsed with the loop environment and the run gets
	// the relay one.
	env = demoEnv(root, dataRoot, clip, zones, model, anchors, flags["sim-addr"], flags["ui-addr"], seconds, relay)

	step(5, 7, "start the simulator the run will push to")
	fmt.Println("  This is xpsim, a SIMULATOR. It is not an XProtect server, and no XProtect")
	fmt.Println("  server was reachable. The screen and the report both say so on every page.")
	fmt.Println()
	_, stop, err := runSim(flags["sim-addr"])
	if err != nil {
		return err
	}
	defer stop()
	fmt.Printf("  xpsim on http://%s\n\n", flags["sim-addr"])

	step(6, 7, "run the real pipeline for the demo window")
	fmt.Println("  mode=demo-replay is stamped on the screen, in the log and in the report.")
	fmt.Println("  A viewer must not have to look for it.")
	if relay.on {
		fmt.Println("  --rtsp: the source line reads `live rtsp (relay of recorded material, NOT a")
		fmt.Println("  camera)` on the screen, in the log and in the report. No camera is connected.")
		fmt.Println("  To see the transport fail, kill the mediamtx process and watch the reader stop.")
		fmt.Println("  Do NOT kill the ffmpeg publisher to test a RECONNECT: ffmpeg's rtsp client has")
		fmt.Println("  no reconnect option, so the reader exits and the run ends. That is the")
		fmt.Println("  transport's real limit, not a bug, and it is written down in source.InputArgs.")
	}
	fmt.Println()
	fmt.Printf("  open http://%s once the line below says the screen is up\n", flags["ui-addr"])
	fmt.Println()
	watch := filepath.Join(root, "bin", "watch.exe")
	// The run's exit code is NOT discarded.
	//
	// It was, with `_ =`, and that is how a demo announced "here is what landed"
	// over a pipeline that had died in its first second: `watch` refused a bad
	// zone file, printed the reason, exited non-zero, and step 7 went right
	// ahead and reported an EMPTY run as a result. A failed run and a run with
	// nothing in it produce the same zero events, and only one of them is a
	// measurement — so the difference has to be carried by the exit code or not
	// at all.
	if err := runEnv(root, env, watch); err != nil {
		return fmt.Errorf("demo: the run failed and its exit code is being honoured, because a run that produced nothing and a run that failed both report zero events and only one of them is a measurement: %w\n\n"+
			"  Nothing below is shown. A demo that prints `here is what landed` over a dead pipeline is worse than one that refuses.", err)
	}

	step(7, 7, "show what landed, read back")
	return showLanded(root, env, dataRoot, flags["sim-addr"], relay)
}

func step(n, total int, what string) {
	fmt.Printf("\n[%d/%d] %s\n", n, total, what)
	fmt.Println(strings.Repeat("-", 72))
}

func repoRoot() string {
	// The command is run from the module root in every documented invocation,
	// and this is checked rather than assumed.
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	if _, err := os.Stat(filepath.Join(wd, "go.mod")); err != nil {
		fmt.Println("  WARNING: this does not look like the module root (no go.mod here).")
		fmt.Println("  Run pocctl from poc/active-monitoring.")
	}
	return wd
}

// --- the relay --------------------------------------------------------------

// rtspPort is mediamtx's default RTSP port and the one the demo publishes on.
//
// It is a constant and not a flag on purpose. A demo on an unexpected port is a
// demo nobody can reproduce from the transcript, and a port collision is
// refused by name below rather than worked around by picking another.
const rtspPort = 8554

// relayName is the single path the relay serves.
const relayName = "street"

// relayAddr is the URL the publisher serves and the run reads. No credentials,
// because source.Parse refuses a URL that carries them inline and a loopback
// publisher has none to give.
func relayAddr() string { return fmt.Sprintf("rtsp://127.0.0.1:%d/%s", rtspPort, relayName) }

// relay is the pair of processes a `--rtsp` demo runs: an rtsp server and the
// ffmpeg publisher feeding it. It is a two-field struct and not a process
// registry, because it is two processes with a known teardown order.
type relay struct {
	on         bool
	server     *exec.Cmd
	publisher  *exec.Cmd
	pubLog     *syncBuffer
	serverLog  *syncBuffer
	version    string
	dataDir    string
	configPath string
}

// syncBuffer is a buffer two goroutines can write to: ffmpeg and mediamtx each
// write into theirs on their own goroutine, and the demo reads them on this one
// when something has to be explained.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) tail(n int) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.buf.String()
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// startRelay publishes clip over rtsp, or returns an inert relay when --rtsp was
// not given.
//
// Three refusals, all by name and none silent:
//
//   - mediamtx is not on PATH. The command and the URL are printed. There is no
//     fallback to the loop, because a demo that quietly downgrades is a demo
//     that reports a source it did not use.
//   - :8554 is already listening. The owner is named rather than another port
//     picked.
//   - the stream did not answer within 15 seconds. ffmpeg's and mediamtx's own
//     logs are printed, because that is where they say what they dislike.
func startRelay(dataRoot, clip string, want bool) (*relay, error) {
	if !want {
		fmt.Println("  not requested. --rtsp publishes this clip through an rtsp server so the live")
		fmt.Println("  socket is real. The loop path is the default and stays a valid gate.")
		fmt.Println()
		return &relay{}, nil
	}

	mediamtx, err := exec.LookPath("mediamtx")
	if err != nil {
		return nil, fmt.Errorf("demo: --rtsp needs an rtsp server and there is none on PATH: %v\n\n"+
			"  mediamtx is a single MIT binary that serves rtsp://host:8554/anything with no config.\n"+
			"    winget install --id Bluenviron.MediaMTX -e\n"+
			"  or download it from %s\n\n"+
			"  The demo does NOT fall back to the loop. A run that reports `live rtsp` over a file\n"+
			"  is a run reporting a source it did not use, and `pocctl demo` (no --rtsp) still works\n"+
			"  on this machine exactly as before.", err, mediamtxURL)
	}
	if err := portIsFree(fmt.Sprintf("127.0.0.1:%d", rtspPort)); err != nil {
		return nil, fmt.Errorf("demo: port %d is already in use, so the relay would publish somewhere other than where the transcript says: %v\n\n"+
			"  Stop whatever is listening on %d and run it again. The port is not negotiable:\n"+
			"  a demo on an unexpected port is a demo nobody can reproduce.", rtspPort, err, rtspPort)
	}

	dataDir := filepath.Join(dataRoot, "relay")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("demo: the relay's data directory could not be created: %w", err)
	}

	// mediamtx needs its path DECLARED. The plan said it serves any path with no
	// configuration, and that was true of the versions current when the plan was
	// written and is FALSE of every release since: v1.21 answers
	// "path 'street' is not configured" and refuses the publisher. So a five-line
	// file is written here, in the relay's own directory, and its path is printed
	// — which is a better outcome than the premise being mistaken would have been,
	// because the file says exactly what was served.
	//
	// Everything except RTSP is switched off so the relay occupies one TCP port
	// and one path. A demo that quietly starts an HTTP server and a WebRTC
	// endpoint nobody asked for is a demo that opens more ports than it says.
	cfgPath := filepath.Join(dataDir, "mediamtx.yml")
	cfg, err := os.Create(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("demo: the relay's configuration could not be written to %s: %w", cfgPath, err)
	}
	_, werr := cfg.WriteString(mediamtxConfig(rtspPort, relayName))
	werr2 := cfg.Close()
	if werr != nil || werr2 != nil {
		return nil, fmt.Errorf("demo: the relay's configuration at %s is incomplete: %v %v", cfgPath, werr, werr2)
	}

	r := &relay{on: true, pubLog: &syncBuffer{}, serverLog: &syncBuffer{}, dataDir: dataDir, configPath: cfgPath}

	// The server runs on the generated file. Its working directory is the relay's
	// own directory because that is where mediamtx writes when it is generating a
	// TLS key for WebRTC, and the module root is not a place to put that.
	r.server = exec.Command(mediamtx, cfgPath)
	r.server.Dir = dataDir
	r.server.Stdout, r.server.Stderr = r.serverLog, r.serverLog
	if err := r.server.Start(); err != nil {
		return nil, fmt.Errorf("demo: the rtsp server could not be started: %w", err)
	}
	r.version = mediamtxVersion(mediamtx, r.serverLog)

	// The publisher. `-re` is what makes it a live-RATE stream rather than a
	// burst as fast as the disk allows, and `-c copy` is not negotiable: a
	// transcode would change the pixel format under a decoder whose letterbox
	// arithmetic is written against the shape that was measured and committed in
	// pyworker/fixtures/yolov8n.contract.json.
	r.publisher = exec.Command("ffmpeg",
		"-hide_banner", "-loglevel", "warning",
		"-re", "-stream_loop", "-1", "-i", clip,
		"-c", "copy", "-rtsp_transport", "tcp", "-f", "rtsp", relayAddr())
	r.publisher.Stdout, r.publisher.Stderr = r.pubLog, r.pubLog
	if err := r.publisher.Start(); err != nil {
		r.stop()
		return nil, fmt.Errorf("demo: the ffmpeg publisher could not be started: %w\n\n"+
			"  Is ffmpeg on PATH? It is a hard dependency of the whole module, not just of --rtsp.", err)
	}

	if err := r.waitUntilAnswering(15 * time.Second); err != nil {
		r.stop()
		return nil, err
	}
	fmt.Printf("  relaying %s at %s\n", filepath.Base(clip), relayAddr())
	fmt.Printf("  rtsp server %s, config %s\n", r.serverLogVersion(), cfgPath)
	fmt.Printf("  the source line on the screen and in the report will read:\n")
	fmt.Printf("    source: live rtsp (relay of recorded material, NOT a camera)\n\n")
	return r, nil
}

// mediamtxConfig is the whole configuration: one RTSP listener, one path, and
// every other protocol off.
func mediamtxConfig(port int, name string) string {
	return fmt.Sprintf(`# Written by 'pocctl demo --rtsp'. One RTSP listener, one path.
# Every other protocol is off so the relay occupies exactly one TCP port: a demo
# that quietly starts an HTTP server and a WebRTC endpoint nobody asked for is a
# demo that opens more ports than it says.
logLevel: info
rtspAddress: :%d
hls: no
webrtc: no
rtmp: no
srt: no
api: no
metrics: no
pprof: no
playback: no
moq: no
paths:
  %s:
`, port, name)
}

// waitUntilAnswering polls the relay with ffprobe until it can describe a
// stream. It is a probe rather than a sleep because a fixed wait is either too
// long on a fast machine or too short on a loaded one, and both look like the
// same failure.
//
// Each probe is killed after `probeTimeout`. That is not a precaution: ffprobe
// has NO default read timeout, so against a server that accepts the TCP
// connection and then says nothing — which is exactly what a wedged rtsp server
// does — `CombinedOutput()` blocks forever and the 15-second deadline below never
// fires. A readiness check that can hang past its own deadline is not a readiness
// check; it is the thing it is waiting for.
func (r *relay) waitUntilAnswering(within time.Duration) error {
	const probeTimeout = 5 * time.Second
	deadline := time.Now().Add(within)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
		probe := exec.CommandContext(ctx, "ffprobe",
			"-v", "error",
			"-rw_timeout", "4000000", // microseconds: the socket read timeout
			"-rtsp_transport", "tcp",
			"-i", relayAddr(),
			"-show_entries", "format=format_name",
			"-of", "default=nw=1:nk=1")
		out, err := probe.CombinedOutput()
		cancel()
		if err == nil && strings.TrimSpace(string(out)) != "" {
			return nil
		}
		// A publisher or a server that has already died will never answer, so the
		// wait stops there rather than running out the clock on a corpse.
		if r.publisher.ProcessState != nil || r.server.ProcessState != nil {
			return r.explain(within, fmt.Errorf("the relay exited: %w", err))
		}
		if time.Now().After(deadline) {
			return r.explain(within, fmt.Errorf("the relay did not answer within %s (%w)", within, err))
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// explain turns a relay that will not come up into both processes' own words.
func (r *relay) explain(within time.Duration, cause error) error {
	return fmt.Errorf("demo: the rtsp relay did not come up within %s, so the run was not started: %w\n\n"+
		"  ffmpeg publisher (%s):\n%s\n\n  mediamtx (%s, config %s):\n%s",
		within, cause, relayAddr(), indent(r.pubLog.tail(20)), r.serverLogVersion(), r.configPath, indent(r.serverLog.tail(20)))
}

// stop tears the relay down, PUBLISHER FIRST.
//
// The order is the whole of this function. Killing the server first leaves
// ffmpeg writing into a dead socket, and its stderr becomes noise instead of the
// diagnosis it was.
func (r *relay) stop() {
	killAndWait(r.publisher, 3*time.Second)
	killAndWait(r.server, 3*time.Second)
}

func killAndWait(cmd *exec.Cmd, within time.Duration) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	done := make(chan struct{})
	go func() { _, _ = cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(within):
	}
}

// serverLogVersion is the version as mediamtx announced it. There is no flag for
// it that every release has had, so the banner is the authority, and an
// unparseable banner is reported as unknown rather than as an empty string that
// reads like a version nobody printed.
func (r *relay) serverLogVersion() string {
	if r.version != "" {
		return "v" + r.version
	}
	return "unknown version"
}

// mediamtxVersion asks the binary, and falls back to its banner. The banner is
// the fallback because a `--version` flag that a given release does not have
// must not turn a working relay into a refusal.
func mediamtxVersion(path string, banner *syncBuffer) string {
	if out, err := exec.Command(path, "--version").CombinedOutput(); err == nil {
		s := strings.TrimSpace(string(out))
		if s != "" {
			return strings.TrimPrefix(strings.TrimSpace(strings.SplitN(s, "\n", 2)[0]), "v")
		}
	}
	return versionFromBanner(banner.tail(40))
}

func versionFromBanner(s string) string {
	for _, line := range strings.Split(s, "\n") {
		fields := strings.Fields(line)
		for _, f := range fields {
			if strings.HasPrefix(f, "v1.") || strings.HasPrefix(f, "v0.") {
				return strings.TrimPrefix(f, "v")
			}
		}
	}
	return ""
}

// portIsFree refuses to publish on a port that is already answering. It binds
// and closes rather than dialling, because a TCP connect succeeding only proves
// something is listening, and what we need to know is whether WE can have it.
func portIsFree(addr string) error {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return l.Close()
}

func indent(s string) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return "        (nothing: the process wrote nothing)"
	}
	return "        " + strings.ReplaceAll(s, "\n", "\n        ")
}

const mediamtxURL = "https://github.com/bluenviron/mediamtx/releases"

// demoEnv is the whole child environment. It is a hardcoded slice and not a map
// merged over the parent's, because a demo that inherits an operator's
// SP_POC_* from their shell is a demo whose numbers belong to a run they cannot
// see.
//
// `--rtsp` replaces FIVE of them and adds nothing else. Every other value is
// the same as the loop demo's, which is what makes the two runs comparable:
// changing the transport and the transport's own honesty line at the same time
// is what the source label exists to prevent.
func demoEnv(root, dataRoot, clip, zones, model, anchors, simAddr, uiAddr string, seconds int, r *relay) []string {
	env := []string{
		"SP_POC_DATA_ROOT=" + dataRoot,
		"SP_POC_MODE=demo-replay",
		"SP_POC_SOURCE_KIND=loop",
		"SP_POC_SOURCE_ARG=" + clip,
		// WITH a scheme. It did not have one, and `ai.New` refuses an address
		// that is not http or https — correctly, because a sidecar is a loopback
		// service and there is no third form. So the demo built an environment
		// its own child refused, and the headline command of the whole project
		// had never worked.
		"SP_POC_AI_ADDR=http://127.0.0.1:8099",
		"SP_POC_AI_MODEL=" + model,
		"SP_POC_SIDECAR_SCRIPT=" + filepath.Join(root, "pyworker", "server.py"),
		"SP_POC_AI_FPS=5",
		"SP_POC_AI_DEADLINE_MS=2000ms",
		"SP_POC_AI_BUDGET_MS=900ms",
		"SP_POC_AI_BACKPRESSURE_MS=5s",
		"SP_POC_AI_INPUT_WIDTH=640",
		// The measured export is ANCHORED — 85 fields, three stride outputs — and
		// its anchor boxes live in the model's .cfg and not in the ONNX file. The
		// sidecar reads this variable itself; without it an anchored export is
		// refused by name rather than answered with no objects for the whole run.
		"SP_POC_AI_ANCHORS=" + anchors,
		"SP_POC_SCORE_THRESHOLD=0.4",
		"SP_POC_CLASSES=car,bird,person,truck,bus",
		// These four arrived with the approved/rejected split and the family
		// taxonomy, and they are REQUIRED with no defaults. The demo had never
		// been updated for them, which means `pocctl demo` — the headline command
		// of the whole module — died in `config.Load` before it printed a step.
		"SP_POC_MIN_BOX_AREA=0.0005",
		"SP_POC_GHOST_MIN_SCORE=0.10",
		"SP_POC_TRACK_MIN_FRAMES=3",
		"SP_POC_MAX_BOXES_DRAWN=12",
		"SP_POC_REPORT_METRICS=events",
		"SP_POC_ZONES=" + zones,
		"SP_POC_COOLDOWN_MS=5s",
		"SP_POC_TRACK_MAX_AGE_MS=2s",
		"SP_POC_TRACK_IOU=0.3",
		"SP_POC_LINE_TOLERANCE=0.01",
		"SP_POC_CLIP_BEFORE_MS=5s",
		"SP_POC_CLIP_AFTER_MS=5s",
		"SP_POC_MAX_DISK_BYTES=10737418240",
		"SP_POC_RSS_LIMIT_BYTES=1610612736",
		"SP_POC_DB_LIMIT_BYTES=536870912",
		"SP_POC_CPU_LIMIT_PERCENT=400",
		"SP_POC_DISK_HEADROOM=1.5",
		"SP_POC_DISK_BUDGET_DAYS=1",
		"SP_POC_HEARTBEAT_INTERVAL=10s",
		"SP_POC_GAP_THRESHOLD=30s",
		fmt.Sprintf("SP_POC_SOAK_TARGET=%ds", seconds),
		"SP_POC_SEGMENT_SECONDS=10",
		"SP_POC_CLOCK_SOURCE=host",
		"SP_POC_SIM_URL=http://" + simAddr,
		"SP_POC_XPROTECT_BASE_URL=http://" + simAddr,
		"SP_POC_XPROTECT_CLIENT_ID=GrantValidatorClient",
		"SP_POC_XPROTECT_CLIENT_SECRET=demo-only-not-a-real-secret",
		"SP_POC_XPROTECT_USERNAME=demo",
		"SP_POC_XPROTECT_PASSWORD=demo",
		"SP_POC_CAMERA_GUID=0d9a1b2c-1111-2222-3333-444455556666",
		"SP_POC_DEVICE_PATH=/Local/00000000-0000-0000-0000-000000000000",
		"SP_POC_EVENT_TYPE_GUID=b4d1e1c2-0000-4000-8000-000000000001",
		"SP_POC_PUSH_MAX_ATTEMPTS=6",
		"SP_POC_PUSH_TIMEOUT=10s",
		"SP_POC_UI_ADDR=" + uiAddr,
		"SP_POC_LABEL=demo-replay",
		// The provenance is the MATERIAL's, and a relay does not change it. The
		// clip the relay publishes is still the labelled recording it was, so
		// publishing it over a socket changes what the transport proves and not
		// one bit of what the numbers are.
		"SP_POC_PROVENANCE=synthetic",
		// D-5, in the configuration as well as on the screen: the relay is
		// declared here and nowhere else, and `source.Label` derives the sentence.
		"SP_POC_SOURCE_RELAY=false",
	}
	if !r.on {
		return env
	}
	return setEnv(env,
		// mode=live because `mode=demo-replay` is defined as playing labelled
		// recorded material out of a file, and config refuses a demo replay with
		// a live source. The relay sentence in source.Label is what tells a
		// viewer no camera is involved, so the header does not have to lie about
		// the mode either.
		"SP_POC_MODE=live",
		"SP_POC_SOURCE_KIND=rtsp",
		"SP_POC_SOURCE_ARG="+relayAddr(),
		"SP_POC_SOURCE_RELAY=true",
		// A live source is required to carry a recorded permission basis, and the
		// relay's basis is the clip's own label. As of this writing the clip this
		// demo ships with has NO recorded licence, which is stated here rather
		// than papered over: the footage may be read on this machine and may not
		// be shown to anyone else. Replace it with the real basis as soon as the
		// material has one — see testdata/labels.json.
		"SP_POC_SOURCE_LICENCE=the relay re-publishes the demo's labelled clip, whose licence is NOT recorded in testdata/labels.json (three REPLACE-ME fields). Usable on this machine; NOT cleared to show outside it. No camera is connected.",
	)
}

// setEnv replaces named entries in place and appends the ones that were not
// there. Replacing rather than appending matters twice over: a duplicate in a
// child's environment is a value whose meaning depends on which reader looks
// first, and an override that only appends is how a demo ends up running in
// demo-replay over a relay it claims is live.
func setEnv(env []string, overrides ...string) []string {
	index := make(map[string]int, len(env))
	for i, kv := range env {
		if k, _, ok := strings.Cut(kv, "="); ok {
			index[k] = i
		}
	}
	for _, kv := range overrides {
		k, _, _ := strings.Cut(kv, "=")
		if i, ok := index[k]; ok {
			env[i] = kv
			continue
		}
		index[k] = len(env)
		env = append(env, kv)
	}
	return env
}

func showLanded(root string, env []string, dataRoot, simAddr string, r *relay) error {
	fmt.Println("  What the simulator actually holds, read back over HTTP. This is the same")
	fmt.Println("  read-back the client uses, and the same one the idempotency guarantee rests on.")
	fmt.Println()
	out, err := httpGet("http://" + simAddr + "/__control/whoami")
	if err != nil {
		return err
	}
	fmt.Println("  whoami:", oneLine(out))
	fmt.Println()
	who, err := httpGet("http://" + simAddr + "/__control/events")
	if err != nil {
		return err
	}
	fmt.Println("  pushed:", oneLine(who))
	fmt.Println()
	md, err := os.ReadFile(filepath.Join(dataRoot, "report.md"))
	if err != nil {
		fmt.Printf("  the report was not written: %v\n", err)
		return nil
	}
	fmt.Println("  report (first 40 lines):")
	fmt.Println()
	sc := bufio.NewScanner(strings.NewReader(string(md)))
	n := 0
	for sc.Scan() && n < 40 {
		fmt.Println("   ", sc.Text())
		n++
	}
	fmt.Println()
	fmt.Println("  the full report is at " + filepath.Join(dataRoot, "report.md"))
	fmt.Println()
	fmt.Println("  What to say if someone asks what this proved:")
	fmt.Println("    - the POST is contract-shaped, checked against Milestone's own downloaded spec")
	fmt.Println("    - a read-back found the event, so it is not a status code we are guessing about")
	if r.on {
		fmt.Println("    - source: live rtsp (relay of recorded material, not a camera), published by")
		fmt.Println("      mediamtx " + r.serverLogVersion() + " on 127.0.0.1:" + fmt.Sprint(rtspPort))
		fmt.Println("      No camera is connected and no camera is claimed")
	} else {
		fmt.Println("    - the run was mode=demo-replay over a labelled clip, and the header says so")
		fmt.Println("    - the rtsp path was NOT exercised: no --rtsp, so the source was a file in a loop")
	}
	fmt.Println("    - every pushed event carries the real bounding box in 0-1000 thousandths,")
	fmt.Println("      the same scale as detections.box_*; the pushed payload and the row agree")
	fmt.Println("    - nothing reached Milestone: the target was a simulator on this machine")
	return nil
}

func run(dir, name string, args ...string) error { return runEnv(dir, nil, name, args...) }

func runEnv(dir string, env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func httpGet(url string) (string, error) {
	out, err := exec.Command("curl", "-sS", "--max-time", "10", url).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("could not read %s: %w (%s)", url, err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

var (
	_ = xprotect.PathEvents
	_ = xpsim.Name
	_ = time.Now
)
