//go:build cgo

// The half of execution-provider selection that touches ONNX Runtime.
//
// The vocabulary, the normalisation, the refusals and the pre-flight device check are
// pure Go and live in execution_provider.go, which has no build tag — so a machine
// with no C toolchain still gets the whole vocabulary, and the refusal for a
// misspelled SP_POC_ORT_EP is the same sentence in both builds. Only this file needs
// cgo, because only this file calls the runtime.

package ortdetect

import (
	"strconv"

	ort "github.com/yalue/onnxruntime_go"
)

// applyExecutionProvider appends the configured provider to the session options.
//
// The CPU needs no append: it is the runtime's built-in default, and appending it
// would wrap a no-op in a failure mode. `cuda` is the only one that does anything
// here, and it either appends or the run does not start.
//
// The order inside is load-bearing and is the reason this is not three lines: normalise,
// refuse an unknown value, refuse a negative device, CONFIRM THE DEVICE, append. The
// confirmation is before the append because ORT's own failure — falling back to the CPU
// inside session creation, logging it and returning success — happens after the append,
// where nothing in this package can see it.

func applyExecutionProvider(opts *ort.SessionOptions, want string, deviceID int) error {
	ep, err := ValidateEP(want)
	if err != nil {
		return err
	}
	if ep == EPProviderCPU {
		return nil
	}
	if deviceID < 0 {
		return &Refusal{
			What: sprintf("SP_POC_ORT_CUDA_DEVICE=%d is not a CUDA device id", deviceID),
			Detail: "CUDA numbers devices from zero, so a negative id names no device. Set it to one of the " +
				"indices `nvidia-smi --query-gpu=index --format=csv` prints",
		}
	}
	if _, err := checkCUDA(deviceID, nil); err != nil {
		return err
	}
	cuda, err := ort.NewCUDAProviderOptions()
	if err != nil {
		return cudaRefusal(deviceID, err)
	}
	defer func() { _ = cuda.Destroy() }()
	// The device id goes through the options map rather than a setter: this binding
	// has no `SetDeviceID`, and ORT's CUDA provider options are a key/value bag.
	if err := cuda.Update(map[string]string{"device_id": strconv.Itoa(deviceID)}); err != nil {
		return cudaRefusal(deviceID, err)
	}
	if err := opts.AppendExecutionProviderCUDA(cuda); err != nil {
		return cudaRefusal(deviceID, err)
	}
	return nil
}

// cudaRefusal is one sentence for every way the CUDA provider can fail, because they
// have the same owner and the same fix, and an operator who reads four different
// errors for one problem reads none of them.
func cudaRefusal(deviceID int, err error) error {
	return &Refusal{
		What: sprintf("SP_POC_ORT_EP=cuda was configured for device %d and the CUDA execution provider could not be used", deviceID),
		Detail: sprintf("%v. This is refused rather than run on the CPU on purpose: the run would produce real "+
			"numbers from the wrong machine, every recorded field would say `cuda`, and nothing in the output "+
			"would say otherwise. The usual causes, in the order they are worth checking: the ONNX Runtime at "+
			"SP_POC_ORT_LIB was built WITHOUT the CUDA provider (the CPU-only build is the common one); the "+
			"CUDA and cuDNN versions do not match what that runtime was built against, and a mismatch fails "+
			"here rather than at inference; or the device is busy. Run `pocctl detector --selftest` to see which, "+
			"and set SP_POC_ORT_EP=cpu to accept a slower run on the CPU deliberately", err),
	}
}
