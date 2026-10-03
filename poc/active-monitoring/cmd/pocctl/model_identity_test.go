package main

// The model's own identity, asserted from the repository rather than from a name.
//
// Every other place in this repository that says what the detector is, says it from
// the licence record. This is the test that makes that record load-bearing: if the
// recorded architecture and the measured tensor shape ever disagree, this fails
// rather than a judge finding out on stage.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type licenceRecord struct {
	File         string `json:"file"`
	Bytes        int64  `json:"bytes"`
	SHA256       string `json:"sha256"`
	ShapeRecord  string `json:"shape_record"`
	FilenameSays string `json:"filename_says"`
	Licence      string `json:"licence"`
	Decisions    struct {
		Rename         string `json:"rename"`
		RenameProvedBy string `json:"rename_proved_by"`
	} `json:"decisions"`
	// The 85-field objectness claim, as prose. It is parsed for the number rather
	// than trusted as a sentence, because a sentence can be reworded and a number
	// cannot. BOTH arrays are read: one records the architecture and the other
	// records why the old name was wrong, and the number belongs in both.
	HowTheArchitectureWasEstablished []string `json:"how_the_architecture_was_established"`
	WhyTheOldFilenameWasWrong        []string `json:"why_the_old_filename_was_wrong"`
}

type contractRecord struct {
	Model         string `json:"model"`
	NamesVerbatim string `json:"names_verbatim"`
	DecoderFamily string `json:"decoder_family"`
}

func repoFile(t *testing.T, rel string) string {
	t.Helper()
	p := filepath.Join("..", "..", rel)
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("%s is referenced by the licence record and does not exist: %v", rel, err)
	}
	return p
}

// TestTheLicenceRecordNamesTheModelTheMeasurementDescribes is the control.
//
// The filename asserts an architecture. The measured tensor shape decides whether
// that assertion is true. Nothing else in the repository ties the two together, so
// this is the one place that does, and it is why the licence record is a record and
// not a note.
func TestTheLicenceRecordNamesTheModelTheMeasurementDescribes(t *testing.T) {
	raw, err := os.ReadFile(repoFile(t, "pyworker/fixtures/yolov4n.licence.json"))
	if err != nil {
		t.Fatal(err)
	}
	var lic licenceRecord
	if err := json.Unmarshal(raw, &lic); err != nil {
		t.Fatal(err)
	}

	// 1. The recorded file and the recorded shape record are BOTH about the same file.
	// A licence record pointing at a contract for a model it does not describe is a
	// record of nothing.
	if !strings.HasSuffix(lic.File, ".onnx") {
		t.Fatalf("licence.file = %q, want the weights path", lic.File)
	}
	if base := filepath.Base(lic.File); base != lic.FilenameSays+".onnx" {
		t.Fatalf("licence.file is %q but filename_says is %q. The name the weights file has and the "+
			"architecture the record claims are different claims, and a reader has to be able to see "+
			"they agree", base, lic.FilenameSays)
	}

	raw, err = os.ReadFile(repoFile(t, lic.ShapeRecord))
	if err != nil {
		t.Fatal(err)
	}
	var con contractRecord
	if err := json.Unmarshal(raw, &con); err != nil {
		t.Fatal(err)
	}
	if con.Model != filepath.Base(lic.File) {
		t.Fatalf("the measured contract says the model is %q and the licence record says %q. The "+
			"contract is a MEASUREMENT and the licence record is a claim; when they disagree the "+
			"measurement wins and the claim is wrong", con.Model, filepath.Base(lic.File))
	}
	if con.DecoderFamily != "anchored" {
		t.Fatalf("decoder_family = %q, want anchored. The rename rests on the export being anchored "+
			"at three scales, which is what separates a v4-lineage head from a fused v8 one. If this "+
			"changes, the architecture claim has to be re-derived rather than carried over", con.DecoderFamily)
	}
	if con.NamesVerbatim != "" {
		t.Logf("the export now carries class metadata (%q). That does not change the architecture, "+
			"but the COCO-80-by-index fallback prose in the licence record is now stale", con.NamesVerbatim)
	}

	// 2. The number the whole rename turns on: 85 fields means an objectness column at
	// index 4, and a fused YOLOv8 export has 84. It is read out of the record's own
	// prose so the number cannot be quietly reworded away.
	var fields int
	for _, line := range append(append([]string{}, lic.HowTheArchitectureWasEstablished...),
		lic.WhyTheOldFilenameWasWrong...) {
		if n, ok := leadingInt(line); ok {
			fields = n
			break
		}
	}
	if fields == 0 {
		t.Fatal("the licence record no longer states a field count, so the claim that the export is " +
			"not a fused v8 head has lost its arithmetic. A filename nobody can check is a filename " +
			"nobody can trust")
	}
	if fields != 85 {
		t.Fatalf("the record states %d fields per detection. 85 is what an objectness at column 4 "+
			"implies and is the whole basis for the rename; any other number has to be re-derived "+
			"before the name is trusted", fields)
	}
	if fields == 84 {
		t.Fatal("84 fields means a FUSED export with no objectness column, which is the v8 shape. " +
			"The name would then be right and the record would have to say so")
	}

	// 3. The rename is recorded as a decision WITH its reason, and the licence is
	// separately still unrecorded. The failure this protects against is the demo
	// saying "YOLOv4" and letting a listener assume that settles the licensing.
	if !strings.HasPrefix(strings.TrimSpace(lic.Decisions.Rename), "YES") {
		t.Fatalf("the rename decision is %q. The file was renamed on 2026-10-03 and the record has "+
			"to say so, with the reasoning, because a rename nobody recorded is indistinguishable "+
			"from a file that was always called that", lic.Decisions.Rename)
	}
	if lic.Decisions.RenameProvedBy == "" {
		t.Error("the rename decision records no provenance for the name. `yolov4` has to be derived " +
			"from the yolov4-416.cfg anchors, not guessed from a tensor shape that v5 also has")
	}
	if !strings.Contains(lic.Licence, "NOT RECORDED") {
		t.Fatalf("licence = %q. The rename fixed the FILENAME and this is a different fact about a "+
			"different subject. `NOT RECORDED` is still what is true, and anything said on stage "+
			"about this model has to say both halves", lic.Licence)
	}
}

// TestNothingInTheCommittedTreeStillNamesTheRenamedModel is the rename's completeness
// check.
//
// A rename that leaves one path pointing at the old name is a build that works on one
// machine and fails on another, and the machine it works on is the one the rename was
// run on.
func TestNothingInTheCommittedTreeStillNamesTheRenamedModel(t *testing.T) {
	root := filepath.Join("..", "..")
	var stale []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			base := filepath.Base(p)
			// models/ is gitignored and holds 257 MB of weights, and the two
			// licence fixtures this file names are the ones being searched for, so
			// they are skipped as data rather than as source.
			if base == ".git" || base == "models" || base == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".go", ".py", ".ps1", ".md", ".json", ".txt", ".cfg":
		default:
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		// The licence record and the anchors header both NAME the old file on
		// purpose, to explain why it was renamed. A grep that flags those is a grep
		// that makes the explanation impossible to write down.
		if strings.HasSuffix(rel, "yolov4n.licence.json") || strings.HasSuffix(rel, "yolov4n.anchors.txt") {
			return nil
		}
		// This file searches for the old name, so it cannot also contain it: the
		// needle is assembled rather than written, which is the only way a
		// completeness grep can live in the tree it greps.
		needle := "yolo" + "v8n"
		for _, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, needle) {
				stale = append(stale, rel+": "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) > 0 {
		t.Fatalf("%d place(s) still name the renamed model, so a path resolves on the machine the "+
			"rename was run on and nowhere else:\n  %s", len(stale), strings.Join(stale, "\n  "))
	}
}

// leadingInt returns the first integer in a sentence, if there is one.
func leadingInt(s string) (int, bool) {
	i := strings.IndexFunc(s, func(r rune) bool { return r >= '0' && r <= '9' })
	if i < 0 {
		return 0, false
	}
	j := i
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	n := 0
	for _, c := range s[i:j] {
		n = n*10 + int(c-'0')
	}
	return n, true
}
