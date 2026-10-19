package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// modelSpec is a detector model this emulator knows how to fetch.
//
// The digest is IN THE SOURCE, and that is the whole point: a model is the one
// thing whose output the platform records as fact, and a file that arrived from
// a URL nobody pinned is a file whose numbers describe nothing in particular.
//
// The licence is recorded because it is a fact about the answer. Ultralytics'
// weights are AGPL-3.0, which is a real constraint on what a product built on
// them may do, and a demo that used them without saying so would be making that
// decision invisibly.
type modelSpec struct {
	// Name is the file name it is written under.
	Name string
	// URL is where it comes from, with the release in it so a URL that drifts
	// is visible in the source.
	URL string
	// SHA256 is the digest the bytes must have.
	SHA256 string
	// Bytes is the expected size, checked as well as the digest.
	Bytes int64
	// Licence is the licence the weights are under.
	Licence string
	// Note says what the export is, in one line.
	Note string
}

// modelSpecs are the models this emulator fetches.
//
// The fused-head export is the one that matters, and it matters because of a
// specific refusal: the Go decoder reads FUSED outputs — one output carrying
// every box — and the anchored export that was committed before refuses to be
// decoded at all. A detector that cannot read the model is a detector that
// reports nothing.
var modelSpecs = []modelSpec{
	{
		Name:    "yolo11n.onnx",
		URL:     "https://github.com/ultralytics/assets/releases/download/v8.3.0/yolo11n.onnx",
		SHA256:  "634279b40c07c6391472c51ad45b81ebc48706a9a1fe72dd3396322acd0c053b",
		Bytes:   10930182,
		Licence: "AGPL-3.0",
		Note: "YOLO11 nano, fused head, COCO-80. The head is fused: one output carries " +
			"every box, which is the shape the Go decoder reads.",
	},
}

// modelManifest is what the fetch writes beside the model.
//
// It is written because a model without a recorded provenance is a file, and a
// file is not an answer anybody can check.
type modelManifest struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	Bytes     int64  `json:"bytes"`
	Licence   string `json:"licence"`
	Source    string `json:"source"`
	Note      string `json:"note"`
	FetchedAt string `json:"fetched_at"`
}

// runFetchModel fetches a digest-pinned model.
func runFetchModel(args []string) error {
	fs := flag.NewFlagSet("vms-emulator fetch-model", flag.ContinueOnError)
	name := fs.String("name", "yolo11n.onnx", "which model to fetch")
	out := fs.String("out", "models", "the directory to write it into")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var spec modelSpec
	found := false
	for _, candidate := range modelSpecs {
		if candidate.Name == *name {
			spec = candidate
			found = true
			break
		}
	}
	if !found {
		known := make([]string, 0, len(modelSpecs))
		for _, candidate := range modelSpecs {
			known = append(known, candidate.Name)
		}
		return fmt.Errorf("no model is called %q. This build knows: %s", *name, strings.Join(known, ", "))
	}
	if err := os.MkdirAll(*out, 0o750); err != nil {
		return fmt.Errorf("make %s: %w", *out, err)
	}
	target := filepath.Join(*out, spec.Name)
	if existing, err := os.Stat(target); err == nil && existing.Size() == spec.Bytes {
		digest, _, err := digestFile(target)
		if err == nil && digest == spec.SHA256 {
			fmt.Printf("%s is already here and matches the pinned digest.\n", target)
			return nil
		}
	}
	fmt.Printf("fetching %s\n  from %s\n  sha256 %s\n", spec.Name, spec.URL, spec.SHA256)
	if err := download(spec.URL, target); err != nil {
		return err
	}
	digest, size, err := digestFile(target)
	if err != nil {
		return err
	}
	if digest != spec.SHA256 {
		_ = os.Remove(target)
		return fmt.Errorf("the file that arrived has digest %s and the pinned one is %s. It was deleted: "+
			"a model whose bytes are not the ones that were measured is a model whose numbers describe "+
			"nothing in particular", digest, spec.SHA256)
	}
	if spec.Bytes > 0 && size != spec.Bytes {
		_ = os.Remove(target)
		return fmt.Errorf("the file that arrived is %d bytes and the pinned one is %d. It was deleted", size, spec.Bytes)
	}
	manifest := modelManifest{
		Name: spec.Name, Path: target, SHA256: digest, Bytes: size,
		Licence: spec.Licence, Source: spec.URL, Note: spec.Note,
		FetchedAt: time.Now().UTC().Format(time.RFC3339),
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*out, "model.json"), raw, 0o600); err != nil {
		return err
	}
	fmt.Printf("wrote %s (%d bytes)\n", target, size)
	fmt.Printf("licence: %s\n", spec.Licence)
	return nil
}

// runFetchRuntime fetches the ONNX Runtime the detector needs.
//
// The runtime is a release ASSET — a zip of the shared library and the CUDA
// provider — and the command takes the URL rather than carrying one, because
// the runtime's versions move faster than this source does and a URL baked in
// here would go stale into a download that fails with a 404 nobody can read.
//
// The expected digest is optional and, when given, CHECKED. A first run with
// no expected digest records what arrived and says plainly that nobody has
// verified it yet; a second run with one fails loudly if it does not match.
func runFetchRuntime(args []string) error {
	fs := flag.NewFlagSet("vms-emulator fetch-runtime", flag.ContinueOnError)
	url := fs.String("url", envOr("SP_ORT_URL", ""), "the release asset to fetch")
	want := fs.String("sha256", envOr("SP_ORT_SHA256", ""), "the digest the archive must have")
	out := fs.String("out", "models", "the directory to unpack it into")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*url) == "" {
		return errors.New("no URL. Pass --url or set SP_ORT_URL. The runtime is a release asset and this " +
			"source does not carry one: take the URL from https://github.com/microsoft/onnxruntime/releases " +
			"and pick the asset for your platform")
	}
	if err := os.MkdirAll(*out, 0o750); err != nil {
		return err
	}
	archive := filepath.Join(*out, "onnxruntime.zip")
	fmt.Printf("fetching the ONNX Runtime\n  from %s\n", *url)
	if err := download(*url, archive); err != nil {
		return err
	}
	digest, size, err := digestFile(archive)
	if err != nil {
		return err
	}
	if strings.TrimSpace(*want) != "" && digest != strings.ToLower(strings.TrimSpace(*want)) {
		_ = os.Remove(archive)
		return fmt.Errorf("the archive has digest %s and %s was expected. It was deleted", digest, *want)
	}
	if strings.TrimSpace(*want) == "" {
		fmt.Printf("the archive's digest is %s and NOBODY HAS VERIFIED IT: pass --sha256 with the digest "+
			"the release page publishes to have it checked\n", digest)
	}
	if err := unzip(archive, *out); err != nil {
		return err
	}
	_ = os.Remove(archive)
	fmt.Printf("unpacked into %s (%d bytes of archive)\n", *out, size)
	return nil
}

// download writes a URL to a path.
func download(url, target string) error {
	client := &http.Client{Timeout: 20 * time.Minute}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build the request for %s: %w", url, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch %s: the server answered %s", url, resp.Status)
	}
	f, err := os.Create(target)
	if err != nil {
		return fmt.Errorf("create %s: %w", target, err)
	}
	defer func() { _ = f.Close() }()
	written, err := io.Copy(f, resp.Body)
	if err != nil {
		return fmt.Errorf("write %s: %w", target, err)
	}
	if written == 0 {
		return fmt.Errorf("fetch %s: nothing arrived", url)
	}
	return nil
}

// digestFile returns a file's digest and size.
func digestFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return "", 0, fmt.Errorf("read %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), size, nil
}

// unzip extracts an archive into a directory.
//
// It refuses an entry whose path escapes the directory: the archive comes off
// the network, and a zip entry named `../../something` is a way to write
// outside the place the operator asked for.
func unzip(archive, dir string) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return fmt.Errorf("open %s: %w", archive, err)
	}
	defer func() { _ = r.Close() }()
	base, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	for _, entry := range r.File {
		target := filepath.Join(base, filepath.FromSlash(entry.Name))
		if !strings.HasPrefix(target, base+string(os.PathSeparator)) && target != base {
			return fmt.Errorf("the archive contains %q, which would be written outside %s", entry.Name, dir)
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o750); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		src, err := entry.Open()
		if err != nil {
			return fmt.Errorf("open %s in the archive: %w", entry.Name, err)
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			_ = src.Close()
			return fmt.Errorf("create %s: %w", target, err)
		}
		_, copyErr := io.Copy(out, src)
		_ = src.Close()
		_ = out.Close()
		if copyErr != nil {
			return fmt.Errorf("write %s: %w", target, copyErr)
		}
	}
	return nil
}
