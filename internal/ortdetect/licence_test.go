package ortdetect

import (
	"os"
	"path/filepath"
	"testing"
)

// The licence travels with the model.
//
// A model used without recording its terms is a model whose terms nobody read,
// and the manifest the fetch command writes beside the model is where they are
// recorded. The test writes a manifest and reads it back, because the
// alternative -- reading a file that only exists after a fetch -- would make
// the test depend on the network.
func TestTheLicenceComesFromTheManifestBesideTheModel(t *testing.T) {
	dir := t.TempDir()
	model := filepath.Join(dir, "model.onnx")
	if err := os.WriteFile(model, []byte("not really a model"), 0o600); err != nil {
		t.Fatalf("write the model: %v", err)
	}
	manifest := `{"name":"model.onnx","sha256":"abc","licence":"AGPL-3.0","bytes":17}`
	if err := os.WriteFile(filepath.Join(dir, "model.json"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("write the manifest: %v", err)
	}
	if got := readLicence(model); got != "AGPL-3.0" {
		t.Errorf("the licence read as %q, want AGPL-3.0", got)
	}
}

// A model with no manifest has no licence, and that is reported rather than
// defaulted.
//
// "Unlicensed" is a claim about the weights, and a claim the detector cannot
// support would be worse than an empty string: an empty string says nobody
// said.
func TestAModelWithNoManifestHasNoLicence(t *testing.T) {
	dir := t.TempDir()
	model := filepath.Join(dir, "model.onnx")
	if err := os.WriteFile(model, []byte("not really a model"), 0o600); err != nil {
		t.Fatalf("write the model: %v", err)
	}
	if got := readLicence(model); got != "" {
		t.Errorf("a model with no manifest reported the licence %q, want an empty string", got)
	}
}

// A manifest that cannot be parsed is no licence, and it does not fail the
// run.
//
// The licence is a fact about the answer, not a condition of the answer: a
// model that runs and whose terms were not recorded is a gap in the record,
// and refusing to run would turn a gap into an outage.
func TestAnUnreadableManifestIsNoLicence(t *testing.T) {
	dir := t.TempDir()
	model := filepath.Join(dir, "model.onnx")
	if err := os.WriteFile(model, []byte("not really a model"), 0o600); err != nil {
		t.Fatalf("write the model: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write the manifest: %v", err)
	}
	if got := readLicence(model); got != "" {
		t.Errorf("an unreadable manifest reported the licence %q, want an empty string", got)
	}
}
