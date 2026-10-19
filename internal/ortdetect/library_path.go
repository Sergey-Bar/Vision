package ortdetect

import (
	"os"
	"path/filepath"
	"strings"
)

// PrependLibraryDirToPATH puts the runtime library's own directory at the front
// of PATH, and says so.
//
// It exists because of a failure this module measured, and the failure is worth
// writing down in full because every symptom pointed somewhere else.
//
// `pocctl fetch-runtime` places the ONNX Runtime GPU bundle beside the model
// it governs: `models/onnxruntime.dll`, `models/onnxruntime_providers_cuda.dll`,
// and the CUDA and cuDNN libraries those two load at inference time. The library
// itself is named by PATH variable — SP_POC_ORT_LIB — and Go loads it directly
// from that path. The DEPENDENCIES are not: on Windows, `cudnn64_9.dll` is
// resolved through the process DLL search order, which covers the executable's
// directory, the system directories, and PATH. The executable's directory is
// `bin/` (or the temp directory a `go test` binary lives in), not `models/`
// where the fetch actually put the file.
//
// The result is a run that appends the CUDA provider successfully — the
// nvidia-smi check passes, the append returns nil, the session opens — and then
// fails its first Conv node with
//
//	cuDNN is unavailable or disabled for CUDA Execution Provider:
//	LoadLibrary failed for cudnn64_9.dll with error 2
//
// which is exactly the defect the CUDA path's own doc comment says it refuses to
// tolerate: real numbers from the wrong machine, every recorded field saying
// `cuda`, and nothing in the output saying otherwise. Measured on this machine
// on 2026-10-15: 7.9ms per inference with the kernel failing, 600.9ms with
// PATH fixed and the GPU genuinely engaged.
//
// So the directory goes on PATH before the environment is initialised. It is a
// PREPEND rather than an append so the fetched bundle wins over anything else on
// the machine: two ORT versions can be installed at once and they are not
// ABI-compatible with each other's bindings, which is the same reason
// SP_POC_ORT_LIB is a path rather than a name.
func PrependLibraryDirToPATH(libPath string) error {
	if strings.TrimSpace(libPath) == "" {
		return &Refusal{
			What: "no ONNX Runtime library was named",
			Detail: "set SP_POC_ORT_LIB, or run `pocctl fetch-runtime`. Without a library path there " +
				"is no directory to place on PATH for the runtime's own dependencies, and the CUDA " +
				"provider loads cudnn64_9.dll through PATH rather than beside the library",
		}
	}
	dir, err := filepath.Abs(filepath.Dir(libPath))
	if err != nil {
		return &Refusal{
			What:   sprintf("the ONNX Runtime library's directory could not be resolved from %s", libPath),
			Detail: err.Error(),
		}
	}
	// Already there is a pass, not a skip-by-luck: the directory may have been
	// added by an earlier Open in the same process, and prepending twice would
	// grow PATH once per session. The test that covers this opens twice.
	sep := string(os.PathListSeparator)
	old := os.Getenv("PATH")
	for _, entry := range filepath.SplitList(old) {
		if strings.EqualFold(entry, dir) {
			return nil
		}
	}
	next := dir + sep + old
	if err := os.Setenv("PATH", next); err != nil {
		return &Refusal{
			What: sprintf("the ONNX Runtime library's directory (%s) could not be placed on PATH", dir),
			Detail: err.Error() + ". Without it the CUDA execution provider cannot load cudnn64_9.dll, " +
				"which it resolves through PATH and not beside the library. Run the binary with the " +
				"directory already on PATH, or set it in the shell before starting",
		}
	}
	return nil
}
