// Command vms-emulator is a VMS you can play video into.
//
// It plays local files, an RTSP camera or a YouTube URL as virtual cameras,
// runs a detector over the frames on the GPU, publishes what it finds the way a
// video management system publishes its own analytics, and serves a media
// surface the platform can cut clips from.
//
// It exists because the product's Milestone adapter has never spoken to
// anything. The seam is real; what is missing is the other end of it.
//
// # The vendor surface
//
// The API is at `/api/rest/v1`, which is the prefix the vendor documents and
// the prefix the adapter asks for. The BASE URL is the site root —
// `http://127.0.0.1:8010` — because the adapter appends the prefix itself, and
// a base that already carries it asks for `/api/api/rest/v1`.
//
// # The control surface
//
// `/__control` is how a console drives a VMS: list the cameras, read the live
// frame and the boxes on it, read what the engine measured. It is outside the
// vendor's prefix on purpose, so nothing can mistake it for a vendor route.
//
// # What it does not do
//
// It does not implement the vendor's whole API, and it does not pretend to. The
// routes it serves are the ones the adapter reads and the probe checks; the
// rest answer an empty array, which is the honest answer for a device that has
// the route and nothing to put in it.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Sergey-Bar/vision/internal/classify"
	"github.com/Sergey-Bar/vision/internal/emulator"
	"github.com/Sergey-Bar/vision/internal/ortdetect"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "vms-emulator: "+err.Error())
		os.Exit(1)
	}
}

// run dispatches the subcommands.
//
// The dispatch is on the first argument and nothing else, because a flag
// parser that also reads a subcommand is a flag parser whose help text lies
// about what it accepts.
func run(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "fetch-model":
			return runFetchModel(args[1:])
		case "fetch-runtime":
			return runFetchRuntime(args[1:])
		case "help", "-h", "--help":
			usage()
			return nil
		}
	}
	return runServe(args)
}

// usage prints the help.
func usage() {
	fmt.Fprint(os.Stderr, `vms-emulator — a VMS you can play video into

  vms-emulator [flags]              play the cameras and serve the vendor surface
  vms-emulator fetch-model [flags]   fetch a digest-pinned detector model
  vms-emulator fetch-runtime [flags] fetch the ONNX Runtime the detector needs

Run "vms-emulator <command> -h" for a command's flags.
`)
}

// sourceFlags collects the repeatable --source flag.
type sourceFlags []string

// String renders the collected sources.
func (s sourceFlags) String() string { return strings.Join(s, ",") }

// Set collects one more source.
func (s *sourceFlags) Set(value string) error {
	*s = append(*s, value)
	return nil
}

// runServe starts the emulator.
func runServe(args []string) error {
	fs := flag.NewFlagSet("vms-emulator", flag.ContinueOnError)
	var sources sourceFlags
	fs.Var(&sources, "source", "a camera, as id=kind:location. Repeatable. kind is file, url, rtsp or youtube")
	listen := fs.String("listen", envOr("SP_VMS_LISTEN", "127.0.0.1:8010"), "host:port for the vendor surface")
	token := fs.String("token", envOr("SP_VMS_TOKEN", ""), "the bearer credential the surface requires")
	model := fs.String("model", envOr("SP_DET_MODEL", ""), "the ONNX model to run")
	ortLib := fs.String("ort-lib", envOr("SP_ORT_LIB", ""), "the ONNX Runtime shared library")
	provider := fs.String("provider", envOr("SP_ORT_EP", "cuda"), "the execution provider: cuda or cpu")
	device := fs.Int("cuda-device", 0, "which GPU, zero-based")
	detInput := fs.Int("det-input", 640, "the size the model is given. A dynamic export takes whatever the run asks for")
	threads := fs.Int("threads", 4, "the inference threads. Recorded with every latency, because a p99 without a thread count describes no build")
	fps := fs.Float64("fps", emulator.DefaultFPS, "frames per second measured per camera")
	width := fs.Int("width", emulator.DefaultWidth, "the detection width")
	height := fs.Int("height", emulator.DefaultHeight, "the detection height")
	clipDir := fs.String("clip-dir", envOr("SP_VMS_CLIP_DIR", ""), "where cut clips and snapshots are written")
	loop := fs.Bool("loop", true, "play a source again when it ends")
	vendor := fs.String("vendor", "vision-detector/yolo11", "the provenance published with every analytics event")
	prompt := fs.String("prompt", "", "the class prompt, for a model whose classes are named by a prompt. It is recorded with the vocabulary, because a measurement whose class list is not reproducible is not a measurement")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(sources) == 0 {
		return errors.New("no cameras were given. Pass --source id=file:Videos/Shoppers.mp4, once per camera")
	}

	parsed := make([]emulator.Source, 0, len(sources))
	for _, raw := range sources {
		src, err := parseSource(raw, *fps)
		if err != nil {
			return err
		}
		parsed = append(parsed, src)
	}

	engineOpts := emulator.Options{
		Listen:  *listen,
		Token:   *token,
		Sources: parsed,
		Vendor:  *vendor,
		Width:   *width,
		Height:  *height,
		ClipDir: *clipDir,
		Loop:    *loop,
		Logger:  slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})),
	}

	if strings.TrimSpace(*model) != "" {
		detector, sess, vocabulary, err := openDetector(*model, *ortLib, *provider, *device, *threads, *detInput, *prompt)
		if err != nil {
			return err
		}
		defer func() { _ = sess.Close() }()
		engineOpts.Detector = detector
		engineOpts.Vocabulary = vocabulary
	}

	engine, err := emulator.New(engineOpts)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return engine.Run(ctx)
}

// parseSource reads one --source value.
//
// The form is `id=kind:location`, because a camera needs a name the platform
// can record and a place to read from, and a bare path would leave the first
// unnamed.
func parseSource(raw string, fps float64) (emulator.Source, error) {
	id, rest, found := strings.Cut(raw, "=")
	if !found || strings.TrimSpace(id) == "" {
		return emulator.Source{}, fmt.Errorf("--source %q is not id=kind:location", raw)
	}
	kindText, location, found := strings.Cut(rest, ":")
	if !found || strings.TrimSpace(location) == "" {
		return emulator.Source{}, fmt.Errorf("--source %q has no location", raw)
	}
	kind := emulator.Kind(strings.ToLower(strings.TrimSpace(kindText)))
	switch kind {
	case emulator.KindFile, emulator.KindURL, emulator.KindRTSP, emulator.KindYouTube:
	case "":
		kind = emulator.KindFile
	default:
		return emulator.Source{}, fmt.Errorf("--source %q has kind %q, which is not one of file, url, rtsp or youtube", raw, kind)
	}
	return emulator.Source{
		ID:       strings.TrimSpace(id),
		Name:     strings.TrimSpace(id),
		Kind:     kind,
		Location: strings.TrimSpace(location),
		FPS:      fps,
		Loop:     true,
	}, nil
}

// openDetector opens the model and builds the detector over it.
//
// The provider is chosen explicitly and never defaulted: a run that silently
// fell back to the CPU because the GPU was busy would report the CPU's latency
// under the GPU's name, which is the exact shape of the defect the detector's
// own preflight exists to prevent.
//
// The preflight runs FIRST, and a failure in it stops the run. A preflight that
// opens a session is not a preflight, it is the thing that failed, and by then
// it has taken the process's ONNX Runtime environment with it.
func openDetector(model, ortLib, provider string, device, threads, detInput int, prompt string) (*ortdetect.Detector, *ortdetect.Session, *ortdetect.Vocabulary, error) {
	if strings.TrimSpace(ortLib) == "" {
		return nil, nil, nil, fmt.Errorf("no ONNX Runtime library was named. Pass --ort-lib, or set SP_ORT_LIB. " +
			"Run `vms-emulator fetch-runtime` to obtain one")
	}
	if err := ortdetect.PrependLibraryDirToPATH(ortLib); err != nil {
		return nil, nil, nil, err
	}
	normalized, err := ortdetect.ValidateEP(provider)
	if err != nil {
		return nil, nil, nil, err
	}
	sessionCfg := ortdetect.SessionConfig{
		LibraryPath:       ortLib,
		ModelPath:         model,
		ExecutionProvider: normalized,
		CUDADeviceID:      device,
		Threads:           threads,
	}
	for _, check := range ortdetect.Preflight(sessionCfg) {
		if check.State == "fail" {
			return nil, nil, nil, fmt.Errorf("the preflight refused this run: %s: %s", check.Name, check.Detail)
		}
	}
	sess, err := ortdetect.Open(sessionCfg)
	if err != nil {
		return nil, nil, nil, err
	}
	input := sess.Input()
	// The input size is the model's, unless the export declares a dynamic one.
	// A dynamic export takes whatever the run asks for, and the run's number is
	// then the fact that gets recorded.
	size := input.W
	if input.Dynamic || size <= 0 {
		size = detInput
	}
	// The class list is a vocabulary rather than a constant: the names the model
	// declared, and the FAMILIES the product groups them into. The product's
	// taxonomy is the one that knows what a family is; the detector only carries
	// what it was told.
	vocabulary := ortdetect.COCOVocabulary()
	families := classify.FamiliesFor(vocabulary.Names)
	if strings.TrimSpace(prompt) != "" {
		// A model whose classes are named by a PROMPT has a vocabulary that
		// records the prompt, because a measurement whose class list is not
		// reproducible is not a measurement.
		vocabulary = ortdetect.OpenVocabulary(prompt, vocabulary.Names, families)
	} else {
		vocabulary.Families = families
	}
	detector, err := ortdetect.NewDetector(ortdetect.Config{
		Session:    sess,
		InputSize:  size,
		Vocabulary: vocabulary,
		Decode: ortdetect.DecodeConfig{
			EmitScore:  0.25,
			MaxBoxes:   64,
			NMSClass:   0.45,
			NMSGlobal:  0.75,
			MinBoxArea: 0.0005,
		},
	})
	if err != nil {
		_ = sess.Close()
		return nil, nil, nil, err
	}
	return detector, sess, vocabulary, nil
}

// envOr reads an environment variable or takes a default.
func envOr(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

// absPath makes a path absolute, for the messages that name a file.
func absPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// unusedHelpers keeps the imports honest in the builds that do not use them.
var (
	_ = absPath
	_ = strconv.Itoa
	_ = time.Second
)
