package main

// The anchors file, and the one place its bytes come from.
//
// # Why this file exists as code and not as a copy step
//
// `models/` is gitignored â€” it holds 257 MB of weights nobody commits â€” and the
// anchors are the MODEL's own configuration, which is NOT in the ONNX export.
// So a clean clone has the weights' provenance record
// (`pyworker/fixtures/yolov4n.licence.json`) and nothing else: before this
// change, `pocctl fetch-model` left the operator to hand-copy nine numbers into
// `models/yolov4n.anchors.txt` from a cfg file the record names. A number copied
// by hand is a number nobody checked, and the sidecar's own guard is the only
// thing that would catch a typo â€” at load, on a run, in front of a client.
//
// So the committed copy under `pyworker/fixtures/` is the source of truth, this
// command reads it, and `fetch-model`/`bootstrap` write it beside the weights
// and check what they wrote against a pinned digest. Same discipline as the
// weights themselves: the bytes are identified by content, not by a name.
//
// # What the file is, and why it is not a guess
//
// The `[anchors]` section of `yolov4-416.cfg`, the configuration the weights
// were trained with, in the session's output order (finest stride first). The
// shape alone cannot separate v4 from v5 â€” both are anchored at three scales â€”
// so this is transcription of a recorded fact, and the provenance chain is in
// the licence record's `anchors` object beside the weights' own.
//
// # The three-scale check
//
// `pyworker/server.py`'s `load_anchors` refuses a file whose length does not
// divide into three scales. This is the same rule on the Go side, applied
// BEFORE the file is written, because a malformed anchors file is a sidecar
// that decodes wrong box sizes for a whole run and reports a clean scene while
// doing it. Two implementations of one rule is a thing to keep in step, and a
// test does: `TestTheAnchorsChecksKeepInStepWithTheSidecar`.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// anchorsFixturePath is the committed copy's path, relative to the module root.
//
// It is READ rather than embedded, and the reason is a boundary rather than a
// preference: `go:embed` cannot reach outside the package's own directory, and
// the anchors are the model's record, which belongs beside the weights' licence
// record in `pyworker/fixtures/` where a reviewer of this repository will find
// it. Two copies of a provenance file â€” one to read and one to embed â€” is the
// arrangement this repository keeps finding defects in, so there is one copy
// and a test that reads the same path the binary does.
const anchorsFixturePath = "pyworker/fixtures/yolov4n.anchors.txt"

// pocModuleRoot is the POC module's root, found rather than assumed.
//
// `pocctl` commands resolve their module root from the WORKING DIRECTORY (see
// `repoRoot`), and the two callers of this file â€” `fetch-model` and
// `bootstrap` â€” are always run from the module root. The walk up from the
// executable covers the case of a binary started from `bin/` by the demo
// script, and `go test`'s package-directory working directory is covered by the
// same walk. A root that cannot be found is an error with the path in it, not a
// silent "." that would write the anchors file into whatever directory the
// command happened to start in.
func pocModuleRoot() (string, error) {
	starts := []string{}
	if wd, err := os.Getwd(); err == nil {
		starts = append(starts, wd)
	}
	if exe, err := os.Executable(); err == nil {
		starts = append(starts, filepath.Dir(exe))
	}
	for _, start := range starts {
		dir := start
		for {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(anchorsFixturePath))); err == nil {
					return dir, nil
				}
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return "", fmt.Errorf("the POC module root could not be found from %v, so the anchors file (%s) "+
		"could not be located. Run pocctl from poc/active-monitoring", starts, anchorsFixturePath)
}

// anchorsFixtureBytes is the committed copy's content.
func anchorsFixtureBytes() ([]byte, error) {
	root, err := pocModuleRoot()
	if err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(root, filepath.FromSlash(anchorsFixturePath)))
}

// defaultAnchorsSHA256 is what those bytes must hash to, lowercase hex.
//
// A pinned digest for a 2 KB file is not ceremony: the file is the model's own
// configuration, and a digest is what makes "the same nine numbers" a checkable
// claim instead of a resemblance. A test asserts this constant, the committed
// file's bytes and the licence record's `anchors.sha256` all say the same
// thing, because three records of one fact drift the moment one of them is
// edited alone.
const defaultAnchorsSHA256 = "ca1e768b83115385416d39e9481e26e9a365c3b9394390888c71a55c4b38ca2a"

// anchorPair is one `width,height` from the model's .cfg, in input pixels.
type anchorPair struct {
	W float64
	H float64
}

// parseAnchorsFile reads the darknet anchors format into one list per scale.
//
// The rules are `pyworker/server.py`'s `load_anchors`, restated: blank lines and
// `#` comments ignored, one `width,height` per line, and a count that divides
// into three scales. The refusal is the point â€” a guessed or malformed anchor
// set produces boxes in the right place with the wrong sizes, which is the one
// wrong answer nobody can catch by looking at a picture.
func parseAnchorsFile(b []byte) ([][]anchorPair, error) {
	var pairs []anchorPair
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	for i, line := range lines {
		s := strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if s == "" {
			continue
		}
		parts := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
		if len(parts) != 2 {
			return nil, fmt.Errorf("anchors line %d is %q, which is not a `width,height` pair. "+
				"The anchors are the model's own configuration and this sidecar will not substitute a "+
				"default for them", i+1, strings.TrimSpace(line))
		}
		w, err := strconv.ParseFloat(parts[0], 64)
		if err != nil {
			return nil, fmt.Errorf("anchors line %d has a non-numeric width %q", i+1, parts[0])
		}
		h, err := strconv.ParseFloat(parts[1], 64)
		if err != nil {
			return nil, fmt.Errorf("anchors line %d has a non-numeric height %q", i+1, parts[1])
		}
		if w <= 0 || h <= 0 {
			return nil, fmt.Errorf("anchors line %d is %q: an anchor box with no area is not an anchor", i+1, s)
		}
		pairs = append(pairs, anchorPair{W: w, H: h})
	}
	if len(pairs) == 0 {
		return nil, fmt.Errorf("the anchors file contains no anchors")
	}
	per := len(pairs) / 3
	if per == 0 || len(pairs)%3 != 0 {
		return nil, fmt.Errorf("the anchors file has %d anchors, which does not divide into three "+
			"scales. A three-scale darknet head needs a multiple of 3, and a file that does not "+
			"match the model's own tensor is refused rather than interpolated around", len(pairs))
	}
	scales := make([][]anchorPair, 3)
	for i := range scales {
		scales[i] = pairs[i*per : (i+1)*per]
	}
	return scales, nil
}

// verifyAnchors is the pin and the shape check together.
//
// It takes the bytes rather than reading the file, so a test can hand it a
// corrupted copy and watch the refusal â€” which is the only way a digest check
// is a control and not a comment.
func verifyAnchors(b []byte) error {
	sum := sha256.Sum256(b)
	if got := hex.EncodeToString(sum[:]); got != defaultAnchorsSHA256 {
		return fmt.Errorf("the anchors bytes hash to %s and the pinned default is %s. These bytes are "+
			"not the recorded yolov4-416 anchors, and a sidecar decoding with them would draw boxes of "+
			"the wrong sizes", got, defaultAnchorsSHA256)
	}
	if _, err := parseAnchorsFile(b); err != nil {
		return fmt.Errorf("the anchors bytes are the pinned digest but are not readable as anchors: %w", err)
	}
	return nil
}

// writeAnchorsBeside writes the model's anchors as its sibling, digest-checked.
//
// The sibling rule is `anchorsBeside`'s â€” the geometry belongs to the model, so
// the file goes beside the weights under the same base name â€” with an empty
// root so a bare `--out yolov4n.onnx` resolves against the working directory
// the fetch itself is writing into.
//
// It refuses a destination that is not an `.onnx` path: the anchors belong to
// an ONNX export, and writing them beside a name that says nothing about its
// exports would be a guess about a file nobody asked about.
func writeAnchorsBeside(model string) (string, error) {
	if !strings.EqualFold(filepath.Ext(model), ".onnx") {
		return "", fmt.Errorf("the anchors file was not written: %s is not an .onnx path, and the "+
			"anchors are the sibling of an ONNX export", model)
	}
	b, err := anchorsFixtureBytes()
	if err != nil {
		return "", fmt.Errorf("the committed anchors copy could not be read: %w", err)
	}
	if err := verifyAnchors(b); err != nil {
		return "", err
	}
	path := anchorsBeside(model, "")
	if existing, err := os.ReadFile(path); err == nil {
		if sha256hex(existing) == defaultAnchorsSHA256 {
			return path, nil
		}
		// A file is there and it is not the pinned one. It is overwritten and
		// said, because a fetch that quietly kept an operator's hand-edited
		// copy would leave the sidecar decoding with numbers nobody recorded.
		fmt.Printf("fetch-model: %s was present and did NOT match the pinned anchors digest; it is\n"+
			"  rewritten from the recorded copy (sha256 %s).\n", path, defaultAnchorsSHA256[:16])
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return "", fmt.Errorf("the anchors file %s could not be written: %w", path, err)
	}
	return path, nil
}

// anchorsNoteForFetch is what the fetch prints about the anchors file. It is a
// function of the digest that arrived, because the anchors belong to the
// IDENTIFIED weights: an operator who fetched a different export must not get
// yolov4's anchors written beside it.
func anchorsNoteForFetch(out, sum string) string {
	if sum != defaultModelSHA256 {
		return "fetch-model: the anchors file was NOT written. It belongs to the identified yolov4-416 " +
			"export (sha256 " + defaultModelSHA256[:16] + "), and the bytes that arrived are not those " +
			"bytes. If this fetch is a different model, find its anchors in its own .cfg; if it is the " +
			"same one, record what arrived and re-run with --sha256."
	}
	path, err := writeAnchorsBeside(out)
	if err != nil {
		return "fetch-model: the anchors file could not be written: " + err.Error() + ". The sidecar " +
			"cannot decode an anchored export without them, so this is a refusal and not a warning."
	}
	return "fetch-model: wrote the anchors file " + path + " (sha256 " + defaultAnchorsSHA256[:16] + "), " +
		"the [anchors] section of yolov4-416.cfg recorded in pyworker/fixtures/yolov4n.licence.json."
}

// sha256hex is the lowercase hex digest of some bytes.
func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
