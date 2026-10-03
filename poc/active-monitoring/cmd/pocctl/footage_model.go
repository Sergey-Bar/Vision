package main

// footage_model.go - opening the detector for a measurement, and what happens when
// it cannot be opened.
//
// # An absent model is NOT a refusal of the command
//
// `pocctl footage` measures two things at once: the crop ARITHMETIC on real
// pixels, which needs no model at all, and whether the model FINDS anything, which
// needs one. A machine with no weights still measures the first, so this opener
// returns a nil detector and a reason rather than an error, and the rest of the
// command runs. Only the detection columns become `NOT RUN`.
//
// The alternative - refusing the whole command - would throw away a measurement
// that is available over an absence of one that is not, on a repository whose
// entire discipline is refusing to let the second hide the first.
//
// # What is refused, loudly
//
// A MODEL FILE THAT IS NAMED AND NOT READABLE is an error, not an absence. The
// operator pointed at a file; a wrong path silently degrading to "no model" would
// report a measurement of nothing and attribute it to a run they asked for.
//
// A runtime library with no recorded licence is refused, and it is refused HERE
// rather than by `pocctl fetch-runtime`, because this command is a measurement and
// the licence of the thing that produced the number belongs on the number. An
// unlicensed runtime produces numbers nobody may quote, and quoting them is worse
// than having none.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Sergey-Bar/ai-captain-poc/internal/bench"
	"github.com/Sergey-Bar/ai-captain-poc/internal/ortdetect"
)

// resolveFootagePath finds a model file.
//
// The flag wins, then the environment, then the conventional name inside --models.
// A name that resolves to nothing is reported with the three places that were
// tried, because "model not found" without the search path is the single most
// common way an operator loses ten minutes.
func resolveFootagePath(modelsDir, flagValue, envName, conventional string) string {
	if v := strings.TrimSpace(flagValue); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv(envName)); v != "" {
		return v
	}
	return filepath.Join(modelsDir, conventional)
}

// openFootageDetector opens the session and the detector, or explains why not.
//
// The three returns are: the detector (nil when it could not be opened), a note
// for the operator on how to make it work, and the error. The note is separate
// from the error because the error is what a caller returns and the note is what
// a person reads; a measurement tool that folds both into one sentence sends its
// reader to a stack instead of to a flag.
func openFootageDetector(flags map[string]string, opts bench.Options) (*ortdetect.Detector, string, error) {
	modelPath := resolveFootagePath(flags["models"], flags["model"], "SP_POC_DET_MODEL", "yolov4n.onnx")
	libPath := resolveFootagePath(flags["models"], flags["ort"], "SP_POC_ORT_LIB", "onnxruntime.dll")

	if _, err := os.Stat(modelPath); err != nil {
		return nil, fmt.Sprintf("no weights at %s. Tried --model, then $SP_POC_DET_MODEL, then "+
				"%s inside --models %s. `pocctl fetch-model` puts them there.", modelPath,
				"yolov4n.onnx", flags["models"]),
			fmt.Errorf("footage: the model at %s is not readable, so the two-pass comparison cannot run: %w",
				modelPath, err)
	}
	if _, err := os.Stat(libPath); err != nil {
		return nil, fmt.Sprintf("no ONNX Runtime at %s. Tried --ort, then $SP_POC_ORT_LIB, then "+
				"onnxruntime.dll inside --models %s. `pocctl fetch-runtime` puts it there.", libPath, flags["models"]),
			fmt.Errorf("footage: the ONNX Runtime at %s is not readable, so the two-pass comparison cannot run: %w",
				libPath, err)
	}
	if err := checkRuntimeLicence(flags["models"], libPath); err != nil {
		return nil, "the runtime's recorded licence could not be established, so no number produced with it may " +
				"be quoted. `pocctl fetch-runtime --verify` re-checks what is on disk against models/runtime.json.",
			err
	}

	sess, err := ortdetect.Open(ortdetect.SessionConfig{
		LibraryPath:       libPath,
		ModelPath:         modelPath,
		Threads:           envInt("SP_POC_ORT_THREADS", flags["threads"], 4),
		ExecutionProvider: envOr("SP_POC_ORT_EP", flags["ep"]),
		CUDADeviceID:      envInt("SP_POC_ORT_CUDA_DEVICE", flags["cuda-device"], 0),
	})
	if err != nil {
		return nil, "the session would not open. `pocctl detector --selftest` diagnoses the runtime, the " +
				"provider and the model's declared shapes in that order, and it is safe to run: it loads the " +
				"runtime in its LAST row precisely so a failure does not take the process's ORT environment with it.",
			fmt.Errorf("footage: %w", err)
	}
	det, err := ortdetect.NewDetector(ortdetect.Config{
		Session: sess,
		Decode: ortdetect.DecodeConfig{
			EmitScore:  envFloat("SP_POC_DET_SCORE", flags["emit"], 0.05),
			MaxBoxes:   envInt("SP_POC_DET_MAX_BOXES", flags["max-boxes"], 100),
			NMSClass:   envFloat("SP_POC_DET_NMS_CLASS", flags["nms-class"], 0.45),
			NMSGlobal:  envFloat("SP_POC_DET_NMS_GLOBAL", flags["nms-global"], 0.75),
			MinBoxArea: envFloat("SP_POC_MIN_BOX_AREA", flags["crop-min-area"], 0.0005),
		},
		InputSize: sess.Input().W,
	})
	if err != nil {
		_ = sess.Close()
		return nil, "the session opened but the detector refused the configuration. The refusal names which " +
				"value is wrong; it is printed by `pocctl detector --selftest` too.",
			fmt.Errorf("footage: %w", err)
	}
	return det, "", nil
}

// checkRuntimeLicence refuses a runtime whose terms nobody recorded.
//
// It reads models/runtime.json, the same file `pocctl fetch-runtime` verifies
// against, and it does NOT verify the digest: this command is a measurement, and
// re-hashing a 16 MB library on every invocation to produce a licence row would
// make the tool slower for a check that has its own command. The digest check is
// `pocctl fetch-runtime --verify`.
func checkRuntimeLicence(modelsDir, libPath string) error {
	raw, err := os.ReadFile(filepath.Join(modelsDir, "runtime.json"))
	if err != nil {
		return fmt.Errorf("models/runtime.json is not readable, so the ONNX Runtime at %s has no recorded "+
			"licence. A number produced with a runtime whose terms nobody wrote down is a number nobody may "+
			"quote, and quoting it is worse than having none: %w", libPath, err)
	}
	var rows []runtimeRecord
	if err := json.Unmarshal(raw, &rows); err != nil {
		return fmt.Errorf("models/runtime.json is not readable as a list of runtime records: %w", err)
	}
	base := filepath.Base(libPath)
	for _, r := range rows {
		if filepath.Base(r.Path) != base {
			continue
		}
		if strings.TrimSpace(r.Licence) == "" {
			return fmt.Errorf("the row for %s in models/runtime.json records no licence", base)
		}
		return nil
	}
	return fmt.Errorf("models/runtime.json holds no row for %s, so the library about to be loaded has no "+
		"recorded provenance. `pocctl fetch-runtime` writes that row, and it is the only thing that may", base)
}
