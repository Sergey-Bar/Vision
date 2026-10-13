package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/ai"
	"github.com/Sergey-Bar/vision-poc/internal/config"
	"github.com/Sergey-Bar/vision-poc/internal/recorder"
	"github.com/Sergey-Bar/vision-poc/internal/soak"
	"github.com/Sergey-Bar/vision-poc/internal/source"
	"github.com/Sergey-Bar/vision-poc/internal/store"
	"github.com/Sergey-Bar/vision-poc/internal/vlm"
	"github.com/Sergey-Bar/vision-poc/internal/xprotect"
	"github.com/Sergey-Bar/vision-poc/internal/xpsim"
)

// httpServer is the simulator's listener. It is here so `pocctl sim` and the demo
// can start the same server the tests use, rather than a second implementation
// that could drift from the one the contract tests exercise.
type httpServer struct {
	sim   *xpsim.Server
	addr  string
	srv   *http.Server
	bound string
}

func (h *httpServer) start() error {
	ln, err := net.Listen("tcp", h.addr)
	if err != nil {
		return fmt.Errorf("cannot listen on %s: %w", h.addr, err)
	}
	h.bound = ln.Addr().String()
	h.srv = &http.Server{Handler: h.sim.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = h.srv.Serve(ln) }()
	h.addr = h.bound
	return nil
}

func (h *httpServer) stop() {
	if h.srv == nil {
		return
	}
	c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = h.srv.Shutdown(c)
}

func download(url, out string) error {
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %s: a model that could not be fetched is not a model", resp.Status)
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return fmt.Errorf("download was interrupted: %w", err)
	}
	return f.Sync()
}

// cmdPreflight prints the full preflight table and refuses by name.
func cmdPreflight(args []string) error {
	flags, err := flagSet("preflight", map[string]string{"gate-d": "", "gate-d-age": "0s", "clock-state": "not verified", "power-state": ""}, args)
	if err != nil {
		return err
	}
	cfg, cfgErr := config.LoadEnv()
	if cfgErr != nil {
		fmt.Println("preflight: the environment is not usable, so the run is refused before any of it starts.")
		fmt.Println()
		fmt.Println(cfgErr)
		fmt.Println()
		fmt.Println("Every one of these is a value the pipeline would otherwise have to guess, and a")
		fmt.Println("guessed value in a reliability measurement is the defect the whole module exists")
		fmt.Println("to prevent.")
		return cfgErr
	}
	spec, err := source.Parse(cfg.SourceKind, cfg.SourceArg)
	if err != nil {
		return err
	}
	prober := source.Prober{Runner: source.ExecRunner{}, FFmpeg: "ffmpeg", FFprobe: "ffprobe",
		Segments: cfg.SegmentSeconds, Days: cfg.DiskBudgetDays}
	rep, probeErr := prober.Probe(ctx(), spec)

	facts := soak.StreamFacts{}
	if probeErr == nil && rep.VideoStream != nil {
		facts = soak.StreamFacts{Codec: rep.VideoStream.Codec, Width: rep.VideoStream.Width,
			Height: rep.VideoStream.Height, FPS: rep.VideoStream.FPS,
			BitRateBps: rep.BitRateBps, Assumed: rep.Disk.Assumed, DecodeRealTime: rep.Decode.Realtime}
	}
	free, _ := recorder.Free(cfg.DataRoot)
	aic, aierr := ai.New(cfg.AIAddr, cfg.AIDeadline)
	if aierr != nil {
		aic = nil
	}
	power := flags["power-state"]
	if power == "" {
		power = powerState()
	}
	gateD := flags["gate-d"]
	if gateD == "" {
		gateD = os.Getenv("SP_POC_GATE_D")
	}
	var gateDAge time.Duration
	fmt.Sscanf(flags["gate-d-age"], "%s", &gateDAge)

	res, err := soak.Preflight(ctx(), soak.PreflightInput{
		FFmpeg: "ffmpeg", FFprobe: "ffprobe", SourceReport: facts,
		FreeBytes: free, Headroom: cfg.DiskHeadroom, BudgetDays: cfg.DiskBudgetDays,
		DataRoot: cfg.DataRoot, Client: aic, SimURL: cfg.SimURL,
		// W1-4: which component is required depends on the trigger, and preflight
		// is told which rather than inferring it from whether an address was
		// configured — inference is how a row came to require a sidecar the run
		// does not have.
		TriggerMode: cfg.TriggerMode,
		VLMProvider: cfg.VLMProvider,
		VLMRegion:   cfg.VLMRegion,
		VLMHost:     vlmHost(cfg),
		// The clock row defaults to the run's own declared clock source, so an
		// operator who set SP_POC_CLOCK_SOURCE is not then refused by a second
		// flag for the same fact.
		ConfiguredClock: cfg.ClockSource,
		ClockState:      flags["clock-state"],
		PowerState:      power,
		GateDPrevious:   gateD, RehearsalAge: gateDAge,
	})
	if probeErr != nil {
		res.Summary = "preflight refuses to start: " + probeErr.Error()
		res.Pass = false
	}
	printPreflight(res)
	if !res.Pass {
		return fmt.Errorf("preflight refused to start. The run clock has not started and no data was written")
	}
	fmt.Println()
	fmt.Println("preflight: every check passes. `pocctl rehearse all` is still required before a")
	fmt.Println("long run: gate D is about surviving, and this table is about starting.")
	return nil
}

// vlmHost resolves the pinned host for the declared region, for the preflight row
// that tells an operator where their footage is about to go.
//
// It returns an empty string when there is no verifier, and preflight treats that
// as `none` rather than as a failure: a run that asks nobody is a legitimate
// configuration and the report labels it as unverified.
func vlmHost(cfg config.Config) string {
	if strings.EqualFold(strings.TrimSpace(cfg.VLMProvider), "none") || cfg.VLMProvider == "" {
		return ""
	}
	host, err := vlm.Region(strings.ToLower(strings.TrimSpace(cfg.VLMRegion))).Host()
	if err != nil {
		return ""
	}
	return host
}

func printPreflight(res soak.PreflightResult) {
	fmt.Println("# preflight")
	fmt.Println()
	fmt.Println("A run refuses to start while any row below fails. Each row says what breaks,")
	fmt.Println("because \"it failed\" is not an action.")
	fmt.Println()
	for _, c := range res.Checks {
		mark := "PASS"
		if !c.Pass {
			mark = "FAIL"
		}
		fmt.Printf("  [%s] %s\n", mark, c.Name)
		if c.Measured != "" {
			fmt.Printf("         measured: %s\n", c.Measured)
		}
		if c.Detail != "" {
			fmt.Printf("         detail:   %s\n", c.Detail)
		}
		fmt.Printf("         why:      %s\n", c.Why)
		fmt.Println()
	}
	fmt.Printf("  %s\n\n", res.Summary)
}

// powerState reads the machine's sleep policy. The wording matters: this row
// exists because a machine that sleeps in the middle of a seven-day run produces
// a week of dropouts that belong to the operating system.
func powerState() string {
	out, err := exec2("powercfg", "/a")
	if err != nil {
		return "ABSENT — powercfg could not be run: " + err.Error()
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var kept []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "The following") || strings.HasPrefix(l, "This machine") {
			continue
		}
		kept = append(kept, l)
	}
	if len(kept) == 0 {
		return "ABSENT — powercfg /a produced no lines"
	}
	return strings.Join(kept, "; ")
}

// cmdRehearse injects every rehearsable fault against a local simulator and
// records what happened. This is gate D, and it must be green before a long run
// starts, not after.
func cmdRehearse(args []string) error {
	// The optional fault name is pulled out BEFORE flagSet sees the arguments.
	//
	// `rehearse` has the shape `rehearse [all|<fault>]` — a positional — and the
	// parser refuses positionals, because a positional that reaches it means a
	// flag somewhere earlier swallowed the token that should have been its value.
	// That refusal is right; the fix is here and not there.
	only := ""
	rest := make([]string, 0, len(args))
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			rest = append(rest, a)
			continue
		}
		if only == "" {
			only = a
			continue
		}
		return fmt.Errorf("rehearse: %q is a second bare word. The shape is `pocctl rehearse [all|<fault>]`, so exactly one fault name is accepted", a)
	}
	flags, err := flagSet("rehearse", map[string]string{"data-root": "", "sim-addr": "127.0.0.1:8098"}, rest)
	if err != nil {
		return err
	}
	dataRoot := flags["data-root"]
	if dataRoot == "" {
		dataRoot = os.Getenv("SP_POC_DATA_ROOT")
	}
	if dataRoot == "" {
		return fmt.Errorf("rehearse: no data root. Pass --data-root or set SP_POC_DATA_ROOT: a rehearsal with nowhere to record its results cannot gate anything")
	}

	sim, stop, err := runSim(flags["sim-addr"])
	if err != nil {
		return err
	}
	defer stop()
	logf("xpsim is listening on %s (a simulator, not an XProtect server)", flags["sim-addr"])

	st, err := store.Open(dataRoot)
	if err != nil {
		return err
	}
	defer st.Close()

	runID := "rehearsal-" + time.Now().UTC().Format("20060102T150405")
	if err := st.StartRun(ctx(), store.Run{ID: runID, Label: "fault rehearsal", Mode: "live",
		SourceKind: "loop", SourceArg: "(none: this run injects faults into a simulator)",
		PushBaseURL: "http://127.0.0.1:1", ClockSource: "host",
		StartedAt: time.Now().UTC(), TargetSeconds: 60,
		Note: "gate D: the rehearsal itself"}); err != nil {
		return err
	}

	client, err := xprotect.New(xprotect.Config{BaseURL: "http://" + flags["sim-addr"], Timeout: 3 * time.Second,
		Credentials: xprotect.Credentials{ClientID: "GrantValidatorClient", ClientSecret: "rehearsal",
			Username: "rehearsal", Password: "rehearsal"},
		Logf: func(string, ...any) {}})
	if err != nil {
		return err
	}
	if err := client.Authenticate(ctx()); err != nil {
		return fmt.Errorf("rehearse: the simulator's token call failed, so nothing can be pushed against it: %w", err)
	}

	fmt.Println()
	fmt.Println("# fault rehearsal (gate D)")
	fmt.Println()
	fmt.Println("Each row is an injected fault and the outcome the client produced. The expectation")
	fmt.Println("is the client's contract, not the simulator's behaviour.")
	fmt.Println()

	type row struct {
		fault xpsim.Fault
		want  string
		why   string
	}
	rows := []row{
		{xpsim.FaultUnauthorized, "auth_failed", "a 401 refreshes the token once; a second 401 is a credential problem, not a stale token"},
		{xpsim.FaultForbidden, "push_refused", "a 403 is a definite no and is terminal for that detection"},
		{xpsim.FaultBadRequest, "push_refused", "a 400 is a definite no and is terminal for that detection"},
		{xpsim.FaultServerError, "retry", "a 5xx is worth another attempt"},
		{xpsim.FaultConnectionReset, "retry", "a reset connection is not a data problem; the outbox budget is what dead-letters it"},
		{xpsim.FaultTimeout, "retry", "a timeout must not wedge the worker"},
		{xpsim.FaultTruncatedJSON, "not-sent", "a 202 whose body arrived in half is not an accepted event"},
	}
	pass, fail := 0, 0
	for _, r := range rows {
		if only != "" && only != "all" && !strings.EqualFold(only, string(r.fault)) {
			continue
		}
		sim.Clear()
		// Scope every fault to the POST on /events, so the rehearsal measures the
		// push and not the token call or the read-back.
		if err := sim.ArmOn(r.fault, 0, "/API/rest/v1/events", "POST"); err != nil {
			return err
		}
		tag := "aicap-rehearsal-" + string(r.fault)
		out, err := client.TriggerEvent(ctx(), xprotect.EventInput{
			EventTypeGUID: "b4d1e1c2-0000-4000-8000-000000000001",
			SourceGUID:    "0d9a1b2c-1111-2222-3333-444455556666",
			At:            time.Now().UTC(), Tag: tag,
			Class: "bird", Value: "bird", Confidence: 0.9,
			Box:      xprotect.Box{Top: 10, Left: 20, Bottom: 60, Right: 80},
			RuleName: "rehearsal",
			// R8: the rehearsal is about the transport and the read-back, so its
			// fixture carries the attribution every pushed event now needs. Without
			// it the rehearsal would fail on a refusal that has nothing to do with the
			// fault being injected.
			Provenance: xprotect.Provenance{AIState: ai.StateNotAsked},
		})
		observed := classify(err, out)
		mark := "PASS"
		if observed != r.want {
			mark = "FAIL"
			fail++
		} else {
			pass++
		}
		fmt.Printf("  [%s] %-18s expected %-14s observed %-14s %s\n", mark, r.fault, r.want, observed, r.why)
		if err := st.RecordFault(ctx(), runID, store.Fault{
			At: time.Now().UTC(), Reason: "rehearsal:" + string(r.fault), Detail: r.why,
			Expected: r.want, Observed: observed,
		}); err != nil {
			return err
		}
	}
	sim.Clear()
	_ = st.EndRun(ctx(), runID, time.Now().UTC(), "rehearsal complete")

	fmt.Println()
	verdict := "PASS"
	if fail > 0 {
		verdict = "FAIL"
	}
	fmt.Printf("  gate D: %s (%d passed, %d failed), recorded in %s under run %s\n\n", verdict, pass, fail, st.Path(), runID)
	if fail > 0 {
		return fmt.Errorf("gate D is not green: %d rehearsed fault(s) behaved unexpectedly. A long run on code that does not survive a 500 measures the wrong thing", fail)
	}
	fmt.Println("  Export the verdict so preflight can see it:")
	fmt.Printf("    $env:SP_POC_GATE_D='%s'\n", verdict)
	return nil
}

func classify(err error, out xprotect.Outcome) string {
	if err == nil {
		switch out.State {
		case "push_sent":
			return "push_sent"
		case "push_refused":
			return "push_refused"
		case "unknown":
			return "not-sent"
		default:
			return out.State
		}
	}
	var auth *xprotect.ErrAuth
	if asAuth(err, &auth) {
		return "auth_failed"
	}
	var refused *xprotect.RefusedError
	if asRefused(err, &refused) {
		return "push_refused"
	}
	var retry *xprotect.RetryableError
	if asRetryable(err, &retry) {
		return "retry"
	}
	return "other"
}

func asAuth(err error, dst **xprotect.ErrAuth) bool {
	a, ok := err.(*xprotect.ErrAuth)
	if ok {
		*dst = a
	}
	return ok
}

func asRefused(err error, dst **xprotect.RefusedError) bool {
	a, ok := err.(*xprotect.RefusedError)
	if ok {
		*dst = a
	}
	return ok
}

func asRetryable(err error, dst **xprotect.RetryableError) bool {
	a, ok := err.(*xprotect.RetryableError)
	if ok {
		*dst = a
	}
	return ok
}

// cmdReport re-reads the store and prints the report, so a report can be
// regenerated without re-running anything.
func cmdReport(args []string) error {
	flags, err := flagSet("report", map[string]string{"data-root": "", "run": "", "json": boolSpec, "out": ""}, args)
	if err != nil {
		return err
	}
	root := flags["data-root"]
	if root == "" {
		root = os.Getenv("SP_POC_DATA_ROOT")
	}
	if root == "" {
		return fmt.Errorf("report: no data root. Pass --data-root or set SP_POC_DATA_ROOT")
	}
	// The root is checked BEFORE SQLite opens the file. SQLite reports a missing
	// directory as "out of memory (14)", which sends an operator looking for a
	// memory problem that does not exist and cannot be found.
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return fmt.Errorf("report: %s is not a directory, so there is no run to report. Check the path before reading anything else: SQLite would say `out of memory (14)` here, which is exactly the wrong thing to send an operator to", root)
	}
	st, err := store.OpenPath(filepath.Join(root, "poc.db"))
	if err != nil {
		return fmt.Errorf("report: %s holds no readable database, so there is nothing to report and no number to print. A silent empty report would be read as a run that recorded nothing: %w", root, err)
	}
	defer st.Close()
	c := ctx()
	id := flags["run"]
	if id == "" {
		run, err := st.LatestRun(c)
		if err != nil {
			return err
		}
		if run == nil {
			return fmt.Errorf("report: %s holds no run, so there is nothing to report. 'pocctl demo --clip <clip>' produces one in about three minutes", filepath.Join(root, "poc.db"))
		}
		id = run.ID
	}
	run, err := st.RunByID(c, id)
	if err != nil {
		return err
	}
	rep, err := reportFor(st, run, c)
	if err != nil {
		return err
	}
	if flags["json"] == "true" {
		b, err := rep.JSON()
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}
	md := rep.Markdown()
	if out := flags["out"]; out != "" {
		if err := os.WriteFile(out, []byte(md), 0o644); err != nil {
			return err
		}
		logf("wrote %s", out)
		return nil
	}
	fmt.Print(md)
	return nil
}

func cmdRun(args []string) error { return runForeground(args) }
