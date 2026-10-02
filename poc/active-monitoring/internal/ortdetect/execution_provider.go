// Execution-provider selection, in the part that is pure Go.
//
// The CPU is the default and NOT a fallback. That distinction is the whole file.
//
// ONNX Runtime runs a session on the CPU when the CUDA provider cannot be used — the
// library was built without it, the CUDA/cuDNN version does not match what the
// runtime was built against, or no device is visible to the process — and that
// fallback is a log line at session creation, not an error the caller receives. So a
// run configured for a GPU can come back with the CPU's latency, the CPU's memory
// profile and the CPU's detection rate while every recorded field says `cuda`. That
// is worse than a failure, because a failure is visible: this one is a green run with
// numbers from the wrong machine.
//
// Two consequences, and they are why this is not three lines:
//
//   - `cuda` is CHECKED BEFORE THE SESSION and REFUSED when it cannot be honoured.
//     Nothing is attempted and then assumed to have worked.
//
//   - The provider recorded against every measurement is the CONFIGURED one, and the
//     device was confirmed present before the session was opened.
//     `onnxruntime_go` v1.35.0 exposes no read-back of a session's active providers,
//     so a read-back after the fact is not available and pretending otherwise would be
//     worse than saying so here. What can be verified before the session is verified
//     before it.
//
// This file has NO build tag on purpose. The vocabulary, the normalisation, the
// refusals and the pre-flight check need no C toolchain, so a machine without one
// still gets the whole of them — and a misspelled SP_POC_ORT_EP is refused with the
// same sentence in both builds, rather than only where ORT happens to be present.

package ortdetect

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// The closed set of providers this build performs. Strings and not an enum because
// they come from environment variables and a refusal has to name the value the
// operator typed.
const (
	EPProviderCPU  = "cpu"
	EPProviderCUDA = "cuda"
)

// nvidiaSMI is the command that lists the GPUs this process can see.
//
// It is a CONSTANT and not derived from PATH, so that the refusal can print the
// command the operator should run and that command is the one that was actually run. A
// refusal that says "check your GPU setup" and a check that runs something else is a
// refusal that cannot be acted on.
const nvidiaSMI = "nvidia-smi"

// deviceLister returns the CUDA device indices this process can see.
//
// It is a parameter and not a direct call, so that the check is testable without a
// GPU. A check for a GPU that only runs on a machine with a GPU is a check this
// repository cannot run, which means it is a check nobody runs.
type deviceLister func() ([]int, error)

// NormalizeEP resolves what was configured into one of the two, and returns "" for
// anything else.
//
// The refusal belongs at the CALLER, and that is why this returns "" rather than an
// error: [ValidateEP] is the version with one. The three-word default is the failure
// this file exists to prevent — a misspelled `SP_POC_ORT_EP=CUDA_EP` silently becoming
// `cpu` produces a run that is correct, slower than expected, and labelled with a
// provider nobody asked for.
//
// The spellings accepted beyond the two canonical ones exist because CUDA users write
// `gpu` and the ONNX documentation writes `CUDAExecutionProvider`. They normalise
// rather than being refused, and that is a deliberate exception: an alias naming the
// same device is a synonym, while an unknown word is a mistake with a different cause.
func NormalizeEP(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", EPProviderCPU, "cpu_ep", "cpuexecutionprovider":
		return EPProviderCPU
	case EPProviderCUDA, "cuda_ep", "cudaexecutionprovider":
		return EPProviderCUDA
	case "gpu":
		return gpuMeansCUDA()
	default:
		return ""
	}
}

// gpuMeansCUDA is one line, and on its own, so that RC-28 can change exactly one line.
//
// It is a named function rather than `return EPProviderCUDA` inline because that
// literal appears in two arms of the switch above: a control whose removal line is not
// unique is a control whose mutation is ambiguous, and the gate substitutes strings
// rather than editing AST. It is also the honest shape for the decision — "gpu means
// CUDA in this build" is a claim somebody could reasonably disagree with, and it
// deserves a name they can point at.
func gpuMeansCUDA() string { return EPProviderCUDA }

// ValidateEP is [NormalizeEP] with a refusal attached, so no caller has to know that
// an empty return means "unknown" rather than "the default".
func ValidateEP(s string) (string, error) {
	if ep := NormalizeEP(s); ep != "" {
		return ep, nil
	}
	return "", &Refusal{
		What: sprintf("SP_POC_ORT_EP=%q is not an execution provider this build performs", strings.TrimSpace(s)),
		Detail: "it is `cpu` or `cuda`, and nothing else. An unknown value is refused rather than treated as the " +
			"default, because ONNX Runtime falls back to the CPU when the CUDA provider cannot be used and says " +
			"so in a log line rather than in an error — so a typo would produce a correct, slower run labelled " +
			"with a provider nobody asked for. Set SP_POC_ORT_EP=cpu to accept a slower run on the CPU " +
			"deliberately",
	}
}

// DeviceCheck is what the pre-flight actually saw.
//
// It is returned rather than only an error, because "there is no nvidia-smi on PATH",
// "the driver is installed and sees no device" and "the device you asked for is not
// one of them" are three different problems with three different fixes, and a single
// boolean would collapse them into one sentence that names none of them.
type DeviceCheck struct {
	Visible []int
	Err     error
}

// checkCUDA confirms the configured device is visible before any session is opened.
func checkCUDA(deviceID int, list deviceLister) (DeviceCheck, error) {
	if list == nil {
		list = visibleCUDADevices
	}
	ids, err := list()
	if err != nil {
		return DeviceCheck{Err: err}, &Refusal{
			What: sprintf("SP_POC_ORT_EP=cuda was configured and the CUDA devices could not be listed (%s --query-gpu=index --format=csv,noheader)", nvidiaSMI),
			Detail: sprintf("%v. This is refused before a session is opened rather than run on the CPU on "+
				"purpose: the run would produce real numbers from the wrong machine and every recorded field "+
				"would still say `cuda`. If `%s` is not on PATH then the NVIDIA driver is not installed; if it "+
				"runs and prints nothing then the driver sees no device. Set SP_POC_ORT_EP=cpu to accept a "+
				"slower run on the CPU deliberately", err, nvidiaSMI),
		}
	}
	if len(ids) == 0 {
		return DeviceCheck{}, &Refusal{
			What: sprintf("SP_POC_ORT_EP=cuda was configured and %s reported no CUDA device", nvidiaSMI),
			Detail: "the runtime would fall back to the CPU and every recorded field would still say `cuda`, so " +
				"the run is refused instead. Check the driver with `nvidia-smi`, and set SP_POC_ORT_EP=cpu to " +
				"accept a slower run on the CPU deliberately",
		}
	}
	for _, id := range ids {
		if id == deviceID {
			return DeviceCheck{Visible: ids}, nil
		}
	}
	return DeviceCheck{Visible: ids}, &Refusal{
		What: sprintf("SP_POC_ORT_CUDA_DEVICE=%d names no CUDA device", deviceID),
		Detail: sprintf("%s sees device(s) %s. CUDA numbers devices from zero, so the id has to be one of "+
			"them; pick one with `%s --query-gpu=index,name --format=csv`. Set SP_POC_ORT_EP=cpu to accept a "+
			"slower run on the CPU deliberately", nvidiaSMI, formatInts(ids), nvidiaSMI),
	}
}

// visibleCUDADevices is the real check: it parses `nvidia-smi`'s own output rather than
// asking ORT, because ORT's answer is the log line this whole file exists not to have
// to trust.
func visibleCUDADevices() ([]int, error) {
	out, err := exec.Command(nvidiaSMI, "--query-gpu=index", "--format=csv,noheader").Output()
	if err != nil {
		return nil, err
	}
	var ids []int
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		n, cerr := strconv.Atoi(line)
		if cerr != nil {
			return nil, cerr
		}
		ids = append(ids, n)
	}
	return ids, nil
}

// providerDLLChecks reports whether the DLLs a CUDA session needs are BESIDE the
// library.
//
// Beside, and not merely on PATH, because that is where Windows looks first and
// because "it worked for me because my PATH had it" is not a property of a build
// anybody can reproduce. ONNX Runtime loads `onnxruntime_providers_cuda.dll` from its
// own directory at session-creation time, which is exactly where the CPU-only
// library has no provider at all and the GPU package has one this module has to carry
// next to the library or not at all.
func providerDLLChecks(libraryPath string) []Check {
	var out []Check
	dir := filepath.Dir(libraryPath)
	// ABSOLUTE, before anything compares it with a PATH entry.
	//
	// `SP_POC_ORT_LIB=models\onnxruntime.dll` is what the README tells an operator to
	// set, so `filepath.Dir` gives a RELATIVE directory, and comparing that with the
	// absolute entries in PATH never matches. The check then reports "your directory is
	// not on PATH" for a machine where it very nearly is, and prints the remedy as a
	// relative path that would not fix it if followed literally.
	//
	// Resolving first costs one call and removes the whole class. It cannot fail for a
	// library that exists, which is the only case this function runs in.
	if abs, err := filepath.Abs(libraryPath); err == nil {
		dir = filepath.Dir(abs)
	}
	prov := filepath.Join(dir, "onnxruntime_providers_cuda.dll")
	if _, err := os.Stat(prov); err != nil {
		out = append(out, Check{Name: "CUDA provider DLL", State: StateFail,
			Detail: sprintf("%s is absent. The package this came from is either the CPU-only build, or the "+
				"provider was not extracted beside the library. Re-run `pocctl fetch-runtime` against the "+
				"`-gpu` asset with --provider=keep", prov)})
	} else {
		out = append(out, Check{Name: "CUDA provider DLL", State: StatePass, Detail: prov})
	}
	// cuDNN 9 is the other half of the provider's imports and it is NOT shipped with
	// ONNX Runtime. Its absence is the single most common cause of a CUDA session
	// that opens and then runs on the CPU, so it is checked here.
	//
	// RESOLVABILITY, not presence. This row originally passed on "the file is there"
	// and was wrong: ONNX Runtime loads cuDNN by BARE NAME through the OS loader, and
	// the OS loader searches the executable's directory and PATH — not the directory
	// the calling module happens to live in. So a cuDNN copied beside onnxruntime.dll
	// is found by this check, not found by the runtime, and the session fails at the
	// first Conv node with `LoadLibrary failed for cudnn64_9.dll with error 2`. A
	// diagnostic that reports PASS for the condition that causes the failure is worse
	// than no diagnostic, so the row now tests what is actually needed.
	if p := lookPathAnywhere("cudnn64_9.dll", dir); p == "" {
		out = append(out, Check{Name: "cuDNN 9", State: StateFail,
			Detail: "cudnn64_9.dll was not found beside the runtime library or on PATH. ONNX Runtime does not " +
				"ship it; install the CUDA 12 cuDNN 9 redistributable and put its directory on PATH, " +
				"because the runtime loads cuDNN by bare name rather than from beside its own library"})
	} else {
		state, detail := StatePass, p
		if !onPath(dir) {
			state = StateFail
			detail = p + " — present, but its directory is NOT on PATH. ONNX Runtime loads cuDNN by bare " +
				"name, so the OS loader looks in the running program's directory and on PATH and not " +
				"beside onnxruntime.dll; the session will open and then fail at the first Conv node with " +
				"\"LoadLibrary failed for cudnn64_9.dll with error 2\". Add " + dir + " to PATH"
		}
		out = append(out, Check{Name: "cuDNN 9", State: state, Detail: detail})
	}
	return out
}

// onPath reports whether a directory is on PATH as an entry, matched case-insensitively
// and with trailing separators normalised, because PATH on Windows is a long string
// assembled by hand and every one of those differences is a way for it to be "on PATH"
// to a human and not to the loader.
func onPath(dir string) bool {
	if dir == "" {
		return false
	}
	want := strings.ToUpper(strings.TrimRight(filepath.Clean(dir), `\/`))
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if strings.TrimRight(filepath.Clean(p), `\/`) == "" {
			continue
		}
		if strings.ToUpper(strings.TrimRight(filepath.Clean(p), `\/`)) == want {
			return true
		}
	}
	return false
}

// lookPathAnywhere finds a DLL beside a library or on PATH, and reports which of the
// two it found — because "on PATH" is a thing that has to be repeated on every shell
// and "beside the library" is a thing that travels with the build.
func lookPathAnywhere(dll, dir string) string {
	if dir != "" {
		if p := filepath.Join(dir, dll); fileExists(p) {
			return p + " (beside the runtime library)"
		}
	}
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if p == "" {
			continue
		}
		if cand := filepath.Join(p, dll); fileExists(cand) {
			return cand + " (on PATH)"
		}
	}
	return ""
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// formatInts renders the visible device ids for a refusal, in the shape a person reads
// a list in: "0", "0 and 1", "0, 1 and 2".
func formatInts(ids []int) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.Itoa(id))
	}
	switch len(parts) {
	case 0:
		return "none"
	case 1:
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

// Check is one row of a pre-flight result.
//
// The vocabulary is PASS / FAIL / NOT RUN and nothing else, because that is this
// module's vocabulary everywhere else and a diagnostic with a fourth word in it is a
// diagnostic whose FAIL and whose NOT RUN get confused at two in the morning.
//
// NOT RUN means the row could not be evaluated on this machine — it is an admission,
// not a pass, and it is never written as PASS.
type Check struct {
	Name   string
	State  string
	Detail string
}

// Preflight answers, before any session is opened, whether this machine could honour
// what was configured. It is exported because the alternative is an operator with a
// refusal and nothing to run.
//
// It deliberately does NOT open a session: a pre-flight that loads the runtime is not
// a pre-flight, it is the thing that failed, and by then it has taken the process's
// ORT environment with it. Every row here is a file, a device, or a string, so each one
// is cheap, safe to re-run, and testable on a machine with no GPU — which is the only
// way a check like this gets run at all.
func Preflight(cfg SessionConfig) []Check {
	var out []Check
	add := func(name, state, format string, args ...any) {
		out = append(out, Check{Name: name, State: state, Detail: sprintf(format, args...)})
	}

	// 1. What was asked for.
	ep, err := ValidateEP(cfg.ExecutionProvider)
	if err != nil {
		add("execution provider", StateFail, "%v", err)
	} else {
		add("execution provider", StatePass, "%s, device %d", ep, cfg.CUDADeviceID)
	}

	// 2. The library is where the variable says.
	if cfg.LibraryPath == "" {
		add("runtime library", StateFail, "SP_POC_ORT_LIB is not set; run `pocctl fetch-runtime`")
		return append(out, PreflightProviders(EPProviderCPU, "")...)
	}
	if _, serr := os.Stat(cfg.LibraryPath); serr != nil {
		add("runtime library", StateFail, "%s: %v", cfg.LibraryPath, serr)
		return append(out, PreflightProviders(EPProviderCPU, "")...)
	}
	add("runtime library", StatePass, "%s", cfg.LibraryPath)

	// 3. The model, because a pre-flight that passes and then refuses the model is a
	// pre-flight that answered the wrong question.
	if cfg.ModelPath == "" {
		add("model file", StateFail, "SP_POC_DET_MODEL is not set")
	} else if _, serr := os.Stat(cfg.ModelPath); serr != nil {
		add("model file", StateFail, "%s: %v", cfg.ModelPath, serr)
	} else {
		add("model file", StatePass, "%s", cfg.ModelPath)
	}

	if ep == "" {
		ep = EPProviderCPU
	}
	return append(out, PreflightProviders(ep, cfg.LibraryPath)...)
}

// PreflightProviders is the device half of [Preflight], split out because it is also
// what a caller checks when it has a library path and no configuration to speak of.
func PreflightProviders(ep, libraryPath string) []Check {
	var out []Check
	if ep != EPProviderCUDA {
		out = append(out, Check{Name: "CUDA device", State: StateNotRun,
			Detail: "the CPU execution provider was configured, so there is no GPU to check. NOT RUN is the " +
				"honest state here rather than PASS: nothing about this machine's GPU was examined"})
		return out
	}
	ids, err := visibleCUDADevices()
	switch {
	case err != nil:
		out = append(out, Check{Name: "CUDA device", State: StateFail,
			Detail: sprintf("`%s --query-gpu=index --format=csv,noheader` failed: %v. The NVIDIA driver is "+
				"either not installed or not on PATH", nvidiaSMI, err)})
	case len(ids) == 0:
		out = append(out, Check{Name: "CUDA device", State: StateFail,
			Detail: sprintf("`%s` ran and reported no device. The driver is present and sees nothing", nvidiaSMI)})
	default:
		out = append(out, Check{Name: "CUDA device", State: StatePass,
			Detail: sprintf("device(s) %s visible to this process", formatInts(ids))})
	}
	if libraryPath != "" {
		out = append(out, providerDLLChecks(libraryPath)...)
	}
	return out
}

// The three states, as constants, because a row's state is printed by this package,
// asserted by tests and read by humans, and three string literals in four files is
// three chances to spell one of them differently.
const (
	StatePass   = "PASS"
	StateFail   = "FAIL"
	StateNotRun = "NOT RUN"
)
