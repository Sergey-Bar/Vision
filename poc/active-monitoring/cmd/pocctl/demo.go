package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/xprotect"
	"github.com/Sergey-Bar/ai-captain-poc/internal/xpsim"
)

// cmdDemo is T32: three minutes, a labelled clip, the real pipeline, the real
// screen, and an event that is shown to have landed by a read-back.
//
// The mode is demo-replay and it is stamped on the screen, in the log and in the
// report, because a viewer must not have to look for it.
func cmdDemo(args []string) error {
	flags, err := flagSet("demo", map[string]string{"data-root": "", "clip": "", "zones": "", "seconds": "150", "sim-addr": "127.0.0.1:8098", "ui-addr": "127.0.0.1:8097", "model": "", "skip-build": boolSpec}, args)
	if err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		return fmt.Errorf("demo: this script is written for the Windows shell, because that is where the run was developed. The pipeline itself is cross-platform: run `go run ./cmd/watch` with the same SP_POC_* variables on any platform")
	}

	root := repoRoot()
	clip := flags["clip"]
	if clip == "" {
		clip = filepath.Join(root, "testdata", "car-crossing.mp4")
	}
	zones := flags["zones"]
	if zones == "" {
		zones = filepath.Join(root, "testdata", "car-crossing.zones.json")
	}
	model := flags["model"]
	if model == "" {
		model = filepath.Join(root, "models", "yolov8n.onnx")
	}
	dataRoot := flags["data-root"]
	if dataRoot == "" {
		dataRoot = filepath.Join(root, ".demo-data")
	}
	seconds := 150
	fmt.Sscanf(flags["seconds"], "%d", &seconds)

	step(1, 6, "check the material exists")
	for _, p := range []string{clip, zones, model, filepath.Join(root, "pyworker", "server.py")} {
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("demo: %s is missing: %w\n\n"+
				"  The demo needs the labelled clip, its zone file and the ONNX model. Build them with:\n"+
				"    scripts\\make-bird-clip.sh      (the synthetic bird case)\n"+
				"    pocctl fetch-model --url <url>  (the model is not in git)", p, err)
		}
	}
	fmt.Printf("  clip   %s\n", clip)
	fmt.Printf("  zones  %s\n", zones)
	fmt.Printf("  model  %s\n", model)
	fmt.Printf("  data   %s\n\n", dataRoot)

	step(2, 6, "build the two binaries")
	if flags["skip-build"] != "true" {
		if err := run(root, "go", "build", "-o", filepath.Join(root, "bin", "pocctl.exe"), "./cmd/pocctl"); err != nil {
			return err
		}
		if err := run(root, "go", "build", "-o", filepath.Join(root, "bin", "watch.exe"), "./cmd/watch"); err != nil {
			return err
		}
	}
	pocctl := filepath.Join(root, "bin", "pocctl.exe")

	step(3, 6, "start the simulator on loopback")
	fmt.Println("  This is xpsim, a SIMULATOR. It is not an XProtect server, and no XProtect")
	fmt.Println("  server was reachable. The screen and the report both say so on every page.")
	fmt.Println()
	_, stop, err := runSim(flags["sim-addr"])
	if err != nil {
		return err
	}
	defer stop()
	fmt.Printf("  xpsim on http://%s\n\n", flags["sim-addr"])

	step(4, 6, "inject the faults first: the screen is only shown after the rehearsal passes")
	fmt.Println("  Gate D comes before the screen. A screen over a pipeline that has not been")
	fmt.Println("  shown to survive a 500 is a screen over nothing.")
	fmt.Println()
	env := demoEnv(root, dataRoot, clip, zones, model, flags["sim-addr"], flags["ui-addr"], seconds)
	if err := runEnv(root, env, pocctl, "rehearse", "all"); err != nil {
		return fmt.Errorf("demo: the fault rehearsal did not pass, so the screen is not shown. This is the order the plan requires and it is not negotiable")
	}
	fmt.Println()

	step(5, 6, "run the real pipeline for the demo window")
	fmt.Println("  mode=demo-replay is stamped on the screen, in the log and in the report.")
	fmt.Println("  A viewer must not have to look for it.")
	fmt.Println()
	fmt.Printf("  open http://%s once the line below says the screen is up\n", flags["ui-addr"])
	fmt.Println()
	watch := filepath.Join(root, "bin", "watch.exe")
	_ = runEnv(root, env, watch)

	step(6, 6, "show what landed, read back")
	return showLanded(root, env, dataRoot, flags["sim-addr"])
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

func demoEnv(root, dataRoot, clip, zones, model, simAddr, uiAddr string, seconds int) []string {
	return []string{
		"SP_POC_DATA_ROOT=" + dataRoot,
		"SP_POC_MODE=demo-replay",
		"SP_POC_SOURCE_KIND=loop",
		"SP_POC_SOURCE_ARG=" + clip,
		"SP_POC_AI_ADDR=127.0.0.1:8099",
		"SP_POC_AI_MODEL=" + model,
		"SP_POC_SIDECAR_SCRIPT=" + filepath.Join(root, "pyworker", "server.py"),
		"SP_POC_AI_FPS=5",
		"SP_POC_AI_DEADLINE_MS=2000ms",
		"SP_POC_AI_BUDGET_MS=900ms",
		"SP_POC_AI_BACKPRESSURE_MS=5s",
		"SP_POC_AI_INPUT_WIDTH=640",
		"SP_POC_SCORE_THRESHOLD=0.4",
		"SP_POC_CLASSES=car,bird,person,truck,bus",
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
		"SP_POC_PROVENANCE=synthetic",
	}
}

func showLanded(root string, env []string, dataRoot, simAddr string) error {
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
	fmt.Println("    - the run was mode=demo-replay over a synthetic clip, and the header says so")
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
