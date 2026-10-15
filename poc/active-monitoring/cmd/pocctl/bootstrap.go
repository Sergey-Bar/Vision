package main

// bootstrap: one command from a clean clone to a runnable demo.
//
// # Why this exists, and what it deliberately does not do
//
// The plan's B5. Before it, a judge who cloned the repository met a wall at
// `demo` step 1: the model is not in git, the runtime is not in git, the anchors
// file is not in git, and the clip this demo used to default to was not in git
// either. Four missing files and four separate answers, one of which required
// reading a formula out of a Python file and typing it into a terminal. The
// result was that the repository's own README called itself demonstrable and a
// fresh clone was not demonstrable at all.
//
// This command does the four fetches and says what it did and did not verify.
// It does NOT choose footage on the operator's behalf: the material step prints
// the committed table and the command refuses to pick one, which is B2, and it
// does not claim the run proves what a run over synthetic material proves
// nothing about.
//
// # What is verified and what is not
//
// The MODEL fetch is verified by digest, because the digests of the bytes this
// repository measured against are recorded in the licence file and the default
// URL's bytes were checked against them on 2026-10-11 (see cap.go).
//
// The RUNTIME fetch is verified when a digest is known — this machine's
// `models/runtime.json` records one — and is explicitly RECORDED-NOT-VERIFIED on
// a clean clone, where nothing is. `fetch-runtime` has no hardcoded default for
// that reason and still does not: a pinned digest is provenance and an unpinned
// one is a request for trust. Bootstrap passes the URL it was given and lets the
// two-step print exactly which of the two happened.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/Sergey-Bar/vision-poc/internal/soak"
)

func cmdBootstrap(args []string) error {
	flags, err := flagSet("bootstrap", map[string]string{
		"model": "", "runtime-url": "", "skip-model": boolSpec, "skip-runtime": boolSpec,
	}, args)
	if err != nil {
		return err
	}
	root := findModuleRoot()
	if root == "" {
		root = repoRoot()
	}

	fmt.Println("bootstrap — a clean clone to a runnable demo, one step at a time")
	fmt.Println()
	fmt.Println("  Every step prints what it VERIFIED and what it only RECORDED. A step that")
	fmt.Println("  cannot finish names what it leaves undone, because a bootstrap that reported")
	fmt.Println("  success with something missing would be the same defect in a new place.")
	fmt.Println()

	var failed []string

	if err := bootstrapToolchain(); err != nil {
		failed = append(failed, err.Error())
	}

	if flags["skip-model"] != "true" {
		if err := bootstrapModel(root, flags["model"]); err != nil {
			failed = append(failed, "the model: "+err.Error())
		}
	} else {
		step(2, 5, "the model")
		fmt.Println("  SKIPPED by --skip-model. A demo without the model has no detector and")
		fmt.Println("  `pocctl detector --selftest` will say NOT RUN with that reason.")
		fmt.Println()
	}

	if flags["skip-runtime"] != "true" {
		if err := bootstrapRuntime(root, flags["runtime-url"]); err != nil {
			failed = append(failed, "the runtime: "+err.Error())
		}
	} else {
		step(3, 5, "the ONNX runtime")
		fmt.Println("  SKIPPED by --skip-runtime. `pocctl fetch-runtime --url <archive> --sha256 <sum>`")
		fmt.Println("  remains, and the detectors that need it will refuse by name.")
		fmt.Println()
	}

	step(4, 5, "the material")
	printCommittedMaterial()
	fmt.Println()

	step(5, 5, "the truth line")
	fmt.Println("  What this bootstrap produced is a machine that CAN run the demo, and nothing")
	fmt.Println("  has been claimed about what the demo shows yet. When it runs:")
	fmt.Println("    push target: local simulator (xpsim). No XProtect server was reachable during this run.")
	fmt.Println("    source: the clip named by --clip, as recorded in testdata/labels.json.")
	fmt.Println()
	fmt.Println("  What a run over the committed material does NOT prove: the clips are synthetic")
	fmt.Println("  rectangles a COCO model returns zero boxes on (measured, three instants), so")
	fmt.Println("  AC2/AC3/AC4 come out NOT RUN with that reason — honestly empty rather than")
	fmt.Println("  quietly passing. This command is waiting on B1 exactly as the demo is: a clip of")
	fmt.Println("  real motion whose licence row can be stated in full.")
	fmt.Println()
	fmt.Println("  Next:")
	fmt.Println("    pocctl demo --clip testdata/car-crossing.mp4    (the synthetic clip with a recorded")
	fmt.Println("                                                      licence; a run over it is honestly")
	fmt.Println("                                                      empty — NOT RUN, not a quiet road)")
	fmt.Println("    pocctl demo --live                              (a real camera; no clip, no model)")
	fmt.Println()

	if len(failed) > 0 {
		for _, f := range failed {
			fmt.Printf("  NOT DONE: %s\n", f)
		}
		return fmt.Errorf("bootstrap: %d step(s) did not finish. What they left undone is listed above, and a "+
			"bootstrap that reported success here would be reporting a machine that cannot run the demo", len(failed))
	}
	return nil
}

// bootstrapToolchain is the same capability question the demo asks, asked before
// anything is fetched rather than at the door of a run.
func bootstrapToolchain() error {
	step(1, 5, "the toolchain")
	present, missing := toolchainReport(false)
	for _, p := range present {
		fmt.Printf("  %-10s %s\n", p.name, p.detail)
	}
	if len(missing) > 0 {
		for _, m := range missing {
			fmt.Printf("  MISSING     %s\n", m)
			fmt.Printf("              %s\n", installHintFor(m))
		}
		fmt.Println("  ffmpeg and ffprobe are hard dependencies: without them there is no source, no")
		fmt.Println("  recording and no frames. Everything else this bootstrap does does not need them,")
		fmt.Println("  so the fetch steps below still run — but the demo does not, and this command")
		fmt.Println("  refuses to end reporting a machine that can.")
		fmt.Println()
		return fmt.Errorf("%d tool(s) missing", len(missing))
	}
	fmt.Println()
	return nil
}

// foundTool is one tool that is on PATH, with where it was found.
type foundTool struct {
	name   string
	detail string
}

// missingTool is one tool that is not, with what breaks.
type missingTool struct {
	name string
	why  string
}

// toolchainReport is the capability table the demo and bootstrap share.
//
// It is one function because two commands that ask the same question differently
// produce two answers, and one of them is the one the operator reads when they
// are deciding which to trust.
func toolchainReport(wantBuild bool) (present []foundTool, missing []missingTool) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		path, err := exec.LookPath(tool)
		if err != nil {
			missing = append(missing, missingTool{name: tool, why: "a hard dependency (D2)"})
			continue
		}
		present = append(present, foundTool{name: tool, detail: path})
	}
	// Python is a NOTE, not a refusal, and the difference is the trigger mode: the
	// shipped trigger is the bundled ONNX detector and needs no sidecar.
	if _, err := exec.LookPath("py"); err != nil {
		if _, err2 := exec.LookPath("python"); err2 != nil {
			present = append(present, foundTool{name: "python", detail: "ABSENT — a note, not a refusal: the shipped trigger needs no sidecar"})
		}
	}
	if wantBuild {
		if path, err := exec.LookPath("go"); err != nil {
			missing = append(missing, missingTool{name: "go", why: "step 2 builds the binaries with it"})
		} else {
			present = append(present, foundTool{name: "go", detail: path})
		}
	}
	return present, missing
}

func installHintFor(m missingTool) string {
	return installHint(m.name)
}

// bootstrapModel fetches the weights with the pinned default and its digest check.
//
// It delegates to `cmdFetchModel` rather than implementing a second fetch: the
// pinned default, the warning-not-wall semantics and the provenance print all
// live there, and a bootstrap that fetched slightly differently would be a second
// place for them to be wrong.
func bootstrapModel(root, model string) error {
	step(2, 5, "the model")
	if model == "" {
		model = filepath.Join(root, "models", "yolov4n.onnx")
	}
	if info, err := os.Stat(model); err == nil && info.Size() > 0 {
		sum, size, herr := hashFile(model)
		switch {
		case herr != nil:
			return herr
		case sum == defaultModelSHA256:
			fmt.Printf("  PASS  %s is on disk and matches the pinned digest (%s).\n", model, soak.Bytes(size))
			fmt.Println("        No download was needed.")
			// The anchors, in the branch that downloads nothing. This box had
			// exactly that gap: the weights on disk, the anchors nowhere,
			// because nothing that put the weights there wrote them. The
			// sidecar refused by name at load and the demo could not decode a
			// frame. See cmd/pocctl/anchors.go.
			p, aerr := writeAnchorsBeside(model)
			if aerr != nil {
				return fmt.Errorf("the weights are the identified model but their anchors file is not: %w", aerr)
			}
			fmt.Printf("  PASS  %s is the model's [anchors] section, digest-checked.\n", p)
			fmt.Println()
			return nil
		default:
			fmt.Printf("  %s is on disk but does NOT match the pinned digest (%s).\n", model, sum)
			fmt.Println("  It is kept, and the pinned download is written beside it only if you say so:")
			fmt.Println("  the file on disk may be a build somebody chose deliberately.")
			fmt.Println()
			return nil
		}
	}
	fmt.Printf("  fetching to %s\n", model)
	if err := cmdFetchModel([]string{"--out", model}); err != nil {
		return err
	}
	fmt.Println()
	return nil
}

// bootstrapRuntime fetches the ONNX Runtime library, and says which of the two
// verification states it reached.
func bootstrapRuntime(root, url string) error {
	step(3, 5, "the ONNX runtime")
	lib := filepath.Join(root, filepath.FromSlash(defaultRuntimePath()))
	manifest := filepath.Join(root, "models", "runtime.json")

	// What is ALREADY here wins, and it wins with its recorded digest: the
	// manifest is the authority for what this machine installed, and re-fetching
	// over it would discard the one verification that has happened.
	if info, err := os.Stat(lib); err == nil && info.Size() > 0 {
		if _, recsErr := readRuntimeRecords(manifest); recsErr == nil {
			if verr := verifyRuntime(manifest, lib); verr != nil {
				return fmt.Errorf("the recorded runtime at %s does not match models/runtime.json: %w. "+
					"Re-fetch deliberately rather than let a load resolve to a library nobody checked", lib, verr)
			}
			fmt.Printf("  PASS  %s is on disk and matches its recorded digest (%s).\n", lib, soak.Bytes(info.Size()))
			fmt.Println("        models/runtime.json is the authority for what this machine runs.")
			fmt.Println()
			return nil
		}
		fmt.Printf("  PASS  %s is on disk (%s). Nothing records what it is, so nothing is verified:\n",
			lib, soak.Bytes(info.Size()))
		fmt.Println("        `pocctl fetch-runtime --verify` after recording it, or re-fetch with --sha256.")
		fmt.Println()
		return nil
	}

	// A recorded row for THIS platform with a digest is a re-fetch of a measured thing.
	if recs, rerr := readRuntimeRecords(manifest); rerr == nil {
		for _, r := range recs {
			if r.Platform == runtime.GOOS+"/"+runtime.GOARCH && r.SHA256 != "" && r.Kind == "onnxruntime" {
				fmt.Printf("  models/runtime.json records this platform's runtime from %s\n", r.Source)
				fmt.Println("  Re-fetching it and checking the recorded digest.")
				fmt.Println()
				if err := cmdFetchRuntime([]string{"--url", r.Source, "--sha256", r.SHA256, "--out", lib}); err != nil {
					return err
				}
				fmt.Println()
				return nil
			}
		}
	}

	// A clean clone: the per-platform release asset, named and cited, with no
	// digest claimed.
	if url == "" {
		var ok bool
		url, ok = defaultRuntimeURL()
		if !ok {
			return fmt.Errorf("no runtime URL for %s/%s. The ONNX Runtime release publishes a CPU asset "+
				"per platform and this one has none: onnxruntime %s publishes %s. Pass --runtime-url, or "+
				"download the archive for your platform and pass its URL", runtime.GOOS, runtime.GOARCH,
				runtimeReleaseVersion, "win-x64, linux-x64 and osx-arm64 only (checked 2026-10-11)")
		}
	}
	fmt.Printf("  models/ holds no runtime and models/runtime.json records none, so the release\n")
	fmt.Printf("  asset for this platform is used, with NO digest claimed:\n    %s\n", url)
	fmt.Println("  The URLs and the version were read off the release's own asset list on 2026-10-11;")
	fmt.Println("  the version is named so the URL is reproducible. What arrives is RECORDED, NOT")
	fmt.Println("  VERIFIED until you pass --sha256 — read the sum from the release page and re-run")
	fmt.Println("  `pocctl fetch-runtime --url <url> --sha256 <sum>`. That is the two-step this")
	fmt.Println("  repository designed, and a clean clone cannot skip it honestly.")
	fmt.Println()
	if err := cmdFetchRuntime([]string{"--url", url, "--out", lib, "--licence", "MIT"}); err != nil {
		return err
	}
	fmt.Println()
	return nil
}

// runtimeReleaseVersion is the release every default URL names.
//
// It is a named version rather than `latest` because a fetch that resolves to
// whatever is current at the moment of cloning produces a different library on
// two days, and the manifest's digests are about one release. `fetch-runtime
// --url` overrides it, and the recorded manifest on a machine that has one wins
// over it entirely.
const runtimeReleaseVersion = "1.29.0"

// defaultRuntimeURL is the CPU release asset for this platform.
//
// Every URL here was checked against the release's own asset list on 2026-10-11
// (the two candidates that matched the documented naming pattern but did not
// exist — `onnxruntime-win-x64-cpu-1.29.0.zip`, `onnxruntime-osx-x86_64-1.29.0.tgz`
// — were the ones that 404'd, and are named so the check is repeatable). No
// digest is claimed for any of them: this repository has not measured the CPU
// package, and bootstrap does not invent verification it cannot do.
func defaultRuntimeURL() (string, bool) {
	const rel = "https://github.com/microsoft/onnxruntime/releases/download/v" + runtimeReleaseVersion + "/"
	switch runtime.GOOS {
	case "windows":
		return rel + "onnxruntime-win-x64-" + runtimeReleaseVersion + ".zip", true
	case "darwin":
		// No osx-x86_64 asset exists for this release; macOS is arm64 only here.
		if runtime.GOARCH != "arm64" {
			return "", false
		}
		return rel + "onnxruntime-osx-arm64-" + runtimeReleaseVersion + ".tgz", true
	case "linux":
		if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
			return "", false
		}
		return rel + "onnxruntime-linux-x64-" + runtimeReleaseVersion + ".tgz", true
	}
	return "", false
}
