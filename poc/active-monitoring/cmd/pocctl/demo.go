package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/config"
	"github.com/Sergey-Bar/ai-captain-poc/internal/soak"
	"github.com/Sergey-Bar/ai-captain-poc/internal/store"
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
		"skip-build": boolSpec, "rtsp": boolSpec, "live": boolSpec,
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
	live := flags["live"] == "true"
	if live {
		// W2-10: the live demo, in one flag.
		//
		// Two things stop working the moment this runs, and both have to be checked
		// here rather than discovered as a confusing failure twenty minutes later.
		if useRTSP {
			return errors.New("demo --live --rtsp: the rtsp relay publishes the local clip over a loopback socket, and " +
				"the live demo reads the camera in SP_POC_SOURCE_ARG. Running both would print a relay source line on a " +
				"demo that is reading a real camera, which is the one impression this module exists not to give")
		}
		if err := livePreconditions(root); err != nil {
			return err
		}
	}

	step(1, 7, "check the material exists")
	// In live mode the ONNX model, the anchors file and the Python worker are NOT
	// needed: the shipped trigger is the local novelty engine and the verifier is a
	// hosted model. Requiring them here would put W1-4's dependency straight back
	// into the one command an operator runs in front of a customer.
	if live {
		for _, p := range []string{flags["zones"]} {
			if _, err := os.Stat(p); err != nil {
				return fmt.Errorf("demo --live: %s is missing: %w", p, err)
			}
		}
		fmt.Printf("  zones  %s\n", zones)
		fmt.Printf("  data   %s\n\n", dataRoot)
		fmt.Printf("  source %s %s\n", os.Getenv("SP_POC_SOURCE_KIND"), redactSource(os.Getenv("SP_POC_SOURCE_ARG")))
		fmt.Println()
		// DERIVED, not asserted. See printTriggerBanner.
	} else {
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
		// The banner is printed for BOTH paths, and it reads the mode out of the
		// environment slice this command builds. It used to be a literal in the live
		// branch only, saying "pure Go, no ONNX, no Python, no sidecar" whatever the
		// run did.
		printTriggerBanner(demoEnv(root, dataRoot, clip, zones, model, anchors,
			flags["sim-addr"], flags["ui-addr"], seconds, &relay{}))
		fmt.Println()
	}

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
	if live {
		// W2-10. The live environment REPLACES the demo's environment rather than
		// adding to it: a demo that left `SP_POC_SOURCE_ARG` pointing at the local
		// clip would read a recording while the screen said `live rtsp camera`.
		env = liveEnv(env)
	}

	step(5, 7, "start the push target the run will write to")
	if live {
		fmt.Println("  LIVE: this is the XProtect instance in SP_POC_XPROTECT_BASE_URL. No simulator is")
		fmt.Println("  started, and nothing in this demo is a rehearsal. Events and bookmarks will")
		fmt.Println("  appear in that instance's alarm list within seconds of a detection.")
		fmt.Println()
	} else {
		fmt.Println("  This is xpsim, a SIMULATOR. It is not an XProtect server, and no XProtect")
		fmt.Println("  server was reachable. The screen and the report both say so on every page.")
		fmt.Println()
		_, stop, err := runSim(flags["sim-addr"])
		if err != nil {
			return err
		}
		defer stop()
		fmt.Printf("  xpsim on http://%s\n\n", flags["sim-addr"])
	}

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
	if live {
		return showLandedLive(root, env, dataRoot)
	}
	return showLanded(root, env, dataRoot, flags["sim-addr"], relay)
}

// showLandedLive reads the run back and PROVES one decision from stored facts.
//
// W2-10's last step. Against a simulator, "here is what landed" is a read of a
// process this module started. Against a live instance it is a read of somebody
// else's alarm list, so it is worth doing properly: the report, the ledger, the
// sealed head and one replay with zero provider calls.
func showLandedLive(root string, env []string, dataRoot string) error {
	fmt.Println("  Every number below is read back out of the data root, not out of the")
	fmt.Println("  process that produced it. Nothing here is a claim about what happened;")
	fmt.Println("  it is what the database holds.")
	fmt.Println()
	if err := runEnv(root, env, filepath.Join(root, "bin", "pocctl.exe"), "audit", "ledger"); err != nil {
		return fmt.Errorf("demo --live: the ledger did not verify: %w", err)
	}
	fmt.Println()
	if err := runEnv(root, env, filepath.Join(root, "bin", "pocctl.exe"), "audit", "observations"); err != nil {
		return fmt.Errorf("demo --live: the attribution records could not be read: %w", err)
	}
	fmt.Println()
	// The proof. It picks the newest detection because naming one means copying an
	// id off a screen during a live demo, and a proof command that needs a copied
	// id is a proof command nobody runs.
	runID := os.Getenv("SP_POC_DATA_ROOT")
	_ = runID
	if err := runEnv(root, env, filepath.Join(root, "bin", "pocctl.exe"), "prove", "--run", newestRunID(dataRoot)); err != nil {
		return fmt.Errorf("demo --live: the replay did not reproduce the newest decision: %w", err)
	}
	fmt.Println()
	fmt.Println("  Go and look at the camera in Milestone XProtect. The alarm you see there is")
	fmt.Println("  the one this run pushed; its bookmark is the pointer to the recording.")
	return nil
}

func newestRunID(dataRoot string) string {
	st, err := store.Open(dataRoot)
	if err != nil {
		return ""
	}
	defer func() { _ = st.Close() }()
	run, err := st.LatestRun(context.Background())
	if err != nil || run == nil {
		return ""
	}
	return run.ID
}

// livePreconditions refuses a live demo that is not configured to be one.
//
// Every item is checked HERE, before the build, so the operator finds out in one
// pass rather than one variable per run — and each refusal says what breaks, which
// is the rule the whole preflight table is built on.
//
// The list is not "everything the module needs". It is "everything whose absence
// would make a live demo quietly not-live", and the three that matter most:
//
//   - a push target that is not the loopback simulator, because a demo that
//     reports events and pushed them nowhere is the worst outcome available
//   - `SP_POC_SOURCE_KIND=rtsp`, because a loop over a local file prints a source
//     line that reads like a camera and is not one
//   - `SP_POC_VLM_REGION` with a key, because footage that has nowhere declared to
//     go must not be sent anywhere at all
func livePreconditions(root string) error {
	var missing []string
	require := func(name, why string) {
		v := strings.TrimSpace(os.Getenv(name))
		if v == "" {
			missing = append(missing, name+"\n           without it "+why)
		}
	}
	require("SP_POC_XPROTECT_BASE_URL", "there is nothing to push to, and a demo that reports events and pushed them nowhere is the worst outcome available")
	require("SP_POC_XPROTECT_CLIENT_ID", "the push target cannot authenticate")
	require("SP_POC_XPROTECT_CLIENT_SECRET", "the push target cannot authenticate")
	require("SP_POC_XPROTECT_CAMERA_GUID", "a pushed event would name no camera, so no Alarm Definition could match it")
	require("SP_POC_EVENT_TYPE_GUID", "a pushed event would have no type, and the VMS would refuse it as a 400")
	require("SP_POC_DEVICE_PATH", "a bookmark points at no camera, so a pushed event has nowhere for an operator to go")
	require("SP_POC_SOURCE_KIND", "the source line cannot say what the run is reading")
	require("SP_POC_SOURCE_ARG", "there is no source")
	require("SP_POC_SOURCE_LICENCE", "a live source has no declared lawful basis, and this module will not record footage whose basis nobody can name")
	if strings.TrimSpace(os.Getenv("SP_POC_SOURCE_KIND")) != "rtsp" {
		missing = append(missing, "SP_POC_SOURCE_KIND\n           the live demo reads a CAMERA. With kind=loop the source line reads\n"+
			"           `loop over recorded material` and a run against a recording is not a live demo")
	}
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("SP_POC_VLM_PROVIDER")))
	switch provider {
	case "none":
		fmt.Println("  SP_POC_VLM_PROVIDER=none: every event will be an UNVERIFIED proposal. The screen, the")
		fmt.Println("  report and every bookmark will say so. That is a legitimate configuration and this demo")
		fmt.Println("  will not pretend otherwise.")
	case "hafnia":
		if r := strings.ToLower(strings.TrimSpace(os.Getenv("SP_POC_VLM_REGION"))); r != "eu" && r != "us" {
			missing = append(missing, "SP_POC_VLM_REGION\n           footage has no declared destination. There is NO failover between regions")
		}
		if strings.TrimSpace(os.Getenv("SP_POC_VLM_API_KEY")) == "" {
			missing = append(missing, "SP_POC_VLM_API_KEY\n           the call would upload footage unauthenticated, or silently not happen")
		}
	default:
		missing = append(missing, "SP_POC_VLM_PROVIDER\n           it must be `none` or `hafnia`. A provider name on a report is a claim\n"+
			"           that something verified the events, and an unrecognised one claims nothing")
	}

	if len(missing) == 0 {
		fmt.Println("  live demo preconditions pass. The push target is:")
		fmt.Printf("    %s\n", os.Getenv("SP_POC_XPROTECT_BASE_URL"))
		fmt.Println("  This is NOT the loopback simulator. Events and bookmarks will be written into that")
		fmt.Println("  XProtect instance and the operator's real alarm list.")
		fmt.Println()
		return nil
	}
	fmt.Println("demo --live refuses to start. These are missing or wrong, and each one makes the demo")
	fmt.Println("something other than a live demo:")
	fmt.Println()
	for _, m := range missing {
		fmt.Printf("  - %s\n", m)
	}
	return fmt.Errorf("demo --live: %d precondition(s) not met", len(missing))
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
// liveEnv replaces the demo environment with the operator's live one.
//
// It REPLACES rather than appends. The demo environment exists to point a run at a
// local clip and the loopback simulator; leaving any of those in place for a live
// demo would produce a run that reads a recording and pushes to nothing while every
// line of its own banner says `live`. A demo whose environment is half-substituted
// is the exact defect `IsLiveVMS` was added to detect.
//
// The variables the demo owns and the operator's live run must set themselves are
// cleared explicitly, so a value inherited from the operator's shell is not quietly
// used.
func liveEnv(env []string) []string {
	cleared := []string{
		"SP_POC_MODE",
		"SP_POC_SOURCE_KIND", "SP_POC_SOURCE_ARG", "SP_POC_SOURCE_RELAY",
		"SP_POC_SOURCE_LICENCE", "SP_POC_SOURCE_USER", "SP_POC_SOURCE_PASSWORD",
		"SP_POC_XPROTECT_BASE_URL", "SP_POC_XPROTECT_CLIENT_ID", "SP_POC_XPROTECT_CLIENT_SECRET",
		"SP_POC_XPROTECT_USERNAME", "SP_POC_XPROTECT_PASSWORD",
		"SP_POC_CAMERA_GUID", "SP_POC_DEVICE_PATH", "SP_POC_EVENT_TYPE_GUID",
		"SP_POC_SIM_URL", "SP_POC_TRIGGER_MODE", "SP_POC_AI_MODEL", "SP_POC_AI_ANCHORS",
		"SP_POC_SIDECAR_SCRIPT", "SP_POC_VLM_PROVIDER", "SP_POC_VLM_REGION",
		"SP_POC_VLM_API_KEY", "SP_POC_VLM_MODEL", "SP_POC_VLM_DAILY_CAP",
		"SP_POC_CAMERA_INTEGRITY",
	}
	out := make([]string, 0, len(env)+8)
	for _, kv := range env {
		name, _, ok := strings.Cut(kv, "=")
		if ok && inList(cleared, name) {
			continue
		}
		out = append(out, kv)
	}
	for _, name := range cleared {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			out = append(out, name+"="+v)
		}
	}
	// Stamped after the loop, because it is this command's decision and not a
	// default: a live run has no simulator, and the screen says so.
	out = append(out, "SP_POC_MODE=live")
	return out
}

func inList(list []string, name string) bool {
	for _, n := range list {
		if n == name {
			return true
		}
	}
	return false
}

// redactSource prints a source URL without its credentials, because this line
// lands in a terminal and in a screenshot.
func redactSource(raw string) string {
	rest := raw
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		rest = rest[:j]
	}
	if strings.Contains(rest, "@") {
		rest = "REDACTED@" + rest[strings.Index(rest, "@")+1:]
	}
	return rest
}

// demoAbsenceGUIDs are the three blindness Analytics Event Types the demo run
// configures, against the LOCAL simulator.
//
// They are invented here and mean nothing outside it. The demo proves that a window
// of `not_asked` produces ONE aggregated event carrying a count, that the count is
// right, and that the read-back finds it — a claim about the mechanism. It is not a
// claim that any Milestone account has these types registered, and the demo says so
// on screen: the truth line names xpsim, and nothing in this repository may say
// otherwise.
//
// Three and not four: `no_objects` is a negative ANSWER, it already drives the
// `false_positive` verdict on the detection's own event, and an alarm that fires to
// say "nothing" is the fatigue this product exists to remove. `config` refuses that
// fourth entry by name.
const demoAbsenceGUIDs = "*@not_asked=b4d1e1c2-0000-4000-8000-000000000002;" +
	"*@degraded=b4d1e1c2-0000-4000-8000-000000000003;" +
	"*@cannot_tell=b4d1e1c2-0000-4000-8000-000000000004"

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
		// W1-4 retired the sidecar from the trigger path and this line says so where
		// the run reads it. It was ABSENT from this slice, and the slice is merged
		// over the operator's environment, so the mode came from the shell. A shell
		// with `SP_POC_TRIGGER_MODE=sidecar` set produced a per-frame ONNX run whose
		// banner still read "pure Go, no ONNX, no Python, no sidecar" — the only
		// measured claim in this project that was printed without being derived from
		// the measurement.
		//
		// Hardcoded rather than defaulted, for the reason the rest of this slice is
		// hardcoded: `pocctl demo` demonstrates the SHIPPED path. Somebody who wants
		// the sidecar runs `watch` with their own environment.
		//
		// THE MODE IS NOW DETECTOR, and this is the line that had to change with it.
		// The demo used to hardcode `novelty` while the configuration's structural
		// default was also `novelty`, so the two agreed by coincidence rather than by
		// being the same declaration. Flipping the default alone would have left the
		// demo overriding it back, and the headline command would have demonstrated
		// a trigger this module is being retired from.
		//
		// `novelty` remains available and still works; it is licence-free and needs no
		// weights, which is a real virtue on a machine with no ONNX Runtime. It is
		// just no longer the shipped path, and a demo that showed it while claiming
		// to show the shipped path was the last thing standing between this project
		// and an honest report.
		"SP_POC_TRIGGER_MODE=detector",
		// The model and the runtime library, by PATH.
		//
		// The library is named rather than discovered from PATH because two ONNX
		// Runtime versions can be installed on one machine, they are not
		// ABI-compatible with each other's Go bindings, and the SHA-256 recorded
		// beside the weights names one specific build. Both paths come from
		// `pocctl fetch-model` and `pocctl fetch-runtime`, which verify what they
		// write.
		//
		// The demo REFUSES if either is missing rather than falling back to
		// `novelty`. A fallback here would be the worst version of this: the run
		// would report zero vehicles, zero people and zero animals at full health
		// and every number in it would be true.
		"SP_POC_DET_MODEL=" + model,
		"SP_POC_ORT_LIB=" + filepath.Join(root, "models", ortLibraryName()),
		// The model's DECLARED input size, cross-checked against the session at
		// startup. It is a model property rather than a preference: letterboxing a
		// frame into a square the model was not exported for produces boxes, not an
		// error, and those boxes are in the wrong place by a scale factor.
		"SP_POC_DET_INPUT=640",
		// The score threshold is on a DIFFERENT SCALE for each trigger, and this demo
		// now always runs the novelty one, so this is a novelty number and not the
		// sidecar's 0.4.
		//
		// The sidecar emits a model CONFIDENCE in (0,1]: 0.4 means "believe this
		// object is a car with 40% confidence". The novelty engine emits the FRACTION
		// OF THE FRAME THAT CHANGED, which for anything a novelty detector can
		// reasonably be asked about is small: a full-height object crossing a
		// 1280-wide frame measures 0.23, and a car-sized one measures 0.005. At 0.4
		// the shipped trigger could not clear its own bar on any material, and every
		// candidate was dropped as `below the score threshold`.
		//
		// 0.02 is two percent of the frame. It is deliberately low, because the point
		// of the bar is to reject JPEG speckle and not to express scepticism about
		// whether something moved — that is `SP_POC_NOVELTY_MIN_CELLS` and the
		// minimum-component size, which are the knobs about shape. Whether 0.02 is
		// too low on a real camera is a question for `pocctl mark`, not for a demo
		// that has to produce an event to show anything at all.
		"SP_POC_SCORE_THRESHOLD=0.02",
		// `motion` is the class the novelty engine emits and nothing else, and it
		// was MISSING from this list. `detect.Apply` checks the class BEFORE the
		// score, so every candidate the novelty trigger produced was refused as
		// `class not on the approved list` — a filter that no material and no line
		// can satisfy.
		//
		// It is spelled `other` and NOT `motion`, because `classify.Resolve` refuses
		// a name that is neither one of the four families nor a class in the taxonomy
		// — and `motion` is the novelty engine's own word for a measurement, not a
		// COCO class. `other` is the family `FamilyOf` already returns for it, and it
		// is the honest answer: a novelty region is a localised change, which is a
		// thing this taxonomy has no word for.
		//
		// In DETECTOR mode the COCO names are what the model actually emits, so the
		// list below is the one that matters: a zone naming no real COCO class gives
		// zero crossings and no error, and that is the most likely way a demo with a
		// real detector finds nothing.
		"SP_POC_CLASSES=other,car,bird,person,truck,bus",
		// The score CHAIN, which must read left to right:
		//
		//	DET_SCORE (0.05) <= GHOST_MIN_SCORE (0.005) <= SCORE_THRESHOLD (0.02)
		//
		// An inverted chain lies in BOTH directions at once, which is why it cannot be
		// caught downstream: a box the model never emitted is drawn as a ghost, and a
		// box it did emit is refused. The picture shows objects that are not there and
		// omits objects that are, and every number in the report is computed over that.
		//
		// The emit floor is deliberately LOW and the approval bar is well above it, so
		// the band between them is where the two-stage ghost recovery reads from.
		"SP_POC_DET_SCORE=0.05",
		// The detector's own knobs. The two NMS thresholds are SEPARATE and the global
		// one is looser on purpose: class-aware suppression at 0.45 collapses duplicates
		// of one class, and the class-agnostic pass at 0.75 is the only thing that can
		// collapse a `car` and a `truck` box on ONE object — a pair the class-aware pass
		// cannot compare at all.
		"SP_POC_DET_NMS_CLASS=0.45",
		"SP_POC_DET_NMS_GLOBAL=0.75",
		"SP_POC_DET_MAX_BOXES=100",
		"SP_POC_ORT_THREADS=4",
		// The per-frame box log. At the cap every crossing frame is kept and every Nth
		// of the rest, and the run records box_log_sampled with both counts — because
		// once the crossing box is Kalman-filtered, replay reads THIS table and not the
		// decisions table.
		"SP_POC_BOX_LOG_FRAMES=20000",
		// These four arrived with the approved/rejected split and the family
		// taxonomy, and they are REQUIRED with no defaults. The demo had never
		// been updated for them, which means `pocctl demo` — the headline command
		// of the whole module — died in `config.Load` before it printed a step.
		"SP_POC_GHOST_MIN_SCORE=0.005",
		// `SP_POC_MIN_BOX_AREA` is in NORMALISED area (fraction of the frame), not
		// thousandths, and a novelty region is measured rather than drawn by a model,
		// so it is small. 0.0001 is a hundredth of a percent of the frame.
		"SP_POC_MIN_BOX_AREA=0.0001",
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
		// The blindness map, set so the demo can show the absence path without a
		// real VMS. These GUIDs are the simulator's, not a real instance's: the
		// events go to xpsim and the read-back finds them there, which proves the
		// mechanism and not that any Milestone account has these types registered.
		// The three states are the only ones accepted — `no_objects` is a negative
		// answer and gets no alarm of its own.
		"SP_POC_EVENT_TYPE_GUIDS=" + demoAbsenceGUIDs,
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
		// Required and not defaulted, so the demo has to say which trigger it is
		// running. The demo is the ALARM trigger: the reference trigger needs a
		// camera that was genuinely moved, covered and restored, and a demo that
		// pinned a reference view would be asserting integrity it did not measure.
		"SP_POC_CAMERA_INTEGRITY=false",
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

// runEnv runs a child with THIS command's environment, not the shell's.
//
// `cmd.Env = append(os.Environ(), env...)` was here, and `demoEnv`'s own comment
// said it must not be: a demo that inherits an operator's SP_POC_* runs a
// configuration the operator cannot see, and the report belongs to a run nobody
// watched. It is not a theoretical hazard — `SP_POC_TRIGGER_MODE` was absent from
// the slice, so it came from the shell, and a shell carrying `sidecar` produced a
// per-frame ONNX run whose banner claimed pure Go.
//
// Every SP_POC_ variable is therefore dropped from the parent's environment, and
// `env` is the whole truth. `PATH` and the rest of the platform stay: a child
// cannot find ffmpeg without them, and no SP_POC_ value can decide whether a run
// is honest.
func runEnv(dir string, env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = append(withoutPOC(os.Environ()), env...)
	}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// withoutPOC drops every SP_POC_ variable from an environment slice.
func withoutPOC(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if name, _, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(name, config.EnvPrefix) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// printTriggerBanner prints what the trigger WILL BE, read out of the environment
// slice this command is about to run the child with.
//
// It was a literal. It said "pure Go, no ONNX, no Python, no sidecar" and that
// sentence was printed whether or not the run agreed: `SP_POC_TRIGGER_MODE` was
// absent from the slice, the slice was merged over the operator's shell, and a
// shell carrying `sidecar` produced a per-frame ONNX run under a banner denying
// one. It was the only measured claim in this project that was not derived from a
// measurement, and a screen that lies about its own cost model is worse than no
// screen: every number on it is discounted once the reader knows.
//
// It also prints the approved classes, because that list is the other half of the
// same claim. A run whose trigger emits `motion` and whose class list omits
// `motion` approves nothing, silently, and a reader of the screen cannot tell that
// from a quiet road.
func printTriggerBanner(env []string) {
	mode := envValue(env, "SP_POC_TRIGGER_MODE")
	switch mode {
	case soak.TriggerSidecar:
		fmt.Println("  trigger: the per-frame ONNX sidecar. The model runs on EVERY frame at 5 fps,")
		fmt.Println("  not once per event, and the report says which mode produced the boxes.")
	case soak.TriggerNovelty:
		fmt.Println("  trigger: the local novelty engine. model: pure Go, no ONNX, no Python, no")
		fmt.Println("  sidecar. The verifier is the model in SP_POC_VLM_MODEL, called ONCE per event.")
	default:
		// An unrecognised mode is not a third sentence to print. `config.Load` refuses
		// it by name moments later; this says so first and names the value it saw,
		// rather than describing a run that is not configured.
		fmt.Printf("  trigger: %q — not a mode this build operates. The run will refuse by name.\n", mode)
	}
	fmt.Printf("  approved classes %s\n", envValue(env, "SP_POC_CLASSES"))
}

// envValue reads one value out of a NAME=value slice.
func envValue(env []string, name string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			return v
		}
	}
	return ""
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

// ortLibraryName is the shared library's filename on this platform.
//
// It is written out here rather than imported because `pocctl demo` builds an
// ENVIRONMENT for a child process and never loads the library itself — it is
// spelling a path, and spelling it from a constant keeps the two spellings from
// drifting.
func ortLibraryName() string {
	switch runtime.GOOS {
	case "windows":
		return "onnxruntime.dll"
	case "darwin":
		return "libonnxruntime.dylib"
	}
	return "libonnxruntime.so"
}
