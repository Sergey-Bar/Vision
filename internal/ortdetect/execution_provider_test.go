package ortdetect

import (
	"errors"
	"strings"
	"testing"
)

// Execution-provider selection. The check that matters is the one that is NOT here:
// these tests never touch a GPU, because a check for a GPU that only runs on a machine
// with a GPU is a check this repository cannot run.

// TestAnUnknownProviderIsRefusedAndNotTreatedAsTheCPU is the whole reason this file
// exists. ONNX Runtime falls back to the CPU when the CUDA provider cannot be used and
// says so in a log line rather than in an error, so a misspelled provider name that
// became the CPU would produce a correct, slower run labelled with a provider nobody
// asked for.
func TestAnUnknownProviderIsRefusedAndNotTreatedAsTheCPU(t *testing.T) {
	for _, bad := range []string{"gpu0", "nonsense", "cpu,cuda", "cuda0", "opencl", "CUDA EP"} {
		if got := NormalizeEP(bad); got != "" {
			t.Fatalf("NormalizeEP(%q) = %q; an unknown value must resolve to nothing so the caller refuses it", bad, got)
		}
		_, err := ValidateEP(bad)
		if err == nil {
			t.Fatalf("ValidateEP(%q) accepted an unknown provider", bad)
		}
		for _, want := range []string{"SP_POC_ORT_EP", "cpu", "cuda", "falls back to the CPU"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("the refusal for %q does not say %q: %v", bad, want, err)
			}
		}
	}
}

// TestTheEmptyProviderIsTheCPUAndSaysSo: an unset variable means the CPU, and the
// normalisation has to agree with the schema's declared structural value, or `watch
// --config` prints one device and the session opens another.
func TestTheEmptyProviderIsTheCPUAndSaysSo(t *testing.T) {
	for _, in := range []string{"", "   ", "CPU", " cpu ", "cpu_ep", "CPUExecutionProvider"} {
		if got := NormalizeEP(in); got != EPProviderCPU {
			t.Fatalf("NormalizeEP(%q) = %q, want %q", in, got, EPProviderCPU)
		}
	}
	for _, in := range []string{"cuda", "CUDA", " gpu ", "cuda_ep", "CUDAExecutionProvider"} {
		if got := NormalizeEP(in); got != EPProviderCUDA {
			t.Fatalf("NormalizeEP(%q) = %q, want %q", in, got, EPProviderCUDA)
		}
	}
}

// TestACUDADeviceIsConfirmedBeforeTheSessionOpens: ORT's own failure mode is a
// fallback inside session creation, after the append, where nothing in this package
// can see it. So the device is checked FIRST, with the tool whose output the operator
// can run and read themselves.
func TestACUDADeviceIsConfirmedBeforeTheSessionOpens(t *testing.T) {
	list := func(ids ...int) deviceLister {
		return func() ([]int, error) { return ids, nil }
	}
	if _, err := checkCUDA(1, list(0, 1, 2)); err != nil {
		t.Fatalf("a visible device was refused: %v", err)
	}
	check, err := checkCUDA(0, list(0, 1))
	if err != nil {
		t.Fatalf("device 0 was refused: %v", err)
	}
	if len(check.Visible) != 2 {
		t.Fatalf("the check returned %v; a caller needs to know what it saw, not only whether it passed", check.Visible)
	}

	// A device that does not exist names the ones that do, because "the run was
	// refused" is not actionable and "run sees 0 and 1" is.
	_, err = checkCUDA(7, list(0, 1))
	if err == nil {
		t.Fatal("a device id that is not present was accepted")
	}
	for _, want := range []string{"SP_POC_ORT_CUDA_DEVICE=7", "0 and 1", "numbers devices from zero"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not say %q: %v", want, err)
		}
	}
}

// TestNoDeviceAndNoDriverAreTwoDifferentRefusals: three problems with three fixes —
// no nvidia-smi on PATH, the driver installed and seeing nothing, and the device asked
// for not being one of them. One boolean would collapse them into a sentence naming
// none of the three.
func TestNoDeviceAndNoDriverAreTwoDifferentRefusals(t *testing.T) {
	noCommand := func() ([]int, error) { return nil, errors.New("exec: \"nvidia-smi\": executable file not found") }
	_, err := checkCUDA(0, noCommand)
	if err == nil {
		t.Fatal("a machine with no nvidia-smi was accepted for a CUDA run")
	}
	if !strings.Contains(err.Error(), "executable file not found") {
		t.Fatalf("the refusal does not carry the underlying error: %v", err)
	}
	if !strings.Contains(err.Error(), "driver is not installed") {
		t.Fatalf("the refusal does not say what to check: %v", err)
	}

	nothing := func() ([]int, error) { return nil, nil }
	_, err = checkCUDA(0, nothing)
	if err == nil {
		t.Fatal("a driver that reports no device was accepted for a CUDA run")
	}
	if !strings.Contains(err.Error(), "reported no CUDA device") {
		t.Fatalf("the refusal does not distinguish an empty device list from a missing command: %v", err)
	}
	if strings.Contains(err.Error(), "executable file not found") {
		t.Fatalf("the refusal blames the command when the command ran fine: %v", err)
	}
}

// TestEveryRefusalOffersTheCPU: an account without a GPU has to be able to say so and
// carry on, and the sentence that offers it is the sentence an operator reads at two
// in the morning. It is in every refusal because every refusal has it as the answer.
func TestEveryRefusalOffersTheCPU(t *testing.T) {
	refusals := []error{
		func() error { _, e := ValidateEP("nonsense"); return e }(),
		func() error {
			_, e := checkCUDA(0, func() ([]int, error) { return nil, errors.New("no driver") })
			return e
		}(),
		func() error { _, e := checkCUDA(0, func() ([]int, error) { return nil, nil }); return e }(),
		func() error { _, e := checkCUDA(9, func() ([]int, error) { return []int{0}, nil }); return e }(),
	}
	for i, err := range refusals {
		if err == nil {
			t.Fatalf("refusal %d is nil, so there was nothing to offer a way out of", i)
		}
		if !strings.Contains(err.Error(), "SP_POC_ORT_EP=cpu") {
			t.Fatalf("refusal %d does not offer the CPU: %v", i, err)
		}
	}
}

// TestTheVisibleDeviceListReadsAsAList: the refusal prints indices, and "0,1,2" in a
// sentence is a puzzle while "0, 1 and 2" is not.
func TestTheVisibleDeviceListReadsAsAList(t *testing.T) {
	for _, tc := range []struct {
		in   []int
		want string
	}{
		{[]int{0}, "0"},
		{[]int{0, 1}, "0 and 1"},
		{[]int{0, 1, 2}, "0, 1 and 2"},
		{nil, "none"},
	} {
		if got := formatInts(tc.in); got != tc.want {
			t.Fatalf("formatInts(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
