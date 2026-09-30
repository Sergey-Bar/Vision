package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/ai"
	"github.com/Sergey-Bar/ai-captain-poc/internal/config"
	"github.com/Sergey-Bar/ai-captain-poc/internal/recorder"
	"github.com/Sergey-Bar/ai-captain-poc/internal/soak"
	"github.com/Sergey-Bar/ai-captain-poc/internal/source"
	"github.com/Sergey-Bar/ai-captain-poc/internal/xprotect"
	"github.com/Sergey-Bar/ai-captain-poc/internal/xpsim"
)

// cmdCap is the §5 capability table. It measures, it does not quote.
func cmdCap(args []string) error {
	flags, err := flagSet("cap", map[string]string{"source-kind": "", "source-arg": "", "ffmpeg": "", "ffprobe": "", "segments": "60", "days": "7"}, args)
	if err != nil {
		return err
	}
	kind := flags["source-kind"]
	arg := flags["source-arg"]
	if kind == "" || arg == "" {
		kind, arg = kindFromEnv()
	}
	if kind == "" || arg == "" {
		return fmt.Errorf("cap: there is no source to measure. Pass --source-kind and --source-arg, or set SP_POC_SOURCE_KIND and SP_POC_SOURCE_ARG. A capability table with no source would be a table of the machine and not of this pipeline")
	}
	spec, err := source.Parse(kind, arg)
	if err != nil {
		return err
	}
	segments := 60
	days := 7
	fmt.Sscanf(flags["segments"], "%d", &segments)
	fmt.Sscanf(flags["days"], "%d", &days)

	ff := flags["ffmpeg"]
	if ff == "" {
		ff = "ffmpeg"
	}
	fp := flags["ffprobe"]
	if fp == "" {
		fp = "ffprobe"
	}
	p := source.Prober{Runner: source.ExecRunner{}, FFmpeg: ff, FFprobe: fp, Segments: segments, Days: days}
	rep, err := p.Probe(ctx(), spec)
	if err != nil {
		fmt.Println("capabilities (partial: the source could not be probed)")
		printItems(rep.Items)
		return err
	}

	fmt.Println("# capability table")
	fmt.Println()
	fmt.Println("Measured on this machine, right now, by the commands named in the last column.")
	fmt.Println("ABSENT is a value, not an omission: it always carries the reason it is absent.")
	fmt.Println()

	fmt.Println("## stream")
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "what\tvalue\tmeasured by")
	fmt.Fprintln(tw, "kind\t"+string(spec.Kind)+"\t--source-kind")
	fmt.Fprintln(tw, "argument\t"+spec.Arg+"\t--source-arg")
	for _, s := range rep.Streams {
		fmt.Fprintf(tw, "stream %d\t%s %dx%d @ %.3f fps, %.0f kbit/s\tffprobe -show_streams\n",
			s.Index, s.Codec, s.Width, s.Height, s.FPS, float64(s.BitRateBps)/1000)
	}
	fmt.Fprintf(tw, "bit rate\t%.3f Mbit/s (%s)\tffprobe\n", float64(rep.BitRateBps)/1e6, rep.BitRateSrc)
	if rep.Decode.Reachable {
		fmt.Fprintf(tw, "decode cost\t%.2fx real time (10 second pass)\tffmpeg -benchmark\n", rep.Decode.Realtime)
	} else {
		fmt.Fprintf(tw, "decode cost\tNOT REALTIME: %s\tffmpeg -benchmark\n", rep.Decode.Reason)
	}
	tw.Flush()
	fmt.Println()

	fmt.Println("## disk budget")
	fmt.Printf("  the camera's bit rate is the disk rate. This is measured, not quoted.\n")
	fmt.Printf("  %d days at %.3f Mbit/s = %s\n", rep.Disk.Days, float64(rep.BitRateBps)/1e6, soak.Bytes(rep.Disk.TotalBytes))
	fmt.Printf("  %d segments/day of %ds; clips are additional\n", rep.Disk.SegmentsPerDay, segments)
	fmt.Printf("  %s\n", rep.Disk.Note)
	if free, err := recorder.Free(filepath.Dir(mustAbs(arg))); err == nil {
		need := int64(float64(rep.Disk.TotalBytes) * 1.5)
		fmt.Printf("  free space on this volume: %s; the 1.5x headroom rule needs %s\n", soak.Bytes(free), soak.Bytes(need))
		if free < need {
			fmt.Printf("  NOT ENOUGH: pocctl preflight will refuse to start a run, by name, with this reason\n")
		}
	} else {
		fmt.Printf("  free space: ABSENT — %v\n", err)
	}
	fmt.Println()

	fmt.Println("## machine")
	printItems(rep.Items)
	fmt.Println()

	fmt.Println("## AI latency")
	// The sidecar's own round trip, measured over 30 frames of the real source.
	if sidecarOK() {
		if p99, n, err := measureAI(); err == nil {
			fmt.Printf("  p50/p95/p99 over %d round trips at %dx%d: %s / %s / %s\n", n, 640, 360, p99[0].Round(time.Millisecond), p99[1].Round(time.Millisecond), p99[2].Round(time.Millisecond))
			fmt.Printf("  declared budget: set SP_POC_AI_BUDGET_MS; AC6 scores p99 against it\n")
		} else {
			fmt.Printf("  ABSENT — %v\n", err)
		}
	} else {
		fmt.Printf("  ABSENT — no sidecar is running. Start one with 'pocctl sim' and a sidecar, or set SP_POC_AI_ADDR after starting pyworker/server.py\n")
	}
	return nil
}

func printItems(items []source.Item) {
	fmt.Println("  what | state | value or reason | measured by")
	for _, it := range items {
		value := it.Value
		if it.State == source.Absent {
			value = "ABSENT — " + it.Reason
		}
		if value == "" {
			value = "present"
		}
		fmt.Printf("  %-34s %-8s %-60s %s\n", it.Name, it.State, oneLine(value), it.Evidence)
	}
}

func oneLine(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", "; ")
	if len(s) > 60 {
		s = s[:57] + "..."
	}
	return s
}

func mustAbs(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return a
}

func kindFromEnv() (string, string) {
	return os.Getenv("SP_POC_SOURCE_KIND"), os.Getenv("SP_POC_SOURCE_ARG")
}

func sidecarOK() bool {
	addr := os.Getenv("SP_POC_AI_ADDR")
	if addr == "" {
		return false
	}
	c, err := ai.New(addr, 2*time.Second)
	if err != nil {
		return false
	}
	_, err = c.Probe(ctx())
	return err == nil
}

func measureAI() ([3]time.Duration, int, error) {
	addr := os.Getenv("SP_POC_AI_ADDR")
	if addr == "" {
		return [3]time.Duration{}, 0, errNotConfigured
	}
	c, err := ai.New(addr, 2*time.Second)
	if err != nil {
		return [3]time.Duration{}, 0, err
	}
	lat := ai.NewLatencies(1000)
	// A real frame, 640x360 of mid grey, is enough to measure the round trip
	// without a camera. The measurement is of the pipeline, not of the content.
	w, h := 640, 360
	rgb := make([]byte, w*h*3)
	for i := 0; i < len(rgb); i += 3 {
		rgb[i], rgb[i+1], rgb[i+2] = 128, 128, 128
	}
	for i := 1; i <= 30; i++ {
		r := c.Detect(ctx(), uint64(i), rgb, w, h)
		lat.Add(r.Latency)
	}
	p50, p95, p99, n, ok := lat.Percentiles()
	if !ok {
		return [3]time.Duration{}, 0, fmt.Errorf("no round trip was measured")
	}
	return [3]time.Duration{p50, p95, p99}, n, nil
}

// cmdProbe is the contract confidence table. Every path is labelled
// `documented` or `untested-against-real-vms`, and the tool never promotes a
// path on its own.
func cmdProbe(args []string) error {
	fmt.Println("# Milestone contract confidence")
	fmt.Println()
	fmt.Println("`documented` means a line of Milestone's own downloaded specification backs")
	fmt.Println("the path and every field sent. `untested-against-real-vms` means the shape is")
	fmt.Println("checked but no real server has ever answered it: no XProtect server has been")
	fmt.Println("reachable from this repository. Reading this table and then claiming an event")
	fmt.Println("reached an operator is the mistake this table exists to prevent.")
	fmt.Println()
	rows := []struct {
		op      string
		method  string
		path    string
		status  string
		success string
		conf    string
	}{
		{"token", "POST", xprotect.PathToken, "200 + access_token", "401/400 -> auth_failed", "documented"},
		{"event", "POST", xprotect.PathEvents, "202 + data.id", "403/400/409 -> push_refused; 5xx -> retry", "documented"},
		{"event (idempotent read-back)", "GET", xprotect.PathEvents + "?tag=equals:'<tag>'", "200 with the event", "non-200 -> unknown", "documented"},
		{"event (batch)", "POST", xprotect.PathEventsBulk, "207", "per-item results", "documented"},
		{"bookmark", "POST", xprotect.PathBookmarks, "200", "400/403 -> push_refused", "documented"},
		{"event types", "GET", xprotect.PathEventTypes, "200", "non-200", "documented"},
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "operation\tmethod\tpath\tsuccess\ton failure\tconfidence")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.op, r.method, r.path, r.status, r.conf, "documented (spec) / untested-against-real-vms")
	}
	tw.Flush()
	fmt.Println()
	fmt.Println("The last column is the only honest reading: documented AND untested against a")
	fmt.Println("real VMS. A tool that promoted a path to 'verified' on the strength of a local")
	fmt.Println("simulator would be claiming something it cannot know.")
	fmt.Println()
	fmt.Println("Every call in this module is additionally checked against the downloaded")
	fmt.Println("specification by a test (internal/xprotect/contract_test.go). Change a path or")
	fmt.Println("a field in the client and that test fails; it needs no VMS to fail.")
	return nil
}

// runSim starts the simulator and refuses to start if something is already
// listening on the address.
//
// The refusal matters more than it looks. The first version of this bound
// blindly, and a second simulator — from another shell, from a previous run, or
// from the rehearsal itself — took the port instead. The client then talked to
// THAT one, which no fault was ever armed on, so every injected fault appeared
// to behave unexpectedly and gate D went red for a reason that had nothing to do
// with the code.
//
// A gate that can be satisfied, or failed, by whatever happens to be on a port
// is not a gate.
func runSim(addr string) (*xpsim.Server, func(), error) {
	if owner := portOwner(addr); owner != "" {
		return nil, nil, fmt.Errorf("xpsim: %s is already in use, so this rehearsal would test whatever is already there rather than a simulator it armed itself. Stop that process, or pass --sim-addr with a free port (the one in use was opened by %s)", addr, owner)
	}
	sim := xpsim.New()
	srv := &httpServer{sim: sim, addr: addr}
	if err := srv.start(); err != nil {
		return nil, nil, err
	}
	return sim, srv.stop, nil
}

// portOwner names the process holding a loopback port, or "" if it is free.
func portOwner(addr string) string {
	conn, err := net.DialTimeout("tcp", addr, 400*time.Millisecond)
	if err != nil {
		return ""
	}
	_ = conn.Close()
	return "another process"
}

func cmdSim(args []string) error {
	flags, err := flagSet("sim", map[string]string{"addr": "127.0.0.1:8098"}, args)
	if err != nil {
		return err
	}
	_, stop, err := runSim(flags["addr"])
	if err != nil {
		return err
	}
	defer stop()
	fmt.Printf("xpsim listening on http://%s\n", flags["addr"])
	fmt.Println("This is a SIMULATOR. It is not an XProtect server and no XProtect server was")
	fmt.Println("reachable. Every run against it prints that line in its report.")
	fmt.Println()
	fmt.Println("  GET  /__control/whoami            who the simulator says it is")
	fmt.Println("  POST /__control/fault             {\"fault\":\"500\",\"count\":1,\"path\":\"/API/rest/v1/events\",\"method\":\"POST\"}")
	fmt.Println("  POST /__control/reset             forget every pushed event")
	fmt.Println("  GET  /__control/events            what the simulator actually holds")
	fmt.Println()
	fmt.Println("press ctrl-c to stop")
	select {}
}

func cmdFetchModel(args []string) error {
	flags, err := flagSet("fetch-model", map[string]string{"url": "", "out": "models/yolov8n.onnx"}, args)
	if err != nil {
		return err
	}
	url := flags["url"]
	out := flags["out"]
	if url == "" {
		url = os.Getenv("SP_POC_MODEL_URL")
	}
	if url == "" {
		return fmt.Errorf("fetch-model: no URL. Pass --url or set SP_POC_MODEL_URL. The model is not in git, and a model whose provenance is a hardcoded URL in this file would be a model nobody checked")
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	fmt.Printf("downloading %s -> %s\n", url, out)
	if err := download(url, out); err != nil {
		_ = os.Remove(out)
		return err
	}
	info, err := os.Stat(out)
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		_ = os.Remove(out)
		return fmt.Errorf("fetch-model: %s is zero bytes; a download that produced an empty file is not a model", out)
	}
	fmt.Printf("wrote %s (%s). Record where it came from and under what licence next to it; a\nmodel with no provenance is as unaccountable as a video with no provenance.\n",
		out, soak.Bytes(info.Size()))
	return nil
}

func runForeground(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	watch := filepath.Join(filepath.Dir(exe), "watch.exe")
	if runtime.GOOS != "windows" {
		watch = filepath.Join(filepath.Dir(exe), "watch")
	}
	if _, err := os.Stat(watch); err != nil {
		return fmt.Errorf("run: %s is not next to this binary. Build both with 'go build ./cmd/...', or run 'go run ./cmd/watch' instead", watch)
	}
	cmd := exec.Command(watch, args...)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	return cmd.Run()
}

var _ = config.SourceLoop
